package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github-release-notifier/internal/domain"
	"github-release-notifier/internal/service"
)

// Confirm handles GET /api/confirm/:token.
func Confirm(svc *service.SubscriptionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Param("token")

		err := svc.Confirm(c.Request.Context(), token)
		if err == nil {
			c.JSON(http.StatusOK, gin.H{"message": "subscription confirmed successfully"})
			return
		}

		switch {
		case errors.Is(err, domain.ErrInvalidInput):
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid token"})
		case errors.Is(err, domain.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "token not found"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		}
	}
}
