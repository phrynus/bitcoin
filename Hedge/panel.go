// 信息展示面板：每次快照拉取的数据留存到内存，并通过 Gin + html/template 提供网页展示。
// 网页打开时只读取留存数据，不主动向交易所请求新数据；机器人每次 Snap() 后更新留存。
// 留存数据仅存内存（不落盘），页面为单个嵌入的 panel.html（无自动刷新，手动刷新浏览器即可）。
package main

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/phrynus/go-utils/plog"
)

//go:embed panel.html
var panelHTML embed.FS

// PanelData 面板留存数据的顶层结构（仅内存）。
type PanelData struct {
	UpdatedAt time.Time
	Accounts  map[string]*AccountData
}

// AccountData 单个账户的留存快照。
type AccountData struct {
	Name        string
	Balance     float64 // 可用余额
	Pnl         float64 // 未实现盈亏
	Margin      float64 // 保证金比率（百分比，0.55 = 0.55%）
	Wallet      float64 // 钱包余额
	Maint       float64 // 维持保证金
	MarginBal   float64 // 保证金余额
	InitMargin  float64 // 初始保证金
	PosInitM    float64 // 持仓初始保证金
	OpenInitM   float64 // 挂单初始保证金
	MaxWithdraw float64 // 最大可提现
	Reducing    bool    // 是否正在独立减仓
	Positions   map[string]*PosData
	UpdatedAt   time.Time
}

// PosData 单个对冲对的留存数据。
type PosData struct {
	Value float64 // 两条腿名义价值合计
	Pnl   float64 // 两条腿未实现盈亏合计
	Usdt  LegData
	Usdc  LegData
}

// LegData 单条腿的留存数据。
type LegData struct {
	Qty       float64 // 持仓数量
	Value     float64 // 名义价值
	Pnl       float64 // 未实现盈亏
	Side      string  // 方向（LONG / SHORT）
	Entry     float64 // 开仓价
	Mark      float64 // 标记价
	Liq       float64 // 强平价
	BreakEven float64 // 保本价
	InitM     float64 // 初始保证金
	MaintM    float64 // 维持保证金
}

// PanelStore 留存数据存储（仅内存，线程安全）。
type PanelStore struct {
	mu   sync.RWMutex
	data *PanelData
}

// panel 全局面板存储实例。
var panel = &PanelStore{data: &PanelData{Accounts: make(map[string]*AccountData)}}

// Update 用最新快照更新指定账户的留存数据。
func (p *PanelStore) Update(name string, s *snap, reducing bool) {
	if s == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	ad := &AccountData{
		Name:        name,
		Balance:     s.balance,
		Pnl:         s.pnl,
		Margin:      s.margin,
		Wallet:      s.wallet,
		Maint:       s.maint,
		MarginBal:   s.marginBal,
		InitMargin:  s.initMargin,
		PosInitM:    s.posInitM,
		OpenInitM:   s.openInitM,
		MaxWithdraw: s.maxWithdraw,
		Reducing:    reducing,
		Positions:   make(map[string]*PosData, len(s.positions)),
		UpdatedAt:   time.Now(),
	}
	for base, pos := range s.positions {
		ad.Positions[base] = &PosData{
			Value: pos.value,
			Pnl:   pos.pnl,
			Usdt:  LegData{Qty: pos.usdt.qty, Value: pos.usdt.value, Pnl: pos.usdt.pnl, Side: pos.usdt.side, Entry: pos.usdt.entry, Mark: pos.usdt.mark, Liq: pos.usdt.liq, BreakEven: pos.usdt.breakEven, InitM: pos.usdt.initM, MaintM: pos.usdt.maintM},
			Usdc:  LegData{Qty: pos.usdc.qty, Value: pos.usdc.value, Pnl: pos.usdc.pnl, Side: pos.usdc.side, Entry: pos.usdc.entry, Mark: pos.usdc.mark, Liq: pos.usdc.liq, BreakEven: pos.usdc.breakEven, InitM: pos.usdc.initM, MaintM: pos.usdc.maintM},
		}
	}
	p.data.Accounts[name] = ad
	p.data.UpdatedAt = time.Now()
}

// Snapshot 返回当前留存数据的只读引用（调用方只读使用）。
func (p *PanelStore) Snapshot() *PanelData {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.data
}

// pageData 模板渲染数据。
type pageData struct {
	UpdatedAt time.Time
	Accounts  []*AccountData
	Config    *Config
}

// StartWebServer 启动信息面板 HTTP 服务。
func StartWebServer(addr string) {
	r := newPanelRouter()
	plog.Infof("信息面板已启动: http://localhost%s", addr)
	if err := r.Run(addr); err != nil {
		plog.Errorf("信息面板启动失败: %v", err)
	}
}

// newPanelRouter 构建信息面板路由（独立出来便于测试）。
func newPanelRouter() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	tmpl := template.Must(template.New("panel.html").Funcs(templateFuncs()).ParseFS(panelHTML, "panel.html"))
	r.SetHTMLTemplate(tmpl)

	// 首页：渲染留存数据
	r.GET("/", func(c *gin.Context) {
		pd := panel.Snapshot()
		accounts := make([]*AccountData, 0, len(pd.Accounts))
		for _, a := range pd.Accounts {
			accounts = append(accounts, a)
		}
		sort.Slice(accounts, func(i, j int) bool { return accounts[i].Name < accounts[j].Name })
		c.HTML(http.StatusOK, "panel.html", pageData{
			UpdatedAt: pd.UpdatedAt,
			Accounts:  accounts,
			Config:    cfg,
		})
	})

	return r
}

// templateFuncs 模板辅助函数。
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		// 数字格式化：管道用法 {{ .Balance | fmt 4 }} 实际调用 fmt(4, .Balance)
		"fmt": func(prec int, f float64) string {
			return fmt.Sprintf("%."+strconv.Itoa(prec)+"f", f)
		},
		// 数字格式化，为 0 时显示 "-"（用于价格 / 保证金 / 数量等可能无值的字段）
		"fmtz": func(prec int, f float64) string {
			if f == 0 {
				return "-"
			}
			return fmt.Sprintf("%."+strconv.Itoa(prec)+"f", f)
		},
		// 保证金率百分比 → 万分比（0.55% → 55），与配置基准点同单位
		"wan": func(m float64) float64 { return m * 100 },
		// 百分比显示
		"pct": func(m float64) string { return fmt.Sprintf("%.2f%%", m) },
		// 时间格式化
		"time": func(t time.Time) string {
			if t.IsZero() {
				return "-"
			}
			return t.Format("2006-01-02 15:04:05")
		},
		// 方向中文
		"side": func(s string) string {
			switch s {
			case "LONG":
				return "多"
			case "SHORT":
				return "空"
			}
			return "-"
		},
		// 正负号
		"sign": func(f float64) string {
			if f > 0 {
				return "+"
			}
			return ""
		},
		// 加法 / 减法
		"add": func(a, b float64) float64 { return a + b },
		"sub": func(a, b float64) float64 { return a - b },
	}
}