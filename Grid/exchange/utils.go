package exchange

import "main/pkg/unknown"

func ToString(data any) string {
	return unknown.NewUnknown(data).String()
}

func ToInt64(data any) int64 {
	return unknown.NewUnknown(data).Int()
}

func ToFloat64(data any) float64 {
	return unknown.NewUnknown(data).Float()
}
