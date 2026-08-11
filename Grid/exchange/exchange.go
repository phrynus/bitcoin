package exchange

import (
	"context"
	"sync"
	"time"

	"main/logger"

	"github.com/adshao/go-binance/v2/futures"
)

var (
	exc *Exchange
)

// symbolRefreshInterval 交易对信息(Symbols)自动刷新周期。
const symbolRefreshInterval = 4 * time.Hour

type Exchange struct {
	Client  *futures.Client
	Symbols map[string]*SymbolInfo
	WsApi   *WsApi // WS API 客户端(账户/订单操作)

	mu   sync.RWMutex  // 保护 Symbols 的并发读写(后台自动刷新与业务读取)
	stop chan struct{} // 关闭后台自动刷新 goroutine
}

// GetSymbol 并发安全地按交易对符号获取交易对信息。
func (e *Exchange) GetSymbol(symbol string) (*SymbolInfo, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	si, ok := e.Symbols[symbol]
	return si, ok
}

// Close 停止交易对信息自动刷新等后台任务。
func (e *Exchange) Close() {
	if e.stop != nil {
		select {
		case <-e.stop:
		default:
			close(e.stop)
		}
	}
}

// startAutoRefresh 后台每 symbolRefreshInterval(4 小时)自动刷新一次交易对信息(Symbols)。
func (e *Exchange) startAutoRefresh() {
	ticker := time.NewTicker(symbolRefreshInterval)
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

// refreshSymbols 重新拉取交易所交易对信息并原子替换 Symbols(并发安全)。
// 刷新失败仅记日志, 不影响现有 Symbols 数据。
func (e *Exchange) refreshSymbols() {
	info, err := e.Client.NewExchangeInfoService().Do(context.Background())
	if err != nil {
		logger.Warnf("刷新交易对信息失败: %v", err)
		return
	}
	newSymbols := buildSymbols(info)
	e.mu.Lock()
	e.Symbols = newSymbols
	e.mu.Unlock()
	logger.Infof("交易对信息已自动刷新, 共 %d 个交易对", len(newSymbols))
}

// buildSymbols 从交易所信息构建 TRADING 状态交易对的过滤器映射。
func buildSymbols(info *futures.ExchangeInfo) map[string]*SymbolInfo {
	// 创建一个新的临时map用于存储当前TRADING状态的交易对
	symbols := make(map[string]*SymbolInfo)

	// 处理新的交易对数据
	for _, symbol := range info.Symbols {
		// 只处理状态为"TRADING"的交易对
		if symbol.Status == "TRADING" {
			symbols[symbol.Symbol] = &SymbolInfo{
				Symbol:                symbol.Symbol,
				Pair:                  symbol.Pair,
				ContractType:          ToString(symbol.ContractType),
				Status:                symbol.Status,
				PricePrecision:        ToInt64(symbol.PricePrecision),
				QuantityPrecision:     ToInt64(symbol.QuantityPrecision),
				BaseAssetPrecision:    ToInt64(symbol.BaseAssetPrecision),
				QuotePrecision:        ToInt64(symbol.QuotePrecision),
				UnderlyingType:        symbol.UnderlyingType,
				QuoteAsset:            symbol.QuoteAsset,
				BaseAsset:             symbol.BaseAsset,
				MaintMarginPercent:    symbol.MaintMarginPercent,
				RequiredMarginPercent: symbol.RequiredMarginPercent,
				LiquidationFee:        symbol.LiquidationFee,
				MarketTakeBound:       symbol.MarketTakeBound,
				PriceFilter:           parsePriceFilter(symbol),
				MarketLotSizeFilter:   parseMarketLotSizeFilter(symbol),
				MinNotionalFilter:     parseMinNotionalFilter(symbol),
			}
		}
	}
	return symbols
}

func RunExc(apiKey, secret, proxy string) (*Exchange, error) {
	c := &futures.Client{}
	if proxy != "" {
		c = futures.NewProxiedClient(apiKey, secret, proxy)
		futures.SetWsProxyUrl(proxy) // WebSocket 代理需单独设置
	} else {
		c = futures.NewClient(apiKey, secret)
	}
	if err := c.NewPingService().Do(context.Background()); err != nil {
		panic(err)
	}

	info, err := c.NewExchangeInfoService().Do(context.Background())
	if err != nil {
		panic(err)
	}

	exc = &Exchange{
		Client:  c,
		Symbols: buildSymbols(info),
		WsApi:   NewWsApi(apiKey, secret, proxy),
		stop:    make(chan struct{}),
	}
	// 后台每 4 小时自动刷新一次交易对信息
	go exc.startAutoRefresh()

	return exc, nil
}
