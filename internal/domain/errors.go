package domain

import "errors"

var (
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("already exists")
	ErrInvalidInput = errors.New("invalid input")
	ErrRateLimited  = errors.New("rate limited by external API")
	ErrExternalAPI  = errors.New("external API error")
)
