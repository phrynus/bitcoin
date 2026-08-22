// 保证金再平衡主循环。
// 项目目标：双向持仓（USDC/USDT 对冲对）保持数量平衡，并把保证金比率维持在基准点附近。
// 本文件负责根据保证金比率与基准点的偏差触发加仓 / 减仓，并在间隙清理异常持仓。
// 节奏与阈值全部来自配置（cfg.Margin / cfg.Loop）。
package main

import (
	"time"
)

// marginReduce 减仓触发阈值：基准点 + 带宽。
func marginReduce() float64 { return cfg.Margin.Base + cfg.Margin.Range }

// marginAdd 加仓触发阈值：基准点 - 带宽。
func marginAdd() float64 { return cfg.Margin.Base - cfg.Margin.Range }

// RebalanceLoop 再平衡主循环。
//   - 保证金比率高于上限（base+range）→ 减仓，并启动独立减仓线程收敛到基准点
//   - 保证金比率低于下限（base-range）→ 加仓（开一对新对冲），加仓后复查一次，若比率反超上限则再触发减仓
//   - 其余时间 → 清理异常持仓：平衡对冲对数量、平单边、平反向 / 规划外对冲对
func (h *hedge) RebalanceLoop() {
	h.log.Infof("账户 %s 的平衡主循环启动: 加仓阈值=%.2f%% 减仓阈值=%.2f%% 扫描间隔=%s", h.name,
		marginAdd(), marginReduce(), cfg.Loop.ScanInterval)
	for {
		// 主循环扫描间隔
		time.Sleep(cfg.Loop.ScanInterval)

		// 若独立减仓线程正在进行，等它结束再继续，避免并发下单
		for h.isReducing() {
			h.log.Infof("独立减仓进行中，主循环等待 %s", cfg.Loop.ReduceWait)
			time.Sleep(cfg.Loop.ReduceWait)
		}

		// 账户快照：余额、保证金比率、全部持仓
		s, err := h.Snap()
		if err != nil {
			h.log.Errorf("再平衡循环获取快照失败，跳过本轮: %v", err)
			continue
		}

		// 保证金比率过高 → 减仓，并启动独立线程持续减到基准点
		if s.margin > marginReduce() {
			h.log.Warnf("保证金率 %.2f%% 高于减仓阈值 %.2f%%，触发减仓", s.margin, marginReduce())
			h.Reduce(s.positions)
			go h.reduceToTarget()
			continue
		}

		// 可用余额不足 → 不加仓
		if s.balance <= cfg.Add.MinBalance {
			h.log.Warnf("加仓: 可用余额 %.4f 低于下限 %.4f，跳过加仓", s.balance, cfg.Add.MinBalance)
			continue
		}

		// 比率正常 → 维护持仓健康：
		h.log.Infof("保证金率 %.2f%% 处于正常区间，维护持仓健康(持仓对数=%d)", s.margin, len(s.positions))
		// 1) 平衡对冲对两边数量
		h.Balance(s.positions)
		time.Sleep(cfg.Loop.StepPause)
		// 2) 平掉单边持仓（对冲失效的残余腿）
		h.CloseOneLeg(s.positions)
		time.Sleep(cfg.Loop.StepPause)
		// 3) 平掉反向 / 规划外对冲对
		h.CloseIrregular(s.positions)

		// 再次快照，保证金比率过低 → 加仓（开一对新对冲）
		s2, err := h.Snap()
		if err != nil {
			h.log.Errorf("再次获取快照失败，跳过加仓判断: %v", err)
			time.Sleep(cfg.Loop.ScanInterval)
			continue
		}
		if s2.margin < marginAdd() {
			h.log.Infof("保证金率 %.2f%% 低于加仓阈值 %.2f%%，触发加仓", s2.margin, marginAdd())
			h.AddPair(s2)
			time.Sleep(cfg.Loop.StepPause)

			// 加仓后复查：加仓会占用更多保证金、推高保证金率，再拉一次快照判断是否需减仓
			s3, err := h.Snap()
			if err != nil {
				h.log.Errorf("加仓后复查快照失败，跳过本轮: %v", err)
				time.Sleep(cfg.Loop.ScanInterval)
				continue
			}
			if s3.margin > marginReduce() {
				h.log.Warnf("加仓后保证金率 %.2f%% 高于减仓阈值 %.2f%%，触发减仓", s3.margin, marginReduce())
				h.Reduce(s3.positions)
				go h.reduceToTarget()
				continue
			}
			h.log.Infof("加仓后保证金率 %.2f%% 未超减仓阈值，无需减仓", s3.margin)

			time.Sleep(cfg.Loop.StepPause)
			// 1) 平衡对冲对两边数量
			h.Balance(s3.positions)
			time.Sleep(cfg.Loop.StepPause)
			// 2) 平掉单边持仓（对冲失效的残余腿）
			h.CloseOneLeg(s3.positions)
			time.Sleep(cfg.Loop.StepPause)
			// 3) 平掉反向 / 规划外对冲对
			h.CloseIrregular(s3.positions)

		} else {
			h.log.Infof("保证金率 %.2f%% 无需加仓", s2.margin)
		}

	}
}

// reduceToTarget 独立减仓线程：反复减仓直至保证金比率收敛到基准点。
// 由 RebalanceLoop 在触发减仓时启动，期间主循环等待其结束。
func (h *hedge) reduceToTarget() {
	h.setReducing(true)
	defer h.setReducing(false)
	h.log.Infof("启动独立减仓线程，目标收敛到基准点 %.2f%%", cfg.Margin.Base)

	for {

		s, err := h.Snap()
		if err != nil {
			h.log.Errorf("独立减仓获取快照失败，退出减仓线程: %v", err)
			return
		}
		// 已收敛到基准点 → 结束
		if s.margin <= cfg.Margin.Base {
			h.log.Infof("保证金率 %.2f%% 已收敛到基准点，减仓线程结束", s.margin)
			return
		}

		// 按超出基准点的程度动态调整减仓间隔
		interval := reduceInterval(s.margin)
		h.log.Infof("减仓中: 当前保证金率 %.2f%% 目标 %.2f%%，%s 后继续减仓", s.margin, cfg.Margin.Base, interval)
		time.Sleep(interval)

		h.Reduce(s.positions)
	}
}

// reduceInterval 计算减仓循环中每轮的等待间隔。
// 保证金率未超出基准点时返回基础间隔（cfg.Reduce.Interval）；
// 超出后间隔随超出点数衰减：间隔 = base × (1 - reduce_cut × 超出点数)，
// 超出越多间隔越短；当 reduce_cut × 超出点数 ≥ 1 时返回 0（立即连续减仓，不等待）。
func reduceInterval(margin float64) time.Duration {
	base := cfg.Reduce.Interval
	over := margin - cfg.Margin.Base
	if over <= 0 {
		return base
	}

	if cut := cfg.Reduce.Cut * over; cut >= 1 {
		return 0
	} else {
		return time.Duration(float64(base) * (1 - cut))
	}
}

// setReducing 设置减仓线程运行状态。
func (h *hedge) setReducing(v bool) {
	h.reduceMu.Lock()
	h.reducing = v
	h.reduceMu.Unlock()
}

// isReducing 查询是否正在独立减仓。
func (h *hedge) isReducing() bool {
	h.reduceMu.Lock()
	defer h.reduceMu.Unlock()
	return h.reducing
}
