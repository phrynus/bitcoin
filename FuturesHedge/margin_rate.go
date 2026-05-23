package main

import (
	"context"
	"errors"
	"strings"

	"github.com/adshao/go-binance/v2/futures"
	"github.com/shopspring/decimal"
)

type ConversionRate struct {
	BidRate decimal.Decimal
	AskRate decimal.Decimal
}

type PositionMarginDetail struct {
	Symbol           string          `json:"symbol"`
	Asset            string          `json:"asset"`
	PositionAmt      decimal.Decimal `json:"positionAmt"`
	MarkPrice        decimal.Decimal `json:"markPrice"`
	UnrealizedProfit decimal.Decimal `json:"unrealizedProfit"`
	MaintMarginRate  decimal.Decimal `json:"maintMarginRate"`
	MaintMargin      decimal.Decimal `json:"maintMargin"`
	AssetEquity      decimal.Decimal `json:"assetEquity"`
}

type MarginRateResult struct {
	MarginRatio      decimal.Decimal        `json:"marginRatio"`      // 保证金率
	TotalEquity      decimal.Decimal        `json:"totalEquity"`      // 总权益
	TotalAvailable   decimal.Decimal        `json:"totalAvailable"`   // 总可用资金
	TotalMaintMargin decimal.Decimal        `json:"totalMaintMargin"` // 总维持保证金
	Assets           []AssetDetail          `json:"assets"`
	Positions        []PositionMarginDetail `json:"positions"`
}

type AssetDetail struct {
	Asset           string          `json:"asset"`
	Equity          decimal.Decimal `json:"equity"`
	EquityUSD       decimal.Decimal `json:"equityUsd"`
	Available       decimal.Decimal `json:"available"`
	AvailableUSD    decimal.Decimal `json:"availableUsd"`
	BidRate         decimal.Decimal `json:"bidRate"`
	AskRate         decimal.Decimal `json:"askRate"`
	HasPosition     bool            `json:"hasPosition"`
	MarginAvailable bool            `json:"marginAvailable"`
}

// CalcMarginRatio 计算多资产模式下的总权益、总可用资金和维持保证金占比。
func CalcMarginRatio(ctx context.Context, client *futures.Client, symbol string) (*MarginRateResult, error) {
	convMap, err := GetConversionRateMap(ctx, client)
	if err != nil {
		return nil, err
	}

	account, err := client.NewGetAccountV3Service().Do(ctx)
	if err != nil {
		return nil, err
	}

	assetEquity := make(map[string]decimal.Decimal)
	assetAvailable := make(map[string]decimal.Decimal)
	assetMarginAvailable := make(map[string]bool)
	for _, a := range account.Assets {
		assetEquity[a.Asset] = parseDecimal(a.WalletBalance).Add(parseDecimal(a.UnrealizedProfit))
		assetMarginAvailable[a.Asset] = a.MarginAvailable
		if a.MarginAvailable {
			assetAvailable[a.Asset] = parseDecimal(a.AvailableBalance)
		}
	}

	assetIndexMap := buildAssetToIndexMap(convMap)

	totalEquity := decimal.Zero
	totalAvailable := decimal.Zero
	assetDetails := make([]AssetDetail, 0, len(account.Assets))
	for _, a := range account.Assets {
		asset := a.Asset
		equity := assetEquity[asset]
		available := assetAvailable[asset]
		if equity.IsZero() && available.IsZero() {
			continue
		}

		bidRate, askRate := getAssetRates(asset, assetIndexMap, convMap)
		equityUSD := convertAssetToUSD(equity, bidRate, askRate)
		availableUSD := convertAssetToUSD(available, bidRate, askRate)
		assetDetails = append(assetDetails, AssetDetail{
			Asset:           asset,
			Equity:          equity,
			EquityUSD:       equityUSD,
			Available:       available,
			AvailableUSD:    availableUSD,
			BidRate:         bidRate,
			AskRate:         askRate,
			MarginAvailable: assetMarginAvailable[asset],
		})
		totalEquity = totalEquity.Add(equityUSD)

	}
	// totalAvailable = account.AvailableBalance
	totalAvailable = parseDecimal(account.AvailableBalance)

	var positions []*futures.AccountPositionV3
	if symbol != "" {
		for _, p := range account.Positions {
			if p.Symbol == symbol {
				positions = append(positions, p)
			}
		}
	} else {
		positions = account.Positions
	}

	totalMaintMargin := decimal.Zero
	posDetails := make([]PositionMarginDetail, 0, len(positions))
	assetsWithPosition := make(map[string]bool)

	for _, pos := range positions {
		amt := parseDecimal(pos.PositionAmt)
		if amt.IsZero() {
			continue
		}

		markPrice, err := GetMarkPrice(ctx, client, pos.Symbol)
		if err != nil {
			return nil, err
		}

		marginAsset := inferMarginAsset(pos.Symbol)
		assetsWithPosition[marginAsset] = true

		askRate := decimal.NewFromInt(1)
		if idxSymbol, hasIndex := assetIndexMap[marginAsset]; hasIndex {
			askRate = convMap[idxSymbol].AskRate
		}

		maintRate, err := GetMaintMarginRate(ctx, client, pos.Symbol, markPrice)
		if err != nil {
			return nil, err
		}

		maintMargin := amt.Abs().Mul(markPrice).Mul(maintRate).Mul(askRate)
		unrealizedProfit := parseDecimal(pos.UnrealizedProfit)

		posDetails = append(posDetails, PositionMarginDetail{
			Symbol:           pos.Symbol,
			Asset:            marginAsset,
			PositionAmt:      amt,
			MarkPrice:        markPrice,
			UnrealizedProfit: unrealizedProfit,
			MaintMarginRate:  maintRate,
			MaintMargin:      maintMargin,
			AssetEquity:      assetEquity[marginAsset],
		})
		totalMaintMargin = totalMaintMargin.Add(maintMargin)
	}

	for i := range assetDetails {
		assetDetails[i].HasPosition = assetsWithPosition[assetDetails[i].Asset]
	}

	if len(posDetails) == 0 {
		return &MarginRateResult{
			MarginRatio:      decimal.Zero,
			TotalEquity:      totalEquity,
			TotalAvailable:   totalAvailable,
			TotalMaintMargin: decimal.Zero,
			Assets:           assetDetails,
			Positions:        nil,
		}, nil
	}

	if totalEquity.LessThanOrEqual(decimal.Zero) {
		return nil, errors.New("total equity is zero or negative, account may be liquidated")
	}

	m := &MarginRateResult{
		MarginRatio:      totalMaintMargin.Div(totalEquity).Mul(decimalHundred),
		TotalEquity:      totalEquity,
		TotalAvailable:   totalAvailable,
		TotalMaintMargin: totalMaintMargin,
		Assets:           assetDetails,
		Positions:        posDetails,
	}

	return m, nil
}

func GetConversionRateMap(ctx context.Context, client *futures.Client) (map[string]ConversionRate, error) {
	result, err := client.NewAssetIndexService().Do(ctx)
	if err != nil {
		return nil, err
	}

	m := make(map[string]ConversionRate, len(result))
	for _, idx := range result {
		m[idx.Symbol] = ConversionRate{
			BidRate: parseDecimal(idx.BidRate),
			AskRate: parseDecimal(idx.AskRate),
		}
	}
	return m, nil
}

func buildAssetToIndexMap(convMap map[string]ConversionRate) map[string]string {
	m := make(map[string]string, len(convMap))
	for idxSymbol := range convMap {
		asset := strings.TrimSuffix(idxSymbol, "USD")
		asset = strings.TrimSuffix(asset, "USD")
		if _, exists := m[asset]; !exists {
			m[asset] = idxSymbol
		}
	}
	return m
}

// getAssetRates 获取某个保证金币种折算 USD 时使用的买一和卖一价格。
func getAssetRates(asset string, assetIndexMap map[string]string, convMap map[string]ConversionRate) (decimal.Decimal, decimal.Decimal) {
	bidRate := decimal.NewFromInt(1)
	askRate := decimal.NewFromInt(1)
	if idxSymbol, hasIndex := assetIndexMap[asset]; hasIndex {
		bidRate = convMap[idxSymbol].BidRate
		askRate = convMap[idxSymbol].AskRate
	}
	return bidRate, askRate
}

// convertAssetToUSD 使用更保守的一侧价格把币种数量折算成 USD。
func convertAssetToUSD(amount, bidRate, askRate decimal.Decimal) decimal.Decimal {
	return decimalMin(amount.Mul(bidRate), amount.Mul(askRate))
}

func GetMarkPrice(ctx context.Context, client *futures.Client, symbol string) (decimal.Decimal, error) {
	prices, err := client.NewPremiumIndexService().Symbol(symbol).Do(ctx)
	if err != nil {
		return decimal.Zero, err
	}
	if len(prices) == 0 {
		return decimal.Zero, errors.New("mark price not found")
	}
	return parseDecimal(prices[0].MarkPrice), nil
}

func GetMaintMarginRate(ctx context.Context, client *futures.Client, symbol string, markPrice decimal.Decimal) (decimal.Decimal, error) {
	brackets, err := client.NewGetLeverageBracketService().Symbol(symbol).Do(ctx)
	if err == nil && len(brackets) > 0 && len(brackets[0].Brackets) > 0 {
		notional := markPrice.Abs()
		for _, b := range brackets[0].Brackets {
			floor := decimalFromFloat(b.NotionalFloor)
			cap := decimalFromFloat(b.NotionalCap)
			if notional.GreaterThanOrEqual(floor) && notional.LessThan(cap) {
				return decimalFromFloat(b.MaintMarginRatio), nil
			}
		}

		last := brackets[0].Brackets[len(brackets[0].Brackets)-1]
		return decimalFromFloat(last.MaintMarginRatio), nil
	}

	sym := ExchangeInfo.Get(symbol)
	if sym == nil {
		return decimal.Zero, errors.New("symbol not found in exchange info")
	}
	return decimalFromFloat(sym.MaintMarginPercent).Div(decimalHundred), nil
}

func inferMarginAsset(symbol string) string {
	if strings.HasSuffix(symbol, "USDT") {
		return "USDT"
	}
	if strings.HasSuffix(symbol, "USDC") {
		return "USDC"
	}
	if strings.HasSuffix(symbol, "USD") {
		return "USD"
	}
	return "USDT"
}
