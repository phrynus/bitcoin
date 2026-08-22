package main

import (
	"context"
	"fmt"
	"time"

	"main/exchange"
	"main/logger"

	"github.com/adshao/go-binance/v2/futures"
	"github.com/shopspring/decimal"
)

// Account 单账户运行状态：每个账户持有独立的客户端、用户数据处理器和持仓快照。
type Account struct {
	Index       int
	Name        string
	Config      *AccountConfig
	Client      *futures.Client
	WsApi       *exchange.WsApi
	Uh          *UserHandler
	MarginRate  *MarginRateResult
	TCPositions map[string]TCPosition
	Log         *logger.Logger
}

// NewAccount 根据配置创建账户：初始化独立客户端和带账户标签的日志器。
// 所有账户共享同一代理与交易参数，仅 API Key 不同。
func NewAccount(index int, cfg *AccountConfig) *Account {
	a := &Account{
		Index:       index,
		Name:        cfg.Name,
		Config:      cfg,
		TCPositions: make(map[string]TCPosition),
	}
	if a.Name == "" {
		a.Name = fmt.Sprintf("account_%d", index+1)
	}
	a.Log = logger.Default().Sub(fmt.Sprintf("%s[%d]", a.Name, index+1))

	proxy := GetEnv().ProxyURL
	if proxy != "" {
		a.Client = futures.NewProxiedClient(cfg.APIKey, cfg.SecretKey, proxy)
	} else {
		a.Client = futures.NewClient(cfg.APIKey, cfg.SecretKey)
	}
	// 每账户独立的 WS API 客户端(下单/撤单/仓位查询均走 WS API)
	a.WsApi = exchange.NewWsApi(cfg.APIKey, cfg.SecretKey, proxy)
	return a
}

// runMainLoop 账户独立主循环。
func (a *Account) runMainLoop() {
	for {
		env := GetEnv()
		a.Log.Info("=========================")
		a.Log.Info("===== 新一轮处理开始 =====")
		a.Log.Info("=========================")
		a.GetTCPositions()

		if a.MarginRate == nil {
			a.Log.Infof("保证金率暂时不可用，休息 %s 再试", env.MainLoopIntervalDuration.String())
			time.Sleep(env.MainLoopIntervalDuration)
			continue
		}

		a.Log.Infof("当前保证金率 %s%%", formatDecimalFixed(a.MarginRate.MarginRatio, 4))

		// 若本轮提交了再平衡订单，则跳过加/减仓操作，但仍正常休息，
		// 避免在市价单反映到账户前立即进入下一轮造成重复下单。
		rebalanced := false
		if !a.MarginRate.MarginRatio.IsZero() {
			a.Log.Info("检查双边仓位是否平衡…")
			if a.BalancePositions() {
				a.Log.Info("再平衡订单已提交，本轮跳过加/减仓操作")
				rebalanced = true
			}
		}

		if !rebalanced {
			if a.MarginRate.MarginRatio.GreaterThan(env.MarginRatioReduceTrigger) {
				a.MarginRatioBeyond()
			} else if a.MarginRate.MarginRatio.LessThan(env.MarginRatioAddTrigger) {
				a.MarginRatioSmall()
			} else {
				a.Log.Info("保证金率在安全范围，无需操作")
			}
		}

		// 逐轮退出不在交易范围内的遗留币种(每轮每个币种最多退出一笔)
		a.CleanupOrphanPositions()

		a.Log.Infof("本轮处理完成，休息 %s", env.MainLoopIntervalDuration.String())
		time.Sleep(env.MainLoopIntervalDuration)

		if err := RefreshEnv(); err != nil {
			a.Log.Errorf("刷新配置失败: %v", err)
		}
	}
}

// GetTCPositions 拉取账户保证金率并整理双边持仓。
func (a *Account) GetTCPositions() {
	a.Log.Debug("开始获取仓位和保证金率…")
	result, err := a.CalcMarginRatio(context.Background(), "")
	if err != nil {
		a.Log.Errorf("计算保证金率失败: %v", err)
		return
	}

	a.MarginRate = result
	a.Log.Debugf("仓位更新完成: marginRatio=%s%% positions=%d",
		formatDecimalFixed(result.MarginRatio, 4), len(result.Positions))
	a.FormatSymbol(result.Positions)
}

// CleanupOrphanPositions 逐轮退出不在交易范围内的币种:
// 每轮对每个遗留币种挂限价单减仓 USDC(金额为 orphan_exit_usdt, 不超过剩余仓位),
// 成交后再市价平掉对应数量的 USDT。orphan_exit_usdt 为 0 时关闭。
func (a *Account) CleanupOrphanPositions() {
	env := GetEnv()
	if !env.OrphanExitUsdt.IsPositive() {
		return
	}

	for symbol, pos := range a.TCPositions {
		if env.GetSymbol(symbol) != nil {
			continue // 在交易范围内, 跳过
		}
		if !pos.USDC.Quantity.IsPositive() && !pos.USDT.Quantity.IsPositive() {
			continue // 无仓位
		}

		// 本轮减仓金额 = min(设定金额, USDC 剩余仓位价值)
		usdt := env.OrphanExitUsdt
		if usdcValue := pos.USDC.Quantity.Mul(pos.USDC.Price); usdcValue.LessThan(usdt) {
			usdt = usdcValue
		}

		if !usdt.IsPositive() {
			// USDC 已无仓位, 仅剩 USDT: 直接市价平掉
			if pos.USDT.Quantity.IsPositive() {
				si, err := getSymbolInfo(symbol + "USDT")
				if err != nil {
					a.Log.Errorf("%s 获取 USDT 交易对信息失败: %v", symbol, err)
					continue
				}
				if si.LotSizeFilter != nil {
					if minQty := parseDecimal(si.LotSizeFilter.MinQuantity); minQty.IsPositive() && pos.USDT.Quantity.LessThan(minQty) {
						a.Log.Warnf("%s USDT 剩余仓位 %s 低于最小下单数量 %s, 跳过(仅剩粉尘)",
							symbol, formatDecimalFixed(pos.USDT.Quantity, 4), si.LotSizeFilter.MinQuantity)
						continue
					}
				}
				q, err := formatQuantity(symbol+"USDT", pos.USDT.Quantity)
				if err != nil {
					a.Log.Errorf("格式化 %s USDT 数量失败: %v", symbol, err)
					continue
				}
				a.Log.Infof("%s 不在交易范围内，仅剩 USDT 仓位，市价平掉 %s", symbol, q)
				a.CloseUSDT(symbol, q)
			}
			continue
		}

		// USDC 剩余仓位低于最小下单数量时无法挂单减仓, 跳过本轮
		si, err := getSymbolInfo(symbol + "USDC")
		if err != nil {
			a.Log.Errorf("%s 获取 USDC 交易对信息失败: %v", symbol, err)
			continue
		}
		if si.LotSizeFilter == nil {
			a.Log.Errorf("%s 缺少 USDC 数量过滤器", symbol)
			continue
		}
		if minQty := parseDecimal(si.LotSizeFilter.MinQuantity); minQty.IsPositive() && pos.USDC.Quantity.LessThan(minQty) {
			a.Log.Warnf("%s USDC 剩余仓位 %s 低于最小下单数量 %s, 跳过本轮(仅剩粉尘)",
				symbol, formatDecimalFixed(pos.USDC.Quantity, 4), si.LotSizeFilter.MinQuantity)
			continue
		}

		a.Log.Infof("%s 不在交易范围内，本轮退出 USDC 金额 %s", symbol, formatDecimalFixed(usdt, 2))
		o := a.NewOrphanExit(symbol, usdt)
		a.Uh.HandleFilled(o.ID, &o.Handle)
		doneC, errCh := o.Start()
		select {
		case <-doneC:
			time.Sleep(env.TCWaitIntervalDuration)
			a.GetTCPositions()
		case err := <-errCh:
			a.Log.Errorf("退出 %s 失败: %v", symbol, err)
			a.Uh.HandleFilledDelete(o.ID)
			time.Sleep(env.TCWaitIntervalDuration)
			a.GetTCPositions()
		}
	}
}

// HasSufficientAvailableBalance 计算多币种账户折算后的总可用资金，低于阈值时停止继续下单。
func (a *Account) HasSufficientAvailableBalance(scene string) bool {
	if a.MarginRate == nil {
		a.Log.Warnf("保证金率结果为空，跳过 %s 下单", scene)
		return false
	}

	env := GetEnv()
	if a.MarginRate.TotalAvailable.LessThan(env.MinAvailableUSD) {
		a.Log.Warnf("当前总可用资金 %s USD 已低于阈值 %s USD，%s 暂停下单",
			formatDecimalFixed(a.MarginRate.TotalAvailable, 2),
			formatDecimalFixed(env.MinAvailableUSD, 2),
			scene,
		)
		return false
	}

	a.Log.Debugf("可用资金检查通过: available=%s USD threshold=%s USD scene=%s",
		formatDecimalFixed(a.MarginRate.TotalAvailable, 2),
		formatDecimalFixed(env.MinAvailableUSD, 2),
		scene,
	)
	return true
}

// MarginRatioBeyond 保证金率过高时，优先减掉盈利最大的组合，释放保证金占用。
func (a *Account) MarginRatioBeyond() {
	a.Log.Info("保证金率偏高，开始减仓处理")
	env := GetEnv()
	for {
		symbol := a.GetMaxProfitSymbol()
		if symbol == "" {
			a.Log.Info("没有找到可减仓的盈利币种")
			return
		}

		a.Log.Infof("减仓中… 当前保证金率 %s%%", formatDecimalFixed(a.MarginRate.MarginRatio, 4))
		if a.MarginRate.MarginRatio.LessThan(env.MarginRatioReduceTarget) {
			a.Log.Info("保证金率已降到安全水位，减仓结束")
			break
		}

		if env.GetSymbol(symbol) == nil {
			a.Log.Warnf("没有 %s 的配置信息，无法减仓，退出减仓处理", symbol)
			break
		}

		extra := decimalMax(decimal.Zero, a.MarginRate.MarginRatio.Sub(env.MarginRatioReduceTarget))
		usdt := env.ReduceBaseUsdt.Add(extra.Mul(env.ReduceStepUsdtPerRatioPoint)).Round(0)
		a.Log.Debugf("减仓计算: ratio=%s extra=%s usdt=%s base=%s step=%s",
			formatDecimalFixed(a.MarginRate.MarginRatio, 4),
			formatDecimalFixed(extra, 2),
			formatDecimalFixed(usdt, 2),
			formatDecimalFixed(env.ReduceBaseUsdt, 2),
			formatDecimalFixed(env.ReduceStepUsdtPerRatioPoint, 2),
		)
		tc := a.TCPositions[symbol]
		maxCloseValue := tc.USDC.Quantity.Mul(tc.USDC.Price)

		q := decimal.Zero
		if usdt.GreaterThan(maxCloseValue) {
			q = tc.USDC.Quantity
		}

		if !a.HasSufficientAvailableBalance("高保证金率处理") {
			return
		}

		a.CreateTC(symbol, usdt, q)
		time.Sleep(env.ReduceWaitIntervalDuration)
		a.GetTCPositions()
	}
}

// MarginRatioSmall 保证金率偏低时，优先给当前总持仓价值最小的币种补仓。
func (a *Account) MarginRatioSmall() {
	a.Log.Info("保证金率偏低，开始补仓处理")
	env := GetEnv()
	for i := 0; i < env.MaxAddRounds; i++ {
		symbol := a.GetMinValueSymbol()
		if symbol == "" {
			a.Log.Info("没有找到适合补仓的币种")
			return
		}

		a.Log.Infof("补仓 当前保证金率 %s%%", formatDecimalFixed(a.MarginRate.MarginRatio, 4))
		a.Log.Debugf("补仓第 %d/%d 轮", i+1, env.MaxAddRounds)
		if a.MarginRate.MarginRatio.GreaterThan(env.MarginRatioAddTarget) {
			if a.MarginRate.MarginRatio.GreaterThan(env.MarginRatioReduceTrigger) {
				a.MarginRatioBeyond()
				return
			}
			break
		}

		symbolConfig := env.GetSymbol(symbol)
		if symbolConfig == nil {
			a.Log.Warnf("没有 %s 的配置信息，跳过", symbol)
			return
		}

		if !a.HasSufficientAvailableBalance("低保证金率处理") {
			return
		}

		tc := a.NewTC(symbol, symbolConfig.Usdt)
		a.Uh.HandleFilled(tc.ID, &tc.Handle)

		doneC, errCh := tc.Start()
		select {
		case <-doneC:
			time.Sleep(env.TCWaitIntervalDuration)
			a.GetTCPositions()
		case err := <-errCh:
			a.Log.Errorf("组合单执行失败: %v", err)
			a.Uh.HandleFilledDelete(tc.ID)
			time.Sleep(env.TCWaitIntervalDuration)
			a.GetTCPositions()
		}

		time.Sleep(env.LoopStepIntervalDuration)
	}
}
