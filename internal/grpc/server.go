package grpc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github-release-notifier/internal/domain"
	pb "github-release-notifier/internal/grpc/proto"
	"github-release-notifier/internal/service"
)

// Server implements the gRPC SubscriptionService, delegating to the same
// business logic layer as the REST handlers.
type Server struct {
	pb.UnimplementedSubscriptionServiceServer
	svc *service.SubscriptionService
}

func NewServer(svc *service.SubscriptionService) *Server {
	return &Server{svc: svc}
}

func (s *Server) Subscribe(ctx context.Context, req *pb.SubscribeRequest) (*pb.SubscribeResponse, error) {
	err := s.svc.Subscribe(ctx, req.Email, req.Repo)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.SubscribeResponse{Message: "subscription successful, confirmation email sent"}, nil
}

func (s *Server) Confirm(ctx context.Context, req *pb.ConfirmRequest) (*pb.ConfirmResponse, error) {
	err := s.svc.Confirm(ctx, req.Token)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.ConfirmResponse{Message: "subscription confirmed successfully"}, nil
}

func (s *Server) Unsubscribe(ctx context.Context, req *pb.UnsubscribeRequest) (*pb.UnsubscribeResponse, error) {
	err := s.svc.Unsubscribe(ctx, req.Token)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.UnsubscribeResponse{Message: "unsubscribed successfully"}, nil
}

func (s *Server) ListSubscriptions(ctx context.Context, req *pb.ListRequest) (*pb.ListResponse, error) {
	views, err := s.svc.ListByEmail(ctx, req.Email)
	if err != nil {
		return nil, mapError(err)
	}

	items := make([]*pb.SubscriptionItem, 0, len(views))
	for _, v := range views {
		items = append(items, &pb.SubscriptionItem{
			Email:       v.Email,
			Repo:        v.Repo,
			Confirmed:   v.Confirmed,
			LastSeenTag: v.LastSeenTag,
		})
	}
	return &pb.ListResponse{Subscriptions: items}, nil
}

func mapError(err error) error {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, domain.ErrConflict):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, domain.ErrRateLimited):
		return status.Error(codes.Unavailable, "GitHub API rate limited, try again later")
	default:
		return status.Error(codes.Internal, "internal server error")
	}
}
