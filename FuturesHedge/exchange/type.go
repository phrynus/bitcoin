package exchange

// {
//     "exchangeFilters": [],
//     "rateLimits": [ // API访问的限制
//         {
//             "interval": "MINUTE", // 按照分钟计算
//             "intervalNum": 1, // 按照1分钟计算
//             "limit": 2400, // 上限次数
//             "rateLimitType": "REQUEST_WEIGHT" // 按照访问权重来计算
//         },
//         {
//             "interval": "MINUTE",
//             "intervalNum": 1,
//             "limit": 1200,
//             "rateLimitType": "ORDERS" // 按照订单数量来计算
//         }
//     ],
//     "serverTime": 1565613908500, // 请忽略。如果需要获取当前系统时间，请查询接口 “GET /fapi/v1/time”
//     "assets": [ // 资产信息
//         {
//             "asset": "BTC",
//             "marginAvailable": true, // 是否可用作保证金
//             "autoAssetExchange": "-0.10" // 保证金资产自动兑换阈值
//         },
//         {
//             "asset": "USDT",
//             "marginAvailable": true, // 是否可用作保证金
//             "autoAssetExchange": "0" // 保证金资产自动兑换阈值
//         },
//         {
//             "asset": "BNB",
//             "marginAvailable": false, // 是否可用作保证金
//             "autoAssetExchange": null // 保证金资产自动兑换阈值
//         }
//     ],
//     "symbols": [ // 交易对信息
//         {
//             "symbol": "BLZUSDT", // 交易对
//             "pair": "BLZUSDT", // 标的交易对
//             "contractType": "PERPETUAL", // 合约类型
//             "deliveryDate": 4133404800000, // 交割日期
//             "onboardDate": 1598252400000, // 上线日期
//             "status": "TRADING", // 交易对状态
//             "maintMarginPercent": "2.5000", // 请忽略
//             "requiredMarginPercent": "5.0000", // 请忽略
//             "baseAsset": "BLZ", // 标的资产
//             "quoteAsset": "USDT", // 报价资产
//             "marginAsset": "USDT", // 保证金资产
//             "pricePrecision": 5, // 价格小数点位数(仅作为系统精度使用，注意同tickSize 区分）
//             "quantityPrecision": 0, // 数量小数点位数(仅作为系统精度使用，注意同stepSize 区分）
//             "baseAssetPrecision": 8, // 标的资产精度
//             "quotePrecision": 8, // 报价资产精度
//             "underlyingType": "COIN",
//             "underlyingSubType": [
//                 "STORAGE"
//             ],
//             "settlePlan": 0,
//             "triggerProtect": "0.15", // 开启"priceProtect"的条件订单的触发阈值
//             "filters": [
//                 {
//                     "filterType": "PRICE_FILTER", // 价格限制
//                     "maxPrice": "300", // 价格上限, 最大价格
//                     "minPrice": "0.0001", // 价格下限, 最小价格
//                     "tickSize": "0.0001" // 订单最小价格间隔
//                 },
//                 {
//                     "filterType": "LOT_SIZE", // 数量限制
//                     "maxQty": "10000000", // 数量上限, 最大数量
//                     "minQty": "1", // 数量下限, 最小数量
//                     "stepSize": "1" // 订单最小数量间隔
//                 },
//                 {
//                     "filterType": "MARKET_LOT_SIZE", // 市价订单数量限制
//                     "maxQty": "590119", // 数量上限, 最大数量
//                     "minQty": "1", // 数量下限, 最小数量
//                     "stepSize": "1" // 允许的步进值
//                 },
//                 {
//                     "filterType": "MAX_NUM_ORDERS", // 最多订单数限制
//                     "limit": 200
//                 },
//                 {
//                     "filterType": "MIN_NOTIONAL", // 最小名义价值
//                     "notional": "5.0",
//                 },
//                 {
//                     "filterType": "PERCENT_PRICE", // 价格比限制
//                     "multiplierUp": "1.1500", // 价格上限百分比
//                     "multiplierDown": "0.8500", // 价格下限百分比
//                     "multiplierDecimal": "4"
//                 }
//             ],
//             "orderTypes": [ // 订单类型
//                 "LIMIT", // 限价单
//                 "MARKET", // 市价单
//                 "STOP", // 止损单
//                 "STOP_MARKET", // 止损市价单
//                 "TAKE_PROFIT", // 止盈单
//                 "TAKE_PROFIT_MARKET", // 止盈暑市价单
//                 "TRAILING_STOP_MARKET" // 跟踪止损市价单
//             ],
//             "timeInForce": [ // 有效方式
//                 "GTC", // 成交为止, 一直有效
//                 "IOC", // 无法立即成交(吃单)的部分就撤销
//                 "FOK", // 无法全部立即成交就撤销
//                 "GTX" // 无法成为挂单方就撤销
//             ],
//             "liquidationFee": "0.010000", // 强平费率
//             "marketTakeBound": "0.30", // 市价吃单(相对于标记价格)允许可造成的最大价格偏离比例
//         }
//     ],
//     "timezone": "UTC" // 服务器所用的时间区域
// }

// RateLimitType 频率限制类型
type RateLimitType string

const (
	RateLimitTypeRequestWeight RateLimitType = "REQUEST_WEIGHT" // 按请求权重限制
	RateLimitTypeOrders        RateLimitType = "ORDERS"         // 按订单数限制
)

// RateLimitInterval 频率限制的时间间隔
type RateLimitInterval string

const (
	RateLimitIntervalMinute RateLimitInterval = "MINUTE" // 分钟
	RateLimitIntervalSecond RateLimitInterval = "SECOND" // 秒
	RateLimitIntervalDay    RateLimitInterval = "DAY"    // 天
)

// RateLimit API 访问频率限制
type RateLimit struct {
	Interval      RateLimitInterval `json:"interval"`      // 限制周期（分钟/秒/天）
	IntervalNum   int               `json:"intervalNum"`   // 周期内限制次数
	Limit         int               `json:"limit"`         // 周期内最大请求数
	RateLimitType RateLimitType     `json:"rateLimitType"` // 限制类型（权重/订单数）
}

// Asset 币种资产信息
type Asset struct {
	Asset             string  `json:"asset"`             // 资产名称（如 USDT）
	MarginAvailable   bool    `json:"marginAvailable"`   // 是否可用于杠杆交易
	AutoAssetExchange float64 `json:"autoAssetExchange"` // 自动兑换阈值
}

// FilterType 交易对过滤器类型
type FilterType string

const (
	FilterTypePriceFilter      FilterType = "PRICE_FILTER"        // 价格过滤器
	FilterTypeLotSize          FilterType = "LOT_SIZE"            // 数量过滤器（限价单）
	FilterTypeMarketLotSize    FilterType = "MARKET_LOT_SIZE"     // 数量过滤器（市价单）
	FilterTypeMaxNumOrders     FilterType = "MAX_NUM_ORDERS"      // 最大挂单数限制
	FilterTypeMinNotional      FilterType = "MIN_NOTIONAL"        // 最小名义价值限制
	FilterTypePercentPrice     FilterType = "PERCENT_PRICE"       // 价格百分比限制
	FilterTypeMaxNumAlgoOrders FilterType = "MAX_NUM_ALGO_ORDERS" // 最大算法订单数限制
)

// Filters 交易对的所有过滤器
type Filters struct {
	PriceFilter            PriceFilter            `json:"priceFilter"`
	LotSizeFilter          LotSizeFilter          `json:"lotSizeFilter"`
	MarketLotSizeFilter    MarketLotSizeFilter    `json:"marketLotSizeFilter"`
	MaxNumOrdersFilter     MaxNumOrdersFilter     `json:"maxNumOrdersFilter"`
	MinNotionalFilter      MinNotionalFilter      `json:"minNotionalFilter"`
	PercentPriceFilter     PercentPriceFilter     `json:"percentPriceFilter"`
	MaxNumAlgoOrdersFilter MaxNumAlgoOrdersFilter `json:"maxNumAlgoOrdersFilter"`
}

// PriceFilter 价格过滤器（挂单价格约束）
type PriceFilter struct {
	FilterType FilterType `json:"filterType"` // 过滤器类型
	MaxPrice   float64    `json:"maxPrice"`   // 最高价格
	MinPrice   float64    `json:"minPrice"`   // 最低价格
	TickSize   float64    `json:"tickSize"`   // 价格步进单位（精度）
}

// LotSizeFilter 数量过滤器（限价单）
type LotSizeFilter struct {
	FilterType FilterType `json:"filterType"` // 过滤器类型
	MaxQty     float64    `json:"maxQty"`     // 最大下单数量
	MinQty     float64    `json:"minQty"`     // 最小下单数量
	StepSize   float64    `json:"stepSize"`   // 数量步进单位（精度）
}

// MarketLotSizeFilter 数量过滤器（市价单）
type MarketLotSizeFilter struct {
	FilterType FilterType `json:"filterType"` // 过滤器类型
	MaxQty     float64    `json:"maxQty"`     // 最大下单数量
	MinQty     float64    `json:"minQty"`     // 最小下单数量
	StepSize   float64    `json:"stepSize"`   // 数量步进单位
}

// MaxNumOrdersFilter 最大挂单数限制
type MaxNumOrdersFilter struct {
	FilterType FilterType `json:"filterType"` // 过滤器类型
	Limit      int        `json:"limit"`      // 最大挂单数量
}

// MinNotionalFilter 最小名义价值限制（订单金额门槛）
type MinNotionalFilter struct {
	FilterType FilterType `json:"filterType"` // 过滤器类型
	Notional   float64    `json:"notional"`   // 最小名义价值
}

// PercentPriceFilter 价格偏离限制（防止异常价格）
type PercentPriceFilter struct {
	FilterType        FilterType `json:"filterType"`        // 过滤器类型
	MultiplierUp      float64    `json:"multiplierUp"`      // 价格上限倍数
	MultiplierDown    float64    `json:"multiplierDown"`    // 价格下限倍数
	MultiplierDecimal float64    `json:"multiplierDecimal"` // 倍数精度
}

// MaxNumAlgoOrdersFilter 最大算法订单数限制（止盈止损等）
type MaxNumAlgoOrdersFilter struct {
	FilterType FilterType `json:"filterType"` // 过滤器类型
	Limit      int        `json:"limit"`      // 最大算法订单数量
}

// Symbol 交易对信息
type Symbol struct {
	Symbol                string              `json:"symbol"`                // 交易对符号（如 BTCUSDT）
	Pair                  string              `json:"pair"`                  // 交易对（如 BTCUSDT）
	ContractType          string              `json:"contractType"`          // 合约类型（PERPETUAL：永续）
	DeliveryDate          int64               `json:"deliveryDate"`          // 合约交割日期（时间戳，毫秒）
	OnboardDate           int64               `json:"onboardDate"`           // 上线日期（时间戳，毫秒）
	Status                string              `json:"status"`                // 交易对状态（TRADING：可交易）
	MaintMarginPercent    float64             `json:"maintMarginPercent"`    // 维持保证金率
	RequiredMarginPercent float64             `json:"requiredMarginPercent"` // 起始保证金率
	BaseAsset             string              `json:"baseAsset"`             // 基础资产（BTC）
	QuoteAsset            string              `json:"quoteAsset"`            // 计价资产（USDT）
	MarginAsset           string              `json:"marginAsset"`           // 保证金资产
	PricePrecision        int                 `json:"pricePrecision"`        // 价格精度（小数位数）
	QuantityPrecision     int                 `json:"quantityPrecision"`     // 数量精度
	BaseAssetPrecision    int                 `json:"baseAssetPrecision"`    // 基础资产精度
	QuotePrecision        int                 `json:"quotePrecision"`        // 计价资产精度
	UnderlyingType        string              `json:"underlyingType"`        // 标的资产类型（如 COIN、USDT）
	UnderlyingSubType     []string            `json:"underlyingSubType"`     // 标的资产子类型（如 BTC、ETH）
	SettlePlan            int                 `json:"settlePlan"`            // 结算计划
	TriggerProtect        float64             `json:"triggerProtect"`        // 触发保护阈值
	FiltersRaw            []map[string]string `json:"filters"`               // 原始过滤器数据（JSON 反序列化用）
	Filters               Filters             `json:"-"`                     // 解析后的过滤器结构
	OrderTypes            []string            `json:"orderTypes"`            // 支持的订单类型
	TimeInForce           []string            `json:"timeInForce"`           // 支持的时效类型（GTC、IOC、FOK）
	LiquidationFee        float64             `json:"liquidationFee"`        // 强平费率
	MarketTakeBound       float64             `json:"marketTakeBound"`       // 市价吃单上限
}

// ExchangeInfo 交易所完整信息响应
type ExchangeInfo struct {
	ExchangeFilters []any              `json:"exchangeFilters"` // 交易所过滤器（预留）
	RateLimits      []RateLimit        `json:"rateLimits"`      // API 频率限制规则
	ServerTime      int64              `json:"serverTime"`      // 服务器当前时间（毫秒）
	Assets          []Asset            `json:"assets"`          // 资产列表（保证金币种信息）
	Symbols_        []Symbol           `json:"symbols"`         // 交易对列表
	Symbols         map[string]*Symbol `json:"-"`               // 交易对列表（symbol 为 key）
	Timezone        string             `json:"timezone"`        // 服务器时区
}
