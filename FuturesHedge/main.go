package main

// Linux 构建命令：
// $env:GOOS="linux"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"; go build -ldflags="-s -w" .
// Windows 构建命令：
// $env:GOOS="windows"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"; go build -ldflags="-s -w" .

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"main/exchange"
	"main/logger"

	"github.com/adshao/go-binance/v2/futures"
	"github.com/shopspring/decimal"
)

var (
	ExchangeInfo *exchange.ExchangeInfo
	Accounts     []*Account
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

	// 初始化日志器
	logger.SetDefault(logger.Config{
		Filename:    "app.log",
		MaxSize:     52428800,
		MinLevel:    logger.ParseLevel("DEBUG"),
		StdoutLevel: logger.ParseLevel("INFO"),
		Color:       true,
		FileLine:    false,
		Tag:         "MAIN",
		Async:       true,
		BufferSize:  4096,
		Compress:    true,
	})

	if err := initEnv(); err != nil {
		fmt.Printf("初始化环境失败: %v", err)
		os.Exit(1)
	}

	env := GetEnv()
	if env.ProxyURL != "" {
		futures.SetWsProxyUrl(env.ProxyURL) // WebSocket 代理需单独设置
	}

	var err error
	ExchangeInfo, err = exchange.FetchExchangeInfo(env.ProxyURL)
	if err != nil {
		fmt.Printf("获取交易所信息失败了: %v", err)
		os.Exit(1)
	}

	Accounts = make([]*Account, 0, len(env.Accounts))
	for i, cfg := range env.Accounts {
		acc := NewAccount(i, &cfg)
		if err := acc.Client.NewPingService().Do(context.Background()); err != nil {
			fmt.Printf("账户 %s Ping 失败: %v", acc.Name, err)
			os.Exit(1)
		}
		Accounts = append(Accounts, acc)
	}

	for _, acc := range Accounts {
		acc.Log.Info("正在启动用户数据处理器…")
		acc.Uh = NewFuturesUserHandler(acc.Client)
		uhComplete, err := acc.Uh.Start()
		if err != nil {
			fmt.Printf("账户 %s 启动用户数据处理器失败: %v", acc.Name, err)
			os.Exit(1)
		}
		acc.Log.Info("等待用户数据流预热完成…")
		<-uhComplete
		acc.Log.Info("用户数据处理器已就绪")
	}

	logger.Infof("已加载 %d 个账户: %v", len(Accounts), accountNames())
	logger.Debugf("当前配置: symbols=%v holdingRatio=%s reduceTrigger=%s addTrigger=%s",
		env.GetAllSymbols(), formatDecimal(env.HoldingRatio),
		formatDecimal(env.MarginRatioReduceTrigger), formatDecimal(env.MarginRatioAddTrigger))
}

func main() {
	defer func() {
		for _, acc := range Accounts {
			acc.Uh.Close()
		}
		logger.Close()
	}()

	// 并行运行各账户主循环，但按账户序号错位启动，避免同时请求接口触发限频。
	stagger := GetEnv().AccountStaggerIntervalDuration
	for i, acc := range Accounts {
		go func(a *Account, idx int) {
			offset := time.Duration(0)
			if idx > 0 && stagger > 0 {
				offset = stagger * time.Duration(idx)
				time.Sleep(offset)
			}
			a.Log.Infof("账户主循环启动（时间错位 %s）", offset.String())
			a.runMainLoop()
		}(acc, i)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	for _, acc := range Accounts {
		acc.Uh.Close()
	}
}

// accountNames 返回所有账户名称，用于启动日志。
func accountNames() []string {
	names := make([]string, len(Accounts))
	for i, acc := range Accounts {
		names[i] = acc.Name
	}
	return names
}
