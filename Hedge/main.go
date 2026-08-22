package main

// Linux 构建命令：
// $env:GOOS="linux"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"; go build -ldflags="-s -w" .
// Windows 构建命令：
// $env:GOOS="windows"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"; go build -ldflags="-s -w" .

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"main/exchange"

	"github.com/phrynus/go-utils/plog"
)

var (
	cfg  *Config
	bots map[string]*hedge = make(map[string]*hedge)
)

// init 加载配置、初始化交易所连接，并按账户创建对冲机器人实例。
func init() {
	var err error
	if cfg, err = LoadConfig(); err != nil {
		panic(err)
	}
	plog.Infof("配置加载完成: 账户数=%d 规划币数=%d 基准点=%.2f%% 代理=%q",
		len(cfg.Accounts), len(cfg.Plans), cfg.Margin.Base, cfg.ProxyURL)
	if _, err = exchange.Init(cfg.ProxyURL); err != nil {
		panic(err)
	}

	for _, a := range cfg.Accounts {
		bots[a.Name] = newHedge(a.Name, a.APIKey, a.SecretKey, cfg.ProxyURL)
	}
	plog.Infof("交易所初始化完成，共创建 %d 个对冲机器人", len(bots))
}

// main 启动所有账户的再平衡主循环，并等待退出信号。
func main() {

	// 退出前关闭所有连接与日志
	defer func() {
		for name, b := range bots {
			plog.Infof("关闭账户 %s 的连接", name)
			b.Close()
		}
		plog.Infof("所有连接已关闭，程序退出")
	}()

	// 逐个启动各账户主循环（间隔 10s，避免同时发起大量请求）
	for _, a := range cfg.Accounts {
		bot := bots[a.Name]
		if bot == nil {
			continue
		}
		go bot.RebalanceLoop()
		time.Sleep(10 * time.Second)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	plog.Infof("收到退出信号，开始关闭所有连接")
}
