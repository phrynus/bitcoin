package main

// # 打包为 Windows 64位
// $env:GOOS="windows"; $env:GOARCH="amd64"; go build -o windows-amd64.exe .

// # 打包为 Linux 64位
// $env:GOOS="linux"; $env:GOARCH="amd64"; go build -o linux-amd64 .

// # 打包为 macOS 64位
// $env:GOOS="darwin"; $env:GOARCH="amd64"; go build -o darwin-amd64 .

// # 打包为 macOS ARM64 (M1/M2)
// $env:GOOS="darwin"; $env:GOARCH="arm64"; go build -o darwin-arm64 .

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	apiBaseURL   = "https://api.coinmarketcap.com/data-api/v3/cryptocurrency/listing"
	imageBaseURL = "https://s2.coinmarketcap.com/static/img/coins"
	pageLimit    = 500   // 每页数量
	maxPages     = 10    // 最多抓多少页
	reverse      = false // 是否反向抓取页数（true: 从后往前；false: 从前往后）
)

// 对应 @Untitled-1 的结构，只保留需要的字段
type apiResponse struct {
	Data struct {
		CryptoCurrencyList []struct {
			ID     int    `json:"id"`
			Name   string `json:"name"`
			Symbol string `json:"symbol"`
			Slug   string `json:"slug"`
		} `json:"cryptoCurrencyList"`
	} `json:"data"`
}

// list.json 中每一个币种的记录，对应 coins/list.json 中的结构
// 例如:
//
//	{
//	  "BNB": {
//	    "slug": "bnb",
//	    "symbol": "BNB",
//	    "size": [32, 64, 128]
//	  }
//	}
type coinInfo struct {
	Slug   string `json:"slug"`   // 使用 slug 替代 name
	Symbol string `json:"symbol"` // 币种符号，如 BTC
	Size   []int  `json:"size"`   // 已下载的图片尺寸列表
}

// list.json 顶层结构：symbol 作为 key
// key: 币种符号，如 "BNB"、"BTC"
type listData map[string]*coinInfo

func main() {

	if reverse {
		log.Println("开始抓取 CoinMarketCap 图片（反向页数，从后往前）...")
	} else {
		log.Println("开始抓取 CoinMarketCap 图片（正向页数，从前往后）...")
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	// coins/list.json 的路径
	listPath := filepath.Join("coins", "list.json")

	// 读取已有的 list.json，用于去重
	ld, downloadedMap, err := loadListData(listPath)
	if err != nil {
		log.Printf("读取 list.json 失败，将从空列表开始: %v\n", err)
		ld = make(listData)
		downloadedMap = make(map[string]struct{})
	}

	// 设置信号处理，捕获系统关闭、Ctrl+C 等信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// 启动协程监听信号
	go func() {
		<-sigChan
		log.Println("\n收到关闭信号，正在保存数据...")
		if err := saveListData(listPath, ld); err != nil {
			log.Printf("写入 list.json 失败: %v\n", err)
		} else {
			log.Println("数据已成功保存。")
		}
		os.Exit(0)
	}()

	defer func() {
		// 把最新的列表写回 coins/list.json
		if err := saveListData(listPath, ld); err != nil {
			log.Printf("写入 list.json 失败: %v\n", err)
		} else {
			log.Println("数据已成功保存。")
		}
	}()

	crawlPages(client, ld, downloadedMap)

	log.Println("全部任务完成。")
}

// 根据配置的方向抓取所有页
func crawlPages(client *http.Client, ld listData, downloadedMap map[string]struct{}) {
	for i := 0; i < maxPages; i++ {
		// 根据 reverse 决定当前页号
		page := i
		if reverse {
			page = maxPages - 1 - i
		}

		start := 1 + page*pageLimit
		url := fmt.Sprintf("%s?start=%d&limit=%d", apiBaseURL, start, pageLimit)

		if reverse {
			log.Printf("请求列表（反向，第 %d 页）：%s\n", page+1, url)
		} else {
			log.Printf("请求列表（正向，第 %d 页）：%s\n", page+1, url)
		}

		respData, err := fetchListing(client, url)
		if err != nil {
			log.Printf("获取列表失败: %v\n", err)
			break
		}

		coins := respData.Data.CryptoCurrencyList
		if len(coins) == 0 {
			log.Println("没有更多数据，结束。")
			break
		}

		for _, coin := range coins {
			if coin.ID == 0 || coin.Symbol == "" {
				continue
			}
			if err := downloadCoinImages(client, coin.ID, coin.Symbol, coin.Slug, ld, downloadedMap); err != nil {
				log.Printf("下载 %s(%s) 失败: %v\n", coin.Symbol, coin.Slug, err)
			}
			// 稍微休息一下，避免过快请求
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// 请求列表并解析 JSON
func fetchListing(client *http.Client, url string) (*apiResponse, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	// 伪装一个正常 UA，降低被屏蔽概率
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; CMC-Image-Crawler/1.0)")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	var data apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	return &data, nil
}

// 根据 id 下载 128/64/32 三种尺寸的图片
// 优先保存为 coins/{symbol}/{size}.png
// 同时在 coins/list.json 中记录已下载的 {symbol}_{size} 作为 key，重复则直接跳过
func downloadCoinImages(client *http.Client, id int, symbol, slug string, ld listData, downloaded map[string]struct{}) error {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return nil
	}

	// 使用 slug 作为名称相关部分（slug 本身已较为安全）
	slug = strings.TrimSpace(slug)
	if slug == "" {
		slug = "noname"
	}

	sizes := []int{128, 64, 32}

	for _, size := range sizes {
		sizeStr := fmt.Sprintf("%d", size)
		// 使用 {symbol}_{size} 作为唯一键
		key := fmt.Sprintf("%s_%s", symbol, sizeStr)

		// 先根据 list.json 里的记录去重：同一个 {symbol}_{size} 已记录过就跳过
		if _, ok := downloaded[key]; ok {
			log.Printf("list.json 已存在记录，跳过: %s 尺寸 %s\n", symbol, sizeStr)
			continue
		}

		imageURL := fmt.Sprintf("%s/%dx%d/%d.png", imageBaseURL, size, size, id)
		dir := filepath.Join("coins", symbol)
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			return fmt.Errorf("创建目录失败 %s: %w", dir, err)
		}

		var destPath string

		// 1. 优先使用 coins/{symbol}/{size}.png
		defaultFilename := fmt.Sprintf("%d.png", size)
		defaultPath := filepath.Join(dir, defaultFilename)

		if _, statErr := os.Stat(defaultPath); os.IsNotExist(statErr) {
			// 不存在，直接用 {size}.png
			destPath = defaultPath
		} else {
			continue
		}

		log.Printf("下载图片: %s -> %s\n", symbol, destPath)
		if err := downloadFile(client, imageURL, destPath); err != nil {
			log.Printf("下载失败: %s: %v\n", symbol, err)
		} else {
			// 下载成功后，更新内存中的 list.json 数据和去重 map
			downloaded[key] = struct{}{}

			// 确保该 symbol 对应的记录存在
			info, ok := ld[symbol]
			if !ok || info == nil {
				info = &coinInfo{
					Slug:   slug,
					Symbol: symbol,
					Size:   []int{},
				}
				ld[symbol] = info
			}

			// 如果当前尺寸尚未记录，则追加到 size 列表中
			alreadyHasSize := false
			for _, s := range info.Size {
				if s == size {
					alreadyHasSize = true
					break
				}
			}
			if !alreadyHasSize {
				info.Size = append(info.Size, size)
			}
		}

		// 稍微停顿一下，避免太猛
		time.Sleep(50 * time.Millisecond)
	}

	return nil
}

// 实际执行图片下载
func downloadFile(client *http.Client, url, destPath string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; CMC-Image-Crawler/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败，状态码: %d", resp.StatusCode)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

// 读取 coins/list.json，返回列表数据，以及一个以 "{symbol}_{size}" 为 key 的去重 map
func loadListData(path string) (listData, map[string]struct{}, error) {
	ld := make(listData)
	downloaded := make(map[string]struct{})

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// 文件不存在，返回空结构即可
			return ld, downloaded, nil
		}
		return nil, nil, err
	}
	defer f.Close()

	if err := json.NewDecoder(f).Decode(&ld); err != nil {
		return nil, nil, err
	}

	for symbol, info := range ld {
		if info == nil {
			continue
		}
		for _, size := range info.Size {
			key := fmt.Sprintf("%s_%d", symbol, size)
			downloaded[key] = struct{}{}
		}
		ld[symbol] = info
	}

	return ld, downloaded, nil
}

// 把列表数据写入 coins/list.json
func saveListData(path string, ld listData) error {
	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(path), os.ModePerm); err != nil {
		return err
	}

	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(ld); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	// 原子替换
	return os.Rename(tmpPath, path)
}
