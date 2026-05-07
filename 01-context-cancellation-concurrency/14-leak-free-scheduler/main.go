package main

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"time"
)

const (
	defaultInterval      = 5 * time.Second
	defaultPercentJitter = 0.10
)

type Option func(*scheduler)

type Scheduler interface {
	Run(context.Context, func(context.Context) error) error
}

type scheduler struct {
	log           *slog.Logger
	interval      time.Duration
	percentJitter float64
}

func NewScheduler(options ...Option) Scheduler {
	schedule := &scheduler{
		log:           slog.Default(),
		interval:      defaultInterval,
		percentJitter: defaultPercentJitter,
	}

	for _, opt := range options {
		opt(schedule)
	}

	return schedule
}

func WithLogger(logger *slog.Logger) Option {
	return func(s *scheduler) {
		s.log = logger
	}
}

func WithInterval(interval time.Duration, jitter float64) Option {
	return func(s *scheduler) {
		s.interval = interval
		s.percentJitter = jitter
	}
}

func (s *scheduler) Run(ctx context.Context, job func(context.Context) error) error {
	if err := s.validate(job); err != nil {
		return err
	}

	timer := time.NewTimer(s.nextDelay())
	defer stopTimer(timer)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			start := time.Now()
			err := job(ctx)
			duration := time.Since(start)

			if err != nil {
				s.logger().ErrorContext(ctx, "scheduler job failed",
					slog.Duration("duration", duration),
					slog.Any("error", err),
				)
			} else {
				s.logger().InfoContext(ctx, "scheduler job completed",
					slog.Duration("duration", duration),
				)
			}

			if ctx.Err() != nil {
				return ctx.Err()
			}

			timer.Reset(s.nextDelay())
		}
	}
}

func (s *scheduler) validate(job func(context.Context) error) error {
	if job == nil {
		return errors.New("scheduler job must not be nil")
	}
	if s.interval <= 0 {
		return errors.New("scheduler interval must be positive")
	}
	if s.percentJitter < 0 || s.percentJitter > 1 {
		return errors.New("scheduler jitter must be between 0 and 1")
	}
	return nil
}

func (s *scheduler) nextDelay() time.Duration {
	if s.percentJitter == 0 {
		return s.interval
	}

	maxJitter := float64(s.interval) * s.percentJitter
	jitter := (rand.Float64()*2 - 1) * maxJitter
	delay := s.interval + time.Duration(jitter)

	if delay < time.Nanosecond {
		return time.Nanosecond
	}
	return delay
}

func (s *scheduler) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func job(ctx context.Context) error {
	timer := time.NewTimer(time.Second)
	defer stopTimer(timer)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
