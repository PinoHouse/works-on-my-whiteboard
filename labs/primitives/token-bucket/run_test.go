package tokenbucket

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
	"github.com/PinoHouse/works-on-my-whiteboard/labs/harness"
)

var runImplementationIDs = []string{
	"token-bucket-reference-model",
	"token-bucket",
}

type firstRefillOnlySubject struct {
	start time.Time
}

func (s *firstRefillOnlySubject) Take(now time.Time, amount uint64) (Decision, error) {
	offset := now.Sub(s.start)
	switch {
	case offset == 0 && amount == runBurstAmount:
		return Decision{Allowed: true, Remaining: 0}, nil
	case offset == 0 && amount == runUnitAmount:
		return Decision{Allowed: false, Remaining: 0, RetryAfter: runRefillEvery}, nil
	case offset == runRefillEvery-time.Nanosecond && amount == runUnitAmount:
		return Decision{Allowed: false, Remaining: 0, RetryAfter: time.Nanosecond}, nil
	case offset == runRefillEvery && amount == runUnitAmount:
		return Decision{Allowed: true, Remaining: 0}, nil
	case offset == 3*runRefillEvery && amount == 2*runUnitAmount:
		return Decision{Allowed: false, Remaining: 0, RetryAfter: runRefillEvery}, nil
	default:
		return Decision{}, fmt.Errorf("unexpected probe at %s for amount %d", offset, amount)
	}
}

func TestBuildRunSpecRejectsUnknownImplementations(t *testing.T) {
	for _, implementationID := range []string{"", "unknown", "Token-Bucket"} {
		t.Run(implementationID, func(t *testing.T) {
			if _, err := BuildRunSpec(implementationID); err == nil {
				t.Fatalf("BuildRunSpec(%q) error = nil, want rejection", implementationID)
			}
		})
	}
}

func TestBuildRunSpecFreezesIdentityParametersAndSchedule(t *testing.T) {
	for _, implementationID := range runImplementationIDs {
		t.Run(implementationID, func(t *testing.T) {
			spec, err := BuildRunSpec(implementationID)
			if err != nil {
				t.Fatalf("BuildRunSpec() error = %v", err)
			}
			if spec.LabID != "token-bucket" ||
				spec.RequiredRunID != "burst-and-refill-boundary" ||
				spec.BindingID != "token-bucket-burst-boundary" ||
				spec.ClaimID != "token-bucket-bounds-burst-and-average-rate" ||
				spec.ImplementationID != implementationID ||
				spec.AdapterID != "" {
				t.Fatalf("run identity = %#v", spec)
			}
			if spec.Seed != 1 || !spec.Start.Equal(time.Unix(0, 0).UTC()) || spec.Start.Location() != time.UTC || spec.Deadline != 4*time.Second {
				t.Fatalf("seed/start/deadline = %d/%v/%s", spec.Seed, spec.Start, spec.Deadline)
			}
			wantParameters := map[string]int64{
				"capacity":        4,
				"refill_every_ns": int64(time.Second),
				"burst_amount":    4,
				"unit_amount":     1,
			}
			if !reflect.DeepEqual(spec.Parameters, wantParameters) {
				t.Fatalf("parameters = %#v, want %#v", spec.Parameters, wantParameters)
			}

			wantEvents := []struct {
				at       time.Duration
				phase    harness.Phase
				sequence uint64
				name     string
			}{
				{at: 0, phase: harness.PhaseRequest, sequence: 0, name: "consume-initial-capacity"},
				{at: 0, phase: harness.PhaseRequest, sequence: 1, name: "deny-immediate-unit"},
				{at: time.Second - time.Nanosecond, phase: harness.PhaseRequest, sequence: 2, name: "deny-pre-boundary-unit"},
				{at: time.Second, phase: harness.PhaseRequest, sequence: 3, name: "allow-boundary-unit"},
				{at: 3 * time.Second, phase: harness.PhaseRequest, sequence: 4, name: "allow-multi-interval-refill"},
			}
			if len(spec.Events) != len(wantEvents) {
				t.Fatalf("events = %d, want %d", len(spec.Events), len(wantEvents))
			}
			for index, want := range wantEvents {
				got := spec.Events[index]
				if got.At != want.at || got.Phase != want.phase || got.Sequence != want.sequence || got.Name != want.name || got.Apply == nil {
					t.Fatalf("event %d = %#v, want at=%s phase=%d sequence=%d name=%q and nonnil action", index, got, want.at, want.phase, want.sequence, want.name)
				}
			}

			wantAssertions := []string{
				"initial-burst-bounded",
				"pre-boundary-denied",
				"boundary-refills-one",
				"capacity-never-exceeded",
				"implementation-matches-reference",
			}
			gotAssertions := make([]string, len(spec.Assertions))
			for index, assertion := range spec.Assertions {
				gotAssertions[index] = assertion.ID
				if assertion.Check == nil {
					t.Fatalf("assertion %q has nil check", assertion.ID)
				}
			}
			if !reflect.DeepEqual(gotAssertions, wantAssertions) {
				t.Fatalf("assertion IDs = %#v, want %#v", gotAssertions, wantAssertions)
			}
		})
	}
}

func TestBuildRunSpecReturnsFreshIndependentValues(t *testing.T) {
	first, err := BuildRunSpec("token-bucket")
	if err != nil {
		t.Fatalf("first BuildRunSpec() error = %v", err)
	}
	second, err := BuildRunSpec("token-bucket")
	if err != nil {
		t.Fatalf("second BuildRunSpec() error = %v", err)
	}

	first.Parameters["capacity"] = 99
	first.Events[0].Name = "mutated-event"
	first.Assertions[0].ID = "mutated-assertion"
	if second.Parameters["capacity"] != 4 {
		t.Fatalf("parameter maps alias: second capacity = %d", second.Parameters["capacity"])
	}
	if second.Events[0].Name != "consume-initial-capacity" {
		t.Fatalf("event slices alias: second first event = %q", second.Events[0].Name)
	}
	if second.Assertions[0].ID != "initial-burst-bounded" {
		t.Fatalf("assertion slices alias: second first assertion = %q", second.Assertions[0].ID)
	}
}

func TestRunOracleRejectsFirstRefillOnlySubject(t *testing.T) {
	for _, implementationID := range runImplementationIDs {
		t.Run(implementationID, func(t *testing.T) {
			start := time.Unix(0, 0).UTC()
			spec, err := buildRunSpecWithSubject(implementationID, &firstRefillOnlySubject{start: start})
			if err != nil {
				t.Fatalf("buildRunSpecWithSubject() error = %v", err)
			}

			result, runErr := harness.NewRunner().Run(context.Background(), spec)
			if runErr == nil {
				t.Fatalf("Run() error = nil; result=%#v", result)
			}
			if result.Status == harness.StatusPassed {
				t.Fatalf("Run() status = %q, want non-passed", result.Status)
			}
			assertion, exists := assertionResultByID(result.Assertions, "implementation-matches-reference")
			if !exists {
				t.Fatalf("implementation-matches-reference assertion missing: %#v", result.Assertions)
			}
			if assertion.Passed {
				t.Fatalf("implementation-matches-reference passed for first-refill-only subject")
			}
			for _, other := range result.Assertions {
				if other.ID != assertion.ID && !other.Passed {
					t.Fatalf("unrelated assertion %q failed: %s", other.ID, other.Message)
				}
			}
			mismatches, exists := metricValueByName(result.Metrics, "reference.mismatches")
			if !exists || mismatches != 1 {
				t.Fatalf("reference.mismatches = %d, %t; want 1, true", mismatches, exists)
			}
		})
	}
}

func TestFrozenProbeOracleRequiresMultiIntervalRefill(t *testing.T) {
	got, exists := frozenProbeDecision(probeMultiInterval)
	if !exists {
		t.Fatal("multi-interval probe has no frozen oracle decision")
	}
	want := Decision{Allowed: true, Remaining: 0}
	if got != want {
		t.Fatalf("multi-interval frozen decision = %#v, want %#v", got, want)
	}
}

func TestBuildRunSpecExecutesBothImplementationsDeterministically(t *testing.T) {
	wantMetrics := []harness.Metric{
		{Name: "probe.boundary_allowed", Unit: "count", Value: 1},
		{Name: "probe.immediate_denied", Unit: "count", Value: 1},
		{Name: "probe.initial_burst_allowed", Unit: "count", Value: 1},
		{Name: "probe.pre_boundary_denied", Unit: "count", Value: 1},
		{Name: "reference.mismatches", Unit: "count", Value: 0},
		{Name: "requests.allowed", Unit: "count", Value: 3},
		{Name: "requests.denied", Unit: "count", Value: 2},
		{Name: "requests.total", Unit: "count", Value: 5},
		{Name: "tokens.capacity", Unit: "tokens", Value: 4},
		{Name: "tokens.max_observed", Unit: "tokens", Value: 4},
		{Name: "tokens.remaining", Unit: "tokens", Value: 0},
	}
	wantAssertionIDs := []string{
		"initial-burst-bounded",
		"pre-boundary-denied",
		"boundary-refills-one",
		"capacity-never-exceeded",
		"implementation-matches-reference",
	}

	results := make([]harness.RunResult, 0, len(runImplementationIDs))
	for _, implementationID := range runImplementationIDs {
		t.Run(implementationID, func(t *testing.T) {
			firstSpec, err := BuildRunSpec(implementationID)
			if err != nil {
				t.Fatalf("first BuildRunSpec() error = %v", err)
			}
			secondSpec, err := BuildRunSpec(implementationID)
			if err != nil {
				t.Fatalf("second BuildRunSpec() error = %v", err)
			}
			firstResult, err := harness.NewRunner().Run(context.Background(), firstSpec)
			if err != nil {
				t.Fatalf("first Run() error = %v; result=%#v", err, firstResult)
			}
			secondResult, err := harness.NewRunner().Run(context.Background(), secondSpec)
			if err != nil {
				t.Fatalf("second Run() error = %v; result=%#v", err, secondResult)
			}
			if !reflect.DeepEqual(firstResult, secondResult) {
				t.Fatalf("fresh specs are nondeterministic or share state:\nfirst=%#v\nsecond=%#v", firstResult, secondResult)
			}
			if firstResult.Status != harness.StatusPassed || firstResult.EventsExecuted != 5 {
				t.Fatalf("status/events = %q/%d, want passed/5", firstResult.Status, firstResult.EventsExecuted)
			}
			if !firstResult.StartedAt.Equal(time.Unix(0, 0).UTC()) || !firstResult.FinishedAt.Equal(time.Unix(0, 0).UTC().Add(3*time.Second)) {
				t.Fatalf("start/finish = %v/%v", firstResult.StartedAt, firstResult.FinishedAt)
			}
			if !reflect.DeepEqual(firstResult.Metrics, wantMetrics) {
				t.Fatalf("metrics = %#v, want %#v", firstResult.Metrics, wantMetrics)
			}
			gotAssertionIDs := make([]string, len(firstResult.Assertions))
			for index, assertion := range firstResult.Assertions {
				gotAssertionIDs[index] = assertion.ID
				if !assertion.Passed {
					t.Fatalf("assertion %q failed: %s", assertion.ID, assertion.Message)
				}
			}
			if !reflect.DeepEqual(gotAssertionIDs, wantAssertionIDs) {
				t.Fatalf("assertion IDs = %#v, want %#v", gotAssertionIDs, wantAssertionIDs)
			}
			results = append(results, firstResult)
		})
	}
	if len(results) != 2 || !reflect.DeepEqual(results[0], results[1]) {
		t.Fatalf("implementation results differ: %#v", results)
	}
}

func TestLabManifestMatchesExecutableRun(t *testing.T) {
	data, err := os.ReadFile("lab.yaml")
	if err != nil {
		t.Fatalf("ReadFile(lab.yaml) error = %v", err)
	}
	manifest, err := catalog.DecodeStrict[catalog.LabManifest]("lab.yaml", data)
	if err != nil {
		t.Fatalf("DecodeStrict(lab.yaml) error = %v", err)
	}
	if manifest.ID != "token-bucket" || manifest.Kind != catalog.LabKindPrimitive || !manifest.Required || manifest.Status != catalog.LifecycleStatusComplete {
		t.Fatalf("lab contract = %#v", manifest)
	}
	if !reflect.DeepEqual(manifest.Implementations, runImplementationIDs) {
		t.Fatalf("implementations = %#v, want %#v", manifest.Implementations, runImplementationIDs)
	}
	if len(manifest.CaseBindings) != 0 || len(manifest.PrincipleBindings) != 1 {
		t.Fatalf("bindings = case %#v principle %#v", manifest.CaseBindings, manifest.PrincipleBindings)
	}
	binding := manifest.PrincipleBindings[0]
	if binding.ID != "token-bucket-burst-boundary" || binding.PrincipleID != "token-bucket" || binding.Claim != "token-bucket-bounds-burst-and-average-rate" || binding.Workload != "burst-refill-boundary" {
		t.Fatalf("principle binding = %#v", binding)
	}
	wantAssertions := []string{
		"initial-burst-bounded",
		"pre-boundary-denied",
		"boundary-refills-one",
		"capacity-never-exceeded",
		"implementation-matches-reference",
	}
	if !reflect.DeepEqual(binding.Assertions, wantAssertions) {
		t.Fatalf("binding assertions = %#v, want %#v", binding.Assertions, wantAssertions)
	}
	if len(manifest.RequiredRuns) != 1 {
		t.Fatalf("required runs = %#v, want one", manifest.RequiredRuns)
	}
	run := manifest.RequiredRuns[0]
	if run.ID != "burst-and-refill-boundary" || run.Binding != binding.ID || run.Baseline != runImplementationIDs[0] || !reflect.DeepEqual(run.Variants, []string{runImplementationIDs[1]}) || run.Workload != binding.Workload {
		t.Fatalf("required run = %#v", run)
	}
	if run.Faults == nil || len(run.Faults) != 0 || len(run.Adapters) != 0 {
		t.Fatalf("faults/adapters = %#v/%#v, want explicit empty faults and omitted adapters", run.Faults, run.Adapters)
	}

	wantMetricNames := []string{
		"requests.total",
		"requests.allowed",
		"requests.denied",
		"tokens.capacity",
		"tokens.remaining",
		"tokens.max_observed",
		"probe.initial_burst_allowed",
		"probe.immediate_denied",
		"probe.pre_boundary_denied",
		"probe.boundary_allowed",
		"reference.mismatches",
	}
	if !reflect.DeepEqual(manifest.Metrics, wantMetricNames) {
		t.Fatalf("manifest metrics = %#v, want %#v", manifest.Metrics, wantMetricNames)
	}
	spec, err := BuildRunSpec("token-bucket")
	if err != nil {
		t.Fatalf("BuildRunSpec() error = %v", err)
	}
	result, err := harness.NewRunner().Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	resultMetricNames := make([]string, len(result.Metrics))
	for index, metric := range result.Metrics {
		resultMetricNames[index] = metric.Name
	}
	sort.Strings(wantMetricNames)
	if !reflect.DeepEqual(resultMetricNames, wantMetricNames) {
		t.Fatalf("result metric names = %#v, manifest names = %#v", resultMetricNames, wantMetricNames)
	}
}

func assertionResultByID(results []harness.AssertionResult, id string) (harness.AssertionResult, bool) {
	for _, result := range results {
		if result.ID == id {
			return result, true
		}
	}
	return harness.AssertionResult{}, false
}

func metricValueByName(metrics []harness.Metric, name string) (int64, bool) {
	for _, metric := range metrics {
		if metric.Name == name {
			return metric.Value, true
		}
	}
	return 0, false
}
