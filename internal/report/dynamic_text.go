package report

import (
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/release"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

var (
	reportPlatformIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	reportGoVersionPattern  = regexp.MustCompile(`^(?:go[0-9][0-9A-Za-z ._:+-]*|devel go[0-9A-Za-z][0-9A-Za-z ._:+-]*)$`)
)

func validateReportDynamicText(model Model) error {
	if _, err := inputdigest.Parse(model.InputDigest); err != nil {
		return invalidRenderedModel("input digest is invalid")
	}
	if !validRenderedProfile(model.Profile) {
		return invalidRenderedModel("profile is invalid")
	}
	if err := validateRenderedCoverage(model.Coverage); err != nil {
		return err
	}
	if model.Rows == nil || model.Sources == nil || model.Diagnostics == nil {
		return invalidRenderedModel("collections must be explicit")
	}
	seenEvidence := make(map[string]struct{}, len(model.Rows))
	for index, row := range model.Rows {
		if evidence.ValidateID(row.EvidenceID) != nil {
			return invalidRenderedModel("row %d evidence ID is invalid", index)
		}
		if _, exists := seenEvidence[row.EvidenceID]; exists {
			return invalidRenderedModel("row %d repeats an evidence ID", index)
		}
		seenEvidence[row.EvidenceID] = struct{}{}
		if err := validateRenderedRow(index, row); err != nil {
			return err
		}
	}
	seenSources := make(map[string]struct{}, len(model.Sources))
	for index, source := range model.Sources {
		if !validStableID(source.ID) {
			return invalidRenderedModel("source %d ID is invalid", index)
		}
		if _, exists := seenSources[source.ID]; exists {
			return invalidRenderedModel("source %d repeats an ID", index)
		}
		seenSources[source.ID] = struct{}{}
		if !validPortableNonblank(source.Title) || !validHTTPSURL(source.URL) {
			return invalidRenderedModel("source %d text or URL is invalid", index)
		}
	}
	for index, diagnostic := range model.Diagnostics {
		if err := validateRenderedDiagnostic(index, diagnostic); err != nil {
			return err
		}
	}
	return nil
}

func validateRenderedCoverage(coverage validator.Coverage) error {
	if coverage.BaselineTotal < 0 || coverage.CompleteTotal < 0 || coverage.CompleteTotal > coverage.BaselineTotal ||
		coverage.MissingCaseIDs == nil || coverage.UnexpectedCaseIDs == nil || coverage.Families == nil ||
		coverage.RequiredPrinciples == nil || coverage.RequiredScenarioLabs == nil || coverage.RequiredPrimitiveLabs == nil || coverage.RequiredAdapters == nil {
		return invalidRenderedModel("coverage structure is invalid")
	}
	for _, values := range [][]string{
		coverage.MissingCaseIDs,
		coverage.UnexpectedCaseIDs,
		coverage.RequiredPrinciples,
		coverage.RequiredScenarioLabs,
		coverage.RequiredPrimitiveLabs,
		coverage.RequiredAdapters,
	} {
		if !validStableIDSet(values) {
			return invalidRenderedModel("coverage IDs are invalid")
		}
	}
	seenFamilies := make(map[string]struct{}, len(coverage.Families))
	for _, family := range coverage.Families {
		if !validStableID(family.ID) || family.Required < 0 || family.Complete < 0 || family.Complete > family.Required {
			return invalidRenderedModel("family coverage is invalid")
		}
		if _, exists := seenFamilies[family.ID]; exists {
			return invalidRenderedModel("family coverage repeats an ID")
		}
		seenFamilies[family.ID] = struct{}{}
	}
	return nil
}

func validateRenderedRow(index int, row Row) error {
	if !validRenderedMatrixCell(row.Cell) || row.Status != evidence.StatusPassed {
		return invalidRenderedModel("row %d cell or status is invalid", index)
	}
	if !validStableID(row.Workload.ID) || row.Workload.ID != row.Cell.Workload || row.Workload.Parameters == nil {
		return invalidRenderedModel("row %d workload is invalid", index)
	}
	for parameter := range row.Workload.Parameters {
		if !validPortableNonblank(parameter) {
			return invalidRenderedModel("row %d workload parameter is invalid", index)
		}
	}
	if row.Faults == nil || len(row.Faults) != len(row.Cell.Faults) {
		return invalidRenderedModel("row %d fault set is invalid", index)
	}
	for faultIndex, fault := range row.Faults {
		if !validStableID(fault.ID) || fault.ID != row.Cell.Faults[faultIndex] || fault.At < 0 || fault.Duration < 0 {
			return invalidRenderedModel("row %d fault %d is invalid", index, faultIndex)
		}
	}
	if row.Measurements == nil {
		return invalidRenderedModel("row %d measurements are not explicit", index)
	}
	measurementIDs := make([]string, 0, len(row.Measurements))
	for id := range row.Measurements {
		measurementIDs = append(measurementIDs, id)
	}
	sort.Strings(measurementIDs)
	for _, id := range measurementIDs {
		if evidence.ValidateMetricID(id) != nil || !validMeasurementUnit(row.Measurements[id].Unit) {
			return invalidRenderedModel("row %d measurement is invalid", index)
		}
	}
	if row.Assertions == nil || len(row.Assertions) != len(row.Cell.AssertionIDs) {
		return invalidRenderedModel("row %d assertion set is invalid", index)
	}
	for assertionIndex, assertion := range row.Assertions {
		if !validStableID(assertion.ID) || assertion.ID != row.Cell.AssertionIDs[assertionIndex] || !assertion.Passed || release.ValidateDynamicText(assertion.Message) != nil {
			return invalidRenderedModel("row %d assertion %d is invalid", index, assertionIndex)
		}
	}
	if !validRenderedEnvironment(row.Environment) || !validPortableNonblank(row.Conclusion) || row.Limitations == nil {
		return invalidRenderedModel("row %d environment or conclusion is invalid", index)
	}
	seenLimitations := make(map[string]struct{}, len(row.Limitations))
	for _, limitation := range row.Limitations {
		if !validPortableNonblank(limitation) {
			return invalidRenderedModel("row %d limitation is invalid", index)
		}
		if _, exists := seenLimitations[limitation]; exists {
			return invalidRenderedModel("row %d repeats a limitation", index)
		}
		seenLimitations[limitation] = struct{}{}
	}
	return nil
}

func validRenderedMatrixCell(cell validator.MatrixCell) bool {
	identities := []string{cell.LabID, cell.RequiredRunID, cell.BindingID, cell.ClaimID, cell.ImplementationID, cell.Workload}
	for _, identity := range identities {
		if !validStableID(identity) {
			return false
		}
	}
	switch evidence.Role(cell.Role) {
	case evidence.RoleBaseline, evidence.RoleVariant:
		if cell.AdapterID != "" {
			return false
		}
	case evidence.RoleAdapter:
		if !validStableID(cell.AdapterID) || cell.AdapterID != cell.ImplementationID {
			return false
		}
	default:
		return false
	}
	return cell.Faults != nil && cell.AssertionIDs != nil && validStableIDSet(cell.Faults) && validStableIDSet(cell.AssertionIDs)
}

func validRenderedEnvironment(environment evidence.Environment) bool {
	return reportGoVersionPattern.MatchString(environment.GoVersion) && len(environment.GoVersion) <= 128 &&
		release.ValidateDynamicText(environment.GoVersion) == nil && reportPlatformIDPattern.MatchString(environment.OS) &&
		reportPlatformIDPattern.MatchString(environment.Arch) && environment.CPU == "unknown" && environment.LogicalCPUs > 0
}

func validateRenderedDiagnostic(index int, diagnostic validator.Diagnostic) error {
	if _, known := knownDiagnosticCodes[diagnostic.Code]; !known || diagnostic.Severity != "error" {
		return invalidRenderedModel("diagnostic %d code or severity is invalid", index)
	}
	if !validRenderedRelativePath(diagnostic.Path) || diagnostic.EntityID != "" && !validStableID(diagnostic.EntityID) || !validPortableNonblank(diagnostic.Message) {
		return invalidRenderedModel("diagnostic %d text is invalid", index)
	}
	return nil
}

func validRenderedRelativePath(value string) bool {
	if release.ValidateDynamicText(value) != nil || filepath.IsAbs(value) {
		return false
	}
	normalized := strings.ReplaceAll(value, `\`, "/")
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return false
		}
	}
	return true
}

func validHTTPSURL(value string) bool {
	if release.ValidateDynamicText(value) != nil || strings.ContainsAny(value, "<>|\\") || strings.IndexFunc(value, func(character rune) bool {
		return unicode.IsSpace(character) || unicode.IsControl(character)
	}) >= 0 {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil && parsed.Opaque == ""
}

func validateDiffDynamicText(model DiffModel) error {
	if _, err := inputdigest.Parse(model.InputDigest); err != nil || !validRenderedProfile(model.Profile) || model.Rows == nil {
		return invalidRenderedModel("diff header is invalid")
	}
	switch model.Status {
	case DiffStatusChanges:
		if len(model.Rows) == 0 {
			return invalidRenderedModel("changed diff has no rows")
		}
	case DiffStatusNoChanges, DiffStatusNoBaseline:
		if len(model.Rows) != 0 {
			return invalidRenderedModel("unchanged diff has rows")
		}
	default:
		return invalidRenderedModel("diff status is invalid")
	}
	seenCells := make(map[diffCellKey]struct{}, len(model.Rows))
	for index, row := range model.Rows {
		if !validRenderedMatrixCell(row.Cell) {
			return invalidRenderedModel("diff row %d cell is invalid", index)
		}
		key := typedDiffCellKey(evidence.CellKey{
			LabID: row.Cell.LabID, RequiredRunID: row.Cell.RequiredRunID, BindingID: row.Cell.BindingID,
			ClaimID: row.Cell.ClaimID, ImplementationID: row.Cell.ImplementationID, AdapterID: row.Cell.AdapterID,
		})
		if _, exists := seenCells[key]; exists {
			return invalidRenderedModel("diff row %d repeats a cell", index)
		}
		seenCells[key] = struct{}{}
		if err := validateRenderedDiffRow(index, row); err != nil {
			return err
		}
	}
	return nil
}

func validateRenderedDiffRow(index int, row DiffRow) error {
	if row.MetricChanges == nil || row.EnvironmentChanges == nil || row.AssertionMessageChanges == nil {
		return invalidRenderedModel("diff row %d collections are not explicit", index)
	}
	seenMetrics := make(map[string]struct{}, len(row.MetricChanges))
	for _, change := range row.MetricChanges {
		if evidence.ValidateMetricID(change.ID) != nil {
			return invalidRenderedModel("diff row %d metric ID is invalid", index)
		}
		if _, exists := seenMetrics[change.ID]; exists {
			return invalidRenderedModel("diff row %d repeats a metric", index)
		}
		seenMetrics[change.ID] = struct{}{}
		if !validRenderedMetricChange(change) {
			return invalidRenderedModel("diff row %d metric change is invalid", index)
		}
	}
	seenEnvironment := make(map[string]struct{}, len(row.EnvironmentChanges))
	for _, change := range row.EnvironmentChanges {
		if _, exists := seenEnvironment[change.Field]; exists || !validRenderedEnvironmentChange(change) {
			return invalidRenderedModel("diff row %d environment change is invalid", index)
		}
		seenEnvironment[change.Field] = struct{}{}
	}
	seenAssertions := make(map[string]struct{}, len(row.AssertionMessageChanges))
	for _, change := range row.AssertionMessageChanges {
		if !validStableID(change.ID) || change.Left == change.Right || release.ValidateDynamicText(change.Left) != nil || release.ValidateDynamicText(change.Right) != nil {
			return invalidRenderedModel("diff row %d assertion change is invalid", index)
		}
		if _, exists := seenAssertions[change.ID]; exists {
			return invalidRenderedModel("diff row %d repeats an assertion", index)
		}
		seenAssertions[change.ID] = struct{}{}
	}
	if row.Conclusion != nil && (!validPortableNonblank(row.Conclusion.Left) || !validPortableNonblank(row.Conclusion.Right) || row.Conclusion.Left == row.Conclusion.Right) {
		return invalidRenderedModel("diff row %d conclusion change is invalid", index)
	}
	if row.Limitations != nil {
		if row.Limitations.Left == nil || row.Limitations.Right == nil || reflect.DeepEqual(row.Limitations.Left, row.Limitations.Right) {
			return invalidRenderedModel("diff row %d limitation change is invalid", index)
		}
		for _, values := range [][]string{row.Limitations.Left, row.Limitations.Right} {
			for _, limitation := range values {
				if !validPortableNonblank(limitation) {
					return invalidRenderedModel("diff row %d limitation is invalid", index)
				}
			}
		}
	}
	if len(row.MetricChanges) == 0 && len(row.EnvironmentChanges) == 0 && len(row.AssertionMessageChanges) == 0 && row.Conclusion == nil && row.Limitations == nil {
		return invalidRenderedModel("diff row %d has no changes", index)
	}
	return nil
}

func validRenderedMetricChange(change MetricChange) bool {
	if change.Left == nil && change.Right == nil {
		return false
	}
	if change.Left != nil && !validMeasurementUnit(change.Left.Unit) || change.Right != nil && !validMeasurementUnit(change.Right.Unit) {
		return false
	}
	if change.Left == nil || change.Right == nil {
		return change.Delta == ""
	}
	if change.Left.Unit != change.Right.Unit || *change.Left == *change.Right {
		return false
	}
	return change.Delta == exactIntegerDelta(change.Left.Value, change.Right.Value)
}

func validRenderedEnvironmentChange(change EnvironmentChange) bool {
	if change.Left == change.Right {
		return false
	}
	switch change.Field {
	case "arch", "os":
		return reportPlatformIDPattern.MatchString(change.Left) && reportPlatformIDPattern.MatchString(change.Right)
	case "go_version":
		return reportGoVersionPattern.MatchString(change.Left) && reportGoVersionPattern.MatchString(change.Right) && release.ValidateDynamicText(change.Left) == nil && release.ValidateDynamicText(change.Right) == nil
	case "logical_cpus":
		return validPositiveCanonicalInteger(change.Left) && validPositiveCanonicalInteger(change.Right)
	case "cpu":
		return false
	default:
		return false
	}
}

func validPositiveCanonicalInteger(value string) bool {
	parsed, err := strconv.Atoi(value)
	return err == nil && parsed > 0 && strconv.Itoa(parsed) == value
}

func validMeasurementUnit(value string) bool {
	return strings.TrimSpace(value) == value && validPortableNonblank(value)
}

func validPortableNonblank(value string) bool {
	return strings.TrimSpace(value) != "" && release.ValidateDynamicText(value) == nil
}

func validStableID(value string) bool {
	return evidence.ValidateStableID(value) == nil
}

func validStableIDSet(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validStableID(value) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validRenderedProfile(profile evidence.Profile) bool {
	return profile == evidence.ProfileSmoke || profile == evidence.ProfileDeep
}

func invalidRenderedModel(format string, values ...any) error {
	return fmt.Errorf("%w: %s", ErrModelInvalid, fmt.Sprintf(format, values...))
}
