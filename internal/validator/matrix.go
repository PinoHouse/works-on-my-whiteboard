package validator

import (
	"fmt"
	"sort"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
)

type MatrixCell struct {
	LabID            string   `json:"lab_id"`
	RequiredRunID    string   `json:"required_run_id"`
	BindingID        string   `json:"binding_id"`
	ClaimID          string   `json:"claim_id"`
	Role             string   `json:"role"`
	ImplementationID string   `json:"implementation_id"`
	AdapterID        string   `json:"adapter_id,omitempty"`
	Workload         string   `json:"workload"`
	Faults           []string `json:"faults"`
	AssertionIDs     []string `json:"assertion_ids"`
}

func BuildRequiredMatrix(c *catalog.Catalog) ([]MatrixCell, []Diagnostic) {
	matrix := make([]MatrixCell, 0)
	diagnostics := make([]Diagnostic, 0)
	if c == nil {
		return matrix, diagnostics
	}
	cases := caseManifestsByID(c)
	principles := principleManifestsByID(c)
	claimOwners := buildClaimOwners(cases, principles)
	adapters := adapterManifestsByID(c)
	labs := labManifestsByID(c)
	coverage := ComputeCoverage(c)
	requiredLabs := makeStringSet(coverage.RequiredScenarioLabs...)
	for _, labID := range coverage.RequiredPrimitiveLabs {
		requiredLabs.add(labID)
	}
	seenCells := make(map[[6]string]struct{})

	for _, labID := range sortedLabManifestIDs(labs) {
		lab := labs[labID]
		labMatrixEligible := requiredLabs.contains(lab.ID) &&
			lab.Status == catalog.LifecycleStatusComplete &&
			(lab.Kind == catalog.LabKindScenario || lab.Kind == catalog.LabKindPrimitive) &&
			len(missingCompleteLabFields(lab)) == 0
		bindings, bindingsByID := collectMatrixBindings(lab)
		for _, bindingID := range sortedStringMapKeys(bindingsByID) {
			if len(bindingsByID[bindingID]) > 1 {
				diagnostics = append(diagnostics, errorDiagnostic(CodeDuplicateBindingID, lab.ID, fmt.Sprintf("lab %q declares binding ID %q more than once", lab.ID, bindingID)))
			}
		}
		for _, binding := range bindings {
			ownerID := resolveID(c, binding.kind, binding.ownerID)
			binding.claimValid = claimOwnedBy(claimOwners, binding.claimID, binding.kind, ownerID)
			if !binding.claimValid {
				diagnostics = append(diagnostics, errorDiagnostic(CodeForeignClaim, lab.ID, fmt.Sprintf("lab %q binding %q claim %q is absent or not owned by %s %q", lab.ID, binding.id, binding.claimID, binding.kind, ownerID)))
			}
		}

		runIDCounts := make(map[string]int, len(lab.RequiredRuns))
		for _, run := range lab.RequiredRuns {
			runIDCounts[run.ID]++
		}
		for _, runID := range sortedStringMapKeys(runIDCounts) {
			if runIDCounts[runID] > 1 {
				diagnostics = append(diagnostics, errorDiagnostic(CodeDuplicateRequiredRun, lab.ID, fmt.Sprintf("lab %q declares required run ID %q more than once", lab.ID, runID)))
			}
		}
		implementations := makeStringSet(lab.Implementations...)
		for _, run := range lab.RequiredRuns {
			runValid := runIDCounts[run.ID] == 1
			var binding *matrixBinding
			resolved := bindingsByID[run.Binding]
			if run.Binding == "" || len(resolved) != 1 {
				diagnostics = append(diagnostics, errorDiagnostic(CodeInvalidRunBinding, lab.ID, fmt.Sprintf("lab %q required run %q binding %q resolves to %d bindings; want exactly one", lab.ID, run.ID, run.Binding, len(resolved))))
				runValid = false
			} else {
				binding = resolved[0]
				binding.used = true
				if !bindingKindMatchesLab(binding.kind, lab.Kind) {
					diagnostics = append(diagnostics, errorDiagnostic(CodeOrphanedLab, lab.ID, fmt.Sprintf("lab %q required run %q resolves to the wrong binding kind", lab.ID, run.ID)))
					runValid = false
				}
				if !binding.claimValid {
					runValid = false
				}
				if run.Workload != binding.workload {
					diagnostics = append(diagnostics, errorDiagnostic(CodeBindingWorkloadMismatch, lab.ID, fmt.Sprintf("lab %q required run %q workload %q differs from binding %q workload %q", lab.ID, run.ID, run.Workload, binding.id, binding.workload)))
					runValid = false
				}
			}

			if !implementations.contains(run.Baseline) {
				diagnostics = append(diagnostics, errorDiagnostic(CodeUnknownImplementation, lab.ID, fmt.Sprintf("lab %q required run %q baseline %q is outside implementations", lab.ID, run.ID, run.Baseline)))
				runValid = false
			}
			seenSelections := makeStringSet(run.Baseline)
			for _, variantID := range run.Variants {
				if !implementations.contains(variantID) {
					diagnostics = append(diagnostics, errorDiagnostic(CodeUnknownImplementation, lab.ID, fmt.Sprintf("lab %q required run %q variant %q is outside implementations", lab.ID, run.ID, variantID)))
					runValid = false
				}
				if seenSelections.contains(variantID) {
					diagnostics = append(diagnostics, errorDiagnostic(CodeInvalidRunBinding, lab.ID, fmt.Sprintf("lab %q required run %q repeats implementation %q as a baseline or variant", lab.ID, run.ID, variantID)))
					runValid = false
				}
				seenSelections.add(variantID)
			}
			seenAdapters := makeStringSet()
			for _, requirement := range run.Adapters {
				if seenAdapters.contains(requirement.ID) {
					diagnostics = append(diagnostics, errorDiagnostic(CodeInvalidRunBinding, lab.ID, fmt.Sprintf("lab %q required run %q repeats adapter %q", lab.ID, run.ID, requirement.ID)))
					runValid = false
				}
				seenAdapters.add(requirement.ID)
				if !requirement.Required {
					continue
				}
				adapter, exists := adapters[requirement.ID]
				if !exists {
					diagnostics = append(diagnostics, errorDiagnostic(CodeMissingRequiredAdapter, lab.ID, fmt.Sprintf("lab %q required run %q needs complete adapter %q", lab.ID, run.ID, requirement.ID)))
					runValid = false
				} else if adapter.Status != catalog.LifecycleStatusComplete {
					if lab.Status == catalog.LifecycleStatusComplete {
						diagnostics = append(diagnostics, errorDiagnostic(CodeDependencyIncomplete, lab.ID, fmt.Sprintf("complete lab %q required run %q needs complete adapter %q", lab.ID, run.ID, requirement.ID)))
						runValid = false
					}
				} else if len(missingCompleteAdapterFields(adapter)) != 0 {
					runValid = false
				}
			}
			if !runValid || !labMatrixEligible {
				continue
			}

			choices := make([]matrixChoice, 0, 1+len(run.Variants)+len(run.Adapters))
			choices = append(choices, matrixChoice{role: "baseline", implementationID: run.Baseline})
			for _, variantID := range run.Variants {
				choices = append(choices, matrixChoice{role: "variant", implementationID: variantID})
			}
			for _, requirement := range run.Adapters {
				if requirement.Required {
					choices = append(choices, matrixChoice{role: "adapter", implementationID: requirement.ID, adapterID: requirement.ID})
				}
			}
			for _, choice := range choices {
				cell := MatrixCell{
					LabID:            lab.ID,
					RequiredRunID:    run.ID,
					BindingID:        binding.id,
					ClaimID:          binding.claimID,
					Role:             choice.role,
					ImplementationID: choice.implementationID,
					AdapterID:        choice.adapterID,
					Workload:         run.Workload,
					Faults:           append([]string{}, run.Faults...),
					AssertionIDs:     append([]string{}, binding.assertions...),
				}
				key := matrixCellIdentity(cell)
				if _, exists := seenCells[key]; exists {
					diagnostics = append(diagnostics, errorDiagnostic(CodeInvalidRunBinding, lab.ID, fmt.Sprintf("required run %q produces duplicate six-field matrix key %v", run.ID, key)))
					continue
				}
				seenCells[key] = struct{}{}
				matrix = append(matrix, cell)
			}
		}

		for _, binding := range bindings {
			if !binding.used {
				diagnostics = append(diagnostics, errorDiagnostic(CodeUnusedBinding, lab.ID, fmt.Sprintf("lab %q binding %q is not consumed by any required run", lab.ID, binding.id)))
			}
		}
	}

	sort.Slice(matrix, func(left, right int) bool {
		leftKey := matrixCellIdentity(matrix[left])
		rightKey := matrixCellIdentity(matrix[right])
		for index := range leftKey {
			if leftKey[index] != rightKey[index] {
				return leftKey[index] < rightKey[index]
			}
		}
		return matrix[left].Role < matrix[right].Role
	})
	return matrix, sortDiagnostics(diagnostics)
}

type matrixBinding struct {
	id         string
	kind       catalog.EntityKind
	ownerID    string
	claimID    string
	workload   string
	assertions []string
	claimValid bool
	used       bool
}

type matrixChoice struct {
	role             string
	implementationID string
	adapterID        string
}

func collectMatrixBindings(lab catalog.LabManifest) ([]*matrixBinding, map[string][]*matrixBinding) {
	bindings := make([]*matrixBinding, 0, len(lab.CaseBindings)+len(lab.PrincipleBindings))
	byID := make(map[string][]*matrixBinding)
	for _, source := range lab.CaseBindings {
		binding := &matrixBinding{
			id:         source.ID,
			kind:       catalog.EntityKindCase,
			ownerID:    source.CaseID,
			claimID:    source.Claim,
			workload:   source.Workload,
			assertions: source.Assertions,
		}
		bindings = append(bindings, binding)
		byID[binding.id] = append(byID[binding.id], binding)
	}
	for _, source := range lab.PrincipleBindings {
		binding := &matrixBinding{
			id:         source.ID,
			kind:       catalog.EntityKindPrinciple,
			ownerID:    source.PrincipleID,
			claimID:    source.Claim,
			workload:   source.Workload,
			assertions: source.Assertions,
		}
		bindings = append(bindings, binding)
		byID[binding.id] = append(byID[binding.id], binding)
	}
	return bindings, byID
}

func bindingKindMatchesLab(kind catalog.EntityKind, labKind catalog.LabKind) bool {
	return kind == catalog.EntityKindCase && labKind == catalog.LabKindScenario ||
		kind == catalog.EntityKindPrinciple && labKind == catalog.LabKindPrimitive
}

func matrixCellIdentity(cell MatrixCell) [6]string {
	return [6]string{
		cell.LabID,
		cell.RequiredRunID,
		cell.BindingID,
		cell.ClaimID,
		cell.ImplementationID,
		cell.AdapterID,
	}
}
