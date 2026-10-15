// Package exchange 封装币安 U 本位合约（FAPI）的行情、账户与交易接口：
// 提供基于 WebSocket 的请求/响应客户端 WsApi、用户数据流 UserData、
// 轻量 REST 客户端 restClient，以及交易对精度修正与类型转换等工具函数。
package exchange

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/phrynus/plog"
)

// ---------------------------------------------------------------------------
// 常量
// ---------------------------------------------------------------------------

// 币安 U 本位合约接入地址：实盘与模拟盘（Demo Trading / Testnet）各一套。
// 由 Init 的 demo 参数切换，需在 NewWsApi、NewUserData 之前调用。
const (
	liveRestURL   = "https://fapi.binance.com"             // 实盘 REST
	liveTradeURL  = "wss://ws-fapi.binance.com/ws-fapi/v1" // 实盘 WS API（行情、账户、交易）
	liveStreamURL = "wss://fstream.binance.com/private/ws" // 实盘私有用户数据流

	demoRestURL   = "https://demo-fapi.binance.com"              // 模拟盘 REST
	demoTradeURL  = "wss://testnet.binancefuture.com/ws-fapi/v1" // 模拟盘 WS API（行情、账户、交易）
	demoStreamURL = "wss://fstream.binancefuture.com/private/ws" // 模拟盘私有用户数据流

	// restClient
	restTimeout       = 15 * time.Second // 单次 REST 请求超时
	restMaxBodySize   = 32 << 20         // 响应体读取上限
	restRecvWindow    = 5000             // 请求有效时间窗口（毫秒）
	restAPIKeyHeader  = "X-MBX-APIKEY"   // API 密钥请求头
	restSignKey       = "signature"      // 签名参数名
	restTimestampKey  = "timestamp"      // 时间戳参数名
	restRecvWindowKey = "recvWindow"     // 有效窗口参数名

	// WsApi
	wsDialTimeout = 10 * time.Second // 连接握手超时
	wsWriteWait   = 10 * time.Second // 单次写入超时
	wsPongWait    = 60 * time.Second // 读超时（收到 pong 后重置）
	wsPingPeriod  = 50 * time.Second // ping 发送周期

	// UserData
	userDataDialTimeout       = 10 * time.Second // 连接握手超时
	userDataKeepaliveInterval = 30 * time.Minute // listenKey 续期周期
	userDataRenewTimeout      = 10 * time.Second // listenKey 相关请求超时
	userDataPingPeriod        = 50 * time.Second // ping 发送周期
	userDataPongWait          = 60 * time.Second // 读超时（收到 pong 后重置）
	userDataWriteWait         = 10 * time.Second // 单次写入超时
	userDataCloseTimeout      = 5 * time.Second  // 关闭时注销 listenKey 的超时
	userDataEventBuffer       = 256              // 待分发事件队列容量
)

// ---------------------------------------------------------------------------
// 包级变量
// ---------------------------------------------------------------------------

var (
	// Exc 为全局 Exchange 单例，由 Init 赋值。
	Exc *Exchange

	// 当前生效的接入地址，默认实盘；由 demoMu 保护，可并发读写。
	demoMu    sync.RWMutex
	demo      bool
	restURL   = liveRestURL
	tradeURL  = liveTradeURL
	streamURL = liveStreamURL

	// 全局共享的连接参数：API 密钥与代理。
	// 由 Init 设置，供 Init、NewWsApi、NewUserData 共用，避免每处调用重复传入。
	authMu     sync.RWMutex
	authAPIKey string
	authSecret string
	authProxy  string

	// 包内本地错误（非币安返回），可用 errors.Is 判断，避免与币安真实错误码混淆。
	ErrClosed          = errors.New("WsApi 已关闭")       // WsApi 已关闭
	ErrConnUnavailable = errors.New("WsApi 连接不可用")     // 连接不可用
	ErrConnReset       = errors.New("WsApi 连接断开, 请重试") // 连接断开导致在途请求失败
	ErrUserDataClosed  = errors.New("UserData 已关闭")    // UserData 已关闭
)

// Exchange 持有交易所级别的共享状态：交易对信息表、与服务器的时间偏移及代理配置。
// 通过 Init 创建，交易对信息每 4 小时在后台自动刷新。
// 可并发使用，交易对表由读写锁保护。
type Exchange struct {
	rest *restClient

	symbols    map[string]*SymbolInfo
	timeOffset int64

	mu   sync.RWMutex
	stop chan struct{}
}

// Init 初始化全局 Exchange 单例：设置实盘/模拟盘与共享密钥、代理，校正服务器时间、
// 拉取交易对信息，并启动后台定时刷新。demo 为 true 时使用模拟盘，proxy 为空表示直连。
// 该函数会把结果赋给包级变量 exc，并启动一个后台 goroutine（由 Close 停止）。
// 任一网络请求失败都会返回错误。
func Init(demo bool, apiKey, secret, proxy string) (*Exchange, error) {
	setDemo(demo)
	setAuth(apiKey, secret, proxy)

	rest := newRestClient()
	ctx := context.Background()

	plog.Debugf("[连接] 开始初始化交易所 模式=%s REST=%s", demoLabel(demo), currentRestURL())

	plog.Debugf("[连接] 探测 REST 连通性 /fapi/v1/ping")
	if err := rest.ping(ctx); err != nil {
		return nil, err
	}

	plog.Debugf("[连接] 查询服务器时间 /fapi/v1/time")
	serverTime, err := rest.serverTime(ctx)
	if err != nil {
		return nil, err
	}

	plog.Debugf("[连接] 拉取交易对信息 /fapi/v1/exchangeInfo")
	info, err := rest.exchangeInfo(ctx)
	if err != nil {
		return nil, err
	}

	offset := time.Now().UnixMilli() - serverTime
	symbols := buildSymbols(info)

	Exc = &Exchange{
		rest: rest,

		timeOffset: offset,
		symbols:    symbols,

		stop: make(chan struct{}),
	}

	plog.Infof("[连接] 交易所初始化完成 模式=%s 可用交易对=%d 个 时间偏移=%dms",
		demoLabel(demo), len(symbols), offset)

	go Exc.startAutoRefresh()

	return Exc, nil
}

func (e *Exchange) GetSymbol(symbol string) (*SymbolInfo, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	si, ok := e.symbols[symbol]
	return si, ok
}

// timeOffset 返回本地时间相对币安服务器时间的偏移（毫秒）；未初始化时返回 0。
func timeOffset() int64 {
	if Exc == nil {
		return 0
	}
	return Exc.timeOffset
}

// Close 停止后台交易对刷新 goroutine。可重复调用。
func (e *Exchange) Close() {
	if e.stop != nil {
		select {
		case <-e.stop:
		default:
			close(e.stop)
			plog.Debugf("[交易对] 停止交易对后台刷新")
		}
	}
}

func (e *Exchange) startAutoRefresh() {
	ticker := time.NewTicker(4 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			e.refreshSymbols()
		case <-e.stop:
			return
		}
	}
}

func (e *Exchange) refreshSymbols() {
	plog.Debugf("[交易对] 后台刷新交易对信息")
	info, err := e.rest.exchangeInfo(context.Background())
	if err != nil {
		plog.Warnf("[交易对] 刷新交易对信息失败, 沿用旧表: %v", err)
		return
	}
	newSymbols := buildSymbols(info)
	e.mu.Lock()
	e.symbols = newSymbols
	e.mu.Unlock()
	plog.Infof("[交易对] 交易对信息已刷新 可用交易对=%d 个", len(newSymbols))
}

// FixPrice 按交易对规则修正价格：先截断到 MinPrice/MaxPrice 区间，再向下取整到 TickSize 的整数倍。
// 交易对不在列表中，或修正后仍不满足规则（越界、非 TickSize 倍数）时返回错误。
func (e *Exchange) FixPrice(symbol string, price float64) (float64, error) {
	si, ok := e.GetSymbol(symbol)
	if !ok {
		return 0, fmt.Errorf("交易对 %s 不在 Symbols 交易对列表中", symbol)
	}
	fp, err := FixPriceText(si, strconv.FormatFloat(price, 'f', -1, 64))
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(fp, 64)
}

// FixQuantity 按交易对规则修正数量：先截断到 MinQuantity/MaxQuantity 区间，再向下取整到 StepSize 的整数倍。
// 交易对不在列表中，或修正后仍不满足规则（越界、非 StepSize 倍数）时返回错误。
func (e *Exchange) FixQuantity(symbol string, quantity float64) (float64, error) {
	si, ok := e.GetSymbol(symbol)
	if !ok {
		return 0, fmt.Errorf("交易对 %s 不在 Symbols 交易对列表中", symbol)
	}
	fq, err := fixQuantityText(si, strconv.FormatFloat(quantity, 'f', -1, 64))
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(fq, 64)
}

// FixOrder 同时修正价格与数量；若修正后名义价值低于 MinNotional，会自动向上取整数量以满足要求。
// 交易对不在列表中、价格或数量非法，或无法在最大数量限制内满足最小名义价值时返回错误。
func (e *Exchange) FixOrder(symbol string, price, quantity float64) (float64, float64, error) {
	si, ok := e.GetSymbol(symbol)
	if !ok {
		return 0, 0, fmt.Errorf("交易对 %s 不在 Symbols 交易对列表中", symbol)
	}
	fp, fq, err := FixOrderText(si,
		strconv.FormatFloat(price, 'f', -1, 64),
		strconv.FormatFloat(quantity, 'f', -1, 64))
	if err != nil {
		return 0, 0, err
	}
	p, err1 := strconv.ParseFloat(fp, 64)
	q, err2 := strconv.ParseFloat(fq, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("修正结果解析失败: %v / %v", err1, err2)
	}
	return p, q, nil
}

// ---------------------------------------------------------------------------
// 接入地址
// ---------------------------------------------------------------------------

// setDemo 切换实盘/模拟盘接入地址：on 为 true 时使用模拟盘。
// 只影响调用之后创建的客户端，由 Init 在创建任何客户端之前调用。
func setDemo(on bool) {
	demoMu.Lock()
	defer demoMu.Unlock()
	demo = on
	if on {
		restURL, tradeURL, streamURL = demoRestURL, demoTradeURL, demoStreamURL
		plog.Debugf("[连接] 接入地址切换为模拟盘 REST=%s WA=%s STREAM=%s", restURL, tradeURL, streamURL)
		return
	}
	restURL, tradeURL, streamURL = liveRestURL, liveTradeURL, liveStreamURL
	plog.Debugf("[连接] 接入地址切换为实盘 REST=%s WA=%s STREAM=%s", restURL, tradeURL, streamURL)
}

// demoLabel 返回运行模式的中文名称，供日志展示。
func demoLabel(on bool) string {
	if on {
		return "模拟盘"
	}
	return "实盘"
}

// Demo 返回当前是否为模拟盘。
func Demo() bool {
	demoMu.RLock()
	defer demoMu.RUnlock()
	return demo
}

// currentRestURL 返回当前生效的 REST 接入地址。
func currentRestURL() string {
	demoMu.RLock()
	defer demoMu.RUnlock()
	return restURL
}

// currentTradeURL 返回当前生效的 WS API 接入地址。
func currentTradeURL() string {
	demoMu.RLock()
	defer demoMu.RUnlock()
	return tradeURL
}

// currentStreamURL 返回当前生效的用户数据流地址前缀。
func currentStreamURL() string {
	demoMu.RLock()
	defer demoMu.RUnlock()
	return streamURL
}

// ---------------------------------------------------------------------------
// 共享连接参数
// ---------------------------------------------------------------------------

// setAuth 设置全局共享的 API 密钥与代理：apiKey/secret 用于签名请求，proxy 为空表示直连。
// 由 Init 调用，设置后 Init、NewWsApi、NewUserData 都会复用这套参数。
func setAuth(apiKey, secret, proxy string) {
	authMu.Lock()
	defer authMu.Unlock()
	authAPIKey, authSecret, authProxy = apiKey, secret, proxy
}

// auth 返回当前共享的连接参数快照。
func auth() (apiKey, secret, proxy string) {
	authMu.RLock()
	defer authMu.RUnlock()
	return authAPIKey, authSecret, authProxy
}
