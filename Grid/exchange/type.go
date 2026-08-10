package exchange

type SymbolInfo struct {
	Symbol                string               `json:"symbol"`                // 交易对符号
	Pair                  string               `json:"pair"`                  // 交易对
	ContractType          string               `json:"contractType"`          // 合约类型
	Status                string               `json:"status"`                // 交易对状态
	PricePrecision        int64                `json:"pricePrecision"`        // 价格精度
	QuantityPrecision     int64                `json:"quantityPrecision"`     // 数量精度
	BaseAssetPrecision    int64                `json:"baseAssetPrecision"`    // 基础资产精度
	QuotePrecision        int64                `json:"quotePrecision"`        // 报价精度
	UnderlyingType        string               `json:"underlyingType"`        // 底层类型
	QuoteAsset            string               `json:"quoteAsset"`            // 报价资产
	BaseAsset             string               `json:"baseAsset"`             // 基础资产
	MaintMarginPercent    string               `json:"maintMarginPercent"`    // 维持保证金比例
	RequiredMarginPercent string               `json:"requiredMarginPercent"` // 要求保证金比例
	LiquidationFee        string               `json:"liquidationFee"`        // 强平费率
	MarketTakeBound       string               `json:"marketTakeBound"`       // 市价单限制
	PriceFilter           *PriceFilter         `json:"priceFilter"`           // 价格过滤器
	LotSizeFilter         *LotSizeFilter       `json:"lotSizeFilter"`         // 数量过滤器
	MarketLotSizeFilter   *MarketLotSizeFilter `json:"marketLotSizeFilter"`   // 市价单数量过滤器
	MaxNumOrdersFilter    *MaxNumOrdersFilter  `json:"maxNumOrdersFilter"`    // 最大订单数过滤器
	MinNotionalFilter     *MinNotionalFilter   `json:"minNotionalFilter"`     // 最小名义价值过滤器
	PercentPriceFilter    *PercentPriceFilter  `json:"percentPriceFilter"`    // 百分比价格过滤器
}

// PriceFilter 价格过滤器
type PriceFilter struct {
	MaxPrice  string  `json:"maxPrice"`  // 价格上限
	MinPrice  string  `json:"minPrice"`  // 价格下限
	TickSize  string  `json:"tickSize"`  // 订单最小价格间隔
	MaxPriceF float64 `json:"maxPriceF"` // 价格上限(float64)
	MinPriceF float64 `json:"minPriceF"` // 价格下限(float64)
	TickSizeF float64 `json:"tickSizeF"` // 订单最小价格间隔(float64)
}

// LotSizeFilter 数量过滤器
type LotSizeFilter struct {
	MaxQuantity  string  `json:"maxQty"`    // 最大数量
	MinQuantity  string  `json:"minQty"`    // 最小数量
	StepSize     string  `json:"stepSize"`  // 订单最小数量间隔
	MaxQuantityF float64 `json:"maxQtyF"`   // 最大数量(float64)
	MinQuantityF float64 `json:"minQtyF"`   // 最小数量(float64)
	StepSizeF    float64 `json:"stepSizeF"` // 订单最小数量间隔(float64)
}

// MarketLotSizeFilter 市价单数量过滤器
type MarketLotSizeFilter struct {
	MaxQuantity  string  `json:"maxQty"`    // 最大数量
	MinQuantity  string  `json:"minQty"`    // 最小数量
	StepSize     string  `json:"stepSize"`  // 允许的步进值
	MaxQuantityF float64 `json:"maxQtyF"`   // 最大数量(float64)
	MinQuantityF float64 `json:"minQtyF"`   // 最小数量(float64)
	StepSizeF    float64 `json:"stepSizeF"` // 允许的步进值(float64)
}

// MaxNumOrdersFilter 最大订单数过滤器
type MaxNumOrdersFilter struct {
	Limit    int64 `json:"limit"`    // 订单限制数量
	LimitI64 int64 `json:"limitI64"` // 订单限制数量(int64)
}

// MinNotionalFilter 最小名义价值过滤器
type MinNotionalFilter struct {
	Notional  string  `json:"notional"`  // 最小名义价值
	NotionalF float64 `json:"notionalF"` // 最小名义价值(float64)
}

// PercentPriceFilter 百分比价格过滤器
type PercentPriceFilter struct {
	MultiplierDecimal  string  `json:"multiplierDecimal"`  // 乘数小数位数
	MultiplierUp       string  `json:"multiplierUp"`       // 价格上限百分比
	MultiplierDown     string  `json:"multiplierDown"`     // 价格下限百分比
	MultiplierDecimalF float64 `json:"multiplierDecimalF"` // 乘数小数位数(float64)
	MultiplierUpF      float64 `json:"multiplierUpF"`      // 价格上限百分比(float64)
	MultiplierDownF    float64 `json:"multiplierDownF"`    // 价格下限百分比(float64)
}
