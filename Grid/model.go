package main

import (
	"fmt"
	"sort"
	"sync"

	"github.com/phrynus/plog"
)

// 网格存储：全内存实现，替代原 SQLite 持久化。
// 策略由多条 goroutine 并发驱动（行情回调、用户数据流成交回调、窗口移动），
// 所有读写都在 RWMutex 保护下完成：读返回副本、写按编号在单次加锁内批量改字段，
// 调用方拿不到内部指针，杜绝绕过锁修改共享状态。
type gridStore struct {
	mu sync.RWMutex    // 保护 m
	m  map[int64]*Grid // 网格编号 → 网格状态
}

// grids 为全局唯一的网格存储实例。
var grids = newGridStore()

func newGridStore() *gridStore {
	return &gridStore{m: make(map[int64]*Grid)}
}

// InitStore 初始化内存存储（替代原 SQLite 建表与连接池初始化）。
// 状态只存在于进程内存中，进程退出即丢弃——启动时原本也会清空数据，无需持久化。
func InitStore() error {
	plog.Debugf("[存储] 内存模式: 网格状态仅保存在进程内存中, 不再使用数据库")
	grids.Clear()
	plog.Debugf("[存储] 内存存储初始化完成")
	return nil
}

// Create 写入一个新网格；编号已存在时返回错误（等价于原主键冲突）。
func (s *gridStore) Create(g Grid) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[g.ID]; ok {
		return fmt.Errorf("网格 %d 已存在", g.ID)
	}
	s.m[g.ID] = &g
	return nil
}

// Get 读取单个网格的副本；不存在时 ok 为 false。
func (s *gridStore) Get(id int64) (Grid, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, found := s.m[id]
	if !found {
		return Grid{}, false
	}
	return *p, true
}

// List 按编号升序返回给定编号中已存在的网格副本，缺失的编号直接跳过。
func (s *gridStore) List(ids []int64) []Grid {
	s.mu.RLock()
	defer s.mu.RUnlock()
	gs := make([]Grid, 0, len(ids))
	for _, id := range ids {
		if p, ok := s.m[id]; ok {
			gs = append(gs, *p)
		}
	}
	sort.Slice(gs, func(i, j int) bool { return gs[i].ID < gs[j].ID })
	return gs
}

// Update 按编号修改若干字段，在一次加锁内原子应用（等价于原来的一条多列 UPDATE）；
// 编号不存在时返回错误。
func (s *gridStore) Update(id int64, fields ...gridField) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, found := s.m[id]
	if !found {
		return fmt.Errorf("网格 %d 不存在", id)
	}
	for _, f := range fields {
		f(p)
	}
	return nil
}

// Clear 清空全部网格（对应原 DELETE FROM grid）。
func (s *gridStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m = make(map[int64]*Grid)
}

// gridField 描述对网格的一次字段写入，由 set* 构造函数产生，
// 供 Update 在一次加锁内批量应用多个字段。
type gridField func(*Grid)

func setFilledQty(v string) gridField      { return func(g *Grid) { g.FilledQty = v } }
func setBidID(v string) gridField          { return func(g *Grid) { g.BidID = v } }
func setAskID(v string) gridField          { return func(g *Grid) { g.AskID = v } }
func setEmptyFilledQty(v string) gridField { return func(g *Grid) { g.EmptyFilledQty = v } }
func setEmptyBidID(v string) gridField     { return func(g *Grid) { g.EmptyBidID = v } }
func setEmptyAskID(v string) gridField     { return func(g *Grid) { g.EmptyAskID = v } }

// Grid 网格状态。
// ID 为网格编号：0 为中心格，向下（价格更低）递增为 1、2、3…，向上（价格更高）递减为 -1、-2…。
// 价格/数量统一用字符串保存，避免浮点误差；空串表示「无该订单号」或「未持仓」。
type Grid struct {
	ID             int64  `json:"id"`
	Qty            string `json:"qty"`              // 货币数量
	FilledQty      string `json:"filled_qty"`       // 多单成交数量
	BidID          string `json:"bid_id"`           // 多单买单ID
	AskID          string `json:"ask_id"`           // 多单卖单ID
	EmptyFilledQty string `json:"empty_filled_qty"` // 空单成交数量
	EmptyBidID     string `json:"empty_bid_id"`     // 空单回补买单ID
	EmptyAskID     string `json:"empty_ask_id"`     // 空单开空卖单ID
	BidPrice       string `json:"bid_price"`        // 买单价格
	AskPrice       string `json:"ask_price"`        // 卖单价格
	Space          string `json:"space"`            // 间距
}
