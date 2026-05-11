package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github-release-notifier/internal/domain"
	"github-release-notifier/internal/service"
)

func Confirm(svc *service.SubscriptionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := svc.Confirm(c.Request.Context(), c.Param("token")); err != nil {
			respondError(c, err, errorMessages{
				domain.ErrInvalidInput: "invalid token",
				domain.ErrNotFound:     "token not found",
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "subscription confirmed successfully"})
	}
}
