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
	"trafficSwitching/model"
	"trafficSwitching/reporter"
	"trafficSwitching/router"
)

func main() {
	// 优化Go运行时参数
	setupRuntime()

	// 加载配置
	err := config.LoadConfig("config/config.yaml")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 启动实时指标计算器
	model.StartRealtimeMetrics()

	// 启动每日重置（7天滚动窗口）
	model.OnDailyReset = api.ResetErrorTypeStats // 注册错误类型重置回调
	model.InitDailyReset()

	// 启动上报功能
	reporter.Start()

	// 设置路由（使用router包）
	r := router.SetupRouter()

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
	numCPU := runtime.NumCPU()

	// GOMAXPROCS 设为 CPU 核心数（Go 默认已是最优，无需 *2）
	runtime.GOMAXPROCS(numCPU)

	// GC 参数：高性能场景减少 GC 频率
	gcPercent := 100
	if numCPU >= 8 {
		gcPercent = 200
	}
	oldGCPercent := debug.SetGCPercent(gcPercent)

	log.Printf("运行参数: CPU=%d, GOMAXPROCS=%d, GC=%d->%d",
		numCPU, runtime.GOMAXPROCS(0), oldGCPercent, gcPercent)
}
