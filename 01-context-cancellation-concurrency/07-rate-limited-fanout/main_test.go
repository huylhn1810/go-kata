package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/semaphore"
	"golang.org/x/time/rate"
)

// ---------------------------------------------------------------------------
// Helper: build a FanOutClient that talks to the given httptest.Server.
// Always uses spec values: QPS=10, burst=20, MaxInFlight=8.
// ---------------------------------------------------------------------------
func setupClient(t *testing.T, ts *httptest.Server) FanOutClient {
	t.Helper()

	fc, err := NewFanOutClient(
		WithLimiter(rate.NewLimiter(rate.Limit(10), 20)),
		WithSem(semaphore.NewWeighted(8)),
		WithTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatalf("NewFanOutClient: %v", err)
	}

	// Replace transport so every request goes to the test server.
	fco := fc.(*fanOutClient)
	fco.client.Transport = &rewriteTransport{
		base:  ts.URL,
		inner: ts.Client().Transport,
	}
	return fc
}

// rewriteTransport prepends the test-server base URL to every request.
type rewriteTransport struct {
	base  string
	inner http.RoundTripper
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	newURL := rt.base + "/" + req.URL.Path
	req2 := req.Clone(req.Context())
	u, err := req2.URL.Parse(newURL)
	if err != nil {
		return nil, err
	}
	req2.URL = u
	return rt.inner.RoundTrip(req2)
}

// ---------------------------------------------------------------------------
// Test 1 – Backpressure: concurrent requests must not exceed MaxInFlight=8
// ---------------------------------------------------------------------------
func TestBackpressure(t *testing.T) {
	t.Run("concurrent goroutines must not exceed MaxInFlight=8", func(t *testing.T) {
		const maxInFlight int64 = 8
		var concurrent int64
		var peak int64

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cur := atomic.AddInt64(&concurrent, 1)
			for {
				old := atomic.LoadInt64(&peak)
				if cur <= old || atomic.CompareAndSwapInt64(&peak, old, cur) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
			atomic.AddInt64(&concurrent, -1)
			fmt.Fprintf(w, `{"ok":true}`)
		}))
		defer ts.Close()

		fc := setupClient(t, ts)

		userIDs := make([]int, 30)
		for i := range userIDs {
			userIDs[i] = i + 1
		}

		_, _ = fc.FetchAll(context.Background(), userIDs)

		if p := atomic.LoadInt64(&peak); p > maxInFlight {
			t.Errorf("peak in-flight = %d, want <= %d (backpressure violated)", p, maxInFlight)
		}
	})
}

// ---------------------------------------------------------------------------
// Test 2 – Context cancellation must stop remaining work
// ---------------------------------------------------------------------------
func TestContextCancellation(t *testing.T) {
	t.Run("cancellation must propagate and stop remaining work", func(t *testing.T) {
		var served int64

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt64(&served, 1)
			time.Sleep(200 * time.Millisecond)
			fmt.Fprintf(w, `{"ok":true}`)
		}))
		defer ts.Close()

		fc := setupClient(t, ts)

		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()

		userIDs := make([]int, 50)
		for i := range userIDs {
			userIDs[i] = i + 1
		}

		_, err := fc.FetchAll(ctx, userIDs)
		if err == nil {
			t.Fatal("expected error from cancelled context, got nil")
		}

		s := atomic.LoadInt64(&served)
		if s >= int64(len(userIDs)) {
			t.Errorf("served %d / %d requests — cancellation did not stop work", s, len(userIDs))
		}
	})
}

// ---------------------------------------------------------------------------
// Test 3 – Rate limiting must be enforced (via rate.Limiter, NOT time.Sleep)
// ---------------------------------------------------------------------------
func TestRateLimiterUsed(t *testing.T) {
	t.Run("QPS=10 burst=20 must be enforced via rate.Limiter not time.Sleep", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"ok":true}`)
		}))
		defer ts.Close()

		fc := setupClient(t, ts)

		// 30 requests: burst covers first 20 instantly, remaining 10 at 10/sec ≈ 1s
		userIDs := make([]int, 30)
		for i := range userIDs {
			userIDs[i] = i + 1
		}

		start := time.Now()
		_, _ = fc.FetchAll(context.Background(), userIDs)
		elapsed := time.Since(start)

		if elapsed < 500*time.Millisecond {
			t.Errorf("elapsed = %v; rate limiter seems bypassed (expected >= 500ms)", elapsed)
		}
	})
}

// ---------------------------------------------------------------------------
// Test 4 – Must NOT use http.DefaultClient
// ---------------------------------------------------------------------------
func TestNoDefaultClient(t *testing.T) {
	t.Run("FanOutClient must use its own http.Client not http.DefaultClient", func(t *testing.T) {
		fc, err := NewFanOutClient(
			WithLimiter(rate.NewLimiter(rate.Limit(10), 20)),
			WithSem(semaphore.NewWeighted(8)),
			WithTransport(&http.Transport{
				MaxIdleConns:    100,
				IdleConnTimeout: 90 * time.Second,
			}),
			WithTimeout(10*time.Second),
		)
		if err != nil {
			t.Fatalf("NewFanOutClient: %v", err)
		}

		fco, ok := fc.(*fanOutClient)
		if !ok {
			t.Fatal("NewFanOutClient did not return *fanOutClient")
		}

		if fco.client == http.DefaultClient {
			t.Error("FanOutClient is using http.DefaultClient; must use a dedicated client")
		}

		if fco.client.Transport == nil {
			t.Error("http.Client.Transport is nil; should be explicitly configured")
		}

		if fco.client.Timeout == 0 {
			t.Error("http.Client.Timeout is 0; should be explicitly set")
		}
	})
}
