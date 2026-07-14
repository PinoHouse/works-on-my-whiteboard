package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var testStart = time.Date(2026, time.July, 14, 8, 9, 10, 11, time.FixedZone("test", 8*60*60))

func validRunSpec() RunSpec {
	return RunSpec{
		LabID:            "lab-one",
		RequiredRunID:    "run-one",
		BindingID:        "binding-one",
		ClaimID:          "claim-one",
		ImplementationID: "implementation-one",
		AdapterID:        "",
		Seed:             42,
		Start:            testStart,
		Deadline:         time.Second,
		Parameters:       map[string]int64{"requests": 10},
		Events: []Event{
			{
				At:       0,
				Phase:    PhaseRequest,
				Sequence: 0,
				Name:     "request-one",
				Apply: func(context.Context, *Runtime) error {
					return nil
				},
			},
		},
		Assertions: []Assertion{
			{
				ID: "assertion-one",
				Check: func(Snapshot) (bool, string) {
					return true, ""
				},
			},
		},
	}
}

func TestRunnerOrdersCopiedEventsByTimePhaseAndSequence(t *testing.T) {
	rawStart := time.Now()
	var order []string
	var observedTimes []time.Time
	record := func(name string) Action {
		return func(_ context.Context, runtime *Runtime) error {
			order = append(order, name)
			observedTimes = append(observedTimes, runtime.Clock.Now())
			return nil
		}
	}

	events := []Event{
		{At: 5 * time.Nanosecond, Phase: PhaseObserve, Sequence: 2, Name: "observe", Apply: record("observe")},
		{At: 5 * time.Nanosecond, Phase: PhaseRequest, Sequence: math.MaxUint64, Name: "request-max", Apply: record("request-max")},
		{At: 5 * time.Nanosecond, Phase: PhaseFault, Sequence: math.MaxUint64, Name: "fault-max", Apply: record("fault-max")},
		{At: time.Nanosecond, Phase: PhaseRequest, Sequence: 8, Name: "earlier", Apply: record("earlier")},
		{At: 5 * time.Nanosecond, Phase: PhaseFault, Sequence: 1, Name: "fault-one", Apply: record("fault-one")},
		{At: 5 * time.Nanosecond, Phase: PhaseRequest, Sequence: 0, Name: "request-zero", Apply: record("request-zero")},
	}
	originalNames := eventNames(events)
	spec := validRunSpec()
	spec.Start = rawStart
	spec.Events = events
	spec.Assertions = nil

	result, err := NewRunner().Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	wantOrder := []string{"earlier", "fault-one", "fault-max", "request-zero", "request-max", "observe"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("execution order = %#v, want %#v", order, wantOrder)
	}
	if got := eventNames(events); !reflect.DeepEqual(got, originalNames) {
		t.Fatalf("Run reordered caller events: got %#v, want %#v", got, originalNames)
	}
	wantTimes := []time.Time{
		rawStart.UTC().Add(time.Nanosecond),
		rawStart.UTC().Add(5 * time.Nanosecond),
		rawStart.UTC().Add(5 * time.Nanosecond),
		rawStart.UTC().Add(5 * time.Nanosecond),
		rawStart.UTC().Add(5 * time.Nanosecond),
		rawStart.UTC().Add(5 * time.Nanosecond),
	}
	if !reflect.DeepEqual(observedTimes, wantTimes) {
		t.Fatalf("observed times = %#v, want %#v", observedTimes, wantTimes)
	}
	if result.StartedAt != rawStart.UTC() {
		t.Fatalf("StartedAt = %#v, want %#v", result.StartedAt, rawStart.UTC())
	}
	if result.FinishedAt != rawStart.UTC().Add(5*time.Nanosecond) {
		t.Fatalf("FinishedAt = %#v, want %#v", result.FinishedAt, rawStart.UTC().Add(5*time.Nanosecond))
	}
	if result.EventsExecuted != uint64(len(events)) {
		t.Fatalf("EventsExecuted = %d, want %d", result.EventsExecuted, len(events))
	}
	assertStatusErrorInvariant(t, result, err)
}

func TestRunnerSeededProjectionIsDeterministicAndRunLocal(t *testing.T) {
	makeSpec := func(seed int64) RunSpec {
		spec := validRunSpec()
		spec.Seed = seed
		spec.Events = []Event{
			{
				At:       10 * time.Nanosecond,
				Phase:    PhaseObserve,
				Sequence: 0,
				Name:     "sample",
				Apply: func(_ context.Context, runtime *Runtime) error {
					return runtime.Recorder.Set("random.sample", "value", runtime.Random.Int63())
				},
			},
		}
		spec.Assertions = []Assertion{
			{ID: "sample-present", Check: func(snapshot Snapshot) (bool, string) {
				_, ok := snapshot.Value("random.sample")
				return ok, "sample is missing"
			}},
		}
		return spec
	}

	runner := NewRunner()
	first, firstErr := runner.Run(context.Background(), makeSpec(11))
	second, secondErr := runner.Run(context.Background(), makeSpec(11))
	if firstErr != nil || secondErr != nil {
		t.Fatalf("same-seed runs returned errors: first=%v second=%v", firstErr, secondErr)
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first result: %v", err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second result: %v", err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("same-seed projections differ:\n%s\n%s", firstJSON, secondJSON)
	}

	third, err := runner.Run(context.Background(), makeSpec(12))
	if err != nil {
		t.Fatalf("different-seed run returned error: %v", err)
	}
	if first.Metrics[0].Value == third.Metrics[0].Value {
		t.Fatalf("different seeds produced the same first sample %d", first.Metrics[0].Value)
	}
}

func TestRunnerDeadlineIsInclusiveAndLogicalOnly(t *testing.T) {
	t.Run("event at deadline executes", func(t *testing.T) {
		called := 0
		spec := validRunSpec()
		spec.Deadline = time.Nanosecond
		spec.Events[0].At = time.Nanosecond
		spec.Events[0].Apply = func(ctx context.Context, _ *Runtime) error {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("logical deadline became context deadline: %w", err)
			}
			called++
			return nil
		}

		result, err := NewRunner().Run(context.Background(), spec)
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
		if called != 1 {
			t.Fatalf("callback count = %d, want 1", called)
		}
		if result.FinishedAt != spec.Start.UTC().Add(time.Nanosecond) {
			t.Fatalf("FinishedAt = %v, want logical deadline", result.FinishedAt)
		}
	})

	t.Run("event after deadline is atomically rejected", func(t *testing.T) {
		called := 0
		spec := validRunSpec()
		spec.Deadline = time.Nanosecond
		spec.Events[0].At = 2 * time.Nanosecond
		spec.Events[0].Apply = func(context.Context, *Runtime) error {
			called++
			return nil
		}

		result, err := NewRunner().Run(context.Background(), spec)
		assertFailedInitialized(t, result, err, spec.Start.UTC())
		if called != 0 {
			t.Fatalf("callback count = %d, want 0", called)
		}
	})
}

func TestRunnerPreflightRejectsOverflowingLogicalScheduleAtomically(t *testing.T) {
	start := time.Unix(math.MaxInt64-62_135_596_800, 999_999_999).UTC()
	callbacks := 0
	action := func(context.Context, *Runtime) error {
		callbacks++
		return nil
	}
	spec := validRunSpec()
	spec.Start = start
	spec.Deadline = time.Nanosecond
	spec.Events = []Event{
		{At: 0, Phase: PhaseRequest, Sequence: 0, Name: "at-start", Apply: action},
		{At: time.Nanosecond, Phase: PhaseRequest, Sequence: 1, Name: "overflow", Apply: action},
	}
	spec.Assertions = nil

	result, err := NewRunner().Run(context.Background(), spec)
	assertFailedInitialized(t, result, err, start)
	if callbacks != 0 {
		t.Fatalf("callback count = %d, want atomic preflight rejection with zero callbacks", callbacks)
	}
}

func TestRunnerAcceptsRepresentableLogicalSchedulesAtOrdinaryZeroAndNearMinStarts(t *testing.T) {
	tests := []struct {
		name  string
		start time.Time
	}{
		{name: "ordinary", start: testStart},
		{name: "zero", start: time.Time{}},
		{name: "near minimum Unix", start: time.Unix(math.MinInt64, 0).UTC()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			callbacks := 0
			spec := validRunSpec()
			spec.Start = test.start
			spec.Deadline = time.Nanosecond
			spec.Events[0].At = time.Nanosecond
			spec.Events[0].Apply = func(context.Context, *Runtime) error {
				callbacks++
				return nil
			}
			spec.Assertions = nil

			result, err := NewRunner().Run(context.Background(), spec)
			if err != nil {
				t.Fatalf("Run returned error: %v", err)
			}
			if callbacks != 1 {
				t.Fatalf("callback count = %d, want 1", callbacks)
			}
			if got, want := result.FinishedAt, test.start.UTC().Add(time.Nanosecond); got != want {
				t.Fatalf("FinishedAt = %#v, want %#v", got, want)
			}
		})
	}
}

func TestRunnerPreflightRejectsInvalidSpecsAtomically(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RunSpec)
	}{
		{name: "missing lab ID", mutate: func(spec *RunSpec) { spec.LabID = "" }},
		{name: "invalid lab ID", mutate: func(spec *RunSpec) { spec.LabID = "Lab" }},
		{name: "missing required run ID", mutate: func(spec *RunSpec) { spec.RequiredRunID = "" }},
		{name: "invalid required run ID", mutate: func(spec *RunSpec) { spec.RequiredRunID = "run_one" }},
		{name: "missing binding ID", mutate: func(spec *RunSpec) { spec.BindingID = "" }},
		{name: "invalid binding ID", mutate: func(spec *RunSpec) { spec.BindingID = "1binding" }},
		{name: "missing claim ID", mutate: func(spec *RunSpec) { spec.ClaimID = "" }},
		{name: "invalid claim ID", mutate: func(spec *RunSpec) { spec.ClaimID = "claim.one" }},
		{name: "missing implementation ID", mutate: func(spec *RunSpec) { spec.ImplementationID = "" }},
		{name: "invalid implementation ID", mutate: func(spec *RunSpec) { spec.ImplementationID = "implementation_" }},
		{name: "invalid adapter ID", mutate: func(spec *RunSpec) { spec.AdapterID = "Adapter" }},
		{name: "zero deadline", mutate: func(spec *RunSpec) { spec.Deadline = 0 }},
		{name: "negative deadline", mutate: func(spec *RunSpec) { spec.Deadline = -time.Nanosecond }},
		{name: "negative event offset", mutate: func(spec *RunSpec) { spec.Events[0].At = -time.Nanosecond }},
		{name: "event after deadline", mutate: func(spec *RunSpec) { spec.Events[0].At = spec.Deadline + time.Nanosecond }},
		{name: "invalid phase", mutate: func(spec *RunSpec) { spec.Events[0].Phase = Phase(255) }},
		{name: "blank event name", mutate: func(spec *RunSpec) { spec.Events[0].Name = " \t" }},
		{name: "nil action", mutate: func(spec *RunSpec) { spec.Events[0].Apply = nil }},
		{name: "duplicate event key", mutate: func(spec *RunSpec) {
			spec.Events = append(spec.Events, Event{At: spec.Events[0].At, Phase: spec.Events[0].Phase, Sequence: spec.Events[0].Sequence, Name: "duplicate", Apply: func(context.Context, *Runtime) error { return nil }})
		}},
		{name: "invalid assertion ID", mutate: func(spec *RunSpec) { spec.Assertions[0].ID = "Assertion" }},
		{name: "duplicate assertion ID", mutate: func(spec *RunSpec) {
			spec.Assertions = append(spec.Assertions, Assertion{ID: spec.Assertions[0].ID, Check: func(Snapshot) (bool, string) { return true, "" }})
		}},
		{name: "nil assertion check", mutate: func(spec *RunSpec) { spec.Assertions[0].Check = nil }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actions := 0
			assertions := 0
			spec := validRunSpec()
			spec.Events[0].Apply = func(context.Context, *Runtime) error {
				actions++
				return nil
			}
			spec.Assertions[0].Check = func(Snapshot) (bool, string) {
				assertions++
				return true, ""
			}
			test.mutate(&spec)

			result, err := NewRunner().Run(context.Background(), spec)
			assertFailedInitialized(t, result, err, spec.Start.UTC())
			if actions != 0 || assertions != 0 {
				t.Fatalf("callbacks executed: actions=%d assertions=%d, want zero", actions, assertions)
			}
		})
	}
}

func TestRunnerRejectsNilAndPreCanceledContextsBeforeEvents(t *testing.T) {
	tests := []struct {
		name string
		ctx  func() context.Context
	}{
		{name: "nil", ctx: func() context.Context { return nil }},
		{name: "pre-canceled", ctx: func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := 0
			spec := validRunSpec()
			spec.Events[0].Apply = func(context.Context, *Runtime) error {
				called++
				return nil
			}
			result, err := NewRunner().Run(test.ctx(), spec)
			assertFailedInitialized(t, result, err, spec.Start.UTC())
			if called != 0 {
				t.Fatalf("callback count = %d, want 0", called)
			}
		})
	}
}

func TestRunnerSynchronousCancellationCountsEventAndSkipsRemainingWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	secondCalled := false
	assertionCalled := false
	spec := validRunSpec()
	spec.Events = []Event{
		{At: time.Nanosecond, Phase: PhaseRequest, Sequence: 0, Name: "cancel", Apply: func(_ context.Context, runtime *Runtime) error {
			if err := runtime.Recorder.Add("requests.total", "count", 1); err != nil {
				return err
			}
			cancel()
			return nil
		}},
		{At: 2 * time.Nanosecond, Phase: PhaseRequest, Sequence: 1, Name: "later", Apply: func(context.Context, *Runtime) error {
			secondCalled = true
			return nil
		}},
	}
	spec.Assertions = []Assertion{{ID: "never", Check: func(Snapshot) (bool, string) {
		assertionCalled = true
		return true, ""
	}}}

	result, err := NewRunner().Run(ctx, spec)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if result.EventsExecuted != 1 {
		t.Fatalf("EventsExecuted = %d, want 1", result.EventsExecuted)
	}
	if result.FinishedAt != spec.Start.UTC().Add(time.Nanosecond) {
		t.Fatalf("FinishedAt = %v, want first event time", result.FinishedAt)
	}
	if secondCalled || assertionCalled {
		t.Fatalf("later callbacks executed: event=%t assertion=%t", secondCalled, assertionCalled)
	}
	if len(result.Assertions) != 0 {
		t.Fatalf("Assertions = %#v, want empty", result.Assertions)
	}
	if got := metricValue(t, result.Metrics, "requests.total"); got != 1 {
		t.Fatalf("retained metric = %d, want 1", got)
	}
	assertStatusErrorInvariant(t, result, err)
}

func TestRunnerActionErrorWinsOverPostActionCancellation(t *testing.T) {
	sentinel := errors.New("sentinel action failure")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	spec := validRunSpec()
	spec.Events[0].Apply = func(context.Context, *Runtime) error {
		cancel()
		return sentinel
	}
	spec.Assertions = nil

	result, err := NewRunner().Run(ctx, spec)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Run error = %v, want wrapped sentinel", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, action error must win over context cancellation", err)
	}
	if result.EventsExecuted != 1 {
		t.Fatalf("EventsExecuted = %d, want 1", result.EventsExecuted)
	}
}

func TestRunnerFailingActionStopsAtLogicalTimeAndRetainsEffects(t *testing.T) {
	sentinel := errors.New("event exploded")
	laterCalled := false
	assertionCalled := false
	spec := validRunSpec()
	spec.Events = []Event{
		{At: time.Nanosecond, Phase: PhaseFault, Sequence: 0, Name: "prepare", Apply: func(_ context.Context, runtime *Runtime) error {
			return runtime.Recorder.Set("z.metric", "count", 2)
		}},
		{At: 3 * time.Nanosecond, Phase: PhaseRequest, Sequence: 0, Name: "explode", Apply: func(_ context.Context, runtime *Runtime) error {
			if err := runtime.Recorder.Set("a.metric", "count", 1); err != nil {
				return err
			}
			return sentinel
		}},
		{At: 4 * time.Nanosecond, Phase: PhaseObserve, Sequence: 0, Name: "later", Apply: func(context.Context, *Runtime) error {
			laterCalled = true
			return nil
		}},
	}
	spec.Assertions = []Assertion{{ID: "skipped", Check: func(Snapshot) (bool, string) {
		assertionCalled = true
		return true, ""
	}}}

	result, err := NewRunner().Run(context.Background(), spec)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Run error = %v, want wrapped sentinel", err)
	}
	if result.Status != StatusFailed || result.EventsExecuted != 2 {
		t.Fatalf("status/count = (%q, %d), want (%q, 2)", result.Status, result.EventsExecuted, StatusFailed)
	}
	if result.FinishedAt != spec.Start.UTC().Add(3*time.Nanosecond) {
		t.Fatalf("FinishedAt = %v, want failing event time", result.FinishedAt)
	}
	if laterCalled || assertionCalled || len(result.Assertions) != 0 {
		t.Fatalf("later work executed: event=%t assertion=%t results=%#v", laterCalled, assertionCalled, result.Assertions)
	}
	wantMetrics := []Metric{{Name: "a.metric", Unit: "count", Value: 1}, {Name: "z.metric", Unit: "count", Value: 2}}
	if !reflect.DeepEqual(result.Metrics, wantMetrics) {
		t.Fatalf("Metrics = %#v, want %#v", result.Metrics, wantMetrics)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Event != "explode" || strings.TrimSpace(result.Diagnostics[0].Message) == "" {
		t.Fatalf("Diagnostics = %#v, want one stable diagnostic identifying explode", result.Diagnostics)
	}
	assertStatusErrorInvariant(t, result, err)
}

func TestRunnerCollectsAllAssertionsInDeclarationOrder(t *testing.T) {
	var called []string
	spec := validRunSpec()
	spec.Events = nil
	spec.Assertions = []Assertion{
		{ID: "first-failure", Check: func(Snapshot) (bool, string) {
			called = append(called, "first-failure")
			return false, ""
		}},
		{ID: "middle-pass", Check: func(Snapshot) (bool, string) {
			called = append(called, "middle-pass")
			return true, "informational"
		}},
		{ID: "last-failure", Check: func(Snapshot) (bool, string) {
			called = append(called, "last-failure")
			return false, "last failed"
		}},
	}

	result, err := NewRunner().Run(context.Background(), spec)
	if err == nil {
		t.Fatal("Run returned nil error for false assertions")
	}
	wantCalled := []string{"first-failure", "middle-pass", "last-failure"}
	if !reflect.DeepEqual(called, wantCalled) {
		t.Fatalf("assertion call order = %#v, want %#v", called, wantCalled)
	}
	if len(result.Assertions) != 3 {
		t.Fatalf("Assertions length = %d, want 3", len(result.Assertions))
	}
	for index, wantID := range wantCalled {
		if result.Assertions[index].ID != wantID {
			t.Fatalf("Assertions[%d].ID = %q, want %q", index, result.Assertions[index].ID, wantID)
		}
	}
	if result.Assertions[0].Passed || strings.TrimSpace(result.Assertions[0].Message) == "" {
		t.Fatalf("blank false assertion was not normalized: %#v", result.Assertions[0])
	}
	if !result.Assertions[1].Passed || result.Assertions[1].Message != "informational" {
		t.Fatalf("passing assertion result = %#v", result.Assertions[1])
	}
	if result.Assertions[2].Passed || result.Assertions[2].Message != "last failed" {
		t.Fatalf("last assertion result = %#v", result.Assertions[2])
	}
	wantDiagnosticEvents := []string{"first-failure", "last-failure"}
	if got := diagnosticEvents(result.Diagnostics); !reflect.DeepEqual(got, wantDiagnosticEvents) {
		t.Fatalf("diagnostic order = %#v, want %#v", got, wantDiagnosticEvents)
	}
	assertStatusErrorInvariant(t, result, err)
}

func TestRunnerPassesFreshRuntimeWithoutPoisoningSharedRunState(t *testing.T) {
	var firstRuntime *Runtime
	var originalClock Clock
	var originalRecorder *Recorder
	var originalRandom *rand.Rand
	spec := validRunSpec()
	spec.Events = []Event{
		{At: time.Nanosecond, Phase: PhaseRequest, Sequence: 0, Name: "poison-container", Apply: func(_ context.Context, runtime *Runtime) error {
			firstRuntime = runtime
			originalClock = runtime.Clock
			originalRecorder = runtime.Recorder
			originalRandom = runtime.Random
			if err := runtime.Recorder.Set("events", "count", 1); err != nil {
				return err
			}
			runtime.Clock = nil
			runtime.Recorder = nil
			runtime.Random = nil
			return nil
		}},
		{At: 2 * time.Nanosecond, Phase: PhaseRequest, Sequence: 1, Name: "verify-container", Apply: func(_ context.Context, runtime *Runtime) error {
			if runtime == firstRuntime {
				return errors.New("runtime container was reused")
			}
			if runtime.Clock != originalClock || runtime.Recorder != originalRecorder || runtime.Random != originalRandom {
				return errors.New("shared per-run state was poisoned")
			}
			if got, want := runtime.Clock.Now(), spec.Start.UTC().Add(2*time.Nanosecond); got != want {
				return fmt.Errorf("logical time = %v, want %v", got, want)
			}
			return runtime.Recorder.Add("events", "count", 1)
		}},
	}
	spec.Assertions = nil

	result, err := NewRunner().Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := metricValue(t, result.Metrics, "events"); got != 2 {
		t.Fatalf("events metric = %d, want 2", got)
	}
}

func TestRunnerCopiesCallerInputsBeforeCallbacks(t *testing.T) {
	actions := 0
	assertions := 0
	parameters := map[string]int64{"value": 1}
	var events []Event
	var checks []Assertion
	events = []Event{
		{At: 0, Phase: PhaseRequest, Sequence: 0, Name: "mutate-caller", Apply: func(context.Context, *Runtime) error {
			actions++
			events[1].Name = "poisoned"
			events[1].Apply = func(context.Context, *Runtime) error { return errors.New("poisoned caller event") }
			checks[0].Check = func(Snapshot) (bool, string) { return false, "poisoned caller assertion" }
			parameters["value"] = 99
			return nil
		}},
		{At: time.Nanosecond, Phase: PhaseRequest, Sequence: 1, Name: "original", Apply: func(context.Context, *Runtime) error {
			actions++
			return nil
		}},
	}
	checks = []Assertion{{ID: "original-check", Check: func(Snapshot) (bool, string) {
		assertions++
		return true, ""
	}}}
	spec := validRunSpec()
	spec.Parameters = parameters
	spec.Events = events
	spec.Assertions = checks

	result, err := NewRunner().Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run returned error after caller mutation: %v", err)
	}
	if actions != 2 || assertions != 1 {
		t.Fatalf("callback counts = actions %d, assertions %d; want 2 and 1", actions, assertions)
	}
	if result.EventsExecuted != 2 || len(result.Assertions) != 1 || !result.Assertions[0].Passed {
		t.Fatalf("result reflects poisoned caller slices: %#v", result)
	}
}

func TestRunnerFinalSnapshotAndResultDoNotAliasRecorder(t *testing.T) {
	var recorder *Recorder
	spec := validRunSpec()
	spec.Events[0].Apply = func(_ context.Context, runtime *Runtime) error {
		recorder = runtime.Recorder
		return recorder.Set("value", "count", 1)
	}
	spec.Assertions = []Assertion{
		{ID: "mutate-recorder", Check: func(snapshot Snapshot) (bool, string) {
			value, ok := snapshot.Value("value")
			if err := recorder.Set("value", "count", 99); err != nil {
				return false, err.Error()
			}
			return ok && value == 1, "unexpected first snapshot"
		}},
		{ID: "snapshot-still-frozen", Check: func(snapshot Snapshot) (bool, string) {
			value, ok := snapshot.Value("value")
			return ok && value == 1, "snapshot changed after recorder mutation"
		}},
	}

	result, err := NewRunner().Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := metricValue(t, result.Metrics, "value"); got != 1 {
		t.Fatalf("result metric = %d, want final-snapshot value 1", got)
	}
	result.Metrics[0].Value = 777
	if got, _ := recorder.Snapshot().Value("value"); got != 99 {
		t.Fatalf("mutating result changed recorder to %d, want 99", got)
	}
	if err := recorder.Set("value", "count", 123); err != nil {
		t.Fatalf("Set after Run returned error: %v", err)
	}
	if result.Metrics[0].Value != 777 {
		t.Fatalf("mutating recorder changed result to %d, want 777", result.Metrics[0].Value)
	}
}

func TestRunnerAllowsEmptyEventsAndAssertions(t *testing.T) {
	spec := validRunSpec()
	spec.AdapterID = "adapter-one"
	spec.Events = nil
	spec.Assertions = nil

	result, err := NewRunner().Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Status != StatusPassed || result.EventsExecuted != 0 {
		t.Fatalf("result status/count = (%q, %d), want (%q, 0)", result.Status, result.EventsExecuted, StatusPassed)
	}
	if result.StartedAt != spec.Start.UTC() || result.FinishedAt != spec.Start.UTC() {
		t.Fatalf("empty run times = (%v, %v), want start %v", result.StartedAt, result.FinishedAt, spec.Start.UTC())
	}
	if result.Metrics == nil || result.Assertions == nil || result.Diagnostics == nil {
		t.Fatalf("empty run returned nil slices: %#v", result)
	}
	assertStatusErrorInvariant(t, result, err)
}

func TestRunnerIsReusableConcurrentlyWithRunLocalRandomness(t *testing.T) {
	runner := NewRunner()
	const runs = 32
	errorsFound := make(chan error, runs)
	var wait sync.WaitGroup
	wait.Add(runs)
	for run := 0; run < runs; run++ {
		seed := int64(run)
		go func() {
			defer wait.Done()
			want := rand.New(rand.NewSource(seed)).Int63()
			spec := validRunSpec()
			spec.Seed = seed
			spec.Events[0].Apply = func(_ context.Context, runtime *Runtime) error {
				return runtime.Recorder.Set("sample", "value", runtime.Random.Int63())
			}
			spec.Assertions = nil
			result, err := runner.Run(context.Background(), spec)
			if err != nil {
				errorsFound <- fmt.Errorf("seed %d: %w", seed, err)
				return
			}
			if got := result.Metrics[0].Value; got != want {
				errorsFound <- fmt.Errorf("seed %d: sample %d, want %d", seed, got, want)
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
}

func TestRunnerDoesNotRecoverCallbackPanics(t *testing.T) {
	sentinel := "callback panic"
	defer func() {
		if recovered := recover(); recovered != sentinel {
			t.Fatalf("recovered value = %#v, want %#v", recovered, sentinel)
		}
	}()
	spec := validRunSpec()
	spec.Events[0].Apply = func(context.Context, *Runtime) error {
		panic(sentinel)
	}
	_, _ = NewRunner().Run(context.Background(), spec)
}

func assertFailedInitialized(t *testing.T, result RunResult, err error, wantStart time.Time) {
	t.Helper()
	if err == nil {
		t.Fatal("Run returned nil error")
	}
	if result.Status != StatusFailed {
		t.Fatalf("Status = %q, want %q", result.Status, StatusFailed)
	}
	if result.StartedAt != wantStart || result.FinishedAt != wantStart {
		t.Fatalf("times = (%#v, %#v), want normalized start %#v", result.StartedAt, result.FinishedAt, wantStart)
	}
	if result.EventsExecuted != 0 {
		t.Fatalf("EventsExecuted = %d, want 0", result.EventsExecuted)
	}
	if result.Metrics == nil || result.Assertions == nil || result.Diagnostics == nil {
		t.Fatalf("failed result has nil slices: %#v", result)
	}
	if len(result.Metrics) != 0 || len(result.Assertions) != 0 {
		t.Fatalf("failed pre-execution result has output: metrics=%#v assertions=%#v", result.Metrics, result.Assertions)
	}
	if len(result.Diagnostics) == 0 || strings.TrimSpace(result.Diagnostics[0].Message) == "" {
		t.Fatalf("failed result lacks stable diagnostic: %#v", result.Diagnostics)
	}
	assertStatusErrorInvariant(t, result, err)
}

func assertStatusErrorInvariant(t *testing.T, result RunResult, err error) {
	t.Helper()
	if (err == nil) != (result.Status == StatusPassed) {
		t.Fatalf("error/status invariant violated: status=%q err=%v", result.Status, err)
	}
	if result.Status != StatusPassed && result.Status != StatusFailed {
		t.Fatalf("unexpected harness status %q", result.Status)
	}
}

func eventNames(events []Event) []string {
	names := make([]string, len(events))
	for index := range events {
		names[index] = events[index].Name
	}
	return names
}

func metricValue(t *testing.T, metrics []Metric, name string) int64 {
	t.Helper()
	for _, metric := range metrics {
		if metric.Name == name {
			return metric.Value
		}
	}
	t.Fatalf("metric %q is missing from %#v", name, metrics)
	return 0
}

func diagnosticEvents(diagnostics []Diagnostic) []string {
	events := make([]string, len(diagnostics))
	for index := range diagnostics {
		events[index] = diagnostics[index].Event
	}
	return events
}
