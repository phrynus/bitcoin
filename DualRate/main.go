package main

import (
	"fmt"
	"os"
)

func run() error {
	cfg, usedDefault, err := loadConfig("config.yaml")
	if err != nil {
		return fmt.Errorf("配置加载失败: %w", err)
	}
	if usedDefault {
		fmt.Println("未找到 config.yaml，使用内置默认配置")
	}

	report, total := buildReport(cfg)

	fmt.Printf("共处理 %d 个币种，总费率 %.4f%%\n", len(report.S), total*100)
	if len(report.F) > 0 {
		fmt.Printf("失败币种: %v\n", report.F)
	}

	serveWeb(report)
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
