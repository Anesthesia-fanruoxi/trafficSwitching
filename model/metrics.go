package model

import (
	"sync/atomic"
	"time"
)

// 每日重置
var (
	currentDay   string // 当前日期
	OnDailyReset func() // 每日重置回调（用于重置其他模块的计数器）
)

// RequestStats 请求统计
type RequestStats struct {
	Total       int64            `json:"total"`
	Success     int64            `json:"success"`
	Errors      int64            `json:"errors"`
	SuccessRate string           `json:"success_rate"`
	ErrorTypes  map[string]int64 `json:"error_types,omitempty"` // 错误类型分布
}

// ProxyStats 代理统计
type ProxyStats struct {
	CacheSize    int             `json:"cache_size"`
	MaxCacheSize int             `json:"max_cache_size"`
	Transport    TransportConfig `json:"transport"`
}

// TransportConfig 传输配置
type TransportConfig struct {
	MaxIdleConns        int `json:"max_idle_conns"`
	MaxIdleConnsPerHost int `json:"max_idle_conns_per_host"`
	MaxConnsPerHost     int `json:"max_conns_per_host"`
}

// RuntimeStats 运行时统计
type RuntimeStats struct {
	Goroutines int    `json:"goroutines"`
	MemoryMB   uint64 `json:"memory_mb"`
	GCCycles   uint32 `json:"gc_cycles"`
	CPUCores   int    `json:"cpu_cores"`
	GoMaxProcs int    `json:"gomaxprocs"`
}

// MetricsResponse 监控响应
type MetricsResponse struct {
	Timestamp int64         `json:"timestamp"`
	Requests  *RequestStats `json:"requests"`
	Proxy     *ProxyStats   `json:"proxy"`
	Runtime   *RuntimeStats `json:"runtime"`
}

// 全局统计计数器（轻量级原子操作）
var (
	// 当天统计（每日 0 点重置）
	TodayRequests  int64
	TodaySuccess   int64
	TodayErrors    int64
	TodayCanceled  int64
	TodayStatus2xx int64
	TodayStatus3xx int64
	TodayStatus4xx int64
	TodayStatus5xx int64

	// 历史累计（永不重置）
	TotalRequests    int64
	SuccessRequests  int64
	ErrorRequests    int64 // 代理层错误（连接失败等）
	CanceledRequests int64 // 客户端取消（不算失败）

	// 后端状态码统计（历史累计）
	Status2xx int64
	Status3xx int64
	Status4xx int64
	Status5xx int64

	// 实时指标
	ActiveConnections int64 // 当前活跃连接数
	lastTotal         int64 // 上次统计时的总请求数
	lastSuccess       int64 // 上次统计时的成功数
	lastErrors        int64 // 上次统计时的错误数
	currentQPS        int64 // 当前 QPS
	currentSuccessQPS int64 // 每秒成功数
	currentErrorQPS   int64 // 每秒代理错误数

	// 延迟统计（纳秒）
	totalLatency int64 // 当前秒累计延迟
	latencyCount int64 // 当前秒请求数
	maxLatency   int64 // 当前秒最大延迟
	currentAvgMs int64 // 平均延迟（毫秒）
	currentMaxMs int64 // 最大延迟（毫秒）
)

// IncrementTotal 增加总请求数（同时更新今天和历史）
func IncrementTotal() {
	atomic.AddInt64(&TodayRequests, 1)
	atomic.AddInt64(&TotalRequests, 1)
}

// IncrementSuccess 增加成功数（同时更新今天和历史）
func IncrementSuccess() {
	atomic.AddInt64(&TodaySuccess, 1)
	atomic.AddInt64(&SuccessRequests, 1)
}

// IncrementError 增加代理层错误数（同时更新今天和历史）
func IncrementError() {
	atomic.AddInt64(&TodayErrors, 1)
	atomic.AddInt64(&ErrorRequests, 1)
}

// IncrementCanceled 增加客户端取消数（同时更新今天和历史）
func IncrementCanceled() {
	atomic.AddInt64(&TodayCanceled, 1)
	atomic.AddInt64(&CanceledRequests, 1)
}

// GetCanceled 获取客户端取消数（历史累计）
func GetCanceled() int64 {
	return atomic.LoadInt64(&CanceledRequests)
}

// GetTodayCanceled 获取今日取消数
func GetTodayCanceled() int64 {
	return atomic.LoadInt64(&TodayCanceled)
}

// RecordStatusCode 记录后端状态码（同时更新今天和历史）
func RecordStatusCode(status int) {
	switch {
	case status >= 200 && status < 300:
		atomic.AddInt64(&TodayStatus2xx, 1)
		atomic.AddInt64(&Status2xx, 1)
	case status >= 300 && status < 400:
		atomic.AddInt64(&TodayStatus3xx, 1)
		atomic.AddInt64(&Status3xx, 1)
	case status >= 400 && status < 500:
		atomic.AddInt64(&TodayStatus4xx, 1)
		atomic.AddInt64(&Status4xx, 1)
	case status >= 500:
		atomic.AddInt64(&TodayStatus5xx, 1)
		atomic.AddInt64(&Status5xx, 1)
	}
}

// GetStatusCodeStats 获取状态码统计（历史累计）
func GetStatusCodeStats() (s2xx, s3xx, s4xx, s5xx int64) {
	return atomic.LoadInt64(&Status2xx),
		atomic.LoadInt64(&Status3xx),
		atomic.LoadInt64(&Status4xx),
		atomic.LoadInt64(&Status5xx)
}

// GetTodayStats 获取今日统计
func GetTodayStats() (requests, success, errors, canceled, s2xx, s3xx, s4xx, s5xx int64) {
	return atomic.LoadInt64(&TodayRequests),
		atomic.LoadInt64(&TodaySuccess),
		atomic.LoadInt64(&TodayErrors),
		atomic.LoadInt64(&TodayCanceled),
		atomic.LoadInt64(&TodayStatus2xx),
		atomic.LoadInt64(&TodayStatus3xx),
		atomic.LoadInt64(&TodayStatus4xx),
		atomic.LoadInt64(&TodayStatus5xx)
}

// IncrementActive 增加活跃连接数
func IncrementActive() {
	atomic.AddInt64(&ActiveConnections, 1)
}

// DecrementActive 减少活跃连接数
func DecrementActive() {
	atomic.AddInt64(&ActiveConnections, -1)
}

// GetActiveConnections 获取当前活跃连接数
func GetActiveConnections() int64 {
	return atomic.LoadInt64(&ActiveConnections)
}

// GetQPS 获取当前 QPS
func GetQPS() int64 {
	return atomic.LoadInt64(&currentQPS)
}

// GetSuccessQPS 获取每秒成功数
func GetSuccessQPS() int64 {
	return atomic.LoadInt64(&currentSuccessQPS)
}

// GetErrorQPS 获取每秒错误数
func GetErrorQPS() int64 {
	return atomic.LoadInt64(&currentErrorQPS)
}

// GetAvgLatencyMs 获取平均延迟（毫秒）
func GetAvgLatencyMs() int64 {
	return atomic.LoadInt64(&currentAvgMs)
}

// GetMaxLatencyMs 获取最大延迟（毫秒）
func GetMaxLatencyMs() int64 {
	return atomic.LoadInt64(&currentMaxMs)
}

// RecordLatency 记录请求延迟（纳秒）
func RecordLatency(latencyNs int64) {
	atomic.AddInt64(&totalLatency, latencyNs)
	atomic.AddInt64(&latencyCount, 1)
	// 更新最大延迟（无锁 CAS）
	for {
		old := atomic.LoadInt64(&maxLatency)
		if latencyNs <= old {
			break
		}
		if atomic.CompareAndSwapInt64(&maxLatency, old, latencyNs) {
			break
		}
	}
}

// StartRealtimeMetrics 启动实时指标计算器（每秒更新）
func StartRealtimeMetrics() {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			// 同时采样所有计数器（减少时间差）
			total := atomic.LoadInt64(&TotalRequests)
			success := atomic.LoadInt64(&SuccessRequests)
			errors := atomic.LoadInt64(&ErrorRequests)
			prevTotal := atomic.SwapInt64(&lastTotal, total)
			prevSuccess := atomic.SwapInt64(&lastSuccess, success)
			prevErrors := atomic.SwapInt64(&lastErrors, errors)

			// 计算 QPS
			atomic.StoreInt64(&currentQPS, total-prevTotal)
			atomic.StoreInt64(&currentSuccessQPS, success-prevSuccess)
			atomic.StoreInt64(&currentErrorQPS, errors-prevErrors)

			// 延迟统计
			count := atomic.SwapInt64(&latencyCount, 0)
			totalLat := atomic.SwapInt64(&totalLatency, 0)
			maxLat := atomic.SwapInt64(&maxLatency, 0)
			if count > 0 {
				atomic.StoreInt64(&currentAvgMs, totalLat/count/1e6)
			} else {
				atomic.StoreInt64(&currentAvgMs, 0)
			}
			atomic.StoreInt64(&currentMaxMs, maxLat/1e6)
		}
	}()
}

// GetRequestStats 获取请求统计
func GetRequestStats() (total, success, errors int64) {
	return atomic.LoadInt64(&TotalRequests),
		atomic.LoadInt64(&SuccessRequests),
		atomic.LoadInt64(&ErrorRequests)
}

// CalculateSuccessRate 计算成功率（排除正在处理和取消的请求，4位小数）
func CalculateSuccessRate(success, total int64) string {
	// 排除正在处理的请求和客户端取消的请求
	active := atomic.LoadInt64(&ActiveConnections)
	canceled := atomic.LoadInt64(&CanceledRequests)
	completed := total - active - canceled
	if completed <= 0 {
		return "0.0000%"
	}
	rate := float64(success) / float64(completed) * 100
	if rate >= 99.99995 {
		return "100.0000%"
	}
	return formatFloat4(rate)
}

// formatFloat4 格式化浮点数（4位小数）
func formatFloat4(f float64) string {
	intPart := int(f)
	decPart := int((f-float64(intPart))*10000 + 0.5) // 4位小数，四舍五入

	// 处理进位
	if decPart >= 10000 {
		intPart++
		decPart = 0
	}

	// 处理100%的情况
	if intPart >= 100 {
		return "100.0000%"
	}

	// 构建字符串：XX.XXXX%
	d1 := decPart / 1000
	d2 := (decPart / 100) % 10
	d3 := (decPart / 10) % 10
	d4 := decPart % 10

	return string(rune(intPart/10+'0')) + string(rune(intPart%10+'0')) + "." +
		string(rune(d1+'0')) + string(rune(d2+'0')) + string(rune(d3+'0')) + string(rune(d4+'0')) + "%"
}

// GetTimestamp 获取当前时间戳（复用）
func GetTimestamp() int64 {
	return time.Now().Unix()
}

// InitDailyReset 初始化每日重置（在 main 中调用）
func InitDailyReset() {
	currentDay = time.Now().Format("2006-01-02")
	go dailyResetLoop()
}

// dailyResetLoop 每日重置循环
func dailyResetLoop() {
	for {
		now := time.Now()
		today := now.Format("2006-01-02")

		// 检查是否跨天
		if today != currentDay {
			resetDailyStats()
			currentDay = today
		}

		// 计算到下一个 0 点的时间
		next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 1, 0, now.Location())
		time.Sleep(next.Sub(now))
	}
}

// resetDailyStats 每日重置当天计数器（历史累计不重置）
func resetDailyStats() {
	atomic.StoreInt64(&TodayRequests, 0)
	atomic.StoreInt64(&TodaySuccess, 0)
	atomic.StoreInt64(&TodayErrors, 0)
	atomic.StoreInt64(&TodayCanceled, 0)
	atomic.StoreInt64(&TodayStatus2xx, 0)
	atomic.StoreInt64(&TodayStatus3xx, 0)
	atomic.StoreInt64(&TodayStatus4xx, 0)
	atomic.StoreInt64(&TodayStatus5xx, 0)

	// 调用外部重置回调（如重置错误类型统计）
	if OnDailyReset != nil {
		OnDailyReset()
	}
}
