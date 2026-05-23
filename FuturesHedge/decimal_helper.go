package main

import (
	"strings"

	"github.com/shopspring/decimal"
)

var (
	decimalHundred        = decimal.NewFromInt(100)
	balanceEqualThreshold = decimal.RequireFromString("0.00000001")
)

func decimalFromFloat(v float64) decimal.Decimal {
	return decimal.NewFromFloat(v)
}

func parseDecimal(s string) decimal.Decimal {
	v, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero
	}
	return v
}

func decimalMin(a, b decimal.Decimal) decimal.Decimal {
	if a.LessThan(b) {
		return a
	}
	return b
}

func decimalMax(a, b decimal.Decimal) decimal.Decimal {
	if a.GreaterThan(b) {
		return a
	}
	return b
}

func decimalPlaces(v decimal.Decimal) int32 {
	if exp := v.Exponent(); exp < 0 {
		return -exp
	}
	return 0
}

func formatDecimal(v decimal.Decimal) string {
	return trimDecimalString(v.String())
}

func formatDecimalFixed(v decimal.Decimal, places int32) string {
	return trimDecimalString(v.StringFixed(places))
}

func trimDecimalString(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}
