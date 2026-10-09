package exchange

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// GenID 生成带 "g" 前缀、去掉连字符的 UUID，用作币安客户端订单号（newClientOrderId）。
func GenID() string {
	return "g" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

// SymbolInfo 是从币安交易对信息中筛选出的交易规则快照，仅包含状态为 TRADING 的合约，
// 用于价格/数量的精度修正与最小名义价值校验。
// 以 F 结尾的字段是其对应字符串字段解析后的浮点值，避免重复解析。
type SymbolInfo struct {
	Symbol                string               `json:"symbol"`                // 交易对，如 BTCUSDT
	Pair                  string               `json:"pair"`                  // 基础交易对
	ContractType          string               `json:"contractType"`          // 合约类型，如 PERPETUAL、CURRENT_QUARTER
	Status                string               `json:"status"`                // 合约状态，仅 TRADING 会被收录
	PricePrecision        int64                `json:"pricePrecision"`        // 价格小数位数
	QuantityPrecision     int64                `json:"quantityPrecision"`     // 数量小数位数
	BaseAssetPrecision    int64                `json:"baseAssetPrecision"`    // 基础资产精度
	QuotePrecision        int64                `json:"quotePrecision"`        // 计价资产精度
	UnderlyingType        string               `json:"underlyingType"`        // 标的类型，如 COIN、INDEX
	QuoteAsset            string               `json:"quoteAsset"`            // 计价资产，如 USDT
	BaseAsset             string               `json:"baseAsset"`             // 基础资产，如 BTC
	MaintMarginPercent    string               `json:"maintMarginPercent"`    // 维持保证金率
	RequiredMarginPercent string               `json:"requiredMarginPercent"` // 开仓所需保证金率
	LiquidationFee        string               `json:"liquidationFee"`        // 强平费率
	MarketTakeBound       string               `json:"marketTakeBound"`       // 市价单价格保护上限比例
	PriceFilter           *PriceFilter         `json:"priceFilter"`           // 价格过滤规则，nil 表示未下发
	MarketLotSizeFilter   *MarketLotSizeFilter `json:"marketLotSizeFilter"`   // 市价单数量过滤规则，nil 表示未下发
	MinNotionalFilter     *MinNotionalFilter   `json:"minNotionalFilter"`     // 最小名义价值规则，nil 表示未下发
}

// PriceFilter 描述币安 PRICE_FILTER 规则：价格的上下限与最小变动单位（TickSize）。
// MaxPriceF、MinPriceF、TickSizeF 为对应字符串字段解析后的浮点值。
type PriceFilter struct {
	MaxPrice  string  `json:"maxPrice"`  // 最高价
	MinPrice  string  `json:"minPrice"`  // 最低价
	TickSize  string  `json:"tickSize"`  // 最小价格变动单位
	MaxPriceF float64 `json:"maxPriceF"` // MaxPrice 的浮点值
	MinPriceF float64 `json:"minPriceF"` // MinPrice 的浮点值
	TickSizeF float64 `json:"tickSizeF"` // TickSize 的浮点值
}

// MarketLotSizeFilter 描述币安 MARKET_LOT_SIZE 规则：数量的上下限与步进（StepSize）。
// MaxQuantityF、MinQuantityF、StepSizeF 为对应字符串字段解析后的浮点值。
type MarketLotSizeFilter struct {
	MaxQuantity  string  `json:"maxQty"`    // 最大数量
	MinQuantity  string  `json:"minQty"`    // 最小数量
	StepSize     string  `json:"stepSize"`  // 数量步进
	MaxQuantityF float64 `json:"maxQtyF"`   // MaxQuantity 的浮点值
	MinQuantityF float64 `json:"minQtyF"`   // MinQuantity 的浮点值
	StepSizeF    float64 `json:"stepSizeF"` // StepSize 的浮点值
}

// MinNotionalFilter 描述币安 MIN_NOTIONAL 规则：订单的最小名义价值（价格 × 数量）。
// NotionalF 为 Notional 解析后的浮点值。
type MinNotionalFilter struct {
	Notional  string  `json:"notional"`  // 最小名义价值
	NotionalF float64 `json:"notionalF"` // Notional 的浮点值
}

// indirect 逐层解引用指针/接口，nil 指针时返回无效 Value（调用方用 IsValid 判断）。
func indirect(data any) reflect.Value {
	rv := reflect.ValueOf(data)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return reflect.Value{}
		}
		rv = rv.Elem()
	}
	return rv
}

// ToString 将任意类型转换为字符串，nil 或无法识别时返回空串。
// 先按 string/[]byte/error/fmt.Stringer 识别，再逐层解引用指针与接口后按底层类型处理，
// 因此兼容 decimal.Decimal 等实现了 Stringer 的自定义类型。
func ToString(data any) string {
	if data == nil {
		return ""
	}

	switch v := data.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case error:
		return v.Error()
	case fmt.Stringer:
		return v.String()
	}

	rv := indirect(data)
	if !rv.IsValid() {
		return ""
	}

	switch rv.Kind() {
	case reflect.String:
		return rv.String()
	case reflect.Bool:
		return strconv.FormatBool(rv.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(rv.Float(), 'f', -1, 64)
	}

	return fmt.Sprintf("%v", data)
}

// ToInt64 将任意类型转换为 int64，无法转换或为 nil 时返回 0。
// 支持整数、浮点（截断）、bool（true 为 1）以及数字字符串（含科学计数法）；
// json.Number、decimal.Decimal 等 Stringer 按其字符串解析；无符号值超出 int64 范围时返回 0。
func ToInt64(data any) int64 {
	if data == nil {
		return 0
	}

	switch v := data.(type) {
	case int64:
		return v
	case json.Number:
		return parseInt64(v.String())
	case fmt.Stringer:
		return parseInt64(v.String())
	}

	rv := indirect(data)
	if !rv.IsValid() {
		return 0
	}

	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if u := rv.Uint(); u <= math.MaxInt64 {
			return int64(u)
		}
		return 0
	case reflect.Float32, reflect.Float64:
		return int64(rv.Float())
	case reflect.Bool:
		if rv.Bool() {
			return 1
		}
		return 0
	case reflect.String:
		return parseInt64(rv.String())
	}

	return 0
}

// parseInt64 解析十进制字符串，失败时退化为浮点解析（兼容 "1.5"、"1e5"）后截断。
func parseInt64(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f)
	}
	return 0
}

// ToFloat64 将任意类型转换为 float64，无法转换或为 nil 时返回 0。
// 支持整数、浮点、bool（true 为 1）以及数字字符串（含科学计数法）；
// json.Number、decimal.Decimal 等 Stringer 按其字符串解析。
func ToFloat64(data any) float64 {
	if data == nil {
		return 0
	}

	switch v := data.(type) {
	case float64:
		return v
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0
		}
		return f
	case fmt.Stringer:
		return parseFloat64(v.String())
	}

	rv := indirect(data)
	if !rv.IsValid() {
		return 0
	}

	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		return rv.Float()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint())
	case reflect.Bool:
		if rv.Bool() {
			return 1
		}
		return 0
	case reflect.String:
		return parseFloat64(rv.String())
	}

	return 0
}

// parseFloat64 解析数字字符串（含科学计数法），失败返回 0。
func parseFloat64(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return f
}

func buildSymbols(info *exchangeInfoResp) map[string]*SymbolInfo {

	symbols := make(map[string]*SymbolInfo)

	for _, symbol := range info.Symbols {

		if symbol.Status == "TRADING" {
			symbols[symbol.Symbol] = &SymbolInfo{
				Symbol:                symbol.Symbol,
				Pair:                  symbol.Pair,
				ContractType:          symbol.ContractType,
				Status:                symbol.Status,
				PricePrecision:        symbol.PricePrecision,
				QuantityPrecision:     symbol.QuantityPrecision,
				BaseAssetPrecision:    symbol.BaseAssetPrecision,
				QuotePrecision:        symbol.QuotePrecision,
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

// findFilter 返回指定类型的过滤规则，未下发时返回 nil。
func findFilter(symbol exchangeSymbol, filterType string) map[string]any {
	for _, filter := range symbol.Filters {
		if ToString(filter["filterType"]) == filterType {
			return filter
		}
	}
	return nil
}

// filterText 取出过滤规则中 key 对应的字符串值及其浮点值，缺失时返回空串与 0。
func filterText(f map[string]any, key string) (string, float64) {
	v, ok := f[key]
	if !ok {
		return "", 0
	}
	s := ToString(v)
	return s, ToFloat64(s)
}

func parsePriceFilter(symbol exchangeSymbol) *PriceFilter {
	f := findFilter(symbol, "PRICE_FILTER")
	if f == nil {
		return nil
	}
	pf := &PriceFilter{}
	pf.MaxPrice, pf.MaxPriceF = filterText(f, "maxPrice")
	pf.MinPrice, pf.MinPriceF = filterText(f, "minPrice")
	pf.TickSize, pf.TickSizeF = filterText(f, "tickSize")
	return pf
}

func parseMarketLotSizeFilter(symbol exchangeSymbol) *MarketLotSizeFilter {
	f := findFilter(symbol, "MARKET_LOT_SIZE")
	if f == nil {
		return nil
	}
	lf := &MarketLotSizeFilter{}
	lf.MaxQuantity, lf.MaxQuantityF = filterText(f, "maxQty")
	lf.MinQuantity, lf.MinQuantityF = filterText(f, "minQty")
	lf.StepSize, lf.StepSizeF = filterText(f, "stepSize")
	return lf
}

func parseMinNotionalFilter(symbol exchangeSymbol) *MinNotionalFilter {
	f := findFilter(symbol, "MIN_NOTIONAL")
	if f == nil {
		return nil
	}
	nf := &MinNotionalFilter{}
	nf.Notional, nf.NotionalF = filterText(f, "notional")
	return nf
}

func decOrZero(s string) decimal.Decimal {
	d, _ := decimal.NewFromString(s)
	return d
}

func fixPriceText(si *SymbolInfo, price string) (string, error) {
	if si == nil || si.PriceFilter == nil {
		return price, nil
	}
	pf := si.PriceFilter

	p, err := decimal.NewFromString(price)
	if err != nil {
		return "", fmt.Errorf("价格解析失败 %q: %v", price, err)
	}

	if pf.MinPrice != "" {
		if min := decOrZero(pf.MinPrice); p.LessThan(min) {
			p = min
		}
	}
	if pf.MaxPrice != "" {
		if max := decOrZero(pf.MaxPrice); p.GreaterThan(max) {
			p = max
		}
	}

	if pf.TickSize != "" {
		if tick := decOrZero(pf.TickSize); tick.IsPositive() {
			p = p.DivRound(tick, 0).Mul(tick)

			if pf.MinPrice != "" {
				if min := decOrZero(pf.MinPrice); p.LessThan(min) {
					p = min
				}
			}
			if pf.MaxPrice != "" {
				if max := decOrZero(pf.MaxPrice); p.GreaterThan(max) {
					p = max
				}
			}
		}
	}

	if err := validatePrice(pf, p); err != nil {
		return "", err
	}
	return p.String(), nil
}

func validatePrice(pf *PriceFilter, p decimal.Decimal) error {
	if pf.MinPrice != "" && p.LessThan(decOrZero(pf.MinPrice)) {
		return fmt.Errorf("价格 %s 小于 MinPrice %s", p, pf.MinPrice)
	}
	if pf.MaxPrice != "" && p.GreaterThan(decOrZero(pf.MaxPrice)) {
		return fmt.Errorf("价格 %s 大于 MaxPrice %s", p, pf.MaxPrice)
	}
	if pf.TickSize != "" {
		if tick := decOrZero(pf.TickSize); tick.IsPositive() && !p.Div(tick).IsInteger() {
			return fmt.Errorf("价格 %s 不是 TickSize %s 的整数倍", p, pf.TickSize)
		}
	}
	return nil
}

func fixQuantityText(si *SymbolInfo, quantity string) (string, error) {
	if si == nil || si.MarketLotSizeFilter == nil {
		return quantity, nil
	}
	lf := si.MarketLotSizeFilter

	q, err := decimal.NewFromString(quantity)
	if err != nil {
		return "", fmt.Errorf("数量解析失败 %q: %v", quantity, err)
	}

	if lf.MinQuantity != "" {
		if min := decOrZero(lf.MinQuantity); q.LessThan(min) {
			q = min
		}
	}
	if lf.MaxQuantity != "" {
		if max := decOrZero(lf.MaxQuantity); q.GreaterThan(max) {
			q = max
		}
	}

	if lf.StepSize != "" {
		if step := decOrZero(lf.StepSize); step.IsPositive() {
			q = q.DivRound(step, 0).Mul(step)

			if lf.MinQuantity != "" {
				if min := decOrZero(lf.MinQuantity); q.LessThan(min) {
					q = min
				}
			}
			if lf.MaxQuantity != "" {
				if max := decOrZero(lf.MaxQuantity); q.GreaterThan(max) {
					q = max
				}
			}
		}
	}

	if err := validateQuantity(lf, q); err != nil {
		return "", err
	}
	return q.String(), nil
}

func validateQuantity(lf *MarketLotSizeFilter, q decimal.Decimal) error {
	if lf.MinQuantity != "" && q.LessThan(decOrZero(lf.MinQuantity)) {
		return fmt.Errorf("数量 %s 小于 MinQuantity %s", q, lf.MinQuantity)
	}
	if lf.MaxQuantity != "" && q.GreaterThan(decOrZero(lf.MaxQuantity)) {
		return fmt.Errorf("数量 %s 大于 MaxQuantity %s", q, lf.MaxQuantity)
	}
	if lf.StepSize != "" {
		if step := decOrZero(lf.StepSize); step.IsPositive() && !q.Div(step).IsInteger() {
			return fmt.Errorf("数量 %s 不是 StepSize %s 的整数倍", q, lf.StepSize)
		}
	}
	return nil
}

func fixOrderText(si *SymbolInfo, price, quantity string) (string, string, error) {
	if si == nil {
		return price, quantity, errors.New("SymbolInfo 为空, 无法修正订单数据")
	}
	fp, err := fixPriceText(si, price)
	if err != nil {
		return "", "", err
	}
	fq, err := fixQuantityText(si, quantity)
	if err != nil {
		return "", "", err
	}

	if si.MinNotionalFilter != nil && si.MinNotionalFilter.Notional != "" {
		minNotional := decOrZero(si.MinNotionalFilter.Notional)
		p := decOrZero(fp)
		q := decOrZero(fq)
		if minNotional.IsPositive() && p.IsPositive() && p.Mul(q).LessThan(minNotional) {

			need := minNotional.Div(p)
			if si.MarketLotSizeFilter != nil {
				lf := si.MarketLotSizeFilter
				if step := decOrZero(lf.StepSize); step.IsPositive() {
					q = need.Div(step).Ceil().Mul(step)
				} else {
					q = need
				}

				if lf.MinQuantity != "" {
					if min := decOrZero(lf.MinQuantity); q.LessThan(min) {
						if step := decOrZero(lf.StepSize); step.IsPositive() {
							q = min.Div(step).Ceil().Mul(step)
						} else {
							q = min
						}
					}
				}

				if lf.MaxQuantity != "" {
					if max := decOrZero(lf.MaxQuantity); q.GreaterThan(max) {
						return "", "", fmt.Errorf("数量需提高到 %s 才能满足最小名义价值 %s, 但已超过最大数量 %s", q, si.MinNotionalFilter.Notional, lf.MaxQuantity)
					}
				}

				if err := validateQuantity(lf, q); err != nil {
					return "", "", err
				}
			} else {
				q = need
			}

			if p.Mul(q).LessThan(minNotional) {
				return "", "", fmt.Errorf("无法满足最小名义价值 %s(价格 %s × 数量 %s)", si.MinNotionalFilter.Notional, fp, q)
			}
			fq = q.String()
		}
	}
	return fp, fq, nil
}

// buildQuery 按币安签名要求拼接参数：键按字典序升序、值做 URL 转义。
// 直接用 url.Values，其 Encode 会按 key 排序，省去手工排序与拼接。
func buildQuery(params map[string]any) string {
	q := make(url.Values, len(params))
	for k, v := range params {
		q.Set(k, encodeValue(v))
	}
	return q.Encode()
}

// encodeValue 把签名参数值转成字符串形式。
func encodeValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// nextBackoff 指数退避：每次翻倍，上限 30 秒。
func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > 30*time.Second {
		return 30 * time.Second
	}
	return d
}
