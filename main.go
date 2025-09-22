package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"
	"trafficSwitching/api"
	"trafficSwitching/config"

	"github.com/gin-gonic/gin"
)

func main() {
	// 优化Go运行时参数
	setupRuntime()

	// 加载配置
	err := config.LoadConfig("config/config.yaml")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 设置Gin为Release模式以提升性能
	gin.SetMode(gin.ReleaseMode)

	// 创建路由，禁用不必要的中间件
	r := gin.New()

	// 只添加Recovery中间件，移除Logger以提升性能
	r.Use(gin.Recovery())

	// 代理所有请求
	r.NoRoute(api.ProxyHandler)

	// 切换版本接口
	r.POST("/switch", api.SwitchHandler)

	// 查看当前状态
	r.GET("/status", api.StatusHandler)

	// 性能监控
	r.GET("/metrics", api.MetricsHandler)

	// 启动服务
	port := config.GetServerPort()
	addr := fmt.Sprintf(":%d", port)
	log.Printf("代理服务启动在端口 %d", port)
	log.Printf("当前激活版本: %s -> %s", config.GetCurrentVersion(), config.GetActiveBackend())

	// 创建10万+QPS高性能HTTP服务器
	server := &http.Server{
		Addr:    addr,
		Handler: r,
		// 10万+QPS优化配置
		ReadTimeout:       10 * time.Second,  // 增加读取超时
		WriteTimeout:      30 * time.Second,  // 增加写入超时
		IdleTimeout:       300 * time.Second, // 5分钟空闲超时
		ReadHeaderTimeout: 5 * time.Second,   // 增加请求头超时
		MaxHeaderBytes:    1 << 21,           // 2MB请求头限制
	}

	// 优雅关闭支持
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("正在关闭服务...")

	// 给服务30秒时间完成现有请求
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("服务关闭超时: %v", err)
	} else {
		log.Println("服务已优雅关闭")
	}
}

// 设置Go运行时参数 - 极限性能优化
func setupRuntime() {
	// 获取CPU核心数
	numCPU := runtime.NumCPU()

	// 设置GOMAXPROCS为CPU核心数，充分利用多核
	runtime.GOMAXPROCS(numCPU)

	// 极限性能优化
	runtime.GC()                   // 立即执行一次GC
	runtime.GOMAXPROCS(numCPU * 2) // 设置为CPU核心数的2倍，提高并发

	// 设置GC参数以减少GC压力
	var gcPercent int
	if numCPU >= 8 {
		// 高性能服务器：减少GC频率
		gcPercent = 200
	} else {
		// 普通服务器：平衡性能和内存
		gcPercent = 100
	}

	// 设置GC百分比
	oldGCPercent := debug.SetGCPercent(gcPercent)

	log.Printf("当前运行参数: CPU核心数=%d, GOMAXPROCS=%d, GCPercent=%d->%d",
		numCPU, runtime.GOMAXPROCS(0), oldGCPercent, gcPercent)
}
