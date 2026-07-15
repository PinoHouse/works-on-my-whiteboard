package report

import (
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/release"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

func Build(c *catalog.Catalog, validation validator.Report, snapshot release.AuditedSnapshot) (Model, error) {
	if err := release.ValidateAuditedSnapshot(snapshot); err != nil {
		return Model{}, fmt.Errorf("build report: %w", err)
	}
	if c == nil {
		return Model{}, fmt.Errorf("%w: catalog is nil", ErrModelInvalid)
	}
	if err := validateCatalogAliases(c); err != nil {
		return Model{}, err
	}
	computedCoverage := validator.ComputeCoverage(c)
	if !reflect.DeepEqual(validation.Coverage, computedCoverage) {
		return Model{}, fmt.Errorf("%w: validation coverage differs from catalog", ErrModelInvalid)
	}
	computedMatrix, _ := validator.BuildRequiredMatrix(c)
	if !reflect.DeepEqual(validation.Matrix, computedMatrix) {
		return Model{}, fmt.Errorf("%w: validation matrix differs from catalog", ErrModelInvalid)
	}
	if err := validateDiagnostics(validation.Diagnostics); err != nil {
		return Model{}, err
	}
	if len(validation.Matrix) != len(snapshot.Expected) || len(snapshot.Records) != len(snapshot.Expected) {
		return Model{}, fmt.Errorf("%w: validation matrix and audited snapshot lengths differ", ErrModelInvalid)
	}

	rows := make([]Row, len(snapshot.Records))
	sourceIDs := make(map[string]struct{})
	for index, record := range snapshot.Records {
		if !reflect.DeepEqual(validation.Matrix[index], snapshot.Expected[index].Cell) {
			return Model{}, fmt.Errorf("%w: validation matrix cell %d differs from audited expected cell", ErrModelInvalid, index)
		}
		rows[index] = rowFromRecord(validation.Matrix[index], record)
		ids, err := referencedSourceIDs(c, validation.Matrix[index])
		if err != nil {
			return Model{}, err
		}
		for _, id := range ids {
			sourceIDs[id] = struct{}{}
		}
	}

	sources, err := sourceLinks(c, sourceIDs)
	if err != nil {
		return Model{}, err
	}
	model := Model{
		InputDigest: string(snapshot.Manifest.InputDigest),
		Profile:     snapshot.Manifest.Profile,
		Coverage:    cloneCoverage(validation.Coverage),
		Rows:        rows,
		Sources:     sources,
		Diagnostics: cloneSortedDiagnostics(validation.Diagnostics),
	}
	if err := validateReportDynamicText(model); err != nil {
		return Model{}, fmt.Errorf("build report: %w", err)
	}
	return model, nil
}

func validateDiagnostics(diagnostics []validator.Diagnostic) error {
	for index, diagnostic := range diagnostics {
		if _, known := knownDiagnosticCodes[diagnostic.Code]; !known || diagnostic.Severity != "error" {
			return fmt.Errorf("%w: diagnostic %d has an open code or severity", ErrModelInvalid, index)
		}
		if !utf8.ValidString(diagnostic.Path) || !utf8.ValidString(diagnostic.EntityID) || !utf8.ValidString(diagnostic.Message) || strings.TrimSpace(diagnostic.Message) == "" || strings.ContainsRune(diagnostic.Path, 0) || filepath.IsAbs(diagnostic.Path) {
			return fmt.Errorf("%w: diagnostic %d has invalid text or path", ErrModelInvalid, index)
		}
	}
	return nil
}

func rowFromRecord(cell validator.MatrixCell, record evidence.Record) Row {
	return Row{
		Cell:         cloneMatrixCell(cell),
		EvidenceID:   record.ID,
		Status:       record.Status,
		Workload:     evidence.Workload{ID: record.Workload.ID, Parameters: cloneIntMap(record.Workload.Parameters)},
		Faults:       append([]evidence.Fault{}, record.Faults...),
		Measurements: cloneMeasurements(record.Measurements),
		Assertions:   append([]evidence.Assertion{}, record.Assertions...),
		Environment:  record.Environment,
		Conclusion:   record.Conclusion,
		Limitations:  append([]string{}, record.Limitations...),
	}
}

func referencedSourceIDs(c *catalog.Catalog, cell validator.MatrixCell) ([]string, error) {
	labID, err := resolveCatalogID(c, catalog.EntityKindLab, cell.LabID)
	if err != nil {
		return nil, err
	}
	lab, found := findLab(c, labID)
	if !found {
		return nil, fmt.Errorf("%w: lab %q is absent from catalog", ErrModelInvalid, labID)
	}
	ids := append([]string{}, lab.Sources...)
	owners := 0
	for _, binding := range lab.CaseBindings {
		if binding.ID != cell.BindingID {
			continue
		}
		if binding.Claim != cell.ClaimID {
			return nil, fmt.Errorf("%w: binding %q claim differs from matrix", ErrModelInvalid, cell.BindingID)
		}
		ownerID, err := resolveCatalogID(c, catalog.EntityKindCase, binding.CaseID)
		if err != nil {
			return nil, err
		}
		owner, exists := findCase(c, ownerID)
		if !exists {
			return nil, fmt.Errorf("%w: case owner %q is absent", ErrModelInvalid, ownerID)
		}
		ids = append(ids, owner.Sources...)
		owners++
	}
	for _, binding := range lab.PrincipleBindings {
		if binding.ID != cell.BindingID {
			continue
		}
		if binding.Claim != cell.ClaimID {
			return nil, fmt.Errorf("%w: binding %q claim differs from matrix", ErrModelInvalid, cell.BindingID)
		}
		ownerID, err := resolveCatalogID(c, catalog.EntityKindPrinciple, binding.PrincipleID)
		if err != nil {
			return nil, err
		}
		owner, exists := findPrinciple(c, ownerID)
		if !exists {
			return nil, fmt.Errorf("%w: principle owner %q is absent", ErrModelInvalid, ownerID)
		}
		ids = append(ids, owner.Sources...)
		owners++
	}
	if owners != 1 {
		return nil, fmt.Errorf("%w: binding %q resolves to %d owners", ErrModelInvalid, cell.BindingID, owners)
	}
	resolved := make([]string, len(ids))
	for index, id := range ids {
		resolved[index], err = resolveCatalogID(c, catalog.EntityKindSource, id)
		if err != nil {
			return nil, err
		}
	}
	return resolved, nil
}

func sourceLinks(c *catalog.Catalog, ids map[string]struct{}) ([]SourceLink, error) {
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	links := make([]SourceLink, 0, len(ordered))
	for _, id := range ordered {
		source, exists := c.Sources[id]
		if !exists || source.ID != id || !utf8.ValidString(source.Title) {
			return nil, fmt.Errorf("%w: source %q is absent or invalid", ErrModelInvalid, id)
		}
		parsed, err := url.Parse(source.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
			return nil, fmt.Errorf("%w: source %q is not a direct HTTPS link", ErrModelInvalid, id)
		}
		links = append(links, SourceLink{ID: id, Title: source.Title, URL: source.URL})
	}
	return links, nil
}

func findLab(c *catalog.Catalog, id string) (catalog.LabManifest, bool) {
	value, exists := c.Labs[id]
	return value, exists && value.ID == id
}

func findCase(c *catalog.Catalog, id string) (catalog.CaseManifest, bool) {
	value, exists := c.Cases[id]
	return value, exists && value.ID == id
}

func findPrinciple(c *catalog.Catalog, id string) (catalog.PrincipleManifest, bool) {
	value, exists := c.Principles[id]
	return value, exists && value.ID == id
}

func validateCatalogAliases(c *catalog.Catalog) error {
	caseIDs := make(map[string]struct{}, len(c.Scope.Cases))
	for _, scopeCase := range c.Scope.Cases {
		caseIDs[scopeCase.ID] = struct{}{}
	}
	canonical := map[catalog.EntityKind]map[string]struct{}{
		catalog.EntityKindCase:      caseIDs,
		catalog.EntityKindPrinciple: catalogMapKeys(c.Principles),
		catalog.EntityKindLab:       catalogMapKeys(c.Labs),
		catalog.EntityKindSource:    catalogMapKeys(c.Sources),
	}
	if err := c.Aliases.ValidateCanonical(canonical); err != nil {
		return fmt.Errorf("%w: catalog aliases: %v", ErrModelInvalid, err)
	}
	return nil
}

func resolveCatalogID(c *catalog.Catalog, kind catalog.EntityKind, id string) (string, error) {
	resolved, err := c.Aliases.Resolve(kind, id)
	if err != nil {
		return "", fmt.Errorf("%w: resolve %s alias %q: %v", ErrModelInvalid, kind, id, err)
	}
	return resolved, nil
}

func catalogMapKeys[T any](source map[string]T) map[string]struct{} {
	keys := make(map[string]struct{}, len(source))
	for key := range source {
		keys[key] = struct{}{}
	}
	return keys
}

func cloneCoverage(source validator.Coverage) validator.Coverage {
	cloned := source
	cloned.MissingCaseIDs = append([]string{}, source.MissingCaseIDs...)
	cloned.UnexpectedCaseIDs = append([]string{}, source.UnexpectedCaseIDs...)
	cloned.Families = append([]validator.FamilyCoverage{}, source.Families...)
	cloned.RequiredPrinciples = append([]string{}, source.RequiredPrinciples...)
	cloned.RequiredScenarioLabs = append([]string{}, source.RequiredScenarioLabs...)
	cloned.RequiredPrimitiveLabs = append([]string{}, source.RequiredPrimitiveLabs...)
	cloned.RequiredAdapters = append([]string{}, source.RequiredAdapters...)
	return cloned
}

func cloneSortedDiagnostics(source []validator.Diagnostic) []validator.Diagnostic {
	cloned := append([]validator.Diagnostic{}, source...)
	sort.Slice(cloned, func(left, right int) bool {
		leftValues := [...]string{cloned[left].Code, cloned[left].Path, cloned[left].EntityID, cloned[left].Message}
		rightValues := [...]string{cloned[right].Code, cloned[right].Path, cloned[right].EntityID, cloned[right].Message}
		for index := range leftValues {
			if leftValues[index] != rightValues[index] {
				return leftValues[index] < rightValues[index]
			}
		}
		return false
	})
	if cloned == nil {
		return []validator.Diagnostic{}
	}
	return cloned
}

func cloneMatrixCell(source validator.MatrixCell) validator.MatrixCell {
	cloned := source
	cloned.Faults = append([]string{}, source.Faults...)
	cloned.AssertionIDs = append([]string{}, source.AssertionIDs...)
	return cloned
}

func cloneIntMap(source map[string]int64) map[string]int64 {
	if source == nil {
		return nil
	}
	cloned := make(map[string]int64, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneMeasurements(source map[string]evidence.Measurement) map[string]evidence.Measurement {
	if source == nil {
		return nil
	}
	cloned := make(map[string]evidence.Measurement, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
