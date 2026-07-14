package distributedratelimiter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/labs/primitives/token-bucket"
)

type countingClock struct {
	mu    sync.Mutex
	now   time.Time
	calls int
}

func (c *countingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.now
}

func (c *countingClock) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *countingClock) setNow(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

type coordinatorStub struct {
	mu       sync.Mutex
	decision tokenbucket.Decision
	err      error
	calls    int
}

type cancelingClock struct {
	now    time.Time
	cancel context.CancelFunc
	calls  atomic.Int64
}

type controlledIncreasingClock struct {
	base         time.Time
	firstEntered chan struct{}
	releaseFirst chan struct{}
	calls        atomic.Int64
}

func newControlledIncreasingClock() *controlledIncreasingClock {
	return &controlledIncreasingClock{
		base:         time.Unix(0, 0).UTC(),
		firstEntered: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
}

func (c *controlledIncreasingClock) Now() time.Time {
	switch call := c.calls.Add(1); call {
	case 1:
		close(c.firstEntered)
		<-c.releaseFirst
		return c.base
	case 2:
		return c.base.Add(time.Nanosecond)
	default:
		panic(fmt.Sprintf("unexpected clock call %d", call))
	}
}

func (c *cancelingClock) Now() time.Time {
	c.calls.Add(1)
	c.cancel()
	return c.now
}

func (s *coordinatorStub) Take(context.Context, string, uint64) (tokenbucket.Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.decision, s.err
}

func (s *coordinatorStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestConstructorsRejectInvalidDependenciesAndConfiguration(t *testing.T) {
	config := tokenbucket.Config{Capacity: 2, RefillEvery: time.Second}
	if _, err := NewSharedCoordinator(nil, config); err == nil {
		t.Fatal("NewSharedCoordinator(nil) error = nil")
	}
	if _, err := NewPerNodeLimiter(nil, config); err == nil {
		t.Fatal("NewPerNodeLimiter(nil) error = nil")
	}
	clock := &countingClock{now: time.Unix(0, 0).UTC()}
	if _, err := NewSharedCoordinator(clock, tokenbucket.Config{}); !errors.Is(err, tokenbucket.ErrInvalidCapacity) {
		t.Fatalf("NewSharedCoordinator(invalid config) error = %v", err)
	}
	if _, err := NewPerNodeLimiter(clock, tokenbucket.Config{}); !errors.Is(err, tokenbucket.ErrInvalidCapacity) {
		t.Fatalf("NewPerNodeLimiter(invalid config) error = %v", err)
	}
	if _, err := NewCoordinatedLimiter(nil, PolicyFailClosed); err == nil {
		t.Fatal("NewCoordinatedLimiter(nil) error = nil")
	}
	if _, err := NewCoordinatedLimiter(&coordinatorStub{}, OutagePolicy(99)); err == nil {
		t.Fatal("NewCoordinatedLimiter(invalid policy) error = nil")
	}
	if _, err := NewAvailabilityCoordinator(nil, config); err == nil {
		t.Fatal("NewAvailabilityCoordinator(nil) error = nil")
	}
	if _, err := NewAvailabilityCoordinator(&coordinatorStub{}, tokenbucket.Config{}); !errors.Is(err, tokenbucket.ErrInvalidCapacity) {
		t.Fatalf("NewAvailabilityCoordinator(invalid config) error = %v", err)
	}
}

func TestLimitersRejectInvalidAndCanceledRequestsBeforeClockOrCoordinatorUse(t *testing.T) {
	tests := []Request{
		{},
		{TenantID: " ", NodeID: "node-a", Amount: 1},
		{TenantID: "tenant", NodeID: "\t", Amount: 1},
		{TenantID: "tenant", NodeID: "node-a", Amount: 0},
	}
	for _, kind := range []string{"shared", "per-node"} {
		t.Run(kind, func(t *testing.T) {
			clock := &countingClock{now: time.Unix(0, 0).UTC()}
			config := tokenbucket.Config{Capacity: 2, RefillEvery: time.Second}
			var limiter Limiter
			var coordinator *coordinatorStub
			if kind == "per-node" {
				var err error
				limiter, err = NewPerNodeLimiter(clock, config)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				coordinator = &coordinatorStub{}
				var err error
				limiter, err = NewCoordinatedLimiter(coordinator, PolicyFailClosed)
				if err != nil {
					t.Fatal(err)
				}
			}

			for _, request := range tests {
				decision, err := limiter.Allow(context.Background(), request)
				if err == nil || decision != (Decision{}) {
					t.Fatalf("Allow(%#v) = %#v, %v; want zero decision and error", request, decision, err)
				}
			}
			if kind == "per-node" {
				request := Request{TenantID: "tenant", NodeID: "node-a", Amount: 3}
				decision, err := limiter.Allow(context.Background(), request)
				if !errors.Is(err, tokenbucket.ErrAmountExceedsCapacity) || decision != (Decision{}) {
					t.Fatalf("Allow(%#v) = %#v, %v", request, decision, err)
				}
			}
			decision, err := limiter.Allow(nil, Request{TenantID: "tenant", NodeID: "node-a", Amount: 1})
			if err == nil || decision != (Decision{}) {
				t.Fatalf("Allow(nil) = %#v, %v", decision, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			decision, err = limiter.Allow(ctx, Request{TenantID: "tenant", NodeID: "node-a", Amount: 1})
			if !errors.Is(err, context.Canceled) || decision != (Decision{}) {
				t.Fatalf("Allow(canceled) = %#v, %v", decision, err)
			}
			if kind == "per-node" && clock.callCount() != 0 {
				t.Fatalf("clock calls = %d, want 0", clock.callCount())
			}
			if coordinator != nil && coordinator.callCount() != 0 {
				t.Fatalf("coordinator calls = %d, want 0", coordinator.callCount())
			}
		})
	}
}

func TestSharedCoordinatorValidatesBeforeReadingClock(t *testing.T) {
	clock := &countingClock{now: time.Unix(0, 0).UTC()}
	coordinator, err := NewSharedCoordinator(clock, tokenbucket.Config{Capacity: 2, RefillEvery: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		ctx    context.Context
		tenant string
		amount uint64
	}{
		{ctx: nil, tenant: "tenant", amount: 1},
		{ctx: context.Background(), tenant: " ", amount: 1},
		{ctx: context.Background(), tenant: "tenant", amount: 0},
		{ctx: context.Background(), tenant: "tenant", amount: 3},
	} {
		decision, takeErr := coordinator.Take(input.ctx, input.tenant, input.amount)
		if takeErr == nil || decision != (tokenbucket.Decision{}) {
			t.Fatalf("Take(%q,%d) = %#v, %v", input.tenant, input.amount, decision, takeErr)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	decision, err := coordinator.Take(ctx, "tenant", 1)
	if !errors.Is(err, context.Canceled) || decision != (tokenbucket.Decision{}) {
		t.Fatalf("Take(canceled) = %#v, %v", decision, err)
	}
	if clock.callCount() != 0 {
		t.Fatalf("clock calls = %d, want 0", clock.callCount())
	}
}

func TestCancellationTriggeredByClockPreventsBucketMutation(t *testing.T) {
	config := tokenbucket.Config{Capacity: 2, RefillEvery: time.Second}
	t.Run("shared", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		clock := &cancelingClock{now: time.Unix(0, 0).UTC(), cancel: cancel}
		coordinator, err := NewSharedCoordinator(clock, config)
		if err != nil {
			t.Fatal(err)
		}
		decision, err := coordinator.Take(ctx, "tenant", 1)
		if !errors.Is(err, context.Canceled) || decision != (tokenbucket.Decision{}) {
			t.Fatalf("Take() = %#v, %v", decision, err)
		}
		if clock.calls.Load() != 1 || len(coordinator.buckets) != 0 {
			t.Fatalf("clock calls/buckets = %d/%d, want 1/0", clock.calls.Load(), len(coordinator.buckets))
		}
	})
	t.Run("per-node", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		clock := &cancelingClock{now: time.Unix(0, 0).UTC(), cancel: cancel}
		limiter, err := NewPerNodeLimiter(clock, config)
		if err != nil {
			t.Fatal(err)
		}
		decision, err := limiter.Allow(ctx, Request{TenantID: "tenant", NodeID: "node", Amount: 1})
		if !errors.Is(err, context.Canceled) || decision != (Decision{}) {
			t.Fatalf("Allow() = %#v, %v", decision, err)
		}
		if clock.calls.Load() != 1 || len(limiter.buckets) != 0 {
			t.Fatalf("clock calls/buckets = %d/%d, want 1/0", clock.calls.Load(), len(limiter.buckets))
		}
	})
}

func TestSharedAndPerNodeLimitersFreezeExactBudgetSemantics(t *testing.T) {
	config := tokenbucket.Config{Capacity: 4, RefillEvery: 10 * time.Second}
	sharedClock := &countingClock{now: time.Unix(0, 0).UTC()}
	coordinator, err := NewSharedCoordinator(sharedClock, config)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := NewCoordinatedLimiter(coordinator, PolicyFailClosed)
	if err != nil {
		t.Fatal(err)
	}
	perNodeClock := &countingClock{now: time.Unix(0, 0).UTC()}
	perNode, err := NewPerNodeLimiter(perNodeClock, config)
	if err != nil {
		t.Fatal(err)
	}

	sharedAllowed := runAlternatingBurst(t, shared, "tenant", 4)
	perNodeAllowed := runAlternatingBurst(t, perNode, "tenant", 4)
	if sharedAllowed != 4 {
		t.Fatalf("shared allowed = %d, want 4", sharedAllowed)
	}
	if perNodeAllowed != 8 {
		t.Fatalf("per-node allowed = %d, want 8", perNodeAllowed)
	}
	if sharedClock.callCount() != 8 || perNodeClock.callCount() != 8 {
		t.Fatalf("clock calls shared/per-node = %d/%d, want 8/8", sharedClock.callCount(), perNodeClock.callCount())
	}
}

func TestHealthySharedLimiterTranslatesPrimitiveDecisionExactly(t *testing.T) {
	want := tokenbucket.Decision{Allowed: false, Remaining: 7, RetryAfter: 345 * time.Millisecond}
	limiter, err := NewCoordinatedLimiter(&coordinatorStub{decision: want}, PolicyFailOpen)
	if err != nil {
		t.Fatal(err)
	}
	got, err := limiter.Allow(context.Background(), Request{TenantID: "tenant", NodeID: "node", Amount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got != (Decision{Allowed: want.Allowed, Remaining: want.Remaining, RetryAfter: want.RetryAfter}) {
		t.Fatalf("decision = %#v", got)
	}
}

func TestByteExactTenantNodeIsolationAndUnambiguousTupleKeys(t *testing.T) {
	clock := &countingClock{now: time.Unix(0, 0).UTC()}
	config := tokenbucket.Config{Capacity: 1, RefillEvery: time.Hour}
	perNode, err := NewPerNodeLimiter(clock, config)
	if err != nil {
		t.Fatal(err)
	}
	requests := []Request{
		{TenantID: "a", NodeID: "bc", Amount: 1},
		{TenantID: "ab", NodeID: "c", Amount: 1},
		{TenantID: "tenant", NodeID: "node", Amount: 1},
		{TenantID: " tenant", NodeID: "node", Amount: 1},
		{TenantID: "tenant", NodeID: " node", Amount: 1},
	}
	for _, request := range requests {
		decision, allowErr := perNode.Allow(context.Background(), request)
		if allowErr != nil || !decision.Allowed {
			t.Fatalf("first Allow(%#v) = %#v, %v", request, decision, allowErr)
		}
	}
	for _, request := range requests {
		decision, allowErr := perNode.Allow(context.Background(), request)
		if allowErr != nil || decision.Allowed {
			t.Fatalf("second Allow(%#v) = %#v, %v", request, decision, allowErr)
		}
	}

	sharedClock := &countingClock{now: time.Unix(0, 0).UTC()}
	shared, err := NewSharedCoordinator(sharedClock, config)
	if err != nil {
		t.Fatal(err)
	}
	for _, tenant := range []string{"tenant", " tenant", "tenant "} {
		decision, takeErr := shared.Take(context.Background(), tenant, 1)
		if takeErr != nil || !decision.Allowed {
			t.Fatalf("Take(%q) = %#v, %v", tenant, decision, takeErr)
		}
	}
}

func TestOutagePolicyOnlyHandlesUnavailableSentinel(t *testing.T) {
	tests := []struct {
		name   string
		policy OutagePolicy
		want   Decision
	}{
		{
			name:   "fail-closed",
			policy: PolicyFailClosed,
			want:   Decision{Degraded: true, Reason: "coordinator-unavailable-fail-closed"},
		},
		{
			name:   "fail-open",
			policy: PolicyFailOpen,
			want:   Decision{Allowed: true, Degraded: true, Reason: "coordinator-unavailable-fail-open"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &coordinatorStub{err: fmt.Errorf("wrapped: %w", ErrCoordinatorUnavailable)}
			limiter, err := NewCoordinatedLimiter(stub, test.policy)
			if err != nil {
				t.Fatal(err)
			}
			decision, err := limiter.Allow(context.Background(), Request{TenantID: "tenant", NodeID: "node", Amount: 1})
			if err != nil || decision != test.want {
				t.Fatalf("Allow() = %#v, %v; want %#v, nil", decision, err, test.want)
			}
		})
	}

	for _, coordinatorErr := range []error{errors.New("internal"), context.Canceled, tokenbucket.ErrTimeRollback} {
		stub := &coordinatorStub{err: coordinatorErr}
		limiter, err := NewCoordinatedLimiter(stub, PolicyFailOpen)
		if err != nil {
			t.Fatal(err)
		}
		decision, allowErr := limiter.Allow(context.Background(), Request{TenantID: "tenant", NodeID: "node", Amount: 1})
		if decision != (Decision{}) || !errors.Is(allowErr, coordinatorErr) {
			t.Fatalf("coordinator error %v produced %#v, %v", coordinatorErr, decision, allowErr)
		}
	}
}

func TestActualCoordinatorRollbackBypassesFailOpenAndPreservesBudget(t *testing.T) {
	start := time.Unix(1, 0).UTC()
	clock := &countingClock{now: start}
	coordinator, err := NewSharedCoordinator(clock, tokenbucket.Config{Capacity: 2, RefillEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	limiter, err := NewCoordinatedLimiter(coordinator, PolicyFailOpen)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{TenantID: "tenant", NodeID: "node", Amount: 1}
	first, err := limiter.Allow(context.Background(), request)
	if err != nil || !first.Allowed || first.Remaining != 1 {
		t.Fatalf("first = %#v, %v", first, err)
	}
	clock.setNow(start.Add(-time.Nanosecond))
	rollback, err := limiter.Allow(context.Background(), request)
	if !errors.Is(err, tokenbucket.ErrTimeRollback) || rollback != (Decision{}) {
		t.Fatalf("rollback = %#v, %v", rollback, err)
	}
	clock.setNow(start)
	second, err := limiter.Allow(context.Background(), request)
	if err != nil || !second.Allowed || second.Remaining != 0 {
		t.Fatalf("second = %#v, %v", second, err)
	}
}

func TestUnavailableCoordinatorDoesNotMutateAndRestoreKeepsState(t *testing.T) {
	clock := &countingClock{now: time.Unix(0, 0).UTC()}
	shared, err := NewSharedCoordinator(clock, tokenbucket.Config{Capacity: 2, RefillEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	availability, err := NewAvailabilityCoordinator(shared, tokenbucket.Config{Capacity: 2, RefillEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	limiter, err := NewCoordinatedLimiter(availability, PolicyFailOpen)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{TenantID: "tenant", NodeID: "node", Amount: 1}

	first, err := limiter.Allow(context.Background(), request)
	if err != nil || !first.Allowed || first.Remaining != 1 {
		t.Fatalf("first = %#v, %v", first, err)
	}
	availability.SetAvailable(false)
	for index := 0; index < 4; index++ {
		decision, allowErr := limiter.Allow(context.Background(), request)
		if allowErr != nil || !decision.Allowed || !decision.Degraded {
			t.Fatalf("outage %d = %#v, %v", index, decision, allowErr)
		}
	}
	if clock.callCount() != 1 {
		t.Fatalf("underlying clock calls during outage = %d, want 1", clock.callCount())
	}
	availability.SetAvailable(true)
	second, err := limiter.Allow(context.Background(), request)
	if err != nil || !second.Allowed || second.Remaining != 0 {
		t.Fatalf("second healthy = %#v, %v", second, err)
	}
	third, err := limiter.Allow(context.Background(), request)
	if err != nil || third.Allowed {
		t.Fatalf("third healthy = %#v, %v", third, err)
	}
}

func TestAvailabilityCoordinatorValidatesContextBeforeAvailability(t *testing.T) {
	stub := &coordinatorStub{}
	availability, err := NewAvailabilityCoordinator(stub, tokenbucket.Config{Capacity: 2, RefillEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	availability.SetAvailable(false)
	decision, err := availability.Take(nil, "tenant", 1)
	if err == nil || errors.Is(err, ErrCoordinatorUnavailable) || decision != (tokenbucket.Decision{}) {
		t.Fatalf("Take(nil) = %#v, %v", decision, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	decision, err = availability.Take(ctx, "tenant", 1)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrCoordinatorUnavailable) || decision != (tokenbucket.Decision{}) {
		t.Fatalf("Take(canceled) = %#v, %v", decision, err)
	}
	if stub.callCount() != 0 {
		t.Fatalf("underlying calls = %d", stub.callCount())
	}
}

func TestUnavailableCoordinatorRejectsOversizedAmountWithoutPolicyOrDelegation(t *testing.T) {
	stub := &coordinatorStub{}
	availability, err := NewAvailabilityCoordinator(stub, tokenbucket.Config{Capacity: 2, RefillEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	availability.SetAvailable(false)
	limiter, err := NewCoordinatedLimiter(availability, PolicyFailOpen)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := limiter.Allow(context.Background(), Request{TenantID: "tenant", NodeID: "node", Amount: 3})
	if !errors.Is(err, tokenbucket.ErrAmountExceedsCapacity) || decision != (Decision{}) {
		t.Fatalf("Allow(oversized during outage) = %#v, %v", decision, err)
	}
	if stub.callCount() != 0 {
		t.Fatalf("underlying calls = %d, want 0", stub.callCount())
	}
}

func TestConcurrentBudgetsAreLinearizable(t *testing.T) {
	t.Run("shared", func(t *testing.T) {
		clock := &countingClock{now: time.Unix(0, 0).UTC()}
		coordinator, err := NewSharedCoordinator(clock, tokenbucket.Config{Capacity: 17, RefillEvery: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		limiter, err := NewCoordinatedLimiter(coordinator, PolicyFailClosed)
		if err != nil {
			t.Fatal(err)
		}
		if allowed := concurrentAllows(t, limiter, 128, func(index int) Request {
			return Request{TenantID: "tenant", NodeID: fmt.Sprintf("node-%d", index%4), Amount: 1}
		}); allowed != 17 {
			t.Fatalf("allowed = %d, want 17", allowed)
		}
	})

	t.Run("per-node", func(t *testing.T) {
		clock := &countingClock{now: time.Unix(0, 0).UTC()}
		limiter, err := NewPerNodeLimiter(clock, tokenbucket.Config{Capacity: 7, RefillEvery: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		if allowed := concurrentAllows(t, limiter, 128, func(index int) Request {
			return Request{TenantID: "tenant", NodeID: fmt.Sprintf("node-%d", index%2), Amount: 1}
		}); allowed != 14 {
			t.Fatalf("allowed = %d, want 14", allowed)
		}
	})
}

func TestIncreasingClockOverlapsDoNotCreateFalseRollback(t *testing.T) {
	config := tokenbucket.Config{Capacity: 1, RefillEvery: time.Hour}
	t.Run("shared", func(t *testing.T) {
		clock := newControlledIncreasingClock()
		coordinator, err := NewSharedCoordinator(clock, config)
		if err != nil {
			t.Fatal(err)
		}
		assertIncreasingClockOverlap(t, &coordinator.mu, clock, func() (bool, error) {
			decision, takeErr := coordinator.Take(context.Background(), "tenant", 1)
			return decision.Allowed, takeErr
		})
	})
	t.Run("per-node", func(t *testing.T) {
		clock := newControlledIncreasingClock()
		limiter, err := NewPerNodeLimiter(clock, config)
		if err != nil {
			t.Fatal(err)
		}
		assertIncreasingClockOverlap(t, &limiter.mu, clock, func() (bool, error) {
			decision, allowErr := limiter.Allow(context.Background(), Request{TenantID: "tenant", NodeID: "node", Amount: 1})
			return decision.Allowed, allowErr
		})
	})
}

func TestAvailabilityStateIsRaceSafeDuringDecisions(t *testing.T) {
	config := tokenbucket.Config{Capacity: 17, RefillEvery: time.Hour}
	clock := &countingClock{now: time.Unix(0, 0).UTC()}
	shared, err := NewSharedCoordinator(clock, config)
	if err != nil {
		t.Fatal(err)
	}
	availability, err := NewAvailabilityCoordinator(shared, config)
	if err != nil {
		t.Fatal(err)
	}
	limiter, err := NewCoordinatedLimiter(availability, PolicyFailClosed)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errorsFound := make(chan error, 800)
	var decisions atomic.Int64
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		<-start
		for index := 0; index < 800; index++ {
			availability.SetAvailable(index%2 == 0)
		}
	}()
	for worker := 0; worker < 8; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			for index := 0; index < 100; index++ {
				decision, allowErr := limiter.Allow(context.Background(), Request{TenantID: "tenant", NodeID: "node", Amount: 1})
				if allowErr != nil {
					errorsFound <- allowErr
					continue
				}
				if decision.Degraded && (decision.Allowed || decision.Reason != "coordinator-unavailable-fail-closed" || decision.Remaining != 0 || decision.RetryAfter != 0) {
					errorsFound <- fmt.Errorf("invalid degraded decision %#v", decision)
					continue
				}
				if !decision.Degraded && decision.Reason != "" {
					errorsFound <- fmt.Errorf("invalid healthy decision %#v", decision)
					continue
				}
				decisions.Add(1)
			}
		}()
	}
	close(start)
	group.Wait()
	close(errorsFound)
	for decisionErr := range errorsFound {
		t.Error(decisionErr)
	}
	if decisions.Load() != 800 {
		t.Fatalf("decisions = %d, want 800", decisions.Load())
	}
}

func runAlternatingBurst(t *testing.T, limiter Limiter, tenant string, perNode int) int {
	t.Helper()
	allowed := 0
	for index := 0; index < perNode; index++ {
		for _, node := range []string{"node-a", "node-b"} {
			decision, err := limiter.Allow(context.Background(), Request{TenantID: tenant, NodeID: node, Amount: 1})
			if err != nil {
				t.Fatalf("Allow(%s,%d) error = %v", node, index, err)
			}
			if decision.Degraded || decision.Reason != "" {
				t.Fatalf("healthy decision = %#v", decision)
			}
			if decision.Allowed {
				allowed++
			}
		}
	}
	return allowed
}

func concurrentAllows(t *testing.T, limiter Limiter, count int, request func(int) Request) int {
	t.Helper()
	start := make(chan struct{})
	decisions := make(chan Decision, count)
	errorsFound := make(chan error, count)
	var group sync.WaitGroup
	for index := 0; index < count; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			decision, err := limiter.Allow(context.Background(), request(index))
			if err != nil {
				errorsFound <- err
				return
			}
			decisions <- decision
		}(index)
	}
	close(start)
	group.Wait()
	close(decisions)
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("Allow() error = %v", err)
	}
	allowed := 0
	for decision := range decisions {
		if decision.Allowed {
			allowed++
		}
	}
	return allowed
}

type overlapResult struct {
	allowed bool
	err     error
}

func assertIncreasingClockOverlap(t *testing.T, stateMutex *sync.Mutex, clock *controlledIncreasingClock, operation func() (bool, error)) {
	t.Helper()
	results := make(chan overlapResult, 2)
	run := func() {
		allowed, err := operation()
		results <- overlapResult{allowed: allowed, err: err}
	}
	go run()
	<-clock.firstEntered
	clockWasReadOutsideStateLock := stateMutex.TryLock()
	if clockWasReadOutsideStateLock {
		stateMutex.Unlock()
	}
	go run()

	collected := make([]overlapResult, 0, 2)
	if clockWasReadOutsideStateLock {
		collected = append(collected, <-results)
		close(clock.releaseFirst)
	} else {
		close(clock.releaseFirst)
	}
	for len(collected) < 2 {
		collected = append(collected, <-results)
	}

	allowed := 0
	denied := 0
	for _, result := range collected {
		if result.err != nil {
			t.Errorf("overlapping operation error = %v", result.err)
			continue
		}
		if result.allowed {
			allowed++
		} else {
			denied++
		}
	}
	if allowed != 1 || denied != 1 {
		t.Fatalf("allowed/denied = %d/%d, want 1/1; results = %#v", allowed, denied, collected)
	}
	if clock.calls.Load() != 2 {
		t.Fatalf("clock calls = %d, want 2", clock.calls.Load())
	}
}
