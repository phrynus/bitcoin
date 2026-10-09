package exchange

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// RestError 表示币安 REST 接口返回的业务错误，Code 为错误码，Msg 为错误描述。
type RestError struct {
	Code int    `json:"code"` // 错误码
	Msg  string `json:"msg"`  // 错误描述
}

// Error 实现 error 接口，返回错误码与错误信息。
func (e *RestError) Error() string {
	return fmt.Sprintf("binance rest 错误 code=%d msg=%s", e.Code, e.Msg)
}

// restClient 是基于币安 U 本位合约 REST（fapi）的轻量客户端，只实现本包用到的接口：
// 连通性探测、服务器时间、交易对信息与 listenKey 管理。
// 支持代理与 HMAC-SHA256 签名，可并发使用。
type restClient struct {
	apiKey string
	secret string
	base   string
	http   *http.Client
}

// newRestClient 创建 REST 客户端：密钥与代理取全局共享配置（见 Init），
// 接入地址取当前实盘/模拟盘配置（见 Init）。
func newRestClient() *restClient {
	apiKey, secret, proxy := auth()
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}
	return &restClient{
		apiKey: apiKey,
		secret: secret,
		base:   currentRestURL(),
		http:   &http.Client{Transport: transport, Timeout: restTimeout},
	}
}

// do 发起一次 REST 请求并返回原始响应体。
// signed 为 true 时附加 timestamp 与 HMAC-SHA256 签名（timestamp 会按 Exchange 的服务器时间偏移校正）；
// apiKey 非空时始终携带 X-MBX-APIKEY 头。
// HTTP 状态非 200 且响应体为币安错误结构时返回 *RestError。
func (c *restClient) do(ctx context.Context, method, path string, params map[string]any, signed bool) (json.RawMessage, error) {
	q := make(url.Values, len(params)+3)
	for k, v := range params {
		q.Set(k, encodeValue(v))
	}
	if signed {
		ts := time.Now().UnixMilli() - timeOffset()
		q.Set(restTimestampKey, strconv.FormatInt(ts, 10))
		q.Set(restRecvWindowKey, strconv.Itoa(restRecvWindow))

		mac := hmac.New(sha256.New, []byte(c.secret))
		mac.Write([]byte(q.Encode()))
		q.Set(restSignKey, hex.EncodeToString(mac.Sum(nil)))
	}

	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	if c.apiKey != "" {
		req.Header.Set(restAPIKeyHeader, c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, restMaxBodySize))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		var re RestError
		if json.Unmarshal(body, &re) == nil && re.Code != 0 {
			return nil, &re
		}
		return nil, fmt.Errorf("binance rest %s %s 失败: HTTP %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// ping 探测 REST 接口连通性，用于确认网络与代理配置可用。
func (c *restClient) ping(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodGet, "/fapi/v1/ping", nil, false)
	return err
}

// serverTime 查询币安服务器时间（毫秒）。
func (c *restClient) serverTime(ctx context.Context) (int64, error) {
	data, err := c.do(ctx, http.MethodGet, "/fapi/v1/time", nil, false)
	if err != nil {
		return 0, err
	}
	var res struct {
		ServerTime int64 `json:"serverTime"` // 服务器时间（毫秒）
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return 0, fmt.Errorf("解析服务器时间失败: %w", err)
	}
	if res.ServerTime == 0 {
		return 0, errors.New("币安未返回服务器时间")
	}
	return res.ServerTime, nil
}

// exchangeInfo 查询全部合约的交易规则。
func (c *restClient) exchangeInfo(ctx context.Context) (*exchangeInfoResp, error) {
	data, err := c.do(ctx, http.MethodGet, "/fapi/v1/exchangeInfo", nil, false)
	if err != nil {
		return nil, err
	}
	res := new(exchangeInfoResp)
	if err := json.Unmarshal(data, res); err != nil {
		return nil, fmt.Errorf("解析交易对信息失败: %w", err)
	}
	return res, nil
}

// startListenKey 创建用户数据流的 listenKey，需要 API 密钥。
func (c *restClient) startListenKey(ctx context.Context) (string, error) {
	data, err := c.do(ctx, http.MethodPost, "/fapi/v1/listenKey", nil, false)
	if err != nil {
		return "", err
	}
	var res struct {
		ListenKey string `json:"listenKey"` // 用户数据流的 listenKey
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return "", fmt.Errorf("解析 listenKey 失败: %w", err)
	}
	if res.ListenKey == "" {
		return "", errors.New("币安未返回 listenKey")
	}
	return res.ListenKey, nil
}

// keepaliveListenKey 续期 listenKey，需要在 60 分钟内至少调用一次，需要 API 密钥。
func (c *restClient) keepaliveListenKey(ctx context.Context, listenKey string) error {
	_, err := c.do(ctx, http.MethodPut, "/fapi/v1/listenKey", map[string]any{"listenKey": listenKey}, false)
	return err
}

// closeListenKey 注销 listenKey，需要 API 密钥。
func (c *restClient) closeListenKey(ctx context.Context, listenKey string) error {
	_, err := c.do(ctx, http.MethodDelete, "/fapi/v1/listenKey", map[string]any{"listenKey": listenKey}, false)
	return err
}

// exchangeInfoResp 是 GET /fapi/v1/exchangeInfo 的响应，仅保留本包需要的字段。
type exchangeInfoResp struct {
	Symbols []exchangeSymbol `json:"symbols"` // 全部合约
}

// exchangeSymbol 是 exchangeInfo 响应中单个合约的交易规则。
// Filters 为币安原始过滤规则列表，每项含 filterType 及对应的字符串字段。
type exchangeSymbol struct {
	Symbol                string           `json:"symbol"`                // 交易对，如 BTCUSDT
	Pair                  string           `json:"pair"`                  // 基础交易对
	ContractType          string           `json:"contractType"`          // 合约类型，如 PERPETUAL、CURRENT_QUARTER
	Status                string           `json:"status"`                // 合约状态，仅 TRADING 会被收录
	PricePrecision        int64            `json:"pricePrecision"`        // 价格小数位数
	QuantityPrecision     int64            `json:"quantityPrecision"`     // 数量小数位数
	BaseAssetPrecision    int64            `json:"baseAssetPrecision"`    // 基础资产精度
	QuotePrecision        int64            `json:"quotePrecision"`        // 计价资产精度
	UnderlyingType        string           `json:"underlyingType"`        // 标的类型，如 COIN、INDEX
	QuoteAsset            string           `json:"quoteAsset"`            // 计价资产，如 USDT
	BaseAsset             string           `json:"baseAsset"`             // 基础资产，如 BTC
	MaintMarginPercent    string           `json:"maintMarginPercent"`    // 维持保证金率
	RequiredMarginPercent string           `json:"requiredMarginPercent"` // 开仓所需保证金率
	LiquidationFee        string           `json:"liquidationFee"`        // 强平费率
	MarketTakeBound       string           `json:"marketTakeBound"`       // 市价单价格保护上限比例
	Filters               []map[string]any `json:"filters"`               // 过滤规则，含 PRICE_FILTER、MARKET_LOT_SIZE、MIN_NOTIONAL 等
}
