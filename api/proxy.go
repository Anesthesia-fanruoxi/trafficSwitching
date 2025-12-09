package api

import (
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"trafficSwitching/config"
	"trafficSwitching/model"

	"github.com/gin-gonic/gin"
)

var (
	// 全局代理实例，避免重复创建
	proxyCache = make(map[string]*httputil.ReverseProxy)
	proxyMutex sync.RWMutex

	// 缓存配置
	maxCacheSize = 2

	// 错误类型统计
	errorTypeCount = make(map[string]*int64)

	// 生产级高性能配置 - 针对Gateway 30个Pod优化
	transport = &http.Transport{
		// 连接池配置 - 2个VIP，每个VIP背后30个Gateway Pod
		MaxIdleConns:        10000,             // 总连接池，2个VIP × 500 = 1000实际 + 冗余
		MaxIdleConnsPerHost: 500,               // 每个VIP 500个空闲连接 → 分散到30个Pod，每个Pod约16个
		MaxConnsPerHost:     1000,              // 每个VIP最大1000连接，避免VIP过载
		IdleConnTimeout:     300 * time.Second, // 5分钟空闲超时，平衡连接复用与资源释放
		DisableKeepAlives:   false,             // 启用Keep-Alive，提升性能
		DisableCompression:  true,              // 禁用压缩，减少CPU开销

		// 生产级连接设置
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,   // 5秒连接超时，减少假超时
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

// getOrCreateProxy 获取或创建代理实例
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

	// 设置 Director：透传或兜底设置 IP 头部
	defaultDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		defaultDirector(req)

		// 兜底：如果 Nginx 未传递 IP 头部，则自行获取
		if req.Header.Get("X-Forwarded-For") == "" {
			clientIP := req.RemoteAddr
			if ip, _, err := net.SplitHostPort(clientIP); err == nil {
				clientIP = ip
			}
			req.Header.Set("X-Forwarded-For", clientIP)
		}

		if req.Header.Get("X-Real-IP") == "" {
			// 从 X-Forwarded-For 取第一个 IP
			if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
				if idx := strings.Index(xff, ","); idx > 0 {
					req.Header.Set("X-Real-IP", strings.TrimSpace(xff[:idx]))
				} else {
					req.Header.Set("X-Real-IP", xff)
				}
			}
		}

	}

	// 错误处理 - 统计错误类型
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		w.Header().Set("X-Proxy-Error", "1") // 标记代理错误

		// 识别错误类型
		var errorType string
		var statusCode int
		var isCanceled bool

		errStr := err.Error()
		switch {
		case strings.HasSuffix(errStr, "connection refused"):
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
		case strings.Contains(errStr, "EOF"):
			errorType = "eof"
			statusCode = http.StatusBadGateway
		case strings.Contains(errStr, "broken pipe"):
			errorType = "broken_pipe"
			statusCode = http.StatusBadGateway
		case strings.Contains(errStr, "context canceled"):
			errorType = "client_canceled"
			statusCode = http.StatusBadGateway
			isCanceled = true
		default:
			errorType = "backend_error"
			statusCode = http.StatusBadGateway
			// 记录未分类的错误，方便排查
			log.Printf("WARN: 未分类错误: %s", errStr)
		}

		// 客户端取消单独计数，不算失败
		if isCanceled {
			model.IncrementCanceled()
		} else {
			model.IncrementError()
		}

		// 错误类型统计（轻量级）
		proxyMutex.Lock()
		if errorTypeCount[errorType] == nil {
			var counter int64
			errorTypeCount[errorType] = &counter
		}
		atomic.AddInt64(errorTypeCount[errorType], 1)
		proxyMutex.Unlock()

		// 条件日志（减少IO开销）
		if currentErrors := atomic.LoadInt64(&model.ErrorRequests); currentErrors%1000 == 0 {
			log.Printf("ERROR: [%s] 累计错误数: %d", errorType, currentErrors)
		}

		// 快速响应
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = w.Write(model.GetErrorResponse(errorType))
	}

	// 简单的缓存大小控制
	if len(proxyCache) >= maxCacheSize {
		for k := range proxyCache {
			delete(proxyCache, k)
			break
		}
	}

	proxyCache[backend] = proxy
	return proxy
}

// ProxyHandler 代理处理器（带连接统计）
func ProxyHandler(c *gin.Context) {
	// 记录开始时间
	start := time.Now()

	// 活跃连接数 +1，请求结束时 -1 并记录延迟
	model.IncrementActive()

	// 请求计数
	model.IncrementTotal()

	// 获取后端
	backend := config.GetActiveBackend()

	// 请求日志
	clientIP := c.ClientIP()
	method := c.Request.Method
	path := c.Request.URL.Path
	log.Printf("[REQ] %s %s %s -> %s", clientIP, method, path, backend)

	// 快速路径：直接从缓存获取代理
	proxyMutex.RLock()
	proxy, exists := proxyCache[backend]
	proxyMutex.RUnlock()

	if !exists {
		// 慢路径：创建新代理
		proxy = getOrCreateProxy(backend)
		if proxy == nil {
			model.IncrementError()
			model.DecrementActive()
			log.Printf("[ERR] %s %s %s -> 代理初始化失败", clientIP, method, path)
			c.Header("Content-Type", "application/json")
			c.String(http.StatusInternalServerError, string(model.ErrProxyInitError))
			return
		}
	}

	// 执行代理转发
	proxy.ServeHTTP(c.Writer, c.Request)

	// 记录延迟和活跃连接
	latency := time.Since(start)
	model.DecrementActive()
	model.RecordLatency(latency.Nanoseconds())

	// 判断成功/失败
	status := c.Writer.Status()
	if c.Writer.Header().Get("X-Proxy-Error") == "" {
		// 代理成功（后端正常响应）
		model.IncrementSuccess()
		model.RecordStatusCode(status)
		log.Printf("[RES] %s %s %s -> %d %v", clientIP, method, path, status, latency)
	} else {
		// 代理失败
		log.Printf("[ERR] %s %s %s -> %d %v (proxy error)", clientIP, method, path, status, latency)
	}
}

// GetProxyStats 获取代理统计（供metrics使用）
func GetProxyStats() *model.ProxyStats {
	proxyMutex.RLock()
	cacheSize := len(proxyCache)
	proxyMutex.RUnlock()

	return &model.ProxyStats{
		CacheSize:    cacheSize,
		MaxCacheSize: maxCacheSize,
		Transport: model.TransportConfig{
			MaxIdleConns:        transport.MaxIdleConns,
			MaxIdleConnsPerHost: transport.MaxIdleConnsPerHost,
			MaxConnsPerHost:     transport.MaxConnsPerHost,
		},
	}
}

// GetErrorTypeStats 获取错误类型统计
func GetErrorTypeStats() map[string]int64 {
	proxyMutex.RLock()
	defer proxyMutex.RUnlock()

	errorStats := make(map[string]int64)
	for errType, counter := range errorTypeCount {
		if counter != nil {
			errorStats[errType] = atomic.LoadInt64(counter)
		}
	}
	return errorStats
}

// ResetErrorTypeStats 重置错误类型统计（每日重置时调用）
func ResetErrorTypeStats() {
	proxyMutex.Lock()
	defer proxyMutex.Unlock()

	for errType, counter := range errorTypeCount {
		if counter != nil {
			atomic.StoreInt64(counter, 0)
		}
		// 或者直接删除
		delete(errorTypeCount, errType)
	}
}
