package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github-release-notifier/internal/domain"
	"github-release-notifier/internal/service"
)

type subscribeRequest struct {
	Email string `json:"email" binding:"required"`
	Repo  string `json:"repo" binding:"required"`
}

// Subscribe handles POST /api/subscribe.
func Subscribe(svc *service.SubscriptionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req subscribeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}

		err := svc.Subscribe(c.Request.Context(), req.Email, req.Repo)
		if err == nil {
			c.JSON(http.StatusOK, gin.H{"message": "subscription successful, confirmation email sent"})
			return
		}

		switch {
		case errors.Is(err, domain.ErrInvalidInput):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case errors.Is(err, domain.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "repository not found on GitHub"})
		case errors.Is(err, domain.ErrConflict):
			c.JSON(http.StatusConflict, gin.H{"error": "email already subscribed to this repository"})
		case errors.Is(err, domain.ErrRateLimited):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "GitHub API rate limit exceeded, try again later"})
		default:
			slog.Error("subscribe failed", "email", req.Email, "repo", req.Repo, "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		}
	}
}
