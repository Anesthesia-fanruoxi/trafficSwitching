package api

import (
	"fmt"
	"log"
	"net/http"
	"trafficSwitching/config"
	"trafficSwitching/model"

	"github.com/gin-gonic/gin"
)

// SwitchHandler 切换版本处理器
func SwitchHandler(c *gin.Context) {
	var req model.SwitchRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: "参数错误",
		})
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
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: "无效的版本，只支持 v1 或 v2",
		})
		return
	}

	// 直接执行切换（健康检查由部署流程保证）
	if config.SwitchVersion(req.Version) {
		log.Printf("版本切换成功: %s -> %s", req.Version, targetBackend)
		c.JSON(http.StatusOK, model.SwitchResponse{
			Success: true,
			Message: fmt.Sprintf("已切换到版本 %s", req.Version),
			Version: req.Version,
			Backend: targetBackend,
		})
	} else {
		c.JSON(http.StatusInternalServerError, model.SwitchResponse{
			Success: false,
			Error:   "切换失败：配置更新错误",
			Version: req.Version,
		})
	}
}
