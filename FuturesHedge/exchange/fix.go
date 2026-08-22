package exchange

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/shopspring/decimal"
)

// 本文件提供价格/数量的数据修正逻辑, 用于生成订单前校验与修正:
//
//   - 交易对必须存在于 Exchange.Symbols 列表中(不在则报错);
//   - 价格: 不得大于 MaxPrice、不得小于 MinPrice, 且必须是 TickSize 的整数倍;
//   - 数量: 不得大于 MaxQuantity、不得小于 MinQuantity, 且必须是 StepSize 的整数倍;
//   - 价格与数量同时存在时(限价单): 名义价值 价格×数量 不得小于 MinNotional,
//     不满足时自动把数量向上取整到 StepSize 的整数倍;
//   - 全程使用 github.com/shopspring/decimal 定点十进制运算,
//     正确处理小数与大数: 不会出现 0.30000000000000004 之类的浮点尾差,
//     也不会出现 1e+06 之类的科学计数法, 文本即交易所接受的精确值。
//
// 过滤器均使用交易所返回的原始字符串字段(MinPrice/MaxPrice/TickSize、
// MinQuantity/MaxQuantity/StepSize、Notional), 精度高于解析后的 float64 版本。

// decOrZero 解析十进制字符串, 失败返回零值(不会 panic)。
// 用于解析交易所过滤器字符串(信任其格式), 输入来自用户/业务层时应先用 dec() 校验。
func decOrZero(s string) decimal.Decimal {
	d, _ := decimal.NewFromString(s)
	return d
}

// FixPriceText 按交易对的价格过滤器修正价格文本:
// 1) 夹取到 [MinPrice, MaxPrice];
// 2) 四舍五入为 TickSize 的整数倍(舍入后若越界再夹一次)。
func FixPriceText(si *SymbolInfo, price string) (string, error) {
	if si == nil || si.PriceFilter == nil {
		return price, nil
	}
	pf := si.PriceFilter

	p, err := decimal.NewFromString(price)
	if err != nil {
		return "", fmt.Errorf("价格解析失败 %q: %v", price, err)
	}

	// 夹取到 [MinPrice, MaxPrice]
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

	// 四舍五入为 TickSize 的整数倍
	if pf.TickSize != "" {
		if tick := decOrZero(pf.TickSize); tick.IsPositive() {
			p = p.DivRound(tick, 0).Mul(tick)
			// 舍入后可能越过边界, 再夹一次(交易所保证 min/max 是 tick 的整数倍)
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

	// 最终校验: 必须在 [Min,Max] 内, 且是 TickSize 的整数倍
	if err := validatePrice(pf, p); err != nil {
		return "", err
	}
	return p.String(), nil
}

// validatePrice 校验修正后的价格是否符合价格过滤器。
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

// FixQuantityText 按交易对的数量过滤器修正数量文本:
// 1) 夹取到 [MinQuantity, MaxQuantity];
// 2) 四舍五入为 StepSize 的整数倍(舍入后若越界再夹一次)。
func FixQuantityText(si *SymbolInfo, quantity string) (string, error) {
	if si == nil || si.MarketLotSizeFilter == nil {
		return quantity, nil
	}
	lf := si.MarketLotSizeFilter

	q, err := decimal.NewFromString(quantity)
	if err != nil {
		return "", fmt.Errorf("数量解析失败 %q: %v", quantity, err)
	}

	// 夹取到 [MinQuantity, MaxQuantity]
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

	// 四舍五入为 StepSize 的整数倍
	if lf.StepSize != "" {
		if step := decOrZero(lf.StepSize); step.IsPositive() {
			q = q.DivRound(step, 0).Mul(step)
			// 舍入后可能越过边界, 再夹一次(交易所保证 min/max 是 step 的整数倍)
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

	// 最终校验: 必须在 [Min,Max] 内, 且是 StepSize 的整数倍
	if err := validateQuantity(lf, q); err != nil {
		return "", err
	}
	return q.String(), nil
}

// validateQuantity 校验修正后的数量是否符合数量过滤器。
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

// FixOrderText 同时修正价格与数量(限价单):
//   - 价格与数量分别满足各自的过滤器;
//   - 名义价值 价格×数量 不得小于 MinNotional, 不足时自动提高数量
//     (向上取整到 StepSize 的整数倍, 且不超过 MaxQuantity)。
func FixOrderText(si *SymbolInfo, price, quantity string) (string, string, error) {
	if si == nil {
		return price, quantity, errors.New("SymbolInfo 为空, 无法修正订单数据")
	}
	fp, err := FixPriceText(si, price)
	if err != nil {
		return "", "", err
	}
	fq, err := FixQuantityText(si, quantity)
	if err != nil {
		return "", "", err
	}

	// 名义价值校验: 价格×数量 >= MinNotional
	if si.MinNotionalFilter != nil && si.MinNotionalFilter.Notional != "" {
		minNotional := decOrZero(si.MinNotionalFilter.Notional)
		p := decOrZero(fp)
		q := decOrZero(fq)
		if minNotional.IsPositive() && p.IsPositive() && p.Mul(q).LessThan(minNotional) {
			// 所需最小数量 = MinNotional / 价格, 向上取整到 StepSize 的整数倍
			need := minNotional.Div(p) // Div 内部按 DivisionPrecision=16 取精度, 不会报错
			if si.MarketLotSizeFilter != nil {
				lf := si.MarketLotSizeFilter
				if step := decOrZero(lf.StepSize); step.IsPositive() {
					q = need.Div(step).Ceil().Mul(step)
				} else {
					q = need
				}
				// 不能小于最小数量(必要时向上取整, 保持 step 整数倍)
				if lf.MinQuantity != "" {
					if min := decOrZero(lf.MinQuantity); q.LessThan(min) {
						if step := decOrZero(lf.StepSize); step.IsPositive() {
							q = min.Div(step).Ceil().Mul(step)
						} else {
							q = min
						}
					}
				}
				// 不能超过最大数量
				if lf.MaxQuantity != "" {
					if max := decOrZero(lf.MaxQuantity); q.GreaterThan(max) {
						return "", "", fmt.Errorf("数量需提高到 %s 才能满足最小名义价值 %s, 但已超过最大数量 %s", q, si.MinNotionalFilter.Notional, lf.MaxQuantity)
					}
				}
				// 最终校验数量合法性(范围 + step 整数倍)
				if err := validateQuantity(lf, q); err != nil {
					return "", "", err
				}
			} else {
				q = need
			}
			// 最终确认名义价值(提高后必须满足)
			if p.Mul(q).LessThan(minNotional) {
				return "", "", fmt.Errorf("无法满足最小名义价值 %s(价格 %s × 数量 %s)", si.MinNotionalFilter.Notional, fp, q)
			}
			fq = q.String()
		}
	}
	return fp, fq, nil
}

// FixPrice 校验交易对是否存在并修正价格(浮点版, 内部走十进制文本修正)。
// 交易对不在 Exchange.Symbols 列表中时返回错误。
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

// FixQuantity 校验交易对是否存在并修正数量(浮点版, 内部走十进制文本修正)。
// 交易对不在 Exchange.Symbols 列表中时返回错误。
func (e *Exchange) FixQuantity(symbol string, quantity float64) (float64, error) {
	si, ok := e.GetSymbol(symbol)
	if !ok {
		return 0, fmt.Errorf("交易对 %s 不在 Symbols 交易对列表中", symbol)
	}
	fq, err := FixQuantityText(si, strconv.FormatFloat(quantity, 'f', -1, 64))
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(fq, 64)
}

// FixOrder 校验交易对并同时修正价格与数量(浮点版, 含最小名义价值校验)。
// 交易对不在 Exchange.Symbols 列表中时返回错误。
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
