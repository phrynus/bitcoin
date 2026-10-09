package exchange

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phrynus/plog"
)

// UserData 维护币安用户数据流（listenKey）的生命周期：自动创建、保活、断线重连，
// 并将 ORDER_TRADE_UPDATE 事件分发给通过 Subscribe 注册的回调。
// 由 NewUserData 创建并立即在后台运行；Close 之后不可再用。
type UserData struct {
	proxy string
	rest  *restClient

	mu        sync.Mutex
	listenKey string
	conn      *websocket.Conn
	ready     bool
	closed    bool
	stop      chan struct{}

	events chan *WsUserDataEvent // 待分发的事件队列，由单个 goroutine 顺序消费

	subMu sync.Mutex
	subs  map[string]*subEntry // 订单事件订阅，键为「客户端订单号-状态」
}

// NewUserData 创建并启动 UserData，随后最多等待 30 秒直到数据流就绪（超时不报错）。
// 密钥与代理取全局共享配置（见 Init），proxy 为空表示直连用户数据流。
// 接入地址取当前实盘/模拟盘配置（见 Init）。
// 调用方可自行用 WaitReady 确认就绪状态。
func NewUserData() *UserData {
	_, _, proxy := auth()
	u := &UserData{
		proxy:  proxy,
		rest:   newRestClient(),
		stop:   make(chan struct{}),
		events: make(chan *WsUserDataEvent, userDataEventBuffer),
		subs:   make(map[string]*subEntry),
	}

	go u.run()
	go u.dispatch()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = u.WaitReady(ctx)
	return u
}

// WaitReady 阻塞直到数据流建立就绪，或 ctx 结束、UserData 关闭。
// 若 UserData 已关闭则返回错误。
func (u *UserData) WaitReady(ctx context.Context) error {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		u.mu.Lock()
		ready := u.ready && !u.closed
		u.mu.Unlock()
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-u.stop:
			return ErrUserDataClosed
		case <-t.C:
		}
	}
}

// dispatch 由单独的 goroutine 顺序消费事件队列，避免每条推送都新建 goroutine。
func (u *UserData) dispatch() {
	for {
		select {
		case ev := <-u.events:
			u.userHandler(ev)
		case <-u.stop:
			return
		}
	}
}

// Close 关闭数据流并释放资源：停止重连、清空订阅、关闭连接并异步注销 listenKey。可重复调用。
func (u *UserData) Close() {
	u.mu.Lock()
	if u.closed {
		u.mu.Unlock()
		return
	}
	u.closed = true
	close(u.stop)
	conn := u.conn
	u.conn = nil
	u.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	u.clearSubs()
	go u.closeListenKey()
}

func (u *UserData) run() {
	backoff := time.Second
	for {
		if u.isClosed() {
			return
		}

		if u.getListenKey() == "" {
			lk, err := u.newListenKey()
			if err != nil {
				if !u.sleep(backoff) {
					return
				}
				backoff = nextBackoff(backoff)
				continue
			}
			u.setListenKey(lk)
			backoff = time.Second
		}

		conn, err := u.dial()
		if err != nil {
			u.clearListenKey()
			if !u.sleep(backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		u.mu.Lock()
		if u.closed {
			u.mu.Unlock()
			_ = conn.Close()
			return
		}
		u.conn = conn
		u.ready = true
		u.mu.Unlock()
		backoff = time.Second

		keepaliveStop := make(chan struct{})
		go u.keepalive(conn, keepaliveStop)

		u.readLoop(conn)

		close(keepaliveStop)
		u.clearConn()

		if u.isClosed() {
			return
		}

		u.clearListenKey()
	}
}

// dial 以 streamURL 拼接 listenKey 建立用户数据流连接，并注册 pong 处理器以续期读超时。
func (u *UserData) dial() (*websocket.Conn, error) {
	lk := u.getListenKey()
	if lk == "" {
		return nil, errors.New("listenKey 为空")
	}

	d := websocket.Dialer{HandshakeTimeout: userDataDialTimeout}
	if u.proxy != "" {
		p, err := url.Parse(u.proxy)
		if err != nil {
			return nil, err
		}
		d.Proxy = http.ProxyURL(p)
	}

	conn, _, err := d.Dial(currentStreamURL()+lk, nil)
	if err != nil {
		return nil, err
	}
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(userDataPongWait))
	})
	return conn, nil
}

// readLoop 持续读取推送消息直到连接断开或读超时，收到的消息交给 handleMessage 分发。
func (u *UserData) readLoop(conn *websocket.Conn) {
	for {
		_ = conn.SetReadDeadline(time.Now().Add(userDataPongWait))
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		u.handleMessage(data)
	}
}

// handleMessage 解析推送消息：listenKey 失效时断开连接触发重建，其余事件送入队列顺序分发。
func (u *UserData) handleMessage(data []byte) {
	var event WsUserDataEvent
	if err := json.Unmarshal(data, &event); err != nil {
		plog.Warnf("exchange: 解析用户数据流消息失败: %v", err)
		return
	}
	if event.Event == "listenKeyExpired" {
		u.clearListenKey()
		u.closeConn()
		return
	}
	select {
	case u.events <- &event:
	case <-u.stop:
	}
}

func (u *UserData) newListenKey() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), userDataRenewTimeout)
	defer cancel()
	return u.rest.startListenKey(ctx)
}

// closeListenKey 注销 listenKey（尽力而为），使用独立的短超时避免拖慢 Close。
func (u *UserData) closeListenKey() {
	lk := u.getListenKey()
	if lk == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), userDataCloseTimeout)
	defer cancel()
	_ = u.rest.closeListenKey(ctx, lk)
}

// keepalive 周期性发送 ping 帧探测连接，并按用户数据流要求定期续期 listenKey。
func (u *UserData) keepalive(conn *websocket.Conn, stop chan struct{}) {
	ping := time.NewTicker(userDataPingPeriod)
	defer ping.Stop()
	renew := time.NewTicker(userDataKeepaliveInterval)
	defer renew.Stop()
	for {
		select {
		case <-ping.C:
			_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(userDataWriteWait))
		case <-renew.C:
			u.renewListenKey()
		case <-stop:
			return
		}
	}
}

// renewListenKey 续期 listenKey；失败时清除 listenKey 并断开连接，触发整体重建。
func (u *UserData) renewListenKey() {
	lk := u.getListenKey()
	if lk == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), userDataRenewTimeout)
	defer cancel()
	if err := u.rest.keepaliveListenKey(ctx, lk); err != nil {
		u.clearListenKey()
		u.closeConn()
	}
}

func (u *UserData) getListenKey() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.listenKey
}

func (u *UserData) setListenKey(lk string) {
	u.mu.Lock()
	u.listenKey = lk
	u.mu.Unlock()
}

func (u *UserData) clearListenKey() {
	u.mu.Lock()
	u.listenKey = ""
	u.mu.Unlock()
}

func (u *UserData) isClosed() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.closed
}

// clearConn 标记连接未就绪并关闭当前连接。
func (u *UserData) clearConn() {
	u.mu.Lock()
	conn := u.conn
	u.conn = nil
	u.ready = false
	u.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

// closeConn 关闭当前连接以唤醒 readLoop，触发外层重连。
func (u *UserData) closeConn() {
	u.mu.Lock()
	conn := u.conn
	u.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

func (u *UserData) sleep(d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-u.stop:
		return false
	}
}
