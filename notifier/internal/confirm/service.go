// Package confirm serves the saga participant that sends subscription
// confirmation emails on behalf of the API orchestrator (ADR-0007). The same
// Service backs both transports: REST (ADR-0007) and gRPC (ADR-0008).
package confirm

import (
	"context"

	"github-release-notifier/notifier/internal/email"
)

// Service renders and sends one confirmation email. It is transport-agnostic;
// the HTTP handler and gRPC server both call Send, record the per-transport
// metric, and map its error.
type Service struct {
	sender    email.Sender
	templates email.Templates
}

func NewService(sender email.Sender) *Service {
	return &Service{sender: sender}
}

func (s *Service) Send(ctx context.Context, to, repo, confirmURL string) error {
	msg := s.templates.Confirmation(to, repo, confirmURL)
	return s.sender.Send(ctx, msg)
}
