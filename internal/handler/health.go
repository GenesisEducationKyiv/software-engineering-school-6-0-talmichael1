package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Health handles GET /health for liveness checks.
func Health() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}
