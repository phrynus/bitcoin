package main

import (
	"context"
	"errors"
	"strings"

	"main/logger"

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

// CalcMarginRatio 通过 WS API 计算多资产模式下的总权益、总可用资金和维持保证金占比。
// 账户信息走 v2/account.status, 持仓明细走 v2/account.position,
// 币种折算率沿用 REST 资产指数(公开接口)。
func (a *Account) CalcMarginRatio(ctx context.Context, symbol string) (*MarginRateResult, error) {
	convMap, err := GetConversionRateMap(ctx, a.Client)
	if err != nil {
		return nil, err
	}
	logger.Debugf("[保证金率] 获取到 %d 条转换率", len(convMap))
	assetIndexMap := buildAssetToIndexMap(convMap)

	acct, err := a.WsApi.AccountStatus(ctx)
	if err != nil {
		return nil, err
	}
	logger.Debugf("[保证金率] 账户资产数=%d 持仓数=%d", len(acct.Assets), len(acct.Positions))

	assetEquity := make(map[string]decimal.Decimal)
	assetAvailable := make(map[string]decimal.Decimal)
	assetMarginAvailable := make(map[string]bool)
	for _, as := range acct.Assets {
		assetEquity[as.Asset] = parseDecimal(as.WalletBalance).Add(parseDecimal(as.UnrealizedProfit))
		assetMarginAvailable[as.Asset] = as.MarginAvailable
		if as.MarginAvailable {
			assetAvailable[as.Asset] = parseDecimal(as.AvailableBalance)
		}
	}

	totalEquity := decimal.Zero
	assetDetails := make([]AssetDetail, 0, len(acct.Assets))
	for _, as := range acct.Assets {
		asset := as.Asset
		equity := assetEquity[asset]
		available := assetAvailable[asset]
		if equity.IsZero() && available.IsZero() {
			continue
		}

		bidRate, askRate := getAssetRates(asset, assetIndexMap, convMap)
		assetDetails = append(assetDetails, AssetDetail{
			Asset:           asset,
			Equity:          equity,
			EquityUSD:       convertAssetToUSD(equity, bidRate, askRate),
			Available:       available,
			AvailableUSD:    convertAssetToUSD(available, bidRate, askRate),
			BidRate:         bidRate,
			AskRate:         askRate,
			MarginAvailable: assetMarginAvailable[asset],
		})
		totalEquity = totalEquity.Add(convertAssetToUSD(equity, bidRate, askRate))
	}
	totalAvailable := parseDecimal(acct.AvailableBalance)

	risks, err := a.WsApi.PositionRisk(ctx, symbol)
	if err != nil {
		return nil, err
	}

	totalMaintMargin := parseDecimal(acct.TotalMaintMargin)
	posDetails := make([]PositionMarginDetail, 0, len(risks))
	assetsWithPosition := make(map[string]bool)

	for _, r := range risks {
		if r == nil {
			continue
		}
		if symbol != "" && r.Symbol != symbol {
			continue
		}
		amt := parseDecimal(r.PositionAmt)
		if amt.IsZero() {
			continue
		}

		markPrice := parseDecimal(r.MarkPrice)
		if !markPrice.IsPositive() {
			markPrice = parseDecimal(r.EntryPrice)
		}
		if !markPrice.IsPositive() {
			a.Log.Warnf("[保证金率] %s 无有效标记价, 跳过该持仓", r.Symbol)
			continue
		}

		marginAsset := r.MarginAsset
		if marginAsset == "" {
			marginAsset = inferMarginAsset(r.Symbol)
		}
		assetsWithPosition[marginAsset] = true

		askRate := decimal.NewFromInt(1)
		if idxSymbol, hasIndex := assetIndexMap[marginAsset]; hasIndex {
			askRate = convMap[idxSymbol].AskRate
		}

		maintMargin := parseDecimal(r.MaintMargin).Mul(askRate)
		notional := amt.Abs().Mul(markPrice).Mul(askRate)
		maintRate := decimal.Zero
		if notional.IsPositive() {
			maintRate = maintMargin.Div(notional)
		}
		unrealizedProfit := parseDecimal(r.UnRealizedProfit)

		posDetails = append(posDetails, PositionMarginDetail{
			Symbol:           r.Symbol,
			Asset:            marginAsset,
			PositionAmt:      amt,
			MarkPrice:        markPrice,
			UnrealizedProfit: unrealizedProfit,
			MaintMarginRate:  maintRate,
			MaintMargin:      maintMargin,
			AssetEquity:      assetEquity[marginAsset],
		})
		logger.Debugf("[保证金率] %s mark=%s amt=%s asset=%s maintMargin=%s",
			r.Symbol, formatDecimalFixed(markPrice, 6), formatDecimalFixed(amt, 4),
			marginAsset, formatDecimalFixed(maintMargin, 2))
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

	// 正常情况下以交易所返回的 TotalMaintMargin(USD 计价)为准;
	// 字段缺失时为回退为各持仓维持保证金之和, 避免误判为 0。
	if totalMaintMargin.IsZero() {
		for _, p := range posDetails {
			totalMaintMargin = totalMaintMargin.Add(p.MaintMargin)
		}
	}

	m := &MarginRateResult{
		MarginRatio:      totalMaintMargin.Div(totalEquity).Mul(decimalHundred),
		TotalEquity:      totalEquity,
		TotalAvailable:   totalAvailable,
		TotalMaintMargin: totalMaintMargin,
		Assets:           assetDetails,
		Positions:        posDetails,
	}

	logger.Debugf("[保证金率] 最终结果: ratio=%s%% equity=%s available=%s maintMargin=%s positions=%d",
		formatDecimalFixed(m.MarginRatio, 4),
		formatDecimalFixed(m.TotalEquity, 2),
		formatDecimalFixed(m.TotalAvailable, 2),
		formatDecimalFixed(m.TotalMaintMargin, 2),
		len(m.Positions),
	)
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
