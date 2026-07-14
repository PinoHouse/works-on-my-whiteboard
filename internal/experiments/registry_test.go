package experiments

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
	"github.com/PinoHouse/works-on-my-whiteboard/labs/harness"
)

var primitiveMeasurementContract = []MetricSpec{
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

var scenarioMeasurementContract = []MetricSpec{
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

func TestNewRegistryRejectsDuplicateExactKeys(t *testing.T) {
	cell := expectedMatrixCells()[0]
	entry := registryEntry{cell: cell}
	if _, err := newRegistry([]registryEntry{entry, entry}); err == nil {
		t.Fatal("newRegistry(duplicate keys) error = nil")
	}
}

func TestNewRegistryRejectsInvalidMeasurementContracts(t *testing.T) {
	tests := []struct {
		name         string
		measurements []MetricSpec
	}{
		{name: "blank-id", measurements: []MetricSpec{{ID: " ", Unit: "count"}}},
		{name: "blank-unit", measurements: []MetricSpec{{ID: "requests.total", Unit: " "}}},
		{name: "duplicate-id", measurements: []MetricSpec{{ID: "requests.total", Unit: "count"}, {ID: "requests.total", Unit: "requests"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := defaultRegistryEntries()[0]
			entry.measurements = test.measurements
			if _, err := newRegistry([]registryEntry{entry}); err == nil {
				t.Fatalf("newRegistry(%#v) error = nil", test.measurements)
			}
		})
	}
}

func TestRegistryIsExactBijectionWithRealRequiredMatrix(t *testing.T) {
	repository, err := catalog.LoadDir(context.Background(), repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	matrix, diagnostics := validator.BuildRequiredMatrix(repository)
	if len(diagnostics) != 0 {
		t.Fatalf("matrix diagnostics = %#v", diagnostics)
	}
	want := expectedMatrixCells()
	if !reflect.DeepEqual(matrix, want) {
		t.Fatalf("matrix = %#v, want %#v", matrix, want)
	}
	for _, cell := range matrix {
		if _, ok := Lookup(cell); !ok {
			t.Errorf("Lookup(%#v) = false", cell)
		}
	}
	if _, ok := Lookup(validator.MatrixCell{LabID: "unknown", RequiredRunID: "unknown", ImplementationID: "unknown"}); ok {
		t.Fatal("Lookup(unknown) = true")
	}
}

func TestEveryRegistryDefinitionExecutesAndClosesManifestContracts(t *testing.T) {
	repository, err := catalog.LoadDir(context.Background(), repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, cell := range expectedMatrixCells() {
		factory, ok := Lookup(cell)
		if !ok {
			t.Fatalf("Lookup(%#v) = false", cell)
		}
		for _, profile := range []Profile{ProfileSmoke, ProfileDeep} {
			t.Run(cell.LabID+"/"+cell.RequiredRunID+"/"+cell.ImplementationID+"/"+string(profile), func(t *testing.T) {
				definition, err := factory(cell, profile)
				if err != nil {
					t.Fatal(err)
				}
				assertDefinitionContract(t, repository, cell, profile, definition)
				result, err := harness.NewRunner().Run(context.Background(), definition.Spec)
				if err != nil {
					t.Fatalf("Run() error = %v; result = %#v", err, result)
				}
				if result.Status != harness.StatusPassed {
					t.Fatalf("result status = %q", result.Status)
				}
				assertMeasurementsMatchResult(t, definition.Measurements, result.Metrics)
			})
		}
	}
}

func TestFactoryRejectsDriftInEveryMatrixCellField(t *testing.T) {
	base := expectedMatrixCells()[4]
	factory, ok := Lookup(base)
	if !ok {
		t.Fatal("Lookup(base) = false")
	}
	mutations := []struct {
		name   string
		mutate func(*validator.MatrixCell)
	}{
		{name: "lab", mutate: func(cell *validator.MatrixCell) { cell.LabID = "other-lab" }},
		{name: "run", mutate: func(cell *validator.MatrixCell) { cell.RequiredRunID = "other-run" }},
		{name: "binding", mutate: func(cell *validator.MatrixCell) { cell.BindingID = "other-binding" }},
		{name: "claim", mutate: func(cell *validator.MatrixCell) { cell.ClaimID = "other-claim" }},
		{name: "role", mutate: func(cell *validator.MatrixCell) {
			if cell.Role == "baseline" {
				cell.Role = "variant"
			} else {
				cell.Role = "baseline"
			}
		}},
		{name: "implementation", mutate: func(cell *validator.MatrixCell) { cell.ImplementationID = "other-implementation" }},
		{name: "adapter", mutate: func(cell *validator.MatrixCell) { cell.AdapterID = "other-adapter" }},
		{name: "workload", mutate: func(cell *validator.MatrixCell) { cell.Workload = "other-workload" }},
		{name: "fault-content", mutate: func(cell *validator.MatrixCell) { cell.Faults = []string{"other-fault"} }},
		{name: "fault-order", mutate: func(cell *validator.MatrixCell) { cell.Faults = []string{"coordinator-unavailable", "other-fault"} }},
		{name: "fault-nil", mutate: func(cell *validator.MatrixCell) { cell.Faults = nil }},
		{name: "assertion-content", mutate: func(cell *validator.MatrixCell) { cell.AssertionIDs[0] = "other-assertion" }},
		{name: "assertion-order", mutate: func(cell *validator.MatrixCell) {
			cell.AssertionIDs[0], cell.AssertionIDs[1] = cell.AssertionIDs[1], cell.AssertionIDs[0]
		}},
		{name: "assertion-nil", mutate: func(cell *validator.MatrixCell) { cell.AssertionIDs = nil }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			cell := cloneCell(base)
			mutation.mutate(&cell)
			if _, err := factory(cell, ProfileSmoke); err == nil {
				t.Fatalf("factory(%#v) error = nil", cell)
			}
		})
	}
}

func TestFactoryRejectsUnknownProfiles(t *testing.T) {
	cell := expectedMatrixCells()[0]
	factory, ok := Lookup(cell)
	if !ok {
		t.Fatal("Lookup(cell) = false")
	}
	for _, profile := range []Profile{"", "fast", "SMOKE"} {
		if _, err := factory(cell, profile); err == nil {
			t.Errorf("factory(profile=%q) error = nil", profile)
		}
	}
}

func TestDefinitionsAreFreshAndAliasIsolated(t *testing.T) {
	for _, cell := range expectedMatrixCells() {
		factory, ok := Lookup(cell)
		if !ok {
			t.Fatal("Lookup(cell) = false")
		}
		first, err := factory(cell, ProfileSmoke)
		if err != nil {
			t.Fatal(err)
		}
		first.Spec.Parameters["capacity"] = 999
		first.Workload.Parameters["capacity"] = 998
		first.Spec.Events[0].Name = "mutated"
		first.Spec.Assertions[0].ID = "mutated"
		first.Faults = append(first.Faults, Fault{ID: "mutated"})
		first.Measurements[0].ID = "mutated"
		if len(first.Limitations) == 0 {
			first.Limitations = append(first.Limitations, "mutated")
		} else {
			first.Limitations[0] = "mutated"
		}

		second, err := factory(cell, ProfileSmoke)
		if err != nil {
			t.Fatal(err)
		}
		if second.Spec.Parameters["capacity"] == 999 || second.Workload.Parameters["capacity"] == 998 || second.Spec.Events[0].Name == "mutated" || second.Spec.Assertions[0].ID == "mutated" || second.Measurements[0].ID == "mutated" {
			t.Fatalf("definition aliases prior call: %#v", second)
		}
		if len(second.Faults) != len(cell.Faults) {
			t.Fatalf("faults = %#v, want %d", second.Faults, len(cell.Faults))
		}
		if len(second.Limitations) == 0 || second.Limitations[0] == "mutated" {
			t.Fatalf("limitations = %#v", second.Limitations)
		}
		result, runErr := harness.NewRunner().Run(context.Background(), second.Spec)
		if runErr != nil || result.Status != harness.StatusPassed {
			t.Fatalf("fresh definition run = %#v, %v", result, runErr)
		}
	}
}

func TestConclusionsAreDeterministicNonblankForPassedAndFailed(t *testing.T) {
	for _, cell := range expectedMatrixCells() {
		factory, ok := Lookup(cell)
		if !ok {
			t.Fatal("Lookup(cell) = false")
		}
		definition, err := factory(cell, ProfileSmoke)
		if err != nil {
			t.Fatal(err)
		}
		passed := definition.Conclude(harness.RunResult{Status: harness.StatusPassed})
		passedAgain := definition.Conclude(harness.RunResult{Status: harness.StatusPassed})
		failed := definition.Conclude(harness.RunResult{Status: harness.StatusFailed})
		failedAgain := definition.Conclude(harness.RunResult{Status: harness.StatusFailed})
		if strings.TrimSpace(passed) == "" || strings.TrimSpace(failed) == "" || passed != passedAgain || failed != failedAgain || passed == failed {
			t.Fatalf("conclusions passed=%q/%q failed=%q/%q", passed, passedAgain, failed, failedAgain)
		}
	}
}

func assertDefinitionContract(t *testing.T, repository *catalog.Catalog, cell validator.MatrixCell, profile Profile, definition Definition) {
	t.Helper()
	if definition.Profile != profile {
		t.Errorf("profile = %q, want %q", definition.Profile, profile)
	}
	spec := definition.Spec
	if spec.LabID != cell.LabID || spec.RequiredRunID != cell.RequiredRunID || spec.BindingID != cell.BindingID || spec.ClaimID != cell.ClaimID || spec.ImplementationID != cell.ImplementationID || spec.AdapterID != cell.AdapterID {
		t.Errorf("spec identity = %#v, cell = %#v", spec, cell)
	}
	if definition.Workload.ID != cell.Workload {
		t.Errorf("workload = %q, want %q", definition.Workload.ID, cell.Workload)
	}
	if !reflect.DeepEqual(spec.Parameters, definition.Workload.Parameters) {
		t.Errorf("parameter values differ: %#v / %#v", spec.Parameters, definition.Workload.Parameters)
	}
	definition.Workload.Parameters["alias-probe"] = 1
	if _, exists := spec.Parameters["alias-probe"]; exists {
		t.Error("Spec.Parameters aliases Workload.Parameters")
	}
	delete(definition.Workload.Parameters, "alias-probe")
	if got := faultIDs(definition.Faults); !reflect.DeepEqual(got, cell.Faults) {
		t.Errorf("fault IDs = %#v, want %#v", got, cell.Faults)
	}
	if got := runAssertionIDs(spec.Assertions); !reflect.DeepEqual(got, cell.AssertionIDs) {
		t.Errorf("assertion IDs = %#v, want %#v", got, cell.AssertionIDs)
	}
	if strings.TrimSpace(definition.Hypothesis) == "" || definition.Conclude == nil || definition.Limitations == nil || definition.Faults == nil || definition.Measurements == nil {
		t.Errorf("incomplete definition metadata = %#v", definition)
	}
	if cell.RequiredRunID == "coordinator-outage-policy" {
		wantFault := []Fault{{ID: "coordinator-unavailable", At: 100 * time.Millisecond, Duration: 800 * time.Millisecond}}
		if !reflect.DeepEqual(definition.Faults, wantFault) {
			t.Errorf("outage faults = %#v, want %#v", definition.Faults, wantFault)
		}
		joined := strings.Join(definition.Limitations, " ")
		if !strings.Contains(joined, "post-recovery") || !strings.Contains(joined, "not probed") {
			t.Errorf("outage limitations = %#v", definition.Limitations)
		}
	}
	wantMeasurements := scenarioMeasurementContract
	if cell.LabID == "token-bucket" {
		wantMeasurements = primitiveMeasurementContract
	}
	if !reflect.DeepEqual(definition.Measurements, wantMeasurements) {
		t.Errorf("measurements = %#v, want %#v", definition.Measurements, wantMeasurements)
	}
	manifest := repository.Labs[cell.LabID]
	if got := measurementIDs(definition.Measurements); !reflect.DeepEqual(got, manifest.Metrics) {
		t.Errorf("measurement IDs = %#v, manifest metrics = %#v", got, manifest.Metrics)
	}
}

func assertMeasurementsMatchResult(t *testing.T, measurements []MetricSpec, metrics []harness.Metric) {
	t.Helper()
	want := make(map[string]string, len(measurements))
	for _, measurement := range measurements {
		want[measurement.ID] = measurement.Unit
	}
	got := make(map[string]string, len(metrics))
	for _, metric := range metrics {
		got[metric.Name] = metric.Unit
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("result metric units = %#v, measurements = %#v", got, want)
	}
}

func expectedMatrixCells() []validator.MatrixCell {
	cells := []validator.MatrixCell{
		{
			LabID: "token-bucket", RequiredRunID: "burst-and-refill-boundary", BindingID: "token-bucket-burst-boundary", ClaimID: "token-bucket-bounds-burst-and-average-rate", Role: "baseline", ImplementationID: "token-bucket-reference-model", AdapterID: "", Workload: "burst-refill-boundary", Faults: []string{},
			AssertionIDs: []string{"initial-burst-bounded", "pre-boundary-denied", "boundary-refills-one", "capacity-never-exceeded", "implementation-matches-reference"},
		},
		{
			LabID: "token-bucket", RequiredRunID: "burst-and-refill-boundary", BindingID: "token-bucket-burst-boundary", ClaimID: "token-bucket-bounds-burst-and-average-rate", Role: "variant", ImplementationID: "token-bucket", AdapterID: "", Workload: "burst-refill-boundary", Faults: []string{},
			AssertionIDs: []string{"initial-burst-bounded", "pre-boundary-denied", "boundary-refills-one", "capacity-never-exceeded", "implementation-matches-reference"},
		},
		{
			LabID: "distributed-rate-limiter", RequiredRunID: "per-node-vs-shared-quota", BindingID: "distributed-rate-limiter-global-quota", ClaimID: "distributed-rate-limiter-per-node-multiplies-global-quota", Role: "baseline", ImplementationID: "shared-token-bucket", AdapterID: "", Workload: "two-node-burst", Faults: []string{},
			AssertionIDs: []string{"all-requests-decided", "expected-allowed-count", "expected-global-quota-overshoot", "no-unexpected-errors"},
		},
		{
			LabID: "distributed-rate-limiter", RequiredRunID: "per-node-vs-shared-quota", BindingID: "distributed-rate-limiter-global-quota", ClaimID: "distributed-rate-limiter-per-node-multiplies-global-quota", Role: "variant", ImplementationID: "per-node-token-bucket", AdapterID: "", Workload: "two-node-burst", Faults: []string{},
			AssertionIDs: []string{"all-requests-decided", "expected-allowed-count", "expected-global-quota-overshoot", "no-unexpected-errors"},
		},
		{
			LabID: "distributed-rate-limiter", RequiredRunID: "coordinator-outage-policy", BindingID: "distributed-rate-limiter-outage-policy", ClaimID: "distributed-rate-limiter-outage-policy-trades-availability-for-quota", Role: "baseline", ImplementationID: "shared-fail-closed", AdapterID: "", Workload: "coordinator-outage", Faults: []string{"coordinator-unavailable"},
			AssertionIDs: []string{"all-requests-decided", "expected-outage-decision", "expected-outage-availability", "expected-quota-overshoot", "no-unexpected-errors"},
		},
		{
			LabID: "distributed-rate-limiter", RequiredRunID: "coordinator-outage-policy", BindingID: "distributed-rate-limiter-outage-policy", ClaimID: "distributed-rate-limiter-outage-policy-trades-availability-for-quota", Role: "variant", ImplementationID: "shared-fail-open", AdapterID: "", Workload: "coordinator-outage", Faults: []string{"coordinator-unavailable"},
			AssertionIDs: []string{"all-requests-decided", "expected-outage-decision", "expected-outage-availability", "expected-quota-overshoot", "no-unexpected-errors"},
		},
	}
	sort.Slice(cells, func(left, right int) bool {
		leftKey := []string{cells[left].LabID, cells[left].RequiredRunID, cells[left].BindingID, cells[left].ClaimID, cells[left].ImplementationID, cells[left].AdapterID, cells[left].Role}
		rightKey := []string{cells[right].LabID, cells[right].RequiredRunID, cells[right].BindingID, cells[right].ClaimID, cells[right].ImplementationID, cells[right].AdapterID, cells[right].Role}
		for index := range leftKey {
			if leftKey[index] != rightKey[index] {
				return leftKey[index] < rightKey[index]
			}
		}
		return false
	})
	return cells
}

func cloneCell(source validator.MatrixCell) validator.MatrixCell {
	cloned := source
	cloned.Faults = append([]string{}, source.Faults...)
	cloned.AssertionIDs = append([]string{}, source.AssertionIDs...)
	return cloned
}

func faultIDs(faults []Fault) []string {
	ids := make([]string, len(faults))
	for index, fault := range faults {
		ids[index] = fault.ID
	}
	return ids
}

func runAssertionIDs(assertions []harness.Assertion) []string {
	ids := make([]string, len(assertions))
	for index, assertion := range assertions {
		ids[index] = assertion.ID
	}
	return ids
}

func measurementIDs(measurements []MetricSpec) []string {
	ids := make([]string, len(measurements))
	for index, measurement := range measurements {
		ids[index] = measurement.ID
	}
	return ids
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	return "../.."
}
