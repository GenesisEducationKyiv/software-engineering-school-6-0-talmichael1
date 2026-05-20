package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github-release-notifier/internal/domain"
)

// errorMessages lets a handler override the default message per domain error.
type errorMessages map[error]string

// respondError maps a domain error to an HTTP status + JSON body. Internal
// errors are logged before responding.
func respondError(c *gin.Context, err error, msgs errorMessages) {
	status, msg := classifyError(err, msgs)
	if status == http.StatusInternalServerError {
		slog.Error("handler error", "path", c.FullPath(), "error", err)
	}
	c.JSON(status, gin.H{"error": msg})
}

func classifyError(err error, msgs errorMessages) (int, string) {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return http.StatusBadRequest, pick(msgs, domain.ErrInvalidInput, err.Error())
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, pick(msgs, domain.ErrNotFound, "not found")
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict, pick(msgs, domain.ErrConflict, "already exists")
	case errors.Is(err, domain.ErrRateLimited):
		return http.StatusServiceUnavailable, pick(msgs, domain.ErrRateLimited, "rate limited, try again later")
	default:
		return http.StatusInternalServerError, "internal server error"
	}
}

func pick(m errorMessages, key error, fallback string) string {
	if v, ok := m[key]; ok {
		return v
	}
	return fallback
}
