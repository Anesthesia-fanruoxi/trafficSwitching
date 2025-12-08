package api

import (
	"net/http"
	"trafficSwitching/config"
	"trafficSwitching/model"

	"github.com/gin-gonic/gin"
)

// StatusHandler 状态查询处理器
func StatusHandler(c *gin.Context) {
	v1, v2 := config.GetBackends()

	response := model.StatusResponse{
		CurrentVersion: config.GetCurrentVersion(),
		CurrentBackend: config.GetActiveBackend(),
		Backends: map[string]string{
			"v1": v1,
			"v2": v2,
		},
	}

	c.JSON(http.StatusOK, response)
}
