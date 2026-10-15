package main

// Linux 构建命令：
// $env:GOOS="linux"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"; go build -ldflags="-s -w" .
// Windows 构建命令：
// $env:GOOS="windows"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"; go build -ldflags="-s -w" .

import (
	"context"
	"grid/exchange"
	"os"
	"os/signal"
	"syscall"

	"github.com/phrynus/plog"
)

// 策略参数（proxy / apiKey / secret / demo / symbol / spacing / usdt /
// miniChange / gridUp / gridDown / gridWindow）已移至 config.go，
// 由 .env 或环境变量加载，字段含义见 .env 内注释。

// 日志约定：各文件直接调用 plog，不再集中定义包装函数；阶段标签写在格式串开头，
// 便于按链路检索。主包标签：[启动] [配置] [存储] [行情] [网格] [窗口]
// [买单] [止盈] [撤单] [成交]；exchange 包标签：[连接] [请求] [数据流] [交易对]。
// 级别约定：Debug 逻辑链每一步（入参/分支/中间结果/跳过原因），Info 业务事实
// （网格成型、下单成功、成交、窗口移动完成），Warn 可恢复异常（接口报错已兜底、
// 配置回退默认值），Error 需人工介入（存储异常、下单/撤单失败）。

// 运行期共享句柄：交易所 WebSocket 客户端、用户数据流与最近一次错误。
var (
	wa  *exchange.WsApi
	ud  *exchange.UserData
	err error
)

func init() {
	plog.SetDefault(plog.Config{
		Filename:    "grid.log",      // 日志文件路径（必填）
		MaxSize:     50 << 20,        // 单个文件最大 50MB，超过自动轮转
		MinLevel:    plog.LevelDebug, // 最低记录级别 Debug
		StdoutLevel: plog.LevelInfo,  // 控制台输出最低级别 Info
		Color:       true,            // 控制台 ANSI 彩色
		FileLine:    true,            // 记录调用位置 file:line
		Tag:         "GRID",          // 日志标识符
		BufferSize:  4096,            // 异步缓冲大小
		Compress:    true,            // 轮转后 gzip 压缩
		MaxBackups:  5,               // 保留的压缩备份数
	})

	plog.Debugf("[启动] ===== 网格策略启动 =====")

	// 加载 .env / 环境变量配置（详见 config.go 与 .env）
	loadConfig()

	plog.Debugf("[启动] 初始化内存存储")
	if err := InitStore(); err != nil {
		plog.Errorf("[启动] 初始化内存存储失败: %v", err)
		panic(err)
	}

	plog.Debugf("[启动] 初始化交易所 (模式=%s)", modeName(demo))
	if _, err := exchange.Init(demo, apiKey, secret, proxy); err != nil {
		plog.Errorf("[启动] 初始化交易所失败: %v", err)
		panic(err)
	}

	plog.Debugf("[启动] 创建 WebSocket 交易客户端")
	wa = exchange.NewWsApi()
	plog.Debugf("[启动] 创建用户数据流客户端")
	ud = exchange.NewUserData()

	plog.Infof("[启动] 初始化完成")
}
func main() {
	defer func() {
		plog.Debugf("[启动] 收到退出信号, 开始释放资源")
		LogStats()
		plog.Debugf("[启动] 关闭 WebSocket 交易客户端")
		wa.Close()
		plog.Debugf("[启动] 关闭用户数据流客户端")
		ud.Close()
		plog.Infof("[启动] 已退出")
		plog.Close()
	}()

	// 清空内存网格
	plog.Debugf("[存储] 清空内存网格")
	grids.Clear()
	ResetStats()

	plog.Debugf("[行情] 查询交易对 %s 最优挂单", symbol)
	price, err := wa.Book(context.Background(), symbol)
	if err != nil {
		plog.Errorf("[行情] 查询 %s 最优挂单失败: %v", symbol, err)
		return
	}
	plog.Infof("[行情] %s 买一=%s 卖一=%s", symbol, price.BidPrice, price.AskPrice)

	// sellPrice := exchange.ToString(exchange.ToFloat64(price.BidPrice) * (1 + spacing/10000))

	plog.Debugf("[网格] 以买一价 %s 为基准初始化中心网格", price.BidPrice)
	CalculateGrid(0, price.BidPrice)

	// CalculateDownGrid(0)
	// CalculateDownGrid(1)
	// CalculateDownGrid(2)
	// CalculateUpGrid(0)
	// CalculateUpGrid(-1)
	// CalculateUpGrid(-2)

	plog.Debugf("[窗口] 首次形成窗口并挂单 中心网格=0")

	IntervalVariation(0)      // 多单
	EmptyIntervalVariation(0) // 空单

	// gs := CompleteGrid(0)
	// plog.Debugf("[网格] %+v", gs)

	plog.Debugf("[启动] 主流程就绪, 等待退出信号 (Ctrl+C)")
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
}
