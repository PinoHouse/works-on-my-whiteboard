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
	tests := []struct {
		name   string
		mutate func(*catalog.Catalog)
	}{
		{
			name: "missing",
			mutate: func(c *catalog.Catalog) {
				delete(c.Adapters, "adapter-a")
			},
		},
		{
			name: "draft",
			mutate: func(c *catalog.Catalog) {
				adapter := c.Adapters["adapter-a"]
				adapter.Status = catalog.LifecycleStatusDraft
				c.Adapters[adapter.ID] = adapter
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := validCatalog()
			lab := c.Labs["scenario-lab-a"]
			lab.RequiredRuns[0].Adapters[0].Required = false
			c.Labs[lab.ID] = lab
			test.mutate(c)

			matrix, diagnostics := BuildRequiredMatrix(c)
			assertNoDiagnosticCode(t, diagnostics, CodeMissingRequiredAdapter)
			assertNoDiagnosticCode(t, diagnostics, CodeDependencyIncomplete)
			for _, cell := range matrix {
				if cell.Role == "adapter" {
					t.Fatalf("optional adapter produced required matrix cell: %#v", cell)
				}
			}
			for _, mode := range []Mode{ModeDevelopment, ModeRelease} {
				report := Validate(c, mode)
				assertNoDiagnosticCode(t, report.Diagnostics, CodeMissingRequiredAdapter)
				assertNoDiagnosticCode(t, report.Diagnostics, CodeDependencyIncomplete)
			}
		})
	}
}

func TestBuildRequiredMatrixProjectsOnlyRequiredCompleteLabs(t *testing.T) {
	c := validCatalog()
	addOptionalPrimitiveLabWithAdapter(c, catalog.LifecycleStatusComplete, catalog.LifecycleStatusComplete)

	matrix, diagnostics := BuildRequiredMatrix(c)
	if len(diagnostics) != 0 {
		t.Fatalf("optional complete lab diagnostics = %#v, want none", diagnostics)
	}
	assertNoMatrixCellsForRun(t, matrix, "primitive-lab-optional", "run-principle-optional")
	for _, mode := range []Mode{ModeDevelopment, ModeRelease} {
		report := Validate(c, mode)
		if len(report.Diagnostics) != 0 {
			t.Fatalf("Validate(%q) diagnostics = %#v, want none", mode, report.Diagnostics)
		}
		assertNoMatrixCellsForRun(t, report.Matrix, "primitive-lab-optional", "run-principle-optional")
	}
}

func TestBuildRequiredMatrixRejectsPrimitiveLabFromScenarioOnlyClosure(t *testing.T) {
	c := validCatalog()
	addOptionalPrimitiveLabWithAdapter(c, catalog.LifecycleStatusComplete, catalog.LifecycleStatusComplete)
	manifest := c.Cases["case-a"]
	manifest.Labs = append(manifest.Labs, "primitive-lab-optional")
	c.Cases[manifest.ID] = manifest

	report := Validate(c, ModeDevelopment)
	assertDiagnosticCode(t, report.Diagnostics, CodeUnknownReference)
	assertDiagnosticCode(t, report.Diagnostics, CodeDependencyIncomplete)
	if makeStringSet(report.Coverage.RequiredAdapters...).contains("adapter-optional") {
		t.Fatalf("required adapters = %#v, wrong-kind scenario edge must not require adapter-optional", report.Coverage.RequiredAdapters)
	}
	assertNoMatrixCellsForRun(t, report.Matrix, "primitive-lab-optional", "run-principle-optional")
}

func TestBuildRequiredMatrixRejectsScenarioLabFromPrimitiveOnlyClosure(t *testing.T) {
	c := validCatalog()
	addOptionalScenarioLab(c)
	manifest := c.Principles["principle-a"]
	manifest.Labs = append(manifest.Labs, "scenario-lab-optional")
	c.Principles[manifest.ID] = manifest

	report := Validate(c, ModeDevelopment)
	assertDiagnosticCode(t, report.Diagnostics, CodeUnknownReference)
	assertDiagnosticCode(t, report.Diagnostics, CodeDependencyIncomplete)
	if makeStringSet(report.Coverage.RequiredAdapters...).contains("adapter-scenario-optional") {
		t.Fatalf("required adapters = %#v, wrong-kind primitive edge must not require adapter-scenario-optional", report.Coverage.RequiredAdapters)
	}
	assertNoMatrixCellsForRun(t, report.Matrix, "scenario-lab-optional", "run-case-optional")
}

func TestBuildRequiredMatrixKeepsValidPrimitiveClosureDespiteWrongScenarioReference(t *testing.T) {
	c := validCatalog()
	addOptionalPrimitiveLabWithAdapter(c, catalog.LifecycleStatusComplete, catalog.LifecycleStatusComplete)
	principle := c.Principles["principle-optional"]
	principle.Required = true
	principle.Status = catalog.LifecycleStatusComplete
	principle.Dimensions = []catalog.DimensionID{"contracts-data-invariants"}
	c.Principles[principle.ID] = principle
	manifest := c.Cases["case-a"]
	manifest.Labs = append(manifest.Labs, "primitive-lab-optional")
	c.Cases[manifest.ID] = manifest

	report := Validate(c, ModeDevelopment)
	assertDiagnosticCode(t, report.Diagnostics, CodeUnknownReference)
	assertDiagnosticCode(t, report.Diagnostics, CodeDependencyIncomplete)
	if !makeStringSet(report.Coverage.RequiredAdapters...).contains("adapter-optional") {
		t.Fatalf("required adapters = %#v, want adapter-optional from valid primitive closure", report.Coverage.RequiredAdapters)
	}
	assertHasMatrixCellsForRun(t, report.Matrix, "primitive-lab-optional", "run-principle-optional")
}

func TestBuildRequiredMatrixRequiresCompleteLabContractForCells(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.LabManifest)
	}{
		{
			name: "binding assertions",
			mutate: func(lab *catalog.LabManifest) {
				lab.CaseBindings[0].Assertions = nil
			},
		},
		{
			name: "run faults",
			mutate: func(lab *catalog.LabManifest) {
				lab.RequiredRuns[0].Faults = nil
			},
		},
		{
			name: "run variants",
			mutate: func(lab *catalog.LabManifest) {
				lab.RequiredRuns[0].Variants = nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := validCatalog()
			lab := c.Labs["scenario-lab-a"]
			test.mutate(&lab)
			c.Labs[lab.ID] = lab

			matrix, _ := BuildRequiredMatrix(c)
			assertNoMatrixCellsForRun(t, matrix, lab.ID, lab.RequiredRuns[0].ID)

			report := Validate(c, ModeDevelopment)
			assertDiagnosticCodeCount(t, report.Diagnostics, CodeCompleteContractEmpty, 1)
			assertNoMatrixCellsForRun(t, report.Matrix, lab.ID, lab.RequiredRuns[0].ID)
		})
	}
}

func TestBuildRequiredMatrixRequiresCompleteAdapterContractForCells(t *testing.T) {
	c := validCatalog()
	adapter := c.Adapters["adapter-a"]
	adapter.Interface = ""
	c.Adapters[adapter.ID] = adapter

	matrix, diagnostics := BuildRequiredMatrix(c)
	assertNoDiagnosticCode(t, diagnostics, CodeDependencyIncomplete)
	assertNoMatrixCellsForRun(t, matrix, "scenario-lab-a", "run-case-a")

	report := Validate(c, ModeDevelopment)
	assertDiagnosticCodeCount(t, report.Diagnostics, CodeCompleteContractEmpty, 1)
	assertNoDiagnosticCode(t, report.Diagnostics, CodeDependencyIncomplete)
	assertNoMatrixCellsForRun(t, report.Matrix, "scenario-lab-a", "run-case-a")
}

func TestRequiredAdapterCompletenessDependsOnOwningLabStatus(t *testing.T) {
	tests := []struct {
		name           string
		setup          func(*catalog.Catalog) (string, string)
		wantDependency int
	}{
		{
			name: "required complete lab with draft adapter",
			setup: func(c *catalog.Catalog) (string, string) {
				adapter := c.Adapters["adapter-a"]
				adapter.Status = catalog.LifecycleStatusDraft
				c.Adapters[adapter.ID] = adapter
				return "scenario-lab-a", "run-case-a"
			},
			wantDependency: 1,
		},
		{
			name: "optional complete lab with draft adapter",
			setup: func(c *catalog.Catalog) (string, string) {
				addOptionalPrimitiveLabWithAdapter(c, catalog.LifecycleStatusComplete, catalog.LifecycleStatusDraft)
				return "primitive-lab-optional", "run-principle-optional"
			},
			wantDependency: 1,
		},
		{
			name: "optional draft lab with draft adapter",
			setup: func(c *catalog.Catalog) (string, string) {
				addOptionalPrimitiveLabWithAdapter(c, catalog.LifecycleStatusDraft, catalog.LifecycleStatusDraft)
				return "primitive-lab-optional", "run-principle-optional"
			},
			wantDependency: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := validCatalog()
			labID, runID := test.setup(c)

			matrix, diagnostics := BuildRequiredMatrix(c)
			assertDiagnosticCodeCount(t, diagnostics, CodeDependencyIncomplete, test.wantDependency)
			assertNoDiagnosticCode(t, diagnostics, CodeMissingRequiredAdapter)
			assertNoMatrixCellsForRun(t, matrix, labID, runID)

			for _, mode := range []Mode{ModeDevelopment, ModeRelease} {
				report := Validate(c, mode)
				assertDiagnosticCodeCount(t, report.Diagnostics, CodeDependencyIncomplete, test.wantDependency)
				assertNoDiagnosticCode(t, report.Diagnostics, CodeMissingRequiredAdapter)
				if len(report.Diagnostics) != test.wantDependency {
					t.Fatalf("Validate(%q) diagnostics = %#v, want only %d dependency diagnostics", mode, report.Diagnostics, test.wantDependency)
				}
				assertNoMatrixCellsForRun(t, report.Matrix, labID, runID)
			}
		})
	}
}

func addOptionalPrimitiveLabWithAdapter(c *catalog.Catalog, labStatus, adapterStatus catalog.LifecycleStatus) {
	c.Principles["principle-optional"] = catalog.PrincipleManifest{
		SchemaVersion: 1,
		ID:            "principle-optional",
		Title:         "Optional principle",
		Status:        catalog.LifecycleStatusDraft,
		Claims: []catalog.Claim{{
			ID:        "claim-principle-optional",
			Statement: "Optional principle claim.",
		}},
		Labs: []string{"primitive-lab-optional"},
		EvidenceRequirements: []catalog.EvidenceRequirement{{
			Claim: "claim-principle-optional",
			Lab:   "primitive-lab-optional",
		}},
		Sources: []string{"source-a"},
	}
	c.Labs["primitive-lab-optional"] = catalog.LabManifest{
		SchemaVersion:   1,
		ID:              "primitive-lab-optional",
		Kind:            catalog.LabKindPrimitive,
		Status:          labStatus,
		Implementations: []string{"primitive-optional-a", "primitive-optional-b"},
		PrincipleBindings: []catalog.PrincipleBinding{{
			ID:          "binding-principle-optional",
			PrincipleID: "principle-optional",
			Claim:       "claim-principle-optional",
			Workload:    "workload-principle-optional",
			Assertions:  []string{"assertion-principle-optional"},
		}},
		RequiredRuns: []catalog.RequiredRun{{
			ID:       "run-principle-optional",
			Binding:  "binding-principle-optional",
			Baseline: "primitive-optional-a",
			Variants: []string{"primitive-optional-b"},
			Workload: "workload-principle-optional",
			Faults:   []string{"fault-principle-optional"},
			Adapters: []catalog.AdapterRequirement{{ID: "adapter-optional", Required: true}},
		}},
		Metrics: []string{"metric-principle-optional"},
		Sources: []string{"source-a"},
	}
	c.Adapters["adapter-optional"] = catalog.AdapterManifest{
		SchemaVersion: 1,
		ID:            "adapter-optional",
		Title:         "Optional adapter",
		Status:        adapterStatus,
		Interface:     "interface-optional",
		Runtime:       "docker",
		Sources:       []string{"source-a"},
	}
}

func addOptionalScenarioLab(c *catalog.Catalog) {
	c.Cases["case-optional"] = catalog.CaseManifest{
		SchemaVersion: 1,
		ID:            "case-optional",
		Title:         "Optional case",
		PrimaryFamily: "addressing-traffic",
		Status:        catalog.LifecycleStatusDraft,
		Claims: []catalog.Claim{{
			ID:        "claim-case-optional",
			Statement: "Optional case claim.",
		}},
		Labs: []string{"scenario-lab-optional"},
		EvidenceRequirements: []catalog.EvidenceRequirement{{
			Claim: "claim-case-optional",
			Lab:   "scenario-lab-optional",
		}},
		Sources: []string{"source-a"},
	}
	c.Labs["scenario-lab-optional"] = catalog.LabManifest{
		SchemaVersion:   1,
		ID:              "scenario-lab-optional",
		Kind:            catalog.LabKindScenario,
		Status:          catalog.LifecycleStatusComplete,
		Implementations: []string{"scenario-optional-a", "scenario-optional-b"},
		CaseBindings: []catalog.CaseBinding{{
			ID:         "binding-case-optional",
			CaseID:     "case-optional",
			Claim:      "claim-case-optional",
			Workload:   "workload-case-optional",
			Assertions: []string{"assertion-case-optional"},
		}},
		RequiredRuns: []catalog.RequiredRun{{
			ID:       "run-case-optional",
			Binding:  "binding-case-optional",
			Baseline: "scenario-optional-a",
			Variants: []string{"scenario-optional-b"},
			Workload: "workload-case-optional",
			Faults:   []string{"fault-case-optional"},
			Adapters: []catalog.AdapterRequirement{{ID: "adapter-scenario-optional", Required: true}},
		}},
		Metrics: []string{"metric-case-optional"},
		Sources: []string{"source-a"},
	}
	c.Adapters["adapter-scenario-optional"] = catalog.AdapterManifest{
		SchemaVersion: 1,
		ID:            "adapter-scenario-optional",
		Title:         "Optional scenario adapter",
		Status:        catalog.LifecycleStatusComplete,
		Interface:     "interface-scenario-optional",
		Runtime:       "docker",
		Sources:       []string{"source-a"},
	}
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

func TestBuildRequiredMatrixRejectsDuplicateRunsWithoutCells(t *testing.T) {
	c := validCatalog()
	lab := c.Labs["scenario-lab-a"]
	lab.RequiredRuns = append(lab.RequiredRuns, lab.RequiredRuns[0])
	c.Labs[lab.ID] = lab

	matrix, diagnostics := BuildRequiredMatrix(c)
	assertDiagnosticCode(t, diagnostics, CodeDuplicateRequiredRun)
	assertNoDiagnosticCode(t, diagnostics, CodeInvalidRunBinding)
	assertNoMatrixCellsForRun(t, matrix, lab.ID, lab.RequiredRuns[0].ID)
	assertUniqueMatrixKeys(t, matrix)
}

func TestBuildRequiredMatrixReportsIndependentRunDiagnostics(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*catalog.Catalog, *catalog.LabManifest)
		wantCodes []string
	}{
		{
			name: "invalid binding and missing adapter",
			mutate: func(c *catalog.Catalog, lab *catalog.LabManifest) {
				lab.RequiredRuns[0].Binding = ""
				delete(c.Adapters, "adapter-a")
			},
			wantCodes: []string{CodeInvalidRunBinding, CodeMissingRequiredAdapter},
		},
		{
			name: "foreign claim and unknown implementation",
			mutate: func(_ *catalog.Catalog, lab *catalog.LabManifest) {
				lab.CaseBindings[0].Claim = "claim-missing"
				lab.RequiredRuns[0].Baseline = "implementation-missing"
			},
			wantCodes: []string{CodeForeignClaim, CodeUnknownImplementation},
		},
		{
			name: "workload mismatch and duplicate implementation selection",
			mutate: func(_ *catalog.Catalog, lab *catalog.LabManifest) {
				lab.RequiredRuns[0].Workload = "workload-other"
				lab.RequiredRuns[0].Variants = []string{lab.RequiredRuns[0].Baseline}
			},
			wantCodes: []string{CodeBindingWorkloadMismatch, CodeInvalidRunBinding},
		},
		{
			name: "wrong binding kind and duplicate adapter selection",
			mutate: func(_ *catalog.Catalog, lab *catalog.LabManifest) {
				lab.PrincipleBindings = []catalog.PrincipleBinding{{
					ID:          "binding-wrong-kind",
					PrincipleID: "principle-a",
					Claim:       "claim-principle-a",
					Workload:    "workload-a",
					Assertions:  []string{"assertion-principle-a"},
				}}
				lab.RequiredRuns[0].Binding = "binding-wrong-kind"
				lab.RequiredRuns[0].Adapters = append(lab.RequiredRuns[0].Adapters, lab.RequiredRuns[0].Adapters[0])
			},
			wantCodes: []string{CodeOrphanedLab, CodeInvalidRunBinding},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := validCatalog()
			lab := c.Labs["scenario-lab-a"]
			test.mutate(c, &lab)
			c.Labs[lab.ID] = lab

			matrix, diagnostics := BuildRequiredMatrix(c)
			for _, code := range test.wantCodes {
				assertDiagnosticCodeCount(t, diagnostics, code, 1)
			}
			assertNoMatrixCellsForRun(t, matrix, lab.ID, lab.RequiredRuns[0].ID)
		})
	}
}

func TestBuildRequiredMatrixOmitsCellsWhenRequiredAdapterIsMissing(t *testing.T) {
	c := validCatalog()
	delete(c.Adapters, "adapter-a")

	matrix, diagnostics := BuildRequiredMatrix(c)
	assertDiagnosticCodeCount(t, diagnostics, CodeMissingRequiredAdapter, 1)
	assertNoMatrixCellsForRun(t, matrix, "scenario-lab-a", "run-case-a")
}

func TestBuildRequiredMatrixOmitsEveryRunWithADuplicateID(t *testing.T) {
	c := validCatalog()
	lab := c.Labs["scenario-lab-a"]
	lab.CaseBindings = append(lab.CaseBindings, catalog.CaseBinding{
		ID:         "binding-case-second",
		CaseID:     "case-a",
		Claim:      "claim-case-a",
		Workload:   "workload-a",
		Assertions: []string{"assertion-case-second"},
	})
	lab.RequiredRuns = append(lab.RequiredRuns, catalog.RequiredRun{
		ID:       lab.RequiredRuns[0].ID,
		Binding:  "binding-case-second",
		Baseline: "implementation-b",
		Workload: "workload-a",
		Faults:   []string{"fault-second"},
	})
	c.Labs[lab.ID] = lab

	matrix, diagnostics := BuildRequiredMatrix(c)
	assertDiagnosticCodeCount(t, diagnostics, CodeDuplicateRequiredRun, 1)
	assertNoDiagnosticCode(t, diagnostics, CodeUnusedBinding)
	assertNoMatrixCellsForRun(t, matrix, lab.ID, lab.RequiredRuns[0].ID)
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

func assertDiagnosticCodeCount(t *testing.T, diagnostics []Diagnostic, code string, want int) {
	t.Helper()
	got := 0
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			got++
		}
	}
	if got != want {
		t.Fatalf("diagnostic %q count = %d, want %d; codes=%#v diagnostics=%#v", code, got, want, diagnosticCodes(diagnostics), diagnostics)
	}
}

func assertNoMatrixCellsForRun(t *testing.T, matrix []MatrixCell, labID, runID string) {
	t.Helper()
	for _, cell := range matrix {
		if cell.LabID == labID && cell.RequiredRunID == runID {
			t.Fatalf("invalid required run produced matrix cell: %#v", cell)
		}
	}
}

func assertHasMatrixCellsForRun(t *testing.T, matrix []MatrixCell, labID, runID string) {
	t.Helper()
	for _, cell := range matrix {
		if cell.LabID == labID && cell.RequiredRunID == runID {
			return
		}
	}
	t.Fatalf("matrix = %#v, want cells for lab %q run %q", matrix, labID, runID)
}
