package experiments

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
	"github.com/PinoHouse/works-on-my-whiteboard/labs/harness"
	tokenbucket "github.com/PinoHouse/works-on-my-whiteboard/labs/primitives/token-bucket"
	distributedratelimiter "github.com/PinoHouse/works-on-my-whiteboard/labs/scenarios/distributed-rate-limiter"
)

type Profile string

const (
	ProfileSmoke Profile = "smoke"
	ProfileDeep  Profile = "deep"
)

type Workload struct {
	ID         string
	Parameters map[string]int64
}

type Fault struct {
	ID       string
	At       time.Duration
	Duration time.Duration
}

type MetricSpec struct {
	ID   string
	Unit string
}

type Definition struct {
	Spec         harness.RunSpec
	Profile      Profile
	Hypothesis   string
	Workload     Workload
	Faults       []Fault
	Measurements []MetricSpec
	Limitations  []string
	Conclude     func(harness.RunResult) string
}

type Factory func(validator.MatrixCell, Profile) (Definition, error)

type registryKey struct {
	labID            string
	requiredRunID    string
	implementationID string
	adapterID        string
}

type runBuilder func(string) (harness.RunSpec, error)

type registryEntry struct {
	cell         validator.MatrixCell
	builder      runBuilder
	faults       []Fault
	measurements []MetricSpec
	hypothesis   string
	limitations  []string
}

func Lookup(cell validator.MatrixCell) (Factory, bool) {
	registry, err := newRegistry(defaultRegistryEntries())
	if err != nil {
		return nil, false
	}
	factory, exists := registry[keyForCell(cell)]
	return factory, exists
}

func newRegistry(entries []registryEntry) (map[registryKey]Factory, error) {
	factories := make(map[registryKey]Factory, len(entries))
	for _, entry := range entries {
		key := keyForCell(entry.cell)
		if _, exists := factories[key]; exists {
			return nil, fmt.Errorf("duplicate experiment registry key %#v", key)
		}
		factories[key] = nil
	}
	for _, entry := range entries {
		if entry.builder == nil {
			return nil, fmt.Errorf("experiment registry entry %#v has nil builder", keyForCell(entry.cell))
		}
		if strings.TrimSpace(entry.hypothesis) == "" {
			return nil, fmt.Errorf("experiment registry entry %#v has blank hypothesis", keyForCell(entry.cell))
		}
		if entry.faults == nil || entry.measurements == nil || entry.limitations == nil {
			return nil, fmt.Errorf("experiment registry entry %#v has nil metadata", keyForCell(entry.cell))
		}
		seenMeasurements := make(map[string]struct{}, len(entry.measurements))
		for _, measurement := range entry.measurements {
			if strings.TrimSpace(measurement.ID) == "" || strings.TrimSpace(measurement.Unit) == "" {
				return nil, fmt.Errorf("experiment registry entry %#v has blank measurement identity %#v", keyForCell(entry.cell), measurement)
			}
			if _, exists := seenMeasurements[measurement.ID]; exists {
				return nil, fmt.Errorf("experiment registry entry %#v repeats measurement %q", keyForCell(entry.cell), measurement.ID)
			}
			seenMeasurements[measurement.ID] = struct{}{}
		}
		if !reflect.DeepEqual(registryFaultIDs(entry.faults), entry.cell.Faults) {
			return nil, fmt.Errorf("experiment registry entry %#v fault metadata differs from cell", keyForCell(entry.cell))
		}
		factories[keyForCell(entry.cell)] = factoryForEntry(entry)
	}
	return factories, nil
}

func factoryForEntry(source registryEntry) Factory {
	entry := cloneRegistryEntry(source)
	return func(cell validator.MatrixCell, profile Profile) (Definition, error) {
		if profile != ProfileSmoke && profile != ProfileDeep {
			return Definition{}, fmt.Errorf("unknown experiment profile %q", profile)
		}
		if !reflect.DeepEqual(cell, entry.cell) {
			return Definition{}, fmt.Errorf("matrix cell %#v differs from registered cell %#v", cell, entry.cell)
		}
		spec, err := entry.builder(entry.cell.ImplementationID)
		if err != nil {
			return Definition{}, fmt.Errorf("build experiment run: %w", err)
		}
		if err := validateBuiltSpec(spec, cell); err != nil {
			return Definition{}, err
		}
		claimID := entry.cell.ClaimID
		conclude := func(result harness.RunResult) string {
			if result.Status == harness.StatusPassed {
				return fmt.Sprintf("passed: evidence supports claim %s for the frozen workload", claimID)
			}
			return fmt.Sprintf("failed: evidence does not support claim %s for the frozen workload", claimID)
		}
		return Definition{
			Spec:       spec,
			Profile:    profile,
			Hypothesis: entry.hypothesis,
			Workload: Workload{
				ID:         entry.cell.Workload,
				Parameters: cloneParameters(spec.Parameters),
			},
			Faults:       append([]Fault{}, entry.faults...),
			Measurements: append([]MetricSpec{}, entry.measurements...),
			Limitations:  append([]string{}, entry.limitations...),
			Conclude:     conclude,
		}, nil
	}
}

func validateBuiltSpec(spec harness.RunSpec, cell validator.MatrixCell) error {
	identity := [6]string{spec.LabID, spec.RequiredRunID, spec.BindingID, spec.ClaimID, spec.ImplementationID, spec.AdapterID}
	wantIdentity := [6]string{cell.LabID, cell.RequiredRunID, cell.BindingID, cell.ClaimID, cell.ImplementationID, cell.AdapterID}
	if identity != wantIdentity {
		return fmt.Errorf("built run identity %v differs from matrix cell %v", identity, wantIdentity)
	}
	assertions := make([]string, len(spec.Assertions))
	for index, assertion := range spec.Assertions {
		assertions[index] = assertion.ID
	}
	if !reflect.DeepEqual(assertions, cell.AssertionIDs) {
		return fmt.Errorf("built assertion IDs %v differ from matrix cell %v", assertions, cell.AssertionIDs)
	}
	return nil
}

func defaultRegistryEntries() []registryEntry {
	primitiveMeasurements := []MetricSpec{
		{ID: "requests.total", Unit: "count"},
		{ID: "requests.allowed", Unit: "count"},
		{ID: "requests.denied", Unit: "count"},
		{ID: "tokens.capacity", Unit: "tokens"},
		{ID: "tokens.remaining", Unit: "tokens"},
		{ID: "tokens.max_observed", Unit: "tokens"},
		{ID: "probe.initial_burst_allowed", Unit: "count"},
		{ID: "probe.immediate_denied", Unit: "count"},
		{ID: "probe.pre_boundary_denied", Unit: "count"},
		{ID: "probe.boundary_allowed", Unit: "count"},
		{ID: "reference.mismatches", Unit: "count"},
	}
	scenarioMeasurements := []MetricSpec{
		{ID: "requests.total", Unit: "requests"},
		{ID: "requests.allowed", Unit: "requests"},
		{ID: "requests.denied", Unit: "requests"},
		{ID: "requests.outage", Unit: "requests"},
		{ID: "requests.outage_allowed", Unit: "requests"},
		{ID: "requests.outage_denied", Unit: "requests"},
		{ID: "requests.degraded", Unit: "requests"},
		{ID: "decisions.errors", Unit: "decisions"},
		{ID: "quota.nominal_limit", Unit: "tokens"},
		{ID: "quota.overshoot", Unit: "tokens"},
	}
	primitiveAssertions := []string{
		"initial-burst-bounded",
		"pre-boundary-denied",
		"boundary-refills-one",
		"capacity-never-exceeded",
		"implementation-matches-reference",
	}
	globalAssertions := []string{
		"all-requests-decided",
		"expected-allowed-count",
		"expected-global-quota-overshoot",
		"no-unexpected-errors",
	}
	outageAssertions := []string{
		"all-requests-decided",
		"expected-outage-decision",
		"expected-outage-availability",
		"expected-quota-overshoot",
		"no-unexpected-errors",
	}
	return []registryEntry{
		{
			cell: validator.MatrixCell{
				LabID: "token-bucket", RequiredRunID: "burst-and-refill-boundary", BindingID: "token-bucket-burst-boundary", ClaimID: "token-bucket-bounds-burst-and-average-rate", Role: "baseline", ImplementationID: "token-bucket-reference-model", AdapterID: "", Workload: "burst-refill-boundary", Faults: []string{}, AssertionIDs: append([]string{}, primitiveAssertions...),
			},
			builder: tokenbucket.BuildRunSpec, faults: []Fault{}, measurements: append([]MetricSpec{}, primitiveMeasurements...),
			hypothesis:  "The reference model will allow the initial four-token burst, deny before refill, and allow exactly at the refill boundary.",
			limitations: []string{"The deterministic boundary workload does not model scheduler jitter or distributed coordination."},
		},
		{
			cell: validator.MatrixCell{
				LabID: "token-bucket", RequiredRunID: "burst-and-refill-boundary", BindingID: "token-bucket-burst-boundary", ClaimID: "token-bucket-bounds-burst-and-average-rate", Role: "variant", ImplementationID: "token-bucket", AdapterID: "", Workload: "burst-refill-boundary", Faults: []string{}, AssertionIDs: append([]string{}, primitiveAssertions...),
			},
			builder: tokenbucket.BuildRunSpec, faults: []Fault{}, measurements: append([]MetricSpec{}, primitiveMeasurements...),
			hypothesis:  "The token bucket implementation will match the reference model at the initial, pre-boundary, and refill-boundary probes.",
			limitations: []string{"The deterministic boundary workload does not model scheduler jitter or distributed coordination."},
		},
		{
			cell: validator.MatrixCell{
				LabID: "distributed-rate-limiter", RequiredRunID: "per-node-vs-shared-quota", BindingID: "distributed-rate-limiter-global-quota", ClaimID: "distributed-rate-limiter-per-node-multiplies-global-quota", Role: "baseline", ImplementationID: "shared-token-bucket", AdapterID: "", Workload: "two-node-burst", Faults: []string{}, AssertionIDs: append([]string{}, globalAssertions...),
			},
			builder: distributedratelimiter.BuildGlobalQuotaRunSpec, faults: []Fault{}, measurements: append([]MetricSpec{}, scenarioMeasurements...),
			hypothesis:  "A shared capacity-four tenant bucket will allow exactly four of eight simultaneous unit requests and will not overshoot the global quota.",
			limitations: []string{"The frozen two-node burst isolates quota conservation and does not measure coordinator latency."},
		},
		{
			cell: validator.MatrixCell{
				LabID: "distributed-rate-limiter", RequiredRunID: "per-node-vs-shared-quota", BindingID: "distributed-rate-limiter-global-quota", ClaimID: "distributed-rate-limiter-per-node-multiplies-global-quota", Role: "variant", ImplementationID: "per-node-token-bucket", AdapterID: "", Workload: "two-node-burst", Faults: []string{}, AssertionIDs: append([]string{}, globalAssertions...),
			},
			builder: distributedratelimiter.BuildGlobalQuotaRunSpec, faults: []Fault{}, measurements: append([]MetricSpec{}, scenarioMeasurements...),
			hypothesis:  "Two independent capacity-four node buckets will allow eight simultaneous unit requests and overshoot the nominal tenant quota by four.",
			limitations: []string{"The frozen two-node burst isolates quota multiplication and does not model hierarchical allocation."},
		},
		{
			cell: validator.MatrixCell{
				LabID: "distributed-rate-limiter", RequiredRunID: "coordinator-outage-policy", BindingID: "distributed-rate-limiter-outage-policy", ClaimID: "distributed-rate-limiter-outage-policy-trades-availability-for-quota", Role: "baseline", ImplementationID: "shared-fail-closed", AdapterID: "", Workload: "coordinator-outage", Faults: []string{"coordinator-unavailable"}, AssertionIDs: append([]string{}, outageAssertions...),
			},
			builder: distributedratelimiter.BuildOutagePolicyRunSpec,
			faults:  []Fault{{ID: "coordinator-unavailable", At: 100 * time.Millisecond, Duration: 800 * time.Millisecond}}, measurements: append([]MetricSpec{}, scenarioMeasurements...),
			hypothesis:  "Fail-closed will deny all four outage requests, preserve quota, and expose every outage decision as degraded.",
			limitations: []string{"Coordinator behavior after restoration is not probed; post-recovery state is intentionally outside this run."},
		},
		{
			cell: validator.MatrixCell{
				LabID: "distributed-rate-limiter", RequiredRunID: "coordinator-outage-policy", BindingID: "distributed-rate-limiter-outage-policy", ClaimID: "distributed-rate-limiter-outage-policy-trades-availability-for-quota", Role: "variant", ImplementationID: "shared-fail-open", AdapterID: "", Workload: "coordinator-outage", Faults: []string{"coordinator-unavailable"}, AssertionIDs: append([]string{}, outageAssertions...),
			},
			builder: distributedratelimiter.BuildOutagePolicyRunSpec,
			faults:  []Fault{{ID: "coordinator-unavailable", At: 100 * time.Millisecond, Duration: 800 * time.Millisecond}}, measurements: append([]MetricSpec{}, scenarioMeasurements...),
			hypothesis:  "Fail-open will allow all four outage requests, increase availability, overshoot nominal quota by four, and mark every outage decision degraded.",
			limitations: []string{"Coordinator behavior after restoration is not probed; post-recovery state is intentionally outside this run."},
		},
	}
}

func cloneRegistryEntry(source registryEntry) registryEntry {
	cloned := source
	cloned.cell.Faults = append([]string{}, source.cell.Faults...)
	cloned.cell.AssertionIDs = append([]string{}, source.cell.AssertionIDs...)
	cloned.faults = append([]Fault{}, source.faults...)
	cloned.measurements = append([]MetricSpec{}, source.measurements...)
	cloned.limitations = append([]string{}, source.limitations...)
	return cloned
}

func cloneParameters(source map[string]int64) map[string]int64 {
	cloned := make(map[string]int64, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func keyForCell(cell validator.MatrixCell) registryKey {
	return registryKey{
		labID:            cell.LabID,
		requiredRunID:    cell.RequiredRunID,
		implementationID: cell.ImplementationID,
		adapterID:        cell.AdapterID,
	}
}

func registryFaultIDs(faults []Fault) []string {
	ids := make([]string, len(faults))
	for index, fault := range faults {
		ids[index] = fault.ID
	}
	return ids
}
