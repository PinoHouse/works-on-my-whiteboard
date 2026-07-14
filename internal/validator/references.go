package validator

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
)

type claimOwner struct {
	kind catalog.EntityKind
	id   string
}

func validateReferences(c *catalog.Catalog) []Diagnostic {
	if c == nil {
		return []Diagnostic{}
	}
	diagnostics := make([]Diagnostic, 0)
	familyIDs := makeStringSet()
	seenFamilies := makeStringSet()
	for _, family := range c.Scope.Families {
		if seenFamilies.contains(family.ID) {
			diagnostics = append(diagnostics, errorDiagnostic(CodeDuplicateScopeFamily, family.ID, fmt.Sprintf("scope family %q is declared more than once", family.ID)))
		}
		seenFamilies.add(family.ID)
		familyIDs.add(family.ID)
	}

	scopeCaseIDs := makeStringSet()
	for _, scopeCase := range c.Scope.Cases {
		if scopeCaseIDs.contains(scopeCase.ID) {
			diagnostics = append(diagnostics, errorDiagnostic(CodeDuplicateScopeCase, scopeCase.ID, fmt.Sprintf("scope case %q is declared more than once", scopeCase.ID)))
		}
		if !familyIDs.contains(scopeCase.PrimaryFamily) {
			diagnostics = append(diagnostics, errorDiagnostic(CodeUnknownFamily, scopeCase.ID, fmt.Sprintf("scope case %q references unknown primary family %q", scopeCase.ID, scopeCase.PrimaryFamily)))
		}
		scopeCaseIDs.add(scopeCase.ID)
	}
	for _, exclusion := range c.Scope.Exclusions {
		canonicalID := resolveID(c, catalog.EntityKindCase, exclusion.CanonicalCaseID)
		if !scopeCaseIDs.contains(canonicalID) {
			diagnostics = append(diagnostics, errorDiagnostic(CodeUnknownReference, exclusion.ID, fmt.Sprintf("scope exclusion %q references unknown canonical case %q", exclusion.ID, exclusion.CanonicalCaseID)))
		}
	}

	sources := sourceRecordsByID(c)
	for _, sourceID := range sortedStringMapKeys(sources) {
		source := sources[sourceID]
		parsed, err := url.Parse(source.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
			diagnostics = append(diagnostics, errorDiagnostic(CodeInvalidSourceURL, source.ID, fmt.Sprintf("source %q URL %q must be a direct HTTPS URL with a host", source.ID, source.URL)))
		}
		accessedAt, err := time.Parse("2006-01-02", source.AccessedAt)
		if err != nil || accessedAt.Format("2006-01-02") != source.AccessedAt {
			diagnostics = append(diagnostics, errorDiagnostic(CodeInvalidSourceDate, source.ID, fmt.Sprintf("source %q accessed_at %q must be a real YYYY-MM-DD date", source.ID, source.AccessedAt)))
		}
	}

	cases := caseManifestsByID(c)
	principles := principleManifestsByID(c)
	labs := labManifestsByID(c)
	adapters := adapterManifestsByID(c)
	owners := buildClaimOwners(cases, principles)
	for _, claimID := range sortedStringMapKeys(owners) {
		claimOwners := owners[claimID]
		if len(claimOwners) < 2 {
			continue
		}
		ownerNames := make([]string, 0, len(claimOwners))
		for _, owner := range claimOwners {
			ownerNames = append(ownerNames, fmt.Sprintf("%s:%s", owner.kind, owner.id))
		}
		sort.Strings(ownerNames)
		diagnostics = append(diagnostics, errorDiagnostic(CodeDuplicateClaimID, claimID, fmt.Sprintf("claim ID %q is declared by multiple entities: %s", claimID, strings.Join(ownerNames, ", "))))
	}

	for _, caseID := range sortedCaseManifestIDs(cases) {
		manifest := cases[caseID]
		if !scopeCaseIDs.contains(manifest.ID) {
			diagnostics = append(diagnostics, errorDiagnostic(CodeCaseOutsideScope, manifest.ID, fmt.Sprintf("case manifest %q is outside the scope contract", manifest.ID)))
		}
		if !familyIDs.contains(manifest.PrimaryFamily) {
			diagnostics = append(diagnostics, errorDiagnostic(CodeUnknownFamily, manifest.ID, fmt.Sprintf("case %q references unknown primary family %q", manifest.ID, manifest.PrimaryFamily)))
		}
		for _, familyID := range manifest.SecondaryFamilies {
			if !familyIDs.contains(familyID) {
				diagnostics = append(diagnostics, errorDiagnostic(CodeUnknownFamily, manifest.ID, fmt.Sprintf("case %q references unknown secondary family %q", manifest.ID, familyID)))
			}
		}
		for _, principleID := range manifest.Principles {
			canonicalID := resolveID(c, catalog.EntityKindPrinciple, principleID)
			if _, exists := principles[canonicalID]; !exists {
				diagnostics = append(diagnostics, errorDiagnostic(CodeUnknownReference, manifest.ID, fmt.Sprintf("case %q references unknown principle %q", manifest.ID, principleID)))
			}
		}
		declaredLabs := canonicalReferenceSet(c, catalog.EntityKindLab, manifest.Labs)
		for _, labID := range manifest.Labs {
			canonicalID := resolveID(c, catalog.EntityKindLab, labID)
			lab, exists := labs[canonicalID]
			if !exists || lab.Kind != catalog.LabKindScenario {
				diagnostics = append(diagnostics, errorDiagnostic(CodeUnknownReference, manifest.ID, fmt.Sprintf("case %q references unknown scenario lab %q", manifest.ID, labID)))
				continue
			}
			if !hasAnyCaseBinding(c, lab, manifest) {
				diagnostics = append(diagnostics, errorDiagnostic(CodeMissingCaseBinding, manifest.ID, fmt.Sprintf("case %q lists scenario lab %q without a matching case binding", manifest.ID, canonicalID)))
			}
		}
		claimIDs := claimIDSet(manifest.Claims)
		for _, requirement := range manifest.EvidenceRequirements {
			if !claimIDs.contains(requirement.Claim) {
				diagnostics = append(diagnostics, errorDiagnostic(CodeUnknownReference, manifest.ID, fmt.Sprintf("case %q evidence requirement references unknown owned claim %q", manifest.ID, requirement.Claim)))
			}
			canonicalLabID := resolveID(c, catalog.EntityKindLab, requirement.Lab)
			lab, exists := labs[canonicalLabID]
			if !exists || lab.Kind != catalog.LabKindScenario || !declaredLabs.contains(canonicalLabID) {
				diagnostics = append(diagnostics, errorDiagnostic(CodeUnknownReference, manifest.ID, fmt.Sprintf("case %q evidence requirement references undeclared scenario lab %q", manifest.ID, requirement.Lab)))
				continue
			}
			if !hasExactCaseBinding(c, lab, manifest.ID, requirement.Claim) {
				diagnostics = append(diagnostics, errorDiagnostic(CodeMissingCaseBinding, manifest.ID, fmt.Sprintf("case %q claim %q has no reciprocal binding in lab %q", manifest.ID, requirement.Claim, canonicalLabID)))
			}
		}
		diagnostics = append(diagnostics, validateSourceReferences(c, sources, "case", manifest.ID, manifest.Sources)...)
	}

	for _, principleID := range sortedPrincipleManifestIDs(principles) {
		manifest := principles[principleID]
		declaredLabs := canonicalReferenceSet(c, catalog.EntityKindLab, manifest.Labs)
		for _, labID := range manifest.Labs {
			canonicalID := resolveID(c, catalog.EntityKindLab, labID)
			lab, exists := labs[canonicalID]
			if !exists || lab.Kind != catalog.LabKindPrimitive {
				diagnostics = append(diagnostics, errorDiagnostic(CodeUnknownReference, manifest.ID, fmt.Sprintf("principle %q references unknown primitive lab %q", manifest.ID, labID)))
				continue
			}
			if !hasAnyPrincipleBinding(c, lab, manifest) {
				diagnostics = append(diagnostics, errorDiagnostic(CodeMissingPrincipleBinding, manifest.ID, fmt.Sprintf("principle %q lists primitive lab %q without a matching principle binding", manifest.ID, canonicalID)))
			}
		}
		claimIDs := claimIDSet(manifest.Claims)
		for _, requirement := range manifest.EvidenceRequirements {
			if !claimIDs.contains(requirement.Claim) {
				diagnostics = append(diagnostics, errorDiagnostic(CodeUnknownReference, manifest.ID, fmt.Sprintf("principle %q evidence requirement references unknown owned claim %q", manifest.ID, requirement.Claim)))
			}
			canonicalLabID := resolveID(c, catalog.EntityKindLab, requirement.Lab)
			lab, exists := labs[canonicalLabID]
			if !exists || lab.Kind != catalog.LabKindPrimitive || !declaredLabs.contains(canonicalLabID) {
				diagnostics = append(diagnostics, errorDiagnostic(CodeUnknownReference, manifest.ID, fmt.Sprintf("principle %q evidence requirement references undeclared primitive lab %q", manifest.ID, requirement.Lab)))
				continue
			}
			if !hasExactPrincipleBinding(c, lab, manifest.ID, requirement.Claim) {
				diagnostics = append(diagnostics, errorDiagnostic(CodeMissingPrincipleBinding, manifest.ID, fmt.Sprintf("principle %q claim %q has no reciprocal binding in lab %q", manifest.ID, requirement.Claim, canonicalLabID)))
			}
		}
		diagnostics = append(diagnostics, validateSourceReferences(c, sources, "principle", manifest.ID, manifest.Sources)...)
	}

	referencedLabs := makeStringSet()
	for _, manifest := range cases {
		for _, labID := range manifest.Labs {
			referencedLabs.add(resolveID(c, catalog.EntityKindLab, labID))
		}
	}
	for _, manifest := range principles {
		for _, labID := range manifest.Labs {
			referencedLabs.add(resolveID(c, catalog.EntityKindLab, labID))
		}
	}
	for _, labID := range sortedLabManifestIDs(labs) {
		lab := labs[labID]
		if !referencedLabs.contains(lab.ID) {
			diagnostics = append(diagnostics, errorDiagnostic(CodeOrphanedLab, lab.ID, fmt.Sprintf("lab %q is not owned by any case or principle", lab.ID)))
		}
		switch lab.Kind {
		case catalog.LabKindScenario:
			if len(lab.PrincipleBindings) != 0 {
				diagnostics = append(diagnostics, errorDiagnostic(CodeOrphanedLab, lab.ID, fmt.Sprintf("scenario lab %q contains principle bindings", lab.ID)))
			}
			for _, binding := range lab.CaseBindings {
				ownerID := resolveID(c, catalog.EntityKindCase, binding.CaseID)
				owner, exists := cases[ownerID]
				if !exists {
					diagnostics = append(diagnostics, errorDiagnostic(CodeUnknownReference, lab.ID, fmt.Sprintf("scenario lab %q binding %q references unknown case %q", lab.ID, binding.ID, binding.CaseID)))
				}
				if !exists || !canonicalReferenceSet(c, catalog.EntityKindLab, owner.Labs).contains(lab.ID) || !hasCaseEvidenceEdge(c, owner, lab.ID, binding.Claim) {
					diagnostics = append(diagnostics, errorDiagnostic(CodeOrphanedLab, lab.ID, fmt.Sprintf("scenario lab %q binding %q has no reverse owner edge from case %q", lab.ID, binding.ID, binding.CaseID)))
				}
			}
		case catalog.LabKindPrimitive:
			if len(lab.CaseBindings) != 0 {
				diagnostics = append(diagnostics, errorDiagnostic(CodeOrphanedLab, lab.ID, fmt.Sprintf("primitive lab %q contains case bindings", lab.ID)))
			}
			for _, binding := range lab.PrincipleBindings {
				ownerID := resolveID(c, catalog.EntityKindPrinciple, binding.PrincipleID)
				owner, exists := principles[ownerID]
				if !exists {
					diagnostics = append(diagnostics, errorDiagnostic(CodeUnknownReference, lab.ID, fmt.Sprintf("primitive lab %q binding %q references unknown principle %q", lab.ID, binding.ID, binding.PrincipleID)))
				}
				if !exists || !canonicalReferenceSet(c, catalog.EntityKindLab, owner.Labs).contains(lab.ID) || !hasPrincipleEvidenceEdge(c, owner, lab.ID, binding.Claim) {
					diagnostics = append(diagnostics, errorDiagnostic(CodeOrphanedLab, lab.ID, fmt.Sprintf("primitive lab %q binding %q has no reverse owner edge from principle %q", lab.ID, binding.ID, binding.PrincipleID)))
				}
			}
		}
		diagnostics = append(diagnostics, validateSourceReferences(c, sources, "lab", lab.ID, lab.Sources)...)
	}
	for _, adapterID := range sortedStringMapKeys(adapters) {
		adapter := adapters[adapterID]
		diagnostics = append(diagnostics, validateSourceReferences(c, sources, "adapter", adapter.ID, adapter.Sources)...)
	}
	return diagnostics
}

func sourceRecordsByID(c *catalog.Catalog) map[string]catalog.SourceRecord {
	indexed := make(map[string]catalog.SourceRecord, len(c.Sources))
	for _, key := range sortedStringMapKeys(c.Sources) {
		record := c.Sources[key]
		if _, exists := indexed[record.ID]; !exists {
			indexed[record.ID] = record
		}
	}
	return indexed
}

func buildClaimOwners(cases map[string]catalog.CaseManifest, principles map[string]catalog.PrincipleManifest) map[string][]claimOwner {
	owners := make(map[string][]claimOwner)
	for _, caseID := range sortedCaseManifestIDs(cases) {
		for _, claim := range cases[caseID].Claims {
			owners[claim.ID] = append(owners[claim.ID], claimOwner{kind: catalog.EntityKindCase, id: caseID})
		}
	}
	for _, principleID := range sortedPrincipleManifestIDs(principles) {
		for _, claim := range principles[principleID].Claims {
			owners[claim.ID] = append(owners[claim.ID], claimOwner{kind: catalog.EntityKindPrinciple, id: principleID})
		}
	}
	return owners
}

func claimOwnedBy(owners map[string][]claimOwner, claimID string, kind catalog.EntityKind, ownerID string) bool {
	for _, owner := range owners[claimID] {
		if owner.kind == kind && owner.id == ownerID {
			return true
		}
	}
	return false
}

func claimIDSet(claims []catalog.Claim) stringSet {
	ids := makeStringSet()
	for _, claim := range claims {
		ids.add(claim.ID)
	}
	return ids
}

func canonicalReferenceSet(c *catalog.Catalog, kind catalog.EntityKind, references []string) stringSet {
	ids := makeStringSet()
	for _, reference := range references {
		ids.add(resolveID(c, kind, reference))
	}
	return ids
}

func hasAnyCaseBinding(c *catalog.Catalog, lab catalog.LabManifest, manifest catalog.CaseManifest) bool {
	claims := claimIDSet(manifest.Claims)
	for _, binding := range lab.CaseBindings {
		if resolveID(c, catalog.EntityKindCase, binding.CaseID) == manifest.ID && claims.contains(binding.Claim) {
			return true
		}
	}
	return false
}

func hasExactCaseBinding(c *catalog.Catalog, lab catalog.LabManifest, caseID, claimID string) bool {
	for _, binding := range lab.CaseBindings {
		if resolveID(c, catalog.EntityKindCase, binding.CaseID) == caseID && binding.Claim == claimID {
			return true
		}
	}
	return false
}

func hasAnyPrincipleBinding(c *catalog.Catalog, lab catalog.LabManifest, manifest catalog.PrincipleManifest) bool {
	claims := claimIDSet(manifest.Claims)
	for _, binding := range lab.PrincipleBindings {
		if resolveID(c, catalog.EntityKindPrinciple, binding.PrincipleID) == manifest.ID && claims.contains(binding.Claim) {
			return true
		}
	}
	return false
}

func hasExactPrincipleBinding(c *catalog.Catalog, lab catalog.LabManifest, principleID, claimID string) bool {
	for _, binding := range lab.PrincipleBindings {
		if resolveID(c, catalog.EntityKindPrinciple, binding.PrincipleID) == principleID && binding.Claim == claimID {
			return true
		}
	}
	return false
}

func hasCaseEvidenceEdge(c *catalog.Catalog, manifest catalog.CaseManifest, labID, claimID string) bool {
	for _, requirement := range manifest.EvidenceRequirements {
		if resolveID(c, catalog.EntityKindLab, requirement.Lab) == labID && requirement.Claim == claimID {
			return true
		}
	}
	return false
}

func hasPrincipleEvidenceEdge(c *catalog.Catalog, manifest catalog.PrincipleManifest, labID, claimID string) bool {
	for _, requirement := range manifest.EvidenceRequirements {
		if resolveID(c, catalog.EntityKindLab, requirement.Lab) == labID && requirement.Claim == claimID {
			return true
		}
	}
	return false
}

func validateSourceReferences(c *catalog.Catalog, sources map[string]catalog.SourceRecord, kind, entityID string, references []string) []Diagnostic {
	diagnostics := make([]Diagnostic, 0)
	for _, sourceID := range references {
		canonicalID := resolveID(c, catalog.EntityKindSource, sourceID)
		if _, exists := sources[canonicalID]; !exists {
			diagnostics = append(diagnostics, errorDiagnostic(CodeDanglingSource, entityID, fmt.Sprintf("%s %q references unknown source %q", kind, entityID, sourceID)))
		}
	}
	return diagnostics
}
