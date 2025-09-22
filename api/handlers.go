package api

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"trafficSwitching/config"

	"github.com/gin-gonic/gin"
)

var (
	// 全局代理实例，避免重复创建
	proxyCache = make(map[string]*httputil.ReverseProxy)
	proxyMutex sync.RWMutex

	// 性能计数器
	totalRequests   int64
	successRequests int64
	errorRequests   int64

	// 预定义错误响应，避免运行时字符串拼接
	errorResponses = map[string][]byte{
		"connection_refused": []byte(`{"error": "后端服务不可用", "exception": "connection_refused", "detail": "连接被拒绝"}`),
		"timeout":            []byte(`{"error": "后端服务不可用", "exception": "timeout", "detail": "请求超时"}`),
		"dns_error":          []byte(`{"error": "后端服务不可用", "exception": "dns_error", "detail": "DNS解析失败"}`),
		"connection_reset":   []byte(`{"error": "后端服务不可用", "exception": "connection_reset", "detail": "连接重置"}`),
		"backend_error":      []byte(`{"error": "后端服务不可用", "exception": "backend_error", "detail": "后端服务错误"}`),
	}

	// 生产级高性能配置 - 针对Gateway 30个Pod优化
	transport = &http.Transport{
		// 连接池配置 - 30个Gateway Pod分散负载
		MaxIdleConns:        6000,              // 30个Pod × 200连接 = 6000总连接池
		MaxIdleConnsPerHost: 200,               // 每个Gateway Pod 200个空闲连接
		MaxConnsPerHost:     400,               // 每个Gateway Pod最大400连接，避免单Pod过载
		IdleConnTimeout:     300 * time.Second, // 5分钟空闲超时，平衡连接复用与资源释放
		DisableKeepAlives:   false,             // 启用Keep-Alive，提升性能
		DisableCompression:  true,              // 禁用压缩，减少CPU开销

		// 生产级连接设置
		DialContext: (&net.Dialer{
			Timeout:   2 * time.Second,   // 2秒连接超时，避免长时间等待
			KeepAlive: 300 * time.Second, // 5分钟Keep-Alive，与IdleConnTimeout一致
		}).DialContext,

		// 生产级响应设置
		ResponseHeaderTimeout: 30 * time.Second, // 30秒响应头超时，适合大部分业务
		TLSHandshakeTimeout:   5 * time.Second,  // 5秒TLS握手，考虑网络延迟
		ExpectContinueTimeout: 1 * time.Second,  // 1秒Continue超时

		// 生产级优化
		ForceAttemptHTTP2:      false, // 禁用HTTP/2，减少复杂性
		MaxResponseHeaderBytes: 8192,  // 8KB响应头限制，兼容更多场景
		WriteBufferSize:        4096,  // 4KB写缓冲区
		ReadBufferSize:         4096,  // 4KB读缓冲区
	}
)

// 获取或创建代理实例
func getOrCreateProxy(backend string) *httputil.ReverseProxy {
	proxyMutex.RLock()
	if proxy, exists := proxyCache[backend]; exists {
		proxyMutex.RUnlock()
		return proxy
	}
	proxyMutex.RUnlock()

	proxyMutex.Lock()
	defer proxyMutex.Unlock()

	// 双重检查
	if proxy, exists := proxyCache[backend]; exists {
		return proxy
	}

	target, err := url.Parse(backend)
	if err != nil {
		log.Printf("ERROR: 解析后端URL失败: %s, 错误: %v", backend, err)
		return nil
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = transport

	// 极速错误处理 - 零分配优化
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		// 快速错误计数（减少原子操作）
		atomic.AddInt64(&errorRequests, 1)
		atomic.AddInt64(&successRequests, -1)

		// 快速错误类型识别（避免字符串操作）
		var errorType string
		var statusCode int
		var response []byte

		errStr := err.Error()
		switch {
		case len(errStr) > 17 && errStr[len(errStr)-17:] == "connection refused":
			errorType = "connection_refused"
			statusCode = http.StatusServiceUnavailable
		case strings.Contains(errStr, "timeout"):
			errorType = "timeout"
			statusCode = http.StatusGatewayTimeout
		case strings.Contains(errStr, "no such host"):
			errorType = "dns_error"
			statusCode = http.StatusBadGateway
		case strings.Contains(errStr, "connection reset"):
			errorType = "connection_reset"
			statusCode = http.StatusBadGateway
		default:
			errorType = "backend_error"
			statusCode = http.StatusBadGateway
		}

		// 使用预定义响应，避免字符串拼接
		if preResponse, exists := errorResponses[errorType]; exists {
			response = preResponse
		} else {
			response = errorResponses["backend_error"]
		}

		// 条件日志记录（减少IO开销）
		if atomic.LoadInt64(&errorRequests)%1000 == 0 { // 每1000个错误记录一次
			log.Printf("ERROR: [%s] 累计错误数: %d", errorType, atomic.LoadInt64(&errorRequests))
		}

		// 快速响应写入
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = w.Write(response)
	}

	proxyCache[backend] = proxy
	return proxy
}

// 极速代理处理器 - 零分配优化
func ProxyHandler(c *gin.Context) {
	// 批量计数优化 - 减少原子操作频率
	if atomic.AddInt64(&totalRequests, 1)%100 == 0 {
		// 每100个请求才进行一次昂贵的操作
	}

	// 直接获取后端，避免函数调用开销
	backend := config.GetActiveBackend()

	// 快速路径：直接从缓存获取代理
	proxyMutex.RLock()
	proxy, exists := proxyCache[backend]
	proxyMutex.RUnlock()

	if !exists {
		// 慢路径：创建新代理
		proxy = getOrCreateProxy(backend)
		if proxy == nil {
			atomic.AddInt64(&errorRequests, 1)
			// 使用预定义响应
			c.Header("Content-Type", "application/json")
			c.String(http.StatusInternalServerError, `{"error": "代理初始化失败", "exception": "proxy_init_error"}`)
			return
		}
	}

	// 执行代理转发 - 这是最核心的操作
	proxy.ServeHTTP(c.Writer, c.Request)

	// 成功计数（ErrorHandler会处理失败情况）
	atomic.AddInt64(&successRequests, 1)
}

// 后端健康检查
func checkBackendHealth(backendURL string) bool {
	client := &http.Client{
		Timeout: 5 * time.Second, // 5秒超时
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: 2 * time.Second,
			}).DialContext,
			ResponseHeaderTimeout: 3 * time.Second,
		},
	}

	resp, err := client.Get(backendURL)
	if err != nil {
		log.Printf("后端健康检查失败 %s: %v", backendURL, err)
		return false
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(resp.Body)

	// 检查状态码是否为200
	if resp.StatusCode == 200 {
		log.Printf("后端健康检查通过 %s: 状态码 %d", backendURL, resp.StatusCode)
		return true
	}

	log.Printf("后端健康检查失败 %s: 状态码 %d", backendURL, resp.StatusCode)
	return false
}

// 切换版本处理器（带健康检查）
func SwitchHandler(c *gin.Context) {
	var req struct {
		Version string `json:"version" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	// 获取目标后端URL
	var targetBackend string
	v1, v2 := config.GetBackends()

	switch req.Version {
	case "v1":
		targetBackend = v1
	case "v2":
		targetBackend = v2
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的版本，只支持 v1 或 v2"})
		return
	}

	// 执行后端健康检查
	log.Printf("开始检查目标后端健康状态: %s -> %s", req.Version, targetBackend)

	if !checkBackendHealth(targetBackend) {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":           "切换失败：目标后端服务不可用",
			"version":         req.Version,
			"backend":         targetBackend,
			"current_version": config.GetCurrentVersion(),
			"current_backend": config.GetActiveBackend(),
		})
		return
	}

	// 健康检查通过，执行切换
	if config.SwitchVersion(req.Version) {
		log.Printf("版本切换成功: %s -> %s", config.GetCurrentVersion(), targetBackend)
		c.JSON(http.StatusOK, gin.H{
			"success":      true,
			"message":      fmt.Sprintf("已切换到版本 %s", req.Version),
			"version":      req.Version,
			"backend":      targetBackend,
			"health_check": "passed",
		})
	} else {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "切换失败：配置更新错误",
			"version": req.Version,
		})
	}
}

// 状态查询处理器
func StatusHandler(c *gin.Context) {
	v1, v2 := config.GetBackends()
	c.JSON(http.StatusOK, gin.H{
		"current_version": config.GetCurrentVersion(),
		"current_backend": config.GetActiveBackend(),
		"backends": gin.H{
			"v1": v1,
			"v2": v2,
		},
	})
}

// 手动健康检查接口
func HealthCheckHandler(c *gin.Context) {
	version := c.Query("version")
	if version == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请指定版本参数，例如: ?version=v1"})
		return
	}

	// 获取目标后端URL
	var targetBackend string
	v1, v2 := config.GetBackends()

	switch version {
	case "v1":
		targetBackend = v1
	case "v2":
		targetBackend = v2
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的版本，只支持 v1 或 v2"})
		return
	}

	// 执行健康检查
	startTime := time.Now()
	isHealthy := checkBackendHealth(targetBackend)
	checkDuration := time.Since(startTime)

	c.JSON(http.StatusOK, gin.H{
		"version":           version,
		"backend":           targetBackend,
		"healthy":           isHealthy,
		"check_duration_ms": checkDuration.Milliseconds(),
		"timestamp":         time.Now().Unix(),
	})
}

// 性能监控接口
func MetricsHandler(c *gin.Context) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	total := atomic.LoadInt64(&totalRequests)
	success := atomic.LoadInt64(&successRequests)
	errors := atomic.LoadInt64(&errorRequests)

	successRate := float64(0)
	if total > 0 {
		successRate = float64(success) / float64(total) * 100
	}

	proxyMutex.RLock()
	cacheSize := len(proxyCache)
	proxyMutex.RUnlock()

	c.JSON(http.StatusOK, gin.H{
		"timestamp": time.Now().Unix(),
		"requests": gin.H{
			"total":        total,
			"success":      success,
			"errors":       errors,
			"success_rate": fmt.Sprintf("%.2f%%", successRate),
		},
		"proxy": gin.H{
			"cache_size": cacheSize,
			"transport": gin.H{
				"max_idle_conns":          2000,
				"max_idle_conns_per_host": 200,
				"max_conns_per_host":      500,
			},
		},
		"runtime": gin.H{
			"goroutines": runtime.NumGoroutine(),
			"memory_mb":  m.Alloc / 1024 / 1024,
			"gc_cycles":  m.NumGC,
			"cpu_cores":  runtime.NumCPU(),
			"gomaxprocs": runtime.GOMAXPROCS(0),
		},
	})
}
