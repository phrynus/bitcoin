package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Days    int      `yaml:"days"`
	Symbols []string `yaml:"symbols"`
}

var defaultConfig = Config{
	Days: 30,
	Symbols: []string{
		"1000BONK", "1000PEPE", "1000SHIB", "AAVE", "ARB", "AVAX", "BCH", "BIO",
		"BNB", "BOME", "BTC", "CRV", "DOGE", "ENA", "ETH", "ETHFI", "FIL", "HBAR",
		"LINK", "LTC", "NEAR", "NEO", "ORDI", "PNUT", "SOL", "SUI", "TIA", "UNI",
		"WIF", "WLFI", "WLD", "XRP",
	},
}

func cloneConfig(cfg Config) Config {
	return Config{
		Days:    cfg.Days,
		Symbols: append([]string(nil), cfg.Symbols...),
	}
}

func normalizeConfig(cfg Config) Config {
	if cfg.Days <= 0 {
		cfg.Days = defaultConfig.Days
	}

	list := make([]string, 0, len(cfg.Symbols))
	seen := make(map[string]struct{}, len(cfg.Symbols))
	for _, sym := range cfg.Symbols {
		sym = strings.TrimSpace(sym)
		if sym == "" {
			continue
		}
		if _, ok := seen[sym]; ok {
			continue
		}
		seen[sym] = struct{}{}
		list = append(list, sym)
	}

	if len(list) == 0 {
		cfg.Symbols = append([]string(nil), defaultConfig.Symbols...)
		return cfg
	}

	cfg.Symbols = list
	return cfg
}

func loadConfig(path string) (Config, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cloneConfig(defaultConfig), true, nil
		}
		return Config{}, false, fmt.Errorf("读取配置失败: %w", err)
	}

	cfg := cloneConfig(defaultConfig)
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, false, fmt.Errorf("解析配置失败: %w", err)
	}

	return normalizeConfig(cfg), false, nil
}
