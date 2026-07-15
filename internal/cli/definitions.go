package cli

import (
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/experiments"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/release"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
	"github.com/PinoHouse/works-on-my-whiteboard/labs/harness"
)

const (
	codeExperimentRegistryMissing    = "experiment_registry_missing"
	codeExperimentDefinitionMismatch = "experiment_definition_mismatch"
)

type definitionIssue struct {
	Cell    validator.MatrixCell
	Code    string
	Message string
}

type resolvedDefinition struct {
	Expected         release.ExpectedCell
	Definition       experiments.Definition
	role             evidence.Role
	frozenExpected   release.ExpectedCell
	frozenExecutable executableDefinitionSnapshot
}

type executableDefinitionSnapshot struct {
	Events     []executableEventSnapshot
	Assertions []executableAssertionSnapshot
	Conclude   uintptr
}

type executableEventSnapshot struct {
	At       time.Duration
	Phase    harness.Phase
	Sequence uint64
	Name     string
	Apply    uintptr
}

type executableAssertionSnapshot struct {
	ID    string
	Check uintptr
}

type recordIdentity struct {
	ID          string
	RunSetID    evidence.RunSetID
	SourceState inputdigest.State
	Environment evidence.Environment
}

type preparedEvidence struct {
	Status       evidence.Status
	Measurements map[string]evidence.Measurement
	Assertions   []evidence.Assertion
	Diagnostics  []evidence.Diagnostic
	Conclusion   string
}

type preparedRunResult struct {
	preparedEvidence
	frozenEvidence preparedEvidence
	frozenResult   harness.RunResult
}

type experimentLookup func(validator.MatrixCell) (experiments.Factory, bool)

func resolveDefinitions(repository *catalog.Catalog, matrix []validator.MatrixCell, profile evidence.Profile, lookup experimentLookup) ([]resolvedDefinition, []definitionIssue) {
	resolved := make([]resolvedDefinition, 0, len(matrix))
	issues := make([]definitionIssue, 0)
	experimentProfile, profileOK := toExperimentProfile(profile)
	for _, sourceCell := range matrix {
		cell := cloneDefinitionCell(sourceCell)
		if repository == nil || repository.Labs == nil {
			issues = append(issues, definitionMismatch(cell))
			continue
		}
		lab, exists := repository.Labs[cell.LabID]
		if !exists || !profileOK {
			issues = append(issues, definitionMismatch(cell))
			continue
		}
		if lookup == nil {
			issues = append(issues, registryMissing(cell))
			continue
		}
		factory, exists := lookup(cloneDefinitionCell(cell))
		if !exists {
			issues = append(issues, registryMissing(cell))
			continue
		}
		if factory == nil {
			issues = append(issues, definitionMismatch(cell))
			continue
		}
		definition, err := factory(cloneDefinitionCell(cell), experimentProfile)
		if err != nil || !basicDefinitionMatchesCell(definition, cell, experimentProfile, lab) {
			issues = append(issues, definitionMismatch(cell))
			continue
		}
		resolved = append(resolved, freezeDefinition(cell, profile, definition))
	}
	return resolved, issues
}

func toExperimentProfile(profile evidence.Profile) (experiments.Profile, bool) {
	switch profile {
	case evidence.ProfileSmoke:
		return experiments.ProfileSmoke, true
	case evidence.ProfileDeep:
		return experiments.ProfileDeep, true
	default:
		return "", false
	}
}

func toEvidenceRole(role string, implementationID, adapterID string) (evidence.Role, bool) {
	switch evidence.Role(role) {
	case evidence.RoleBaseline:
		return evidence.RoleBaseline, adapterID == ""
	case evidence.RoleVariant:
		return evidence.RoleVariant, adapterID == ""
	case evidence.RoleAdapter:
		return evidence.RoleAdapter, adapterID != "" && adapterID == implementationID
	default:
		return "", false
	}
}

func basicDefinitionMatchesCell(definition experiments.Definition, cell validator.MatrixCell, profile experiments.Profile, lab catalog.LabManifest) bool {
	if definition.Profile != profile || lab.ID != cell.LabID {
		return false
	}
	if _, ok := toEvidenceRole(cell.Role, cell.ImplementationID, cell.AdapterID); !ok {
		return false
	}
	identity := [...]string{
		definition.Spec.LabID,
		definition.Spec.RequiredRunID,
		definition.Spec.BindingID,
		definition.Spec.ClaimID,
		definition.Spec.ImplementationID,
		definition.Spec.AdapterID,
	}
	wantIdentity := [...]string{cell.LabID, cell.RequiredRunID, cell.BindingID, cell.ClaimID, cell.ImplementationID, cell.AdapterID}
	if identity != wantIdentity || !validDefinitionCellIdentity(cell) || definition.Workload.ID != cell.Workload || evidence.ValidateStableID(definition.Workload.ID) != nil {
		return false
	}
	if definition.Spec.Start.IsZero() || definition.Spec.Start.Location() != time.UTC || definition.Spec.Start != definition.Spec.Start.Round(0) || definition.Spec.Deadline <= 0 {
		return false
	}
	if definition.Spec.Parameters == nil || definition.Workload.Parameters == nil || !reflect.DeepEqual(definition.Spec.Parameters, definition.Workload.Parameters) || mapsAlias(definition.Spec.Parameters, definition.Workload.Parameters) {
		return false
	}
	for key := range definition.Spec.Parameters {
		if !validPortableDefinitionText(key) {
			return false
		}
	}
	if definition.Spec.Events == nil {
		return false
	}
	for _, event := range definition.Spec.Events {
		if event.Apply == nil {
			return false
		}
	}
	if !validDefinitionFaults(definition.Faults, cell.Faults) || !validDefinitionAssertions(definition.Spec.Assertions, cell.AssertionIDs) {
		return false
	}
	if !validPortableDefinitionText(definition.Hypothesis) || definition.Conclude == nil {
		return false
	}
	if !validDefinitionLimitations(definition.Limitations) {
		return false
	}
	return validDefinitionMeasurements(definition.Measurements, lab.Metrics)
}

func validDefinitionCellIdentity(cell validator.MatrixCell) bool {
	identities := []struct {
		value    string
		required bool
	}{
		{value: cell.LabID, required: true},
		{value: cell.RequiredRunID, required: true},
		{value: cell.BindingID, required: true},
		{value: cell.ClaimID, required: true},
		{value: cell.ImplementationID, required: true},
		{value: cell.AdapterID, required: false},
		{value: cell.Workload, required: true},
	}
	for _, identity := range identities {
		if identity.value == "" && !identity.required {
			continue
		}
		if evidence.ValidateStableID(identity.value) != nil {
			return false
		}
	}
	return true
}

func mapsAlias(left, right map[string]int64) bool {
	return left != nil && right != nil && reflect.ValueOf(left).Pointer() == reflect.ValueOf(right).Pointer()
}

func validDefinitionFaults(faults []experiments.Fault, expectedIDs []string) bool {
	if faults == nil || expectedIDs == nil || len(faults) != len(expectedIDs) {
		return false
	}
	seen := make(map[string]struct{}, len(faults))
	for index, fault := range faults {
		if fault.ID != expectedIDs[index] || evidence.ValidateStableID(fault.ID) != nil || fault.At < 0 || fault.Duration < 0 {
			return false
		}
		if _, exists := seen[fault.ID]; exists {
			return false
		}
		seen[fault.ID] = struct{}{}
	}
	return true
}

func validDefinitionAssertions(assertions []harness.Assertion, expectedIDs []string) bool {
	if assertions == nil || expectedIDs == nil || len(assertions) != len(expectedIDs) {
		return false
	}
	seen := make(map[string]struct{}, len(assertions))
	for index, assertion := range assertions {
		if assertion.ID != expectedIDs[index] || evidence.ValidateStableID(assertion.ID) != nil || assertion.Check == nil {
			return false
		}
		if _, exists := seen[assertion.ID]; exists {
			return false
		}
		seen[assertion.ID] = struct{}{}
	}
	return true
}

func validDefinitionLimitations(limitations []string) bool {
	if limitations == nil {
		return false
	}
	seen := make(map[string]struct{}, len(limitations))
	for _, limitation := range limitations {
		if !validPortableDefinitionText(limitation) {
			return false
		}
		if _, exists := seen[limitation]; exists {
			return false
		}
		seen[limitation] = struct{}{}
	}
	return true
}

func validDefinitionMeasurements(measurements []experiments.MetricSpec, manifestIDs []string) bool {
	if measurements == nil || manifestIDs == nil || len(measurements) != len(manifestIDs) {
		return false
	}
	definitionSet := make(map[string]struct{}, len(measurements))
	for _, measurement := range measurements {
		if evidence.ValidateMetricID(measurement.ID) != nil || measurement.Unit != strings.TrimSpace(measurement.Unit) || !validPortableDefinitionText(measurement.Unit) {
			return false
		}
		if _, exists := definitionSet[measurement.ID]; exists {
			return false
		}
		definitionSet[measurement.ID] = struct{}{}
	}
	manifestSet := make(map[string]struct{}, len(manifestIDs))
	for _, id := range manifestIDs {
		if evidence.ValidateMetricID(id) != nil {
			return false
		}
		if _, exists := manifestSet[id]; exists {
			return false
		}
		manifestSet[id] = struct{}{}
	}
	return reflect.DeepEqual(definitionSet, manifestSet)
}

func validPortableDefinitionText(value string) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && release.ValidateDynamicText(value) == nil
}

func freezeDefinition(cell validator.MatrixCell, profile evidence.Profile, source experiments.Definition) resolvedDefinition {
	definition := cloneExperimentDefinition(source)
	role, _ := toEvidenceRole(cell.Role, cell.ImplementationID, cell.AdapterID)
	faults := make([]evidence.Fault, len(source.Faults))
	for index, fault := range source.Faults {
		faults[index] = evidence.Fault{ID: fault.ID, At: fault.At, Duration: fault.Duration}
	}
	measurements := make([]evidence.MeasurementSpec, len(source.Measurements))
	for index, measurement := range source.Measurements {
		measurements[index] = evidence.MeasurementSpec{ID: measurement.ID, Unit: measurement.Unit}
	}
	expected := release.ExpectedCell{
		Cell:           cloneDefinitionCell(cell),
		Profile:        profile,
		Start:          source.Spec.Start,
		Workload:       evidence.Workload{ID: source.Workload.ID, Parameters: cloneDefinitionParameters(source.Workload.Parameters)},
		Faults:         faults,
		Seed:           source.Spec.Seed,
		Deadline:       source.Spec.Deadline,
		Hypothesis:     source.Hypothesis,
		Limitations:    append([]string{}, source.Limitations...),
		Measurements:   measurements,
		EventsExpected: uint64(len(source.Spec.Events)),
	}
	return resolvedDefinition{
		Expected:         expected,
		Definition:       definition,
		role:             role,
		frozenExpected:   cloneExpectedCell(expected),
		frozenExecutable: snapshotExecutableDefinition(definition),
	}
}

func snapshotExecutableDefinition(definition experiments.Definition) executableDefinitionSnapshot {
	snapshot := executableDefinitionSnapshot{
		Events:     make([]executableEventSnapshot, len(definition.Spec.Events)),
		Assertions: make([]executableAssertionSnapshot, len(definition.Spec.Assertions)),
		Conclude:   definitionFunctionIdentity(definition.Conclude),
	}
	for index, event := range definition.Spec.Events {
		snapshot.Events[index] = executableEventSnapshot{
			At:       event.At,
			Phase:    event.Phase,
			Sequence: event.Sequence,
			Name:     event.Name,
			Apply:    definitionFunctionIdentity(event.Apply),
		}
	}
	for index, assertion := range definition.Spec.Assertions {
		snapshot.Assertions[index] = executableAssertionSnapshot{
			ID:    assertion.ID,
			Check: definitionFunctionIdentity(assertion.Check),
		}
	}
	return snapshot
}

func executableDefinitionMatchesSnapshot(definition experiments.Definition, snapshot executableDefinitionSnapshot) bool {
	if len(definition.Spec.Events) != len(snapshot.Events) || len(definition.Spec.Assertions) != len(snapshot.Assertions) || definitionFunctionIdentity(definition.Conclude) != snapshot.Conclude {
		return false
	}
	for index, event := range definition.Spec.Events {
		actual := executableEventSnapshot{
			At:       event.At,
			Phase:    event.Phase,
			Sequence: event.Sequence,
			Name:     event.Name,
			Apply:    definitionFunctionIdentity(event.Apply),
		}
		if actual != snapshot.Events[index] {
			return false
		}
	}
	for index, assertion := range definition.Spec.Assertions {
		actual := executableAssertionSnapshot{
			ID:    assertion.ID,
			Check: definitionFunctionIdentity(assertion.Check),
		}
		if actual != snapshot.Assertions[index] {
			return false
		}
	}
	return true
}

// Go exposes a function's code entry point but not its captured closure state.
// This identity therefore detects function replacement except same-code closures
// whose captures differ, which is the strongest portable identity available.
func definitionFunctionIdentity(function any) uintptr {
	value := reflect.ValueOf(function)
	if !value.IsValid() || value.Kind() != reflect.Func || value.IsNil() {
		return 0
	}
	return value.Pointer()
}

func cloneExpectedCell(source release.ExpectedCell) release.ExpectedCell {
	cloned := source
	cloned.Cell = cloneDefinitionCell(source.Cell)
	cloned.Workload.Parameters = cloneDefinitionParameters(source.Workload.Parameters)
	cloned.Faults = append([]evidence.Fault{}, source.Faults...)
	cloned.Limitations = append([]string{}, source.Limitations...)
	cloned.Measurements = append([]evidence.MeasurementSpec{}, source.Measurements...)
	return cloned
}

func cloneExperimentDefinition(source experiments.Definition) experiments.Definition {
	cloned := source
	cloned.Spec = cloneExecutableSpec(source.Spec)
	cloned.Workload.Parameters = cloneDefinitionParameters(source.Workload.Parameters)
	cloned.Faults = append([]experiments.Fault{}, source.Faults...)
	cloned.Measurements = append([]experiments.MetricSpec{}, source.Measurements...)
	cloned.Limitations = append([]string{}, source.Limitations...)
	return cloned
}

func cloneExecutableSpec(source harness.RunSpec) harness.RunSpec {
	cloned := source
	cloned.Parameters = cloneDefinitionParameters(source.Parameters)
	cloned.Events = append([]harness.Event{}, source.Events...)
	cloned.Assertions = append([]harness.Assertion{}, source.Assertions...)
	return cloned
}

func cloneDefinitionCell(source validator.MatrixCell) validator.MatrixCell {
	cloned := source
	cloned.Faults = cloneDefinitionStrings(source.Faults)
	cloned.AssertionIDs = cloneDefinitionStrings(source.AssertionIDs)
	return cloned
}

func cloneDefinitionStrings(source []string) []string {
	if source == nil {
		return nil
	}
	return append([]string{}, source...)
}

func cloneDefinitionParameters(source map[string]int64) map[string]int64 {
	if source == nil {
		return nil
	}
	cloned := make(map[string]int64, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func registryMissing(cell validator.MatrixCell) definitionIssue {
	return definitionIssue{Cell: cloneDefinitionCell(cell), Code: codeExperimentRegistryMissing, Message: "experiment registry entry is missing"}
}

func definitionMismatch(cell validator.MatrixCell) definitionIssue {
	return definitionIssue{Cell: cloneDefinitionCell(cell), Code: codeExperimentDefinitionMismatch, Message: "experiment definition does not match the required matrix cell"}
}

func validateRunResult(resolved resolvedDefinition, result harness.RunResult, runnerErr error) error {
	_, err := prepareRunResult(resolved, result, runnerErr)
	return err
}

func convertRunResult(resolved resolvedDefinition, result harness.RunResult, runnerErr error, identity recordIdentity) (evidence.Record, error) {
	prepared, err := prepareRunResult(resolved, result, runnerErr)
	if err != nil {
		return evidence.Record{}, err
	}
	return convertPreparedRunResult(resolved, result, prepared, identity)
}

func convertPreparedRunResult(resolved resolvedDefinition, result harness.RunResult, prepared preparedRunResult, identity recordIdentity) (evidence.Record, error) {
	if err := validateResolvedDefinition(resolved); err != nil {
		return evidence.Record{}, fmt.Errorf("resolved definition changed after result preparation: %w", err)
	}
	if !reflect.DeepEqual(result, prepared.frozenResult) {
		return evidence.Record{}, fmt.Errorf("run result changed after preparation")
	}
	if !reflect.DeepEqual(prepared.preparedEvidence, prepared.frozenEvidence) {
		return evidence.Record{}, fmt.Errorf("prepared run evidence changed after preparation")
	}
	role, ok := toEvidenceRole(resolved.Expected.Cell.Role, resolved.Expected.Cell.ImplementationID, resolved.Expected.Cell.AdapterID)
	if !ok {
		return evidence.Record{}, fmt.Errorf("resolved role is not closed")
	}
	record := evidence.Record{
		SchemaVersion:    evidence.SchemaVersion,
		ID:               identity.ID,
		RunSetID:         identity.RunSetID,
		LabID:            resolved.Expected.Cell.LabID,
		RequiredRunID:    resolved.Expected.Cell.RequiredRunID,
		BindingID:        resolved.Expected.Cell.BindingID,
		ClaimID:          resolved.Expected.Cell.ClaimID,
		Role:             role,
		ImplementationID: resolved.Expected.Cell.ImplementationID,
		AdapterID:        resolved.Expected.Cell.AdapterID,
		Profile:          resolved.Expected.Profile,
		Hypothesis:       resolved.Expected.Hypothesis,
		Workload: evidence.Workload{
			ID:         resolved.Expected.Workload.ID,
			Parameters: cloneDefinitionParameters(resolved.Expected.Workload.Parameters),
		},
		Faults:         append([]evidence.Fault{}, resolved.Expected.Faults...),
		Status:         prepared.Status,
		SourceCommit:   identity.SourceState.SourceCommit,
		InputDigest:    identity.SourceState.InputDigest,
		Environment:    identity.Environment,
		Seed:           resolved.Expected.Seed,
		Deadline:       resolved.Expected.Deadline,
		StartedAt:      result.StartedAt,
		FinishedAt:     result.FinishedAt,
		EventsExecuted: result.EventsExecuted,
		Parameters:     cloneDefinitionParameters(resolved.Definition.Spec.Parameters),
		Measurements:   cloneDefinitionMeasurements(prepared.Measurements),
		Assertions:     append([]evidence.Assertion{}, prepared.Assertions...),
		Diagnostics:    append([]evidence.Diagnostic{}, prepared.Diagnostics...),
		Conclusion:     prepared.Conclusion,
		Limitations:    append([]string{}, resolved.Expected.Limitations...),
	}
	return evidence.Seal(record)
}

func prepareRunResult(resolved resolvedDefinition, result harness.RunResult, runnerErr error) (preparedRunResult, error) {
	prepared, err := projectRunResult(resolved, result, runnerErr)
	if err != nil {
		return preparedRunResult{}, err
	}
	conclusionInput := cloneHarnessRunResult(result)
	frozenConclusionInput := cloneHarnessRunResult(conclusionInput)
	conclusion := resolved.Definition.Conclude(conclusionInput)
	if err := validateResolvedDefinition(resolved); err != nil {
		return preparedRunResult{}, fmt.Errorf("resolved definition changed during conclusion: %w", err)
	}
	if !reflect.DeepEqual(conclusionInput, frozenConclusionInput) {
		return preparedRunResult{}, fmt.Errorf("experiment conclusion mutated its run result input")
	}
	if !utf8.ValidString(conclusion) || strings.TrimSpace(conclusion) == "" {
		return preparedRunResult{}, fmt.Errorf("experiment conclusion is blank or invalid UTF-8")
	}
	if err := release.ValidateDynamicText(conclusion); err != nil {
		return preparedRunResult{}, fmt.Errorf("experiment conclusion is not portable: %w", err)
	}
	prepared.Conclusion = conclusion
	prepared.frozenEvidence = clonePreparedEvidence(prepared.preparedEvidence)
	prepared.frozenResult = cloneHarnessRunResult(result)
	return prepared, nil
}

func projectRunResult(resolved resolvedDefinition, result harness.RunResult, runnerErr error) (preparedRunResult, error) {
	if err := validateResolvedDefinition(resolved); err != nil {
		return preparedRunResult{}, fmt.Errorf("resolved definition changed after closure: %w", err)
	}
	status, err := resultEvidenceStatus(result.Status, runnerErr)
	if err != nil {
		return preparedRunResult{}, err
	}
	if result.StartedAt != resolved.Expected.Start {
		return preparedRunResult{}, fmt.Errorf("run result start differs from the frozen logical start")
	}
	if result.FinishedAt.IsZero() || result.FinishedAt.Location() != time.UTC || result.FinishedAt != result.FinishedAt.Round(0) || result.FinishedAt.Before(result.StartedAt) {
		return preparedRunResult{}, fmt.Errorf("run result finish is not ordered canonical UTC")
	}
	if status == evidence.StatusPassed && result.EventsExecuted != resolved.Expected.EventsExpected {
		return preparedRunResult{}, fmt.Errorf("passed result executed %d events; want %d", result.EventsExecuted, resolved.Expected.EventsExpected)
	}
	if result.EventsExecuted > resolved.Expected.EventsExpected {
		return preparedRunResult{}, fmt.Errorf("run result executed more events than the frozen definition")
	}
	if result.Metrics == nil || len(result.Metrics) != len(resolved.Expected.Measurements) {
		return preparedRunResult{}, fmt.Errorf("run result measurement set size differs from the frozen definition")
	}
	expectedMeasurements := make(map[string]string, len(resolved.Expected.Measurements))
	for _, measurement := range resolved.Expected.Measurements {
		expectedMeasurements[measurement.ID] = measurement.Unit
	}
	measurements := make(map[string]evidence.Measurement, len(result.Metrics))
	for _, metric := range result.Metrics {
		expectedUnit, exists := expectedMeasurements[metric.Name]
		if !exists || metric.Unit != expectedUnit {
			return preparedRunResult{}, fmt.Errorf("run result measurement %q is extra or has a different unit", metric.Name)
		}
		if _, exists := measurements[metric.Name]; exists {
			return preparedRunResult{}, fmt.Errorf("run result measurement %q is duplicated", metric.Name)
		}
		measurements[metric.Name] = evidence.Measurement{Unit: metric.Unit, Value: metric.Value}
	}
	if result.Assertions == nil || len(result.Assertions) != len(resolved.Expected.Cell.AssertionIDs) {
		return preparedRunResult{}, fmt.Errorf("run result assertion set size differs from the frozen definition")
	}
	assertions := make([]evidence.Assertion, len(result.Assertions))
	seenAssertions := make(map[string]struct{}, len(result.Assertions))
	for index, assertion := range result.Assertions {
		if assertion.ID != resolved.Expected.Cell.AssertionIDs[index] {
			return preparedRunResult{}, fmt.Errorf("run result assertion %d differs from the frozen order", index)
		}
		if _, exists := seenAssertions[assertion.ID]; exists {
			return preparedRunResult{}, fmt.Errorf("run result assertion %q is duplicated", assertion.ID)
		}
		seenAssertions[assertion.ID] = struct{}{}
		if status == evidence.StatusPassed && !assertion.Passed {
			return preparedRunResult{}, fmt.Errorf("passed result contains a false assertion")
		}
		if err := release.ValidateDynamicText(assertion.Message); err != nil {
			return preparedRunResult{}, fmt.Errorf("run result assertion message is not portable: %w", err)
		}
		assertions[index] = evidence.Assertion{ID: assertion.ID, Passed: assertion.Passed, Message: assertion.Message}
	}
	if status == evidence.StatusPassed && result.Diagnostics == nil {
		return preparedRunResult{}, fmt.Errorf("passed result diagnostics must be explicit")
	}
	diagnostics, err := convertHarnessDiagnostics(result.Diagnostics, runnerErr, status)
	if err != nil {
		return preparedRunResult{}, err
	}
	return preparedRunResult{
		preparedEvidence: preparedEvidence{
			Status:       status,
			Measurements: measurements,
			Assertions:   assertions,
			Diagnostics:  diagnostics,
		},
	}, nil
}

func resultEvidenceStatus(status harness.RunStatus, runnerErr error) (evidence.Status, error) {
	switch status {
	case harness.StatusPassed:
		if runnerErr != nil {
			return "", fmt.Errorf("passed run result has a runner error")
		}
		return evidence.StatusPassed, nil
	case harness.StatusFailed:
		if runnerErr == nil {
			return "", fmt.Errorf("failed run result has no runner error")
		}
		return evidence.StatusFailed, nil
	default:
		return "", fmt.Errorf("run result status %q is not closed", status)
	}
}

func convertHarnessDiagnostics(source []harness.Diagnostic, runnerErr error, status evidence.Status) ([]evidence.Diagnostic, error) {
	diagnostics := make([]evidence.Diagnostic, 0, len(source)+1)
	for _, diagnostic := range source {
		if diagnostic.Event != "" && evidence.ValidateStableID(diagnostic.Event) != nil {
			return nil, fmt.Errorf("harness diagnostic event is invalid")
		}
		if !utf8.ValidString(diagnostic.Message) || strings.TrimSpace(diagnostic.Message) == "" {
			return nil, fmt.Errorf("harness diagnostic message is blank or invalid UTF-8")
		}
		if err := release.ValidateDynamicText(diagnostic.Message); err != nil {
			return nil, fmt.Errorf("harness diagnostic message is not portable: %w", err)
		}
		diagnostics = append(diagnostics, evidence.Diagnostic{Code: "harness_failure", Event: diagnostic.Event, Message: diagnostic.Message})
	}
	if status == evidence.StatusFailed && len(diagnostics) == 0 {
		message := runnerErr.Error()
		if !utf8.ValidString(message) || strings.TrimSpace(message) == "" {
			message = "runner returned an error without diagnostics"
		}
		if err := release.ValidateDynamicText(message); err != nil {
			return nil, fmt.Errorf("runner diagnostic message is not portable: %w", err)
		}
		diagnostics = append(diagnostics, evidence.Diagnostic{Code: "experiment_execution_failed", Message: message})
	}
	return diagnostics, nil
}

func validateResolvedDefinition(resolved resolvedDefinition) error {
	expected := resolved.Expected
	definition := resolved.Definition
	if !reflect.DeepEqual(expected, resolved.frozenExpected) {
		return fmt.Errorf("expected cell differs from its frozen value")
	}
	if !executableDefinitionMatchesSnapshot(definition, resolved.frozenExecutable) {
		return fmt.Errorf("executable definition differs from its frozen value")
	}
	experimentProfile, profileOK := toExperimentProfile(expected.Profile)
	if !profileOK || definition.Profile != experimentProfile {
		return fmt.Errorf("profile differs")
	}
	role, roleOK := toEvidenceRole(expected.Cell.Role, expected.Cell.ImplementationID, expected.Cell.AdapterID)
	if !roleOK || role != resolved.role || !validDefinitionCellIdentity(expected.Cell) {
		return fmt.Errorf("cell role or identity is invalid")
	}
	identity := [...]string{definition.Spec.LabID, definition.Spec.RequiredRunID, definition.Spec.BindingID, definition.Spec.ClaimID, definition.Spec.ImplementationID, definition.Spec.AdapterID}
	wantIdentity := [...]string{expected.Cell.LabID, expected.Cell.RequiredRunID, expected.Cell.BindingID, expected.Cell.ClaimID, expected.Cell.ImplementationID, expected.Cell.AdapterID}
	if identity != wantIdentity {
		return fmt.Errorf("six-field identity differs")
	}
	if expected.Start.IsZero() || expected.Start.Location() != time.UTC || expected.Start != expected.Start.Round(0) || definition.Spec.Start != expected.Start {
		return fmt.Errorf("logical start differs")
	}
	if expected.Deadline <= 0 || definition.Spec.Deadline != expected.Deadline || definition.Spec.Seed != expected.Seed {
		return fmt.Errorf("seed or deadline differs")
	}
	if expected.Workload.Parameters == nil || definition.Spec.Parameters == nil || definition.Workload.Parameters == nil ||
		definition.Workload.ID != expected.Workload.ID || expected.Workload.ID != expected.Cell.Workload ||
		!reflect.DeepEqual(definition.Spec.Parameters, expected.Workload.Parameters) ||
		!reflect.DeepEqual(definition.Workload.Parameters, expected.Workload.Parameters) ||
		mapsAlias(definition.Spec.Parameters, definition.Workload.Parameters) ||
		mapsAlias(definition.Spec.Parameters, expected.Workload.Parameters) ||
		mapsAlias(definition.Workload.Parameters, expected.Workload.Parameters) {
		return fmt.Errorf("workload or parameters differ")
	}
	for key := range expected.Workload.Parameters {
		if !validPortableDefinitionText(key) {
			return fmt.Errorf("workload parameter key is not portable")
		}
	}
	if !validDefinitionFaults(definition.Faults, expected.Cell.Faults) {
		return fmt.Errorf("definition faults differ")
	}
	wantFaults := make([]evidence.Fault, len(definition.Faults))
	for index, fault := range definition.Faults {
		wantFaults[index] = evidence.Fault{ID: fault.ID, At: fault.At, Duration: fault.Duration}
	}
	if expected.Faults == nil || !reflect.DeepEqual(expected.Faults, wantFaults) {
		return fmt.Errorf("frozen faults differ")
	}
	if !validDefinitionAssertions(definition.Spec.Assertions, expected.Cell.AssertionIDs) {
		return fmt.Errorf("assertion contract differs")
	}
	if definition.Spec.Events == nil || uint64(len(definition.Spec.Events)) != expected.EventsExpected {
		return fmt.Errorf("event count differs")
	}
	for _, event := range definition.Spec.Events {
		if event.Apply == nil {
			return fmt.Errorf("event action is nil")
		}
	}
	if !validPortableDefinitionText(expected.Hypothesis) || definition.Hypothesis != expected.Hypothesis || definition.Conclude == nil {
		return fmt.Errorf("hypothesis or conclusion function differs")
	}
	if !validDefinitionLimitations(expected.Limitations) || !reflect.DeepEqual(definition.Limitations, expected.Limitations) {
		return fmt.Errorf("limitations differ")
	}
	if expected.Measurements == nil || definition.Measurements == nil || len(expected.Measurements) != len(definition.Measurements) {
		return fmt.Errorf("measurement contract differs")
	}
	seenMeasurements := make(map[string]struct{}, len(expected.Measurements))
	for index, measurement := range expected.Measurements {
		actual := definition.Measurements[index]
		if measurement.ID != actual.ID || measurement.Unit != actual.Unit || evidence.ValidateMetricID(measurement.ID) != nil || measurement.Unit != strings.TrimSpace(measurement.Unit) || !validPortableDefinitionText(measurement.Unit) {
			return fmt.Errorf("measurement %d differs", index)
		}
		if _, exists := seenMeasurements[measurement.ID]; exists {
			return fmt.Errorf("measurement %q is duplicated", measurement.ID)
		}
		seenMeasurements[measurement.ID] = struct{}{}
	}
	return nil
}

func cloneDefinitionMeasurements(source map[string]evidence.Measurement) map[string]evidence.Measurement {
	if source == nil {
		return nil
	}
	cloned := make(map[string]evidence.Measurement, len(source))
	for id, measurement := range source {
		cloned[id] = measurement
	}
	return cloned
}

func clonePreparedEvidence(source preparedEvidence) preparedEvidence {
	cloned := source
	cloned.Measurements = cloneDefinitionMeasurements(source.Measurements)
	cloned.Assertions = append([]evidence.Assertion{}, source.Assertions...)
	cloned.Diagnostics = append([]evidence.Diagnostic{}, source.Diagnostics...)
	return cloned
}

func cloneHarnessRunResult(source harness.RunResult) harness.RunResult {
	cloned := source
	if source.Metrics != nil {
		cloned.Metrics = make([]harness.Metric, len(source.Metrics))
		copy(cloned.Metrics, source.Metrics)
	}
	if source.Assertions != nil {
		cloned.Assertions = make([]harness.AssertionResult, len(source.Assertions))
		copy(cloned.Assertions, source.Assertions)
	}
	if source.Diagnostics != nil {
		cloned.Diagnostics = make([]harness.Diagnostic, len(source.Diagnostics))
		copy(cloned.Diagnostics, source.Diagnostics)
	}
	return cloned
}
