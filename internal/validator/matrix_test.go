package validator

import (
	"reflect"
	"testing"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
)

func TestBuildRequiredMatrixUsesSixIdentityFields(t *testing.T) {
	c := validCatalog()
	want := []MatrixCell{
		{
			LabID:            "primitive-lab-a",
			RequiredRunID:    "run-principle-a",
			BindingID:        "binding-principle-a",
			ClaimID:          "claim-principle-a",
			Role:             "baseline",
			ImplementationID: "primitive-a",
			Workload:         "primitive-workload-a",
			Faults:           []string{"primitive-fault-a"},
			AssertionIDs:     []string{"assertion-principle-a"},
		},
		{
			LabID:            "primitive-lab-a",
			RequiredRunID:    "run-principle-a",
			BindingID:        "binding-principle-a",
			ClaimID:          "claim-principle-a",
			Role:             "variant",
			ImplementationID: "primitive-b",
			Workload:         "primitive-workload-a",
			Faults:           []string{"primitive-fault-a"},
			AssertionIDs:     []string{"assertion-principle-a"},
		},
		{
			LabID:            "scenario-lab-a",
			RequiredRunID:    "run-case-a",
			BindingID:        "binding-case-a",
			ClaimID:          "claim-case-a",
			Role:             "adapter",
			ImplementationID: "adapter-a",
			AdapterID:        "adapter-a",
			Workload:         "workload-a",
			Faults:           []string{"fault-a", "fault-b"},
			AssertionIDs:     []string{"assertion-case-a"},
		},
		{
			LabID:            "scenario-lab-a",
			RequiredRunID:    "run-case-a",
			BindingID:        "binding-case-a",
			ClaimID:          "claim-case-a",
			Role:             "baseline",
			ImplementationID: "implementation-a",
			Workload:         "workload-a",
			Faults:           []string{"fault-a", "fault-b"},
			AssertionIDs:     []string{"assertion-case-a"},
		},
		{
			LabID:            "scenario-lab-a",
			RequiredRunID:    "run-case-a",
			BindingID:        "binding-case-a",
			ClaimID:          "claim-case-a",
			Role:             "variant",
			ImplementationID: "implementation-b",
			Workload:         "workload-a",
			Faults:           []string{"fault-a", "fault-b"},
			AssertionIDs:     []string{"assertion-case-a"},
		},
	}

	got, diagnostics := BuildRequiredMatrix(c)
	if len(diagnostics) != 0 {
		t.Fatalf("BuildRequiredMatrix() diagnostics = %#v, want none", diagnostics)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matrix = %#v, want %#v", got, want)
	}

	keys := make(map[[6]string]struct{}, len(got))
	for _, cell := range got {
		key := matrixIdentity(cell)
		if _, exists := keys[key]; exists {
			t.Fatalf("duplicate six-field matrix key: %#v", key)
		}
		keys[key] = struct{}{}
	}

	got[0].Faults[0] = "mutated"
	got[0].AssertionIDs[0] = "mutated"
	original := c.Labs["primitive-lab-a"]
	if original.RequiredRuns[0].Faults[0] != "primitive-fault-a" || original.PrincipleBindings[0].Assertions[0] != "assertion-principle-a" {
		t.Fatal("BuildRequiredMatrix() aliased mutable catalog slices")
	}
}

func TestBuildRequiredMatrixSkipsOptionalAdapters(t *testing.T) {
	c := validCatalog()
	lab := c.Labs["scenario-lab-a"]
	lab.RequiredRuns[0].Adapters[0].Required = false
	c.Labs[lab.ID] = lab
	delete(c.Adapters, "adapter-a")

	matrix, diagnostics := BuildRequiredMatrix(c)
	assertNoDiagnosticCode(t, diagnostics, CodeMissingRequiredAdapter)
	for _, cell := range matrix {
		if cell.Role == "adapter" {
			t.Fatalf("optional adapter produced required matrix cell: %#v", cell)
		}
	}
	assertNoDiagnosticCode(t, Validate(c, ModeDevelopment).Diagnostics, CodeMissingRequiredAdapter)
}

func TestBuildRequiredMatrixRejectsDuplicateSelections(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.RequiredRun)
	}{
		{
			name: "baseline repeated as variant",
			mutate: func(run *catalog.RequiredRun) {
				run.Variants = []string{run.Baseline}
			},
		},
		{
			name: "duplicate variants",
			mutate: func(run *catalog.RequiredRun) {
				run.Variants = []string{"implementation-b", "implementation-b"}
			},
		},
		{
			name: "duplicate adapters",
			mutate: func(run *catalog.RequiredRun) {
				run.Adapters = append(run.Adapters, run.Adapters[0])
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := validCatalog()
			lab := c.Labs["scenario-lab-a"]
			test.mutate(&lab.RequiredRuns[0])
			c.Labs[lab.ID] = lab
			matrix, diagnostics := BuildRequiredMatrix(c)
			assertDiagnosticCode(t, diagnostics, CodeInvalidRunBinding)
			assertUniqueMatrixKeys(t, matrix)
		})
	}
}

func TestBuildRequiredMatrixRejectsDuplicateRunsWithoutDuplicateCells(t *testing.T) {
	c := validCatalog()
	lab := c.Labs["scenario-lab-a"]
	lab.RequiredRuns = append(lab.RequiredRuns, lab.RequiredRuns[0])
	c.Labs[lab.ID] = lab

	matrix, diagnostics := BuildRequiredMatrix(c)
	assertDiagnosticCode(t, diagnostics, CodeDuplicateRequiredRun)
	assertDiagnosticCode(t, diagnostics, CodeInvalidRunBinding)
	assertUniqueMatrixKeys(t, matrix)
}

func TestBuildRequiredMatrixIsDeterministic(t *testing.T) {
	c := validCatalog()
	want, wantDiagnostics := BuildRequiredMatrix(c)
	for iteration := 0; iteration < 20; iteration++ {
		got, diagnostics := BuildRequiredMatrix(c)
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(diagnostics, wantDiagnostics) {
			t.Fatalf("iteration %d was nondeterministic: matrix=%#v diagnostics=%#v", iteration, got, diagnostics)
		}
	}
}

func matrixIdentity(cell MatrixCell) [6]string {
	return [6]string{
		cell.LabID,
		cell.RequiredRunID,
		cell.BindingID,
		cell.ClaimID,
		cell.ImplementationID,
		cell.AdapterID,
	}
}

func assertUniqueMatrixKeys(t *testing.T, matrix []MatrixCell) {
	t.Helper()
	seen := make(map[[6]string]struct{}, len(matrix))
	for _, cell := range matrix {
		key := matrixIdentity(cell)
		if _, exists := seen[key]; exists {
			t.Fatalf("duplicate matrix key %#v in %#v", key, matrix)
		}
		seen[key] = struct{}{}
	}
}
