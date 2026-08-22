package exchange

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/shopspring/decimal"
)

//

//

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

func (e *Exchange) FixPrice(symbol string, price float64) (float64, error) {
	si, ok := e.getSymbol(symbol)
	if !ok {
		return 0, fmt.Errorf("交易对 %s 不在 Symbols 交易对列表中", symbol)
	}
	fp, err := fixPriceText(si, strconv.FormatFloat(price, 'f', -1, 64))
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(fp, 64)
}

func (e *Exchange) FixQuantity(symbol string, quantity float64) (float64, error) {
	si, ok := e.getSymbol(symbol)
	if !ok {
		return 0, fmt.Errorf("交易对 %s 不在 Symbols 交易对列表中", symbol)
	}
	fq, err := fixQuantityText(si, strconv.FormatFloat(quantity, 'f', -1, 64))
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(fq, 64)
}

func (e *Exchange) FixOrder(symbol string, price, quantity float64) (float64, float64, error) {
	si, ok := e.getSymbol(symbol)
	if !ok {
		return 0, 0, fmt.Errorf("交易对 %s 不在 Symbols 交易对列表中", symbol)
	}
	fp, fq, err := fixOrderText(si,
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
