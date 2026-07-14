package content

import (
	"path/filepath"
	"sort"
	"unicode/utf8"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

const (
	CodeHeadingContractMismatch  = "heading_contract_mismatch"
	CodeEmptySectionBody         = "empty_section_body"
	CodeSectionTooShort          = "section_too_short"
	CodeUnfinishedMarker         = "unfinished_marker"
	CodeInvalidClaimMarker       = "invalid_claim_marker"
	CodeUnknownClaim             = "unknown_claim"
	CodeMissingClaimMarker       = "missing_claim_marker"
	CodeConflictingClaimClass    = "conflicting_claim_class"
	CodeAssumptionContextMissing = "assumption_context_missing"
	CodeMeasuredClaimUnbound     = "measured_claim_unbound"
	CodeSourcedClaimInvalid      = "sourced_claim_invalid"
	CodeMissingContentFile       = "missing_content_file"
	CodeInvalidLinkTarget        = "invalid_link_target"
	CodeMissingLinkTarget        = "missing_link_target"
	CodeMissingHeadingFragment   = "missing_heading_fragment"
	CodeInvalidUTF8              = "invalid_utf8"
	CodeContentReadFailure       = "content_read_failure"
)

var caseSectionContract = []sectionSpec{
	{name: "表面题目", minimumRunes: 120},
	{name: "反问与边界", minimumRunes: 180},
	{name: "客观模型", minimumRunes: 240},
	{name: "必然约束", minimumRunes: 240},
	{name: "从简单方案演进", minimumRunes: 300},
	{name: "设计决定", minimumRunes: 240},
	{name: "运行与演进", minimumRunes: 240},
	{name: "面试考察本质", minimumRunes: 240},
}

var principleSectionContract = []sectionSpec{
	{name: "定义", minimumRunes: 120},
	{name: "不变量", minimumRunes: 180},
	{name: "推导", minimumRunes: 180},
	{name: "失败边界", minimumRunes: 180},
	{name: "可复用实验", minimumRunes: 120},
	{name: "关联题目", minimumRunes: 80},
}

var markdownParser = goldmark.New(goldmark.WithExtensions(extension.Table))

type Result struct {
	Diagnostics []validator.Diagnostic
}

func ValidateCase(path string, markdown []byte, manifest catalog.CaseManifest, repository *catalog.Catalog) Result {
	if manifest.Status != catalog.LifecycleStatusComplete {
		return Result{Diagnostics: []validator.Diagnostic{}}
	}
	path = filepath.ToSlash(path)
	if !utf8.Valid(markdown) {
		return Result{Diagnostics: []validator.Diagnostic{contentDiagnostic(
			CodeInvalidUTF8,
			path,
			manifest.ID,
			"Markdown content is not valid UTF-8",
		)}}
	}
	document := parseMarkdown(markdown)
	diagnostics := validateSectionContract(path, manifest.ID, document, markdown, caseSectionContract)
	owner := ownerContract{
		kind:                 catalog.EntityKindCase,
		id:                   manifest.ID,
		claims:               manifest.Claims,
		evidenceRequirements: manifest.EvidenceRequirements,
		sources:              manifest.Sources,
	}
	diagnostics = append(diagnostics, validateProseContracts(
		path,
		collectProseBlocks(document, markdown),
		collectUnfinishedBlocks(document, markdown),
		owner,
		repository,
	)...)
	return Result{Diagnostics: sortContentDiagnostics(diagnostics)}
}

func ValidatePrinciple(path string, markdown []byte, manifest catalog.PrincipleManifest, repository *catalog.Catalog) Result {
	if manifest.Status != catalog.LifecycleStatusComplete {
		return Result{Diagnostics: []validator.Diagnostic{}}
	}
	path = filepath.ToSlash(path)
	if !utf8.Valid(markdown) {
		return Result{Diagnostics: []validator.Diagnostic{contentDiagnostic(
			CodeInvalidUTF8,
			path,
			manifest.ID,
			"Markdown content is not valid UTF-8",
		)}}
	}
	document := parseMarkdown(markdown)
	diagnostics := validateSectionContract(path, manifest.ID, document, markdown, principleSectionContract)
	owner := ownerContract{
		kind:                 catalog.EntityKindPrinciple,
		id:                   manifest.ID,
		claims:               manifest.Claims,
		evidenceRequirements: manifest.EvidenceRequirements,
		sources:              manifest.Sources,
	}
	diagnostics = append(diagnostics, validateProseContracts(
		path,
		collectProseBlocks(document, markdown),
		collectUnfinishedBlocks(document, markdown),
		owner,
		repository,
	)...)
	return Result{Diagnostics: sortContentDiagnostics(diagnostics)}
}

func parseMarkdown(source []byte) ast.Node {
	return markdownParser.Parser().Parse(text.NewReader(source))
}

func contentDiagnostic(code, path, entityID, message string) validator.Diagnostic {
	return validator.Diagnostic{
		Code:     code,
		Severity: "error",
		Path:     filepath.ToSlash(path),
		EntityID: entityID,
		Message:  message,
	}
}

func sortContentDiagnostics(diagnostics []validator.Diagnostic) []validator.Diagnostic {
	if diagnostics == nil {
		diagnostics = []validator.Diagnostic{}
	}
	sort.Slice(diagnostics, func(left, right int) bool {
		if diagnostics[left].Code != diagnostics[right].Code {
			return diagnostics[left].Code < diagnostics[right].Code
		}
		if diagnostics[left].Path != diagnostics[right].Path {
			return diagnostics[left].Path < diagnostics[right].Path
		}
		if diagnostics[left].EntityID != diagnostics[right].EntityID {
			return diagnostics[left].EntityID < diagnostics[right].EntityID
		}
		return diagnostics[left].Message < diagnostics[right].Message
	})
	unique := diagnostics[:0]
	for _, diagnostic := range diagnostics {
		if len(unique) != 0 && unique[len(unique)-1] == diagnostic {
			continue
		}
		unique = append(unique, diagnostic)
	}
	return unique
}
