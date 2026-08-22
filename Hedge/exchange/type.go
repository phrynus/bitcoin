package exchange

type SymbolInfo struct {
	Symbol                string               `json:"symbol"`
	Pair                  string               `json:"pair"`
	ContractType          string               `json:"contractType"`
	Status                string               `json:"status"`
	PricePrecision        int64                `json:"pricePrecision"`
	QuantityPrecision     int64                `json:"quantityPrecision"`
	BaseAssetPrecision    int64                `json:"baseAssetPrecision"`
	QuotePrecision        int64                `json:"quotePrecision"`
	UnderlyingType        string               `json:"underlyingType"`
	QuoteAsset            string               `json:"quoteAsset"`
	BaseAsset             string               `json:"baseAsset"`
	MaintMarginPercent    string               `json:"maintMarginPercent"`
	RequiredMarginPercent string               `json:"requiredMarginPercent"`
	LiquidationFee        string               `json:"liquidationFee"`
	MarketTakeBound       string               `json:"marketTakeBound"`
	PriceFilter           *PriceFilter         `json:"priceFilter"`
	MarketLotSizeFilter   *MarketLotSizeFilter `json:"marketLotSizeFilter"`
	MinNotionalFilter     *MinNotionalFilter   `json:"minNotionalFilter"`
}

type PriceFilter struct {
	MaxPrice  string  `json:"maxPrice"`
	MinPrice  string  `json:"minPrice"`
	TickSize  string  `json:"tickSize"`
	MaxPriceF float64 `json:"maxPriceF"`
	MinPriceF float64 `json:"minPriceF"`
	TickSizeF float64 `json:"tickSizeF"`
}

type MarketLotSizeFilter struct {
	MaxQuantity  string  `json:"maxQty"`
	MinQuantity  string  `json:"minQty"`
	StepSize     string  `json:"stepSize"`
	MaxQuantityF float64 `json:"maxQtyF"`
	MinQuantityF float64 `json:"minQtyF"`
	StepSizeF    float64 `json:"stepSizeF"`
}

type MinNotionalFilter struct {
	Notional  string  `json:"notional"`
	NotionalF float64 `json:"notionalF"`
}
