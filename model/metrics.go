package model

import (
	"sync/atomic"
	"time"
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
	TotalRequests   int64
	SuccessRequests int64
	ErrorRequests   int64

	// 实时指标
	ActiveConnections int64 // 当前活跃连接数
	lastTotal         int64 // 上次统计时的总请求数
	lastSuccess       int64 // 上次统计时的成功数
	lastErrors        int64 // 上次统计时的错误数
	currentQPS        int64 // 当前 QPS
	currentSuccessQPS int64 // 每秒成功数
	currentErrorQPS   int64 // 每秒错误数

	// 延迟统计（纳秒）
	totalLatency int64 // 当前秒累计延迟
	latencyCount int64 // 当前秒请求数
	maxLatency   int64 // 当前秒最大延迟
	currentAvgMs int64 // 平均延迟（毫秒）
	currentMaxMs int64 // 最大延迟（毫秒）
)

// IncrementTotal 增加总请求数
func IncrementTotal() {
	atomic.AddInt64(&TotalRequests, 1)
}

// IncrementSuccess 增加成功数
func IncrementSuccess() {
	atomic.AddInt64(&SuccessRequests, 1)
}

// IncrementError 增加错误数
func IncrementError() {
	atomic.AddInt64(&ErrorRequests, 1)
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
			// QPS
			total := atomic.LoadInt64(&TotalRequests)
			atomic.StoreInt64(&currentQPS, total-atomic.LoadInt64(&lastTotal))
			atomic.StoreInt64(&lastTotal, total)

			// 成功/错误 QPS
			success := atomic.LoadInt64(&SuccessRequests)
			errors := atomic.LoadInt64(&ErrorRequests)
			atomic.StoreInt64(&currentSuccessQPS, success-atomic.LoadInt64(&lastSuccess))
			atomic.StoreInt64(&currentErrorQPS, errors-atomic.LoadInt64(&lastErrors))
			atomic.StoreInt64(&lastSuccess, success)
			atomic.StoreInt64(&lastErrors, errors)

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

// CalculateSuccessRate 计算成功率（避免重复计算）
func CalculateSuccessRate(success, total int64) string {
	if total == 0 {
		return "0.00%"
	}
	rate := float64(success) / float64(total) * 100
	// 使用固定格式，避免sprintf开销
	if rate >= 99.995 {
		return "100.00%"
	}
	return formatFloat(rate)
}

// formatFloat 格式化浮点数（轻量级实现，支持0-100）
func formatFloat(f float64) string {
	intPart := int(f)
	decPart := int((f-float64(intPart))*100 + 0.5) // 四舍五入

	// 处理进位
	if decPart >= 100 {
		intPart++
		decPart = 0
	}

	// 处理100%的情况
	if intPart >= 100 {
		return "100.00%"
	}

	return string(rune(intPart/10+'0')) + string(rune(intPart%10+'0')) + "." +
		string(rune(decPart/10+'0')) + string(rune(decPart%10+'0')) + "%"
}

// GetTimestamp 获取当前时间戳（复用）
func GetTimestamp() int64 {
	return time.Now().Unix()
}
