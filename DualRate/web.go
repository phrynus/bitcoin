package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"time"

	"github.com/pkg/browser"
)

type PageData struct {
	Begin     string
	End       string
	Days      int
	DataJSON  template.JS
	Generated string
}

//go:embed template.html
var viewFS embed.FS

var pageTpl = template.Must(template.ParseFS(viewFS, "template.html"))

func serveWeb(report Report) {
	raw, err := json.Marshal(report)
	if err != nil {
		fmt.Fprintf(os.Stderr, "序列化页面数据失败: %v\n", err)
		return
	}

	loc, _ := time.LoadLocation("Asia/Shanghai")
	page := PageData{
		Begin:     report.B,
		End:       report.E,
		Days:      report.N,
		DataJSON:  template.JS(raw),
		Generated: time.Now().In(loc).Format("2006-01-02 15:04:05"),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if err := pageTpl.ExecuteTemplate(w, "template.html", page); err != nil {
			http.Error(w, "render failed", http.StatusInternalServerError)
		}
	})

	addr := "127.0.0.1:34552"
	url := "http://" + addr
	fmt.Printf("打开 %s 查看报告\n", url)
	go browser.OpenURL(url)

	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "服务启动失败: %v\n", err)
	}
}
