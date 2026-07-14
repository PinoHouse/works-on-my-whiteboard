package tokenbucket

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/labs/harness"
)

const (
	referenceImplementationID = "token-bucket-reference-model"
	bucketImplementationID    = "token-bucket"
	runCapacity               = uint64(4)
	runRefillEvery            = time.Second
	runBurstAmount            = uint64(4)
	runUnitAmount             = uint64(1)
)

type decisionTaker interface {
	Take(time.Time, uint64) (Decision, error)
}

type probeKind uint8

const (
	probeInitialBurst probeKind = iota
	probeImmediateDenial
	probePreBoundaryDenial
	probeBoundaryAllowance
	probeMultiInterval
)

type experimentState struct {
	subject     decisionTaker
	initialized bool
	maxObserved uint64
}

func BuildRunSpec(implementationID string) (harness.RunSpec, error) {
	start := time.Unix(0, 0).UTC()
	config := Config{Capacity: runCapacity, RefillEvery: runRefillEvery}
	var subject decisionTaker
	switch implementationID {
	case referenceImplementationID:
		subject = newReferenceBucket(config, start)
	case bucketImplementationID:
		bucket, err := New(config, start)
		if err != nil {
			return harness.RunSpec{}, fmt.Errorf("build token bucket implementation: %w", err)
		}
		subject = bucket
	default:
		return harness.RunSpec{}, fmt.Errorf("unknown token bucket implementation %q", implementationID)
	}
	return buildRunSpecWithSubject(implementationID, subject)
}

func buildRunSpecWithSubject(implementationID string, subject decisionTaker) (harness.RunSpec, error) {
	if implementationID != referenceImplementationID && implementationID != bucketImplementationID {
		return harness.RunSpec{}, fmt.Errorf("unknown token bucket implementation %q", implementationID)
	}
	if subject == nil {
		return harness.RunSpec{}, errors.New("token bucket run subject is nil")
	}
	start := time.Unix(0, 0).UTC()
	state := &experimentState{
		subject:     subject,
		maxObserved: runCapacity,
	}
	return harness.RunSpec{
		LabID:            "token-bucket",
		RequiredRunID:    "burst-and-refill-boundary",
		BindingID:        "token-bucket-burst-boundary",
		ClaimID:          "token-bucket-bounds-burst-and-average-rate",
		ImplementationID: implementationID,
		Seed:             1,
		Start:            start,
		Deadline:         4 * time.Second,
		Parameters: map[string]int64{
			"capacity":        int64(runCapacity),
			"refill_every_ns": int64(runRefillEvery),
			"burst_amount":    int64(runBurstAmount),
			"unit_amount":     int64(runUnitAmount),
		},
		Events: []harness.Event{
			newProbeEvent(state, 0, 0, "consume-initial-capacity", runBurstAmount, probeInitialBurst),
			newProbeEvent(state, 0, 1, "deny-immediate-unit", runUnitAmount, probeImmediateDenial),
			newProbeEvent(state, runRefillEvery-time.Nanosecond, 2, "deny-pre-boundary-unit", runUnitAmount, probePreBoundaryDenial),
			newProbeEvent(state, runRefillEvery, 3, "allow-boundary-unit", runUnitAmount, probeBoundaryAllowance),
			newProbeEvent(state, 3*runRefillEvery, 4, "allow-multi-interval-refill", 2*runUnitAmount, probeMultiInterval),
		},
		Assertions: []harness.Assertion{
			metricAssertion("initial-burst-bounded", func(snapshot harness.Snapshot) bool {
				return metricIs(snapshot, "probe.initial_burst_allowed", 1)
			}),
			metricAssertion("pre-boundary-denied", func(snapshot harness.Snapshot) bool {
				return metricIs(snapshot, "probe.immediate_denied", 1) && metricIs(snapshot, "probe.pre_boundary_denied", 1)
			}),
			metricAssertion("boundary-refills-one", func(snapshot harness.Snapshot) bool {
				return metricIs(snapshot, "probe.boundary_allowed", 1)
			}),
			metricAssertion("capacity-never-exceeded", func(snapshot harness.Snapshot) bool {
				maximum, exists := snapshot.Value("tokens.max_observed")
				return exists && maximum <= int64(runCapacity)
			}),
			metricAssertion("implementation-matches-reference", func(snapshot harness.Snapshot) bool {
				return metricIs(snapshot, "reference.mismatches", 0)
			}),
		},
	}, nil
}

func newProbeEvent(state *experimentState, at time.Duration, sequence uint64, name string, amount uint64, kind probeKind) harness.Event {
	return harness.Event{
		At:       at,
		Phase:    harness.PhaseRequest,
		Sequence: sequence,
		Name:     name,
		Apply: func(ctx context.Context, runtime *harness.Runtime) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return state.applyProbe(runtime, amount, kind)
		},
	}
}

func (s *experimentState) applyProbe(runtime *harness.Runtime, amount uint64, kind probeKind) error {
	if !s.initialized {
		if err := initializeRunMetrics(runtime.Recorder); err != nil {
			return err
		}
		s.initialized = true
	}
	now := runtime.Clock.Now()
	got, err := s.subject.Take(now, amount)
	if err != nil {
		return fmt.Errorf("subject take: %w", err)
	}
	want, exists := frozenProbeDecision(kind)
	if !exists {
		return fmt.Errorf("probe kind %d has no frozen oracle decision", kind)
	}
	if got != want {
		if err := runtime.Recorder.Add("reference.mismatches", "count", 1); err != nil {
			return err
		}
	}
	if err := runtime.Recorder.Add("requests.total", "count", 1); err != nil {
		return err
	}
	if got.Allowed {
		if err := runtime.Recorder.Add("requests.allowed", "count", 1); err != nil {
			return err
		}
	} else if err := runtime.Recorder.Add("requests.denied", "count", 1); err != nil {
		return err
	}
	if err := runtime.Recorder.Set("tokens.remaining", "tokens", int64(got.Remaining)); err != nil {
		return err
	}
	if got.Remaining > s.maxObserved {
		s.maxObserved = got.Remaining
		if err := runtime.Recorder.Set("tokens.max_observed", "tokens", int64(got.Remaining)); err != nil {
			return err
		}
	}
	return recordProbeOutcome(runtime.Recorder, kind, got)
}

func frozenProbeDecision(kind probeKind) (Decision, bool) {
	switch kind {
	case probeInitialBurst:
		return Decision{Allowed: true, Remaining: 0}, true
	case probeImmediateDenial:
		return Decision{Allowed: false, Remaining: 0, RetryAfter: runRefillEvery}, true
	case probePreBoundaryDenial:
		return Decision{Allowed: false, Remaining: 0, RetryAfter: time.Nanosecond}, true
	case probeBoundaryAllowance:
		return Decision{Allowed: true, Remaining: 0}, true
	case probeMultiInterval:
		return Decision{Allowed: true, Remaining: 0}, true
	default:
		return Decision{}, false
	}
}

func initializeRunMetrics(recorder *harness.Recorder) error {
	metrics := []harness.Metric{
		{Name: "requests.total", Unit: "count"},
		{Name: "requests.allowed", Unit: "count"},
		{Name: "requests.denied", Unit: "count"},
		{Name: "tokens.capacity", Unit: "tokens", Value: int64(runCapacity)},
		{Name: "tokens.remaining", Unit: "tokens", Value: int64(runCapacity)},
		{Name: "tokens.max_observed", Unit: "tokens", Value: int64(runCapacity)},
		{Name: "probe.initial_burst_allowed", Unit: "count"},
		{Name: "probe.immediate_denied", Unit: "count"},
		{Name: "probe.pre_boundary_denied", Unit: "count"},
		{Name: "probe.boundary_allowed", Unit: "count"},
		{Name: "reference.mismatches", Unit: "count"},
	}
	for _, metric := range metrics {
		if err := recorder.Set(metric.Name, metric.Unit, metric.Value); err != nil {
			return fmt.Errorf("initialize metric %q: %w", metric.Name, err)
		}
	}
	return nil
}

func recordProbeOutcome(recorder *harness.Recorder, kind probeKind, decision Decision) error {
	var name string
	var passed bool
	switch kind {
	case probeInitialBurst:
		name = "probe.initial_burst_allowed"
		passed = decision.Allowed && decision.Remaining == 0 && decision.RetryAfter == 0
	case probeImmediateDenial:
		name = "probe.immediate_denied"
		passed = !decision.Allowed && decision.Remaining == 0 && decision.RetryAfter == runRefillEvery
	case probePreBoundaryDenial:
		name = "probe.pre_boundary_denied"
		passed = !decision.Allowed && decision.Remaining == 0 && decision.RetryAfter == time.Nanosecond
	case probeBoundaryAllowance:
		name = "probe.boundary_allowed"
		passed = decision.Allowed && decision.Remaining == 0 && decision.RetryAfter == 0
	case probeMultiInterval:
		return nil
	default:
		return fmt.Errorf("unknown probe kind %d", kind)
	}
	if !passed {
		return nil
	}
	return recorder.Set(name, "count", 1)
}

func metricAssertion(id string, check func(harness.Snapshot) bool) harness.Assertion {
	return harness.Assertion{
		ID: id,
		Check: func(snapshot harness.Snapshot) (bool, string) {
			if check(snapshot) {
				return true, ""
			}
			return false, "required token bucket invariant was not observed"
		},
	}
}

func metricIs(snapshot harness.Snapshot, name string, want int64) bool {
	got, exists := snapshot.Value(name)
	return exists && got == want
}

// referenceBucket models integer interval refill independently from Bucket. It
// deliberately shares no production refill or retry helper with the subject.
type referenceBucket struct {
	capacity     uint64
	available    uint64
	refillEvery  time.Duration
	refillAnchor time.Time
	lastObserved time.Time
}

func newReferenceBucket(config Config, now time.Time) *referenceBucket {
	return &referenceBucket{
		capacity:     config.Capacity,
		available:    config.Capacity,
		refillEvery:  config.RefillEvery,
		refillAnchor: now.UTC(),
		lastObserved: now.UTC(),
	}
}

func (b *referenceBucket) Take(now time.Time, amount uint64) (Decision, error) {
	now = now.UTC()
	if amount == 0 {
		return Decision{}, ErrInvalidAmount
	}
	if amount > b.capacity {
		return Decision{}, ErrAmountExceedsCapacity
	}
	if now.Before(b.lastObserved) {
		return Decision{}, ErrTimeRollback
	}
	elapsed := now.Sub(b.refillAnchor)
	intervals := uint64(elapsed / b.refillEvery)
	remainder := elapsed % b.refillEvery
	space := b.capacity - b.available
	if intervals >= space {
		b.available = b.capacity
	} else {
		b.available += intervals
	}
	b.refillAnchor = b.refillAnchor.Add(elapsed - remainder)
	b.lastObserved = now
	if b.available >= amount {
		b.available -= amount
		return Decision{Allowed: true, Remaining: b.available}, nil
	}
	missing := amount - b.available
	firstInterval := b.refillEvery - remainder
	wait := time.Duration(math.MaxInt64)
	additional := missing - 1
	if additional <= uint64((wait-firstInterval)/b.refillEvery) {
		wait = firstInterval + time.Duration(additional)*b.refillEvery
	}
	return Decision{Allowed: false, Remaining: b.available, RetryAfter: wait}, nil
}
