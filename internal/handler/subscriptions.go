package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github-release-notifier/internal/domain"
	"github-release-notifier/internal/service"
)

func Subscriptions(svc *service.SubscriptionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		email := c.Query("email")
		if email == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "email query parameter is required"})
			return
		}

		views, err := svc.ListByEmail(c.Request.Context(), email)
		if err != nil {
			respondError(c, err, nil)
			return
		}
		if views == nil {
			views = []domain.SubscriptionView{}
		}
		c.JSON(http.StatusOK, views)
	}
}
