package release

import (
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
)

func validateManifest(manifest Manifest) error {
	invalid := func(format string, values ...any) error {
		return fmt.Errorf("%w: %s", ErrManifestInvalid, fmt.Sprintf(format, values...))
	}
	if manifest.SchemaVersion != SchemaVersion {
		return invalid("schema_version=%d; want %d", manifest.SchemaVersion, SchemaVersion)
	}
	if _, err := inputdigest.Parse(string(manifest.InputDigest)); err != nil {
		return invalid("input_digest: %v", err)
	}
	if !validProfile(manifest.Profile) {
		return invalid("profile %q is not closed", manifest.Profile)
	}
	if err := evidence.ValidateRunSetID(manifest.RunSetID); err != nil {
		return invalid("run_set_id is invalid")
	}
	if manifest.Selections == nil || len(manifest.Selections) == 0 {
		return invalid("selections must be explicit and nonempty")
	}
	seenKeys := make(map[evidence.CellKey]struct{}, len(manifest.Selections))
	seenEvidence := make(map[string]struct{}, len(manifest.Selections))
	for index, selection := range manifest.Selections {
		if err := validateSelection(selection); err != nil {
			return invalid("selection %d: %v", index, err)
		}
		key := selectionCellKey(selection)
		if _, exists := seenKeys[key]; exists {
			return invalid("selection %d repeats cell %#v", index, key)
		}
		seenKeys[key] = struct{}{}
		if _, exists := seenEvidence[selection.EvidenceID]; exists {
			return invalid("selection %d reuses evidence ID", index)
		}
		seenEvidence[selection.EvidenceID] = struct{}{}
		if index > 0 && compareCellRole(selectionCellKey(manifest.Selections[index-1]), manifest.Selections[index-1].Role, key, selection.Role) >= 0 {
			return invalid("selections are not in canonical matrix order")
		}
	}
	return nil
}

func validateSelection(selection Selection) error {
	identities := []struct {
		name     string
		value    string
		required bool
	}{
		{name: "lab_id", value: selection.LabID, required: true},
		{name: "required_run_id", value: selection.RequiredRunID, required: true},
		{name: "binding_id", value: selection.BindingID, required: true},
		{name: "claim_id", value: selection.ClaimID, required: true},
		{name: "implementation_id", value: selection.ImplementationID, required: true},
		{name: "adapter_id", value: selection.AdapterID, required: false},
	}
	for _, identity := range identities {
		if identity.value == "" && !identity.required {
			continue
		}
		if evidence.ValidateStableID(identity.value) != nil {
			return fmt.Errorf("%s is invalid", identity.name)
		}
	}
	if !validRole(selection.Role, selection.ImplementationID, selection.AdapterID) {
		return fmt.Errorf("role/adapter identity is invalid")
	}
	if err := evidence.ValidateID(selection.EvidenceID); err != nil {
		return fmt.Errorf("evidence_id is invalid")
	}
	if _, err := inputdigest.Parse(selection.ContentDigest); err != nil {
		return fmt.Errorf("content_digest is invalid")
	}
	return nil
}

func validateExpectedCells(expected []ExpectedCell) error {
	if expected == nil || len(expected) == 0 {
		return fmt.Errorf("expected cells must be explicit and nonempty")
	}
	seen := make(map[evidence.CellKey]struct{}, len(expected))
	profile := expected[0].Profile
	for index := range expected {
		if err := validateExpectedCell(expected[index]); err != nil {
			return fmt.Errorf("expected cell %d: %w", index, err)
		}
		if expected[index].Profile != profile {
			return fmt.Errorf("expected cell %d has mixed profile", index)
		}
		key := matrixCellKey(expected[index].Cell)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("expected cell %d repeats six-field identity", index)
		}
		seen[key] = struct{}{}
		if index > 0 {
			previous := expected[index-1]
			if compareCellRole(matrixCellKey(previous.Cell), evidence.Role(previous.Cell.Role), key, evidence.Role(expected[index].Cell.Role)) >= 0 {
				return fmt.Errorf("expected cells are not in canonical matrix order")
			}
		}
	}
	return nil
}

func validateExpectedCell(expected ExpectedCell) error {
	selection := Selection{
		Role:             evidence.Role(expected.Cell.Role),
		LabID:            expected.Cell.LabID,
		RequiredRunID:    expected.Cell.RequiredRunID,
		BindingID:        expected.Cell.BindingID,
		ClaimID:          expected.Cell.ClaimID,
		ImplementationID: expected.Cell.ImplementationID,
		AdapterID:        expected.Cell.AdapterID,
		EvidenceID:       testShapeEvidenceID,
		ContentDigest:    testShapeContentDigest,
	}
	if err := validateSelection(selection); err != nil {
		return err
	}
	if !validProfile(expected.Profile) {
		return fmt.Errorf("profile is not closed")
	}
	if expected.Start.IsZero() || expected.Start.Location() != time.UTC || expected.Start != expected.Start.Round(0) {
		return fmt.Errorf("start must be canonical UTC")
	}
	if expected.Deadline <= 0 {
		return fmt.Errorf("deadline must be positive")
	}
	if !utf8.ValidString(expected.Hypothesis) || strings.TrimSpace(expected.Hypothesis) == "" {
		return fmt.Errorf("hypothesis must be nonblank UTF-8")
	}
	if err := ValidateDynamicText(expected.Hypothesis); err != nil {
		return fmt.Errorf("hypothesis: %w", err)
	}
	if expected.Workload.Parameters == nil || expected.Faults == nil || expected.Cell.Faults == nil || expected.Cell.AssertionIDs == nil || expected.Limitations == nil || expected.Measurements == nil {
		return fmt.Errorf("maps and slices must be explicit")
	}
	if evidence.ValidateStableID(expected.Workload.ID) != nil || expected.Workload.ID != expected.Cell.Workload {
		return fmt.Errorf("workload identity differs from matrix cell")
	}
	for key := range expected.Workload.Parameters {
		if !utf8.ValidString(key) || strings.TrimSpace(key) == "" {
			return fmt.Errorf("workload parameter key: %w", ErrUnsafeDynamicText)
		}
		if err := ValidateDynamicText(key); err != nil {
			return fmt.Errorf("workload parameter key: %w", err)
		}
	}
	if len(expected.Faults) != len(expected.Cell.Faults) {
		return fmt.Errorf("fault metadata differs from matrix cell")
	}
	seenFaults := make(map[string]struct{}, len(expected.Faults))
	for index, fault := range expected.Faults {
		if evidence.ValidateStableID(fault.ID) != nil || fault.At < 0 || fault.Duration < 0 || fault.ID != expected.Cell.Faults[index] {
			return fmt.Errorf("fault %d is invalid or differs from matrix cell", index)
		}
		if _, exists := seenFaults[fault.ID]; exists {
			return fmt.Errorf("fault %q is duplicated", fault.ID)
		}
		seenFaults[fault.ID] = struct{}{}
	}
	seenAssertions := make(map[string]struct{}, len(expected.Cell.AssertionIDs))
	for _, id := range expected.Cell.AssertionIDs {
		if evidence.ValidateStableID(id) != nil {
			return fmt.Errorf("assertion ID %q is invalid", id)
		}
		if _, exists := seenAssertions[id]; exists {
			return fmt.Errorf("assertion ID %q is duplicated", id)
		}
		seenAssertions[id] = struct{}{}
	}
	seenMeasurements := make(map[string]struct{}, len(expected.Measurements))
	for _, measurement := range expected.Measurements {
		if evidence.ValidateMetricID(measurement.ID) != nil {
			return fmt.Errorf("measurement identity is invalid")
		}
		if err := ValidateDynamicText(measurement.Unit); err != nil {
			return fmt.Errorf("measurement unit: %w", err)
		}
		if strings.TrimSpace(measurement.Unit) == "" || measurement.Unit != strings.TrimSpace(measurement.Unit) {
			return fmt.Errorf("measurement unit: %w", ErrUnsafeDynamicText)
		}
		if _, exists := seenMeasurements[measurement.ID]; exists {
			return fmt.Errorf("measurement %q is duplicated", measurement.ID)
		}
		seenMeasurements[measurement.ID] = struct{}{}
	}
	seenLimitations := make(map[string]struct{}, len(expected.Limitations))
	for _, limitation := range expected.Limitations {
		if !utf8.ValidString(limitation) || strings.TrimSpace(limitation) == "" {
			return fmt.Errorf("limitation is blank")
		}
		if err := ValidateDynamicText(limitation); err != nil {
			return fmt.Errorf("limitation: %w", err)
		}
		if _, exists := seenLimitations[limitation]; exists {
			return fmt.Errorf("limitation is duplicated")
		}
		seenLimitations[limitation] = struct{}{}
	}
	return nil
}

const testShapeEvidenceID = "run-20000101T000000.000Z-00000000000000000000000000000000"
const testShapeContentDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

func recordMatchesExpected(record evidence.Record, expected ExpectedCell) error {
	if _, err := evidence.Encode(record); err != nil {
		return fmt.Errorf("record is not a strict sealed value: %w", err)
	}
	if err := validateRecordDynamicText(record); err != nil {
		return err
	}
	if record.CellKey() != matrixCellKey(expected.Cell) {
		return fmt.Errorf("six-field identity differs")
	}
	if record.Role != evidence.Role(expected.Cell.Role) {
		return fmt.Errorf("role differs")
	}
	if record.Profile != expected.Profile {
		return fmt.Errorf("profile differs")
	}
	if !record.StartedAt.Equal(expected.Start) {
		return fmt.Errorf("logical start differs")
	}
	if record.Workload.ID != expected.Workload.ID || !reflect.DeepEqual(record.Workload.Parameters, expected.Workload.Parameters) {
		return fmt.Errorf("workload differs")
	}
	if !reflect.DeepEqual(record.Faults, expected.Faults) {
		return fmt.Errorf("ordered faults differ")
	}
	if record.Seed != expected.Seed || record.Deadline != expected.Deadline || record.Hypothesis != expected.Hypothesis || !reflect.DeepEqual(record.Limitations, expected.Limitations) {
		return fmt.Errorf("definition metadata differs")
	}
	if record.EventsExecuted != expected.EventsExpected {
		return fmt.Errorf("events_executed=%d; want %d", record.EventsExecuted, expected.EventsExpected)
	}
	if record.Status != evidence.StatusPassed {
		return fmt.Errorf("status=%q; want passed", record.Status)
	}
	if len(record.Measurements) != len(expected.Measurements) {
		return fmt.Errorf("measurement set size differs")
	}
	for _, measurement := range expected.Measurements {
		actual, exists := record.Measurements[measurement.ID]
		if !exists || actual.Unit != measurement.Unit {
			return fmt.Errorf("measurement %q is missing or has a different unit", measurement.ID)
		}
	}
	if len(record.Assertions) != len(expected.Cell.AssertionIDs) {
		return fmt.Errorf("assertion set size differs")
	}
	for index, assertion := range record.Assertions {
		if assertion.ID != expected.Cell.AssertionIDs[index] || !assertion.Passed {
			return fmt.Errorf("assertion %d differs or failed", index)
		}
	}
	return nil
}

func validateRecordDynamicText(record evidence.Record) error {
	if err := ValidateDynamicText(record.Hypothesis); err != nil {
		return fmt.Errorf("hypothesis: %w", err)
	}
	for key := range record.Workload.Parameters {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("workload parameter key: %w", ErrUnsafeDynamicText)
		}
		if err := ValidateDynamicText(key); err != nil {
			return fmt.Errorf("workload parameter key: %w", err)
		}
	}
	for _, measurement := range record.Measurements {
		if err := ValidateDynamicText(measurement.Unit); err != nil {
			return fmt.Errorf("measurement unit: %w", err)
		}
	}
	for _, assertion := range record.Assertions {
		if err := ValidateDynamicText(assertion.Message); err != nil {
			return fmt.Errorf("assertion message: %w", err)
		}
	}
	if err := ValidateDynamicText(record.Conclusion); err != nil {
		return fmt.Errorf("conclusion: %w", err)
	}
	for _, limitation := range record.Limitations {
		if err := ValidateDynamicText(limitation); err != nil {
			return fmt.Errorf("limitation: %w", err)
		}
	}
	for _, diagnostic := range record.Diagnostics {
		if err := ValidateDynamicText(diagnostic.Message); err != nil {
			return fmt.Errorf("diagnostic message: %w", err)
		}
	}
	if err := ValidateDynamicText(record.Environment.GoVersion); err != nil {
		return fmt.Errorf("environment Go version: %w", err)
	}
	return nil
}

func validProfile(profile evidence.Profile) bool {
	return profile == evidence.ProfileSmoke || profile == evidence.ProfileDeep
}

func validRole(role evidence.Role, implementationID, adapterID string) bool {
	switch role {
	case evidence.RoleBaseline, evidence.RoleVariant:
		return adapterID == ""
	case evidence.RoleAdapter:
		return adapterID != "" && adapterID == implementationID
	default:
		return false
	}
}

func compareCellRole(left evidence.CellKey, leftRole evidence.Role, right evidence.CellKey, rightRole evidence.Role) int {
	leftValues := [...]string{left.LabID, left.RequiredRunID, left.BindingID, left.ClaimID, left.ImplementationID, left.AdapterID, string(leftRole)}
	rightValues := [...]string{right.LabID, right.RequiredRunID, right.BindingID, right.ClaimID, right.ImplementationID, right.AdapterID, string(rightRole)}
	for index := range leftValues {
		if leftValues[index] < rightValues[index] {
			return -1
		}
		if leftValues[index] > rightValues[index] {
			return 1
		}
	}
	return 0
}
