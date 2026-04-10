package email

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// LogSender is a development email backend that writes email content to stdout
// instead of sending it. Similar to Django's console.EmailBackend.
type LogSender struct{}

func NewLogSender() *LogSender {
	return &LogSender{}
}

func (s *LogSender) SendConfirmation(_ context.Context, to, repo, confirmURL string) error {
	slog.Info("EMAIL (console backend)",
		"type", "confirmation",
		"to", to,
		"repo", repo,
	)
	fmt.Println(strings.Repeat("-", 60))
	fmt.Printf("To: %s\nSubject: Confirm your subscription to %s releases\n\n", to, repo)
	fmt.Printf("Please confirm your subscription:\n%s\n", confirmURL)
	fmt.Println(strings.Repeat("-", 60))
	return nil
}

func (s *LogSender) SendReleaseNotification(_ context.Context, to, repo, tag, releaseURL, unsubURL string) error {
	slog.Info("EMAIL (console backend)",
		"type", "release_notification",
		"to", to,
		"repo", repo,
		"tag", tag,
	)
	fmt.Println(strings.Repeat("-", 60))
	fmt.Printf("To: %s\nSubject: New release of %s: %s\n\n", to, repo, tag)
	fmt.Printf("Version: %s\nDetails: %s\n\nUnsubscribe: %s\n", tag, releaseURL, unsubURL)
	fmt.Println(strings.Repeat("-", 60))
	return nil
}
