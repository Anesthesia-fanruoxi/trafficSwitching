package api

import (
	"net/http"
	"runtime"
	"trafficSwitching/model"

	"github.com/gin-gonic/gin"
)

// MetricsHandler 性能监控接口（扁平化输出，Prometheus友好）
func MetricsHandler(c *gin.Context) {
	// 查询参数：format=nested 使用嵌套格式（兼容旧版）
	format := c.Query("format")

	if format == "nested" {
		// 嵌套格式（旧版兼容）
		response := buildNestedMetricsResponse()
		c.JSON(http.StatusOK, response)
	} else {
		// 扁平化格式（默认，Prometheus友好）
		response := BuildFlatMetricsResponse()
		c.JSON(http.StatusOK, response)
	}
}

// BuildFlatMetricsResponse 构建扁平化监控响应（Prometheus友好）
func BuildFlatMetricsResponse() map[string]interface{} {
	// 获取请求统计
	total, success, errors := model.GetRequestStats()
	successRate := model.CalculateSuccessRate(success, total)

	// 获取错误类型统计
	errorTypes := GetErrorTypeStats()

	// 获取代理统计
	proxyStats := GetProxyStats()

	// 获取运行时统计
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// 构建扁平化响应（按分类前缀排序）
	response := map[string]interface{}{
		// 1. 基础信息
		"timestamp": model.GetTimestamp(),

		// 2. 实时指标（realtime_）
		"realtime_active_connections": model.GetActiveConnections(),
		"realtime_qps":                model.GetQPS(),
		"realtime_success_qps":        model.GetSuccessQPS(),
		"realtime_error_qps":          model.GetErrorQPS(),
		"realtime_avg_latency_ms":     model.GetAvgLatencyMs(),
		"realtime_max_latency_ms":     model.GetMaxLatencyMs(),

		// 3. 累计统计（total_）
		"total_requests":     total,
		"total_success":      success,
		"total_errors":       errors,
		"total_success_rate": successRate,

		// 4. 代理配置（proxy_）
		"proxy_cache_size":     proxyStats.CacheSize,
		"proxy_max_cache_size": proxyStats.MaxCacheSize,

		// 5. 传输配置（transport_）
		"transport_max_idle_conns":          proxyStats.Transport.MaxIdleConns,
		"transport_max_idle_conns_per_host": proxyStats.Transport.MaxIdleConnsPerHost,
		"transport_max_conns_per_host":      proxyStats.Transport.MaxConnsPerHost,

		// 6. 运行时（runtime_）
		"runtime_goroutines": runtime.NumGoroutine(),
		"runtime_memory_mb":  m.Alloc / 1024 / 1024,
		"runtime_gc_cycles":  m.NumGC,
		"runtime_cpu_cores":  runtime.NumCPU(),
		"runtime_gomaxprocs": runtime.GOMAXPROCS(0),
	}

	// 7. 错误类型（error_）
	for errType, count := range errorTypes {
		response["error_"+errType] = count
	}

	return response
}

// buildNestedMetricsResponse 构建嵌套监控响应（旧版兼容）
func buildNestedMetricsResponse() *model.MetricsResponse {
	// 获取请求统计
	total, success, errors := model.GetRequestStats()
	successRate := model.CalculateSuccessRate(success, total)

	// 获取错误类型统计
	errorTypes := GetErrorTypeStats()

	// 构建请求统计
	requestStats := &model.RequestStats{
		Total:       total,
		Success:     success,
		Errors:      errors,
		SuccessRate: successRate,
	}

	// 只有存在错误时才包含错误类型（减少响应大小）
	if len(errorTypes) > 0 {
		requestStats.ErrorTypes = errorTypes
	}

	// 获取代理统计
	proxyStats := GetProxyStats()

	// 获取运行时统计
	runtimeStats := getRuntimeStats()

	return &model.MetricsResponse{
		Timestamp: model.GetTimestamp(),
		Requests:  requestStats,
		Proxy:     proxyStats,
		Runtime:   runtimeStats,
	}
}

// getRuntimeStats 获取运行时统计（轻量级）
func getRuntimeStats() *model.RuntimeStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return &model.RuntimeStats{
		Goroutines: runtime.NumGoroutine(),
		MemoryMB:   m.Alloc / 1024 / 1024,
		GCCycles:   m.NumGC,
		CPUCores:   runtime.NumCPU(),
		GoMaxProcs: runtime.GOMAXPROCS(0),
	}
}
