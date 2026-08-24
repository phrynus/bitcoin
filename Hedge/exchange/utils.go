package exchange

import (
	"strings"

	"github.com/adshao/go-binance/v2/futures"
	"github.com/google/uuid"
	"github.com/phrynus/go-utils/unknown"
)

// GenID 基于 UUID v4 生成全局唯一的 clientOrderID（去连字符）。
// ⚠️ 曾手写"时间戳+原子计数"，早期版本甚至只依赖 time.Now().UnixNano()，
// 多个 goroutine 落在同一纳秒时生成重复 ID，导致订阅 map 覆盖、成交事件匹配不到而卡死。
// 改用 google/uuid（UUID v4，122 位随机熵）后不再有并发重复风险。
func GenID() string {
	// UUID v4 去连字符（32 字符十六进制），加 "g" 前缀共 33 字符 < Binance clientOrderID 36 上限
	return "g" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func ToString(data any) string {
	return unknown.NewUnknown(data).String()
}

func ToInt64(data any) int64 {
	return unknown.NewUnknown(data).Int()
}

func ToFloat64(data any) float64 {
	return unknown.NewUnknown(data).Float()
}

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
