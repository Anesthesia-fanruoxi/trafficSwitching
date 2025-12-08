package router

import (
	"trafficSwitching/api"

	"github.com/gin-gonic/gin"
)

// SetupRouter 配置路由
func SetupRouter() *gin.Engine {
	// 设置Gin为Release模式
	gin.SetMode(gin.ReleaseMode)

	// 创建路由，禁用不必要的中间件
	r := gin.New()

	// 只添加Recovery中间件，移除Logger以提升性能
	r.Use(gin.Recovery())

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
