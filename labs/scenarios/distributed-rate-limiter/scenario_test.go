package distributedratelimiter

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/labs/harness"
)

var scenarioMetricContract = []struct {
	id   string
	unit string
}{
	{id: "requests.total", unit: "requests"},
	{id: "requests.allowed", unit: "requests"},
	{id: "requests.denied", unit: "requests"},
	{id: "requests.outage", unit: "requests"},
	{id: "requests.outage_allowed", unit: "requests"},
	{id: "requests.outage_denied", unit: "requests"},
	{id: "requests.degraded", unit: "requests"},
	{id: "decisions.errors", unit: "decisions"},
	{id: "quota.nominal_limit", unit: "tokens"},
	{id: "quota.overshoot", unit: "tokens"},
}

func TestScenarioBuildersRejectUnknownImplementations(t *testing.T) {
	for _, implementationID := range []string{"", "unknown", "Shared-Token-Bucket"} {
		if _, err := BuildGlobalQuotaRunSpec(implementationID); err == nil {
			t.Errorf("BuildGlobalQuotaRunSpec(%q) error = nil", implementationID)
		}
		if _, err := BuildOutagePolicyRunSpec(implementationID); err == nil {
			t.Errorf("BuildOutagePolicyRunSpec(%q) error = nil", implementationID)
		}
	}
}

func TestGlobalQuotaRunSpecFreezesIdentityParametersAndSchedule(t *testing.T) {
	wantParameters := map[string]int64{
		"capacity":          4,
		"refill_every_ns":   int64(10 * time.Second),
		"tenant_count":      1,
		"node_count":        2,
		"requests_per_node": 4,
		"request_amount":    1,
	}
	wantEvents := []eventProjection{
		{At: 0, Phase: harness.PhaseRequest, Sequence: 0, Name: "request-a-1"},
		{At: 0, Phase: harness.PhaseRequest, Sequence: 1, Name: "request-b-1"},
		{At: 0, Phase: harness.PhaseRequest, Sequence: 2, Name: "request-a-2"},
		{At: 0, Phase: harness.PhaseRequest, Sequence: 3, Name: "request-b-2"},
		{At: 0, Phase: harness.PhaseRequest, Sequence: 4, Name: "request-a-3"},
		{At: 0, Phase: harness.PhaseRequest, Sequence: 5, Name: "request-b-3"},
		{At: 0, Phase: harness.PhaseRequest, Sequence: 6, Name: "request-a-4"},
		{At: 0, Phase: harness.PhaseRequest, Sequence: 7, Name: "request-b-4"},
		{At: 0, Phase: harness.PhaseObserve, Sequence: 0, Name: "observe-quota"},
	}
	wantAssertions := []string{
		"all-requests-decided",
		"expected-allowed-count",
		"expected-global-quota-overshoot",
		"no-unexpected-errors",
	}
	for _, implementationID := range []string{"shared-token-bucket", "per-node-token-bucket"} {
		t.Run(implementationID, func(t *testing.T) {
			spec, err := BuildGlobalQuotaRunSpec(implementationID)
			if err != nil {
				t.Fatal(err)
			}
			assertScenarioIdentity(t, spec, "per-node-vs-shared-quota", "distributed-rate-limiter-global-quota", "distributed-rate-limiter-per-node-multiplies-global-quota", implementationID)
			if !reflect.DeepEqual(spec.Parameters, wantParameters) {
				t.Fatalf("parameters = %#v, want %#v", spec.Parameters, wantParameters)
			}
			if got := projectEvents(spec.Events); !reflect.DeepEqual(got, wantEvents) {
				t.Fatalf("events = %#v, want %#v", got, wantEvents)
			}
			if got := assertionIDs(spec.Assertions); !reflect.DeepEqual(got, wantAssertions) {
				t.Fatalf("assertions = %#v, want %#v", got, wantAssertions)
			}
		})
	}
}

func TestOutagePolicyRunSpecFreezesIdentityParametersAndBoundaryOrdering(t *testing.T) {
	wantParameters := map[string]int64{
		"capacity":                   2,
		"refill_every_ns":            int64(10 * time.Second),
		"tenant_count":               1,
		"healthy_request_count":      2,
		"outage_request_count":       4,
		"request_amount":             1,
		"outage_first_request_at_ns": int64(100 * time.Millisecond),
		"outage_request_interval_ns": int64(100 * time.Millisecond),
	}
	wantEvents := []eventProjection{
		{At: 0, Phase: harness.PhaseRequest, Sequence: 0, Name: "healthy-request-1"},
		{At: 0, Phase: harness.PhaseRequest, Sequence: 1, Name: "healthy-request-2"},
		{At: 100 * time.Millisecond, Phase: harness.PhaseFault, Sequence: 0, Name: "coordinator-down"},
		{At: 100 * time.Millisecond, Phase: harness.PhaseRequest, Sequence: 0, Name: "outage-request-1"},
		{At: 200 * time.Millisecond, Phase: harness.PhaseRequest, Sequence: 0, Name: "outage-request-2"},
		{At: 300 * time.Millisecond, Phase: harness.PhaseRequest, Sequence: 0, Name: "outage-request-3"},
		{At: 400 * time.Millisecond, Phase: harness.PhaseRequest, Sequence: 0, Name: "outage-request-4"},
		{At: 900 * time.Millisecond, Phase: harness.PhaseFault, Sequence: 0, Name: "coordinator-up"},
		{At: 900 * time.Millisecond, Phase: harness.PhaseObserve, Sequence: 0, Name: "observe-quota"},
	}
	wantAssertions := []string{
		"all-requests-decided",
		"expected-outage-decision",
		"expected-outage-availability",
		"expected-quota-overshoot",
		"no-unexpected-errors",
	}
	for _, implementationID := range []string{"shared-fail-closed", "shared-fail-open"} {
		t.Run(implementationID, func(t *testing.T) {
			spec, err := BuildOutagePolicyRunSpec(implementationID)
			if err != nil {
				t.Fatal(err)
			}
			assertScenarioIdentity(t, spec, "coordinator-outage-policy", "distributed-rate-limiter-outage-policy", "distributed-rate-limiter-outage-policy-trades-availability-for-quota", implementationID)
			if !reflect.DeepEqual(spec.Parameters, wantParameters) {
				t.Fatalf("parameters = %#v, want %#v", spec.Parameters, wantParameters)
			}
			if got := projectEvents(spec.Events); !reflect.DeepEqual(got, wantEvents) {
				t.Fatalf("events = %#v, want %#v", got, wantEvents)
			}
			if got := assertionIDs(spec.Assertions); !reflect.DeepEqual(got, wantAssertions) {
				t.Fatalf("assertions = %#v, want %#v", got, wantAssertions)
			}
			for _, event := range spec.Events {
				if event.At == 900*time.Millisecond && event.Phase == harness.PhaseRequest {
					t.Fatalf("unexpected post-recovery request: %#v", event)
				}
			}
		})
	}
}

func TestScenarioGoldenMetricsIncludeExactZerosAndUnits(t *testing.T) {
	tests := []struct {
		name             string
		builder          func(string) (harness.RunSpec, error)
		implementationID string
		want             map[string]int64
	}{
		{
			name:             "global-shared",
			builder:          BuildGlobalQuotaRunSpec,
			implementationID: "shared-token-bucket",
			want:             metricValues(8, 4, 4, 0, 0, 0, 0, 0, 4, 0),
		},
		{
			name:             "global-per-node",
			builder:          BuildGlobalQuotaRunSpec,
			implementationID: "per-node-token-bucket",
			want:             metricValues(8, 8, 0, 0, 0, 0, 0, 0, 4, 4),
		},
		{
			name:             "outage-fail-closed",
			builder:          BuildOutagePolicyRunSpec,
			implementationID: "shared-fail-closed",
			want:             metricValues(6, 2, 4, 4, 0, 4, 4, 0, 2, 0),
		},
		{
			name:             "outage-fail-open",
			builder:          BuildOutagePolicyRunSpec,
			implementationID: "shared-fail-open",
			want:             metricValues(6, 6, 0, 4, 4, 0, 4, 0, 2, 4),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec, err := test.builder(test.implementationID)
			if err != nil {
				t.Fatal(err)
			}
			result, err := harness.NewRunner().Run(context.Background(), spec)
			if err != nil {
				t.Fatalf("Run() error = %v; result = %#v", err, result)
			}
			if result.Status != harness.StatusPassed || result.EventsExecuted != 9 {
				t.Fatalf("status/events = %s/%d", result.Status, result.EventsExecuted)
			}
			if len(result.Metrics) != len(scenarioMetricContract) {
				t.Fatalf("metrics = %#v", result.Metrics)
			}
			got := make(map[string]int64, len(result.Metrics))
			units := make(map[string]string, len(result.Metrics))
			for _, metric := range result.Metrics {
				got[metric.Name] = metric.Value
				units[metric.Name] = metric.Unit
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("metric values = %#v, want %#v", got, test.want)
			}
			for _, metric := range scenarioMetricContract {
				if units[metric.id] != metric.unit {
					t.Errorf("metric %q unit = %q, want %q", metric.id, units[metric.id], metric.unit)
				}
			}
			for _, assertion := range result.Assertions {
				if !assertion.Passed {
					t.Errorf("assertion = %#v", assertion)
				}
			}
		})
	}
}

func TestScenarioBuildersReturnFreshIndependentState(t *testing.T) {
	builders := []struct {
		name             string
		builder          func(string) (harness.RunSpec, error)
		implementationID string
	}{
		{name: "global", builder: BuildGlobalQuotaRunSpec, implementationID: "shared-token-bucket"},
		{name: "outage", builder: BuildOutagePolicyRunSpec, implementationID: "shared-fail-open"},
	}
	for _, test := range builders {
		t.Run(test.name, func(t *testing.T) {
			first, err := test.builder(test.implementationID)
			if err != nil {
				t.Fatal(err)
			}
			second, err := test.builder(test.implementationID)
			if err != nil {
				t.Fatal(err)
			}
			first.Parameters["capacity"] = 999
			first.Events[0].Name = "mutated"
			first.Assertions[0].ID = "mutated"
			if second.Parameters["capacity"] == 999 || second.Events[0].Name == "mutated" || second.Assertions[0].ID == "mutated" {
				t.Fatalf("builder values alias: %#v", second)
			}
			for index := 0; index < 2; index++ {
				spec, buildErr := test.builder(test.implementationID)
				if buildErr != nil {
					t.Fatal(buildErr)
				}
				result, runErr := harness.NewRunner().Run(context.Background(), spec)
				if runErr != nil || result.Status != harness.StatusPassed {
					t.Fatalf("fresh run %d = %#v, %v", index, result, runErr)
				}
			}
		})
	}
}

func TestScenarioProjectionIsDeterministicAcrossBuilds(t *testing.T) {
	builders := []struct {
		builder          func(string) (harness.RunSpec, error)
		implementationID string
	}{
		{builder: BuildGlobalQuotaRunSpec, implementationID: "per-node-token-bucket"},
		{builder: BuildOutagePolicyRunSpec, implementationID: "shared-fail-closed"},
	}
	for _, test := range builders {
		var first []byte
		for index := 0; index < 25; index++ {
			spec, err := test.builder(test.implementationID)
			if err != nil {
				t.Fatal(err)
			}
			projection, err := json.Marshal(projectRunSpec(spec))
			if err != nil {
				t.Fatal(err)
			}
			if index == 0 {
				first = projection
				continue
			}
			if string(projection) != string(first) {
				t.Fatalf("projection %d = %s, want %s", index, projection, first)
			}
		}
	}
}

type eventProjection struct {
	At       time.Duration `json:"at"`
	Phase    harness.Phase `json:"phase"`
	Sequence uint64        `json:"sequence"`
	Name     string        `json:"name"`
}

type runProjection struct {
	LabID            string           `json:"lab_id"`
	RequiredRunID    string           `json:"required_run_id"`
	BindingID        string           `json:"binding_id"`
	ClaimID          string           `json:"claim_id"`
	ImplementationID string           `json:"implementation_id"`
	AdapterID        string           `json:"adapter_id"`
	Seed             int64            `json:"seed"`
	Start            time.Time        `json:"start"`
	Deadline         time.Duration    `json:"deadline"`
	Parameters       map[string]int64 `json:"parameters"`
	Events           []eventProjection
	Assertions       []string
}

func assertScenarioIdentity(t *testing.T, spec harness.RunSpec, runID, bindingID, claimID, implementationID string) {
	t.Helper()
	if spec.LabID != "distributed-rate-limiter" || spec.RequiredRunID != runID || spec.BindingID != bindingID || spec.ClaimID != claimID || spec.ImplementationID != implementationID || spec.AdapterID != "" {
		t.Fatalf("identity = %#v", projectRunSpec(spec))
	}
	if spec.Seed != 1 || !spec.Start.Equal(time.Unix(0, 0).UTC()) || spec.Start.Location() != time.UTC || spec.Deadline != 2*time.Second {
		t.Fatalf("seed/start/deadline = %d/%v/%s", spec.Seed, spec.Start, spec.Deadline)
	}
}

func projectRunSpec(spec harness.RunSpec) runProjection {
	return runProjection{
		LabID:            spec.LabID,
		RequiredRunID:    spec.RequiredRunID,
		BindingID:        spec.BindingID,
		ClaimID:          spec.ClaimID,
		ImplementationID: spec.ImplementationID,
		AdapterID:        spec.AdapterID,
		Seed:             spec.Seed,
		Start:            spec.Start,
		Deadline:         spec.Deadline,
		Parameters:       spec.Parameters,
		Events:           projectEvents(spec.Events),
		Assertions:       assertionIDs(spec.Assertions),
	}
}

func projectEvents(events []harness.Event) []eventProjection {
	projected := make([]eventProjection, len(events))
	for index, event := range events {
		projected[index] = eventProjection{At: event.At, Phase: event.Phase, Sequence: event.Sequence, Name: event.Name}
		if event.Apply == nil {
			panic("nil event action")
		}
	}
	return projected
}

func assertionIDs(assertions []harness.Assertion) []string {
	ids := make([]string, len(assertions))
	for index, assertion := range assertions {
		ids[index] = assertion.ID
		if assertion.Check == nil {
			panic("nil assertion check")
		}
	}
	return ids
}

func metricValues(total, allowed, denied, outage, outageAllowed, outageDenied, degraded, errorsFound, nominal, overshoot int64) map[string]int64 {
	return map[string]int64{
		"requests.total":          total,
		"requests.allowed":        allowed,
		"requests.denied":         denied,
		"requests.outage":         outage,
		"requests.outage_allowed": outageAllowed,
		"requests.outage_denied":  outageDenied,
		"requests.degraded":       degraded,
		"decisions.errors":        errorsFound,
		"quota.nominal_limit":     nominal,
		"quota.overshoot":         overshoot,
	}
}
