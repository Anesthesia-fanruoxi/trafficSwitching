package reporter

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
	"trafficSwitching/api"
	"trafficSwitching/common"
	"trafficSwitching/config"
)

// SendData 上报数据结构（字段名与服务端解析匹配）
type SendData struct {
	Project string                   `json:"project"`
	Source  string                   `json:"source"`
	Data    []map[string]interface{} `json:"data"`
}

var (
	// 上报专用 HTTP 客户端
	reportClient = &http.Client{
		Timeout: 10 * time.Second,
	}
	// 缓存配置
	reportProject string
	reportSource  string
	reportService string
	reportURL     string
	reportSalt    string
)

// Start 启动上报 goroutine
func Start() {
	enable, project, source, service, intervalStr, url, salt := config.GetReportConfig()
	if !enable {
		log.Println("上报功能已禁用")
		return
	}

	// 未配置上报地址时直接跳过（避免向空 URL 发请求产生无效错误日志）
	if strings.TrimSpace(url) == "" {
		log.Println("上报地址未配置（report.url / TS_REPORT_URL），上报功能跳过")
		return
	}

	// 缓存配置
	reportProject = project
	reportSource = source
	reportService = service
	reportURL = url
	reportSalt = salt

	// 解析间隔时间
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		log.Printf("ERROR: 上报间隔配置错误: %v，使用默认10s", err)
		interval = 10 * time.Second
	}

	// 验证 salt 长度（AES 要求 16/24/32 字节）
	saltLen := len(salt)
	if saltLen != 16 && saltLen != 24 && saltLen != 32 {
		log.Printf("ERROR: salt长度必须为16/24/32字节，当前%d字节，上报功能禁用", saltLen)
		return
	}

	log.Printf("上报功能启动: 项目=%s, 间隔=%s, URL=%s", project, interval, url)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			if err := report(); err != nil {
				log.Printf("WARN: 上报失败: %v", err)
			}
		}
	}()
}

// report 执行一次上报
func report() error {
	// 获取 metrics 数据
	metrics := api.BuildFlatMetricsResponse()

	// 添加 service 和 report_timestamp
	metrics["service"] = reportService
	metrics["report_timestamp"] = time.Now().Unix()

	// 构建上报数据（data 为数组格式）
	sendData := SendData{
		Project: reportProject,
		Source:  reportSource,
		Data:    []map[string]interface{}{metrics},
	}

	// 序列化为 JSON
	jsonData, err := json.Marshal(sendData)
	if err != nil {
		return err
	}

	// 压缩并加密（返回二进制数据，不做 Base64 编码）
	encrypted, err := common.CompressAndEncryptRaw(jsonData, reportSalt)
	if err != nil {
		return err
	}

	// 发送 POST 请求（二进制数据）
	resp, err := reportClient.Post(reportURL, "application/octet-stream", bytes.NewReader(encrypted))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &ReportError{StatusCode: resp.StatusCode}
	}

	return nil
}

// ReportError 上报错误
type ReportError struct {
	StatusCode int
}

func (e *ReportError) Error() string {
	return "上报响应状态码: " + http.StatusText(e.StatusCode)
}
