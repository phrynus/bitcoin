package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/adshao/go-binance/v2/futures"
)

type point struct {
	Ts int64
	U  float64
	C  float64
}

type FundingRow [3]float64

type SymbolReport struct {
	S string       `json:"s"`
	P []FundingRow `json:"p"`
}

type Report struct {
	B string         `json:"b"`
	E string         `json:"e"`
	N int            `json:"n"`
	S []SymbolReport `json:"s"`
	F []string       `json:"f,omitempty"`
}

func fetchFundingRate(client *futures.Client, symbol string, start, end int64) ([]*futures.FundingRate, error) {
	var all []*futures.FundingRate
	for next := start; ; {
		list, err := client.NewFundingRateService().
			Symbol(symbol).
			StartTime(next).
			EndTime(end).
			Limit(1000).
			Do(context.Background())
		if err != nil {
			return nil, fmt.Errorf("请求 %s 失败: %w", symbol, err)
		}
		if len(list) == 0 {
			return all, nil
		}

		all = append(all, list...)
		if len(list) < 1000 {
			return all, nil
		}
		next = list[len(list)-1].FundingTime + 1
	}
}

func roundRate(v float64) float64 {
	return math.Round(v*1e8) / 1e8
}

func mergeRates(usdt, usdc []*futures.FundingRate) []point {
	merged := make(map[int64]*point, len(usdt)+len(usdc))

	for _, item := range usdt {
		rate, _ := strconv.ParseFloat(item.FundingRate, 64)
		merged[item.FundingTime] = &point{
			Ts: item.FundingTime,
			U:  roundRate(-rate),
		}
	}

	for _, item := range usdc {
		rate, _ := strconv.ParseFloat(item.FundingRate, 64)
		row, ok := merged[item.FundingTime]
		if !ok {
			row = &point{Ts: item.FundingTime}
			merged[item.FundingTime] = row
		}
		row.C = roundRate(rate)
	}

	list := make([]point, 0, len(merged))
	for _, item := range merged {
		if item.U == 0 && item.C == 0 {
			continue
		}
		list = append(list, *item)
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].Ts < list[j].Ts
	})
	return list
}

func buildSymbol(sym string, usdt, usdc []*futures.FundingRate) SymbolReport {
	rows := mergeRates(usdt, usdc)
	data := make([]FundingRow, 0, len(rows))

	for _, item := range rows {
		data = append(data, FundingRow{
			float64(item.Ts),
			item.U,
			item.C,
		})
	}

	return SymbolReport{
		S: sym,
		P: data,
	}
}

func sumRows(rows []FundingRow) float64 {
	var total float64
	for _, row := range rows {
		total += row[1] + row[2]
	}
	return roundRate(total)
}

func buildReport(cfg Config) (Report, float64) {
	now := time.Now()
	end := now.UnixMilli()
	start := now.AddDate(0, 0, -cfg.Days).UnixMilli()
	startDate := time.UnixMilli(start).UTC().Format("2006-01-02")
	endDate := time.UnixMilli(end).UTC().Format("2006-01-02")

	fmt.Printf("开始拉取资金费率数据（%s ~ %s，共 %d 天）\n", startDate, endDate, cfg.Days)

	client := futures.NewClient("", "")
	list := make([]SymbolReport, 0, len(cfg.Symbols))
	failed := make([]string, 0)
	var total float64

	for _, sym := range cfg.Symbols {
		fmt.Printf("[%s] 请求中...\n", sym)

		usdt, errUSDT := fetchFundingRate(client, sym+"USDT", start, end)
		if errUSDT != nil {
			fmt.Printf("[%s] USDT-M 失败: %v\n", sym, errUSDT)
		}
		time.Sleep(1 * time.Second)

		usdc, errUSDC := fetchFundingRate(client, sym+"USDC", start, end)
		if errUSDC != nil {
			fmt.Printf("[%s] USDC-M 失败: %v\n", sym, errUSDC)
		}
		time.Sleep(2 * time.Second)

		if errUSDT != nil && errUSDC != nil {
			failed = append(failed, sym)
			fmt.Printf("[%s] 两个市场都失败，跳过\n", sym)
			continue
		}
		if usdt == nil {
			usdt = []*futures.FundingRate{}
		}
		if usdc == nil {
			usdc = []*futures.FundingRate{}
		}

		item := buildSymbol(sym, usdt, usdc)
		sum := sumRows(item.P)
		list = append(list, item)
		total += sum

		fmt.Printf("[%s] 完成: %d 个节点，合计 %.4f%%\n", sym, len(item.P), sum*100)

	}

	return Report{
		B: startDate,
		E: endDate,
		N: cfg.Days,
		S: list,
		F: failed,
	}, total
}
