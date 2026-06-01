package main

// Linux 构建命令：
// $env:GOOS="linux"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"; go build -ldflags="-s -w" .
// Windows 构建命令：
// $env:GOOS="windows"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"; go build -ldflags="-s -w" .

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"main/exchange"

	"github.com/adshao/go-binance/v2/futures"
	"github.com/shopspring/decimal"
)

var (
	Client       *futures.Client
	ExchangeInfo *exchange.ExchangeInfo
	Uh           *UserHandler
	MarginRate   *MarginRateResult
	TCPositions  = make(map[string]TCPosition)
)

type TCPosition struct {
	USDC struct {
		Profit   decimal.Decimal
		Quantity decimal.Decimal
		Price    decimal.Decimal
		USD      decimal.Decimal
	}
	USDT struct {
		Profit   decimal.Decimal
		Quantity decimal.Decimal
		Price    decimal.Decimal
		USD      decimal.Decimal
	}
}

func init() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	if err := initEnv(); err != nil {
		log.Fatal(err)
	}

	Client = &futures.Client{}
	if Env.ProxyURL != "" {
		Client = futures.NewProxiedClient(Env.APIKey, Env.SecretKey, Env.ProxyURL)
	} else {
		Client = futures.NewClient(Env.APIKey, Env.SecretKey)
	}

	if err := Client.NewPingService().Do(context.Background()); err != nil {
		log.Fatal(err)
	}

	var err error
	ExchangeInfo, err = exchange.FetchExchangeInfo()
	if err != nil {
		log.Fatalf("获取交易所信息失败了: %v", err)
	}

	log.Println("正在启动用户数据处理器…")
	Uh = NewFuturesUserHandler(Client)
	uhComplete, err := Uh.Start()
	if err != nil {
		log.Fatalf("启动用户数据处理器失败: %v", err)
	}
	log.Println("等待用户数据流预热完成…")
	<-uhComplete
	log.Println("用户数据处理器已就绪")
}

func main() {
	defer Uh.Close()

	go func() {
		for {
			log.Println("===== 新一轮处理开始 =====")
			GetTCPositions()

			if MarginRate == nil {
				log.Println("保证金率暂时不可用，休息 10 分钟再试")
				time.Sleep(Env.MainLoopIntervalDuration)
				continue
			}

			log.Printf("当前保证金率 %s%%", formatDecimalFixed(MarginRate.MarginRatio, 4))

			if !MarginRate.MarginRatio.IsZero() {
				log.Println("检查双边仓位是否平衡…")
				if BalancePositions() {
					log.Println("再平衡订单已提交，本轮其他操作跳过")
					continue
				}
			}

			if MarginRate.MarginRatio.GreaterThan(Env.MarginRatioReduceTrigger) {
				MarginRatioBeyond()
			} else if MarginRate.MarginRatio.LessThan(Env.MarginRatioAddTrigger) {
				MarginRatioSmall()
			} else {
				log.Println("保证金率在安全范围，无需操作")
			}

			log.Printf("本轮处理完成，休息 %s", Env.MainLoopIntervalDuration.String())
			time.Sleep(Env.MainLoopIntervalDuration)

			if err := RefreshEnv(); err != nil {
				log.Printf("刷新配置失败: %v", err)
			}
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	Uh.Close()
}

func GetTCPositions() {
	result, err := CalcMarginRatio(context.Background(), Client, "")
	if err != nil {
		log.Printf("计算保证金率失败: %v", err)
		return
	}

	MarginRate = result
	FormatSymbol(result.Positions)
}

// HasSufficientAvailableBalance 计算多币种账户折算后的总可用资金，低于阈值时停止继续下单。
func HasSufficientAvailableBalance(scene string) bool {
	if MarginRate == nil {
		log.Printf("保证金率结果为空，跳过 %s 下单", scene)
		return false
	}

	if MarginRate.TotalAvailable.LessThan(Env.MinAvailableUSD) {
		log.Printf("当前总可用资金 %s USD 已低于阈值 %s USD，%s 暂停下单",
			formatDecimalFixed(MarginRate.TotalAvailable, 2),
			formatDecimalFixed(Env.MinAvailableUSD, 2),
			scene,
		)
		return false
	}

	return true
}

func MarginRatioBeyond() {
	// 保证金率过高时，优先减掉盈利最大的组合，释放保证金占用。
	log.Println("保证金率偏高，开始减仓处理")
	for {
		symbol := GetMaxProfitSymbol()
		if symbol == "" {
			log.Println("没有找到可减仓的盈利币种")
			return
		}

		log.Printf("减仓中… 当前保证金率 %s%%", formatDecimalFixed(MarginRate.MarginRatio, 4))
		if MarginRate.MarginRatio.LessThan(Env.MarginRatioReduceTarget) {
			log.Println("保证金率已降到安全水位，减仓结束")
			break
		}

		if Env.GetSymbol(symbol) == nil {
			log.Printf("没有 %s 的配置信息，跳过", symbol)
			continue
		}

		extra := decimalMax(decimal.Zero, MarginRate.MarginRatio.Sub(Env.MarginRatioReduceTarget))
		usdt := Env.ReduceBaseUsdt.Add(extra.Mul(Env.ReduceStepUsdtPerRatioPoint)).Round(0)
		tc := TCPositions[symbol]
		maxCloseValue := tc.USDC.Quantity.Mul(tc.USDC.Price)

		q := decimal.Zero
		if usdt.GreaterThan(maxCloseValue) {
			q = tc.USDC.Quantity
		}

		if !HasSufficientAvailableBalance("高保证金率处理") {
			return
		}

		CreateTC(symbol, usdt, q)
		time.Sleep(Env.ReduceWaitIntervalDuration)
		GetTCPositions()
	}
}

func MarginRatioSmall() {
	// 保证金率偏低时，优先给当前总持仓价值最小的币种补仓。
	log.Println("保证金率偏低，开始补仓处理")
	for i := 0; i < Env.MaxAddRounds; i++ {
		symbol := GetMinValueSymbol()
		if symbol == "" {
			log.Println("没有找到适合补仓的币种")
			return
		}

		log.Printf("补仓 当前保证金率 %s%%", formatDecimalFixed(MarginRate.MarginRatio, 4))
		if MarginRate.MarginRatio.GreaterThan(Env.MarginRatioAddTarget) {
			if MarginRate.MarginRatio.GreaterThan(Env.MarginRatioReduceTrigger) {
				MarginRatioBeyond()
			}
			break
		}

		symbolConfig := Env.GetSymbol(symbol)
		if symbolConfig == nil {
			log.Printf("没有 %s 的配置信息，跳过", symbol)
			return
		}

		if !HasSufficientAvailableBalance("低保证金率处理") {
			return
		}

		tc := NewTC(symbol, symbolConfig.Usdt)
		Uh.HandleFilled(tc.ID, &tc.Handle)

		doneC, errCh := tc.Start()
		select {
		case <-doneC:
			time.Sleep(Env.TCWaitIntervalDuration)
			GetTCPositions()
		case err := <-errCh:
			log.Printf("组合单执行失败: %v", err)
			Uh.HandleFilledDelete(tc.ID)
			time.Sleep(Env.TCWaitIntervalDuration)
			GetTCPositions()
		}

		time.Sleep(Env.LoopStepIntervalDuration)
	}
}
