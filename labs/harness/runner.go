package harness

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"regexp"
	"sort"
	"strings"
	"time"
)

var stableIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

type Runner struct{}

func NewRunner() *Runner {
	return &Runner{}
}

func (*Runner) Run(ctx context.Context, source RunSpec) (RunResult, error) {
	spec := cloneRunSpec(source)
	start := source.Start.UTC()
	result := newFailedResult(start)
	if err := validateRunSpec(spec); err != nil {
		return appendFailure(result, "preflight", "run specification is invalid", fmt.Errorf("run preflight failed: %w", err))
	}

	if ctx == nil {
		return appendFailure(result, "context", "context is nil", errors.New("run context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return appendFailure(result, "context", "context canceled before execution", fmt.Errorf("run context unavailable before execution: %w", err))
	}

	sort.SliceStable(spec.Events, func(left, right int) bool {
		if spec.Events[left].At != spec.Events[right].At {
			return spec.Events[left].At < spec.Events[right].At
		}
		if spec.Events[left].Phase != spec.Events[right].Phase {
			return spec.Events[left].Phase < spec.Events[right].Phase
		}
		return spec.Events[left].Sequence < spec.Events[right].Sequence
	})

	clock := newManualClock(start)
	recorder := NewRecorder()
	random := rand.New(rand.NewSource(spec.Seed))
	for _, event := range spec.Events {
		if err := ctx.Err(); err != nil {
			result.FinishedAt = clock.Now()
			result.Metrics = recorder.Snapshot().copyMetrics()
			return appendFailure(result, event.Name, "context canceled before event", fmt.Errorf("context unavailable before event %q: %w", event.Name, err))
		}

		if err := clock.advance(start.Add(event.At)); err != nil {
			result.FinishedAt = clock.Now()
			result.Metrics = recorder.Snapshot().copyMetrics()
			return appendFailure(result, event.Name, "logical clock advance failed", fmt.Errorf("advance clock for event %q: %w", event.Name, err))
		}

		runtime := &Runtime{
			Clock:    clock,
			Recorder: recorder,
			Random:   random,
		}
		err := event.Apply(ctx, runtime)
		result.EventsExecuted++
		if err != nil {
			result.FinishedAt = clock.Now()
			result.Metrics = recorder.Snapshot().copyMetrics()
			return appendFailure(result, event.Name, "action failed", fmt.Errorf("event %q failed: %w", event.Name, err))
		}
		if err := ctx.Err(); err != nil {
			result.FinishedAt = clock.Now()
			result.Metrics = recorder.Snapshot().copyMetrics()
			return appendFailure(result, event.Name, "context canceled after event", fmt.Errorf("context unavailable after event %q: %w", event.Name, err))
		}
	}

	result.FinishedAt = clock.Now()
	snapshot := recorder.Snapshot()
	result.Metrics = snapshot.copyMetrics()
	assertionsFailed := false
	for _, assertion := range spec.Assertions {
		passed, message := assertion.Check(snapshot)
		if !passed && strings.TrimSpace(message) == "" {
			message = "assertion failed"
		}
		result.Assertions = append(result.Assertions, AssertionResult{
			ID:      assertion.ID,
			Passed:  passed,
			Message: message,
		})
		if !passed {
			assertionsFailed = true
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Event:   assertion.ID,
				Message: message,
			})
		}
	}
	if assertionsFailed {
		return result, errors.New("one or more assertions failed")
	}

	result.Status = StatusPassed
	return result, nil
}

func cloneRunSpec(source RunSpec) RunSpec {
	cloned := source
	cloned.Parameters = make(map[string]int64, len(source.Parameters))
	for name, value := range source.Parameters {
		cloned.Parameters[name] = value
	}
	cloned.Events = make([]Event, len(source.Events))
	copy(cloned.Events, source.Events)
	cloned.Assertions = make([]Assertion, len(source.Assertions))
	copy(cloned.Assertions, source.Assertions)
	return cloned
}

func validateRunSpec(spec RunSpec) error {
	start := spec.Start.UTC()
	identities := []struct {
		name     string
		value    string
		required bool
	}{
		{name: "lab ID", value: spec.LabID, required: true},
		{name: "required run ID", value: spec.RequiredRunID, required: true},
		{name: "binding ID", value: spec.BindingID, required: true},
		{name: "claim ID", value: spec.ClaimID, required: true},
		{name: "implementation ID", value: spec.ImplementationID, required: true},
		{name: "adapter ID", value: spec.AdapterID, required: false},
	}
	for _, identity := range identities {
		if identity.value == "" && !identity.required {
			continue
		}
		if !stableIDPattern.MatchString(identity.value) {
			return fmt.Errorf("%s %q must match %s", identity.name, identity.value, stableIDPattern.String())
		}
	}
	if spec.Deadline <= 0 {
		return fmt.Errorf("deadline %s must be positive", spec.Deadline)
	}

	type eventKey struct {
		at       time.Duration
		phase    Phase
		sequence uint64
	}
	seenEvents := make(map[eventKey]struct{}, len(spec.Events))
	for index, event := range spec.Events {
		if event.At < 0 {
			return fmt.Errorf("event %d offset %s must be non-negative", index, event.At)
		}
		if event.At > spec.Deadline {
			return fmt.Errorf("event %d offset %s exceeds deadline %s", index, event.At, spec.Deadline)
		}
		if !logicalEventTimeRepresentable(start, event.At) {
			return fmt.Errorf("event %d offset %s overflows logical time", index, event.At)
		}
		if event.Phase != PhaseFault && event.Phase != PhaseRequest && event.Phase != PhaseObserve {
			return fmt.Errorf("event %d has invalid phase %d", index, event.Phase)
		}
		if strings.TrimSpace(event.Name) == "" {
			return fmt.Errorf("event %d name must be nonblank", index)
		}
		if event.Apply == nil {
			return fmt.Errorf("event %q action is nil", event.Name)
		}
		key := eventKey{at: event.At, phase: event.Phase, sequence: event.Sequence}
		if _, exists := seenEvents[key]; exists {
			return fmt.Errorf("event %q repeats schedule key (%s,%d,%d)", event.Name, event.At, event.Phase, event.Sequence)
		}
		seenEvents[key] = struct{}{}
	}

	seenAssertions := make(map[string]struct{}, len(spec.Assertions))
	for index, assertion := range spec.Assertions {
		if !stableIDPattern.MatchString(assertion.ID) {
			return fmt.Errorf("assertion %d ID %q must match %s", index, assertion.ID, stableIDPattern.String())
		}
		if assertion.Check == nil {
			return fmt.Errorf("assertion %q check is nil", assertion.ID)
		}
		if _, exists := seenAssertions[assertion.ID]; exists {
			return fmt.Errorf("assertion ID %q is duplicated", assertion.ID)
		}
		seenAssertions[assertion.ID] = struct{}{}
	}
	return nil
}

func logicalEventTimeRepresentable(start time.Time, offset time.Duration) bool {
	target := start.Add(offset)
	if offset == 0 {
		return target.Equal(start)
	}
	return target.After(start) && target.Add(-offset).Equal(start)
}

func newFailedResult(start time.Time) RunResult {
	return RunResult{
		Status:      StatusFailed,
		StartedAt:   start,
		FinishedAt:  start,
		Metrics:     make([]Metric, 0),
		Assertions:  make([]AssertionResult, 0),
		Diagnostics: make([]Diagnostic, 0),
	}
}

func appendFailure(result RunResult, event, message string, err error) (RunResult, error) {
	result.Diagnostics = append(result.Diagnostics, Diagnostic{Event: event, Message: message})
	return result, err
}
