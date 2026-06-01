package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/adshao/go-binance/v2/futures"
)

type FuturesUserHandlerHook struct {
	HandleMarginCall              *func(data *futures.WsUserDataMarginCall)       // 处理保证金不足通知
	HandleAccountUpdate           *func(data *futures.WsUserDataAccountUpdate)    // 处理账户更新
	HandleOrderTradeUpdate        *func(data *futures.WsUserDataOrderTradeUpdate) // 处理订单交易更新
	HandleOrderTradeUpdateNew     *func(data *futures.WsUserDataOrderTradeUpdate) // 处理订单交易更新 新订单
	HandleOrderTradeUpdateFilled  *func(data *futures.WsUserDataOrderTradeUpdate) // 处理订单交易更新 完全成交
	HandleOrderTradeUpdatePartial *func(data *futures.WsUserDataOrderTradeUpdate) // 处理订单交易更新 部分成交
}
type UserHandler struct {
	ListenKey string
	stopCh    chan struct{}  // 用于停止续签和WebSocket的统一通道
	wg        sync.WaitGroup // 等待goroutine完成
	mu        sync.Mutex     // 保护共享资源的访问
	isRunning bool           // 标记是否正在运行
	Complete  chan struct{}

	completeClosed bool // 标记 Complete 通道是否已关闭，防止重复关闭

	HandleFilledMap map[string]*func(data *futures.WsUserDataOrderTradeUpdate)
	Hook            *FuturesUserHandlerHook
}

// 监听用户合约数据
func NewFuturesUserHandler(c *futures.Client) *UserHandler {
	uh := &UserHandler{
		stopCh:          make(chan struct{}),
		wg:              sync.WaitGroup{},
		Hook:            &FuturesUserHandlerHook{},
		Complete:        make(chan struct{}),
		HandleFilledMap: make(map[string]*func(data *futures.WsUserDataOrderTradeUpdate)),
	}
	return uh
}

func (uh *UserHandler) Start() (chan struct{}, error) {
	uh.mu.Lock()
	if uh.isRunning {
		uh.mu.Unlock()
		return nil, nil // 已经在运行
	}
	uh.isRunning = true
	// 确保 stopCh 和 Complete 是全新的（支持 Close 后重新 Start）
	if uh.stopCh == nil {
		uh.stopCh = make(chan struct{})
	}
	if uh.Complete == nil {
		uh.Complete = make(chan struct{})
	}
	uh.completeClosed = false
	uh.mu.Unlock()

	// 启动WebSocket管理goroutine（内含无限重连 + 自动刷新listenKey）
	uh.wg.Add(1)
	go func() {
		defer uh.wg.Done()
		uh.runUserDataStream()
	}()

	return uh.Complete, nil
}

// runUserDataStream 管理用户数据流的完整生命周期：获取listenKey → 连接 → 断连重试（永不退出，直到 Close）
func (uh *UserHandler) runUserDataStream() {
	backoffBase := 1 * time.Second
	maxBackoff := 60 * time.Second
	consecutiveFailures := 0

	for {
		// 每次循环前检查是否已收到停止信号
		if uh.isStopChClosed() {
			return
		}

		// ---------- 1. 获取全新的 listenKey ----------
		listenKey, err := Client.NewStartUserStreamService().Do(context.Background())
		if err != nil {
			log.Printf("获取 Listen Key 失败: %v", err)
			consecutiveFailures++
			if !uh.sleepWithStopCheck(backoffDuration(backoffBase, consecutiveFailures, maxBackoff)) {
				return
			}
			continue
		}

		uh.mu.Lock()
		uh.ListenKey = listenKey
		uh.mu.Unlock()

		// ---------- 2. 启动该 listenKey 的定时续签 ----------
		renewDone := make(chan struct{})
		go func(key string) {
			defer close(renewDone)
			uh.renewListenKeyWithStop(key, 10*time.Minute)
		}(listenKey)

		// ---------- 3. 建立 WebSocket 连接 ----------
		doneC, stopC, err := futures.WsUserDataServe(listenKey, uh.UserHandler, func(err error) {
			log.Printf("WebSocket 连接异常: %v", err)
		})
		fmt.Printf("已获取 Listen Key: %s，正在建立 WebSocket 连接…\n", listenKey)
		if err != nil {
			log.Printf("启动 WebSocket 连接失败: %v", err)
			// 通知续签goroutine退出
			uh.cleanupListenKey(listenKey)
			<-renewDone

			consecutiveFailures++
			if !uh.sleepWithStopCheck(backoffDuration(backoffBase, consecutiveFailures, maxBackoff)) {
				return
			}
			continue
		}

		log.Println("WebSocket 已连接")
		consecutiveFailures = 0 // 成功后重置失败计数

		// ---------- 4. 通知外部"已就绪"（仅首次） ----------
		uh.mu.Lock()
		if !uh.completeClosed {
			uh.completeClosed = true
			close(uh.Complete)
		}
		uh.mu.Unlock()

		// ---------- 5. 等待连接断开或收到停止信号 ----------
		select {
		case <-doneC:
			log.Println("WebSocket 连接断开，准备重连…")
			// 清理当前 listenKey
			uh.cleanupListenKey(listenKey)
			// 通知续签goroutine退出
			select {
			case stopC <- struct{}{}:
			default:
			}
			<-renewDone
			// 短暂等待后重试
			if !uh.sleepWithStopCheck(1 * time.Second) {
				return
			}

		case <-uh.stopCh:
			log.Println("收到停止信号，关闭 WebSocket 连接")
			// 通知WebSocket和续签goroutine停止
			select {
			case stopC <- struct{}{}:
			default:
			}
			<-renewDone
			uh.cleanupListenKey(listenKey)
			return
		}
	}
}

// isStopChClosed 检查 stopCh 是否已被关闭
func (uh *UserHandler) isStopChClosed() bool {
	select {
	case <-uh.stopCh:
		return true
	default:
		return false
	}
}

// sleepWithStopCheck 休眠指定时长，期间若收到停止信号则提前返回 false
func (uh *UserHandler) sleepWithStopCheck(d time.Duration) bool {
	select {
	case <-uh.stopCh:
		return false
	case <-time.After(d):
		return true
	}
}

// cleanupListenKey 关闭指定的 listenKey（内部使用，忽略错误）
func (uh *UserHandler) cleanupListenKey(listenKey string) {
	if listenKey == "" {
		return
	}
	if err := Client.NewCloseUserStreamService().ListenKey(listenKey).Do(context.Background()); err != nil {
		log.Printf("关闭 Listen Key 失败: %v", err)
	}
	uh.mu.Lock()
	if uh.ListenKey == listenKey {
		uh.ListenKey = ""
	}
	uh.mu.Unlock()
}

// renewListenKeyWithStop 定时续签 listenKey，直到 stopCh 关闭
func (uh *UserHandler) renewListenKeyWithStop(listenKey string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if listenKey == "" {
				return
			}
			if err := Client.NewKeepaliveUserStreamService().ListenKey(listenKey).Do(context.Background()); err != nil {
				log.Printf("续签 Listen Key 失败: %v", err)
			}
		case <-uh.stopCh:
			return
		}
	}
}

// backoffDuration 计算指数退避时长，上限为 max
func backoffDuration(base time.Duration, failures int, max time.Duration) time.Duration {
	d := base
	for i := 1; i < failures && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	return d
}

// RenewListenKey 定时续签ListenKey
func (uh *UserHandler) RenewListenKey(renewInterval time.Duration) {
	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			uh.mu.Lock()
			listenKey := uh.ListenKey
			uh.mu.Unlock()

			if listenKey != "" {
				err := Client.NewKeepaliveUserStreamService().ListenKey(listenKey).Do(context.Background())
				if err != nil {
					log.Printf("续签 Listen Key 失败: %v", err)
				}
			}
		case <-uh.stopCh:
			// 收到停止信号，退出循环
			return
		}
	}
}

// Close 通用关闭方法
func (uh *UserHandler) Close() error {
	uh.mu.Lock()
	if !uh.isRunning {
		uh.mu.Unlock()
		return nil
	}
	uh.isRunning = false
	uh.mu.Unlock()

	// 发送停止信号
	uh.mu.Lock()
	if uh.stopCh != nil {
		close(uh.stopCh)
		uh.stopCh = nil
	}
	uh.mu.Unlock()

	// 使用带超时的等待，防止无限期阻塞
	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		uh.wg.Wait()
	}()

	select {
	case <-waitDone:
		// 正常完成等待
	case <-time.After(10 * time.Second):
		// 等待超时，记录警告信息
		log.Println("关闭 UserHandler 超时，可能有未正确关闭的后台任务")
	}

	// 如果有ListenKey，则取消它
	uh.mu.Lock()
	listenKey := uh.ListenKey
	uh.ListenKey = ""
	uh.mu.Unlock()

	if listenKey != "" {
		err := Client.NewCloseUserStreamService().ListenKey(listenKey).Do(context.Background())
		if err != nil {
			log.Printf("关闭 Listen Key 时出错: %v", err)
			return err
		}
	}

	return nil
}

// UserHandler 处理用户数据事件
func (uh *UserHandler) UserHandler(event *futures.WsUserDataEvent) {
	// fmt.Printf("[用户数据流] 收到事件: %s\n", event.Event)
	switch event.Event {
	case "MARGIN_CALL": // 处理保证金不足通知
		uh.handleMarginCall(&event.WsUserDataMarginCall)
	case "ACCOUNT_UPDATE": // 处理账户更新
		uh.handleAccountUpdate(&event.WsUserDataAccountUpdate)
	case "ORDER_TRADE_UPDATE": // 处理订单交易更新
		uh.handleOrderTradeUpdate(&event.WsUserDataOrderTradeUpdate)
	}
}

// handleMarginCall 处理保证金不足通知
func (uh *UserHandler) handleMarginCall(data *futures.WsUserDataMarginCall) {
	if uh.Hook.HandleMarginCall != nil {
		go (*uh.Hook.HandleMarginCall)(data)
	}
}

// handleAccountUpdate 处理账户更新
func (uh *UserHandler) handleAccountUpdate(data *futures.WsUserDataAccountUpdate) {
	if uh.Hook.HandleAccountUpdate != nil {
		go (*uh.Hook.HandleAccountUpdate)(data)
	}
}

// handleOrderTradeUpdate 处理订单交易更新
func (uh *UserHandler) handleOrderTradeUpdate(data *futures.WsUserDataOrderTradeUpdate) {
	if uh.Hook.HandleOrderTradeUpdate != nil {
		go (*uh.Hook.HandleOrderTradeUpdate)(data)
	}
	order := data.OrderTradeUpdate
	switch order.Status {
	case "NEW": // 新订单
		if uh.Hook.HandleOrderTradeUpdateNew != nil {
			go (*uh.Hook.HandleOrderTradeUpdateNew)(data)
		}
	case "FILLED": // 完全成交
		if uh.Hook.HandleOrderTradeUpdateFilled != nil {
			go (*uh.Hook.HandleOrderTradeUpdateFilled)(data)
		}
		if uh.HandleFilledMap[order.ClientOrderID] != nil {
			go (*uh.HandleFilledMap[order.ClientOrderID])(data)
			uh.HandleFilledDelete(order.ClientOrderID)
		}
	case "PARTIALLY_FILLED": // 部分成交
		if uh.Hook.HandleOrderTradeUpdatePartial != nil {
			go (*uh.Hook.HandleOrderTradeUpdatePartial)(data)
		}
	}
}

// 处理完全成交 指定ID
func (uh *UserHandler) HandleFilled(ID string, handle *func(data *futures.WsUserDataOrderTradeUpdate)) {
	if _, ok := uh.HandleFilledMap[ID]; !ok {
		uh.HandleFilledMap[ID] = handle
	}
}

// 删除处理完全成交 指定ID
func (uh *UserHandler) HandleFilledDelete(ID string) {
	delete(uh.HandleFilledMap, ID)
}
