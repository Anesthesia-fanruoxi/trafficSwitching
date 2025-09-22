# Traffic Switching 流量切换代理服务

一个高性能的流量切换代理服务，支持在两个后端版本之间动态切换流量，专为生产环境设计，可承载10万+QPS的高并发访问。

## ✨ 核心特性

- 🚀 **极致性能**: 针对10万+QPS优化，支持生产级高并发
- 🔄 **无缝切换**: 支持v1/v2版本间的实时流量切换
- 🏥 **健康检查**: 自动检测后端服务健康状态，确保切换安全
- 📊 **性能监控**: 实时监控请求统计、成功率、内存使用等指标
- ⚡ **零停机**: 热切换不影响正在处理的请求
- 🛡️ **容错机制**: 完善的错误处理和故障转移
- 💾 **状态持久化**: 自动保存激活版本状态，服务重启后自动恢复
- 🔧 **配置驱动**: 基于YAML的灵活配置管理

## 🏗️ 项目架构

```
trafficSwitching/
├── main.go              # 主程序入口，服务器配置
├── go.mod              # Go模块依赖管理
├── go.sum              # 依赖校验文件
├── config/             # 配置模块
│   ├── config.go       # 配置管理逻辑
│   ├── config.yaml     # 服务配置文件
│   └── .version        # 版本状态文件（自动生成）
└── api/                # API处理模块
    └── handlers.go     # HTTP请求处理器
```

## 🚀 快速开始

### 环境要求

- Go 1.24.0+
- 内存: 建议2GB+
- CPU: 多核处理器（自动优化GOMAXPROCS）

### 安装运行

1. **克隆项目**
```bash
git clone <repository-url>
cd trafficSwitching
```

2. **安装依赖**
```bash
go mod tidy
```

3. **配置服务**
编辑 `config/config.yaml`:
```yaml
server:
  port: 8080

backends:
  v1: "http://your-backend-v1:port"
  v2: "http://your-backend-v2:port"

# 当前激活版本: v1 或 v2
active_version: "v1"
```

4. **启动服务**
```bash
go run main.go
```

服务将在配置的端口启动，默认为8080。

## 📡 API接口

### 1. 代理转发 (所有请求)
- **路径**: `/*` (除特殊接口外的所有路径)
- **方法**: 所有HTTP方法
- **功能**: 将请求转发到当前激活的后端版本

### 2. 版本切换
- **路径**: `POST /switch`
- **请求体**:
```json
{
  "version": "v1"  // 或 "v2"
}
```
- **响应**:
```json
{
  "success": true,
  "message": "已切换到版本 v1",
  "version": "v1",
  "backend": "http://192.168.11.0",
  "health_check": "passed"
}
```

### 3. 状态查询
- **路径**: `GET /status`
- **响应**:
```json
{
  "current_version": "v1",
  "current_backend": "http://192.168.11.0",
  "backends": {
    "v1": "http://192.168.11.0",
    "v2": "http://192.168.11.1"
  }
}
```

### 4. 健康检查
- **路径**: `GET /health-check?version=v1`
- **参数**: `version` (v1或v2)
- **响应**:
```json
{
  "version": "v1",
  "backend": "http://192.168.11.0",
  "healthy": true,
  "check_duration_ms": 45,
  "timestamp": 1695123456
}
```

### 5. 性能监控
- **路径**: `GET /metrics`
- **响应**:
```json
{
  "timestamp": 1695123456,
  "requests": {
    "total": 1000000,
    "success": 999500,
    "errors": 500,
    "success_rate": "99.95%"
  },
  "proxy": {
    "cache_size": 2,
    "transport": {
      "max_idle_conns": 2000,
      "max_idle_conns_per_host": 200,
      "max_conns_per_host": 500
    }
  },
  "runtime": {
    "goroutines": 150,
    "memory_mb": 45,
    "gc_cycles": 23,
    "cpu_cores": 8,
    "gomaxprocs": 16
  }
}
```

## ⚡ 性能优化

### 高并发优化配置

- **连接池**: 6000总连接数，每个后端200个空闲连接
- **GOMAXPROCS**: 自动设置为CPU核心数的2倍
- **GC优化**: 根据CPU核心数动态调整GC百分比
- **Keep-Alive**: 5分钟连接复用，减少握手开销
- **零分配**: 预定义错误响应，避免运行时字符串拼接

### 生产级配置

```go
// 针对30个Gateway Pod的优化配置
MaxIdleConns:        6000  // 总连接池
MaxIdleConnsPerHost: 200   // 每个Pod连接数
MaxConnsPerHost:     400   // 每个Pod最大连接
IdleConnTimeout:     300s  // 连接复用时间
```

## 🛠️ 配置说明

### config.yaml 详细配置

```yaml
server:
  port: 8080                    # 代理服务监听端口

backends:
  v1: "http://192.168.11.0"    # 版本1后端地址
  v2: "http://192.168.11.1"    # 版本2后端地址

active_version: "v1"           # 当前激活版本（仅作为默认值）
```

### 💾 状态持久化机制

**重要特性**: 服务会自动将版本切换状态保存到隐藏文件 `config/.version` 中，确保服务重启后能够恢复到上次的激活版本。

#### 极简设计
- **状态文件**: `config/.version` (隐藏文件)
- **文件内容**: 只包含版本字符串，如 `v1` 或 `v2`
- **无需解析**: 直接读写纯文本，性能最优
- **自动恢复**: 服务启动时自动读取并恢复上次版本

#### 持久化流程
1. **版本切换时**: 直接写入版本字符串到 `.version` 文件
2. **服务启动时**: 读取 `.version` 文件恢复版本，失败则使用配置默认值
3. **故障安全**: 文件损坏或不存在时自动回退到 `config.yaml` 默认版本

### 环境变量支持

可通过环境变量覆盖配置：
- `SERVER_PORT`: 服务端口
- `BACKEND_V1`: V1后端地址  
- `BACKEND_V2`: V2后端地址
- `ACTIVE_VERSION`: 激活版本（仅在无状态文件时生效）

## 🔍 监控与运维

### 日志监控
- 启动信息: 端口、激活版本、性能参数
- 错误统计: 每1000个错误记录一次汇总
- 健康检查: 后端服务状态变化日志

### 性能指标
- QPS统计: 总请求数、成功率、错误率
- 连接池状态: 缓存大小、连接数配置
- 运行时信息: Goroutine数量、内存使用、GC次数

### 故障排查
1. 检查后端服务可用性: `GET /health-check?version=v1`
2. 查看性能指标: `GET /metrics`
3. 检查当前状态: `GET /status`

## 🚀 部署建议

### Docker部署
```dockerfile
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod tidy && go build -o traffic-switching

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/traffic-switching .
COPY --from=builder /app/config ./config
CMD ["./traffic-switching"]
```

### Kubernetes部署
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: traffic-switching
spec:
  replicas: 3
  selector:
    matchLabels:
      app: traffic-switching
  template:
    metadata:
      labels:
        app: traffic-switching
    spec:
      containers:
      - name: traffic-switching
        image: traffic-switching:latest
        ports:
        - containerPort: 8080
        resources:
          requests:
            memory: "512Mi"
            cpu: "500m"
          limits:
            memory: "2Gi"
            cpu: "2000m"
```

## 🔧 开发指南

### 本地开发
```bash
# 安装依赖
go mod tidy

# 运行测试
go test ./...

# 本地运行
go run main.go

# 编译
go build -o traffic-switching
```

### 性能测试
```bash
# 使用wrk进行压力测试
wrk -t12 -c400 -d30s http://localhost:8080/

# 版本切换测试
curl -X POST http://localhost:8080/switch \
  -H "Content-Type: application/json" \
  -d '{"version": "v2"}'
```

## 📊 技术栈

- **语言**: Go 1.24.0
- **Web框架**: Gin (高性能HTTP框架)
- **配置管理**: YAML v3
- **反向代理**: Go标准库 httputil.ReverseProxy
- **并发安全**: sync包 (RWMutex, atomic)

## 🤝 贡献指南

1. Fork 项目
2. 创建特性分支 (`git checkout -b feature/AmazingFeature`)
3. 提交更改 (`git commit -m 'Add some AmazingFeature'`)
4. 推送到分支 (`git push origin feature/AmazingFeature`)
5. 打开 Pull Request

## 📄 许可证

本项目采用 MIT 许可证 - 查看 [LICENSE](LICENSE) 文件了解详情。

## 🆘 支持

如遇问题或需要帮助，请：
1. 查看 [Issues](../../issues) 页面
2. 创建新的 Issue 描述问题
3. 联系维护团队

---

**注意**: 本服务专为生产环境设计，建议在部署前进行充分的性能测试和安全评估。
