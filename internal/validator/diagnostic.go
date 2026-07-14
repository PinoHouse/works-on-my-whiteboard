package validator

import "sort"

const (
	CodeInvalidStableID             = "invalid_stable_id"
	CodeDuplicateScopeFamily        = "duplicate_scope_family"
	CodeDuplicateScopeCase          = "duplicate_scope_case"
	CodeCaseOutsideScope            = "case_outside_scope"
	CodeUnknownFamily               = "unknown_family"
	CodeUnknownReference            = "unknown_reference"
	CodeDuplicateClaimID            = "duplicate_claim_id"
	CodeInvalidSourceURL            = "invalid_source_url"
	CodeInvalidSourceDate           = "invalid_source_date"
	CodeDanglingSource              = "dangling_source"
	CodeCompleteContractEmpty       = "complete_contract_empty"
	CodeMissingCaseBinding          = "missing_case_binding"
	CodeMissingPrincipleBinding     = "missing_principle_binding"
	CodeDuplicateBindingID          = "duplicate_binding_id"
	CodeDuplicateRequiredRun        = "duplicate_required_run"
	CodeInvalidRunBinding           = "invalid_run_binding"
	CodeForeignClaim                = "foreign_claim"
	CodeUnusedBinding               = "unused_binding"
	CodeBindingWorkloadMismatch     = "binding_workload_mismatch"
	CodeUnknownImplementation       = "unknown_implementation"
	CodeMissingRequiredPrincipleLab = "missing_required_principle_lab"
	CodeMissingRequiredAdapter      = "missing_required_adapter"
	CodeDependencyIncomplete        = "dependency_incomplete"
	CodeOrphanedLab                 = "orphaned_lab"
	CodeStatusVocabularyMismatch    = "status_vocabulary_mismatch"
	CodeAliasCycle                  = "alias_cycle"
	CodeReleaseScopeIncomplete      = "release_scope_incomplete"
	CodeReleaseFamilyMismatch       = "release_family_mismatch"
)

type Mode string

const (
	ModeDevelopment Mode = "development"
	ModeRelease     Mode = "release"
)

type Diagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Path     string `json:"path,omitempty"`
	EntityID string `json:"entity_id,omitempty"`
	Message  string `json:"message"`
}

func errorDiagnostic(code, entityID, message string) Diagnostic {
	return Diagnostic{
		Code:     code,
		Severity: "error",
		EntityID: entityID,
		Message:  message,
	}
}

func sortDiagnostics(diagnostics []Diagnostic) []Diagnostic {
	if diagnostics == nil {
		diagnostics = []Diagnostic{}
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
	return diagnostics
}
