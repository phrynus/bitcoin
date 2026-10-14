package exchange

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phrynus/plog"
)

// WsApi 是基于币安 WebSocket API（ws-fapi）的请求/响应客户端：单连接内按请求 id 匹配响应，
// 内置心跳、断线自动重连、代理支持与 HMAC-SHA256 签名。
// 由 NewWsApi 创建并自动在后台维护连接，Close 之后不可再用。可并发使用。
type WsApi struct {
	apiKey string
	secret string
	url    string
	proxy  string

	mu      sync.Mutex
	conn    *websocket.Conn
	ready   chan struct{} // 连接就绪信号：连接建立时关闭，断开后重建
	pending map[string]chan *wsResponse
	seq     int64
	closed  bool
	stop    chan struct{}

	writeMu sync.Mutex
}

// WsError 表示币安返回的业务错误，Code 为错误码，Msg 为错误描述。
type WsError struct {
	Code int    `json:"code"` // 错误码
	Msg  string `json:"msg"`  // 错误描述
}

// Error 实现 error 接口，返回错误码与错误信息。
func (e *WsError) Error() string {
	return fmt.Sprintf("binance ws 错误 code=%d msg=%s", e.Code, e.Msg)
}

type wsResponse struct {
	ID         string          `json:"id"`
	Status     int             `json:"status"`
	Result     json.RawMessage `json:"result"`
	RateLimits []*wsRateLimit  `json:"rateLimits"`
	Error      *WsError        `json:"error"`

	err error // 本地错误（连接断开、已关闭等），非币安返回
}

type wsRateLimit struct {
	RateLimitType string `json:"rateLimitType"`
	Interval      string `json:"interval"`
	IntervalNum   int    `json:"intervalNum"`
	Limit         int    `json:"limit"`
	Count         int    `json:"count"`
}

type wsRequest struct {
	ID     string         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

// NewWsApi 创建 WsApi 并启动后台连接，内部最多等待 30 秒直到连接就绪（超时不报错）。
// 密钥与代理取全局共享配置（见 Init），proxy 为空表示直连 WebSocket。
// 接入地址取当前实盘/模拟盘配置（见 Init）。
func NewWsApi() *WsApi {
	apiKey, secret, proxy := auth()
	w := &WsApi{
		apiKey:  apiKey,
		secret:  secret,
		url:     currentTradeURL(),
		proxy:   proxy,
		ready:   make(chan struct{}),
		pending: make(map[string]chan *wsResponse),
		stop:    make(chan struct{}),
	}
	go w.run()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := w.WaitReady(ctx); err != nil {
		plog.Warnf("[连接] 等待 WebSocket 连接就绪超时/失败: %v", err)
	}
	return w
}

// Ping 发送 ping 请求以检测连接可用性。连接不可用或 ctx 结束时返回错误。
func (w *WsApi) Ping(ctx context.Context) error {
	_, err := call[any](w, ctx, "ping", nil, false)
	return err
}

// WaitReady 阻塞直到 WebSocket 连接建立，或 ctx 结束、WsApi 关闭。
// 若 WsApi 已关闭则返回错误。
func (w *WsApi) WaitReady(ctx context.Context) error {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		w.mu.Lock()
		ready := w.conn != nil && !w.closed
		w.mu.Unlock()
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.stop:
			return ErrClosed
		case <-t.C:
		}
	}
}

func (w *WsApi) request(ctx context.Context, method string, params map[string]any, signed bool) (*wsResponse, error) {
	req := &wsRequest{ID: w.nextID(), Method: method}
	if signed {
		req.Params = w.sign(params)
	} else if len(params) > 0 {
		req.Params = params
	}

	ch := make(chan *wsResponse, 1)
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil, ErrClosed
	}
	w.pending[req.ID] = ch
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.pending, req.ID)
		w.mu.Unlock()
	}()

	plog.Debugf("[请求] 发起请求 方法=%s id=%s 签名=%v", method, req.ID, signed)

	if err := w.send(req); err != nil {
		plog.Warnf("[请求] 发送请求失败 方法=%s id=%s: %v", method, req.ID, err)
		return nil, err
	}

	select {
	case resp := <-ch:
		if resp.err != nil {
			plog.Warnf("[请求] 请求返回本地错误 方法=%s id=%s: %v", method, req.ID, resp.err)
			return nil, resp.err
		}
		if resp.Error != nil {
			plog.Warnf("[请求] 请求返回币安错误 方法=%s id=%s: %v", method, req.ID, resp.Error)
			return nil, resp.Error
		}
		plog.Debugf("[请求] 请求成功 方法=%s id=%s", method, req.ID)
		return resp, nil
	case <-ctx.Done():
		plog.Warnf("[请求] 请求超时/取消 方法=%s id=%s: %v", method, req.ID, ctx.Err())
		return nil, ctx.Err()
	case <-w.stop:
		return nil, ErrClosed
	}
}

func call[T any](w *WsApi, ctx context.Context, method string, params map[string]any, signed bool) (T, error) {
	var zero T
	resp, err := w.request(ctx, method, params, signed)
	if err != nil {
		return zero, err
	}
	if err := json.Unmarshal(resp.Result, &zero); err != nil {
		return zero, fmt.Errorf("解析 %s 响应失败: %w", method, err)
	}
	return zero, nil
}

// Close 关闭连接并释放资源，同时唤醒所有等待中的请求使其返回“已关闭”错误。可重复调用。
func (w *WsApi) Close() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	close(w.stop)
	conn := w.conn
	w.conn = nil
	w.mu.Unlock()
	plog.Debugf("[连接] 关闭 WebSocket 交易客户端")
	if conn != nil {
		_ = conn.Close()
	}
}

func (w *WsApi) run() {
	backoff := time.Second
	for {
		w.mu.Lock()
		closed := w.closed
		w.mu.Unlock()
		if closed {
			return
		}

		if err := w.dial(); err != nil {
			plog.Warnf("[连接] 建立 WebSocket 连接失败, %v 后重试: %v", backoff, err)
			select {
			case <-time.After(backoff):
			case <-w.stop:
				return
			}
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			continue
		}
		backoff = time.Second

		pingStop := make(chan struct{})
		go w.keepalive(pingStop)
		w.readLoop()
		close(pingStop)
		plog.Debugf("[连接] WebSocket 连接断开, 唤醒在途请求并重建连接")
		w.failPending(ErrConnReset)
		w.clearConn()
	}
}

func (w *WsApi) dial() error {
	d := websocket.Dialer{HandshakeTimeout: wsDialTimeout}
	if w.proxy != "" {
		u, err := url.Parse(w.proxy)
		if err != nil {
			return err
		}
		d.Proxy = http.ProxyURL(u)
	}
	conn, _, err := d.Dial(w.url, nil)
	if err != nil {
		return err
	}
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(wsPongWait)) })

	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		_ = conn.Close()
		return ErrClosed
	}
	w.conn = conn
	close(w.ready)
	w.mu.Unlock()
	plog.Infof("[连接] WebSocket 连接已就绪 %s", w.url)
	return nil
}

// clearConn 清除已断开的连接并重建就绪信号，等待下次重连。
func (w *WsApi) clearConn() {
	w.mu.Lock()
	w.conn = nil
	w.ready = make(chan struct{})
	w.mu.Unlock()
}

func (w *WsApi) readLoop() {
	for {
		w.mu.Lock()
		conn := w.conn
		w.mu.Unlock()
		if conn == nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
		_, data, err := conn.ReadMessage()
		if err != nil {
			plog.Debugf("[连接] WebSocket 读取结束: %v", err)
			return
		}
		var resp wsResponse
		if err := json.Unmarshal(data, &resp); err != nil || resp.ID == "" {
			continue
		}
		w.mu.Lock()
		ch := w.pending[resp.ID]
		delete(w.pending, resp.ID)
		w.mu.Unlock()
		if ch != nil {
			ch <- &resp
		}
	}
}

func (w *WsApi) keepalive(stop chan struct{}) {
	t := time.NewTicker(wsPingPeriod)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			w.mu.Lock()
			conn := w.conn
			w.mu.Unlock()
			if conn == nil {
				return
			}
			_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait))
		case <-stop:
			return
		}
	}
}

// send 发送请求；连接尚未就绪时等待其建立（最多 wsWriteWait），超时或关闭时返回错误。
func (w *WsApi) send(req *wsRequest) error {
	deadline := time.Now().Add(wsWriteWait)
	for {
		w.mu.Lock()
		conn := w.conn
		ready := w.ready
		w.mu.Unlock()
		if conn != nil {
			return w.write(conn, req)
		}

		remain := time.Until(deadline)
		if remain <= 0 {
			return ErrConnUnavailable
		}

		t := time.NewTimer(remain)
		select {
		case <-ready:
		case <-w.stop:
			t.Stop()
			return ErrClosed
		case <-t.C:
			t.Stop()
			return ErrConnUnavailable
		}
		t.Stop()
	}
}

// write 在写锁内完成一次带超时的 JSON 写入。
func (w *WsApi) write(conn *websocket.Conn, req *wsRequest) error {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
	return conn.WriteJSON(req)
}

func (w *WsApi) nextID() string {
	w.mu.Lock()
	w.seq++
	id := strconv.FormatInt(w.seq, 10)
	w.mu.Unlock()
	return id
}

func (w *WsApi) sign(params map[string]any) map[string]any {
	p := make(map[string]any, len(params)+3)
	for k, v := range params {
		p[k] = v
	}
	p["apiKey"] = w.apiKey
	p["timestamp"] = time.Now().UnixMilli() - timeOffset()
	mac := hmac.New(sha256.New, []byte(w.secret))
	mac.Write([]byte(buildQuery(p)))
	p["signature"] = hex.EncodeToString(mac.Sum(nil))
	return p
}

func (w *WsApi) failPending(err error) {
	w.mu.Lock()
	if len(w.pending) > 0 {
		plog.Warnf("[请求] 连接断开, 唤醒在途请求 数量=%d", len(w.pending))
	}
	for id, ch := range w.pending {
		delete(w.pending, id)
		ch <- &wsResponse{ID: id, err: err}
	}
	w.mu.Unlock()
}
