package router

import (
	"log"
	"net/http"
	"runtime/debug"
	"trafficSwitching/api"

	"github.com/gin-gonic/gin"
)

// recoveryMiddleware 自定义恢复中间件（替代 gin.Recovery）：
// httputil.ReverseProxy 在响应体流式复制中断时（客户端断开、后端连接中断等）会故意
// panic(http.ErrAbortHandler) 以中止连接（Go issue 23643），gin.Recovery 会把它误报为
// 业务 panic 并打印完整堆栈；这里对哨兵 panic 原样上抛，由 net/http 静默关闭连接
// （net/http 自身忽略 http.ErrAbortHandler，不打日志），其余真实 panic 记录堆栈并返回 500。
func recoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				if err == http.ErrAbortHandler {
					panic(err)
				}
				log.Printf("[Recovery] panic recovered: %v\n%s", err, debug.Stack())
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}

// SetupRouter 配置路由
func SetupRouter() *gin.Engine {
	// 设置Gin为Release模式
	gin.SetMode(gin.ReleaseMode)

	// 创建路由，禁用不必要的中间件
	r := gin.New()

	// 只添加自定义Recovery中间件（放行ReverseProxy的设计性abort panic），移除Logger以提升性能
	r.Use(recoveryMiddleware())

	// 管理接口
	admin := r.Group("")
	{
		// 切换版本接口
		admin.POST("/switch", api.SwitchHandler)

		// 查看当前状态
		admin.GET("/status", api.StatusHandler)

		// 性能监控（带实时连接统计）
		admin.GET("/metrics", api.MetricsHandler)
	}

	// 代理所有其他请求（放在最后，避免覆盖管理接口）
	r.NoRoute(api.ProxyHandler)

	return r
}
