package exchange

import (
	"context"
	"sync"
	"time"

	"github.com/adshao/go-binance/v2/futures"
)

var exc *Exchange

type Exchange struct {
	noClient *futures.Client

	proxy string

	symbols    map[string]*SymbolInfo
	timeOffset int64

	mu   sync.RWMutex
	stop chan struct{}
}

func Init(proxy string) (*Exchange, error) {
	c := &futures.Client{}
	if proxy != "" {
		c = futures.NewProxiedClient("", "", proxy)
		futures.SetWsProxyUrl(proxy)
	} else {
		c = futures.NewClient("", "")
	}
	if err := c.NewPingService().Do(context.Background()); err != nil {
		panic(err)
	}

	timeOffset, err := c.NewSetServerTimeService().Do(context.Background())
	if err != nil {
		panic(err)
	}

	info, err := c.NewExchangeInfoService().Do(context.Background())
	if err != nil {
		panic(err)
	}

	exc = &Exchange{
		noClient: c,

		proxy:      proxy,
		timeOffset: timeOffset,
		symbols:    buildSymbols(info),

		stop: make(chan struct{}),
	}

	go exc.startAutoRefresh()

	return exc, nil
}

func (e *Exchange) getSymbol(symbol string) (*SymbolInfo, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	si, ok := e.symbols[symbol]
	return si, ok
}

func (e *Exchange) Close() {
	if e.stop != nil {
		select {
		case <-e.stop:
		default:
			close(e.stop)
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
	info, err := e.noClient.NewExchangeInfoService().Do(context.Background())
	if err != nil {
		return
	}
	newSymbols := buildSymbols(info)
	e.mu.Lock()
	e.symbols = newSymbols
	e.mu.Unlock()
}

func buildSymbols(info *futures.ExchangeInfo) map[string]*SymbolInfo {

	_symbols := make(map[string]*SymbolInfo)

	for _, symbol := range info.Symbols {

		if symbol.Status == "TRADING" {
			_symbols[symbol.Symbol] = &SymbolInfo{
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
	return _symbols
}
