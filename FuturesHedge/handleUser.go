package main

import (
	"context"
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
	uh.mu.Unlock()

	listenKey, err := Client.NewStartUserStreamService().Do(context.Background())
	if err != nil {
		return nil, err
	}
	uh.ListenKey = listenKey
	uh.wg.Add(1)
	go func() {
		defer uh.wg.Done()
		uh.RenewListenKey(10 * time.Minute)
	}()

	// 启动WebSocket连接的goroutine
	uh.wg.Add(1)
	go func() {
		defer uh.wg.Done()

		maxRetries := 5
		retryCount := 0

		for {
			select {
			case <-uh.stopCh: // 检查是否收到停止信号
				return
			default:
				// 尝试建立WebSocket连接
				doneC, stopC, err := futures.WsUserDataServe(listenKey, uh.UserHandler, func(err error) {
					log.Printf("用户数据流连接异常: %v", err)
				})
				if err != nil {
					log.Printf("启动用户数据流连接失败: %v", err)
					retryCount++
					if retryCount >= maxRetries {
						log.Printf("用户数据流连接达到最大重试次数")
						return
					}
					// 指数退避策略
					time.Sleep(time.Duration(retryCount) * time.Second)
					continue
				}

				log.Println("用户数据流连接已建立")
				// 成功建立连接，重置重试计数
				retryCount = 0

				// 启动一个goroutine来监控停止信号并关闭WebSocket
				go func() {
					<-uh.stopCh
					select {
					case stopC <- struct{}{}: // 发送停止信号给WebSocket
					default:
						// 如果stopC通道已经满了或者无法发送，则跳过
					}
				}()

				// 关闭完成通道（只关闭一次）
				uh.mu.Lock()
				if !uh.completeClosed {
					uh.completeClosed = true
					uh.mu.Unlock()
					close(uh.Complete)
				} else {
					uh.mu.Unlock()
				}

				// 等待WebSocket连接结束或收到停止信号
				select {
				case <-doneC:
					log.Println("用户数据流连接已关闭，正在尝试重连...")
					// 连接意外断开，尝试重连
					retryCount++
					if retryCount >= maxRetries {
						log.Printf("用户数据流连接达到最大重试次数")
						return
					}
					time.Sleep(time.Duration(retryCount) * time.Second)
					continue
				case <-uh.stopCh:
					// 收到停止信号，正常退出
					select {
					case stopC <- struct{}{}:
					default:
						// 如果stopC通道已经满了或者无法发送，则跳过
					}
					return
				}
			}
		}
	}()

	return uh.Complete, nil
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
					log.Printf("续签ListenKey失败: %v", err)
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
		log.Println("警告: 关闭UserHandler超时，可能存在未正确关闭的goroutine")
	}

	// 如果有ListenKey，则取消它
	uh.mu.Lock()
	listenKey := uh.ListenKey
	uh.ListenKey = ""
	uh.mu.Unlock()

	if listenKey != "" {
		err := Client.NewCloseUserStreamService().ListenKey(listenKey).Do(context.Background())
		if err != nil {
			log.Printf("关闭ListenKey时出错: %v", err)
			return err
		}
	}

	return nil
}

// UserHandler 处理用户数据事件
func (uh *UserHandler) UserHandler(event *futures.WsUserDataEvent) {
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
