package content

import (
	"strings"
	"testing"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
)

func TestValidateCaseLocksExactOrderedH2Headings(t *testing.T) {
	manifest := catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete}
	tests := []struct {
		name     string
		sections []string
		code     string
	}{
		{
			name:     "missing",
			sections: append([]string{}, testCaseSections[:len(testCaseSections)-1]...),
			code:     "heading_contract_mismatch",
		},
		{
			name:     "duplicated",
			sections: append(append([]string{}, testCaseSections...), "表面题目"),
			code:     "heading_contract_mismatch",
		},
		{
			name: "renamed",
			sections: func() []string {
				sections := append([]string{}, testCaseSections...)
				sections[2] = "主观模型"
				return sections
			}(),
			code: "heading_contract_mismatch",
		},
		{
			name: "out_of_order",
			sections: func() []string {
				sections := append([]string{}, testCaseSections...)
				sections[3], sections[4] = sections[4], sections[3]
				return sections
			}(),
			code: "heading_contract_mismatch",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ValidateCase(
				"cases/case-one/README.md",
				markdownWithSections(test.sections, nil),
				manifest,
				emptyCatalog(),
			)
			assertHasDiagnosticCode(t, result, test.code)
		})
	}
}

func TestValidateCaseUsesOnlyDocumentLevelH2Headings(t *testing.T) {
	sections := append([]string{}, testCaseSections[:len(testCaseSections)-1]...)
	markdown := string(markdownWithSections(sections, nil)) +
		"> ## 面试考察本质\n>\n> " + longProse(300) + "\n\n" +
		"- ## 面试考察本质\n"
	result := ValidateCase(
		"cases/case-one/README.md",
		[]byte(markdown),
		catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete},
		emptyCatalog(),
	)
	assertHasDiagnosticCode(t, result, "heading_contract_mismatch")
}

func TestValidateCaseRequiresAllowedNonEmptyBodyBlock(t *testing.T) {
	result := ValidateCase(
		"cases/case-one/README.md",
		validCaseMarkdown(map[string]string{
			"表面题目": "### " + strings.Repeat("很长的嵌套标题", 80) + "\n\n<!-- " + longProse(400) + " -->",
		}),
		catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete},
		emptyCatalog(),
	)
	assertHasDiagnosticCode(t, result, "empty_section_body")

	allowed := map[string]string{
		"paragraph":        longProse(140),
		"inline_code_only": "`non-empty inline code`",
		"list":             "- " + longProse(140),
		"table":            "| 列 |\n| --- |\n| " + longProse(140) + " |",
		"indented":         "    non-empty code block",
		"fenced":           "```text\nnon-empty code block\n```",
		"nested":           "> " + longProse(140),
	}
	for name, body := range allowed {
		t.Run(name, func(t *testing.T) {
			result := ValidateCase(
				"cases/case-one/README.md",
				validCaseMarkdown(map[string]string{"表面题目": body}),
				catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete},
				emptyCatalog(),
			)
			assertNoDiagnosticCode(t, result, "empty_section_body")
		})
	}
}

func TestValidateCaseRejectsStructurallyEmptyAllowedContainers(t *testing.T) {
	bodies := map[string]string{
		"list_with_heading_only": "- ### nested heading",
		"empty_table":            "|   |\n| --- |\n|   |",
		"empty_fence":            "```go\n```",
		"html_comment_only":      "<!-- " + longProse(400) + " -->",
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			result := ValidateCase(
				"cases/case-one/README.md",
				validCaseMarkdown(map[string]string{"表面题目": body}),
				catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete},
				emptyCatalog(),
			)
			assertHasDiagnosticCode(t, result, "empty_section_body")
		})
	}
}

func TestValidateCaseIgnoresWholeHTMLBlocksButChecksTextBetweenInlineTags(t *testing.T) {
	htmlBlock := "<div>\n" + longProse(400) + " TODO\n</div>"
	result := ValidateCase(
		"cases/case-one/README.md",
		validCaseMarkdown(map[string]string{"表面题目": htmlBlock}),
		catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete},
		emptyCatalog(),
	)
	assertHasDiagnosticCode(t, result, "empty_section_body")
	assertHasDiagnosticCode(t, result, "section_too_short")
	assertNoDiagnosticCode(t, result, "unfinished_marker")

	inlineHTML := longProse(140) + " <span>TODO</span>"
	result = ValidateCase(
		"cases/case-one/README.md",
		validCaseMarkdown(map[string]string{"表面题目": inlineHTML}),
		catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete},
		emptyCatalog(),
	)
	assertHasDiagnosticCode(t, result, "unfinished_marker")
}

func TestValidateCaseCountsCollapsedUnicodeProseAndIgnoresNonProse(t *testing.T) {
	result := ValidateCase(
		"cases/case-one/README.md",
		validCaseMarkdown(map[string]string{"表面题目": strings.Repeat("界", 120)}),
		catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete},
		emptyCatalog(),
	)
	assertNoDiagnosticCode(t, result, "section_too_short")

	ignoredPayload := strings.Repeat("隐", 500)
	result = ValidateCase(
		"cases/case-one/README.md",
		validCaseMarkdown(map[string]string{
			"表面题目": strings.Repeat("界", 20) + " `" + ignoredPayload + "` <!-- " + ignoredPayload + " --> [链](" + ignoredPayload + ")\n\n```text\n" + ignoredPayload + "\n```",
		}),
		catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete},
		emptyCatalog(),
	)
	assertHasDiagnosticCode(t, result, "section_too_short")
}

func TestValidateCaseUsesRenderedTextSemanticsForRuneCounts(t *testing.T) {
	result := ValidateCase(
		"cases/case-one/README.md",
		validCaseMarkdown(map[string]string{"表面题目": strings.Repeat("&#x4E00;", 120)}),
		catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete},
		emptyCatalog(),
	)
	assertNoDiagnosticCode(t, result, "section_too_short")

	result = ValidateCase(
		"cases/case-one/README.md",
		validCaseMarkdown(map[string]string{"表面题目": strings.Repeat("&#x4E00;", 119)}),
		catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete},
		emptyCatalog(),
	)
	assertHasDiagnosticCode(t, result, "section_too_short")
}

func TestValidateCaseIgnoresImagesAndAutolinksAsProse(t *testing.T) {
	longDestination := "https://example.com/" + strings.Repeat("x", 500)
	body := strings.Repeat("界", 20) + " ![" + longProse(500) + "](" + longDestination + ") <" + longDestination + ">"
	result := ValidateCase(
		"cases/case-one/README.md",
		validCaseMarkdown(map[string]string{"表面题目": body}),
		catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete},
		emptyCatalog(),
	)
	assertHasDiagnosticCode(t, result, "section_too_short")
}
