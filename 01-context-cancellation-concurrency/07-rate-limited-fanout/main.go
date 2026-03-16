package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
	"golang.org/x/time/rate"
)

type Option func(fc *fanOutClient)

func WithSem(sem *semaphore.Weighted) Option {
	return func(fc *fanOutClient) {
		fc.sem = sem
	}
}

func WithLimiter(limiter *rate.Limiter) Option {
	return func(fc *fanOutClient) {
		fc.limiter = limiter
	}
}

func WithLogger(logger *slog.Logger) Option {
	return func(fc *fanOutClient) {
		fc.logger = logger
	}
}

func WithTransport(transport *http.Transport) Option {
	return func(fc *fanOutClient) {
		fc.client.Transport = transport
	}
}

func WithTimeout(d time.Duration) Option {
	return func(fc *fanOutClient) {
		fc.client.Timeout = d
	}
}

type FanOutClient interface {
	FetchAll(ctx context.Context, userIDs []int) (map[int][]byte, error)
}

type fanOutClient struct {
	logger  *slog.Logger
	client  *http.Client
	limiter *rate.Limiter
	sem     *semaphore.Weighted
}

func NewFanOutClient(opts ...Option) (FanOutClient, error) {
	// Default
	fc := &fanOutClient{
		client: &http.Client{
			Transport: http.DefaultTransport,
		},
	}

	for _, opt := range opts {
		opt(fc)
	}

	if fc.limiter == nil {
		return nil, errors.New("limiter is required")
	}
	if fc.sem == nil {
		return nil, errors.New("semaphore is required")
	}
	if fc.logger == nil {
		fc.logger = slog.Default() // fallback
	}

	return fc, nil
}

func (fc *fanOutClient) FetchAll(ctx context.Context, userIDs []int) (map[int][]byte, error) {
	res := make(map[int][]byte)

	lock := sync.Mutex{}
	eg, ctx := errgroup.WithContext(ctx)

	for _, userID := range userIDs {
		if err := fc.limiter.Wait(ctx); err != nil {
			fc.logger.Error("Rate limit wait failed", slog.Int("userID", userID), slog.String("error", err.Error()))
			return nil, err
		}

		eg.Go(func() error {
			if err := fc.sem.Acquire(ctx, 1); err != nil {
				fc.logger.Error("Semaphore acquire failed", slog.Int("userID", userID), slog.String("error", err.Error()))
				return err
			}
			defer fc.sem.Release(1)

			// Create request
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("test/%d", userID), nil)
			if err != nil {
				fc.logger.Error("Error to create request", slog.Int("userID", userID), slog.String("error", err.Error()))
				return err
			}

			// Do request
			resp, err := fc.client.Do(req)
			if err != nil {
				fc.logger.Error("Error to fetch data", slog.Int("userID", userID), slog.String("error", err.Error()))
				return err
			}
			defer resp.Body.Close()

			// Check status code
			if resp.StatusCode >= 400 {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("userID %d: HTTP %d: %s", userID, resp.StatusCode, body)
			}

			// Get body
			body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // max 1MB
			if err != nil {
				fc.logger.Error("Error to read data", slog.Int("userID", userID), slog.String("error", err.Error()))
				return err
			}
			lock.Lock()
			res[userID] = body
			lock.Unlock()

			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		fc.logger.Error("Fetch All failed", slog.String("error", err.Error()))
		return nil, err
	}

	return res, nil
}
