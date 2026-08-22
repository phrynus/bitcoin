package exchange

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"main/logger"
)

// Binance USD-M 合约 WebSocket API 客户端(请求-响应式)。
// 文档: https://developers.binance.com/zh-CN/docs/products/derivatives-trading-usds-futures/websocket-api-general-info
//
// 特性: 单连接复用 + 自动重连; id 关联请求/响应, 支持并发; 自动签名(HMAC SHA256)。
const (
	wsApiURL      = "wss://ws-fapi.binance.com/ws-fapi/v1" // 主网 WS API 地址
	wsDialTimeout = 10 * time.Second                       // 握手超时
	wsWriteWait   = 10 * time.Second                       // 写超时(发送请求/心跳帧)
	wsPongWait    = 60 * time.Second                       // 等待服务端 Pong 的超时, 超过视为断线
	wsPingPeriod  = 50 * time.Second                       // 心跳 Ping 帧发送间隔(须小于 wsPongWait)

	timeSyncInterval = 5 * time.Minute // 服务器时间校准间隔(签名 timestamp 修正, 首次必校准)
	wsSyncTimeout    = 5 * time.Second // 单次服务器时间校准超时
)

// WsApi WS API 客户端。
type WsApi struct {
	apiKey string
	secret string
	url    string
	proxy  string

	mu      sync.Mutex
	conn    *websocket.Conn
	pending map[string]chan *WsResponse // 请求 id -> 响应通道
	seq     int64                       // 请求 id 自增
	closed  bool
	stop    chan struct{}

	writeMu    sync.Mutex   // 串行化写入
	timeOffset atomic.Int64 // 服务器时间 - 本地时间(毫秒), 用于修正签名 timestamp
	lastSync   atomic.Int64 // 上次服务器时间校准时间(UnixNano)
}

// WsError 交易所返回的业务错误。
type WsError struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

func (e *WsError) Error() string {
	return fmt.Sprintf("binance ws 错误 code=%d msg=%s", e.Code, e.Msg)
}

// WsResponse WS API 原始响应。
type WsResponse struct {
	ID         string          `json:"id"`
	Status     int             `json:"status"`
	Result     json.RawMessage `json:"result"`
	RateLimits []*WsRateLimit  `json:"rateLimits"`
	Error      *WsError        `json:"error"`
}

// WsRateLimit 请求权重限速信息(随响应返回)。
type WsRateLimit struct {
	RateLimitType string `json:"rateLimitType"` // 限速类型(如 REQUEST_WEIGHT)
	Interval      string `json:"interval"`      // 时间间隔(如 MINUTE)
	IntervalNum   int    `json:"intervalNum"`   // 间隔数量
	Limit         int    `json:"limit"`         // 上限
	Count         int    `json:"count"`         // 当前已用
}

type wsRequest struct {
	ID     string         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

// NewWsApi 创建 WS API 客户端, 后台自动连接与重连。
func NewWsApi(apiKey, secret, proxy string) *WsApi {
	w := &WsApi{
		apiKey:  apiKey,
		secret:  secret,
		url:     wsApiURL,
		proxy:   proxy,
		pending: make(map[string]chan *WsResponse),
		stop:    make(chan struct{}),
	}
	go w.run()
	return w
}

// Ping 连通性测试。
func (w *WsApi) Ping(ctx context.Context) error {
	_, err := Call[any](w, ctx, "ping", nil, false)
	return err
}

// WsTime 服务器时间(time)。
type WsTime struct {
	ServerTime int64 `json:"serverTime"` // 服务器当前时间(毫秒)
}

// Time 查询服务器时间(公开接口, 无需签名)。
func (w *WsApi) Time(ctx context.Context) (*WsTime, error) {
	return Call[*WsTime](w, ctx, "time", nil, false)
}

// SyncServerTime 用服务器时间校准本地时钟偏移, 供签名 timestamp 使用。
func (w *WsApi) SyncServerTime(ctx context.Context) error {
	t, err := w.Time(ctx)
	if err != nil {
		return err
	}
	if t == nil || t.ServerTime == 0 {
		return errors.New("服务器时间响应为空")
	}
	offset := t.ServerTime - time.Now().UnixMilli()
	w.timeOffset.Store(offset)
	w.lastSync.Store(time.Now().UnixNano())
	logger.Debugf("WsApi 服务器时间已校准, 偏移 %d ms", offset)
	return nil
}

// Request 发送 WS API 请求并等待响应。signed=true 时自动附加 apiKey/timestamp/signature。
func (w *WsApi) Request(ctx context.Context, method string, params map[string]any, signed bool) (*WsResponse, error) {
	// 签名请求的 timestamp 依赖准确的服务器时间, 定期校准本地时钟偏移(首次必校准)。
	if signed && time.Since(time.Unix(0, w.lastSync.Load())) > timeSyncInterval {
		syncCtx, cancel := context.WithTimeout(ctx, wsSyncTimeout)
		err := w.SyncServerTime(syncCtx)
		cancel()
		if err != nil {
			logger.Warnf("校准服务器时间失败: %v", err)
		}
	}

	var reqParams map[string]any
	if signed {
		reqParams = w.sign(params)
	} else if len(params) > 0 {
		reqParams = params
	}
	resp, err := w.doRequest(ctx, &wsRequest{ID: w.nextID(), Method: method, Params: reqParams})
	if err != nil {
		// 时间戳超出 recvWindow(-1021): 重新校准后以新时间戳重试一次。
		if signed && isTimestampError(err) {
			logger.Warnf("签名时间戳超出 recvWindow, 校准服务器时间后重试一次")
			syncCtx, cancel := context.WithTimeout(ctx, wsSyncTimeout)
			syncErr := w.SyncServerTime(syncCtx)
			cancel()
			if syncErr != nil {
				logger.Warnf("校准服务器时间失败: %v", syncErr)
				return nil, err
			}
			return w.doRequest(ctx, &wsRequest{ID: w.nextID(), Method: method, Params: w.sign(params)})
		}
		return nil, err
	}
	return resp, nil
}

// doRequest 注册请求到 pending 并发送, 等待响应返回(内部封装)。
func (w *WsApi) doRequest(ctx context.Context, req *wsRequest) (*WsResponse, error) {
	ch := make(chan *WsResponse, 1)
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil, errors.New("WsApi 已关闭")
	}
	w.pending[req.ID] = ch
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.pending, req.ID)
		w.mu.Unlock()
	}()

	if err := w.send(req); err != nil {
		return nil, err
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-w.stop:
		return nil, errors.New("WsApi 已关闭")
	}
}

// isTimestampError 判断是否为时间戳超出 recvWindow 的业务错误(-1021)。
func isTimestampError(err error) bool {
	var we *WsError
	return errors.As(err, &we) && we.Code == -1021
}

// Call 发送请求并把 result 解析为类型 T。通用封装, 具体方法见 ws_api_account.go / ws_api_order.go。
func Call[T any](w *WsApi, ctx context.Context, method string, params map[string]any, signed bool) (T, error) {
	var zero T
	resp, err := w.Request(ctx, method, params, signed)
	if err != nil {
		return zero, err
	}
	if err := json.Unmarshal(resp.Result, &zero); err != nil {
		return zero, fmt.Errorf("解析 %s 响应失败: %w", method, err)
	}
	return zero, nil
}

// Close 关闭连接。
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
	if conn != nil {
		_ = conn.Close()
	}
}

// ─────────────── 内部实现 ───────────────

// run 后台连接维护: 断线自动重连。
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
			logger.Warnf("WsApi 连接失败: %v, %v 后重试", err, backoff)
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
		w.failPending(errors.New("WsApi 连接断开, 请重试"))
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
		return errors.New("WsApi 已关闭")
	}
	w.conn = conn
	w.mu.Unlock()
	logger.Infof("WsApi 已连接: %s", w.url)
	return nil
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
			return
		}
		var resp WsResponse
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

// keepalive 定期发送 WebSocket Ping 帧, 保持连接活跃。
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

// send 等待连接可用后发送请求。
func (w *WsApi) send(req *wsRequest) error {
	deadline := time.Now().Add(wsWriteWait)
	for {
		w.mu.Lock()
		conn := w.conn
		w.mu.Unlock()
		if conn != nil {
			w.writeMu.Lock()
			defer w.writeMu.Unlock()
			_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			return conn.WriteJSON(req)
		}
		if time.Now().After(deadline) {
			return errors.New("WsApi 连接不可用")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (w *WsApi) nextID() string {
	w.mu.Lock()
	w.seq++
	id := strconv.FormatInt(w.seq, 10)
	w.mu.Unlock()
	return id
}

// sign 签名请求参数: apiKey + timestamp + signature(HMAC SHA256)。
func (w *WsApi) sign(params map[string]any) map[string]any {
	p := make(map[string]any, len(params)+3)
	for k, v := range params {
		p[k] = v
	}
	p["apiKey"] = w.apiKey
	p["timestamp"] = time.Now().UnixMilli() + w.timeOffset.Load()
	mac := hmac.New(sha256.New, []byte(w.secret))
	mac.Write([]byte(buildQuery(p)))
	p["signature"] = hex.EncodeToString(mac.Sum(nil))
	return p
}

// failPending 连接断开时, 让所有挂起请求立即返回错误。
func (w *WsApi) failPending(err error) {
	w.mu.Lock()
	for id, ch := range w.pending {
		delete(w.pending, id)
		ch <- &WsResponse{ID: id, Error: &WsError{Code: -1, Msg: err.Error()}}
	}
	w.mu.Unlock()
}

// buildQuery 按 key 排序生成 URL 编码的查询串(用于签名)。
func buildQuery(params map[string]any) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(url.QueryEscape(k))
		sb.WriteByte('=')
		sb.WriteString(url.QueryEscape(encodeValue(params[k])))
	}
	return sb.String()
}

// encodeValue 将参数值转为签名用的字符串(嵌套结构用 JSON 编码)。
func encodeValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}
