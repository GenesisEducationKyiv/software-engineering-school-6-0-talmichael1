package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github-release-notifier/internal/domain"
	"github-release-notifier/internal/service"
)

type subscribeRequest struct {
	Email string `json:"email" binding:"required"`
	Repo  string `json:"repo" binding:"required"`
}

func Subscribe(svc *service.SubscriptionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req subscribeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}

		if err := svc.Subscribe(c.Request.Context(), req.Email, req.Repo); err != nil {
			respondError(c, err, errorMessages{
				domain.ErrNotFound:    "repository not found on GitHub",
				domain.ErrConflict:    "email already subscribed to this repository",
				domain.ErrRateLimited: "GitHub API rate limit exceeded, try again later",
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "subscription successful, confirmation email sent"})
	}
}
