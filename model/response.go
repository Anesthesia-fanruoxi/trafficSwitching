package model

// ErrorResponse 错误响应模型
type ErrorResponse struct {
	Error     string `json:"error"`
	Exception string `json:"exception,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// SwitchRequest 切换请求
type SwitchRequest struct {
	Version string `json:"version" binding:"required"`
}

// SwitchResponse 切换响应
type SwitchResponse struct {
	Success     bool   `json:"success"`
	Message     string `json:"message,omitempty"`
	Version     string `json:"version,omitempty"`
	Backend     string `json:"backend,omitempty"`
	HealthCheck string `json:"health_check,omitempty"`
	Error       string `json:"error,omitempty"`
}

// StatusResponse 状态响应
type StatusResponse struct {
	CurrentVersion string            `json:"current_version"`
	CurrentBackend string            `json:"current_backend"`
	Backends       map[string]string `json:"backends"`
}

// 预定义错误响应（零分配）
var (
	ErrConnectionRefused = []byte(`{"error": "后端服务不可用", "exception": "connection_refused", "detail": "连接被拒绝"}`)
	ErrTimeout           = []byte(`{"error": "后端服务不可用", "exception": "timeout", "detail": "请求超时"}`)
	ErrDNSError          = []byte(`{"error": "后端服务不可用", "exception": "dns_error", "detail": "DNS解析失败"}`)
	ErrConnectionReset   = []byte(`{"error": "后端服务不可用", "exception": "connection_reset", "detail": "连接重置"}`)
	ErrBackendError      = []byte(`{"error": "后端服务不可用", "exception": "backend_error", "detail": "后端服务错误"}`)
	ErrProxyInitError    = []byte(`{"error": "代理初始化失败", "exception": "proxy_init_error"}`)
)

// GetErrorResponse 根据错误类型获取预定义响应（零分配）
func GetErrorResponse(errorType string) []byte {
	switch errorType {
	case "connection_refused":
		return ErrConnectionRefused
	case "timeout":
		return ErrTimeout
	case "dns_error":
		return ErrDNSError
	case "connection_reset":
		return ErrConnectionReset
	default:
		return ErrBackendError
	}
}
