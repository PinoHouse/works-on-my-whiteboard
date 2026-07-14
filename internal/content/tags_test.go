package content

import (
	"testing"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
)

func TestValidateCaseAcceptsAllExactClaimMarkerClasses(t *testing.T) {
	manifest, repository := taggedCaseFixture()
	markdown := validCaseMarkdown(map[string]string{
		"表面题目": longProse(140) + `

[ASSUMED:assumed-claim] 原因：题目没有给出该值。变化影响：容量模型需要重算。

[DEDUCED:deduced-claim] 该结论由不变量推导。

[MEASURED:measured-claim] 该结论由 required run 验证。

[SOURCED:sourced-claim:source-one] 该结论来自登记来源。
`,
	})
	result := ValidateCase("cases/case-one/README.md", markdown, manifest, repository)
	for _, code := range []string{
		"invalid_claim_marker",
		"unknown_claim",
		"missing_claim_marker",
		"conflicting_claim_class",
		"assumption_context_missing",
		"measured_claim_unbound",
		"sourced_claim_invalid",
	} {
		assertNoDiagnosticCode(t, result, code)
	}
}

func TestValidateCaseRejectsMalformedUnknownUnusedAndConflictingClaims(t *testing.T) {
	t.Run("malformed", func(t *testing.T) {
		manifest := catalog.CaseManifest{
			ID:     "case-one",
			Status: catalog.LifecycleStatusComplete,
			Claims: []catalog.Claim{{ID: "claim-one"}},
		}
		markdown := validCaseMarkdown(map[string]string{
			"表面题目": longProse(140) + " [DEDUCED claim-one]",
		})
		result := ValidateCase("cases/case-one/README.md", markdown, manifest, emptyCatalog())
		assertHasDiagnosticCode(t, result, "invalid_claim_marker")
	})

	t.Run("unknown", func(t *testing.T) {
		markdown := validCaseMarkdown(map[string]string{
			"表面题目": longProse(140) + " [DEDUCED:unknown-claim]",
		})
		result := ValidateCase("cases/case-one/README.md", markdown, catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete}, emptyCatalog())
		assertHasDiagnosticCode(t, result, "unknown_claim")
	})

	t.Run("unused", func(t *testing.T) {
		manifest := catalog.CaseManifest{
			ID:     "case-one",
			Status: catalog.LifecycleStatusComplete,
			Claims: []catalog.Claim{{ID: "claim-one"}},
		}
		result := ValidateCase("cases/case-one/README.md", validCaseMarkdown(nil), manifest, emptyCatalog())
		assertHasDiagnosticCode(t, result, "missing_claim_marker")
	})

	t.Run("conflicting_classes", func(t *testing.T) {
		manifest := catalog.CaseManifest{
			ID:     "case-one",
			Status: catalog.LifecycleStatusComplete,
			Claims: []catalog.Claim{{ID: "claim-one"}},
		}
		markdown := validCaseMarkdown(map[string]string{
			"表面题目": longProse(140) + `

[ASSUMED:claim-one] 原因：缺少输入。变化影响：需要重新选择参数。

[DEDUCED:claim-one] 同一主张不能再标成推导结论。
`,
		})
		result := ValidateCase("cases/case-one/README.md", markdown, manifest, emptyCatalog())
		assertHasDiagnosticCode(t, result, "conflicting_claim_class")
	})
}

func TestValidateCaseTreatsCaseVariantsAndWrongArityAsMalformedMarkers(t *testing.T) {
	manifest := catalog.CaseManifest{
		ID:     "case-one",
		Status: catalog.LifecycleStatusComplete,
		Claims: []catalog.Claim{{ID: "claim-one"}},
	}
	markers := []string{
		"[assumed:claim-one]",
		"[Assumed:claim-one]",
		"[DEDUCED:]",
		"[DEDUCED:claim-one:extra]",
		"[SOURCED:claim-one]",
		"[MEASURED: claim-one]",
	}
	for _, marker := range markers {
		t.Run(marker, func(t *testing.T) {
			markdown := validCaseMarkdown(map[string]string{"表面题目": longProse(140) + " " + marker})
			result := ValidateCase("cases/case-one/README.md", markdown, manifest, emptyCatalog())
			assertHasDiagnosticCode(t, result, "invalid_claim_marker")
		})
	}
}

func TestClaimCandidateDetectionIsReservedAndCaseInsensitive(t *testing.T) {
	t.Run("unclosed_lowercase_reserved_marker", func(t *testing.T) {
		markdown := validCaseMarkdown(map[string]string{
			"表面题目": longProse(140) + " [deduced:claim-one",
		})
		result := ValidateCase(
			"cases/case-one/README.md",
			markdown,
			catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete},
			emptyCatalog(),
		)
		assertHasDiagnosticCode(t, result, "invalid_claim_marker")
	})

	t.Run("ordinary_uppercase_reference", func(t *testing.T) {
		markdown := validCaseMarkdown(map[string]string{
			"表面题目": longProse(140) + " [RFC:123] [ADR:001] [DEDUCEDLY:note] [ASSUMEDNESS]",
		})
		result := ValidateCase(
			"cases/case-one/README.md",
			markdown,
			catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete},
			emptyCatalog(),
		)
		assertNoDiagnosticCode(t, result, "invalid_claim_marker")
	})

	t.Run("reserved_non_word_delimiters_are_malformed", func(t *testing.T) {
		for _, raw := range []string{
			"[MEASURED-fake-claim]",
			"[SOURCED：fake-claim：source-one]",
		} {
			markers, malformed := scanClaimMarkers(raw)
			if len(markers) != 0 || len(malformed) != 1 || malformed[0] != raw {
				t.Fatalf("scanClaimMarkers(%q) markers=%#v malformed=%#v; want one malformed reserved marker", raw, markers, malformed)
			}
		}
	})
}

func TestOrdinaryUnclosedBracketDoesNotHideLaterMalformedClaimMarker(t *testing.T) {
	markers, malformed := scanClaimMarkers("[note [DEDUCED:claim-one")
	if len(markers) != 0 || len(malformed) != 1 || malformed[0] != "[DEDUCED:claim-one" {
		t.Fatalf("markers=%#v malformed=%#v; want nested unclosed reserved marker reported", markers, malformed)
	}
}

func TestMalformedReservedMarkerDoesNotHideNestedValidClaimMarker(t *testing.T) {
	markers, malformed := scanClaimMarkers("[DEDUCED:bad [DEDUCED:claim-one]")
	if len(malformed) != 1 || malformed[0] != "[DEDUCED:bad [DEDUCED:claim-one]" {
		t.Fatalf("malformed=%#v; want exactly one outer malformed marker", malformed)
	}
	if len(markers) != 1 || markers[0].class != claimClassDeduced || markers[0].claimID != "claim-one" {
		t.Fatalf("markers=%#v; want nested valid DEDUCED claim", markers)
	}
}

func TestValidateCaseAppliesRenderedTextSemanticsBeforeScanningMarkers(t *testing.T) {
	manifest := catalog.CaseManifest{
		ID:     "case-one",
		Status: catalog.LifecycleStatusComplete,
		Claims: []catalog.Claim{{ID: "claim-one"}},
	}
	markdown := validCaseMarkdown(map[string]string{
		"表面题目": longProse(140) + ` \[DEDUCED:claim-one\] TO&#x44;O`,
	})
	result := ValidateCase("cases/case-one/README.md", markdown, manifest, emptyCatalog())
	assertNoDiagnosticCode(t, result, "missing_claim_marker")
	assertHasDiagnosticCode(t, result, "unfinished_marker")
}

func TestHeadingClaimMarkerDoesNotSatisfyDeclaredCoverage(t *testing.T) {
	manifest := catalog.CaseManifest{
		ID:     "case-one",
		Status: catalog.LifecycleStatusComplete,
		Claims: []catalog.Claim{{ID: "claim-one"}},
	}
	markdown := string(validCaseMarkdown(nil)) + "\n### [DEDUCED:claim-one]\n"
	result := ValidateCase("cases/case-one/README.md", []byte(markdown), manifest, emptyCatalog())
	assertHasDiagnosticCode(t, result, "missing_claim_marker")
}

func TestValidateCaseRequiresAssumptionLabelsInContainingBlock(t *testing.T) {
	manifest := catalog.CaseManifest{
		ID:     "case-one",
		Status: catalog.LifecycleStatusComplete,
		Claims: []catalog.Claim{{ID: "claim-one"}},
	}
	for _, body := range []string{
		"[ASSUMED:claim-one] 原因：缺少输入。",
		"[ASSUMED:claim-one] 变化影响：模型变化。",
		"[ASSUMED:claim-one]\n\n原因：标签位于另一个段落。变化影响：模型变化。",
	} {
		markdown := validCaseMarkdown(map[string]string{"表面题目": longProse(140) + "\n\n" + body})
		result := ValidateCase("cases/case-one/README.md", markdown, manifest, emptyCatalog())
		assertHasDiagnosticCode(t, result, "assumption_context_missing")
	}

	markdown := validCaseMarkdown(map[string]string{
		"表面题目": longProse(140) + `

- [ASSUMED:claim-one]
- 原因：另一个列表项。变化影响：仍在另一个列表项。
`,
	})
	result := ValidateCase("cases/case-one/README.md", markdown, manifest, emptyCatalog())
	assertHasDiagnosticCode(t, result, "assumption_context_missing")
}

func TestValidateCaseRequiresMeasuredBindingAndRequiredRun(t *testing.T) {
	baseManifest := catalog.CaseManifest{
		ID:                   "case-one",
		Status:               catalog.LifecycleStatusComplete,
		Claims:               []catalog.Claim{{ID: "measured-claim"}},
		EvidenceRequirements: []catalog.EvidenceRequirement{{Claim: "measured-claim", Lab: "lab-one"}},
	}
	baseLab := catalog.LabManifest{
		ID:   "lab-one",
		Kind: catalog.LabKindScenario,
		CaseBindings: []catalog.CaseBinding{{
			ID:     "binding-one",
			CaseID: "case-one",
			Claim:  "measured-claim",
		}},
		RequiredRuns: []catalog.RequiredRun{{ID: "run-one", Binding: "binding-one"}},
	}
	markdown := validCaseMarkdown(map[string]string{
		"表面题目": longProse(140) + " [MEASURED:measured-claim]",
	})

	tests := []struct {
		name       string
		manifest   catalog.CaseManifest
		repository *catalog.Catalog
	}{
		{
			name: "missing_evidence_requirement",
			manifest: catalog.CaseManifest{
				ID:     "case-one",
				Status: catalog.LifecycleStatusComplete,
				Claims: []catalog.Claim{{ID: "measured-claim"}},
			},
			repository: &catalog.Catalog{Labs: map[string]catalog.LabManifest{"lab-one": baseLab}},
		},
		{
			name:       "missing_lab",
			manifest:   baseManifest,
			repository: emptyCatalog(),
		},
		{
			name:     "missing_binding",
			manifest: baseManifest,
			repository: &catalog.Catalog{Labs: map[string]catalog.LabManifest{
				"lab-one": {ID: "lab-one", Kind: catalog.LabKindScenario, RequiredRuns: baseLab.RequiredRuns},
			}},
		},
		{
			name:     "binding_not_used_by_required_run",
			manifest: baseManifest,
			repository: &catalog.Catalog{Labs: map[string]catalog.LabManifest{
				"lab-one": {
					ID:           "lab-one",
					Kind:         catalog.LabKindScenario,
					CaseBindings: baseLab.CaseBindings,
					RequiredRuns: []catalog.RequiredRun{{ID: "run-one", Binding: "other-binding"}},
				},
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ValidateCase("cases/case-one/README.md", markdown, test.manifest, test.repository)
			assertHasDiagnosticCode(t, result, "measured_claim_unbound")
		})
	}
}

func TestValidatePrincipleResolvesMeasuredClaimThroughPrincipleBinding(t *testing.T) {
	manifest := catalog.PrincipleManifest{
		ID:                   "principle-one",
		Status:               catalog.LifecycleStatusComplete,
		Claims:               []catalog.Claim{{ID: "measured-claim"}},
		EvidenceRequirements: []catalog.EvidenceRequirement{{Claim: "measured-claim", Lab: "lab-one"}},
	}
	repository := emptyCatalog()
	repository.Labs["lab-one"] = catalog.LabManifest{
		ID:   "lab-one",
		Kind: catalog.LabKindPrimitive,
		PrincipleBindings: []catalog.PrincipleBinding{{
			ID:          "binding-one",
			PrincipleID: "principle-one",
			Claim:       "measured-claim",
		}},
		RequiredRuns: []catalog.RequiredRun{{ID: "run-one", Binding: "binding-one"}},
	}
	markdown := validPrincipleMarkdown(map[string]string{
		"定义": longProse(140) + " [MEASURED:measured-claim]",
	})
	result := ValidatePrinciple("principles/principle-one/README.md", markdown, manifest, repository)
	assertNoDiagnosticCode(t, result, "measured_claim_unbound")
}

func TestValidateCaseRejectsMeasuredBindingIDThatIsNotUniqueAcrossBindingKinds(t *testing.T) {
	manifest := catalog.CaseManifest{
		ID:                   "case-one",
		Status:               catalog.LifecycleStatusComplete,
		Claims:               []catalog.Claim{{ID: "measured-claim"}},
		EvidenceRequirements: []catalog.EvidenceRequirement{{Claim: "measured-claim", Lab: "lab-one"}},
	}
	repository := emptyCatalog()
	repository.Labs["lab-one"] = catalog.LabManifest{
		ID:   "lab-one",
		Kind: catalog.LabKindScenario,
		CaseBindings: []catalog.CaseBinding{{
			ID: "binding-one", CaseID: "case-one", Claim: "measured-claim",
		}},
		PrincipleBindings: []catalog.PrincipleBinding{{
			ID: "binding-one", PrincipleID: "principle-one", Claim: "other-claim",
		}},
		RequiredRuns: []catalog.RequiredRun{{ID: "run-one", Binding: "binding-one"}},
	}
	markdown := validCaseMarkdown(map[string]string{
		"表面题目": longProse(140) + " [MEASURED:measured-claim]",
	})
	result := ValidateCase("cases/case-one/README.md", markdown, manifest, repository)
	assertHasDiagnosticCode(t, result, "measured_claim_unbound")
}

func TestValidateCaseRequiresSourcedClaimOwnerClosure(t *testing.T) {
	markdown := validCaseMarkdown(map[string]string{
		"表面题目": longProse(140) + " [SOURCED:sourced-claim:source-one]",
	})
	manifest := catalog.CaseManifest{
		ID:      "case-one",
		Status:  catalog.LifecycleStatusComplete,
		Claims:  []catalog.Claim{{ID: "sourced-claim"}},
		Sources: []string{"source-one"},
	}

	t.Run("source_missing_from_repository", func(t *testing.T) {
		result := ValidateCase("cases/case-one/README.md", markdown, manifest, emptyCatalog())
		assertHasDiagnosticCode(t, result, "sourced_claim_invalid")
	})

	t.Run("source_not_listed_by_owner", func(t *testing.T) {
		repository := emptyCatalog()
		repository.Sources["source-one"] = catalog.SourceRecord{ID: "source-one"}
		withoutOwnerSource := manifest
		withoutOwnerSource.Sources = nil
		result := ValidateCase("cases/case-one/README.md", markdown, withoutOwnerSource, repository)
		assertHasDiagnosticCode(t, result, "sourced_claim_invalid")
	})
}

func taggedCaseFixture() (catalog.CaseManifest, *catalog.Catalog) {
	manifest := catalog.CaseManifest{
		ID:     "case-one",
		Status: catalog.LifecycleStatusComplete,
		Claims: []catalog.Claim{
			{ID: "assumed-claim"},
			{ID: "deduced-claim"},
			{ID: "measured-claim"},
			{ID: "sourced-claim"},
		},
		EvidenceRequirements: []catalog.EvidenceRequirement{{Claim: "measured-claim", Lab: "lab-one"}},
		Sources:              []string{"source-one"},
	}
	repository := emptyCatalog()
	repository.Sources["source-one"] = catalog.SourceRecord{ID: "source-one"}
	repository.Labs["lab-one"] = catalog.LabManifest{
		ID:   "lab-one",
		Kind: catalog.LabKindScenario,
		CaseBindings: []catalog.CaseBinding{{
			ID:     "binding-one",
			CaseID: "case-one",
			Claim:  "measured-claim",
		}},
		RequiredRuns: []catalog.RequiredRun{{ID: "run-one", Binding: "binding-one"}},
	}
	return manifest, repository
}
