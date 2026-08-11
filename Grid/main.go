package main

// Linux 构建命令：
// $env:GOOS="linux"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"; go build -ldflags="-s -w" .
// Windows 构建命令：
// $env:GOOS="windows"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"; go build -ldflags="-s -w" .

import (
	"context"
	"main/logger"
	"os"
	"os/signal"
	"syscall"

	"main/exchange"
)

var (
	cfg *Config
	exc *exchange.Exchange

	err error
)

func init() {
	cfg, err = RunConfig()
	exc, err = exchange.RunExc(cfg.APIKey, cfg.SecretKey, cfg.ProxyURL)

	logger.Info("═══ 服务启动 ═══")
}

func main() {
	defer func() {
		exc.Close() // 停止交易对信息自动刷新等后台任务
		exc.WsApi.Close()
		logger.Close()
	}()

	// 链式下单(参数直接填文本): 双向持仓开多限价单
	// 下单时自动(基于 exchange 包内包级 exc.Symbols):
	//   校验交易对存在 + 修正价格/数量(范围/步长/最小名义价值, 全程精确十进制运算)
	order, err := exc.WsApi.NewOrder().
		Symbol("SOLUSDT").
		Side("BUY").
		PositionSide("LONG").
		Type("LIMIT").
		Usdt("5").
		Price("52.432523423").
		DoPlace(context.Background())
	if err != nil {
		logger.Errorf("下单失败: %v", err)
		return
	}
	logger.Info(order)

	logger.Info("═══ 关闭项目 ═══")
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
}
