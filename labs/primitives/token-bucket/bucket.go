package tokenbucket

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

var (
	ErrInvalidCapacity       = errors.New("token bucket capacity must be positive")
	ErrInvalidRefillEvery    = errors.New("token bucket refill interval must be positive")
	ErrInvalidAmount         = errors.New("token amount must be positive")
	ErrAmountExceedsCapacity = errors.New("token amount exceeds bucket capacity")
	ErrTimeRollback          = errors.New("token bucket time moved backward")
	ErrTimeOverflow          = errors.New("token bucket elapsed time exceeds time.Duration")
)

type Config struct {
	Capacity    uint64
	RefillEvery time.Duration
}

type Decision struct {
	Allowed    bool
	Remaining  uint64
	RetryAfter time.Duration
}

type Bucket struct {
	mu           sync.Mutex
	capacity     uint64
	available    uint64
	refillEvery  time.Duration
	refillAnchor time.Time
	lastObserved time.Time
}

func New(config Config, now time.Time) (*Bucket, error) {
	if config.Capacity == 0 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidCapacity, config.Capacity)
	}
	if config.RefillEvery <= 0 {
		return nil, fmt.Errorf("%w: got %s", ErrInvalidRefillEvery, config.RefillEvery)
	}
	now = now.UTC()
	return &Bucket{
		capacity:     config.Capacity,
		available:    config.Capacity,
		refillEvery:  config.RefillEvery,
		refillAnchor: now,
		lastObserved: now,
	}, nil
}

func (b *Bucket) Take(now time.Time, amount uint64) (Decision, error) {
	if amount == 0 {
		return Decision{}, ErrInvalidAmount
	}
	if b == nil {
		return Decision{}, errors.New("token bucket is nil")
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if amount > b.capacity {
		return Decision{}, ErrAmountExceedsCapacity
	}
	remainder, err := b.observeLocked(now.UTC())
	if err != nil {
		return Decision{}, err
	}
	if b.available >= amount {
		b.available -= amount
		return Decision{Allowed: true, Remaining: b.available}, nil
	}
	return Decision{
		Allowed:    false,
		Remaining:  b.available,
		RetryAfter: retryAfter(b.refillEvery, remainder, amount-b.available),
	}, nil
}

func (b *Bucket) Available(now time.Time) (uint64, error) {
	if b == nil {
		return 0, errors.New("token bucket is nil")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.observeLocked(now.UTC()); err != nil {
		return 0, err
	}
	return b.available, nil
}

func (b *Bucket) observeLocked(now time.Time) (time.Duration, error) {
	if now.Before(b.lastObserved) {
		return 0, ErrTimeRollback
	}
	elapsed := now.Sub(b.refillAnchor)
	if elapsed == time.Duration(math.MaxInt64) && !b.refillAnchor.Add(elapsed).Equal(now) {
		return 0, ErrTimeOverflow
	}
	remainder := elapsed % b.refillEvery
	whole := uint64(elapsed / b.refillEvery)
	if whole >= b.capacity-b.available {
		b.available = b.capacity
	} else {
		b.available += whole
	}
	b.refillAnchor = b.refillAnchor.Add(elapsed - remainder)
	b.lastObserved = now
	return remainder, nil
}

func retryAfter(refillEvery, remainder time.Duration, need uint64) time.Duration {
	first := refillEvery - remainder
	if need <= 1 {
		return first
	}
	maxDuration := time.Duration(math.MaxInt64)
	extraIntervals := need - 1
	maxExtraIntervals := uint64((maxDuration - first) / refillEvery)
	if extraIntervals > maxExtraIntervals {
		return maxDuration
	}
	return first + time.Duration(extraIntervals)*refillEvery
}
