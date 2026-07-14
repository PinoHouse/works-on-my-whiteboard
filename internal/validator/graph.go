package validator

import (
	"fmt"
	"sort"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
)

type dependencyGraph map[string][]string

func validateDependencyGraph(c *catalog.Catalog, coverage Coverage, mode Mode) []Diagnostic {
	if c == nil {
		return []Diagnostic{}
	}
	diagnostics := make([]Diagnostic, 0)
	cases := caseManifestsByID(c)
	principles := principleManifestsByID(c)
	labs := labManifestsByID(c)
	requiredByCompleteCase := makeStringSet()
	incompleteTargets := makeStringSet()
	appendIncomplete := func(targetKind, targetID, entityID, message string) {
		target := targetKind + ":" + targetID
		if incompleteTargets.contains(target) {
			return
		}
		incompleteTargets.add(target)
		diagnostics = append(diagnostics, errorDiagnostic(CodeDependencyIncomplete, entityID, message))
	}

	for _, caseID := range sortedCaseManifestIDs(cases) {
		manifest := cases[caseID]
		if manifest.Status != catalog.LifecycleStatusComplete {
			continue
		}
		for _, principleReference := range manifest.Principles {
			principleID := resolveID(c, catalog.EntityKindPrinciple, principleReference)
			requiredByCompleteCase.add(principleID)
			principle, exists := principles[principleID]
			if exists && principle.Status != catalog.LifecycleStatusComplete {
				appendIncomplete("principle", principleID, manifest.ID, fmt.Sprintf("complete case %q requires complete principle %q", manifest.ID, principleID))
			}
		}
		for _, labReference := range manifest.Labs {
			labID := resolveID(c, catalog.EntityKindLab, labReference)
			lab, exists := labs[labID]
			if exists && (lab.Kind != catalog.LabKindScenario || lab.Status != catalog.LifecycleStatusComplete) {
				appendIncomplete("lab", labID, manifest.ID, fmt.Sprintf("complete case %q requires complete scenario lab %q", manifest.ID, labID))
			}
		}
	}

	for _, principleID := range sortedPrincipleManifestIDs(principles) {
		manifest := principles[principleID]
		if manifest.Status != catalog.LifecycleStatusComplete {
			continue
		}
		for _, labReference := range manifest.Labs {
			labID := resolveID(c, catalog.EntityKindLab, labReference)
			lab, exists := labs[labID]
			if exists && (lab.Kind != catalog.LabKindPrimitive || lab.Status != catalog.LifecycleStatusComplete) {
				appendIncomplete("lab", labID, manifest.ID, fmt.Sprintf("complete principle %q requires complete primitive lab %q", manifest.ID, labID))
			}
		}
	}

	requiredPrinciples := makeStringSet(coverage.RequiredPrinciples...)
	for principleID := range requiredByCompleteCase {
		requiredPrinciples.add(principleID)
	}
	for _, principleID := range requiredPrinciples.sorted() {
		principle, exists := principles[principleID]
		if !exists {
			continue
		}
		hasPrimitiveEdge := false
		for _, labReference := range principle.Labs {
			labID := resolveID(c, catalog.EntityKindLab, labReference)
			lab, labExists := labs[labID]
			if !labExists || lab.Kind == catalog.LabKindPrimitive {
				hasPrimitiveEdge = true
				break
			}
		}
		if !hasPrimitiveEdge {
			diagnostics = append(diagnostics, errorDiagnostic(CodeMissingRequiredPrincipleLab, principleID, fmt.Sprintf("required principle %q has no owner-listed primitive lab", principleID)))
		}
	}

	if mode == ModeRelease {
		for _, principleID := range coverage.RequiredPrinciples {
			principle, exists := principles[principleID]
			if exists && principle.Status != catalog.LifecycleStatusComplete {
				appendIncomplete("principle", principleID, principleID, fmt.Sprintf("release requires principle %q to be complete", principleID))
			}
		}
		for _, labID := range coverage.RequiredScenarioLabs {
			lab, exists := labs[labID]
			if exists && lab.Kind == catalog.LabKindScenario && lab.Status != catalog.LifecycleStatusComplete {
				appendIncomplete("lab", labID, labID, fmt.Sprintf("release requires scenario lab %q to be complete", labID))
			}
		}
		for _, labID := range coverage.RequiredPrimitiveLabs {
			lab, exists := labs[labID]
			if exists && lab.Kind == catalog.LabKindPrimitive && lab.Status != catalog.LifecycleStatusComplete {
				appendIncomplete("lab", labID, labID, fmt.Sprintf("release requires primitive lab %q to be complete", labID))
			}
		}
	}

	graph := buildRequiredDependencyGraph(c, cases, principles, labs)
	diagnostics = append(diagnostics, dependencyCycleDiagnostics(graph)...)
	return diagnostics
}

func dependencyCycleDiagnostics(graph dependencyGraph) []Diagnostic {
	cycle := firstDependencyCycle(graph)
	if len(cycle) == 0 {
		return []Diagnostic{}
	}
	return []Diagnostic{errorDiagnostic(CodeDependencyIncomplete, cycle[0], fmt.Sprintf("required dependency cycle: %v", cycle))}
}

func buildRequiredDependencyGraph(
	c *catalog.Catalog,
	cases map[string]catalog.CaseManifest,
	principles map[string]catalog.PrincipleManifest,
	labs map[string]catalog.LabManifest,
) dependencyGraph {
	graph := make(dependencyGraph)
	for _, caseID := range sortedCaseManifestIDs(cases) {
		manifest := cases[caseID]
		from := "case:" + manifest.ID
		for _, principleID := range manifest.Principles {
			graph[from] = append(graph[from], "principle:"+resolveID(c, catalog.EntityKindPrinciple, principleID))
		}
		for _, labID := range manifest.Labs {
			graph[from] = append(graph[from], "lab:"+resolveID(c, catalog.EntityKindLab, labID))
		}
	}
	for _, principleID := range sortedPrincipleManifestIDs(principles) {
		manifest := principles[principleID]
		from := "principle:" + manifest.ID
		for _, labID := range manifest.Labs {
			graph[from] = append(graph[from], "lab:"+resolveID(c, catalog.EntityKindLab, labID))
		}
	}
	for _, labID := range sortedLabManifestIDs(labs) {
		lab := labs[labID]
		from := "lab:" + lab.ID
		for _, run := range lab.RequiredRuns {
			for _, adapter := range run.Adapters {
				if adapter.Required {
					graph[from] = append(graph[from], "adapter:"+adapter.ID)
				}
			}
		}
	}
	for node := range graph {
		sort.Strings(graph[node])
	}
	return graph
}

func firstDependencyCycle(graph dependencyGraph) []string {
	state := make(map[string]uint8)
	stack := make([]string, 0)
	stackIndex := make(map[string]int)
	nodes := sortedStringMapKeys(graph)
	var visit func(string) []string
	visit = func(node string) []string {
		switch state[node] {
		case 1:
			start := stackIndex[node]
			cycle := append([]string{}, stack[start:]...)
			return append(cycle, node)
		case 2:
			return nil
		}
		state[node] = 1
		stackIndex[node] = len(stack)
		stack = append(stack, node)
		for _, target := range graph[node] {
			if cycle := visit(target); len(cycle) != 0 {
				return cycle
			}
		}
		stack = stack[:len(stack)-1]
		delete(stackIndex, node)
		state[node] = 2
		return nil
	}
	for _, node := range nodes {
		if cycle := visit(node); len(cycle) != 0 {
			return cycle
		}
	}
	return []string{}
}
