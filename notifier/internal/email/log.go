package email

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// LogSender writes email content to stdout instead of sending it (dev backend).
type LogSender struct{}

func NewLogSender() *LogSender {
	return &LogSender{}
}

func (s *LogSender) Send(_ context.Context, msg Message) error {
	slog.Info("EMAIL (console backend)", "to", msg.To, "subject", msg.Subject)
	fmt.Println(strings.Repeat("-", 60))
	fmt.Printf("To: %s\nSubject: %s\n\n%s\n", msg.To, msg.Subject, msg.Text)
	fmt.Println(strings.Repeat("-", 60))
	return nil
}
