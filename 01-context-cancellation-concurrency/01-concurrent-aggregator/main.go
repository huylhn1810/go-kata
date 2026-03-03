package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

type Option func(*UserAggregator) error

func NewWithTimeout(timeout time.Duration) Option {
	return func(ua *UserAggregator) error {
		if timeout < 0 {
			return fmt.Errorf("invalid timeout")
		}

		ua.timeout = timeout

		return nil
	}
}

func NewWithLogger(logger *slog.Logger) Option {
	return func(ua *UserAggregator) error {
		ua.logger = logger

		return nil
	}
}

func NewWithServices(services []Service) Option {
	return func(ua *UserAggregator) error {
		ua.services = services

		return nil
	}
}

type UserAggregator struct {
	services []Service
	logger   *slog.Logger
	timeout  time.Duration
}

func NewUserAggregator(opts ...Option) (*UserAggregator, error) {
	// Default
	us := &UserAggregator{
		services: nil,
		logger:   slog.Default(),
		timeout:  3 * time.Second,
	}

	// Apply Options
	for _, opt := range opts {
		err := opt(us)

		if err != nil {
			return nil, fmt.Errorf("failed to configure UserAggregator: %w", err)
		}
	}

	return us, nil
}

func (ua *UserAggregator) Aggregate(ctx context.Context, id string) (string, error) {
	if len(ua.services) == 0 {
		return "", fmt.Errorf("no services configured")
	}

	var res []string
	ctx, cancelFunc := context.WithTimeout(ctx, ua.timeout)
	defer cancelFunc()

	eg, ctx := errgroup.WithContext(ctx)
	resultChan := make(chan string, len(ua.services))

	for _, ser := range ua.services {
		ser := ser

		eg.Go(func() error {
			data, err := ser.FetchData(ctx, id)
			if err != nil {
				ua.logger.Error("Service fetch failed", slog.String("user", id), slog.String("service", fmt.Sprintf("%T", ser)))
				return err
			}

			resultChan <- data

			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		ua.logger.Error("Aggregator failed")
		return "", err
	}

	close(resultChan)
	for data := range resultChan {
		res = append(res, data)
	}

	return strings.Join(res, " | "), nil
}

type Service interface {
	FetchData(context.Context, string) (string, error)
}

type ProfileService struct {
	timeout    time.Duration
	shouldFail bool
}

func NewProfileService(timeout time.Duration, shouldFail bool) Service {
	return &ProfileService{
		timeout:    timeout,
		shouldFail: shouldFail,
	}
}

func (ps *ProfileService) FetchData(ctx context.Context, id string) (string, error) {
	timer := time.NewTimer(ps.timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		if ps.shouldFail {
			return "", fmt.Errorf("Error to fetch profile")
		}
		return "Name: Alice", nil
	}
}

type OrderService struct {
	timeout    time.Duration
	shouldFail bool
}

func NewOrderService(timeout time.Duration, shouldFail bool) Service {
	return &OrderService{
		timeout:    timeout,
		shouldFail: shouldFail,
	}
}

func (os *OrderService) FetchData(ctx context.Context, id string) (string, error) {
	timer := time.NewTimer(os.timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		if os.shouldFail {
			return "", fmt.Errorf("Error to fetch user")
		}
		return "Orders: 5", nil
	}
}
