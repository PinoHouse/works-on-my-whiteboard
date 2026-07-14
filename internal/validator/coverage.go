package validator

import (
	"sort"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
)

type Coverage struct {
	BaselineTotal         int              `json:"baseline_total"`
	CompleteTotal         int              `json:"complete_total"`
	MissingCaseIDs        []string         `json:"missing_case_ids"`
	UnexpectedCaseIDs     []string         `json:"unexpected_case_ids"`
	Families              []FamilyCoverage `json:"families"`
	RequiredPrinciples    []string         `json:"required_principles"`
	RequiredScenarioLabs  []string         `json:"required_scenario_labs"`
	RequiredPrimitiveLabs []string         `json:"required_primitive_labs"`
	RequiredAdapters      []string         `json:"required_adapters"`
}

type FamilyCoverage struct {
	ID       string `json:"id"`
	Required int    `json:"required"`
	Complete int    `json:"complete"`
}

func ComputeCoverage(c *catalog.Catalog) Coverage {
	coverage := Coverage{
		MissingCaseIDs:        []string{},
		UnexpectedCaseIDs:     []string{},
		Families:              []FamilyCoverage{},
		RequiredPrinciples:    []string{},
		RequiredScenarioLabs:  []string{},
		RequiredPrimitiveLabs: []string{},
		RequiredAdapters:      []string{},
	}
	if c == nil {
		return coverage
	}

	baselineCases := makeStringSet()
	baselineFamilies := makeStringSet()
	requiredByFamily := make(map[string]stringSet)
	for _, family := range c.Scope.Families {
		baselineFamilies.add(family.ID)
	}
	for _, scopeCase := range c.Scope.Cases {
		baselineCases.add(scopeCase.ID)
		if requiredByFamily[scopeCase.PrimaryFamily] == nil {
			requiredByFamily[scopeCase.PrimaryFamily] = makeStringSet()
		}
		requiredByFamily[scopeCase.PrimaryFamily].add(scopeCase.ID)
	}

	cases := caseManifestsByID(c)
	completeCases := makeStringSet()
	completeByFamily := make(map[string]stringSet)
	for _, id := range sortedCaseManifestIDs(cases) {
		manifest := cases[id]
		if manifest.Status != catalog.LifecycleStatusComplete {
			continue
		}
		completeCases.add(manifest.ID)
		if completeByFamily[manifest.PrimaryFamily] == nil {
			completeByFamily[manifest.PrimaryFamily] = makeStringSet()
		}
		completeByFamily[manifest.PrimaryFamily].add(manifest.ID)
	}

	coverage.BaselineTotal = len(baselineCases)
	coverage.CompleteTotal = len(completeCases)
	coverage.MissingCaseIDs = sortedDifference(baselineCases, completeCases)
	coverage.UnexpectedCaseIDs = sortedDifference(completeCases, baselineCases)
	for _, familyID := range baselineFamilies.sorted() {
		coverage.Families = append(coverage.Families, FamilyCoverage{
			ID:       familyID,
			Required: len(requiredByFamily[familyID]),
			Complete: len(completeByFamily[familyID]),
		})
	}

	principles := principleManifestsByID(c)
	labs := labManifestsByID(c)
	requiredPrinciples := makeStringSet()
	requiredScenarioLabs := makeStringSet()
	requiredPrimitiveLabs := makeStringSet()
	for _, principleID := range sortedPrincipleManifestIDs(principles) {
		if principles[principleID].Required {
			requiredPrinciples.add(principleID)
		}
	}
	for _, caseID := range baselineCases.sorted() {
		manifest, exists := cases[caseID]
		if !exists {
			continue
		}
		for _, principleID := range manifest.Principles {
			requiredPrinciples.add(resolveID(c, catalog.EntityKindPrinciple, principleID))
		}
		for _, labID := range manifest.Labs {
			requiredScenarioLabs.add(resolveID(c, catalog.EntityKindLab, labID))
		}
	}
	for _, principleID := range requiredPrinciples.sorted() {
		manifest, exists := principles[principleID]
		if !exists {
			continue
		}
		for _, labID := range manifest.Labs {
			requiredPrimitiveLabs.add(resolveID(c, catalog.EntityKindLab, labID))
		}
	}
	for _, labID := range sortedLabManifestIDs(labs) {
		lab := labs[labID]
		if !lab.Required {
			continue
		}
		switch lab.Kind {
		case catalog.LabKindScenario:
			requiredScenarioLabs.add(labID)
		case catalog.LabKindPrimitive:
			requiredPrimitiveLabs.add(labID)
		}
	}

	requiredAdapters := makeStringSet()
	requiredLabs := makeStringSet()
	for labID := range requiredScenarioLabs {
		requiredLabs.add(labID)
	}
	for labID := range requiredPrimitiveLabs {
		requiredLabs.add(labID)
	}
	for _, labID := range requiredLabs.sorted() {
		lab, exists := labs[labID]
		if !exists {
			continue
		}
		for _, run := range lab.RequiredRuns {
			for _, adapter := range run.Adapters {
				if adapter.Required {
					requiredAdapters.add(adapter.ID)
				}
			}
		}
	}

	coverage.RequiredPrinciples = requiredPrinciples.sorted()
	coverage.RequiredScenarioLabs = requiredScenarioLabs.sorted()
	coverage.RequiredPrimitiveLabs = requiredPrimitiveLabs.sorted()
	coverage.RequiredAdapters = requiredAdapters.sorted()
	return coverage
}

type stringSet map[string]struct{}

func makeStringSet(values ...string) stringSet {
	set := make(stringSet, len(values))
	for _, value := range values {
		set.add(value)
	}
	return set
}

func (s stringSet) add(value string) {
	s[value] = struct{}{}
}

func (s stringSet) contains(value string) bool {
	_, exists := s[value]
	return exists
}

func (s stringSet) sorted() []string {
	values := make([]string, 0, len(s))
	for value := range s {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func sortedDifference(left, right stringSet) []string {
	difference := makeStringSet()
	for value := range left {
		if !right.contains(value) {
			difference.add(value)
		}
	}
	return difference.sorted()
}

func resolveID(c *catalog.Catalog, kind catalog.EntityKind, id string) string {
	if c == nil {
		return id
	}
	resolved, err := c.Aliases.Resolve(kind, id)
	if err != nil {
		return id
	}
	return resolved
}

func caseManifestsByID(c *catalog.Catalog) map[string]catalog.CaseManifest {
	indexed := make(map[string]catalog.CaseManifest, len(c.Cases))
	for _, key := range sortedStringMapKeys(c.Cases) {
		manifest := c.Cases[key]
		if _, exists := indexed[manifest.ID]; !exists {
			indexed[manifest.ID] = manifest
		}
	}
	return indexed
}

func principleManifestsByID(c *catalog.Catalog) map[string]catalog.PrincipleManifest {
	indexed := make(map[string]catalog.PrincipleManifest, len(c.Principles))
	for _, key := range sortedStringMapKeys(c.Principles) {
		manifest := c.Principles[key]
		if _, exists := indexed[manifest.ID]; !exists {
			indexed[manifest.ID] = manifest
		}
	}
	return indexed
}

func labManifestsByID(c *catalog.Catalog) map[string]catalog.LabManifest {
	indexed := make(map[string]catalog.LabManifest, len(c.Labs))
	for _, key := range sortedStringMapKeys(c.Labs) {
		manifest := c.Labs[key]
		if _, exists := indexed[manifest.ID]; !exists {
			indexed[manifest.ID] = manifest
		}
	}
	return indexed
}

func adapterManifestsByID(c *catalog.Catalog) map[string]catalog.AdapterManifest {
	indexed := make(map[string]catalog.AdapterManifest, len(c.Adapters))
	for _, key := range sortedStringMapKeys(c.Adapters) {
		manifest := c.Adapters[key]
		if _, exists := indexed[manifest.ID]; !exists {
			indexed[manifest.ID] = manifest
		}
	}
	return indexed
}

func sortedStringMapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedCaseManifestIDs(values map[string]catalog.CaseManifest) []string {
	return sortedStringMapKeys(values)
}

func sortedPrincipleManifestIDs(values map[string]catalog.PrincipleManifest) []string {
	return sortedStringMapKeys(values)
}

func sortedLabManifestIDs(values map[string]catalog.LabManifest) []string {
	return sortedStringMapKeys(values)
}
