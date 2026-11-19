package api

import (
	"bufio"
	"context"
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

	// 错误类型计数（类型有限，启动时预建，运行期零锁原子读写）
	errorTypeCounters = map[string]*atomic.Int64{
		"connection_refused": {},
		"timeout":            {},
		"dns_error":          {},
		"connection_reset":   {},
		"eof":                {},
		"broken_pipe":        {},
		"client_canceled":    {},
		"backend_error":      {},
	}

	// 未注册错误类型的兜底归类（防止新增分类漏注册导致 nil deref）
	fallbackErrorType = "backend_error"

	// 响应体拷贝缓冲池：ReverseProxy 的 copyBuffer 会为每个响应分配 32KB 缓冲，
	// 池化复用可显著降低大流量下的 GC 压力（BufferPool 为 httputil.BufferPool 接口）
	copyBufferSize = 32 * 1024
	copyBufferPool = sync.Pool{
		New: func() any {
			return make([]byte, copyBufferSize)
		},
	}

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

// 拦截逻辑封装：读取 O(1) 预计算字典并替换为底层小写
func restoreLowerHeaders(headers http.Header) {
	lowerMap := config.GetParsedLowerHeadersMap()
	if len(lowerMap) == 0 {
		return
	}

	// Fast lookup & replace
	for canonicalKey, lowerKey := range lowerMap {
		if vals, exists := headers[canonicalKey]; exists {
			// 直接从底层 map 中删除大写并强行写入小写
			delete(headers, canonicalKey)
			headers[lowerKey] = vals
		}
	}
}

// errorCounter 取错误类型对应的原子计数器；未注册类型兜底归入 fallbackErrorType，
// 避免新增分类时漏注册触发 nil 指针解引用
func errorCounter(errType string) *atomic.Int64 {
	if c, ok := errorTypeCounters[errType]; ok {
		return c
	}
	return errorTypeCounters[fallbackErrorType]
}

// bufferPool 把 sync.Pool 适配成 httputil.BufferPool 接口
type bufferPool struct{}

func (bufferPool) Get() []byte {
	return copyBufferPool.Get().([]byte)
}

func (bufferPool) Put(b []byte) {
	if cap(b) < copyBufferSize {
		return // 非我方尺寸的切片不放回，避免池被污染
	}
	copyBufferPool.Put(b[:copyBufferSize])
}

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
	// 响应体拷贝改用对象池缓冲，避免每个请求新分配 32KB
	proxy.BufferPool = bufferPool{}

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
			if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
				if idx := strings.Index(xff, ","); idx > 0 {
					req.Header.Set("X-Real-IP", strings.TrimSpace(xff[:idx]))
				} else {
					req.Header.Set("X-Real-IP", xff)
				}
			}
		}

		// 动态转换为小写 Header 发往后端
		restoreLowerHeaders(req.Header)
	}

	// 错误处理 - 统计错误类型
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		// 标记代理层错误：走 request context 内部标志，
		// 不写响应头（避免标记外泄给客户端，以及后端伪造同名头导致误判）
		if mark, ok := r.Context().Value(proxyErrorMarkKey{}).(*proxyErrorMark); ok {
			mark.set = true
		}

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
			// 代理层错误的状态码同样计入分桶，否则 status_5xx 与 errors 长期对不上
			// （取消时响应已截断/客户端已走，不计入状态码统计）
			model.RecordStatusCode(statusCode)
		}

		// 错误类型统计（预建固定类型计数器，零锁）
		errorCounter(errorType).Add(1)

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

// proxyErrorMark 代理层错误标记（request context 内部通信，不经响应头）
type proxyErrorMark struct {
	set bool
}

// proxyErrorMarkKey context key 类型（私有类型防碰撞）
type proxyErrorMarkKey struct{}

// proxyResponseWriter 自定义写入包装器，用于在响应打给 TCP 网卡之前，夺取最高控制权
type proxyResponseWriter struct {
	http.ResponseWriter
}

// WriteHeader 拦截冲刷 HTTP Header 时的函数
// 注意：响应头小写化不能放在 ModifyResponse——ReverseProxy 随后 copyHeader 用 Add 写入
// 会重新 Canonical 化，必须在这里（写网卡前最后一刻）改底层 key 才生效
func (w *proxyResponseWriter) WriteHeader(status int) {
	restoreLowerHeaders(w.Header())
	w.ResponseWriter.WriteHeader(status)
}

// Flush 支持原生的 Server-Sent Events 或 Streaming 流式能力
func (w *proxyResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack 支持 Websocket 的透传能力
func (w *proxyResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// CloseNotify 保持低版本兼容性
func (w *proxyResponseWriter) CloseNotify() <-chan bool {
	if c, ok := w.ResponseWriter.(http.CloseNotifier); ok {
		return c.CloseNotify()
	}
	return nil
}

// ProxyHandler 代理处理器（带连接统计）
func ProxyHandler(c *gin.Context) {
	// 记录开始时间
	start := time.Now()

	// 活跃连接数 +1，请求结束时 -1 并记录延迟
	model.IncrementActive()
	defer func() {
		model.DecrementActive()
		model.RecordLatency(time.Since(start).Nanoseconds())
	}()

	// 请求计数
	model.IncrementTotal()

	// 获取后端
	backend := config.GetActiveBackend()

	// 快速路径：直接从缓存获取代理
	proxyMutex.RLock()
	proxy, exists := proxyCache[backend]
	proxyMutex.RUnlock()

	if !exists {
		// 慢路径：创建新代理
		proxy = getOrCreateProxy(backend)
		if proxy == nil {
			model.IncrementError()
			model.RecordStatusCode(http.StatusInternalServerError)
			c.Header("Content-Type", "application/json")
			c.String(http.StatusInternalServerError, string(model.ErrProxyInitError))
			return
		}
	}

	// 挂代理层错误标记，供 ErrorHandler 置位（同 goroutine 串行读写，无并发）
	mark := &proxyErrorMark{}
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), proxyErrorMarkKey{}, mark))

	// 用我们的包裹器把 Gin 的默认响应包起来
	pw := &proxyResponseWriter{ResponseWriter: c.Writer}

	// 执行代理转发
	// ReverseProxy 在响应体流式复制中断时会故意 panic(http.ErrAbortHandler) 中止连接
	// （Go issue 23643，绝大多数为客户端提前断开），此处捕获补记取消统计后原样上抛，
	// 由路由层 recoveryMiddleware 放行给 net/http 静默关闭连接（响应已截断，不能按成功计）
	func() {
		defer func() {
			if err := recover(); err != nil {
				if err == http.ErrAbortHandler {
					model.IncrementCanceled()
				}
				panic(err)
			}
		}()
		proxy.ServeHTTP(pw, c.Request)
	}()

	// 判断成功/失败：只要不是代理自身错误（能访问到后端并拿到响应），
	// 后端返回的任何状态码（含 4xx/5xx）都算代理成功
	if !mark.set {
		model.IncrementSuccess()
		// 记录后端状态码 (这里不能用 c.Writer.Status 因为可能被包裹了，改用原生)
		model.RecordStatusCode(c.Writer.Status())
	}
	// 代理失败由 ErrorHandler 处理，已调用 IncrementError()
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

// GetErrorTypeStats 获取错误类型统计（固定类型只读 map，零锁）
func GetErrorTypeStats() map[string]int64 {
	errorStats := make(map[string]int64, len(errorTypeCounters))
	for errType, counter := range errorTypeCounters {
		errorStats[errType] = counter.Load()
	}
	return errorStats
}

// ResetErrorTypeStats 重置错误类型统计（每日重置时调用，零锁）
func ResetErrorTypeStats() {
	for _, counter := range errorTypeCounters {
		counter.Store(0)
	}
}
