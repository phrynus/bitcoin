package exchange

import (
	"io"
	"net/http"
	"time"

	"github.com/phrynus/go-utils/unknown"
)

// FetchExchangeInfo 从币安 API 获取交易所信息
func FetchExchangeInfo() (*ExchangeInfo, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://fapi.binance.com/fapi/v1/exchangeInfo")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var exchange ExchangeInfo
	err = unknown.NewUnknown(string(body)).SmartUnmarshal(&exchange)
	if err != nil {
		return nil, err
	}
	exchange.Symbols = make(map[string]*Symbol)
	for i := range exchange.Symbols_ {
		exchange.Symbols_[i].ParseFilters() // 解析过滤器
		symbol := exchange.Symbols_[i].Symbol
		exchange.Symbols[symbol] = &exchange.Symbols_[i]
	}
	return &exchange, nil
}

// ParseFilters 将原始过滤器（[]any）解析为强类型的 Filters
func (s *Symbol) ParseFilters() {
	for _, f := range s.FiltersRaw {
		u := unknown.NewUnknown(unknown.NewUnknown(f).String())

		switch FilterType(f["filterType"]) {
		case FilterTypePriceFilter:
			u.SmartUnmarshal(&s.Filters.PriceFilter)
		case FilterTypeLotSize:
			u.SmartUnmarshal(&s.Filters.LotSizeFilter)
		case FilterTypeMarketLotSize:
			u.SmartUnmarshal(&s.Filters.MarketLotSizeFilter)
		case FilterTypeMaxNumOrders:
			u.SmartUnmarshal(&s.Filters.MaxNumOrdersFilter)
		case FilterTypeMinNotional:
			u.SmartUnmarshal(&s.Filters.MinNotionalFilter)
		}
	}
}
