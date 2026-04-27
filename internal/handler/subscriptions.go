package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github-release-notifier/internal/domain"
	"github-release-notifier/internal/service"
)

// Subscriptions handles GET /api/subscriptions?email=...
func Subscriptions(svc *service.SubscriptionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		email := c.Query("email")
		if email == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "email query parameter is required"})
			return
		}

		views, err := svc.ListByEmail(c.Request.Context(), email)
		if err != nil {
			if errors.Is(err, domain.ErrInvalidInput) {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			slog.Error("list subscriptions failed", "email", email, "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}

		if views == nil {
			views = []domain.SubscriptionView{}
		}
		c.JSON(http.StatusOK, views)
	}
}
