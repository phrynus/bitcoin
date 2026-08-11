package exchange

// 工具库

import (
	"main/pkg/unknown"

	"github.com/adshao/go-binance/v2/futures"
)

func ToString(data any) string {
	return unknown.NewUnknown(data).String()
}

func ToInt64(data any) int64 {
	return unknown.NewUnknown(data).Int()
}

func ToFloat64(data any) float64 {
	return unknown.NewUnknown(data).Float()
}

// 解析价格过滤器
func parsePriceFilter(symbol futures.Symbol) *PriceFilter {
	for _, filter := range symbol.Filters {
		if filter["filterType"] != nil && filter["filterType"].(string) == "PRICE_FILTER" {
			f := &PriceFilter{}
			if i, ok := filter["maxPrice"]; ok {
				f.MaxPrice = i.(string)
				f.MaxPriceF = ToFloat64(f.MaxPrice)
			}
			if i, ok := filter["minPrice"]; ok {
				f.MinPrice = i.(string)
				f.MinPriceF = ToFloat64(f.MinPrice)
			}
			if i, ok := filter["tickSize"]; ok {
				f.TickSize = i.(string)
				f.TickSizeF = ToFloat64(f.TickSize)
			}
			return f
		}
	}
	return nil
}

// 解析市价单数量过滤器
func parseMarketLotSizeFilter(symbol futures.Symbol) *MarketLotSizeFilter {
	for _, filter := range symbol.Filters {
		if filter["filterType"] != nil && filter["filterType"].(string) == "MARKET_LOT_SIZE" {
			f := &MarketLotSizeFilter{}
			if i, ok := filter["maxQty"]; ok {
				f.MaxQuantity = i.(string)
				f.MaxQuantityF = ToFloat64(f.MaxQuantity)
			}
			if i, ok := filter["minQty"]; ok {
				f.MinQuantity = i.(string)
				f.MinQuantityF = ToFloat64(f.MinQuantity)
			}
			if i, ok := filter["stepSize"]; ok {
				f.StepSize = i.(string)
				f.StepSizeF = ToFloat64(f.StepSize)
			}
			return f
		}
	}
	return nil
}

// 解析最小名义价值过滤器
func parseMinNotionalFilter(symbol futures.Symbol) *MinNotionalFilter {
	for _, filter := range symbol.Filters {
		if filter["filterType"] != nil && filter["filterType"].(string) == "MIN_NOTIONAL" {
			f := &MinNotionalFilter{}
			if i, ok := filter["notional"]; ok {
				f.Notional = i.(string)
				f.NotionalF = ToFloat64(f.Notional)
			}
			return f
		}
	}
	return nil
}
