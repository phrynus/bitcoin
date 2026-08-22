package exchange

import (
	"context"
	"fmt"
	"sync"
	"time"

	"main/logger"

	"github.com/adshao/go-binance/v2/futures"
)

var (
	exc *Exchange
)

type Exchange struct {
	Client  *futures.Client
	Symbols map[string]*SymbolInfo

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

// startAutoRefresh 后台每 (4 小时)自动刷新一次交易对信息(Symbols)。
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
				LotSizeFilter:         parseLotSizeFilter(symbol),
				MarketLotSizeFilter:   parseMarketLotSizeFilter(symbol),
				MinNotionalFilter:     parseMinNotionalFilter(symbol),
			}
		}
	}
	return symbols
}

// InitExc 初始化共享的交易所公开元数据(交易对信息), 并启动后台自动刷新。
// 公开数据与账户密钥无关, 所有账户共享同一份;
// 各账户再通过 NewWsApi 建立独立的 WS API 客户端(订单/仓位等签名操作)。
func InitExc(proxy string) (*Exchange, error) {
	c := futures.NewClient("", "")
	if proxy != "" {
		c = futures.NewProxiedClient("", "", proxy)
		futures.SetWsProxyUrl(proxy) // WebSocket 代理需单独设置
	}
	if err := c.NewPingService().Do(context.Background()); err != nil {
		return nil, fmt.Errorf("ping 交易所失败: %w", err)
	}

	info, err := c.NewExchangeInfoService().Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("获取交易所信息失败: %w", err)
	}

	exc = &Exchange{
		Client:  c,
		Symbols: buildSymbols(info),
		stop:    make(chan struct{}),
	}
	// 后台每 4 小时自动刷新一次交易对信息
	go exc.startAutoRefresh()

	return exc, nil
}
