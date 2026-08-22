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
	"time"

	"github.com/gorilla/websocket"
)

//

const (
	wsApiURL      = "wss://ws-fapi.binance.com/ws-fapi/v1"
	wsDialTimeout = 10 * time.Second
	wsWriteWait   = 10 * time.Second
	wsPongWait    = 60 * time.Second
	wsPingPeriod  = 50 * time.Second
)

type WsApi struct {
	apiKey string
	secret string
	url    string
	proxy  string

	mu      sync.Mutex
	conn    *websocket.Conn
	pending map[string]chan *wsResponse
	seq     int64
	closed  bool
	stop    chan struct{}

	writeMu sync.Mutex
}

type WsError struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

func (e *WsError) Error() string {
	return fmt.Sprintf("binance ws 错误 code=%d msg=%s", e.Code, e.Msg)
}

type wsResponse struct {
	ID         string          `json:"id"`
	Status     int             `json:"status"`
	Result     json.RawMessage `json:"result"`
	RateLimits []*wsRateLimit  `json:"rateLimits"`
	Error      *WsError        `json:"error"`
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

func NewWsApi(apiKey, secret, proxy string) *WsApi {
	w := &WsApi{
		apiKey:  apiKey,
		secret:  secret,
		url:     wsApiURL,
		proxy:   proxy,
		pending: make(map[string]chan *wsResponse),
		stop:    make(chan struct{}),
	}
	go w.run()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = w.WaitReady(ctx)
	return w
}

func (w *WsApi) Ping(ctx context.Context) error {
	_, err := call[any](w, ctx, "ping", nil, false)
	return err
}

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
			return errors.New("WsApi 已关闭")
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

func (w *WsApi) sign(params map[string]any) map[string]any {
	p := make(map[string]any, len(params)+3)
	for k, v := range params {
		p[k] = v
	}
	p["apiKey"] = w.apiKey
	p["timestamp"] = time.Now().UnixMilli() - exc.timeOffset
	mac := hmac.New(sha256.New, []byte(w.secret))
	mac.Write([]byte(buildQuery(p)))
	p["signature"] = hex.EncodeToString(mac.Sum(nil))
	return p
}

func (w *WsApi) failPending(err error) {
	w.mu.Lock()
	for id, ch := range w.pending {
		delete(w.pending, id)
		ch <- &wsResponse{ID: id, Error: &WsError{Code: -1, Msg: err.Error()}}
	}
	w.mu.Unlock()
}

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
