package confirmation

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	confirmationv1 "github-release-notifier/notifier/proto/confirmation/v1"
)

type fakeConfirmServer struct {
	confirmationv1.UnimplementedConfirmationServiceServer
	lastReq *confirmationv1.SendConfirmationRequest
	err     error
}

func (f *fakeConfirmServer) SendConfirmation(_ context.Context, req *confirmationv1.SendConfirmationRequest) (*confirmationv1.SendConfirmationResponse, error) {
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	return &confirmationv1.SendConfirmationResponse{}, nil
}

func dialBuf(t *testing.T, fake *fakeConfirmServer) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	confirmationv1.RegisterConfirmationServiceServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestGRPCClientSendSuccess(t *testing.T) {
	fake := &fakeConfirmServer{}
	c := NewGRPCClient(dialBuf(t, fake), time.Second)

	if err := c.Send(context.Background(), "user@example.com", "golang/go", "https://x/confirm?t=abc"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.lastReq.GetEmail() != "user@example.com" || fake.lastReq.GetConfirmUrl() != "https://x/confirm?t=abc" {
		t.Fatalf("unexpected request: %+v", fake.lastReq)
	}
}

func TestGRPCClientSendError(t *testing.T) {
	fake := &fakeConfirmServer{err: status.Error(codes.Unavailable, "down")}
	c := NewGRPCClient(dialBuf(t, fake), time.Second)

	if err := c.Send(context.Background(), "u@e.com", "golang/go", "https://x"); err == nil {
		t.Fatal("expected error")
	}
}
