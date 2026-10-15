package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/phrynus/plog"
)

// maskKey 遮蔽密钥，只保留长度，避免 API Key / Secret 明文写入日志。
func maskKey(s string) string {
	if s == "" {
		return "(空)"
	}
	return fmt.Sprintf("(长度%d)", len(s))
}

func configLogValue(key, value string) string {
	upperKey := strings.ToUpper(key)
	if strings.Contains(upperKey, "KEY") || strings.Contains(upperKey, "SECRET") ||
		strings.Contains(upperKey, "TOKEN") || strings.Contains(upperKey, "PASSWORD") || upperKey == "PROXY" {
		return maskKey(value)
	}
	return value
}

// modeName 返回运行模式的中文名称，用于日志可读性。
func modeName(demo bool) string {
	if demo {
		return "模拟盘"
	}
	return "实盘"
}

// envFile 运行配置文件名，读取后写入进程环境变量。
// 优先级：系统环境变量 > .env 文件 > 代码内默认值（与 .env 注释一致）。
const envFile = ".env"

// 运行配置：由 .env / 环境变量加载，字段含义见 .env 内注释。
var (
	proxy      string  // WebSocket / REST 代理，空表示直连，例如 "http://127.0.0.1:7897"
	apiKey     string  // 币安 API Key
	secret     string  // 币安 Secret Key
	demo       bool    // 模拟盘开关，true 使用模拟盘地址（demo-fapi / testnet / demo-fstream）
	symbol     string  // 交易对，例如 "DOGEUSDT"
	spacing    float64 // 网格间距万分比 / 10000，例如 5 表示 0.05%
	usdt       float64 // 每格投入的 USDT
	miniChange float64 // 最小变动单位，例如 BTCUSDT 为 0.01，DOGEUSDT 为 0.00001
	gridUp     int64   // 上方（价格更高、编号更小）格子数：id-1、id-2
	gridDown   int64   // 下方（价格更低、编号更大）格子数：id+1、id+2、id+3
	gridWindow int64   // 窗口格子总数：gridUp + gridDown + 1
)

// loadConfig 加载 .env 并写入运行配置，最后固定窗口大小。
// 在 plog.SetDefault 之后、使用配置的功能之前调用（见 init）。
func loadConfig() {
	plog.Debugf("[配置] 开始加载运行配置, 配置文件=%s (系统环境变量优先)", envFile)
	loadEnv(envFile)

	proxy = envString("PROXY", "")
	apiKey = envString("API_KEY", "")
	secret = envString("SECRET", "")
	demo = envBool("DEMO", true)
	symbol = envString("SYMBOL", "DOGEUSDT")
	spacing = envFloat("SPACING", 5)
	usdt = envFloat("USDT", 20)
	miniChange = envFloat("MINI_CHANGE", 0.00001)
	gridUp = envInt("GRID_UP", 2)
	gridDown = envInt("GRID_DOWN", 3)
	gridWindow = gridUp + gridDown + 1

	plog.Debugf("[配置] 密钥已读取: apiKey=%s secret=%s", maskKey(apiKey), maskKey(secret))
	plog.Debugf("[配置] 连接参数: proxy=%q 模式=%s", proxy, modeName(demo))
	plog.Debugf("[配置] 交易参数: 交易对=%s 间距=%g(万分比) 每格=%gU 最小变动=%g",
		symbol, spacing, usdt, miniChange)
	plog.Debugf("[配置] 窗口参数: 上方=%d格 下方=%d格 合计=%d格", gridUp, gridDown, gridWindow)

	plog.Infof("[配置] 配置加载完成 模式=%s 交易对=%s 间距=%g 每格=%gU 上=%d 下=%d 窗口=%d",
		modeName(demo), symbol, spacing, usdt, gridUp, gridDown, gridWindow)
}

// loadEnv 逐行解析 .env（支持 # 注释、export 前缀、单/双引号、行尾 " #" 注释），
// 写入进程环境变量。已存在的同名系统环境变量优先，便于容器 / CI 覆盖而不改文件。
// 文件不存在时静默跳过，直接使用环境变量或默认值。
func loadEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		plog.Debugf("[配置] 未找到配置文件 %s, 使用系统环境变量与默认值: %v", path, err)
		return
	}
	defer f.Close()

	loaded := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))

		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}

		val = strings.TrimSpace(val)
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1] // 带引号：原样取值，允许值内含 #
		} else if i := strings.Index(val, " #"); i >= 0 {
			val = strings.TrimSpace(val[:i]) // 无引号：截断行尾注释
		}

		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
			loaded++
			plog.Debugf("[配置] 载入配置项 %s=%q", key, configLogValue(key, val))
		} else {
			plog.Debugf("[配置] 配置项 %s 已由系统环境变量提供, 跳过文件值", key)
		}
	}
	plog.Debugf("[配置] 配置文件解析完成, 实际应用 %d 项", loaded)
}

// envString 读取字符串配置，缺失或为空时返回默认值。
func envString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return def
}

// envFloat 读取浮点配置，解析失败时告警并返回默认值。
func envFloat(key string, def float64) float64 {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		plog.Warnf("[配置] 配置 %s=%q 不是合法浮点数, 使用默认值 %v", key, v, def)
		return def
	}
	return f
}

// envInt 读取整型配置，解析失败时告警并返回默认值。
func envInt(key string, def int64) int64 {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		plog.Warnf("[配置] 配置 %s=%q 不是合法整数, 使用默认值 %d", key, v, def)
		return def
	}
	return n
}

// envBool 读取布尔配置，支持 1/true/yes/on 与 0/false/no/off（大小写不敏感）。
func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	plog.Warnf("[配置] 配置 %s=%q 不是合法布尔值, 使用默认值 %v", key, v, def)
	return def
}
