package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 构造测试用的全局配置与留存数据。
func setupPanelTest() {
	cfg = &Config{
		Margin: MarginRule{Base: 50, Range: 5},
		Loop:   LoopRule{ScanInterval: time.Minute, ReduceWait: 5 * time.Second, StepPause: time.Second},
		Add:    AddRule{MinBalance: 50, CapRatio: 1.6, Timeout: 30 * time.Second},
		Close:  CloseRule{Timeout: 10 * time.Second, StepUsdt: "100", StepNotional: 120},
		Reduce: ReduceRule{StepUsdt: "200", StepNotional: 240, Interval: time.Second, Cut: 0.1},
		Over:   OverRule{Ratio: 1.9, StepUsdt: "100"},
		Panel:  PanelRule{Addr: ":8080"},
		Plans: []Plan{
			{Base: "DOGE", Usdt: 20, Cap: 10000},
			{Base: "XRP", Usdt: 20, Cap: 20000},
		},
	}
	panel = &PanelStore{data: &PanelData{
		UpdatedAt: time.Now(),
		Accounts: map[string]*AccountData{
			"BIN01": {
				Name:        "BIN01",
				Balance:     1234.5678,
				Pnl:         12.34,
				Margin:      0.55,
				Wallet:      2000,
				Maint:       11,
				MarginBal:   2012.34,
				InitMargin:  100,
				PosInitM:    90,
				OpenInitM:   10,
				MaxWithdraw: 1500,
				Reducing:    false,
				Positions: map[string]*PosData{
					"DOGE": {
						Value: 100,
						Pnl:   5,
						Usdt:  LegData{Qty: 100, Value: 50, Pnl: 3, Side: "LONG", Entry: 0.5, Mark: 0.52, Liq: 0.1, BreakEven: 0.5, InitM: 10, MaintM: 5},
						Usdc:  LegData{Qty: 100, Value: 50, Pnl: 2, Side: "SHORT", Entry: 0.5, Mark: 0.52, Liq: 0.9, BreakEven: 0.5, InitM: 10, MaintM: 5},
					},
				},
				UpdatedAt: time.Now(),
			},
		},
	}}
}

func TestPanelIndexRender(t *testing.T) {
	setupPanelTest()
	r := newPanelRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"Hedge 对冲面板",
		"BIN01",
		"DOGE",
		"1234.5678",
		"55",        // 保证金率万分比 0.55*100
		"运行配置",
		"规划币种",
		"XRP",
		"钱包余额",
		"开仓价",
		"强平价",
		"保本价",
		"最大可提现",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("页面缺少内容 %q", want)
		}
	}
}

func TestPanelUpdate(t *testing.T) {
	panel = &PanelStore{data: &PanelData{Accounts: make(map[string]*AccountData)}}
	s := &snap{
		balance:     100,
		pnl:         5,
		margin:      0.55,
		wallet:      200,
		maint:       1.1,
		marginBal:   205,
		initMargin:  10,
		posInitM:    9,
		openInitM:   1,
		maxWithdraw: 150,
		positions: map[string]*pos{
			"DOGE": {
				value: 100,
				pnl:   5,
				usdt:  leg{qty: 100, value: 50, pnl: 3, side: "LONG", entry: 0.5, mark: 0.52, liq: 0.1, initM: 10, maintM: 5},
				usdc:  leg{qty: 100, value: 50, pnl: 2, side: "SHORT", entry: 0.5, mark: 0.52, liq: 0.9, initM: 10, maintM: 5},
			},
		},
	}
	panel.Update("BIN01", s, true)

	pd := panel.Snapshot()
	ad := pd.Accounts["BIN01"]
	if ad == nil {
		t.Fatal("留存数据缺少账户 BIN01")
	}
	if ad.Wallet != 200 || ad.Maint != 1.1 || ad.MarginBal != 205 {
		t.Errorf("账户明细留存错误: wallet=%v maint=%v marginBal=%v", ad.Wallet, ad.Maint, ad.MarginBal)
	}
	if !ad.Reducing {
		t.Error("Reducing 应为 true")
	}
	p := ad.Positions["DOGE"]
	if p == nil {
		t.Fatal("留存数据缺少持仓 DOGE")
	}
	if p.Usdc.Entry != 0.5 || p.Usdc.Liq != 0.9 || p.Usdt.Mark != 0.52 {
		t.Errorf("腿价格明细留存错误: usdc.entry=%v usdc.liq=%v usdt.mark=%v", p.Usdc.Entry, p.Usdc.Liq, p.Usdt.Mark)
	}
}