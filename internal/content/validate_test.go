package content

import (
	"strings"
	"testing"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
)

var testCaseSections = []string{
	"表面题目",
	"反问与边界",
	"客观模型",
	"必然约束",
	"从简单方案演进",
	"设计决定",
	"运行与演进",
	"面试考察本质",
}

var testPrincipleSections = []string{
	"定义",
	"不变量",
	"推导",
	"失败边界",
	"可复用实验",
	"关联题目",
}

func TestContentDiagnosticCodesAreStable(t *testing.T) {
	actual := []string{
		CodeHeadingContractMismatch,
		CodeEmptySectionBody,
		CodeSectionTooShort,
		CodeUnfinishedMarker,
		CodeInvalidClaimMarker,
		CodeUnknownClaim,
		CodeMissingClaimMarker,
		CodeConflictingClaimClass,
		CodeAssumptionContextMissing,
		CodeMeasuredClaimUnbound,
		CodeSourcedClaimInvalid,
		CodeMissingContentFile,
		CodeInvalidLinkTarget,
		CodeMissingLinkTarget,
		CodeMissingHeadingFragment,
		CodeInvalidUTF8,
	}
	expected := []string{
		"heading_contract_mismatch",
		"empty_section_body",
		"section_too_short",
		"unfinished_marker",
		"invalid_claim_marker",
		"unknown_claim",
		"missing_claim_marker",
		"conflicting_claim_class",
		"assumption_context_missing",
		"measured_claim_unbound",
		"sourced_claim_invalid",
		"missing_content_file",
		"invalid_link_target",
		"missing_link_target",
		"missing_heading_fragment",
		"invalid_utf8",
	}
	for index := range expected {
		if actual[index] != expected[index] {
			t.Fatalf("diagnostic code[%d] = %q; want %q", index, actual[index], expected[index])
		}
	}
}

func TestValidateCaseRejectsUnfinishedMarkersOnlyInProse(t *testing.T) {
	markers := []string{"TODO", "TBD", "FIXME", "XXX", "待补充", "待完善"}
	for _, marker := range markers {
		t.Run(marker, func(t *testing.T) {
			markdown := validCaseMarkdown(map[string]string{
				"表面题目": longProse(140) + " " + marker,
			})
			result := ValidateCase("cases/case-one/README.md", markdown, catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete}, emptyCatalog())
			assertHasDiagnosticCode(t, result, "unfinished_marker")
		})
	}

	markdown := validCaseMarkdown(map[string]string{
		"表面题目": longProse(140) +
			"\n\n`TODO TBD FIXME XXX 待补充 待完善`" +
			"\n\n<!-- TODO TBD FIXME XXX 待补充 待完善 -->" +
			"\n\n[安全链接](docs/TODO-TBD-FIXME-XXX-待补充-待完善.md)" +
			"\n\n```text\nTODO TBD FIXME XXX 待补充 待完善\n[MEASURED:not-a-claim]\n```\n",
	})
	result := ValidateCase("cases/case-one/README.md", markdown, catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete}, emptyCatalog())
	assertNoDiagnosticCode(t, result, "unfinished_marker")
	assertNoDiagnosticCode(t, result, "invalid_claim_marker")
	assertNoDiagnosticCode(t, result, "unknown_claim")
}

func TestValidatePrincipleLocksOrderedSectionsAndMinimumRunes(t *testing.T) {
	valid := validPrincipleMarkdown(map[string]string{
		"关联题目": strings.Repeat("关", 80),
	})
	result := ValidatePrinciple("principles/principle-one/README.md", valid, catalog.PrincipleManifest{ID: "principle-one", Status: catalog.LifecycleStatusComplete}, emptyCatalog())
	assertNoDiagnosticCode(t, result, "section_too_short")

	short := validPrincipleMarkdown(map[string]string{
		"关联题目": strings.Repeat("关", 79),
	})
	result = ValidatePrinciple("principles/principle-one/README.md", short, catalog.PrincipleManifest{ID: "principle-one", Status: catalog.LifecycleStatusComplete}, emptyCatalog())
	assertHasDiagnosticCode(t, result, "section_too_short")

	outOfOrder := append([]string{}, testPrincipleSections...)
	outOfOrder[1], outOfOrder[2] = outOfOrder[2], outOfOrder[1]
	result = ValidatePrinciple(
		"principles/principle-one/README.md",
		markdownWithSections(outOfOrder, nil),
		catalog.PrincipleManifest{ID: "principle-one", Status: catalog.LifecycleStatusComplete},
		emptyCatalog(),
	)
	assertHasDiagnosticCode(t, result, "heading_contract_mismatch")
}

func TestDraftDocumentsPermitIncrementalContent(t *testing.T) {
	caseResult := ValidateCase(
		"cases/case-one/README.md",
		[]byte("# 草稿\n\nTODO [DEDUCED:not-declared]\n"),
		catalog.CaseManifest{
			ID:     "case-one",
			Status: catalog.LifecycleStatusDraft,
			Claims: []catalog.Claim{{ID: "unused-claim"}},
		},
		emptyCatalog(),
	)
	if len(caseResult.Diagnostics) != 0 {
		t.Fatalf("draft case diagnostics = %#v; want none", caseResult.Diagnostics)
	}

	principleResult := ValidatePrinciple(
		"principles/principle-one/README.md",
		[]byte("# 草稿\n"),
		catalog.PrincipleManifest{
			ID:     "principle-one",
			Status: catalog.LifecycleStatusDraft,
			Claims: []catalog.Claim{{ID: "unused-claim"}},
		},
		emptyCatalog(),
	)
	if len(principleResult.Diagnostics) != 0 {
		t.Fatalf("draft principle diagnostics = %#v; want none", principleResult.Diagnostics)
	}
}

func TestValidateCaseRejectsInvalidUTF8(t *testing.T) {
	markdown := append(validCaseMarkdown(nil), 0xff)
	result := ValidateCase(
		"cases/case-one/README.md",
		markdown,
		catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete},
		emptyCatalog(),
	)
	assertHasDiagnosticCode(t, result, "invalid_utf8")
}

func TestIgnoredInlineNodesCreateMarkerBoundariesButInlineHTMLDoesNot(t *testing.T) {
	result := ValidateCase(
		"cases/case-one/README.md",
		validCaseMarkdown(map[string]string{"表面题目": longProse(140) + " TO`ignored`DO"}),
		catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete},
		emptyCatalog(),
	)
	assertNoDiagnosticCode(t, result, "unfinished_marker")

	result = ValidateCase(
		"cases/case-one/README.md",
		validCaseMarkdown(map[string]string{"表面题目": longProse(140) + " TO<span></span>DO"}),
		catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete},
		emptyCatalog(),
	)
	assertHasDiagnosticCode(t, result, "unfinished_marker")

	result = ValidateCase(
		"cases/case-one/README.md",
		validCaseMarkdown(map[string]string{"表面题目": longProse(140) + " TO<!-- ignored -->DO"}),
		catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete},
		emptyCatalog(),
	)
	assertHasDiagnosticCode(t, result, "unfinished_marker")

	result = ValidateCase(
		"cases/case-one/README.md",
		validCaseMarkdown(map[string]string{"表面题目": longProse(140) + " TO**DO**"}),
		catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete},
		emptyCatalog(),
	)
	assertHasDiagnosticCode(t, result, "unfinished_marker")
}

func TestUnfinishedMarkersScanHeadingsImagesAndRenderedEntities(t *testing.T) {
	markdown := string(validCaseMarkdown(nil)) + "\n### TODO\n\n![TBD](https://example.com/image.png)\n\n&#84;ODO\n"
	result := ValidateCase(
		"cases/case-one/README.md",
		[]byte(markdown),
		catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete},
		emptyCatalog(),
	)
	assertHasDiagnosticCode(t, result, "unfinished_marker")
}

func validCaseMarkdown(overrides map[string]string) []byte {
	return markdownWithSections(testCaseSections, overrides)
}

func validPrincipleMarkdown(overrides map[string]string) []byte {
	return markdownWithSections(testPrincipleSections, overrides)
}

func markdownWithSections(sections []string, overrides map[string]string) []byte {
	var builder strings.Builder
	builder.WriteString("# 文档标题\n\n")
	for _, section := range sections {
		builder.WriteString("## ")
		builder.WriteString(section)
		builder.WriteString("\n\n")
		body := longProse(360)
		if override, exists := overrides[section]; exists {
			body = override
		}
		builder.WriteString(body)
		builder.WriteString("\n\n")
	}
	return []byte(builder.String())
}

func longProse(runes int) string {
	return strings.Repeat("文", runes)
}

func emptyCatalog() *catalog.Catalog {
	return &catalog.Catalog{
		Sources:    map[string]catalog.SourceRecord{},
		Cases:      map[string]catalog.CaseManifest{},
		Principles: map[string]catalog.PrincipleManifest{},
		Labs:       map[string]catalog.LabManifest{},
		Adapters:   map[string]catalog.AdapterManifest{},
	}
}

func assertHasDiagnosticCode(t *testing.T, result Result, code string) {
	t.Helper()
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == code {
			return
		}
	}
	t.Fatalf("diagnostics = %#v; want code %q", result.Diagnostics, code)
}

func assertNoDiagnosticCode(t *testing.T, result Result, code string) {
	t.Helper()
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == code {
			t.Fatalf("diagnostics = %#v; do not want code %q", result.Diagnostics, code)
		}
	}
}
