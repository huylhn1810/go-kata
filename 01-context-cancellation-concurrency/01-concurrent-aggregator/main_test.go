package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ─────────────────────────────────────────────
// Fake / Stub implementations of Service
// ─────────────────────────────────────────────

// stubService is a controllable test double for the Service interface.
// It simulates network latency via delay and can be configured to fail.
type stubService struct {
	result string
	err    error
	delay  time.Duration
}

func (s *stubService) FetchData(ctx context.Context, _ string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(s.delay):
		if s.err != nil {
			return "", s.err
		}
		return s.result, nil
	}
}

// ─────────────────────────────────────────────
// NewUserAggregator — constructor option tests
// ─────────────────────────────────────────────

func TestNewUserAggregator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		opts        []Option
		wantErr     bool
		wantTimeout time.Duration
	}{
		{
			name:        "defaults are applied correctly",
			opts:        nil,
			wantErr:     false,
			wantTimeout: 3 * time.Second,
		},
		{
			name:        "valid custom timeout is applied",
			opts:        []Option{NewWithTimeout(10 * time.Second)},
			wantErr:     false,
			wantTimeout: 10 * time.Second,
		},
		{
			name:        "zero timeout is valid (not negative)",
			opts:        []Option{NewWithTimeout(0)},
			wantErr:     false,
			wantTimeout: 0,
		},
		{
			name:    "negative timeout returns error",
			opts:    []Option{NewWithTimeout(-1 * time.Second)},
			wantErr: true,
		},
		{
			name:        "custom logger is applied",
			opts:        []Option{NewWithLogger(nil)},
			wantErr:     false,
			wantTimeout: 3 * time.Second,
		},
		{
			name: "multiple options can be combined",
			opts: []Option{
				NewWithTimeout(5 * time.Second),
				NewWithLogger(nil),
			},
			wantErr:     false,
			wantTimeout: 5 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ua, err := NewUserAggregator(tt.opts...)

			if (err != nil) != tt.wantErr {
				t.Fatalf("NewUserAggregator() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if ua.timeout != tt.wantTimeout {
				t.Errorf("timeout = %v, want %v", ua.timeout, tt.wantTimeout)
			}
		})
	}
}

// ─────────────────────────────────────────────
// Aggregate — table-driven tests
// ─────────────────────────────────────────────

func TestAggregate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		timeout     time.Duration
		services    []Service
		ctx         func() (context.Context, context.CancelFunc)
		wantErr     bool
		wantErrIs   error    // checked with errors.Is
		wantContain []string // all substrings that must appear in the result
	}{
		{
			name:     "no services configured",
			timeout:  2 * time.Second,
			services: nil,
			wantErr:  true,
		},
		{
			name:    "single service succeeds",
			timeout: 2 * time.Second,
			services: []Service{
				&stubService{result: "solo-result", delay: 10 * time.Millisecond},
			},
			wantContain: []string{"solo-result"},
		},
		{
			name:    "all services succeed — results combined",
			timeout: 2 * time.Second,
			services: []Service{
				&stubService{result: "Name: Alice", delay: 10 * time.Millisecond},
				&stubService{result: "Orders: 5", delay: 10 * time.Millisecond},
			},
			wantContain: []string{"Name: Alice", "Orders: 5"},
		},
		{
			name:    "one service fails — aggregate returns error",
			timeout: 2 * time.Second,
			services: []Service{
				&stubService{result: "Name: Alice", delay: 10 * time.Millisecond},
				&stubService{err: fmt.Errorf("database unreachable"), delay: 10 * time.Millisecond},
			},
			wantErr: true,
		},
		{
			name:    "all services fail — aggregate returns error",
			timeout: 2 * time.Second,
			services: []Service{
				&stubService{err: errors.New("svc1 down"), delay: 10 * time.Millisecond},
				&stubService{err: errors.New("svc2 down"), delay: 10 * time.Millisecond},
			},
			wantErr: true,
		},
		{
			name:    "aggregator timeout fires before services complete",
			timeout: 50 * time.Millisecond,
			services: []Service{
				&stubService{result: "slow-1", delay: 500 * time.Millisecond},
				&stubService{result: "slow-2", delay: 500 * time.Millisecond},
			},
			wantErr:   true,
			wantErrIs: context.DeadlineExceeded,
		},
		{
			name:    "caller cancels context before services complete",
			timeout: 5 * time.Second,
			services: []Service{
				&stubService{result: "data", delay: 500 * time.Millisecond},
			},
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				go func() {
					time.Sleep(20 * time.Millisecond)
					cancel()
				}()
				return ctx, cancel
			},
			wantErr:   true,
			wantErrIs: context.Canceled,
		},
		{
			name:    "THE DOMINO EFFECT: one service fails immediately, slow service is cancelled",
			timeout: 10 * time.Second, // Long timeout - should not be reached
			services: []Service{
				&stubService{err: errors.New("immediate failure"), delay: 0},
				&stubService{result: "slow-data", delay: 5 * time.Second}, // Should be cancelled
			},
			wantErr: true,
			// This test verifies fast-fail: the function should return immediately,
			// not wait for the slow service to complete
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ua, err := NewUserAggregator(
				NewWithTimeout(tt.timeout),
				NewWithServices(tt.services),
			)
			if err != nil {
				t.Fatalf("constructor error: %v", err)
			}

			ctx := context.Background()
			var cancel context.CancelFunc
			if tt.ctx != nil {
				ctx, cancel = tt.ctx()
				defer cancel()
			}

			// Measure timing for fast-fail verification
			start := time.Now()
			result, err := ua.Aggregate(ctx, "user-test")
			elapsed := time.Since(start)

			if (err != nil) != tt.wantErr {
				t.Fatalf("Aggregate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
				t.Errorf("Aggregate() error = %v, want errors.Is(%v)", err, tt.wantErrIs)
			}
			for _, want := range tt.wantContain {
				if !strings.Contains(result, want) {
					t.Errorf("result %q does not contain %q", result, want)
				}
			}

			// Verify fast-fail behavior for "DOMINO EFFECT" test
			if strings.Contains(tt.name, "DOMINO EFFECT") {
				// Should return in < 1 second, not wait for the 5-second service
				if elapsed > 1*time.Second {
					t.Errorf("DOMINO EFFECT test took %v, expected fast-fail in < 1s (slow service should have been cancelled)", elapsed)
				}
			}
		})
	}
}

// ─────────────────────────────────────────────
// ProfileService — table-driven tests
// ─────────────────────────────────────────────

func TestProfileService_FetchData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		delay      time.Duration
		shouldFail bool
		cancelCtx  bool
		wantResult string
		wantErr    bool
		wantErrIs  error
	}{
		{
			name:       "success — returns profile data",
			delay:      10 * time.Millisecond,
			shouldFail: false,
			wantResult: "Name: Alice",
		},
		{
			name:       "shouldFail=true — returns error",
			delay:      10 * time.Millisecond,
			shouldFail: true,
			wantErr:    true,
		},
		{
			name:      "context already cancelled — returns Canceled",
			delay:     500 * time.Millisecond,
			cancelCtx: true,
			wantErr:   true,
			wantErrIs: context.Canceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			if tt.cancelCtx {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel() // cancel immediately
			}

			svc := NewProfileService(tt.delay, tt.shouldFail)
			result, err := svc.FetchData(ctx, "user-1")

			if (err != nil) != tt.wantErr {
				t.Fatalf("FetchData() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
				t.Errorf("FetchData() error = %v, want errors.Is(%v)", err, tt.wantErrIs)
			}
			if !tt.wantErr && result != tt.wantResult {
				t.Errorf("FetchData() = %q, want %q", result, tt.wantResult)
			}
		})
	}
}

// ─────────────────────────────────────────────
// OrderService — table-driven tests
// ─────────────────────────────────────────────

func TestOrderService_FetchData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		delay      time.Duration
		shouldFail bool
		cancelCtx  bool
		wantResult string
		wantErr    bool
		wantErrIs  error
	}{
		{
			name:       "success — returns order data",
			delay:      10 * time.Millisecond,
			shouldFail: false,
			wantResult: "Orders: 5",
		},
		{
			name:       "shouldFail=true — returns error",
			delay:      10 * time.Millisecond,
			shouldFail: true,
			wantErr:    true,
		},
		{
			name:      "context already cancelled — returns Canceled",
			delay:     500 * time.Millisecond,
			cancelCtx: true,
			wantErr:   true,
			wantErrIs: context.Canceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			if tt.cancelCtx {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			svc := NewOrderService(tt.delay, tt.shouldFail)
			result, err := svc.FetchData(ctx, "user-1")

			if (err != nil) != tt.wantErr {
				t.Fatalf("FetchData() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
				t.Errorf("FetchData() error = %v, want errors.Is(%v)", err, tt.wantErrIs)
			}
			if !tt.wantErr && result != tt.wantResult {
				t.Errorf("FetchData() = %q, want %q", result, tt.wantResult)
			}
		})
	}
}

// ─────────────────────────────────────────────
// Full integration — real services wired together
// ─────────────────────────────────────────────

func TestAggregate_Integration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		aggregatorTimeout time.Duration
		profileFail       bool
		profileDelay      time.Duration
		orderFail         bool
		orderDelay        time.Duration
		wantErr           bool
		wantErrIs         error
		wantContain       []string
	}{
		{
			name:              "both services succeed",
			aggregatorTimeout: 3 * time.Second,
			profileDelay:      50 * time.Millisecond,
			orderDelay:        50 * time.Millisecond,
			wantContain:       []string{"Name: Alice", "Orders: 5"},
		},
		{
			name:              "profile service fails",
			aggregatorTimeout: 3 * time.Second,
			profileFail:       true,
			profileDelay:      50 * time.Millisecond,
			orderDelay:        50 * time.Millisecond,
			wantErr:           true,
		},
		{
			name:              "order service fails",
			aggregatorTimeout: 3 * time.Second,
			profileDelay:      50 * time.Millisecond,
			orderFail:         true,
			orderDelay:        50 * time.Millisecond,
			wantErr:           true,
		},
		{
			name:              "aggregator timeout fires — services too slow",
			aggregatorTimeout: 100 * time.Millisecond,
			profileDelay:      500 * time.Millisecond,
			orderDelay:        500 * time.Millisecond,
			wantErr:           true,
			wantErrIs:         context.DeadlineExceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ua, err := NewUserAggregator(
				NewWithTimeout(tt.aggregatorTimeout),
				NewWithServices([]Service{
					NewProfileService(tt.profileDelay, tt.profileFail),
					NewOrderService(tt.orderDelay, tt.orderFail),
				}),
			)
			if err != nil {
				t.Fatalf("constructor error: %v", err)
			}

			result, err := ua.Aggregate(context.Background(), "user-1")

			if (err != nil) != tt.wantErr {
				t.Fatalf("Aggregate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
				t.Errorf("Aggregate() error = %v, want errors.Is(%v)", err, tt.wantErrIs)
			}
			for _, want := range tt.wantContain {
				if !strings.Contains(result, want) {
					t.Errorf("result %q does not contain %q", result, want)
				}
			}
		})
	}
}
