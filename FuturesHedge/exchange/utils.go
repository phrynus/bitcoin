package exchange

// 工具库

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/adshao/go-binance/v2/futures"
)

// ToString 把过滤器原始值安全地转换为字符串(不依赖外部 unknown 包)。
func ToString(data any) string {
	switch v := data.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case nil:
		return ""
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(b)
	}
}

// ToInt64 把过滤器原始值安全地转换为 int64(失败返回 0, 不 panic)。
func ToInt64(data any) int64 {
	switch v := data.(type) {
	case string:
		i, _ := strconv.ParseInt(v, 10, 64)
		return i
	case json.Number:
		i, _ := v.Int64()
		return i
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	}
	return 0
}

// ToFloat64 把过滤器原始值安全地转换为 float64(失败返回 0, 不 panic)。
func ToFloat64(data any) float64 {
	switch v := data.(type) {
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f
	case json.Number:
		f, _ := v.Float64()
		return f
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
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

// 解析限价单数量过滤器
func parseLotSizeFilter(symbol futures.Symbol) *LotSizeFilter {
	for _, filter := range symbol.Filters {
		if filter["filterType"] != nil && filter["filterType"].(string) == "LOT_SIZE" {
			f := &LotSizeFilter{}
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
