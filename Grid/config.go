package main

import (
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	APIKey    string `yaml:"api_key"`    // Binance API Key
	SecretKey string `yaml:"secret_key"` // Binance Secret Key
	ProxyURL  string `yaml:"proxy_url"`  // 代理地址
}

func RunConfig() (*Config, error) {
	data, err := os.ReadFile("config.yaml")
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if err := cfg.defaults(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (e *Config) defaults() error {
	if e.APIKey == "" {
		return fmt.Errorf("config: api_key 不能为空")
	}
	if e.SecretKey == "" {
		return fmt.Errorf("config: secret_key 不能为空")
	}
	return nil
}
