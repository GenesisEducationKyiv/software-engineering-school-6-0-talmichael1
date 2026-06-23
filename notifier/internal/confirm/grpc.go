package confirm

import (
	"context"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github-release-notifier/notifier/internal/metrics"
	confirmationv1 "github-release-notifier/notifier/proto/confirmation/v1"
)

// GRPCServer is the gRPC transport for the confirmation step (ADR-0008). It is
// the HTTP/2 + protobuf sibling of Handler and shares the same Service.
type GRPCServer struct {
	confirmationv1.UnimplementedConfirmationServiceServer
	svc *Service
}

func NewGRPCServer(svc *Service) *GRPCServer {
	return &GRPCServer{svc: svc}
}

func (g *GRPCServer) SendConfirmation(ctx context.Context, req *confirmationv1.SendConfirmationRequest) (*confirmationv1.SendConfirmationResponse, error) {
	if req.GetEmail() == "" || req.GetRepo() == "" || req.GetConfirmUrl() == "" {
		return nil, status.Error(codes.InvalidArgument, "email, repo and confirm_url are required")
	}

	if err := g.svc.Send(ctx, req.GetEmail(), req.GetRepo(), req.GetConfirmUrl()); err != nil {
		metrics.ConfirmationEmails.WithLabelValues("grpc", "failed").Inc()
		slog.ErrorContext(ctx, "confirm: sending email", "email", req.GetEmail(), "repo", req.GetRepo(), "error", err)
		return nil, status.Errorf(codes.Unavailable, "sending confirmation email: %v", err)
	}

	metrics.ConfirmationEmails.WithLabelValues("grpc", "sent").Inc()
	slog.InfoContext(ctx, "confirmation email sent", "transport", "grpc", "email", req.GetEmail(), "repo", req.GetRepo())
	return &confirmationv1.SendConfirmationResponse{}, nil
}
