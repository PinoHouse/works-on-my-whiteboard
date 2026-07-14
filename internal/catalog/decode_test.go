package catalog

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

type strictDecodeFixture struct {
	SchemaVersion uint32 `yaml:"schema_version"`
	ID            string `yaml:"id"`
	Title         string `yaml:"title,omitempty"`
}

func TestDecodeStrict(t *testing.T) {
	t.Run("accepts one known document", func(t *testing.T) {
		path := "testdata/valid/manifest.yaml"
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		got, err := DecodeStrict[strictDecodeFixture](path, data)
		if err != nil {
			t.Fatalf("DecodeStrict() error = %v", err)
		}
		if got.SchemaVersion != 1 || got.ID != "valid-id" {
			t.Fatalf("DecodeStrict() = %+v", got)
		}
	})

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "unknown field", path: "testdata/unknown-field/manifest.yaml", want: "field unexpected not found"},
		{name: "duplicate key", path: "testdata/duplicate-key/manifest.yaml", want: "mapping key \"id\" already defined"},
		{name: "multiple documents", path: "testdata/multiple-docs/manifest.yaml", want: "multiple YAML documents"},
		{name: "unsupported schema", path: "testdata/unsupported-schema/manifest.yaml", want: "unsupported schema_version 2"},
		{name: "empty stable ID", path: "testdata/empty-id/manifest.yaml", want: "empty stable ID"},
		{name: "trailing content", path: "testdata/trailing-content/manifest.yaml", want: "could not find expected ':'"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := os.ReadFile(test.path)
			if err != nil {
				t.Fatal(err)
			}

			_, err = DecodeStrict[strictDecodeFixture](test.path, data)
			if err == nil {
				t.Fatal("DecodeStrict() error = nil")
			}
			if !strings.Contains(err.Error(), test.path) {
				t.Errorf("error %q does not include source path %q", err, test.path)
			}
			if !strings.Contains(err.Error(), "line ") {
				t.Errorf("error %q does not include a YAML line", err)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error %q does not include %q", err, test.want)
			}
		})
	}

	t.Run("rejects an empty nested stable ID", func(t *testing.T) {
		path := "testdata/empty-id/scope.yaml"
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		_, err = DecodeStrict[Scope](path, data)
		if err == nil {
			t.Fatal("DecodeStrict() error = nil")
		}
		for _, want := range []string{path, "line 3", "empty stable ID"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not include %q", err, want)
			}
		}
	})

	t.Run("rejects an empty stable ID through a YAML alias", func(t *testing.T) {
		path := "testdata/empty-id/alias.yaml"
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		_, err = DecodeStrict[strictDecodeFixture](path, data)
		if err == nil {
			t.Fatal("DecodeStrict() error = nil")
		}
		for _, want := range []string{path, "line 3", "empty stable ID"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not include %q", err, want)
			}
		}
	})

	t.Run("rejects an omitted stable ID", func(t *testing.T) {
		path := "testdata/empty-id/missing.yaml"
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		_, err = DecodeStrict[strictDecodeFixture](path, data)
		if err == nil {
			t.Fatal("DecodeStrict() error = nil")
		}
		for _, want := range []string{path, "line 1", "empty stable ID"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not include %q", err, want)
			}
		}
	})

	t.Run("rejects an omitted nested stable ID", func(t *testing.T) {
		path := "testdata/empty-id/missing-nested.yaml"
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		_, err = DecodeStrict[Scope](path, data)
		if err == nil {
			t.Fatal("DecodeStrict() error = nil")
		}
		for _, want := range []string{path, "line 3", "empty stable ID"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not include %q", err, want)
			}
		}
	})
}

func TestTypedCatalogModel(t *testing.T) {
	wantCase := CaseManifest{
		SchemaVersion:     1,
		ID:                "case-id",
		Title:             "Case",
		PrimaryFamily:     "family-id",
		SecondaryFamilies: []string{"secondary-family"},
		Required:          true,
		Status:            LifecycleStatusComplete,
		Dimensions:        []DimensionID{"problem-slo"},
		Principles:        []string{"principle-id"},
		Claims:            []Claim{{ID: "claim-id", Statement: "statement"}},
		Labs:              []string{"lab-id"},
		EvidenceRequirements: []EvidenceRequirement{{
			Claim: "claim-id",
			Lab:   "lab-id",
		}},
		Sources: []string{"source-id"},
	}
	wantPrinciple := PrincipleManifest{
		SchemaVersion: 1,
		ID:            "principle-id",
		Title:         "Principle",
		Required:      true,
		Status:        LifecycleStatusDraft,
		Dimensions:    []DimensionID{"evidence-validation"},
		Claims:        []Claim{{ID: "principle-claim", Statement: "statement"}},
		Labs:          []string{"lab-id"},
		EvidenceRequirements: []EvidenceRequirement{{
			Claim: "principle-claim",
			Lab:   "lab-id",
		}},
		Sources: []string{"source-id"},
	}
	wantLab := LabManifest{
		SchemaVersion:   1,
		ID:              "lab-id",
		Kind:            LabKindScenario,
		Required:        true,
		Status:          LifecycleStatusComplete,
		Implementations: []string{"implementation"},
		CaseBindings: []CaseBinding{{
			ID:         "case-binding",
			CaseID:     "case-id",
			Claim:      "claim-id",
			Workload:   "workload",
			Assertions: []string{"assertion"},
		}},
		PrincipleBindings: []PrincipleBinding{{
			ID:          "principle-binding",
			PrincipleID: "principle-id",
			Claim:       "principle-claim",
			Workload:    "workload",
			Assertions:  []string{"assertion"},
		}},
		RequiredRuns: []RequiredRun{{
			ID:       "run-id",
			Binding:  "case-binding",
			Baseline: "baseline",
			Variants: []string{"variant"},
			Workload: "workload",
			Faults:   []string{"fault"},
			Adapters: []AdapterRequirement{{ID: "adapter-id", Required: true}},
		}},
		Metrics: []string{"metric"},
		Sources: []string{"source-id"},
	}
	wantAdapter := AdapterManifest{
		SchemaVersion: 1,
		ID:            "adapter-id",
		Title:         "Adapter",
		Status:        LifecycleStatusComplete,
		Interface:     "interface",
		Runtime:       "runtime",
		Sources:       []string{"source-id"},
	}

	got := Catalog{
		Scope: Scope{
			SchemaVersion: 1,
			Families:      []ScopeFamily{{ID: "family-id", Title: "Family"}},
			Cases:         []ScopeCase{{ID: "case-id", Title: "Case", PrimaryFamily: "family-id"}},
			Exclusions:    []ScopeExclusion{{ID: "excluded-id", CanonicalCaseID: "case-id", Rationale: "reason"}},
		},
		Sources: map[string]SourceRecord{
			"source-id": {
				ID:          "source-id",
				Title:       "Source",
				URL:         "https://example.com",
				AccessedAt:  "2026-07-14",
				Kind:        "guide",
				LicenseNote: "note",
			},
		},
		Aliases: AliasSet{},
		Cases: map[string]CaseManifest{
			"case-id": wantCase,
		},
		Principles: map[string]PrincipleManifest{
			"principle-id": wantPrinciple,
		},
		Labs: map[string]LabManifest{
			"lab-id": wantLab,
		},
		Adapters: map[string]AdapterManifest{
			"adapter-id": wantAdapter,
		},
	}

	if !reflect.DeepEqual(got.Cases["case-id"], wantCase) {
		t.Errorf("case model = %+v, want %+v", got.Cases["case-id"], wantCase)
	}
	if !reflect.DeepEqual(got.Principles["principle-id"], wantPrinciple) {
		t.Errorf("principle model = %+v, want %+v", got.Principles["principle-id"], wantPrinciple)
	}
	if !reflect.DeepEqual(got.Labs["lab-id"], wantLab) {
		t.Errorf("lab model = %+v, want %+v", got.Labs["lab-id"], wantLab)
	}
	if !reflect.DeepEqual(got.Adapters["adapter-id"], wantAdapter) {
		t.Errorf("adapter model = %+v, want %+v", got.Adapters["adapter-id"], wantAdapter)
	}

	assertDecodedDocument(t, "case manifest YAML tags", `
schema_version: 1
id: case-id
title: Case
primary_family: family-id
secondary_families: [secondary-family]
required: true
status: complete
dimensions: [problem-slo]
principles: [principle-id]
claims:
  - id: claim-id
    statement: statement
labs: [lab-id]
evidence_requirements:
  - claim: claim-id
    lab: lab-id
sources: [source-id]
`, wantCase)
	assertDecodedDocument(t, "principle manifest YAML tags", `
schema_version: 1
id: principle-id
title: Principle
required: true
status: draft
dimensions: [evidence-validation]
claims:
  - id: principle-claim
    statement: statement
labs: [lab-id]
evidence_requirements:
  - claim: principle-claim
    lab: lab-id
sources: [source-id]
`, wantPrinciple)
	assertDecodedDocument(t, "lab manifest YAML tags", `
schema_version: 1
id: lab-id
kind: scenario
required: true
status: complete
implementations: [implementation]
case_bindings:
  - id: case-binding
    case_id: case-id
    claim: claim-id
    workload: workload
    assertions: [assertion]
principle_bindings:
  - id: principle-binding
    principle_id: principle-id
    claim: principle-claim
    workload: workload
    assertions: [assertion]
required_runs:
  - id: run-id
    binding: case-binding
    baseline: baseline
    variants: [variant]
    workload: workload
    faults: [fault]
    adapters:
      - id: adapter-id
        required: true
metrics: [metric]
sources: [source-id]
`, wantLab)
	assertDecodedDocument(t, "adapter manifest YAML tags", `
schema_version: 1
id: adapter-id
title: Adapter
status: complete
interface: interface
runtime: runtime
sources: [source-id]
`, wantAdapter)

	if LifecycleStatusDraft != "draft" || LifecycleStatusComplete != "complete" {
		t.Errorf("lifecycle values = %q, %q", LifecycleStatusDraft, LifecycleStatusComplete)
	}
	if LabKindPrimitive != "primitive" || LabKindScenario != "scenario" {
		t.Errorf("lab kinds = %q, %q", LabKindPrimitive, LabKindScenario)
	}
	wantKinds := []EntityKind{"case", "principle", "lab", "source"}
	gotKinds := []EntityKind{EntityKindCase, EntityKindPrinciple, EntityKindLab, EntityKindSource}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Errorf("entity kinds = %v, want %v", gotKinds, wantKinds)
	}
}

func assertDecodedDocument[T any](t *testing.T, name, source string, want T) {
	t.Helper()
	got, err := DecodeStrict[T](name, []byte(strings.TrimPrefix(source, "\n")))
	if err != nil {
		t.Fatalf("DecodeStrict(%s) error = %v", name, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DecodeStrict(%s) = %+v, want %+v", name, got, want)
	}
}
