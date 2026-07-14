package validator

import (
	"testing"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
)

func TestDependencyCycleUsesFrozenDiagnostic(t *testing.T) {
	graph := dependencyGraph{
		"case:a":      {"principle:b"},
		"principle:b": {"lab:c"},
		"lab:c":       {"case:a"},
	}
	diagnostics := dependencyCycleDiagnostics(graph)
	if len(diagnostics) != 1 || diagnostics[0].Code != CodeDependencyIncomplete {
		t.Fatalf("dependency cycle diagnostics = %#v, want sole %q", diagnostics, CodeDependencyIncomplete)
	}
}

func TestReleaseRequiresExplicitDraftPrincipleClosureToBeComplete(t *testing.T) {
	c := validCatalog()
	c.Principles["principle-draft"] = catalog.PrincipleManifest{
		SchemaVersion: 1,
		ID:            "principle-draft",
		Title:         "Draft principle",
		Required:      true,
		Status:        catalog.LifecycleStatusDraft,
		Claims: []catalog.Claim{{
			ID:        "claim-principle-draft",
			Statement: "Draft principle claim.",
		}},
		Labs: []string{"primitive-lab-draft"},
		EvidenceRequirements: []catalog.EvidenceRequirement{{
			Claim: "claim-principle-draft",
			Lab:   "primitive-lab-draft",
		}},
		Sources: []string{"source-a"},
	}
	c.Labs["primitive-lab-draft"] = catalog.LabManifest{
		SchemaVersion:   1,
		ID:              "primitive-lab-draft",
		Kind:            catalog.LabKindPrimitive,
		Required:        true,
		Status:          catalog.LifecycleStatusDraft,
		Implementations: []string{"primitive-draft"},
		PrincipleBindings: []catalog.PrincipleBinding{{
			ID:          "binding-principle-draft",
			PrincipleID: "principle-draft",
			Claim:       "claim-principle-draft",
			Workload:    "workload-principle-draft",
			Assertions:  []string{"assertion-principle-draft"},
		}},
		RequiredRuns: []catalog.RequiredRun{{
			ID:       "run-principle-draft",
			Binding:  "binding-principle-draft",
			Baseline: "primitive-draft",
			Workload: "workload-principle-draft",
			Faults:   []string{"fault-principle-draft"},
		}},
		Metrics: []string{"metric-principle-draft"},
		Sources: []string{"source-a"},
	}

	development := Validate(c, ModeDevelopment)
	assertNoDiagnosticCode(t, development.Diagnostics, CodeDependencyIncomplete)
	assertNoDiagnosticCode(t, development.Diagnostics, CodeMissingRequiredPrincipleLab)
	assertNoMatrixCellsForRun(t, development.Matrix, "primitive-lab-draft", "run-principle-draft")

	release := Validate(c, ModeRelease)
	assertDiagnosticCodeCount(t, release.Diagnostics, CodeDependencyIncomplete, 2)
	assertDiagnosticCodeForEntity(t, release.Diagnostics, CodeDependencyIncomplete, "principle-draft")
	assertDiagnosticCodeForEntity(t, release.Diagnostics, CodeDependencyIncomplete, "primitive-lab-draft")
	assertNoDiagnosticCode(t, release.Diagnostics, CodeMissingRequiredPrincipleLab)
	assertNoMatrixCellsForRun(t, release.Matrix, "primitive-lab-draft", "run-principle-draft")
}

func TestRequiredDraftPrincipleNeedsOwnerListedPrimitiveLab(t *testing.T) {
	c := validCatalog()
	c.Principles["principle-draft"] = catalog.PrincipleManifest{
		SchemaVersion: 1,
		ID:            "principle-draft",
		Title:         "Draft principle",
		Required:      true,
		Status:        catalog.LifecycleStatusDraft,
		Claims: []catalog.Claim{{
			ID:        "claim-principle-draft",
			Statement: "Draft principle claim.",
		}},
		Sources: []string{"source-a"},
	}

	diagnostics := Validate(c, ModeDevelopment).Diagnostics
	assertDiagnosticCodeCount(t, diagnostics, CodeMissingRequiredPrincipleLab, 1)
	assertDiagnosticCodeForEntity(t, diagnostics, CodeMissingRequiredPrincipleLab, "principle-draft")
}

func TestReleaseRequiresExplicitDraftScenarioClosureToBeComplete(t *testing.T) {
	c := validCatalog()
	c.Scope.Cases = append(c.Scope.Cases, catalog.ScopeCase{
		ID:            "case-draft",
		Title:         "Draft case",
		PrimaryFamily: "addressing-traffic",
	})
	c.Cases["case-draft"] = catalog.CaseManifest{
		SchemaVersion: 1,
		ID:            "case-draft",
		Title:         "Draft case",
		PrimaryFamily: "addressing-traffic",
		Required:      true,
		Status:        catalog.LifecycleStatusDraft,
		Claims: []catalog.Claim{{
			ID:        "claim-case-draft",
			Statement: "Draft case claim.",
		}},
		Labs: []string{"scenario-lab-draft"},
		EvidenceRequirements: []catalog.EvidenceRequirement{{
			Claim: "claim-case-draft",
			Lab:   "scenario-lab-draft",
		}},
		Sources: []string{"source-a"},
	}
	c.Labs["scenario-lab-draft"] = catalog.LabManifest{
		SchemaVersion:   1,
		ID:              "scenario-lab-draft",
		Kind:            catalog.LabKindScenario,
		Required:        true,
		Status:          catalog.LifecycleStatusDraft,
		Implementations: []string{"implementation-draft"},
		CaseBindings: []catalog.CaseBinding{{
			ID:         "binding-case-draft",
			CaseID:     "case-draft",
			Claim:      "claim-case-draft",
			Workload:   "workload-case-draft",
			Assertions: []string{"assertion-case-draft"},
		}},
		RequiredRuns: []catalog.RequiredRun{{
			ID:       "run-case-draft",
			Binding:  "binding-case-draft",
			Baseline: "implementation-draft",
			Workload: "workload-case-draft",
			Faults:   []string{"fault-case-draft"},
			Adapters: []catalog.AdapterRequirement{{ID: "adapter-draft", Required: true}},
		}},
		Metrics: []string{"metric-case-draft"},
		Sources: []string{"source-a"},
	}
	c.Adapters["adapter-draft"] = catalog.AdapterManifest{
		SchemaVersion: 1,
		ID:            "adapter-draft",
		Title:         "Draft adapter",
		Status:        catalog.LifecycleStatusDraft,
		Interface:     "interface-draft",
		Runtime:       "docker",
		Sources:       []string{"source-a"},
	}

	development := Validate(c, ModeDevelopment)
	assertNoDiagnosticCode(t, development.Diagnostics, CodeDependencyIncomplete)
	assertNoMatrixCellsForRun(t, development.Matrix, "scenario-lab-draft", "run-case-draft")

	release := Validate(c, ModeRelease)
	assertDiagnosticCodeCount(t, release.Diagnostics, CodeDependencyIncomplete, 1)
	assertDiagnosticCodeForEntity(t, release.Diagnostics, CodeDependencyIncomplete, "scenario-lab-draft")
	assertNoMatrixCellsForRun(t, release.Matrix, "scenario-lab-draft", "run-case-draft")
}

func assertDiagnosticCodeForEntity(t *testing.T, diagnostics []Diagnostic, code, entityID string) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && diagnostic.EntityID == entityID {
			return
		}
	}
	t.Fatalf("diagnostics = %#v, want code %q for entity %q", diagnostics, code, entityID)
}
