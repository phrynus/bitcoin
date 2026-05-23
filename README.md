# bitcoin

三个加密货币相关的小工具，Go 编写。

## Coin — 图标爬虫

从 CoinMarketCap 批量下载所有币种的 32/64/128 图标，按符号归档到 `coins/` 目录，`coins/list.json` 记录已下载清单。

```
cd Coin && go run .
```

## FuturesHedge — 合约对冲

监控币安合约账户保证金率，自动在 USDT/USDC 双边仓位间加减仓，把保证金率维持在安全区间。

复制 `config.example.yaml` → `config.yaml`，填入 API 密钥：

```yaml
api_key: ""
secret_key: ""
holding_ratio: "0.5"
margin_ratio_reduce_trigger: "0.5"
margin_ratio_add_trigger: "0.3"
margin_ratio_reduce_target: "0.45"
margin_ratio_add_target: "0.4"
min_available_usd: "100"
main_loop_interval: "10m"
```

```
cd FuturesHedge && go run .
```

## DualRate — 资金费率分析

拉取币安 USDT/USDC 合约历史资金费率，计算双边费率之和，浏览器展示可视化报告。

```yaml
# config.yaml
days: 30
symbols: [BTC, ETH, SOL]
```

```
cd DualRate && go run .
```

## 构建

Go 1.21+，`go mod tidy && go build .`