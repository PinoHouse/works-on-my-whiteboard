package validator

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
)

var stableIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func validateSchemaContracts(c *catalog.Catalog) []Diagnostic {
	if c == nil {
		return []Diagnostic{errorDiagnostic(CodeDependencyIncomplete, "", "catalog is nil")}
	}
	diagnostics := make([]Diagnostic, 0)
	knownFamilies := makeStringSet()
	for _, familyID := range catalog.FamilyIDs {
		knownFamilies.add(string(familyID))
	}
	knownDimensions := makeStringSet()
	for _, dimensionID := range catalog.DimensionIDs {
		knownDimensions.add(string(dimensionID))
	}

	for _, family := range c.Scope.Families {
		appendStableIDDiagnostic(&diagnostics, family.ID, "scope family id", family.ID)
		if !knownFamilies.contains(family.ID) {
			diagnostics = append(diagnostics, errorDiagnostic(
				CodeUnknownFamily,
				family.ID,
				fmt.Sprintf("scope family %q is not in the approved family registry", family.ID),
			))
		}
	}
	for _, scopeCase := range c.Scope.Cases {
		appendStableIDDiagnostic(&diagnostics, scopeCase.ID, "scope case id", scopeCase.ID)
		appendStableIDDiagnostic(&diagnostics, scopeCase.ID, "scope case primary_family", scopeCase.PrimaryFamily)
	}
	for _, exclusion := range c.Scope.Exclusions {
		appendStableIDDiagnostic(&diagnostics, exclusion.ID, "scope exclusion id", exclusion.ID)
		appendStableIDDiagnostic(&diagnostics, exclusion.ID, "scope exclusion canonical_case_id", exclusion.CanonicalCaseID)
	}
	for _, alias := range c.Aliases.Entries() {
		appendStableIDDiagnostic(&diagnostics, alias.From, "alias from", alias.From)
		appendStableIDDiagnostic(&diagnostics, alias.From, "alias to", alias.To)
	}

	for _, sourceID := range sortedStringMapKeys(c.Sources) {
		source := c.Sources[sourceID]
		appendStableIDDiagnostic(&diagnostics, source.ID, "source id", source.ID)
		appendStableIDDiagnostic(&diagnostics, source.ID, "source kind", source.Kind)
	}

	cases := caseManifestsByID(c)
	for _, caseID := range sortedCaseManifestIDs(cases) {
		manifest := cases[caseID]
		appendStableIDDiagnostic(&diagnostics, manifest.ID, "case id", manifest.ID)
		appendStableIDDiagnostic(&diagnostics, manifest.ID, "case primary_family", manifest.PrimaryFamily)
		for _, familyID := range manifest.SecondaryFamilies {
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "case secondary_families reference", familyID)
		}
		for _, dimensionID := range manifest.Dimensions {
			id := string(dimensionID)
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "case dimension reference", id)
			if !knownDimensions.contains(id) {
				diagnostics = append(diagnostics, errorDiagnostic(CodeUnknownReference, manifest.ID, fmt.Sprintf("case %q references unknown dimension %q", manifest.ID, id)))
			}
		}
		for _, principleID := range manifest.Principles {
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "case principle reference", principleID)
		}
		for _, claim := range manifest.Claims {
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "case claim id", claim.ID)
		}
		for _, labID := range manifest.Labs {
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "case lab reference", labID)
		}
		for _, requirement := range manifest.EvidenceRequirements {
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "case evidence claim reference", requirement.Claim)
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "case evidence lab reference", requirement.Lab)
		}
		for _, sourceID := range manifest.Sources {
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "case source reference", sourceID)
		}
		appendLifecycleDiagnostic(&diagnostics, "case", manifest.ID, manifest.Status)
		if manifest.Status == catalog.LifecycleStatusComplete {
			missing := missingCompleteCaseFields(manifest)
			appendCompleteContractDiagnostic(&diagnostics, "case", manifest.ID, missing)
		}
	}

	principles := principleManifestsByID(c)
	for _, principleID := range sortedPrincipleManifestIDs(principles) {
		manifest := principles[principleID]
		appendStableIDDiagnostic(&diagnostics, manifest.ID, "principle id", manifest.ID)
		for _, dimensionID := range manifest.Dimensions {
			id := string(dimensionID)
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "principle dimension reference", id)
			if !knownDimensions.contains(id) {
				diagnostics = append(diagnostics, errorDiagnostic(CodeUnknownReference, manifest.ID, fmt.Sprintf("principle %q references unknown dimension %q", manifest.ID, id)))
			}
		}
		for _, claim := range manifest.Claims {
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "principle claim id", claim.ID)
		}
		for _, labID := range manifest.Labs {
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "principle lab reference", labID)
		}
		for _, requirement := range manifest.EvidenceRequirements {
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "principle evidence claim reference", requirement.Claim)
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "principle evidence lab reference", requirement.Lab)
		}
		for _, sourceID := range manifest.Sources {
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "principle source reference", sourceID)
		}
		appendLifecycleDiagnostic(&diagnostics, "principle", manifest.ID, manifest.Status)
		if manifest.Status == catalog.LifecycleStatusComplete {
			missing := missingCompletePrincipleFields(manifest)
			appendCompleteContractDiagnostic(&diagnostics, "principle", manifest.ID, missing)
		}
	}

	labs := labManifestsByID(c)
	for _, labID := range sortedLabManifestIDs(labs) {
		manifest := labs[labID]
		appendStableIDDiagnostic(&diagnostics, manifest.ID, "lab id", manifest.ID)
		for _, implementationID := range manifest.Implementations {
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "lab implementation id", implementationID)
		}
		for _, binding := range manifest.CaseBindings {
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "case binding id", binding.ID)
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "case binding case reference", binding.CaseID)
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "case binding claim reference", binding.Claim)
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "case binding workload", binding.Workload)
			for _, assertionID := range binding.Assertions {
				appendStableIDDiagnostic(&diagnostics, manifest.ID, "case binding assertion id", assertionID)
			}
		}
		for _, binding := range manifest.PrincipleBindings {
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "principle binding id", binding.ID)
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "principle binding principle reference", binding.PrincipleID)
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "principle binding claim reference", binding.Claim)
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "principle binding workload", binding.Workload)
			for _, assertionID := range binding.Assertions {
				appendStableIDDiagnostic(&diagnostics, manifest.ID, "principle binding assertion id", assertionID)
			}
		}
		for _, run := range manifest.RequiredRuns {
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "required run id", run.ID)
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "required run binding reference", run.Binding)
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "required run baseline reference", run.Baseline)
			for _, variantID := range run.Variants {
				appendStableIDDiagnostic(&diagnostics, manifest.ID, "required run variant reference", variantID)
			}
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "required run workload", run.Workload)
			for _, faultID := range run.Faults {
				appendStableIDDiagnostic(&diagnostics, manifest.ID, "required run fault id", faultID)
			}
			for _, adapter := range run.Adapters {
				appendStableIDDiagnostic(&diagnostics, manifest.ID, "required run adapter reference", adapter.ID)
			}
		}
		for _, metricID := range manifest.Metrics {
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "lab metric id", metricID)
		}
		for _, sourceID := range manifest.Sources {
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "lab source reference", sourceID)
		}
		appendLifecycleDiagnostic(&diagnostics, "lab", manifest.ID, manifest.Status)
		if manifest.Kind != catalog.LabKindScenario && manifest.Kind != catalog.LabKindPrimitive {
			diagnostics = append(diagnostics, errorDiagnostic(CodeUnknownReference, manifest.ID, fmt.Sprintf("lab %q has unknown kind %q", manifest.ID, manifest.Kind)))
		}
		if manifest.Status == catalog.LifecycleStatusComplete {
			missing := missingCompleteLabFields(manifest)
			appendCompleteContractDiagnostic(&diagnostics, "lab", manifest.ID, missing)
		}
	}

	adapters := adapterManifestsByID(c)
	for _, adapterID := range sortedStringMapKeys(adapters) {
		manifest := adapters[adapterID]
		appendStableIDDiagnostic(&diagnostics, manifest.ID, "adapter id", manifest.ID)
		if manifest.Interface != "" {
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "adapter interface", manifest.Interface)
		}
		if manifest.Runtime != "" {
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "adapter runtime", manifest.Runtime)
		}
		for _, sourceID := range manifest.Sources {
			appendStableIDDiagnostic(&diagnostics, manifest.ID, "adapter source reference", sourceID)
		}
		appendLifecycleDiagnostic(&diagnostics, "adapter", manifest.ID, manifest.Status)
		if manifest.Status == catalog.LifecycleStatusComplete {
			missing := missingCompleteAdapterFields(manifest)
			appendCompleteContractDiagnostic(&diagnostics, "adapter", manifest.ID, missing)
		}
	}
	return diagnostics
}

func appendStableIDDiagnostic(diagnostics *[]Diagnostic, entityID, field, value string) {
	if stableIDPattern.MatchString(value) {
		return
	}
	*diagnostics = append(*diagnostics, errorDiagnostic(
		CodeInvalidStableID,
		entityID,
		fmt.Sprintf("%s %q does not match ^[a-z][a-z0-9-]*$", field, value),
	))
}

func appendLifecycleDiagnostic(diagnostics *[]Diagnostic, kind, entityID string, status catalog.LifecycleStatus) {
	if status == catalog.LifecycleStatusDraft || status == catalog.LifecycleStatusComplete {
		return
	}
	*diagnostics = append(*diagnostics, errorDiagnostic(
		CodeStatusVocabularyMismatch,
		entityID,
		fmt.Sprintf("%s %q uses lifecycle status %q; want draft or complete", kind, entityID, status),
	))
}

func appendCompleteContractDiagnostic(diagnostics *[]Diagnostic, kind, entityID string, missing []string) {
	if len(missing) == 0 {
		return
	}
	*diagnostics = append(*diagnostics, errorDiagnostic(
		CodeCompleteContractEmpty,
		entityID,
		fmt.Sprintf("complete %s %q has empty required fields: %s", kind, entityID, strings.Join(missing, ", ")),
	))
}

func missingCompleteCaseFields(manifest catalog.CaseManifest) []string {
	missing := make([]string, 0)
	appendBlank(&missing, "title", manifest.Title)
	appendBlank(&missing, "primary_family", manifest.PrimaryFamily)
	appendEmpty(&missing, "dimensions", len(manifest.Dimensions))
	for index, dimension := range manifest.Dimensions {
		appendBlank(&missing, fmt.Sprintf("dimensions[%d]", index), string(dimension))
	}
	appendEmpty(&missing, "principles", len(manifest.Principles))
	appendEmpty(&missing, "claims", len(manifest.Claims))
	appendEmpty(&missing, "labs", len(manifest.Labs))
	appendEmpty(&missing, "evidence_requirements", len(manifest.EvidenceRequirements))
	appendEmpty(&missing, "sources", len(manifest.Sources))
	for index, claim := range manifest.Claims {
		appendBlank(&missing, fmt.Sprintf("claims[%d].id", index), claim.ID)
		if strings.TrimSpace(claim.Statement) == "" {
			missing = append(missing, fmt.Sprintf("claims[%d].statement", index))
		}
	}
	for index, requirement := range manifest.EvidenceRequirements {
		appendBlank(&missing, fmt.Sprintf("evidence_requirements[%d].claim", index), requirement.Claim)
		appendBlank(&missing, fmt.Sprintf("evidence_requirements[%d].lab", index), requirement.Lab)
	}
	appendBlankStringItems(&missing, "principles", manifest.Principles)
	appendBlankStringItems(&missing, "labs", manifest.Labs)
	appendBlankStringItems(&missing, "sources", manifest.Sources)
	return missing
}

func missingCompletePrincipleFields(manifest catalog.PrincipleManifest) []string {
	missing := make([]string, 0)
	appendBlank(&missing, "title", manifest.Title)
	appendEmpty(&missing, "dimensions", len(manifest.Dimensions))
	for index, dimension := range manifest.Dimensions {
		appendBlank(&missing, fmt.Sprintf("dimensions[%d]", index), string(dimension))
	}
	appendEmpty(&missing, "claims", len(manifest.Claims))
	appendEmpty(&missing, "labs", len(manifest.Labs))
	appendEmpty(&missing, "evidence_requirements", len(manifest.EvidenceRequirements))
	appendEmpty(&missing, "sources", len(manifest.Sources))
	for index, claim := range manifest.Claims {
		appendBlank(&missing, fmt.Sprintf("claims[%d].id", index), claim.ID)
		if strings.TrimSpace(claim.Statement) == "" {
			missing = append(missing, fmt.Sprintf("claims[%d].statement", index))
		}
	}
	for index, requirement := range manifest.EvidenceRequirements {
		appendBlank(&missing, fmt.Sprintf("evidence_requirements[%d].claim", index), requirement.Claim)
		appendBlank(&missing, fmt.Sprintf("evidence_requirements[%d].lab", index), requirement.Lab)
	}
	appendBlankStringItems(&missing, "labs", manifest.Labs)
	appendBlankStringItems(&missing, "sources", manifest.Sources)
	return missing
}

func missingCompleteLabFields(manifest catalog.LabManifest) []string {
	missing := make([]string, 0)
	appendEmpty(&missing, "implementations", len(manifest.Implementations))
	appendEmpty(&missing, "required_runs", len(manifest.RequiredRuns))
	appendEmpty(&missing, "metrics", len(manifest.Metrics))
	appendEmpty(&missing, "sources", len(manifest.Sources))
	switch manifest.Kind {
	case catalog.LabKindScenario:
		appendEmpty(&missing, "case_bindings", len(manifest.CaseBindings))
	case catalog.LabKindPrimitive:
		appendEmpty(&missing, "principle_bindings", len(manifest.PrincipleBindings))
	}
	for index, binding := range manifest.CaseBindings {
		appendBlank(&missing, fmt.Sprintf("case_bindings[%d].id", index), binding.ID)
		appendBlank(&missing, fmt.Sprintf("case_bindings[%d].case_id", index), binding.CaseID)
		appendBlank(&missing, fmt.Sprintf("case_bindings[%d].claim", index), binding.Claim)
		appendBlank(&missing, fmt.Sprintf("case_bindings[%d].workload", index), binding.Workload)
		appendEmpty(&missing, fmt.Sprintf("case_bindings[%d].assertions", index), len(binding.Assertions))
		appendBlankStringItems(&missing, fmt.Sprintf("case_bindings[%d].assertions", index), binding.Assertions)
	}
	for index, binding := range manifest.PrincipleBindings {
		appendBlank(&missing, fmt.Sprintf("principle_bindings[%d].id", index), binding.ID)
		appendBlank(&missing, fmt.Sprintf("principle_bindings[%d].principle_id", index), binding.PrincipleID)
		appendBlank(&missing, fmt.Sprintf("principle_bindings[%d].claim", index), binding.Claim)
		appendBlank(&missing, fmt.Sprintf("principle_bindings[%d].workload", index), binding.Workload)
		appendEmpty(&missing, fmt.Sprintf("principle_bindings[%d].assertions", index), len(binding.Assertions))
		appendBlankStringItems(&missing, fmt.Sprintf("principle_bindings[%d].assertions", index), binding.Assertions)
	}
	for index, run := range manifest.RequiredRuns {
		appendBlank(&missing, fmt.Sprintf("required_runs[%d].id", index), run.ID)
		appendBlank(&missing, fmt.Sprintf("required_runs[%d].binding", index), run.Binding)
		appendBlank(&missing, fmt.Sprintf("required_runs[%d].baseline", index), run.Baseline)
		appendEmpty(&missing, fmt.Sprintf("required_runs[%d].variants", index), len(run.Variants))
		appendBlankStringItems(&missing, fmt.Sprintf("required_runs[%d].variants", index), run.Variants)
		appendBlank(&missing, fmt.Sprintf("required_runs[%d].workload", index), run.Workload)
		appendEmpty(&missing, fmt.Sprintf("required_runs[%d].faults", index), len(run.Faults))
		appendBlankStringItems(&missing, fmt.Sprintf("required_runs[%d].faults", index), run.Faults)
		for adapterIndex, adapter := range run.Adapters {
			appendBlank(&missing, fmt.Sprintf("required_runs[%d].adapters[%d].id", index, adapterIndex), adapter.ID)
		}
	}
	appendBlankStringItems(&missing, "implementations", manifest.Implementations)
	appendBlankStringItems(&missing, "metrics", manifest.Metrics)
	appendBlankStringItems(&missing, "sources", manifest.Sources)
	return missing
}

func missingCompleteAdapterFields(manifest catalog.AdapterManifest) []string {
	missing := make([]string, 0)
	appendBlank(&missing, "title", manifest.Title)
	appendBlank(&missing, "interface", manifest.Interface)
	appendBlank(&missing, "runtime", manifest.Runtime)
	appendEmpty(&missing, "sources", len(manifest.Sources))
	appendBlankStringItems(&missing, "sources", manifest.Sources)
	return missing
}

func appendBlank(missing *[]string, field, value string) {
	if strings.TrimSpace(value) == "" {
		*missing = append(*missing, field)
	}
}

func appendEmpty(missing *[]string, field string, length int) {
	if length == 0 {
		*missing = append(*missing, field)
	}
}

func appendBlankStringItems(missing *[]string, field string, values []string) {
	for index, value := range values {
		appendBlank(missing, fmt.Sprintf("%s[%d]", field, index), value)
	}
}
