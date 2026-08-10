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
		exc.WsApi.Close()
		logger.Close()
	}()

	balance, _ := exc.WsApi.Depth(context.Background(), "BTCUSDT", 5)
	logger.Info(balance)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
}
