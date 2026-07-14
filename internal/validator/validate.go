package validator

import (
	"fmt"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
)

type Report struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
	Coverage    Coverage     `json:"coverage"`
	Matrix      []MatrixCell `json:"matrix"`
}

func Validate(c *catalog.Catalog, mode Mode) Report {
	coverage := ComputeCoverage(c)
	matrix, matrixDiagnostics := BuildRequiredMatrix(c)
	diagnostics := make([]Diagnostic, 0)
	diagnostics = append(diagnostics, validateSchemaContracts(c)...)
	diagnostics = append(diagnostics, validateReferences(c)...)
	diagnostics = append(diagnostics, validateDependencyGraph(c, coverage)...)
	diagnostics = append(diagnostics, matrixDiagnostics...)

	if mode != ModeDevelopment && mode != ModeRelease {
		diagnostics = append(diagnostics, errorDiagnostic(CodeStatusVocabularyMismatch, "", fmt.Sprintf("unknown validation mode %q; want development or release", mode)))
	}
	if mode == ModeRelease {
		if len(coverage.MissingCaseIDs) != 0 || len(coverage.UnexpectedCaseIDs) != 0 {
			diagnostics = append(diagnostics, errorDiagnostic(
				CodeReleaseScopeIncomplete,
				"",
				fmt.Sprintf(
					"release scope incomplete: complete=%d baseline=%d missing=%d unexpected=%d missing_ids=%v unexpected_ids=%v",
					coverage.CompleteTotal,
					coverage.BaselineTotal,
					len(coverage.MissingCaseIDs),
					len(coverage.UnexpectedCaseIDs),
					coverage.MissingCaseIDs,
					coverage.UnexpectedCaseIDs,
				),
			))
		}
		diagnostics = append(diagnostics, validateReleaseFamilies(c)...)
	}

	return Report{
		Diagnostics: sortDiagnostics(diagnostics),
		Coverage:    coverage,
		Matrix:      matrix,
	}
}

func validateReleaseFamilies(c *catalog.Catalog) []Diagnostic {
	if c == nil {
		return []Diagnostic{}
	}
	scopeFamilies := make(map[string]string)
	for _, scopeCase := range c.Scope.Cases {
		if _, exists := scopeFamilies[scopeCase.ID]; !exists {
			scopeFamilies[scopeCase.ID] = scopeCase.PrimaryFamily
		}
	}
	diagnostics := make([]Diagnostic, 0)
	cases := caseManifestsByID(c)
	for _, caseID := range sortedCaseManifestIDs(cases) {
		manifest := cases[caseID]
		if manifest.Status != catalog.LifecycleStatusComplete {
			continue
		}
		expectedFamily, exists := scopeFamilies[manifest.ID]
		if !exists || manifest.PrimaryFamily == expectedFamily {
			continue
		}
		diagnostics = append(diagnostics, errorDiagnostic(
			CodeReleaseFamilyMismatch,
			manifest.ID,
			fmt.Sprintf("release case %q has primary family %q; scope requires %q", manifest.ID, manifest.PrimaryFamily, expectedFamily),
		))
	}
	return diagnostics
}
