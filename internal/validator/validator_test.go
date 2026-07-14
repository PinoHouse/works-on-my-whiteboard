package validator

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
)

func TestDiagnosticCodesAreStable(t *testing.T) {
	want := []string{
		"invalid_stable_id",
		"duplicate_scope_family",
		"duplicate_scope_case",
		"case_outside_scope",
		"unknown_family",
		"unknown_reference",
		"duplicate_claim_id",
		"invalid_source_url",
		"invalid_source_date",
		"dangling_source",
		"complete_contract_empty",
		"missing_case_binding",
		"missing_principle_binding",
		"duplicate_binding_id",
		"duplicate_required_run",
		"invalid_run_binding",
		"foreign_claim",
		"unused_binding",
		"binding_workload_mismatch",
		"unknown_implementation",
		"missing_required_principle_lab",
		"missing_required_adapter",
		"dependency_incomplete",
		"orphaned_lab",
		"status_vocabulary_mismatch",
		"alias_cycle",
		"release_scope_incomplete",
		"release_family_mismatch",
	}
	got := []string{
		CodeInvalidStableID,
		CodeDuplicateScopeFamily,
		CodeDuplicateScopeCase,
		CodeCaseOutsideScope,
		CodeUnknownFamily,
		CodeUnknownReference,
		CodeDuplicateClaimID,
		CodeInvalidSourceURL,
		CodeInvalidSourceDate,
		CodeDanglingSource,
		CodeCompleteContractEmpty,
		CodeMissingCaseBinding,
		CodeMissingPrincipleBinding,
		CodeDuplicateBindingID,
		CodeDuplicateRequiredRun,
		CodeInvalidRunBinding,
		CodeForeignClaim,
		CodeUnusedBinding,
		CodeBindingWorkloadMismatch,
		CodeUnknownImplementation,
		CodeMissingRequiredPrincipleLab,
		CodeMissingRequiredAdapter,
		CodeDependencyIncomplete,
		CodeOrphanedLab,
		CodeStatusVocabularyMismatch,
		CodeAliasCycle,
		CodeReleaseScopeIncomplete,
		CodeReleaseFamilyMismatch,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diagnostic codes = %#v, want %#v", got, want)
	}
}

func TestValidateReportsSemanticDiagnostics(t *testing.T) {
	tests := []struct {
		name   string
		want   string
		mutate func(*catalog.Catalog)
	}{
		{
			name: "invalid stable ID",
			want: CodeInvalidStableID,
			mutate: func(c *catalog.Catalog) {
				family := c.Scope.Families[0]
				family.ID = "Invalid_Family"
				c.Scope.Families[0] = family
				c.Scope.Cases[0].PrimaryFamily = family.ID
				manifest := c.Cases["case-a"]
				manifest.PrimaryFamily = family.ID
				c.Cases[manifest.ID] = manifest
			},
		},
		{
			name: "duplicate scope family",
			want: CodeDuplicateScopeFamily,
			mutate: func(c *catalog.Catalog) {
				c.Scope.Families = append(c.Scope.Families, c.Scope.Families[0])
			},
		},
		{
			name: "duplicate scope case",
			want: CodeDuplicateScopeCase,
			mutate: func(c *catalog.Catalog) {
				c.Scope.Cases = append(c.Scope.Cases, c.Scope.Cases[0])
			},
		},
		{
			name: "case outside scope",
			want: CodeCaseOutsideScope,
			mutate: func(c *catalog.Catalog) {
				c.Cases["case-outside"] = catalog.CaseManifest{
					ID:            "case-outside",
					PrimaryFamily: "addressing-traffic",
					Status:        catalog.LifecycleStatusDraft,
				}
			},
		},
		{
			name: "unknown family",
			want: CodeUnknownFamily,
			mutate: func(c *catalog.Catalog) {
				manifest := c.Cases["case-a"]
				manifest.PrimaryFamily = "family-missing"
				c.Cases[manifest.ID] = manifest
			},
		},
		{
			name: "unknown reference",
			want: CodeUnknownReference,
			mutate: func(c *catalog.Catalog) {
				manifest := c.Cases["case-a"]
				manifest.Principles = []string{"principle-missing"}
				c.Cases[manifest.ID] = manifest
			},
		},
		{
			name: "globally duplicate claim ID",
			want: CodeDuplicateClaimID,
			mutate: func(c *catalog.Catalog) {
				manifest := c.Principles["principle-a"]
				manifest.Claims[0].ID = "claim-case-a"
				manifest.EvidenceRequirements[0].Claim = "claim-case-a"
				c.Principles[manifest.ID] = manifest
				lab := c.Labs["primitive-lab-a"]
				lab.PrincipleBindings[0].Claim = "claim-case-a"
				c.Labs[lab.ID] = lab
			},
		},
		{
			name: "invalid source URL",
			want: CodeInvalidSourceURL,
			mutate: func(c *catalog.Catalog) {
				source := c.Sources["source-a"]
				source.URL = "http://example.com/source"
				c.Sources[source.ID] = source
			},
		},
		{
			name: "invalid source date",
			want: CodeInvalidSourceDate,
			mutate: func(c *catalog.Catalog) {
				source := c.Sources["source-a"]
				source.AccessedAt = "2026-02-30"
				c.Sources[source.ID] = source
			},
		},
		{
			name: "dangling source",
			want: CodeDanglingSource,
			mutate: func(c *catalog.Catalog) {
				manifest := c.Cases["case-a"]
				manifest.Sources = []string{"source-missing"}
				c.Cases[manifest.ID] = manifest
			},
		},
		{
			name: "missing case binding",
			want: CodeMissingCaseBinding,
			mutate: func(c *catalog.Catalog) {
				lab := c.Labs["scenario-lab-a"]
				lab.CaseBindings = nil
				c.Labs[lab.ID] = lab
			},
		},
		{
			name: "missing principle binding",
			want: CodeMissingPrincipleBinding,
			mutate: func(c *catalog.Catalog) {
				lab := c.Labs["primitive-lab-a"]
				lab.PrincipleBindings = nil
				c.Labs[lab.ID] = lab
			},
		},
		{
			name: "duplicate binding ID",
			want: CodeDuplicateBindingID,
			mutate: func(c *catalog.Catalog) {
				lab := c.Labs["scenario-lab-a"]
				lab.CaseBindings = append(lab.CaseBindings, lab.CaseBindings[0])
				c.Labs[lab.ID] = lab
			},
		},
		{
			name: "duplicate required run",
			want: CodeDuplicateRequiredRun,
			mutate: func(c *catalog.Catalog) {
				lab := c.Labs["scenario-lab-a"]
				lab.RequiredRuns = append(lab.RequiredRuns, lab.RequiredRuns[0])
				c.Labs[lab.ID] = lab
			},
		},
		{
			name: "empty run binding",
			want: CodeInvalidRunBinding,
			mutate: func(c *catalog.Catalog) {
				lab := c.Labs["scenario-lab-a"]
				lab.RequiredRuns[0].Binding = ""
				c.Labs[lab.ID] = lab
			},
		},
		{
			name: "ambiguous run binding",
			want: CodeInvalidRunBinding,
			mutate: func(c *catalog.Catalog) {
				lab := c.Labs["scenario-lab-a"]
				lab.PrincipleBindings = []catalog.PrincipleBinding{{
					ID:          lab.CaseBindings[0].ID,
					PrincipleID: "principle-a",
					Claim:       "claim-principle-a",
					Workload:    "workload-a",
					Assertions:  []string{"assertion-a"},
				}}
				c.Labs[lab.ID] = lab
			},
		},
		{
			name: "absent binding claim",
			want: CodeForeignClaim,
			mutate: func(c *catalog.Catalog) {
				lab := c.Labs["scenario-lab-a"]
				lab.CaseBindings[0].Claim = "claim-missing"
				c.Labs[lab.ID] = lab
			},
		},
		{
			name: "claim owned by another entity",
			want: CodeForeignClaim,
			mutate: func(c *catalog.Catalog) {
				lab := c.Labs["scenario-lab-a"]
				lab.CaseBindings[0].Claim = "claim-principle-a"
				c.Labs[lab.ID] = lab
			},
		},
		{
			name: "unused binding",
			want: CodeUnusedBinding,
			mutate: func(c *catalog.Catalog) {
				lab := c.Labs["scenario-lab-a"]
				lab.CaseBindings = append(lab.CaseBindings, catalog.CaseBinding{
					ID:         "binding-unused",
					CaseID:     "case-a",
					Claim:      "claim-case-a",
					Workload:   "workload-a",
					Assertions: []string{"assertion-a"},
				})
				c.Labs[lab.ID] = lab
			},
		},
		{
			name: "binding workload mismatch",
			want: CodeBindingWorkloadMismatch,
			mutate: func(c *catalog.Catalog) {
				lab := c.Labs["scenario-lab-a"]
				lab.RequiredRuns[0].Workload = "workload-other"
				c.Labs[lab.ID] = lab
			},
		},
		{
			name: "unknown baseline implementation",
			want: CodeUnknownImplementation,
			mutate: func(c *catalog.Catalog) {
				lab := c.Labs["scenario-lab-a"]
				lab.RequiredRuns[0].Baseline = "implementation-missing"
				c.Labs[lab.ID] = lab
			},
		},
		{
			name: "unknown variant implementation",
			want: CodeUnknownImplementation,
			mutate: func(c *catalog.Catalog) {
				lab := c.Labs["scenario-lab-a"]
				lab.RequiredRuns[0].Variants = []string{"implementation-missing"}
				c.Labs[lab.ID] = lab
			},
		},
		{
			name: "required principle without primitive lab",
			want: CodeMissingRequiredPrincipleLab,
			mutate: func(c *catalog.Catalog) {
				principle := c.Principles["principle-a"]
				principle.Labs = nil
				c.Principles[principle.ID] = principle
			},
		},
		{
			name: "missing required adapter",
			want: CodeMissingRequiredAdapter,
			mutate: func(c *catalog.Catalog) {
				delete(c.Adapters, "adapter-a")
			},
		},
		{
			name: "incomplete dependency",
			want: CodeDependencyIncomplete,
			mutate: func(c *catalog.Catalog) {
				principle := c.Principles["principle-a"]
				principle.Status = catalog.LifecycleStatusDraft
				c.Principles[principle.ID] = principle
			},
		},
		{
			name: "orphaned lab",
			want: CodeOrphanedLab,
			mutate: func(c *catalog.Catalog) {
				lab := c.Labs["scenario-lab-a"]
				lab.ID = "scenario-lab-orphan"
				lab.CaseBindings[0].ID = "binding-orphan"
				lab.RequiredRuns[0].ID = "run-orphan"
				lab.RequiredRuns[0].Binding = "binding-orphan"
				c.Labs[lab.ID] = lab
			},
		},
		{
			name: "run status used as lifecycle status",
			want: CodeStatusVocabularyMismatch,
			mutate: func(c *catalog.Catalog) {
				manifest := c.Cases["case-a"]
				manifest.Status = catalog.LifecycleStatus("passed")
				c.Cases[manifest.ID] = manifest
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := validCatalog()
			test.mutate(c)
			report := Validate(c, ModeDevelopment)
			assertDiagnosticCode(t, report.Diagnostics, test.want)
		})
	}
}

func TestValidateRejectsForeignClaimOnUnusedBinding(t *testing.T) {
	c := validCatalog()
	lab := c.Labs["scenario-lab-a"]
	lab.CaseBindings = append(lab.CaseBindings, catalog.CaseBinding{
		ID:         "binding-unused-foreign",
		CaseID:     "case-a",
		Claim:      "claim-missing",
		Workload:   "workload-a",
		Assertions: []string{"assertion-a"},
	})
	c.Labs[lab.ID] = lab

	diagnostics := Validate(c, ModeDevelopment).Diagnostics
	assertDiagnosticCode(t, diagnostics, CodeForeignClaim)
	assertDiagnosticCode(t, diagnostics, CodeUnusedBinding)
}

func TestValidateCompleteContractForEveryEntityKind(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.Catalog)
	}{
		{
			name: "case",
			mutate: func(c *catalog.Catalog) {
				manifest := c.Cases["case-a"]
				manifest.Claims = nil
				c.Cases[manifest.ID] = manifest
			},
		},
		{
			name: "principle",
			mutate: func(c *catalog.Catalog) {
				manifest := c.Principles["principle-a"]
				manifest.EvidenceRequirements = nil
				c.Principles[manifest.ID] = manifest
			},
		},
		{
			name: "lab",
			mutate: func(c *catalog.Catalog) {
				manifest := c.Labs["scenario-lab-a"]
				manifest.RequiredRuns = nil
				c.Labs[manifest.ID] = manifest
			},
		},
		{
			name: "adapter",
			mutate: func(c *catalog.Catalog) {
				manifest := c.Adapters["adapter-a"]
				manifest.Interface = ""
				c.Adapters[manifest.ID] = manifest
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := validCatalog()
			test.mutate(c)
			assertDiagnosticCode(t, Validate(c, ModeDevelopment).Diagnostics, CodeCompleteContractEmpty)
		})
	}
}

func TestValidateCompleteContractRejectsEmptyNestedFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.Catalog)
	}{
		{
			name: "claim statement",
			mutate: func(c *catalog.Catalog) {
				manifest := c.Cases["case-a"]
				manifest.Claims[0].Statement = "  "
				c.Cases[manifest.ID] = manifest
			},
		},
		{
			name: "case dimension",
			mutate: func(c *catalog.Catalog) {
				manifest := c.Cases["case-a"]
				manifest.Dimensions = []catalog.DimensionID{" "}
				c.Cases[manifest.ID] = manifest
			},
		},
		{
			name: "principle dimension",
			mutate: func(c *catalog.Catalog) {
				manifest := c.Principles["principle-a"]
				manifest.Dimensions = []catalog.DimensionID{" "}
				c.Principles[manifest.ID] = manifest
			},
		},
		{
			name: "evidence claim",
			mutate: func(c *catalog.Catalog) {
				manifest := c.Cases["case-a"]
				manifest.EvidenceRequirements[0].Claim = " "
				c.Cases[manifest.ID] = manifest
			},
		},
		{
			name: "binding assertions",
			mutate: func(c *catalog.Catalog) {
				lab := c.Labs["scenario-lab-a"]
				lab.CaseBindings[0].Assertions = nil
				c.Labs[lab.ID] = lab
			},
		},
		{
			name: "required run variants",
			mutate: func(c *catalog.Catalog) {
				lab := c.Labs["scenario-lab-a"]
				lab.RequiredRuns[0].Variants = nil
				c.Labs[lab.ID] = lab
			},
		},
		{
			name: "required run faults",
			mutate: func(c *catalog.Catalog) {
				lab := c.Labs["scenario-lab-a"]
				lab.RequiredRuns[0].Faults = nil
				c.Labs[lab.ID] = lab
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := validCatalog()
			test.mutate(c)
			assertDiagnosticCode(t, Validate(c, ModeDevelopment).Diagnostics, CodeCompleteContractEmpty)
		})
	}
}

func TestValidateCompleteRunRequiresFaultSchedulePresenceButAllowsEmpty(t *testing.T) {
	tests := []struct {
		name        string
		faults      []string
		wantMissing bool
	}{
		{name: "omitted", faults: nil, wantMissing: true},
		{name: "explicit empty", faults: []string{}, wantMissing: false},
		{name: "nonempty", faults: []string{"fault-a"}, wantMissing: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := validCatalog()
			lab := c.Labs["primitive-lab-a"]
			lab.RequiredRuns[0].Faults = test.faults
			c.Labs[lab.ID] = lab
			diagnostics := Validate(c, ModeDevelopment).Diagnostics
			if test.wantMissing {
				assertDiagnosticCode(t, diagnostics, CodeCompleteContractEmpty)
				return
			}
			assertNoDiagnosticCode(t, diagnostics, CodeCompleteContractEmpty)
		})
	}
}

func TestValidateUsesDedicatedLabMetricGrammar(t *testing.T) {
	c := validCatalog()
	lab := c.Labs["primitive-lab-a"]
	lab.Metrics = []string{
		"requests.total",
		"probe_initial",
		"tokens-remaining",
		"reference.mismatch_count",
	}
	c.Labs[lab.ID] = lab
	assertNoDiagnosticCode(t, Validate(c, ModeDevelopment).Diagnostics, CodeInvalidStableID)

	invalid := []string{
		"",
		"Requests.total",
		".requests",
		"requests.",
		"requests..total",
		"requests__total",
		"requests.-total",
	}
	for _, metric := range invalid {
		t.Run(metric, func(t *testing.T) {
			c := validCatalog()
			lab := c.Labs["primitive-lab-a"]
			lab.Metrics = []string{metric}
			c.Labs[lab.ID] = lab
			assertDiagnosticCode(t, Validate(c, ModeDevelopment).Diagnostics, CodeInvalidStableID)
		})
	}
}

func TestLabMetricGrammarDoesNotRelaxGraphStableIDs(t *testing.T) {
	c := validCatalog()
	lab := c.Labs["primitive-lab-a"]
	lab.PrincipleBindings[0].ID = "binding.with-dot"
	lab.RequiredRuns[0].Binding = "binding.with-dot"
	c.Labs[lab.ID] = lab
	assertDiagnosticCode(t, Validate(c, ModeDevelopment).Diagnostics, CodeInvalidStableID)
}

func TestValidateAllowsDraftAdapterToOmitConditionalFields(t *testing.T) {
	c := validCatalog()
	lab := c.Labs["scenario-lab-a"]
	lab.RequiredRuns[0].Adapters[0].Required = false
	c.Labs[lab.ID] = lab
	adapter := c.Adapters["adapter-a"]
	adapter.Status = catalog.LifecycleStatusDraft
	adapter.Interface = ""
	adapter.Runtime = ""
	adapter.Sources = nil
	c.Adapters[adapter.ID] = adapter

	report := Validate(c, ModeDevelopment)
	if len(report.Diagnostics) != 0 {
		t.Fatalf("draft adapter diagnostics = %#v, want conditional fields omitted", report.Diagnostics)
	}
}

func TestValidateResolvesAliasesBeforeReferences(t *testing.T) {
	c := validCatalog()
	aliases, err := catalog.NewAliasSet([]catalog.Alias{
		{Kind: catalog.EntityKindPrinciple, From: "principle-old", To: "principle-a"},
	})
	if err != nil {
		t.Fatalf("NewAliasSet() error = %v", err)
	}
	c.Aliases = aliases
	manifest := c.Cases["case-a"]
	manifest.Principles = []string{"principle-old"}
	c.Cases[manifest.ID] = manifest

	report := Validate(c, ModeDevelopment)
	assertNoDiagnosticCode(t, report.Diagnostics, CodeUnknownReference)
	assertNoDiagnosticCode(t, report.Diagnostics, CodeDependencyIncomplete)
	if !reflect.DeepEqual(report.Coverage.RequiredPrinciples, []string{"principle-a"}) {
		t.Fatalf("required principles = %#v, want canonical ID", report.Coverage.RequiredPrinciples)
	}
}

func TestValidateResolvesAllFourAliasKindsAndCrossKindNames(t *testing.T) {
	c := validCatalog()
	aliases, err := catalog.NewAliasSet([]catalog.Alias{
		{Kind: catalog.EntityKindCase, From: "owner-old", To: "case-a"},
		{Kind: catalog.EntityKindPrinciple, From: "owner-old", To: "principle-a"},
		{Kind: catalog.EntityKindPrinciple, From: "principle-legacy", To: "principle-old"},
		{Kind: catalog.EntityKindPrinciple, From: "principle-old", To: "principle-a"},
		{Kind: catalog.EntityKindLab, From: "scenario-old", To: "scenario-lab-a"},
		{Kind: catalog.EntityKindLab, From: "primitive-old", To: "primitive-lab-a"},
		{Kind: catalog.EntityKindSource, From: "source-old", To: "source-a"},
	})
	if err != nil {
		t.Fatalf("NewAliasSet() error = %v", err)
	}
	c.Aliases = aliases

	caseManifest := c.Cases["case-a"]
	caseManifest.Principles = []string{"principle-legacy"}
	caseManifest.Labs = []string{"scenario-old"}
	caseManifest.EvidenceRequirements[0].Lab = "scenario-old"
	caseManifest.Sources = []string{"source-old"}
	c.Cases[caseManifest.ID] = caseManifest

	principle := c.Principles["principle-a"]
	principle.Labs = []string{"primitive-old"}
	principle.EvidenceRequirements[0].Lab = "primitive-old"
	principle.Sources = []string{"source-old"}
	c.Principles[principle.ID] = principle

	scenario := c.Labs["scenario-lab-a"]
	scenario.CaseBindings[0].CaseID = "owner-old"
	scenario.Sources = []string{"source-old"}
	c.Labs[scenario.ID] = scenario

	primitive := c.Labs["primitive-lab-a"]
	primitive.PrincipleBindings[0].PrincipleID = "owner-old"
	primitive.Sources = []string{"source-old"}
	c.Labs[primitive.ID] = primitive

	adapter := c.Adapters["adapter-a"]
	adapter.Sources = []string{"source-old"}
	c.Adapters[adapter.ID] = adapter

	report := Validate(c, ModeDevelopment)
	if len(report.Diagnostics) != 0 {
		t.Fatalf("Validate() diagnostics = %#v, want aliases resolved by kind", report.Diagnostics)
	}
	wantCoverage := Coverage{
		BaselineTotal:         1,
		CompleteTotal:         1,
		MissingCaseIDs:        []string{},
		UnexpectedCaseIDs:     []string{},
		Families:              []FamilyCoverage{{ID: "addressing-traffic", Required: 1, Complete: 1}},
		RequiredPrinciples:    []string{"principle-a"},
		RequiredScenarioLabs:  []string{"scenario-lab-a"},
		RequiredPrimitiveLabs: []string{"primitive-lab-a"},
		RequiredAdapters:      []string{"adapter-a"},
	}
	if !reflect.DeepEqual(report.Coverage, wantCoverage) {
		t.Fatalf("coverage = %#v, want canonical closure %#v", report.Coverage, wantCoverage)
	}
}

func TestValidateChecksUnusedAliasDefinitionStableIDs(t *testing.T) {
	tests := []struct {
		name  string
		alias catalog.Alias
	}{
		{
			name: "invalid from",
			alias: catalog.Alias{
				Kind: catalog.EntityKindCase,
				From: "Invalid_Alias",
				To:   "case-a",
			},
		},
		{
			name: "invalid to",
			alias: catalog.Alias{
				Kind: catalog.EntityKindCase,
				From: "case-old",
				To:   "Invalid_Target",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := validCatalog()
			aliases, err := catalog.NewAliasSet([]catalog.Alias{test.alias})
			if err != nil {
				t.Fatalf("NewAliasSet() error = %v", err)
			}
			c.Aliases = aliases
			assertDiagnosticCode(t, Validate(c, ModeDevelopment).Diagnostics, CodeInvalidStableID)
		})
	}
}

func TestValidateDistinguishesMissingAndIncompleteDependencies(t *testing.T) {
	t.Run("draft primitive lab is incomplete but not missing", func(t *testing.T) {
		c := validCatalog()
		lab := c.Labs["primitive-lab-a"]
		lab.Status = catalog.LifecycleStatusDraft
		c.Labs[lab.ID] = lab
		diagnostics := Validate(c, ModeDevelopment).Diagnostics
		assertDiagnosticCode(t, diagnostics, CodeDependencyIncomplete)
		assertNoDiagnosticCode(t, diagnostics, CodeMissingRequiredPrincipleLab)
	})

	t.Run("draft adapter is incomplete but not missing", func(t *testing.T) {
		c := validCatalog()
		adapter := c.Adapters["adapter-a"]
		adapter.Status = catalog.LifecycleStatusDraft
		c.Adapters[adapter.ID] = adapter
		diagnostics := Validate(c, ModeDevelopment).Diagnostics
		assertDiagnosticCode(t, diagnostics, CodeDependencyIncomplete)
		assertNoDiagnosticCode(t, diagnostics, CodeMissingRequiredAdapter)
	})
}

func TestValidateStableIDRegexUsesTheFrozenLiteral(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.Catalog)
	}{
		{
			name: "single letter implementation definition and reference",
			mutate: func(c *catalog.Catalog) {
				lab := c.Labs["scenario-lab-a"]
				lab.Implementations[0] = "a"
				lab.RequiredRuns[0].Baseline = "a"
				c.Labs[lab.ID] = lab
			},
		},
		{
			name: "trailing hyphen in definition and reference",
			mutate: func(c *catalog.Catalog) {
				c.Scope.Families[0].ID = "a-"
				c.Scope.Cases[0].PrimaryFamily = "a-"
				manifest := c.Cases["case-a"]
				manifest.PrimaryFamily = "a-"
				c.Cases[manifest.ID] = manifest
			},
		},
		{
			name: "consecutive hyphens in definition and reference",
			mutate: func(c *catalog.Catalog) {
				lab := c.Labs["scenario-lab-a"]
				lab.Implementations[0] = "a--b"
				lab.RequiredRuns[0].Baseline = "a--b"
				c.Labs[lab.ID] = lab
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := validCatalog()
			test.mutate(c)
			assertNoDiagnosticCode(t, Validate(c, ModeDevelopment).Diagnostics, CodeInvalidStableID)
		})
	}
}

func TestValidateRejectsStableIDsOutsideTheFrozenRegex(t *testing.T) {
	for _, invalidID := range []string{"", "A", "1a", "_a", "中文"} {
		t.Run(fmt.Sprintf("%q", invalidID), func(t *testing.T) {
			c := validCatalog()
			lab := c.Labs["scenario-lab-a"]
			lab.Implementations[0] = invalidID
			lab.RequiredRuns[0].Baseline = invalidID
			c.Labs[lab.ID] = lab
			assertDiagnosticCode(t, Validate(c, ModeDevelopment).Diagnostics, CodeInvalidStableID)
		})
	}
}

func TestValidateSourceURLAndDateBoundaries(t *testing.T) {
	tests := []struct {
		name string
		url  string
		date string
		want string
	}{
		{name: "HTTPS without hostname", url: "https://:443/path", date: "2026-07-14", want: CodeInvalidSourceURL},
		{name: "relative URL", url: "/source", date: "2026-07-14", want: CodeInvalidSourceURL},
		{name: "non padded date", url: "https://example.com/source", date: "2026-7-14", want: CodeInvalidSourceDate},
		{name: "impossible date", url: "https://example.com/source", date: "2026-02-30", want: CodeInvalidSourceDate},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := validCatalog()
			source := c.Sources["source-a"]
			source.URL = test.url
			source.AccessedAt = test.date
			c.Sources[source.ID] = source
			assertDiagnosticCode(t, Validate(c, ModeDevelopment).Diagnostics, test.want)
		})
	}
}

func TestValidateRejectsWrongBindingKindsAndReverseOwnerEdges(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*catalog.LabManifest)
	}{
		{
			name: "scenario uses principle binding",
			mutate: func(lab *catalog.LabManifest) {
				lab.CaseBindings = nil
				lab.PrincipleBindings = []catalog.PrincipleBinding{{
					ID:          "binding-case-a",
					PrincipleID: "principle-a",
					Claim:       "claim-principle-a",
					Workload:    "workload-a",
					Assertions:  []string{"assertion-a"},
				}}
			},
		},
		{
			name: "scenario binding points at a different owner",
			mutate: func(lab *catalog.LabManifest) {
				lab.CaseBindings[0].CaseID = "case-other"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := validCatalog()
			lab := c.Labs["scenario-lab-a"]
			test.mutate(&lab)
			c.Labs[lab.ID] = lab
			assertDiagnosticCode(t, Validate(c, ModeDevelopment).Diagnostics, CodeOrphanedLab)
		})
	}
}

func TestValidateMapsUnknownLabKindToUnknownReference(t *testing.T) {
	c := validCatalog()
	lab := c.Labs["scenario-lab-a"]
	lab.Kind = catalog.LabKind("hybrid")
	c.Labs[lab.ID] = lab
	assertDiagnosticCode(t, Validate(c, ModeDevelopment).Diagnostics, CodeUnknownReference)
}

func TestValidateDoesNotTreatReciprocalOwnerLabEdgesAsCycles(t *testing.T) {
	report := Validate(validCatalog(), ModeDevelopment)
	if len(report.Diagnostics) != 0 {
		t.Fatalf("Validate() diagnostics = %#v, want none", report.Diagnostics)
	}
}

func TestValidateModeContracts(t *testing.T) {
	c := validCatalog()
	delete(c.Cases, "case-a")

	development := Validate(c, ModeDevelopment)
	assertNoDiagnosticCode(t, development.Diagnostics, CodeReleaseScopeIncomplete)

	release := Validate(c, ModeRelease)
	assertDiagnosticCode(t, release.Diagnostics, CodeReleaseScopeIncomplete)
	if !reflect.DeepEqual(release.Coverage.MissingCaseIDs, []string{"case-a"}) {
		t.Fatalf("release missing cases = %#v, want [case-a]", release.Coverage.MissingCaseIDs)
	}
}

func TestValidateReleaseFamilyMismatch(t *testing.T) {
	c := validCatalog()
	c.Scope.Families = append(c.Scope.Families, catalog.ScopeFamily{ID: "distributed-storage", Title: "Distributed Storage"})
	manifest := c.Cases["case-a"]
	manifest.PrimaryFamily = "distributed-storage"
	c.Cases[manifest.ID] = manifest

	development := Validate(c, ModeDevelopment)
	assertNoDiagnosticCode(t, development.Diagnostics, CodeReleaseFamilyMismatch)

	release := Validate(c, ModeRelease)
	assertDiagnosticCode(t, release.Diagnostics, CodeReleaseFamilyMismatch)
}

func TestValidateDiagnosticsAreDeterministicAndSorted(t *testing.T) {
	c := validCatalog()
	c.Scope.Families = append(c.Scope.Families, c.Scope.Families[0])
	source := c.Sources["source-a"]
	source.URL = "http://example.com"
	source.AccessedAt = "not-a-date"
	c.Sources[source.ID] = source

	first := Validate(c, ModeDevelopment).Diagnostics
	second := Validate(c, ModeDevelopment).Diagnostics
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("diagnostics are nondeterministic:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if !sort.SliceIsSorted(first, func(i, j int) bool {
		return diagnosticLess(first[i], first[j])
	}) {
		t.Fatalf("diagnostics are not sorted: %#v", first)
	}
	for _, diagnostic := range first {
		if diagnostic.Severity != "error" {
			t.Errorf("diagnostic severity = %q, want error: %#v", diagnostic.Severity, diagnostic)
		}
		if strings.TrimSpace(diagnostic.Message) == "" {
			t.Errorf("diagnostic message is empty: %#v", diagnostic)
		}
	}
}

func diagnosticLess(left, right Diagnostic) bool {
	if left.Code != right.Code {
		return left.Code < right.Code
	}
	if left.Path != right.Path {
		return left.Path < right.Path
	}
	if left.EntityID != right.EntityID {
		return left.EntityID < right.EntityID
	}
	return left.Message < right.Message
}

func assertDiagnosticCode(t *testing.T, diagnostics []Diagnostic, want string) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == want {
			return
		}
	}
	t.Fatalf("diagnostic codes = %#v, want %q; diagnostics=%#v", diagnosticCodes(diagnostics), want, diagnostics)
}

func assertNoDiagnosticCode(t *testing.T, diagnostics []Diagnostic, unwanted string) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == unwanted {
			t.Fatalf("unexpected diagnostic %q: %#v", unwanted, diagnostic)
		}
	}
}

func diagnosticCodes(diagnostics []Diagnostic) []string {
	codes := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		codes = append(codes, diagnostic.Code)
	}
	return codes
}

func validCatalog() *catalog.Catalog {
	return &catalog.Catalog{
		Scope: catalog.Scope{
			SchemaVersion: 1,
			Families: []catalog.ScopeFamily{{
				ID:    "addressing-traffic",
				Title: "Family A",
			}},
			Cases: []catalog.ScopeCase{{
				ID:            "case-a",
				Title:         "Case A",
				PrimaryFamily: "addressing-traffic",
			}},
		},
		Sources: map[string]catalog.SourceRecord{
			"source-a": {
				ID:          "source-a",
				Title:       "Source A",
				URL:         "https://example.com/source-a",
				AccessedAt:  "2026-07-14",
				Kind:        "official-documentation",
				LicenseNote: "Used for tests.",
			},
		},
		Cases: map[string]catalog.CaseManifest{
			"case-a": {
				SchemaVersion:     1,
				ID:                "case-a",
				Title:             "Case A",
				PrimaryFamily:     "addressing-traffic",
				SecondaryFamilies: []string{},
				Required:          true,
				Status:            catalog.LifecycleStatusComplete,
				Dimensions:        []catalog.DimensionID{"problem-slo"},
				Principles:        []string{"principle-a"},
				Claims: []catalog.Claim{{
					ID:        "claim-case-a",
					Statement: "Case claim.",
				}},
				Labs: []string{"scenario-lab-a"},
				EvidenceRequirements: []catalog.EvidenceRequirement{{
					Claim: "claim-case-a",
					Lab:   "scenario-lab-a",
				}},
				Sources: []string{"source-a"},
			},
		},
		Principles: map[string]catalog.PrincipleManifest{
			"principle-a": {
				SchemaVersion: 1,
				ID:            "principle-a",
				Title:         "Principle A",
				Required:      true,
				Status:        catalog.LifecycleStatusComplete,
				Dimensions:    []catalog.DimensionID{"contracts-data-invariants"},
				Claims: []catalog.Claim{{
					ID:        "claim-principle-a",
					Statement: "Principle claim.",
				}},
				Labs: []string{"primitive-lab-a"},
				EvidenceRequirements: []catalog.EvidenceRequirement{{
					Claim: "claim-principle-a",
					Lab:   "primitive-lab-a",
				}},
				Sources: []string{"source-a"},
			},
		},
		Labs: map[string]catalog.LabManifest{
			"scenario-lab-a": {
				SchemaVersion:   1,
				ID:              "scenario-lab-a",
				Kind:            catalog.LabKindScenario,
				Required:        true,
				Status:          catalog.LifecycleStatusComplete,
				Implementations: []string{"implementation-a", "implementation-b"},
				CaseBindings: []catalog.CaseBinding{{
					ID:         "binding-case-a",
					CaseID:     "case-a",
					Claim:      "claim-case-a",
					Workload:   "workload-a",
					Assertions: []string{"assertion-case-a"},
				}},
				RequiredRuns: []catalog.RequiredRun{{
					ID:       "run-case-a",
					Binding:  "binding-case-a",
					Baseline: "implementation-a",
					Variants: []string{"implementation-b"},
					Workload: "workload-a",
					Faults:   []string{"fault-a", "fault-b"},
					Adapters: []catalog.AdapterRequirement{{ID: "adapter-a", Required: true}},
				}},
				Metrics: []string{"metric-a"},
				Sources: []string{"source-a"},
			},
			"primitive-lab-a": {
				SchemaVersion:   1,
				ID:              "primitive-lab-a",
				Kind:            catalog.LabKindPrimitive,
				Required:        true,
				Status:          catalog.LifecycleStatusComplete,
				Implementations: []string{"primitive-a", "primitive-b"},
				PrincipleBindings: []catalog.PrincipleBinding{{
					ID:          "binding-principle-a",
					PrincipleID: "principle-a",
					Claim:       "claim-principle-a",
					Workload:    "primitive-workload-a",
					Assertions:  []string{"assertion-principle-a"},
				}},
				RequiredRuns: []catalog.RequiredRun{{
					ID:       "run-principle-a",
					Binding:  "binding-principle-a",
					Baseline: "primitive-a",
					Variants: []string{"primitive-b"},
					Workload: "primitive-workload-a",
					Faults:   []string{"primitive-fault-a"},
				}},
				Metrics: []string{"primitive-metric-a"},
				Sources: []string{"source-a"},
			},
		},
		Adapters: map[string]catalog.AdapterManifest{
			"adapter-a": {
				SchemaVersion: 1,
				ID:            "adapter-a",
				Title:         "Adapter A",
				Status:        catalog.LifecycleStatusComplete,
				Interface:     "interface-a",
				Runtime:       "docker",
				Sources:       []string{"source-a"},
			},
		},
	}
}
