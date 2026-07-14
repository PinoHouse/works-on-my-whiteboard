package distributedratelimiter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/labs/harness"
	tokenbucket "github.com/PinoHouse/works-on-my-whiteboard/labs/primitives/token-bucket"
)

var ErrCoordinatorUnavailable = errors.New("coordinator unavailable")

type Request struct {
	TenantID string
	NodeID   string
	Amount   uint64
}

type Decision struct {
	Allowed    bool
	Degraded   bool
	Reason     string
	Remaining  uint64
	RetryAfter time.Duration
}

type Limiter interface {
	Allow(context.Context, Request) (Decision, error)
}

type Coordinator interface {
	Take(context.Context, string, uint64) (tokenbucket.Decision, error)
}

type OutagePolicy uint8

const (
	PolicyFailClosed OutagePolicy = iota
	PolicyFailOpen
)

type SharedCoordinator struct {
	clock   harness.Clock
	config  tokenbucket.Config
	mu      sync.Mutex
	buckets map[string]*tokenbucket.Bucket
}

func NewSharedCoordinator(clock harness.Clock, config tokenbucket.Config) (*SharedCoordinator, error) {
	if clock == nil {
		return nil, errors.New("shared coordinator clock is nil")
	}
	if err := validateTokenBucketConfig(config); err != nil {
		return nil, fmt.Errorf("shared coordinator config: %w", err)
	}
	return &SharedCoordinator{
		clock:   clock,
		config:  config,
		buckets: make(map[string]*tokenbucket.Bucket),
	}, nil
}

func (c *SharedCoordinator) Take(ctx context.Context, tenantID string, amount uint64) (tokenbucket.Decision, error) {
	if c == nil {
		return tokenbucket.Decision{}, errors.New("shared coordinator is nil")
	}
	if err := validateTakeInput(ctx, tenantID, amount, c.config.Capacity); err != nil {
		return tokenbucket.Decision{}, err
	}
	if err := ctx.Err(); err != nil {
		return tokenbucket.Decision{}, fmt.Errorf("shared coordinator context unavailable: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return tokenbucket.Decision{}, fmt.Errorf("shared coordinator context unavailable before mutation: %w", err)
	}
	now := c.clock.Now()
	if err := ctx.Err(); err != nil {
		return tokenbucket.Decision{}, fmt.Errorf("shared coordinator context unavailable before mutation: %w", err)
	}
	bucket := c.buckets[tenantID]
	if bucket == nil {
		var err error
		bucket, err = tokenbucket.New(c.config, now)
		if err != nil {
			return tokenbucket.Decision{}, fmt.Errorf("create shared tenant bucket: %w", err)
		}
		c.buckets[tenantID] = bucket
	}
	decision, err := bucket.Take(now, amount)
	if err != nil {
		return tokenbucket.Decision{}, fmt.Errorf("take shared tenant token: %w", err)
	}
	return decision, nil
}

type AvailabilityCoordinator struct {
	delegate  *SharedCoordinator
	config    tokenbucket.Config
	mu        sync.RWMutex
	available bool
}

func NewAvailabilityCoordinator(delegate *SharedCoordinator) (*AvailabilityCoordinator, error) {
	if delegate == nil {
		return nil, errors.New("availability coordinator delegate is nil")
	}
	if delegate.clock == nil || delegate.buckets == nil {
		return nil, errors.New("availability coordinator delegate is not initialized")
	}
	return &AvailabilityCoordinator{delegate: delegate, config: delegate.config, available: true}, nil
}

func (c *AvailabilityCoordinator) SetAvailable(available bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.available = available
	c.mu.Unlock()
}

func (c *AvailabilityCoordinator) Take(ctx context.Context, tenantID string, amount uint64) (tokenbucket.Decision, error) {
	if c == nil {
		return tokenbucket.Decision{}, errors.New("availability coordinator is nil")
	}
	if err := validateTakeInput(ctx, tenantID, amount, c.config.Capacity); err != nil {
		return tokenbucket.Decision{}, err
	}
	if err := ctx.Err(); err != nil {
		return tokenbucket.Decision{}, fmt.Errorf("availability coordinator context unavailable: %w", err)
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return tokenbucket.Decision{}, fmt.Errorf("availability coordinator context unavailable before delegation: %w", err)
	}
	if !c.available {
		return tokenbucket.Decision{}, ErrCoordinatorUnavailable
	}
	return c.delegate.Take(ctx, tenantID, amount)
}

type coordinatedLimiter struct {
	coordinator Coordinator
	policy      OutagePolicy
}

func NewCoordinatedLimiter(coordinator Coordinator, policy OutagePolicy) (Limiter, error) {
	if coordinator == nil {
		return nil, errors.New("coordinated limiter coordinator is nil")
	}
	if policy != PolicyFailClosed && policy != PolicyFailOpen {
		return nil, fmt.Errorf("unknown outage policy %d", policy)
	}
	return &coordinatedLimiter{coordinator: coordinator, policy: policy}, nil
}

func (l *coordinatedLimiter) Allow(ctx context.Context, request Request) (Decision, error) {
	if l == nil {
		return Decision{}, errors.New("coordinated limiter is nil")
	}
	if err := validateRequest(ctx, request, 0); err != nil {
		return Decision{}, err
	}
	if err := ctx.Err(); err != nil {
		return Decision{}, fmt.Errorf("coordinated limiter context unavailable: %w", err)
	}
	decision, err := l.coordinator.Take(ctx, request.TenantID, request.Amount)
	if contextErr := ctx.Err(); contextErr != nil {
		return Decision{}, fmt.Errorf("coordinated limiter context unavailable after coordinator take: %w", contextErr)
	}
	if err == nil {
		return Decision{
			Allowed:    decision.Allowed,
			Remaining:  decision.Remaining,
			RetryAfter: decision.RetryAfter,
		}, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Decision{}, fmt.Errorf("coordinator take context failure: %w", err)
	}
	if !isExclusiveCoordinatorUnavailable(err) {
		return Decision{}, fmt.Errorf("coordinator take failed: %w", err)
	}
	if l.policy == PolicyFailOpen {
		return Decision{
			Allowed:  true,
			Degraded: true,
			Reason:   "coordinator-unavailable-fail-open",
		}, nil
	}
	return Decision{
		Degraded: true,
		Reason:   "coordinator-unavailable-fail-closed",
	}, nil
}

func isExclusiveCoordinatorUnavailable(err error) bool {
	const maximumLinearDepth = 64
	current := err
	for depth := 0; depth < maximumLinearDepth && current != nil; depth++ {
		if current == ErrCoordinatorUnavailable {
			return true
		}
		if _, composite := current.(interface{ Unwrap() []error }); composite {
			return false
		}
		wrapper, wrapped := current.(interface{ Unwrap() error })
		if !wrapped {
			return false
		}
		current = wrapper.Unwrap()
	}
	return false
}

type tenantNodeKey struct {
	tenantID string
	nodeID   string
}

type PerNodeLimiter struct {
	clock   harness.Clock
	config  tokenbucket.Config
	mu      sync.Mutex
	buckets map[tenantNodeKey]*tokenbucket.Bucket
}

func NewPerNodeLimiter(clock harness.Clock, config tokenbucket.Config) (*PerNodeLimiter, error) {
	if clock == nil {
		return nil, errors.New("per-node limiter clock is nil")
	}
	if err := validateTokenBucketConfig(config); err != nil {
		return nil, fmt.Errorf("per-node limiter config: %w", err)
	}
	return &PerNodeLimiter{
		clock:   clock,
		config:  config,
		buckets: make(map[tenantNodeKey]*tokenbucket.Bucket),
	}, nil
}

func (l *PerNodeLimiter) Allow(ctx context.Context, request Request) (Decision, error) {
	if l == nil {
		return Decision{}, errors.New("per-node limiter is nil")
	}
	if err := validateRequest(ctx, request, l.config.Capacity); err != nil {
		return Decision{}, err
	}
	if err := ctx.Err(); err != nil {
		return Decision{}, fmt.Errorf("per-node limiter context unavailable: %w", err)
	}

	key := tenantNodeKey{tenantID: request.TenantID, nodeID: request.NodeID}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Decision{}, fmt.Errorf("per-node limiter context unavailable before mutation: %w", err)
	}
	now := l.clock.Now()
	if err := ctx.Err(); err != nil {
		return Decision{}, fmt.Errorf("per-node limiter context unavailable before mutation: %w", err)
	}
	bucket := l.buckets[key]
	if bucket == nil {
		var err error
		bucket, err = tokenbucket.New(l.config, now)
		if err != nil {
			return Decision{}, fmt.Errorf("create per-node bucket: %w", err)
		}
		l.buckets[key] = bucket
	}
	decision, err := bucket.Take(now, request.Amount)
	if err != nil {
		return Decision{}, fmt.Errorf("take per-node token: %w", err)
	}
	return Decision{
		Allowed:    decision.Allowed,
		Remaining:  decision.Remaining,
		RetryAfter: decision.RetryAfter,
	}, nil
}

func validateTokenBucketConfig(config tokenbucket.Config) error {
	_, err := tokenbucket.New(config, time.Unix(0, 0).UTC())
	return err
}

func validateRequest(ctx context.Context, request Request, capacity uint64) error {
	if ctx == nil {
		return errors.New("limiter context is nil")
	}
	if strings.TrimSpace(request.TenantID) == "" {
		return errors.New("tenant ID must be nonblank")
	}
	if strings.TrimSpace(request.NodeID) == "" {
		return errors.New("node ID must be nonblank")
	}
	if request.Amount == 0 {
		return tokenbucket.ErrInvalidAmount
	}
	if capacity != 0 && request.Amount > capacity {
		return tokenbucket.ErrAmountExceedsCapacity
	}
	return nil
}

func validateTakeInput(ctx context.Context, tenantID string, amount, capacity uint64) error {
	if ctx == nil {
		return errors.New("coordinator context is nil")
	}
	if strings.TrimSpace(tenantID) == "" {
		return errors.New("tenant ID must be nonblank")
	}
	if amount == 0 {
		return tokenbucket.ErrInvalidAmount
	}
	if capacity != 0 && amount > capacity {
		return tokenbucket.ErrAmountExceedsCapacity
	}
	return nil
}
