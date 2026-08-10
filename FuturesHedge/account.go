package main

import (
	"context"
	"fmt"
	"time"

	"main/logger"

	"github.com/adshao/go-binance/v2/futures"
	"github.com/shopspring/decimal"
)

// Account 单账户运行状态：每个账户持有独立的客户端、用户数据处理器和持仓快照。
type Account struct {
	Index       int
	Name        string
	Config      *AccountConfig
	Client      *futures.Client
	Uh          *UserHandler
	MarginRate  *MarginRateResult
	TCPositions map[string]TCPosition
	Log         *logger.Logger
}

// NewAccount 根据配置创建账户：初始化独立客户端和带账户标签的日志器。
// 所有账户共享同一代理与交易参数，仅 API Key 不同。
func NewAccount(index int, cfg *AccountConfig) *Account {
	a := &Account{
		Index:       index,
		Name:        cfg.Name,
		Config:      cfg,
		TCPositions: make(map[string]TCPosition),
	}
	if a.Name == "" {
		a.Name = fmt.Sprintf("account_%d", index+1)
	}
	a.Log = logger.Default().Sub(fmt.Sprintf("%s[%d]", a.Name, index+1))

	proxy := GetEnv().ProxyURL
	if proxy != "" {
		a.Client = futures.NewProxiedClient(cfg.APIKey, cfg.SecretKey, proxy)
	} else {
		a.Client = futures.NewClient(cfg.APIKey, cfg.SecretKey)
	}
	return a
}

// runMainLoop 账户独立主循环。
func (a *Account) runMainLoop() {
	for {
		env := GetEnv()
		a.Log.Info("=========================")
		a.Log.Info("===== 新一轮处理开始 =====")
		a.Log.Info("=========================")
		a.GetTCPositions()

		if a.MarginRate == nil {
			a.Log.Infof("保证金率暂时不可用，休息 %s 再试", env.MainLoopIntervalDuration.String())
			time.Sleep(env.MainLoopIntervalDuration)
			continue
		}

		a.Log.Infof("当前保证金率 %s%%", formatDecimalFixed(a.MarginRate.MarginRatio, 4))

		// 若本轮提交了再平衡订单，则跳过加/减仓操作，但仍正常休息，
		// 避免在市价单反映到账户前立即进入下一轮造成重复下单。
		rebalanced := false
		if !a.MarginRate.MarginRatio.IsZero() {
			a.Log.Info("检查双边仓位是否平衡…")
			if a.BalancePositions() {
				a.Log.Info("再平衡订单已提交，本轮跳过加/减仓操作")
				rebalanced = true
			}
		}

		if !rebalanced {
			if a.MarginRate.MarginRatio.GreaterThan(env.MarginRatioReduceTrigger) {
				a.MarginRatioBeyond()
			} else if a.MarginRate.MarginRatio.LessThan(env.MarginRatioAddTrigger) {
				a.MarginRatioSmall()
			} else {
				a.Log.Info("保证金率在安全范围，无需操作")
			}
		}

		a.Log.Infof("本轮处理完成，休息 %s", env.MainLoopIntervalDuration.String())
		time.Sleep(env.MainLoopIntervalDuration)

		if err := RefreshEnv(); err != nil {
			a.Log.Errorf("刷新配置失败: %v", err)
		}
	}
}

// GetTCPositions 拉取账户保证金率并整理双边持仓。
func (a *Account) GetTCPositions() {
	a.Log.Debug("开始获取仓位和保证金率…")
	result, err := CalcMarginRatio(context.Background(), a.Client, "")
	if err != nil {
		a.Log.Errorf("计算保证金率失败: %v", err)
		return
	}

	a.MarginRate = result
	a.Log.Debugf("仓位更新完成: marginRatio=%s%% positions=%d",
		formatDecimalFixed(result.MarginRatio, 4), len(result.Positions))
	a.FormatSymbol(result.Positions)
}

// HasSufficientAvailableBalance 计算多币种账户折算后的总可用资金，低于阈值时停止继续下单。
func (a *Account) HasSufficientAvailableBalance(scene string) bool {
	if a.MarginRate == nil {
		a.Log.Warnf("保证金率结果为空，跳过 %s 下单", scene)
		return false
	}

	env := GetEnv()
	if a.MarginRate.TotalAvailable.LessThan(env.MinAvailableUSD) {
		a.Log.Warnf("当前总可用资金 %s USD 已低于阈值 %s USD，%s 暂停下单",
			formatDecimalFixed(a.MarginRate.TotalAvailable, 2),
			formatDecimalFixed(env.MinAvailableUSD, 2),
			scene,
		)
		return false
	}

	a.Log.Debugf("可用资金检查通过: available=%s USD threshold=%s USD scene=%s",
		formatDecimalFixed(a.MarginRate.TotalAvailable, 2),
		formatDecimalFixed(env.MinAvailableUSD, 2),
		scene,
	)
	return true
}

// MarginRatioBeyond 保证金率过高时，优先减掉盈利最大的组合，释放保证金占用。
func (a *Account) MarginRatioBeyond() {
	a.Log.Info("保证金率偏高，开始减仓处理")
	env := GetEnv()
	for {
		symbol := a.GetMaxProfitSymbol()
		if symbol == "" {
			a.Log.Info("没有找到可减仓的盈利币种")
			return
		}

		a.Log.Infof("减仓中… 当前保证金率 %s%%", formatDecimalFixed(a.MarginRate.MarginRatio, 4))
		if a.MarginRate.MarginRatio.LessThan(env.MarginRatioReduceTarget) {
			a.Log.Info("保证金率已降到安全水位，减仓结束")
			break
		}

		if env.GetSymbol(symbol) == nil {
			a.Log.Warnf("没有 %s 的配置信息，无法减仓，退出减仓处理", symbol)
			break
		}

		extra := decimalMax(decimal.Zero, a.MarginRate.MarginRatio.Sub(env.MarginRatioReduceTarget))
		usdt := env.ReduceBaseUsdt.Add(extra.Mul(env.ReduceStepUsdtPerRatioPoint)).Round(0)
		a.Log.Debugf("减仓计算: ratio=%s extra=%s usdt=%s base=%s step=%s",
			formatDecimalFixed(a.MarginRate.MarginRatio, 4),
			formatDecimalFixed(extra, 2),
			formatDecimalFixed(usdt, 2),
			formatDecimalFixed(env.ReduceBaseUsdt, 2),
			formatDecimalFixed(env.ReduceStepUsdtPerRatioPoint, 2),
		)
		tc := a.TCPositions[symbol]
		maxCloseValue := tc.USDC.Quantity.Mul(tc.USDC.Price)

		q := decimal.Zero
		if usdt.GreaterThan(maxCloseValue) {
			q = tc.USDC.Quantity
		}

		if !a.HasSufficientAvailableBalance("高保证金率处理") {
			return
		}

		a.CreateTC(symbol, usdt, q)
		time.Sleep(env.ReduceWaitIntervalDuration)
		a.GetTCPositions()
	}
}

// MarginRatioSmall 保证金率偏低时，优先给当前总持仓价值最小的币种补仓。
func (a *Account) MarginRatioSmall() {
	a.Log.Info("保证金率偏低，开始补仓处理")
	env := GetEnv()
	for i := 0; i < env.MaxAddRounds; i++ {
		symbol := a.GetMinValueSymbol()
		if symbol == "" {
			a.Log.Info("没有找到适合补仓的币种")
			return
		}

		a.Log.Infof("补仓 当前保证金率 %s%%", formatDecimalFixed(a.MarginRate.MarginRatio, 4))
		a.Log.Debugf("补仓第 %d/%d 轮", i+1, env.MaxAddRounds)
		if a.MarginRate.MarginRatio.GreaterThan(env.MarginRatioAddTarget) {
			if a.MarginRate.MarginRatio.GreaterThan(env.MarginRatioReduceTrigger) {
				a.MarginRatioBeyond()
				return
			}
			break
		}

		symbolConfig := env.GetSymbol(symbol)
		if symbolConfig == nil {
			a.Log.Warnf("没有 %s 的配置信息，跳过", symbol)
			return
		}

		if !a.HasSufficientAvailableBalance("低保证金率处理") {
			return
		}

		tc := a.NewTC(symbol, symbolConfig.Usdt)
		a.Uh.HandleFilled(tc.ID, &tc.Handle)

		doneC, errCh := tc.Start()
		select {
		case <-doneC:
			time.Sleep(env.TCWaitIntervalDuration)
			a.GetTCPositions()
		case err := <-errCh:
			a.Log.Errorf("组合单执行失败: %v", err)
			a.Uh.HandleFilledDelete(tc.ID)
			time.Sleep(env.TCWaitIntervalDuration)
			a.GetTCPositions()
		}

		time.Sleep(env.LoopStepIntervalDuration)
	}
}
