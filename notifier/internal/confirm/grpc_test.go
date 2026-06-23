package confirm

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	confirmationv1 "github-release-notifier/notifier/proto/confirmation/v1"
)

func TestGRPCSendConfirmationSuccess(t *testing.T) {
	sender := &fakeSender{}
	srv := NewGRPCServer(NewService(sender))

	_, err := srv.SendConfirmation(context.Background(), &confirmationv1.SendConfirmationRequest{
		Email:      "user@example.com",
		Repo:       "golang/go",
		ConfirmUrl: "https://x/confirm?t=abc",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sender.sent) != 1 || sender.sent[0].To != "user@example.com" {
		t.Fatalf("unexpected sent messages: %+v", sender.sent)
	}
}

func TestGRPCSendConfirmationMissingFields(t *testing.T) {
	srv := NewGRPCServer(NewService(&fakeSender{}))

	_, err := srv.SendConfirmation(context.Background(), &confirmationv1.SendConfirmationRequest{
		Email: "user@example.com",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestGRPCSendConfirmationSendFailure(t *testing.T) {
	srv := NewGRPCServer(NewService(&fakeSender{err: errors.New("mailgun down")}))

	_, err := srv.SendConfirmation(context.Background(), &confirmationv1.SendConfirmationRequest{
		Email:      "u@e.com",
		Repo:       "golang/go",
		ConfirmUrl: "https://x",
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %v, want Unavailable", status.Code(err))
	}
}
