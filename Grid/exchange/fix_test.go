package exchange

import (
	"strings"
	"testing"

	"github.com/adshao/go-binance/v2/futures"
)

// testSymbolInfo 构造一个带过滤器的 BTC 测试交易对。
func testSymbolInfo() *SymbolInfo {
	return &SymbolInfo{
		Symbol: "BTCUSDT",
		PriceFilter: &PriceFilter{
			MinPrice: "6000", MaxPrice: "1000000", TickSize: "0.1",
			MinPriceF: 6000, MaxPriceF: 1000000, TickSizeF: 0.1,
		},
		MarketLotSizeFilter: &MarketLotSizeFilter{
			MinQuantity: "0.001", MaxQuantity: "1000", StepSize: "0.001",
			MinQuantityF: 0.001, MaxQuantityF: 1000, StepSizeF: 0.001,
		},
		MinNotionalFilter: &MinNotionalFilter{
			Notional: "100", NotionalF: 100,
		},
	}
}

// cheapSymbolInfo 构造一个低价币(SOL 风格)的测试交易对。
func cheapSymbolInfo() *SymbolInfo {
	return &SymbolInfo{
		Symbol: "SOLUSDT",
		PriceFilter: &PriceFilter{
			MinPrice: "0.01", MaxPrice: "10000", TickSize: "0.01",
			MinPriceF: 0.01, MaxPriceF: 10000, TickSizeF: 0.01,
		},
		MarketLotSizeFilter: &MarketLotSizeFilter{
			MinQuantity: "0.001", MaxQuantity: "1000000", StepSize: "0.001",
			MinQuantityF: 0.001, MaxQuantityF: 1000000, StepSizeF: 0.001,
		},
		MinNotionalFilter: &MinNotionalFilter{
			Notional: "5", NotionalF: 5,
		},
	}
}

func TestFixPriceText(t *testing.T) {
	si := testSymbolInfo()
	cases := []struct{ in, want string }{
		{"60000", "60000"},
		{"60000.15", "60000.2"},   // 四舍五入到 0.1
		{"60000.04", "60000"},     // 四舍五入到 0.1
		{"5999.9", "6000"},        // 低于 MinPrice, 夹取
		{"1000001", "1000000"},    // 高于 MaxPrice, 夹取
		{"12345.6789", "12345.7"}, // 多位小数
		{"0.3", "6000"},           // 极低价格被夹取到 MinPrice
	}
	for _, c := range cases {
		got, err := FixPriceText(si, c.in)
		if err != nil {
			t.Errorf("FixPriceText(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("FixPriceText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFixQuantityText(t *testing.T) {
	si := testSymbolInfo()
	cases := []struct{ in, want string }{
		{"0.001", "0.001"},
		{"0.0015", "0.002"},   // 四舍五入到 0.001
		{"0.0009", "0.001"},   // 低于 MinQuantity, 夹取
		{"0.333333", "0.333"}, // 步长舍入
		{"2000", "1000"},      // 高于 MaxQuantity, 夹取
	}
	for _, c := range cases {
		got, err := FixQuantityText(si, c.in)
		if err != nil {
			t.Errorf("FixQuantityText(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("FixQuantityText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFixOrderTextNotional(t *testing.T) {
	si := testSymbolInfo()
	// 名义价值: 60000 × 0.001 = 60 < 100 → 数量提高到 ceil(100/60000/0.001)*0.001 = 0.002
	fp, fq, err := FixOrderText(si, "60000", "0.001")
	if err != nil {
		t.Fatal(err)
	}
	if fp != "60000" {
		t.Errorf("price = %q, want 60000", fp)
	}
	if fq != "0.002" {
		t.Errorf("qty = %q, want 0.002(0.002*60000=120>=100)", fq)
	}

	// 名义价值已满足: 60000 × 0.01 = 600 >= 100, 数量不变
	_, fq2, err := FixOrderText(si, "60000", "0.01")
	if err != nil {
		t.Fatal(err)
	}
	if fq2 != "0.01" {
		t.Errorf("qty = %q, want 0.01", fq2)
	}
}

func TestFixOrderTextNotionalRaisesToStep(t *testing.T) {
	// 低价币: 价格 0.5 × 数量 10 = 5 = MinNotional(恰好满足, 数量不变)
	si := cheapSymbolInfo()
	_, fq, err := FixOrderText(si, "0.5", "10")
	if err != nil {
		t.Fatal(err)
	}
	if fq != "10" {
		t.Errorf("qty = %q, want 10", fq)
	}

	// 价格 52.432523423 × 数量 0.08612893 ≈ 4.516 < 5 → 数量提高
	fp, fq, err := FixOrderText(si, "52.432523423", "0.08612893")
	if err != nil {
		t.Fatal(err)
	}
	if fp != "52.43" { // 四舍五入到 0.01
		t.Errorf("price = %q, want 52.43", fp)
	}
	// need = 5/52.43 = 0.09536..., 向上取整到 0.001 → 0.096
	if fq != "0.096" {
		t.Errorf("qty = %q, want 0.096", fq)
	}
}

func TestFixOrderNotionalCannotSatisfy(t *testing.T) {
	// 数量上限过小: 名义价值需 5, 价格 0.01 时需 500, 但上限只有 100 → 报错
	si := &SymbolInfo{
		PriceFilter: &PriceFilter{
			MinPrice: "0.01", MaxPrice: "10000", TickSize: "0.01",
		},
		MarketLotSizeFilter: &MarketLotSizeFilter{
			MinQuantity: "0.1", MaxQuantity: "100", StepSize: "0.1",
		},
		MinNotionalFilter: &MinNotionalFilter{Notional: "5"},
	}
	_, _, err := FixOrderText(si, "0.01", "10")
	if err == nil {
		t.Fatal("期望报错(无法满足最小名义价值), 实际无错误")
	}
	if !strings.Contains(err.Error(), "最大数量") {
		t.Errorf("错误信息不符合预期: %v", err)
	}
}

func TestFixPricePrecisionNoFloatError(t *testing.T) {
	// 经典浮点陷阱: 0.3/0.1 系列, 绝不能出现 0.30000000000000004
	si := cheapSymbolInfo()
	// 修改为低价高精度场景: 价格 0.0000003, tick 0.0000001
	si.PriceFilter.MinPrice = "0.0000001"
	si.PriceFilter.MaxPrice = "1"
	si.PriceFilter.TickSize = "0.0000001"

	for _, c := range []struct{ in, want string }{
		{"0.0000003", "0.0000003"},
		{"0.00000035", "0.0000004"}, // 0.00000035/0.0000001=3.5 → 四舍五入 4
		{"0.00000123", "0.0000012"},
	} {
		got, err := FixPriceText(si, c.in)
		if err != nil {
			t.Errorf("FixPriceText(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("FixPriceText(%q) = %q, want %q", c.in, got, c.want)
		}
		if strings.Contains(got, "00000000000000") {
			t.Errorf("出现浮点尾差: %q", got)
		}
	}
}

func TestFixLargeNumberNoScientific(t *testing.T) {
	si := testSymbolInfo()
	// 大数: 结果必须是普通十进制文本, 不能是 1e+06 之类
	got, err := FixPriceText(si, "999999.999999")
	if err != nil {
		t.Fatal(err)
	}
	if got != "1000000" {
		t.Errorf("price = %q, want 1000000", got)
	}
	if strings.ContainsAny(got, "eE") {
		t.Errorf("出现科学计数法: %q", got)
	}

	gq, err := FixQuantityText(si, "999.999999")
	if err != nil {
		t.Fatal(err)
	}
	if gq != "1000" {
		t.Errorf("qty = %q, want 1000", gq)
	}
}

func TestExchangeFixSymbolNotInList(t *testing.T) {
	e := &Exchange{Symbols: map[string]*SymbolInfo{"BTCUSDT": testSymbolInfo()}}
	if _, err := e.FixPrice("ETHUSDT", 100); err == nil {
		t.Error("期望未知交易对报错")
	}
	if _, err := e.FixQuantity("ETHUSDT", 1); err == nil {
		t.Error("期望未知交易对报错")
	}
	if _, _, err := e.FixOrder("ETHUSDT", 100, 1); err == nil {
		t.Error("期望未知交易对报错")
	}
}

// withExc 临时设置包级 exc, 测试结束后恢复。
func withExc(e *Exchange) func() {
	old := exc
	exc = e
	return func() { exc = old }
}

func TestOrderServiceFixUsesGlobalExc(t *testing.T) {
	defer withExc(&Exchange{Symbols: map[string]*SymbolInfo{"BTCUSDT": testSymbolInfo()}})()

	// 包级 exc.Symbols 生效: 价格修正 + 数量满足名义价值
	svc := &OrderService{}
	svc.symbol = "BTCUSDT"
	svc.side = "BUY"
	svc.positionSide = "LONG"
	svc.orderType = "LIMIT"
	svc.timeInForce = "GTC"
	svc.quantity = "0.001"
	svc.price = "60000.04"
	params, err := svc.Build()
	if err != nil {
		t.Fatal(err)
	}
	if params["price"] != "60000" {
		t.Errorf("price = %v, want 60000", params["price"])
	}
	// 60000×0.001=60<100 → 数量提高到 0.002
	if params["quantity"] != "0.002" {
		t.Errorf("quantity = %v, want 0.002", params["quantity"])
	}
}

func TestOrderServiceUnknownSymbol(t *testing.T) {
	defer withExc(&Exchange{Symbols: map[string]*SymbolInfo{"BTCUSDT": testSymbolInfo()}})()

	// 交易对不在 exc.Symbols 中 → 报错
	svc := &OrderService{}
	svc.symbol = "ETHUSDT"
	svc.side = "BUY"
	svc.positionSide = "LONG"
	svc.orderType = "LIMIT"
	svc.quantity = "0.001"
	svc.price = "1000"
	if _, err := svc.Build(); err == nil {
		t.Error("期望未知交易对报错")
	} else if !strings.Contains(err.Error(), "不在 Symbols") {
		t.Errorf("错误信息不符合预期: %v", err)
	}
}

func TestOrderServiceWithoutSymbols(t *testing.T) {
	defer withExc(nil)() // exc 为 nil: 不做校验/修正, 保持原值

	svc := &OrderService{}
	svc.symbol = "BTCUSDT"
	svc.side = "BUY"
	svc.positionSide = "LONG"
	svc.orderType = "LIMIT"
	svc.quantity = "0.001"
	svc.price = "60000.04"
	params, err := svc.Build()
	if err != nil {
		t.Fatal(err)
	}
	if params["price"] != "60000.04" || params["quantity"] != "0.001" {
		t.Errorf("exc 为 nil 不应修正: price=%v quantity=%v", params["price"], params["quantity"])
	}
}

func TestOrderServiceDefaults(t *testing.T) {
	defer withExc(nil)()

	// Type 不写 → 默认 MARKET; MARKET 单不带 timeInForce
	svc := &OrderService{}
	svc.symbol = "BTCUSDT"
	svc.side = "BUY"
	svc.positionSide = "LONG"
	svc.quantity = "0.001"
	params, err := svc.Build()
	if err != nil {
		t.Fatal(err)
	}
	if params["type"] != "MARKET" {
		t.Errorf("type = %v, want MARKET(默认)", params["type"])
	}
	if _, ok := params["timeInForce"]; ok {
		t.Errorf("MARKET 单不应含 timeInForce: %v", params["timeInForce"])
	}

	// LIMIT 单 TimeInForce 不写 → 默认 GTC
	svc2 := &OrderService{}
	svc2.symbol = "BTCUSDT"
	svc2.side = "BUY"
	svc2.positionSide = "LONG"
	svc2.orderType = "LIMIT"
	svc2.quantity = "0.001"
	svc2.price = "60000"
	params2, err := svc2.Build()
	if err != nil {
		t.Fatal(err)
	}
	if params2["timeInForce"] != "GTC" {
		t.Errorf("timeInForce = %v, want GTC(默认)", params2["timeInForce"])
	}
}

func TestOrderServiceUsdt(t *testing.T) {
	defer withExc(&Exchange{Symbols: map[string]*SymbolInfo{"BTCUSDT": testSymbolInfo()}})()

	// Usdt("1000") + Price("60000"): 数量 = 1000/60000 = 0.016666... → 步长 0.001 → 0.017
	svc := &OrderService{}
	svc.symbol = "BTCUSDT"
	svc.side = "BUY"
	svc.positionSide = "LONG"
	svc.orderType = "LIMIT"
	svc.usdt = "1000"
	svc.price = "60000"
	params, err := svc.Build()
	if err != nil {
		t.Fatal(err)
	}
	if params["price"] != "60000" {
		t.Errorf("price = %v, want 60000", params["price"])
	}
	if params["quantity"] != "0.017" {
		t.Errorf("quantity = %v, want 0.017", params["quantity"])
	}
}

func TestOrderServiceUsdtBelowNotional(t *testing.T) {
	defer withExc(&Exchange{Symbols: map[string]*SymbolInfo{"BTCUSDT": testSymbolInfo()}})()

	// 金额 50 < MinNotional 100 → 报错
	svc := &OrderService{}
	svc.symbol = "BTCUSDT"
	svc.side = "BUY"
	svc.positionSide = "LONG"
	svc.orderType = "LIMIT"
	svc.usdt = "50"
	svc.price = "60000"
	if _, err := svc.Build(); err == nil {
		t.Fatal("期望报错(金额小于最小名义价值)")
	} else if !strings.Contains(err.Error(), "最小名义价值") {
		t.Errorf("错误信息不符合预期: %v", err)
	}
}

func TestOrderServiceUsdtNoPrice(t *testing.T) {
	defer withExc(&Exchange{Symbols: map[string]*SymbolInfo{"BTCUSDT": testSymbolInfo()}})()

	// 按 USDT 下单必须写价格 → 报错
	svc := &OrderService{}
	svc.symbol = "BTCUSDT"
	svc.side = "BUY"
	svc.positionSide = "LONG"
	svc.orderType = "LIMIT"
	svc.usdt = "1000"
	if _, err := svc.Build(); err == nil {
		t.Fatal("期望报错(缺少价格)")
	} else if !strings.Contains(err.Error(), "价格") {
		t.Errorf("错误信息不符合预期: %v", err)
	}
}

func TestOrderServiceUsdtAndQuantity(t *testing.T) {
	defer withExc(&Exchange{Symbols: map[string]*SymbolInfo{"BTCUSDT": testSymbolInfo()}})()

	// Quantity 与 Usdt 二选一 → 报错
	svc := &OrderService{}
	svc.symbol = "BTCUSDT"
	svc.side = "BUY"
	svc.positionSide = "LONG"
	svc.orderType = "LIMIT"
	svc.usdt = "1000"
	svc.quantity = "0.001"
	svc.price = "60000"
	if _, err := svc.Build(); err == nil {
		t.Fatal("期望报错(Quantity 与 Usdt 二选一)")
	} else if !strings.Contains(err.Error(), "二选一") {
		t.Errorf("错误信息不符合预期: %v", err)
	}
}

func TestExchangeGetSymbol(t *testing.T) {
	e := &Exchange{Symbols: map[string]*SymbolInfo{"BTCUSDT": testSymbolInfo()}}
	si, ok := e.GetSymbol("BTCUSDT")
	if !ok || si == nil {
		t.Fatal("期望获取到 BTCUSDT 交易对信息")
	}
	if si.Symbol != "BTCUSDT" {
		t.Errorf("symbol = %q", si.Symbol)
	}
	if _, ok := e.GetSymbol("ETHUSDT"); ok {
		t.Error("期望 ETHUSDT 不存在")
	}
}

func TestBuildSymbols(t *testing.T) {
	info := &futures.ExchangeInfo{
		Symbols: []futures.Symbol{
			{
				Symbol:            "BTCUSDT",
				Pair:              "BTCUSDT",
				Status:            "TRADING",
				PricePrecision:    2,
				QuantityPrecision: 3,
				Filters: []map[string]any{
					{"filterType": "PRICE_FILTER", "maxPrice": "1000000", "minPrice": "6000", "tickSize": "0.1"},
					{"filterType": "MARKET_LOT_SIZE", "maxQty": "1000", "minQty": "0.001", "stepSize": "0.001"},
					{"filterType": "MIN_NOTIONAL", "notional": "100"},
				},
			},
			{Symbol: "ETHUSDT", Status: "BREAK"}, // 非 TRADING, 应被过滤
		},
	}
	symbols := buildSymbols(info)
	if len(symbols) != 1 {
		t.Fatalf("期望 1 个交易对, 实际 %d", len(symbols))
	}
	si, ok := symbols["BTCUSDT"]
	if !ok {
		t.Fatal("期望包含 BTCUSDT")
	}
	if si.PriceFilter == nil || si.PriceFilter.TickSize != "0.1" {
		t.Errorf("PriceFilter 解析异常: %+v", si.PriceFilter)
	}
	if si.MarketLotSizeFilter == nil || si.MarketLotSizeFilter.StepSize != "0.001" {
		t.Errorf("MarketLotSizeFilter 解析异常: %+v", si.MarketLotSizeFilter)
	}
	if si.MinNotionalFilter == nil || si.MinNotionalFilter.Notional != "100" {
		t.Errorf("MinNotionalFilter 解析异常: %+v", si.MinNotionalFilter)
	}
	if _, ok := symbols["ETHUSDT"]; ok {
		t.Error("期望过滤掉非 TRADING 交易对 ETHUSDT")
	}
}
