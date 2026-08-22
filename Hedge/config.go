// 配置：加载 config.yaml 并校验/填充默认值。
// 所有可调参数（保证金基准点、加/减/平仓规则、主循环节奏）统一在此管理。
package main

import (
	"fmt"
	"os"
	"time"

	"go.yaml.in/yaml/v3"
)

// Account 交易账户配置。
type Account struct {
	Name      string `yaml:"name"`
	APIKey    string `yaml:"api_key"`
	SecretKey string `yaml:"secret_key"`
}

// Plan 加仓规划：每个币构成一个对冲对（USDC 空单 + USDT 多单）。
type Plan struct {
	Base string  `yaml:"symbol"` // 基础币（如 NEO）
	Usdt float64 `yaml:"usdt"`   // 单次加仓金额（USDT）
	Cap  float64 `yaml:"cap"`    // 该币持仓价值上限（名义价值）
}

// MarginRule 保证金比率基准点控制（本项目核心目标：维持保证金比率在基准点附近）。
type MarginRule struct {
	Base  float64 `yaml:"base"`  // 目标基准点（万分比，55 = 0.55%），减仓收敛目标
	Range float64 `yaml:"range"` // 带宽：比率 > base+range 触发减仓，< base-range 触发加仓
}

// LoopRule 主循环节奏控制。
type LoopRule struct {
	ScanInterval time.Duration `yaml:"scan_interval"` // 主循环扫描间隔
	ReduceWait   time.Duration `yaml:"reduce_wait"`   // 减仓进行中时的轮询等待间隔
	StepPause    time.Duration `yaml:"step_pause"`    // 清理持仓各步骤间停顿
}

// AddRule 加仓规则（保证金比率低于基准-带宽时触发）。
type AddRule struct {
	MinBalance float64       `yaml:"min_balance"` // 可用余额低于该值不加仓
	CapRatio   float64       `yaml:"cap_ratio"`   // 持仓价值上限倍数：cap × cap_ratio
	Timeout    time.Duration `yaml:"timeout"`     // 开仓限价单成交等待超时
}

// CloseRule 平仓规则（平单边 / 反向 / 规划外对冲对）。
type CloseRule struct {
	Timeout      time.Duration `yaml:"timeout"`       // 平仓限价单成交等待超时
	StepNotional float64       `yaml:"step_notional"` // 名义价值阈值：超过则分批平，否则一次全平
	StepUsdt     string        `yaml:"step_usdt"`     // 分批平仓金额（USDT）
}

// ReduceRule 减仓规则（保证金比率高于基准+带宽时触发）。
type ReduceRule struct {
	StepNotional float64       `yaml:"step_notional"`   // 名义价值阈值：超过则分批减，否则全减
	StepUsdt     string        `yaml:"step_usdt"`       // 分批减仓金额（USDT）
	Interval     time.Duration `yaml:"reduce_interval"` // 减仓循环基础间隔：保证金率未超基准时每轮减仓的等待时间
	Cut          float64       `yaml:"reduce_cut"`      // 间隔衰减系数（0~1）：每超出基准 1 点缩短 base×此值；此值×超出点数≥1 连续减仓
}

// Config 顶层配置。
type Config struct {
	ProxyURL string     `yaml:"proxy_url"` // 本地 HTTP 代理
	Margin   MarginRule `yaml:"margin"`    // 保证金比率基准点控制
	Loop     LoopRule   `yaml:"loop"`      // 主循环节奏
	Add      AddRule    `yaml:"add"`       // 加仓规则
	Close    CloseRule  `yaml:"close"`     // 平仓规则
	Reduce   ReduceRule `yaml:"reduce"`    // 减仓规则
	Accounts []Account  `yaml:"accounts"`  // 交易账户
	Plans    []Plan     `yaml:"plans"`     // 加仓规划
}

func LoadConfig() (*Config, error) {
	data, err := os.ReadFile("config.yaml")
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, err
	}
	if err := c.defaults(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) defaults() error {
	if len(c.Accounts) == 0 {
		return fmt.Errorf("config: accounts 不能为空")
	}
	for i, acc := range c.Accounts {
		if acc.Name == "" {
			return fmt.Errorf("config: accounts[%d].name 不能为空", i)
		}
		if acc.APIKey == "" {
			return fmt.Errorf("config: accounts[%d].api_key 不能为空", i)
		}
		if acc.SecretKey == "" {
			return fmt.Errorf("config: accounts[%d].secret_key 不能为空", i)
		}
	}
	for i, p := range c.Plans {
		if p.Base == "" {
			return fmt.Errorf("config: plans[%d].symbol 不能为空", i)
		}
		if p.Usdt <= 0 {
			return fmt.Errorf("config: plans[%d].usdt 必须大于 0", i)
		}
		if p.Cap <= 0 {
			return fmt.Errorf("config: plans[%d].cap 必须大于 0", i)
		}
	}

	// 保证金比率基准点
	if c.Margin.Base <= 0 {
		c.Margin.Base = 55
	}
	if c.Margin.Range <= 0 {
		c.Margin.Range = 5
	}

	// 主循环节奏
	if c.Loop.ScanInterval <= 0 {
		c.Loop.ScanInterval = 2 * time.Minute
	}
	if c.Loop.ReduceWait <= 0 {
		c.Loop.ReduceWait = 5 * time.Second
	}
	if c.Loop.StepPause <= 0 {
		c.Loop.StepPause = 1 * time.Second
	}

	// 加仓规则
	if c.Add.MinBalance <= 0 {
		c.Add.MinBalance = 100
	}
	if c.Add.CapRatio <= 0 {
		c.Add.CapRatio = 1.6
	}
	if c.Add.Timeout <= 0 {
		c.Add.Timeout = 30 * time.Second
	}

	// 平仓规则
	if c.Close.Timeout <= 0 {
		c.Close.Timeout = 10 * time.Second
	}
	if c.Close.StepNotional <= 0 {
		c.Close.StepNotional = 240
	}
	if c.Close.StepUsdt == "" {
		c.Close.StepUsdt = "200"
	}

	// 减仓规则
	if c.Reduce.Interval <= 0 {
		c.Reduce.Interval = 2 * time.Second
	}
	if c.Reduce.Cut <= 0 {
		c.Reduce.Cut = 0.1
	}

	// 减仓规则
	if c.Reduce.StepNotional <= 0 {
		c.Reduce.StepNotional = 240
	}
	if c.Reduce.StepUsdt == "" {
		c.Reduce.StepUsdt = "200"
	}
	return nil
}
