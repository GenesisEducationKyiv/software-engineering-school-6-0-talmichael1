package confirmation

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"

	confirmationv1 "github-release-notifier/notifier/proto/confirmation/v1"
)

// GRPCClient is the HW10 gRPC transport for the confirmation step. It satisfies
// the same ConfirmationSender contract as RESTClient (ADR-0008), so the
// orchestrator is unchanged regardless of which one is wired in.
type GRPCClient struct {
	client  confirmationv1.ConfirmationServiceClient
	timeout time.Duration
}

func NewGRPCClient(conn grpc.ClientConnInterface, timeout time.Duration) *GRPCClient {
	return &GRPCClient{
		client:  confirmationv1.NewConfirmationServiceClient(conn),
		timeout: timeout,
	}
}

func (c *GRPCClient) Send(ctx context.Context, to, repo, confirmURL string) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	_, err := c.client.SendConfirmation(ctx, &confirmationv1.SendConfirmationRequest{
		Email:      to,
		Repo:       repo,
		ConfirmUrl: confirmURL,
	})
	if err != nil {
		return fmt.Errorf("calling notifier (grpc): %w", err)
	}
	return nil
}
