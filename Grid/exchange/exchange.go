package exchange

import (
	"context"

	"github.com/adshao/go-binance/v2/futures"
)

type Exchange struct {
	Client  *futures.Client
	Symbols map[string]*SymbolInfo
	WsApi   *WsApi // WS API 客户端(账户/订单操作)
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
	// 创建一个新的临时map用于存储当前TRADING状态的交易对
	tempSymbols := make(map[string]*SymbolInfo)

	// 处理新的交易对数据
	for _, symbol := range info.Symbols {
		// 只处理状态为"TRADING"的交易对
		if symbol.Status == "TRADING" {
			tempSymbols[symbol.Symbol] = &SymbolInfo{
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
				MaxNumOrdersFilter:    parseMaxNumOrdersFilter(symbol),
				MinNotionalFilter:     parseMinNotionalFilter(symbol),
				PercentPriceFilter:    parsePercentPriceFilter(symbol),
			}
		}
	}

	return &Exchange{
		Client:  c,
		Symbols: tempSymbols,
		WsApi:   NewWsApi(apiKey, secret, proxy),
	}, nil
}
