package main

import (
	"errors"
	"os"
	"time"

	"github.com/shopspring/decimal"
	"gopkg.in/yaml.v3"
)

type SymbolConfig struct {
	Symbol string          `yaml:"symbol"`
	Usdt   decimal.Decimal `yaml:"usdt"`
	Price  decimal.Decimal `yaml:"price"`
}

type EnvConfig struct {
	APIKey                      string          `yaml:"api_key"`                          // Binance API Key
	SecretKey                   string          `yaml:"secret_key"`                       // Binance Secret Key
	ProxyURL                    string          `yaml:"proxy_url"`                        // 代理地址
	HoldingRatio                decimal.Decimal `yaml:"holding_ratio"`                    // 持仓比例
	MarginRatioReduceTrigger    decimal.Decimal `yaml:"margin_ratio_reduce_trigger"`      // 降仓触发保证金率
	MarginRatioAddTrigger       decimal.Decimal `yaml:"margin_ratio_add_trigger"`         // 加仓触发保证金率
	MarginRatioReduceTarget     decimal.Decimal `yaml:"margin_ratio_reduce_target"`       // 降仓目标保证金率
	MarginRatioAddTarget        decimal.Decimal `yaml:"margin_ratio_add_target"`          // 加仓目标保证金率
	MinAvailableUSD             decimal.Decimal `yaml:"min_available_usd"`                // 最低可用美元
	ReduceBaseUsdt              decimal.Decimal `yaml:"reduce_base_usdt"`                 // 基础降仓金额
	ReduceStepUsdtPerRatioPoint decimal.Decimal `yaml:"reduce_step_usdt_per_ratio_point"` // 每点保证金率追加降仓金额
	MainLoopInterval            string          `yaml:"main_loop_interval"`               // 主循环间隔
	FillTimeout                 string          `yaml:"fill_timeout"`                     // 成交等待超时
	RetryCount                  int             `yaml:"retry_count"`                      // 下单重试次数
	MainLoopIntervalDuration    time.Duration   `yaml:"-"`                                // 主循环间隔时间
	FillTimeoutDuration         time.Duration   `yaml:"-"`                                // 成交等待超时时间
	Symbols                     []SymbolConfig  `yaml:"symbols"`                          // 交易标的列表
}

var Env *EnvConfig

var (
	defaultMarginRatioReduceTrigger    = decimal.NewFromInt(50)
	defaultMarginRatioAddTrigger       = decimal.NewFromInt(40)
	defaultMarginRatioReduceTarget     = decimal.NewFromInt(45)
	defaultMinAvailableUSD             = decimal.NewFromInt(10)
	defaultReduceBaseUsdt              = decimal.NewFromInt(100)
	defaultReduceStepUsdtPerRatioPoint = decimal.NewFromInt(20)
	defaultMainLoopInterval            = 10 * time.Minute
	defaultFillTimeout                 = 2 * time.Minute
	defaultRetryCount                  = 3
)

func initEnv() error {
	var err error
	Env, err = loadEnvConfig("config.yaml")
	if err != nil {
		return err
	}

	if Env.APIKey == "" || Env.SecretKey == "" {
		return errors.New("missing required config: api_key and secret_key")
	}

	return nil
}

func RefreshEnv() error {
	env, err := loadEnvConfig("config.yaml")
	if err != nil {
		return err
	}

	Env = env
	return nil
}

func loadEnvConfig(path string) (*EnvConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("read config.yaml failed: " + err.Error())
	}

	env := &EnvConfig{}
	if err := yaml.Unmarshal(data, env); err != nil {
		return nil, errors.New("parse config.yaml failed: " + err.Error())
	}
	if err := env.applyDefaults(); err != nil {
		return nil, err
	}

	return env, nil
}

func (e *EnvConfig) applyDefaults() error {
	if e.MarginRatioReduceTrigger.IsZero() {
		e.MarginRatioReduceTrigger = defaultMarginRatioReduceTrigger
	}
	if e.MarginRatioAddTrigger.IsZero() {
		e.MarginRatioAddTrigger = defaultMarginRatioAddTrigger
	}
	if e.MarginRatioReduceTarget.IsZero() {
		e.MarginRatioReduceTarget = defaultMarginRatioReduceTarget
	}
	if e.MinAvailableUSD.IsZero() {
		e.MinAvailableUSD = defaultMinAvailableUSD
	}
	if e.ReduceBaseUsdt.IsZero() {
		e.ReduceBaseUsdt = defaultReduceBaseUsdt
	}
	if e.ReduceStepUsdtPerRatioPoint.IsZero() {
		e.ReduceStepUsdtPerRatioPoint = defaultReduceStepUsdtPerRatioPoint
	}
	if e.RetryCount <= 0 {
		e.RetryCount = defaultRetryCount
	}

	mainLoopInterval := defaultMainLoopInterval
	if e.MainLoopInterval != "" {
		d, err := time.ParseDuration(e.MainLoopInterval)
		if err != nil {
			return errors.New("parse main_loop_interval failed: " + err.Error())
		}
		mainLoopInterval = d
	}
	e.MainLoopIntervalDuration = mainLoopInterval

	fillTimeout := defaultFillTimeout
	if e.FillTimeout != "" {
		d, err := time.ParseDuration(e.FillTimeout)
		if err != nil {
			return errors.New("parse fill_timeout failed: " + err.Error())
		}
		fillTimeout = d
	}
	e.FillTimeoutDuration = fillTimeout

	return nil
}

func (e *EnvConfig) GetSymbol(symbol string) *SymbolConfig {
	for i := range e.Symbols {
		if e.Symbols[i].Symbol == symbol {
			return &e.Symbols[i]
		}
	}
	return nil
}

func (e *EnvConfig) GetAllSymbols() []string {
	symbols := make([]string, len(e.Symbols))
	for i, s := range e.Symbols {
		symbols[i] = s.Symbol
	}
	return symbols
}
