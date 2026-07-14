package content

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

type claimClass string

const (
	claimClassAssumed  claimClass = "ASSUMED"
	claimClassDeduced  claimClass = "DEDUCED"
	claimClassMeasured claimClass = "MEASURED"
	claimClassSourced  claimClass = "SOURCED"
)

var claimClasses = []claimClass{
	claimClassAssumed,
	claimClassDeduced,
	claimClassMeasured,
	claimClassSourced,
}

var unfinishedMarkers = []string{
	"TODO",
	"TBD",
	"FIXME",
	"XXX",
	"待补充",
	"待完善",
}

type claimMarker struct {
	class    claimClass
	claimID  string
	sourceID string
	block    string
}

type ownerContract struct {
	kind                 catalog.EntityKind
	id                   string
	claims               []catalog.Claim
	evidenceRequirements []catalog.EvidenceRequirement
	sources              []string
}

func validateProseContracts(path string, claimBlocks, unfinishedBlocks []proseBlock, owner ownerContract, repository *catalog.Catalog) []validator.Diagnostic {
	diagnostics := make([]validator.Diagnostic, 0)
	for _, marker := range unfinishedMarkers {
		if proseBlocksContain(unfinishedBlocks, marker) {
			diagnostics = append(diagnostics, contentDiagnostic(
				CodeUnfinishedMarker,
				path,
				owner.id,
				fmt.Sprintf("unfinished marker %q appears in prose", marker),
			))
		}
	}

	markers := make([]claimMarker, 0)
	for _, block := range claimBlocks {
		parsed, malformed := scanClaimMarkers(block.text)
		for _, raw := range malformed {
			diagnostics = append(diagnostics, contentDiagnostic(
				CodeInvalidClaimMarker,
				path,
				owner.id,
				fmt.Sprintf("claim marker %q is not one of the exact supported forms", raw),
			))
		}
		for _, marker := range parsed {
			marker.block = block.text
			markers = append(markers, marker)
		}
	}

	declared := make(map[string]struct{}, len(owner.claims))
	for _, claim := range owner.claims {
		declared[claim.ID] = struct{}{}
	}
	usedClasses := make(map[string]map[claimClass]struct{})
	for _, marker := range markers {
		if _, exists := declared[marker.claimID]; !exists {
			diagnostics = append(diagnostics, contentDiagnostic(
				CodeUnknownClaim,
				path,
				owner.id,
				fmt.Sprintf("claim marker references undeclared claim %q", marker.claimID),
			))
			continue
		}
		if usedClasses[marker.claimID] == nil {
			usedClasses[marker.claimID] = make(map[claimClass]struct{})
		}
		usedClasses[marker.claimID][marker.class] = struct{}{}

		switch marker.class {
		case claimClassAssumed:
			if !strings.Contains(marker.block, "原因：") || !strings.Contains(marker.block, "变化影响：") {
				diagnostics = append(diagnostics, contentDiagnostic(
					CodeAssumptionContextMissing,
					path,
					owner.id,
					fmt.Sprintf("ASSUMED claim %q must include 原因： and 变化影响： in its containing block", marker.claimID),
				))
			}
		case claimClassMeasured:
			if !measuredClaimHasRequiredRun(owner, marker.claimID, repository) {
				diagnostics = append(diagnostics, contentDiagnostic(
					CodeMeasuredClaimUnbound,
					path,
					owner.id,
					fmt.Sprintf("MEASURED claim %q does not resolve through an exact binding to a consuming required run", marker.claimID),
				))
			}
		case claimClassSourced:
			if !sourcedClaimHasOwnerClosure(owner, marker.sourceID, repository) {
				diagnostics = append(diagnostics, contentDiagnostic(
					CodeSourcedClaimInvalid,
					path,
					owner.id,
					fmt.Sprintf("SOURCED claim %q source %q must exist and be listed by its owner", marker.claimID, marker.sourceID),
				))
			}
		}
	}

	claimIDs := make([]string, 0, len(declared))
	for claimID := range declared {
		claimIDs = append(claimIDs, claimID)
	}
	sort.Strings(claimIDs)
	for _, claimID := range claimIDs {
		classes := usedClasses[claimID]
		if len(classes) == 0 {
			diagnostics = append(diagnostics, contentDiagnostic(
				CodeMissingClaimMarker,
				path,
				owner.id,
				fmt.Sprintf("declared claim %q does not appear in prose", claimID),
			))
			continue
		}
		if len(classes) > 1 {
			classNames := make([]string, 0, len(classes))
			for class := range classes {
				classNames = append(classNames, string(class))
			}
			sort.Strings(classNames)
			diagnostics = append(diagnostics, contentDiagnostic(
				CodeConflictingClaimClass,
				path,
				owner.id,
				fmt.Sprintf("claim %q appears with conflicting classes %v", claimID, classNames),
			))
		}
	}
	return diagnostics
}

func proseBlocksContain(blocks []proseBlock, marker string) bool {
	for _, block := range blocks {
		if strings.Contains(block.text, marker) {
			return true
		}
	}
	return false
}

func scanClaimMarkers(text string) ([]claimMarker, []string) {
	markers := make([]claimMarker, 0)
	malformed := make([]string, 0)
	for offset := 0; offset < len(text); {
		start := strings.IndexByte(text[offset:], '[')
		if start < 0 {
			break
		}
		start += offset
		endRelative := strings.IndexByte(text[start+1:], ']')
		if endRelative < 0 {
			tail := text[start:]
			if beginsClaimMarker(tail) {
				malformed = append(malformed, tail)
			}
			break
		}
		end := start + 1 + endRelative
		raw := text[start : end+1]
		inner := text[start+1 : end]
		marker, ok, claimLike := parseClaimMarker(inner)
		if ok {
			markers = append(markers, marker)
		} else if claimLike {
			malformed = append(malformed, raw)
		}
		if !ok && !claimLike {
			offset = start + 1
			continue
		}
		offset = end + 1
	}
	return markers, malformed
}

func parseClaimMarker(inner string) (claimMarker, bool, bool) {
	parts := strings.Split(inner, ":")
	if len(parts) == 0 {
		return claimMarker{}, false, false
	}
	class, known := knownClaimClass(parts[0])
	claimLike := reservedClaimCandidate(inner)
	if !known {
		return claimMarker{}, false, claimLike
	}
	if class == claimClassSourced {
		if len(parts) != 3 || !isStableMarkerID(parts[1]) || !isStableMarkerID(parts[2]) {
			return claimMarker{}, false, true
		}
		return claimMarker{class: class, claimID: parts[1], sourceID: parts[2]}, true, true
	}
	if len(parts) != 2 || !isStableMarkerID(parts[1]) {
		return claimMarker{}, false, true
	}
	return claimMarker{class: class, claimID: parts[1]}, true, true
}

func beginsClaimMarker(value string) bool {
	if !strings.HasPrefix(value, "[") {
		return false
	}
	return reservedClaimCandidate(value[1:])
}

func reservedClaimCandidate(value string) bool {
	value = strings.ToUpper(strings.TrimSpace(value))
	for _, class := range claimClasses {
		className := string(class)
		if !strings.HasPrefix(value, className) {
			continue
		}
		suffix := value[len(className):]
		if suffix == "" {
			return true
		}
		first, _ := utf8.DecodeRuneInString(suffix)
		if first == ':' || unicode.IsSpace(first) {
			return true
		}
	}
	return false
}

func knownClaimClass(value string) (claimClass, bool) {
	for _, class := range claimClasses {
		if value == string(class) {
			return class, true
		}
	}
	return "", false
}

func isStableMarkerID(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		char := value[index]
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func measuredClaimHasRequiredRun(owner ownerContract, claimID string, repository *catalog.Catalog) bool {
	if repository == nil {
		return false
	}
	for _, requirement := range owner.evidenceRequirements {
		if requirement.Claim != claimID {
			continue
		}
		labID := resolveCatalogID(repository, catalog.EntityKindLab, requirement.Lab)
		lab, exists := repositoryLab(repository, labID)
		if !exists || !labKindMatchesOwner(lab.Kind, owner.kind) {
			continue
		}
		bindingIDs := exactOwnerBindingIDs(repository, lab, owner, claimID)
		for _, run := range lab.RequiredRuns {
			if _, exists := bindingIDs[run.Binding]; exists {
				return true
			}
		}
	}
	return false
}

func labKindMatchesOwner(kind catalog.LabKind, ownerKind catalog.EntityKind) bool {
	return kind == catalog.LabKindScenario && ownerKind == catalog.EntityKindCase ||
		kind == catalog.LabKindPrimitive && ownerKind == catalog.EntityKindPrinciple
}

func exactOwnerBindingIDs(repository *catalog.Catalog, lab catalog.LabManifest, owner ownerContract, claimID string) map[string]struct{} {
	ids := make(map[string]struct{})
	bindingIDCounts := make(map[string]int, len(lab.CaseBindings)+len(lab.PrincipleBindings))
	for _, binding := range lab.CaseBindings {
		bindingIDCounts[binding.ID]++
	}
	for _, binding := range lab.PrincipleBindings {
		bindingIDCounts[binding.ID]++
	}
	switch owner.kind {
	case catalog.EntityKindCase:
		for _, binding := range lab.CaseBindings {
			bindingOwner := resolveCatalogID(repository, catalog.EntityKindCase, binding.CaseID)
			if bindingOwner == owner.id && binding.Claim == claimID && bindingIDCounts[binding.ID] == 1 {
				ids[binding.ID] = struct{}{}
			}
		}
	case catalog.EntityKindPrinciple:
		for _, binding := range lab.PrincipleBindings {
			bindingOwner := resolveCatalogID(repository, catalog.EntityKindPrinciple, binding.PrincipleID)
			if bindingOwner == owner.id && binding.Claim == claimID && bindingIDCounts[binding.ID] == 1 {
				ids[binding.ID] = struct{}{}
			}
		}
	}
	return ids
}

func sourcedClaimHasOwnerClosure(owner ownerContract, sourceID string, repository *catalog.Catalog) bool {
	if repository == nil {
		return false
	}
	canonicalSource := resolveCatalogID(repository, catalog.EntityKindSource, sourceID)
	if _, exists := repositorySource(repository, canonicalSource); !exists {
		return false
	}
	for _, reference := range owner.sources {
		if resolveCatalogID(repository, catalog.EntityKindSource, reference) == canonicalSource {
			return true
		}
	}
	return false
}

func repositoryLab(repository *catalog.Catalog, id string) (catalog.LabManifest, bool) {
	if lab, exists := repository.Labs[id]; exists {
		return lab, true
	}
	for _, lab := range repository.Labs {
		if lab.ID == id {
			return lab, true
		}
	}
	return catalog.LabManifest{}, false
}

func repositorySource(repository *catalog.Catalog, id string) (catalog.SourceRecord, bool) {
	if source, exists := repository.Sources[id]; exists {
		return source, true
	}
	for _, source := range repository.Sources {
		if source.ID == id {
			return source, true
		}
	}
	return catalog.SourceRecord{}, false
}

func resolveCatalogID(repository *catalog.Catalog, kind catalog.EntityKind, id string) string {
	if repository == nil {
		return id
	}
	resolved, err := repository.Aliases.Resolve(kind, id)
	if err != nil {
		return id
	}
	return resolved
}
