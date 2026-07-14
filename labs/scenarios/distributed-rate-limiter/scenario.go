package distributedratelimiter

import (
	"context"
	"fmt"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/labs/harness"
	tokenbucket "github.com/PinoHouse/works-on-my-whiteboard/labs/primitives/token-bucket"
)

const (
	implementationSharedTokenBucket  = "shared-token-bucket"
	implementationPerNodeTokenBucket = "per-node-token-bucket"
	implementationSharedFailClosed   = "shared-fail-closed"
	implementationSharedFailOpen     = "shared-fail-open"
)

var scenarioMetrics = []struct {
	name string
	unit string
}{
	{name: "requests.total", unit: "requests"},
	{name: "requests.allowed", unit: "requests"},
	{name: "requests.denied", unit: "requests"},
	{name: "requests.outage", unit: "requests"},
	{name: "requests.outage_allowed", unit: "requests"},
	{name: "requests.outage_denied", unit: "requests"},
	{name: "requests.degraded", unit: "requests"},
	{name: "decisions.errors", unit: "decisions"},
	{name: "quota.nominal_limit", unit: "tokens"},
	{name: "quota.overshoot", unit: "tokens"},
}

func BuildGlobalQuotaRunSpec(implementationID string) (harness.RunSpec, error) {
	if implementationID != implementationSharedTokenBucket && implementationID != implementationPerNodeTokenBucket {
		return harness.RunSpec{}, fmt.Errorf("unknown global quota implementation %q", implementationID)
	}
	config := tokenbucket.Config{Capacity: 4, RefillEvery: 10 * time.Second}
	state := &scenarioState{
		implementationID: implementationID,
		config:           config,
		nominalLimit:     4,
	}
	events := make([]harness.Event, 0, 9)
	sequence := uint64(0)
	for round := 1; round <= 4; round++ {
		for _, nodeID := range []string{"a", "b"} {
			request := Request{TenantID: "tenant-1", NodeID: nodeID, Amount: 1}
			events = append(events, harness.Event{
				At:       0,
				Phase:    harness.PhaseRequest,
				Sequence: sequence,
				Name:     fmt.Sprintf("request-%s-%d", nodeID, round),
				Apply:    state.requestAction(request),
			})
			sequence++
		}
	}
	events = append(events, harness.Event{
		At:       0,
		Phase:    harness.PhaseObserve,
		Sequence: 0,
		Name:     "observe-quota",
		Apply:    state.observeAction(),
	})

	expectedAllowed := int64(4)
	expectedOvershoot := int64(0)
	if implementationID == implementationPerNodeTokenBucket {
		expectedAllowed = 8
		expectedOvershoot = 4
	}
	return harness.RunSpec{
		LabID:            "distributed-rate-limiter",
		RequiredRunID:    "per-node-vs-shared-quota",
		BindingID:        "distributed-rate-limiter-global-quota",
		ClaimID:          "distributed-rate-limiter-per-node-multiplies-global-quota",
		ImplementationID: implementationID,
		AdapterID:        "",
		Seed:             1,
		Start:            time.Unix(0, 0).UTC(),
		Deadline:         2 * time.Second,
		Parameters: map[string]int64{
			"capacity":          4,
			"refill_every_ns":   int64(10 * time.Second),
			"tenant_count":      1,
			"node_count":        2,
			"requests_per_node": 4,
			"request_amount":    1,
		},
		Events: events,
		Assertions: []harness.Assertion{
			newAllRequestsDecidedAssertion(8),
			newMetricAssertion("expected-allowed-count", "requests.allowed", expectedAllowed),
			newMetricAssertion("expected-global-quota-overshoot", "quota.overshoot", expectedOvershoot),
			newMetricAssertion("no-unexpected-errors", "decisions.errors", 0),
		},
	}, nil
}

func BuildOutagePolicyRunSpec(implementationID string) (harness.RunSpec, error) {
	if implementationID != implementationSharedFailClosed && implementationID != implementationSharedFailOpen {
		return harness.RunSpec{}, fmt.Errorf("unknown outage policy implementation %q", implementationID)
	}
	config := tokenbucket.Config{Capacity: 2, RefillEvery: 10 * time.Second}
	state := &scenarioState{
		implementationID: implementationID,
		config:           config,
		nominalLimit:     2,
	}
	events := []harness.Event{
		{
			At:       0,
			Phase:    harness.PhaseRequest,
			Sequence: 0,
			Name:     "healthy-request-1",
			Apply:    state.requestAction(Request{TenantID: "tenant-1", NodeID: "a", Amount: 1}),
		},
		{
			At:       0,
			Phase:    harness.PhaseRequest,
			Sequence: 1,
			Name:     "healthy-request-2",
			Apply:    state.requestAction(Request{TenantID: "tenant-1", NodeID: "b", Amount: 1}),
		},
		{
			At:       100 * time.Millisecond,
			Phase:    harness.PhaseFault,
			Sequence: 0,
			Name:     "coordinator-down",
			Apply:    state.availabilityAction(false),
		},
	}
	for index, at := range []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 300 * time.Millisecond, 400 * time.Millisecond} {
		events = append(events, harness.Event{
			At:       at,
			Phase:    harness.PhaseRequest,
			Sequence: 0,
			Name:     fmt.Sprintf("outage-request-%d", index+1),
			Apply:    state.requestAction(Request{TenantID: "tenant-1", NodeID: "a", Amount: 1}),
		})
	}
	events = append(events,
		harness.Event{
			At:       900 * time.Millisecond,
			Phase:    harness.PhaseFault,
			Sequence: 0,
			Name:     "coordinator-up",
			Apply:    state.availabilityAction(true),
		},
		harness.Event{
			At:       900 * time.Millisecond,
			Phase:    harness.PhaseObserve,
			Sequence: 0,
			Name:     "observe-quota",
			Apply:    state.observeAction(),
		},
	)

	expectedAllowed := int64(2)
	expectedDenied := int64(4)
	expectedOutageAllowed := int64(0)
	expectedOutageDenied := int64(4)
	expectedOvershoot := int64(0)
	if implementationID == implementationSharedFailOpen {
		expectedAllowed = 6
		expectedDenied = 0
		expectedOutageAllowed = 4
		expectedOutageDenied = 0
		expectedOvershoot = 4
	}
	return harness.RunSpec{
		LabID:            "distributed-rate-limiter",
		RequiredRunID:    "coordinator-outage-policy",
		BindingID:        "distributed-rate-limiter-outage-policy",
		ClaimID:          "distributed-rate-limiter-outage-policy-trades-availability-for-quota",
		ImplementationID: implementationID,
		AdapterID:        "",
		Seed:             1,
		Start:            time.Unix(0, 0).UTC(),
		Deadline:         2 * time.Second,
		Parameters: map[string]int64{
			"capacity":                   2,
			"refill_every_ns":            int64(10 * time.Second),
			"tenant_count":               1,
			"healthy_request_count":      2,
			"outage_request_count":       4,
			"request_amount":             1,
			"outage_first_request_at_ns": int64(100 * time.Millisecond),
			"outage_request_interval_ns": int64(100 * time.Millisecond),
		},
		Events: events,
		Assertions: []harness.Assertion{
			newAllRequestsDecidedAssertion(6),
			newOutageDecisionAssertion(expectedOutageAllowed, expectedOutageDenied),
			newAvailabilityAssertion(expectedAllowed, expectedDenied),
			newMetricAssertion("expected-quota-overshoot", "quota.overshoot", expectedOvershoot),
			newMetricAssertion("no-unexpected-errors", "decisions.errors", 0),
		},
	}, nil
}

type scenarioState struct {
	implementationID string
	config           tokenbucket.Config
	nominalLimit     int64
	initialized      bool
	limiter          Limiter
	availability     *AvailabilityCoordinator
	outage           bool
}

func (s *scenarioState) initialize(runtime *harness.Runtime) error {
	if s.initialized {
		return nil
	}
	if runtime == nil || runtime.Clock == nil || runtime.Recorder == nil {
		return fmt.Errorf("scenario runtime is incomplete")
	}
	for _, metric := range scenarioMetrics {
		if err := runtime.Recorder.Set(metric.name, metric.unit, 0); err != nil {
			return fmt.Errorf("initialize metric %q: %w", metric.name, err)
		}
	}
	if err := runtime.Recorder.Set("quota.nominal_limit", "tokens", s.nominalLimit); err != nil {
		return fmt.Errorf("set nominal quota: %w", err)
	}

	switch s.implementationID {
	case implementationPerNodeTokenBucket:
		limiter, err := NewPerNodeLimiter(runtime.Clock, s.config)
		if err != nil {
			return fmt.Errorf("create per-node limiter: %w", err)
		}
		s.limiter = limiter
	case implementationSharedTokenBucket:
		coordinator, err := NewSharedCoordinator(runtime.Clock, s.config)
		if err != nil {
			return fmt.Errorf("create shared coordinator: %w", err)
		}
		limiter, err := NewCoordinatedLimiter(coordinator, PolicyFailClosed)
		if err != nil {
			return fmt.Errorf("create shared limiter: %w", err)
		}
		s.limiter = limiter
	case implementationSharedFailClosed, implementationSharedFailOpen:
		coordinator, err := NewSharedCoordinator(runtime.Clock, s.config)
		if err != nil {
			return fmt.Errorf("create shared coordinator: %w", err)
		}
		availability, err := NewAvailabilityCoordinator(coordinator)
		if err != nil {
			return fmt.Errorf("create availability coordinator: %w", err)
		}
		policy := PolicyFailClosed
		if s.implementationID == implementationSharedFailOpen {
			policy = PolicyFailOpen
		}
		limiter, err := NewCoordinatedLimiter(availability, policy)
		if err != nil {
			return fmt.Errorf("create outage limiter: %w", err)
		}
		s.availability = availability
		s.limiter = limiter
	default:
		return fmt.Errorf("unknown scenario implementation %q", s.implementationID)
	}
	s.initialized = true
	return nil
}

func (s *scenarioState) requestAction(request Request) harness.Action {
	return func(ctx context.Context, runtime *harness.Runtime) error {
		if err := s.initialize(runtime); err != nil {
			return err
		}
		if err := runtime.Recorder.Add("requests.total", "requests", 1); err != nil {
			return err
		}
		if s.outage {
			if err := runtime.Recorder.Add("requests.outage", "requests", 1); err != nil {
				return err
			}
		}
		decision, err := s.limiter.Allow(ctx, request)
		if err != nil {
			if metricErr := runtime.Recorder.Add("decisions.errors", "decisions", 1); metricErr != nil {
				return metricErr
			}
			return fmt.Errorf("limiter decision: %w", err)
		}
		if err := s.validateDecision(decision); err != nil {
			return err
		}
		if decision.Allowed {
			if err := runtime.Recorder.Add("requests.allowed", "requests", 1); err != nil {
				return err
			}
			if s.outage {
				if err := runtime.Recorder.Add("requests.outage_allowed", "requests", 1); err != nil {
					return err
				}
			}
		} else {
			if err := runtime.Recorder.Add("requests.denied", "requests", 1); err != nil {
				return err
			}
			if s.outage {
				if err := runtime.Recorder.Add("requests.outage_denied", "requests", 1); err != nil {
					return err
				}
			}
		}
		if decision.Degraded {
			if err := runtime.Recorder.Add("requests.degraded", "requests", 1); err != nil {
				return err
			}
		}
		return nil
	}
}

func (s *scenarioState) validateDecision(decision Decision) error {
	if !s.outage {
		if decision.Degraded || decision.Reason != "" {
			return fmt.Errorf("healthy decision unexpectedly degraded: %#v", decision)
		}
		return nil
	}
	wantReason := "coordinator-unavailable-fail-closed"
	wantAllowed := false
	if s.implementationID == implementationSharedFailOpen {
		wantReason = "coordinator-unavailable-fail-open"
		wantAllowed = true
	}
	if decision.Allowed != wantAllowed || !decision.Degraded || decision.Reason != wantReason || decision.Remaining != 0 || decision.RetryAfter != 0 {
		return fmt.Errorf("outage decision %#v does not match policy", decision)
	}
	return nil
}

func (s *scenarioState) availabilityAction(available bool) harness.Action {
	return func(context.Context, *harness.Runtime) error {
		if !s.initialized || s.availability == nil {
			return fmt.Errorf("outage state is not initialized")
		}
		s.availability.SetAvailable(available)
		s.outage = !available
		return nil
	}
}

func (s *scenarioState) observeAction() harness.Action {
	return func(_ context.Context, runtime *harness.Runtime) error {
		if err := s.initialize(runtime); err != nil {
			return err
		}
		allowed, exists := runtime.Recorder.Snapshot().Value("requests.allowed")
		if !exists {
			return fmt.Errorf("requests.allowed metric is absent")
		}
		overshoot := allowed - s.nominalLimit
		if overshoot < 0 {
			overshoot = 0
		}
		if err := runtime.Recorder.Set("quota.overshoot", "tokens", overshoot); err != nil {
			return fmt.Errorf("set quota overshoot: %w", err)
		}
		return nil
	}
}

func newAllRequestsDecidedAssertion(wantTotal int64) harness.Assertion {
	return harness.Assertion{
		ID: "all-requests-decided",
		Check: func(snapshot harness.Snapshot) (bool, string) {
			total, totalExists := snapshot.Value("requests.total")
			allowed, allowedExists := snapshot.Value("requests.allowed")
			denied, deniedExists := snapshot.Value("requests.denied")
			errorsFound, errorsExist := snapshot.Value("decisions.errors")
			passed := totalExists && allowedExists && deniedExists && errorsExist && total == wantTotal && total == allowed+denied+errorsFound
			return passed, fmt.Sprintf("total=%d allowed=%d denied=%d errors=%d want total=%d", total, allowed, denied, errorsFound, wantTotal)
		},
	}
}

func newMetricAssertion(id, metricName string, want int64) harness.Assertion {
	return harness.Assertion{
		ID: id,
		Check: func(snapshot harness.Snapshot) (bool, string) {
			got, exists := snapshot.Value(metricName)
			return exists && got == want, fmt.Sprintf("%s=%d want %d", metricName, got, want)
		},
	}
}

func newOutageDecisionAssertion(wantAllowed, wantDenied int64) harness.Assertion {
	return harness.Assertion{
		ID: "expected-outage-decision",
		Check: func(snapshot harness.Snapshot) (bool, string) {
			outage, outageExists := snapshot.Value("requests.outage")
			allowed, allowedExists := snapshot.Value("requests.outage_allowed")
			denied, deniedExists := snapshot.Value("requests.outage_denied")
			passed := outageExists && allowedExists && deniedExists && outage == 4 && allowed == wantAllowed && denied == wantDenied && outage == allowed+denied
			return passed, fmt.Sprintf("outage=%d allowed=%d denied=%d want allowed/denied=%d/%d", outage, allowed, denied, wantAllowed, wantDenied)
		},
	}
}

func newAvailabilityAssertion(wantAllowed, wantDenied int64) harness.Assertion {
	return harness.Assertion{
		ID: "expected-outage-availability",
		Check: func(snapshot harness.Snapshot) (bool, string) {
			allowed, allowedExists := snapshot.Value("requests.allowed")
			denied, deniedExists := snapshot.Value("requests.denied")
			degraded, degradedExists := snapshot.Value("requests.degraded")
			passed := allowedExists && deniedExists && degradedExists && allowed == wantAllowed && denied == wantDenied && degraded == 4
			return passed, fmt.Sprintf("allowed=%d denied=%d degraded=%d want=%d/%d/4", allowed, denied, degraded, wantAllowed, wantDenied)
		},
	}
}
