package main

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	shortInterval = 5 * time.Millisecond
	stopTimeout   = 250 * time.Millisecond
)

// ---------------------------------------------------------------------------
// Helper: build a Scheduler with deterministic timing for tests.
// Always disables jitter so timing assertions are stable.
// ---------------------------------------------------------------------------
func setupScheduler(t *testing.T, interval time.Duration) Scheduler {
	t.Helper()

	return NewScheduler(WithInterval(interval, 0))
}

// ---------------------------------------------------------------------------
// Test 1 - Self-Correction: If job overlap occurs, you failed.
// ---------------------------------------------------------------------------
func TestSchedulerNoJobOverlap(t *testing.T) {
	t.Run("job must not overlap with itself", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		scheduler := setupScheduler(t, shortInterval)
		done := make(chan error, 1)

		var activeJobs int64
		var completedJobs int64
		var overlapped atomic.Bool

		go func() {
			done <- scheduler.Run(ctx, func(context.Context) error {
				if running := atomic.AddInt64(&activeJobs, 1); running > 1 {
					overlapped.Store(true)
				}
				defer atomic.AddInt64(&activeJobs, -1)

				time.Sleep(35 * time.Millisecond)
				if atomic.AddInt64(&completedJobs, 1) == 4 {
					cancel()
				}

				return nil
			})
		}()

		select {
		case <-done:
		case <-time.After(750 * time.Millisecond):
			t.Fatal("scheduler did not stop after the test cancelled its context")
		}

		if got := atomic.LoadInt64(&completedJobs); got < 4 {
			t.Fatalf("completed jobs = %d, want at least 4", got)
		}
		if overlapped.Load() {
			t.Fatal("job overlapped with itself")
		}
	})
}

// ---------------------------------------------------------------------------
// Test 2 - Self-Correction: If cancel doesn't stop quickly, you failed.
// ---------------------------------------------------------------------------
func TestSchedulerCancelStopsQuickly(t *testing.T) {
	t.Run("cancellation must stop the running job and scheduler quickly", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		scheduler := setupScheduler(t, shortInterval)
		started := make(chan struct{})
		jobReturned := make(chan struct{})
		done := make(chan error, 1)

		var closeStarted sync.Once
		var closeReturned sync.Once

		go func() {
			done <- scheduler.Run(ctx, func(jobCtx context.Context) error {
				closeStarted.Do(func() { close(started) })

				<-jobCtx.Done()
				closeReturned.Do(func() { close(jobReturned) })
				return jobCtx.Err()
			})
		}()

		select {
		case <-started:
		case <-time.After(stopTimeout):
			t.Fatal("job did not start")
		}

		cancelStart := time.Now()
		cancel()

		select {
		case <-jobReturned:
		case <-time.After(150 * time.Millisecond):
			t.Fatal("job did not observe cancellation through its context")
		}

		select {
		case <-done:
		case <-time.After(150 * time.Millisecond):
			t.Fatal("scheduler did not stop quickly after cancellation")
		}

		if elapsed := time.Since(cancelStart); elapsed > 150*time.Millisecond {
			t.Fatalf("scheduler stopped in %v, want <= 150ms", elapsed)
		}
	})
}

// ---------------------------------------------------------------------------
// Test 3 - Self-Correction: If goroutines remain after exit, you failed.
// ---------------------------------------------------------------------------
func TestSchedulerNoGoroutineLeakAfterExit(t *testing.T) {
	t.Run("scheduler goroutines must exit after cancellation", func(t *testing.T) {
		before := runtime.NumGoroutine()

		for i := 0; i < 25; i++ {
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)

			go func() {
				scheduler := setupScheduler(t, time.Hour)
				done <- scheduler.Run(ctx, func(context.Context) error {
					return nil
				})
			}()

			cancel()

			select {
			case <-done:
			case <-time.After(stopTimeout):
				t.Fatalf("scheduler run %d did not exit after cancellation", i+1)
			}
		}

		deadline := time.Now().Add(300 * time.Millisecond)
		for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}

		if after := runtime.NumGoroutine(); after > before+2 {
			t.Fatalf("goroutines before = %d, after = %d; possible scheduler leak", before, after)
		}
	})
}
