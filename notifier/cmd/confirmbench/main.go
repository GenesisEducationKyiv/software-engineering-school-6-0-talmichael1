// Command confirmbench compares the confirmation step's two transports, REST
// and gRPC (HW10, ADR-0008). Both are served in-process by the same Service
// with a no-op email backend, then driven by one identical closed-loop worker
// pool — so the only variable is the transport, not the load tool.
//
//	go run ./cmd/confirmbench                 # serve only (for ghz / autocannon)
//	go run ./cmd/confirmbench -load           # in-process fair comparison
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github-release-notifier/notifier/internal/confirm"
	"github-release-notifier/notifier/internal/email"
	confirmationv1 "github-release-notifier/notifier/proto/confirmation/v1"
)

const (
	restAddr = "127.0.0.1:8082"
	grpcAddr = "127.0.0.1:9092"
)

type noopSender struct{}

func (noopSender) Send(context.Context, email.Message) error { return nil }

// wire counts actual bytes on the socket for one transport, so the bandwidth
// comparison reflects HTTP framing + headers + body, not just the payload.
type wire struct{ tx, rx int64 }

type countingConn struct {
	net.Conn
	w *wire
}

func (c countingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	atomic.AddInt64(&c.w.rx, int64(n))
	return n, err
}

func (c countingConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	atomic.AddInt64(&c.w.tx, int64(n))
	return n, err
}

func countingDial(w *wire) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return countingConn{Conn: c, w: w}, nil
	}
}

var (
	restWire wire
	grpcWire wire
)

func main() {
	load := flag.Bool("load", false, "run the in-process fair comparison instead of just serving")
	workers := flag.Int("workers", 50, "concurrent workers (closed-loop)")
	requests := flag.Int("requests", 200000, "total requests per transport")
	flag.Parse()

	svc := confirm.NewService(noopSender{})
	stopREST := serveREST(svc)
	stopGRPC := serveGRPC(svc)
	defer stopGRPC()
	defer stopREST()
	time.Sleep(300 * time.Millisecond)

	if !*load {
		log.Printf("serving REST on %s and gRPC on %s (Ctrl-C to stop)", restAddr, grpcAddr)
		select {}
	}

	restCall := newRESTCaller(*workers)
	grpcCall, grpcClose := newGRPCCaller()
	defer grpcClose()

	// Warm up both paths before measuring.
	for range 2000 {
		_ = restCall(context.Background())
		_ = grpcCall(context.Background())
	}

	fmt.Printf("\nfair comparison: %d workers, %d requests each, same harness\n\n", *workers, *requests)

	rest0 := snapshot(&restWire)
	restRes := run(*workers, *requests, restCall)
	report("REST  (HTTP/1.1 + JSON, keep-alive pool)", restRes, delta(&restWire, rest0, restRes.requests))

	grpc0 := snapshot(&grpcWire)
	grpcRes := run(*workers, *requests, grpcCall)
	report("gRPC  (HTTP/2 + protobuf, 1 multiplexed conn)", grpcRes, delta(&grpcWire, grpc0, grpcRes.requests))
}

type bytesPerReq struct{ tx, rx float64 }

func snapshot(w *wire) wire {
	return wire{tx: atomic.LoadInt64(&w.tx), rx: atomic.LoadInt64(&w.rx)}
}

func delta(w *wire, before wire, reqs int) bytesPerReq {
	tx := atomic.LoadInt64(&w.tx) - before.tx
	rx := atomic.LoadInt64(&w.rx) - before.rx
	return bytesPerReq{tx: float64(tx) / float64(reqs), rx: float64(rx) / float64(reqs)}
}

type result struct {
	requests  int
	errors    int
	dur       time.Duration
	latencies []time.Duration
}

func run(workers, total int, call func(context.Context) error) result {
	var wg sync.WaitGroup
	per := total / workers
	perWorkerLat := make([][]time.Duration, workers)
	perWorkerErr := make([]int, workers)
	start := time.Now()
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			ctx := context.Background()
			lat := make([]time.Duration, 0, per)
			for range per {
				t0 := time.Now()
				err := call(ctx)
				lat = append(lat, time.Since(t0))
				if err != nil {
					perWorkerErr[w]++
				}
			}
			perWorkerLat[w] = lat
		}(w)
	}
	wg.Wait()
	dur := time.Since(start)

	all := make([]time.Duration, 0, per*workers)
	errs := 0
	for w := range workers {
		all = append(all, perWorkerLat[w]...)
		errs += perWorkerErr[w]
	}
	return result{requests: per * workers, errors: errs, dur: dur, latencies: all}
}

func report(name string, r result, b bytesPerReq) {
	rps := float64(r.requests) / r.dur.Seconds()
	slices.Sort(r.latencies)
	fmt.Printf("%-46s %9.0f req/s   p50 %6s  p99 %6s   %3.0f↑/%3.0f↓ = %3.0f B/req   (errors=%d)\n",
		name, rps,
		pct(r.latencies, 0.50).Round(10*time.Microsecond),
		pct(r.latencies, 0.99).Round(10*time.Microsecond),
		b.tx, b.rx, b.tx+b.rx, r.errors)
}

func pct(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(p * float64(len(sorted)))
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

func newRESTCaller(maxConns int) func(context.Context) error {
	transport := &http.Transport{
		MaxIdleConns:        maxConns,
		MaxIdleConnsPerHost: maxConns,
		MaxConnsPerHost:     maxConns,
		DialContext:         countingDial(&restWire),
	}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	body := mustJSON()
	url := "http://" + restAddr + "/internal/confirmations"
	return func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("status %d", resp.StatusCode)
		}
		return nil
	}
}

func newGRPCCaller() (func(context.Context) error, func()) {
	conn, err := grpc.NewClient(grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return countingDial(&grpcWire)(ctx, "tcp", addr)
		}),
	)
	if err != nil {
		log.Fatalf("grpc dial: %v", err)
	}
	client := confirmationv1.NewConfirmationServiceClient(conn)
	req := &confirmationv1.SendConfirmationRequest{
		Email: "user@example.com", Repo: "golang/go", ConfirmUrl: "https://example.com/confirm?t=abc",
	}
	return func(ctx context.Context) error {
			_, err := client.SendConfirmation(ctx, req)
			return err
		}, func() {
			_ = conn.Close()
		}
}

func mustJSON() []byte {
	b, _ := json.Marshal(map[string]string{
		"email": "user@example.com", "repo": "golang/go", "confirm_url": "https://example.com/confirm?t=abc",
	})
	return b
}

func serveREST(svc *confirm.Service) func() {
	mux := http.NewServeMux()
	mux.Handle("/internal/confirmations", confirm.NewHandler(svc))
	srv := &http.Server{Addr: restAddr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("rest: %v", err)
		}
	}()
	return func() { _ = srv.Close() }
}

func serveGRPC(svc *confirm.Service) func() {
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("grpc listen: %v", err)
	}
	g := grpc.NewServer()
	confirmationv1.RegisterConfirmationServiceServer(g, confirm.NewGRPCServer(svc))
	go func() {
		if err := g.Serve(lis); err != nil {
			log.Fatalf("grpc serve: %v", err)
		}
	}()
	return func() { g.Stop() }
}
