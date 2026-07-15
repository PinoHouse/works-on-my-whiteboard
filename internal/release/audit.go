package release

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"sort"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

const (
	CodeReleaseSnapshotMissing        = "release_snapshot_missing"
	CodeReleaseManifestInvalid        = "release_manifest_invalid"
	CodeReleaseInputDigestMismatch    = "release_input_digest_mismatch"
	CodeReleaseProfileMismatch        = "release_profile_mismatch"
	CodeReleaseSelectionOrderMismatch = "release_selection_order_mismatch"
	CodeReleaseCellMissing            = "release_cell_missing"
	CodeReleaseCellDuplicate          = "release_cell_duplicate"
	CodeReleaseCellMismatch           = "release_cell_mismatch"
	CodeReleaseEvidenceMissing        = "release_evidence_missing"
	CodeReleaseEvidenceInvalid        = "release_evidence_invalid"
	CodeReleaseEvidenceReused         = "release_evidence_reused"
	CodeReleaseContentDigestMismatch  = "release_content_digest_mismatch"
	CodeReleaseDefinitionMismatch     = "release_definition_mismatch"
	CodeReleaseStatusNotPassed        = "release_status_not_passed"
	CodeReleaseAssertionMismatch      = "release_assertion_mismatch"
	CodeReleaseRunSetMismatch         = "release_run_set_mismatch"
)

func AuditSnapshot(ctx context.Context, manifest Manifest, expected []ExpectedCell, store *evidence.Store) (AuditedSnapshot, []validator.Diagnostic, error) {
	if ctx == nil {
		return AuditedSnapshot{}, []validator.Diagnostic{}, fmt.Errorf("%w: context is nil", ErrSnapshotIO)
	}
	if err := ctx.Err(); err != nil {
		return AuditedSnapshot{}, []validator.Diagnostic{}, err
	}

	diagnostics := make([]validator.Diagnostic, 0)
	expectedValid := validateExpectedCells(expected) == nil
	if !expectedValid {
		diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseDefinitionMismatch, "", "expected experiment definitions are invalid"))
	}
	if err := validateManifest(manifest); err != nil {
		diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseManifestInvalid, "", "release manifest is structurally invalid"))
	}

	var expectedProfile evidence.Profile
	if len(expected) != 0 {
		expectedProfile = expected[0].Profile
		if manifest.Profile != expectedProfile {
			diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseProfileMismatch, "", fmt.Sprintf("manifest profile %q differs from expected profile %q", manifest.Profile, expectedProfile)))
		}
	}
	if _, err := inputdigest.Parse(string(manifest.InputDigest)); err != nil {
		diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseInputDigestMismatch, "", "manifest input digest is invalid"))
	}

	expectedByKey := make(map[evidence.CellKey]ExpectedCell, len(expected))
	for _, definition := range expected {
		expectedByKey[matrixCellKey(definition.Cell)] = definition
	}
	selectionsByKey := make(map[evidence.CellKey][]Selection, len(manifest.Selections))
	selectedIDs := make(map[string]struct{}, len(manifest.Selections))
	for _, selection := range manifest.Selections {
		key := selectionCellKey(selection)
		selectionsByKey[key] = append(selectionsByKey[key], selection)
		if _, exists := selectedIDs[selection.EvidenceID]; exists {
			diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseEvidenceReused, selection.EvidenceID, "one evidence ID is selected more than once"))
		}
		selectedIDs[selection.EvidenceID] = struct{}{}
		if _, exists := expectedByKey[key]; !exists {
			diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseCellMismatch, selection.LabID, "manifest selection does not match an expected six-field cell"))
		}
	}

	for index, definition := range expected {
		key := matrixCellKey(definition.Cell)
		selections := selectionsByKey[key]
		if len(selections) == 0 {
			diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseCellMissing, definition.Cell.LabID, "expected cell is absent from the manifest"))
			continue
		}
		if len(selections) > 1 {
			diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseCellDuplicate, definition.Cell.LabID, "expected cell is selected more than once"))
		}
		selection := selections[0]
		if selection.Role != evidence.Role(definition.Cell.Role) {
			diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseCellMismatch, definition.Cell.LabID, "selection role differs from the expected role"))
		}
		if index >= len(manifest.Selections) || selectionCellKey(manifest.Selections[index]) != key || manifest.Selections[index].Role != evidence.Role(definition.Cell.Role) {
			diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseSelectionOrderMismatch, definition.Cell.LabID, "selection order differs from deterministic matrix order"))
		}
	}
	if len(manifest.Selections) != len(expected) {
		diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseSelectionOrderMismatch, "", "selection count differs from expected matrix count"))
	}

	loaded := make(map[string]evidence.Record, len(manifest.Selections))
	var operationalErr error
	if store == nil && len(manifest.Selections) != 0 {
		operationalErr = fmt.Errorf("%w: evidence store is nil", ErrSnapshotIO)
	} else {
		for _, selection := range manifest.Selections {
			if err := ctx.Err(); err != nil {
				operationalErr = errors.Join(operationalErr, err)
				break
			}
			if _, exists := loaded[selection.EvidenceID]; exists {
				continue
			}
			record, err := store.Get(ctx, selection.EvidenceID)
			if err != nil {
				diagnostic, operation := classifySelectedEvidenceError(selection.EvidenceID, err)
				if diagnostic != nil {
					diagnostics = append(diagnostics, *diagnostic)
				}
				operationalErr = errors.Join(operationalErr, operation)
				continue
			}
			loaded[selection.EvidenceID] = record
		}
	}

	recordsByKey := make(map[evidence.CellKey]evidence.Record, len(expected))
	for _, selection := range manifest.Selections {
		record, exists := loaded[selection.EvidenceID]
		if !exists {
			continue
		}
		key := selectionCellKey(selection)
		definition, expectedExists := expectedByKey[key]
		if record.ID != selection.EvidenceID {
			diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseEvidenceInvalid, selection.EvidenceID, "selected record ID differs from the manifest"))
		}
		if record.ContentDigest != selection.ContentDigest {
			diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseContentDigestMismatch, selection.EvidenceID, "selected record content digest differs from the manifest"))
		}
		if record.RunSetID != manifest.RunSetID {
			diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseRunSetMismatch, selection.EvidenceID, "selected record run-set ID differs from the manifest"))
		}
		if record.InputDigest != manifest.InputDigest {
			diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseInputDigestMismatch, selection.EvidenceID, "selected record input digest differs from the manifest"))
		}
		if record.Profile != manifest.Profile {
			diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseProfileMismatch, selection.EvidenceID, "selected record profile differs from the manifest"))
		}
		if record.Status != evidence.StatusPassed {
			diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseStatusNotPassed, selection.EvidenceID, "selected record status is not passed"))
		}
		if expectedExists {
			if !assertionsMatch(record, definition) {
				diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseAssertionMismatch, selection.EvidenceID, "selected record assertions differ from the expected ordered passed set"))
			}
			if err := recordMatchesExpected(record, definition); err != nil {
				code := CodeReleaseDefinitionMismatch
				message := "selected record differs from its expected experiment definition"
				if errors.Is(err, ErrUnsafeDynamicText) {
					code = CodeReleaseEvidenceInvalid
					message = "selected record contains non-portable dynamic text"
				}
				diagnostics = append(diagnostics, releaseDiagnostic(code, selection.EvidenceID, message))
			}
		}
		if record.CellKey() != key || record.Role != selection.Role {
			diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseCellMismatch, selection.EvidenceID, "selected record cell or role differs from the manifest"))
		}
		if _, duplicate := recordsByKey[key]; duplicate {
			diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseCellDuplicate, selection.EvidenceID, "multiple selected records bind the same cell"))
		} else {
			recordsByKey[key] = record
		}
	}

	if store != nil && operationalErr == nil {
		records, err := store.List(ctx)
		if err != nil {
			diagnostic, operation := classifyListEvidenceError(err)
			if diagnostic != nil {
				diagnostics = append(diagnostics, *diagnostic)
			}
			operationalErr = errors.Join(operationalErr, operation)
		} else {
			for _, record := range records {
				if record.RunSetID != manifest.RunSetID {
					continue
				}
				if _, selected := selectedIDs[record.ID]; !selected {
					diagnostics = append(diagnostics, releaseDiagnostic(CodeReleaseCellDuplicate, record.ID, "unselected evidence record belongs to the manifest run set"))
				}
			}
		}
	}

	diagnostics = sortReleaseDiagnostics(diagnostics)
	if operationalErr != nil || len(diagnostics) != 0 || !expectedValid {
		return AuditedSnapshot{}, diagnostics, operationalErr
	}

	alignedRecords := make([]evidence.Record, len(expected))
	for index, definition := range expected {
		selection := manifest.Selections[index]
		record, exists := loaded[selection.EvidenceID]
		if !exists || record.CellKey() != matrixCellKey(definition.Cell) {
			return AuditedSnapshot{}, []validator.Diagnostic{releaseDiagnostic(CodeReleaseCellMissing, definition.Cell.LabID, "audited alignment is incomplete")}, nil
		}
		alignedRecords[index] = record
	}
	return bindAuditedSnapshot(manifest, expected, alignedRecords)
}

func ValidateAuditedSnapshot(snapshot AuditedSnapshot) error {
	if snapshot.bindingSeal == ([32]byte{}) {
		return ErrSnapshotUnbound
	}
	if err := validateBoundSnapshot(snapshot.Manifest, snapshot.Expected, snapshot.Records); err != nil {
		return fmt.Errorf("%w: %w", ErrSnapshotUnbound, err)
	}
	want, err := calculateBindingSeal(snapshot.Manifest, snapshot.Expected, snapshot.Records)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSnapshotUnbound, err)
	}
	if subtle.ConstantTimeCompare(snapshot.bindingSeal[:], want[:]) != 1 {
		return fmt.Errorf("%w: binding seal mismatch", ErrSnapshotUnbound)
	}
	return nil
}

func bindAuditedSnapshot(manifest Manifest, expected []ExpectedCell, records []evidence.Record) (AuditedSnapshot, []validator.Diagnostic, error) {
	snapshot := AuditedSnapshot{
		Manifest: cloneManifest(manifest),
		Expected: cloneExpectedCells(expected),
		Records:  cloneEvidenceRecords(records),
	}
	if err := validateBoundSnapshot(snapshot.Manifest, snapshot.Expected, snapshot.Records); err != nil {
		return AuditedSnapshot{}, []validator.Diagnostic{releaseDiagnostic(CodeReleaseDefinitionMismatch, "", "audited snapshot alignment is invalid")}, nil
	}
	seal, err := calculateBindingSeal(snapshot.Manifest, snapshot.Expected, snapshot.Records)
	if err != nil {
		return AuditedSnapshot{}, []validator.Diagnostic{}, err
	}
	snapshot.bindingSeal = seal
	return snapshot, []validator.Diagnostic{}, nil
}

func validateBoundSnapshot(manifest Manifest, expected []ExpectedCell, records []evidence.Record) error {
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if err := validateExpectedCells(expected); err != nil {
		return err
	}
	if len(manifest.Selections) != len(expected) || len(records) != len(expected) {
		return fmt.Errorf("aligned slice lengths differ")
	}
	for index, definition := range expected {
		selection := manifest.Selections[index]
		record := records[index]
		if selectionCellKey(selection) != matrixCellKey(definition.Cell) || selection.Role != evidence.Role(definition.Cell.Role) {
			return fmt.Errorf("selection %d differs from expected cell", index)
		}
		if record.ID != selection.EvidenceID || record.ContentDigest != selection.ContentDigest || record.CellKey() != selectionCellKey(selection) || record.Role != selection.Role {
			return fmt.Errorf("record %d differs from selection", index)
		}
		if record.InputDigest != manifest.InputDigest || record.Profile != manifest.Profile || record.RunSetID != manifest.RunSetID {
			return fmt.Errorf("record %d differs from manifest header", index)
		}
		if err := recordMatchesExpected(record, definition); err != nil {
			return fmt.Errorf("record %d differs from expected: %w", index, err)
		}
	}
	return nil
}

func calculateBindingSeal(manifest Manifest, expected []ExpectedCell, records []evidence.Record) ([32]byte, error) {
	hasher := sha256.New()
	writeBindingPart(hasher, []byte("works-on-my-whiteboard-audited-snapshot\x00v1\x00"))
	manifestBytes, err := Encode(manifest)
	if err != nil {
		return [32]byte{}, err
	}
	writeBindingPart(hasher, manifestBytes)
	expectedBytes, err := json.Marshal(expected)
	if err != nil {
		return [32]byte{}, fmt.Errorf("encode expected definitions: %w", err)
	}
	writeBindingPart(hasher, expectedBytes)
	writeBindingLength(hasher, uint64(len(records)))
	for _, record := range records {
		encoded, encodeErr := evidence.Encode(record)
		if encodeErr != nil {
			return [32]byte{}, encodeErr
		}
		writeBindingPart(hasher, encoded)
	}
	var result [32]byte
	copy(result[:], hasher.Sum(nil))
	return result, nil
}

func writeBindingPart(hasher hash.Hash, value []byte) {
	writeBindingLength(hasher, uint64(len(value)))
	_, _ = hasher.Write(value)
}

func writeBindingLength(hasher hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = hasher.Write(encoded[:])
}

func assertionsMatch(record evidence.Record, expected ExpectedCell) bool {
	if len(record.Assertions) != len(expected.Cell.AssertionIDs) {
		return false
	}
	for index, assertion := range record.Assertions {
		if assertion.ID != expected.Cell.AssertionIDs[index] || !assertion.Passed {
			return false
		}
	}
	return true
}

func classifySelectedEvidenceError(id string, err error) (*validator.Diagnostic, error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	var diagnostic *validator.Diagnostic
	if errors.Is(err, evidence.ErrEvidenceCorrupt) || errors.Is(err, evidence.ErrEvidenceUnsafePath) || errors.Is(err, evidence.ErrEvidenceTooLarge) || errors.Is(err, evidence.ErrEvidenceInvalid) {
		classified := releaseDiagnostic(CodeReleaseEvidenceInvalid, id, "selected evidence record is invalid or unsafe")
		diagnostic = &classified
	} else if errors.Is(err, evidence.ErrEvidenceNotFound) {
		classified := releaseDiagnostic(CodeReleaseEvidenceMissing, id, "selected evidence record is missing")
		diagnostic = &classified
	}
	if errors.Is(err, evidence.ErrEvidenceIO) {
		return diagnostic, fmt.Errorf("%w: load selected evidence: %w", ErrSnapshotIO, err)
	}
	if diagnostic != nil {
		return diagnostic, nil
	}
	return nil, fmt.Errorf("%w: load selected evidence: %w", ErrSnapshotIO, err)
}

func classifyListEvidenceError(err error) (*validator.Diagnostic, error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	var diagnostic *validator.Diagnostic
	if errors.Is(err, evidence.ErrEvidenceCorrupt) || errors.Is(err, evidence.ErrEvidenceUnsafePath) || errors.Is(err, evidence.ErrEvidenceTooLarge) || errors.Is(err, evidence.ErrEvidenceInvalid) {
		classified := releaseDiagnostic(CodeReleaseEvidenceInvalid, "", "evidence history cannot be classified safely")
		diagnostic = &classified
	}
	if errors.Is(err, evidence.ErrEvidenceIO) {
		return diagnostic, fmt.Errorf("%w: list evidence: %w", ErrSnapshotIO, err)
	}
	if diagnostic != nil {
		return diagnostic, nil
	}
	return nil, fmt.Errorf("%w: list evidence: %w", ErrSnapshotIO, err)
}

func releaseDiagnostic(code, entityID, message string) validator.Diagnostic {
	return validator.Diagnostic{Code: code, Severity: "error", EntityID: entityID, Message: message}
}

func sortReleaseDiagnostics(diagnostics []validator.Diagnostic) []validator.Diagnostic {
	if diagnostics == nil {
		diagnostics = []validator.Diagnostic{}
	}
	sort.Slice(diagnostics, func(left, right int) bool {
		return compareDiagnostic(diagnostics[left], diagnostics[right]) < 0
	})
	return diagnostics
}

func compareDiagnostic(left, right validator.Diagnostic) int {
	leftValues := [...]string{left.Code, left.Path, left.EntityID, left.Message}
	rightValues := [...]string{right.Code, right.Path, right.EntityID, right.Message}
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

func cloneExpectedCells(source []ExpectedCell) []ExpectedCell {
	if source == nil {
		return nil
	}
	cloned := make([]ExpectedCell, len(source))
	for index, expected := range source {
		cloned[index] = expected
		cloned[index].Cell.Faults = append([]string{}, expected.Cell.Faults...)
		cloned[index].Cell.AssertionIDs = append([]string{}, expected.Cell.AssertionIDs...)
		cloned[index].Workload.Parameters = cloneInt64Map(expected.Workload.Parameters)
		cloned[index].Faults = append([]evidence.Fault{}, expected.Faults...)
		cloned[index].Limitations = append([]string{}, expected.Limitations...)
		cloned[index].Measurements = append([]evidence.MeasurementSpec{}, expected.Measurements...)
	}
	return cloned
}

func cloneEvidenceRecords(source []evidence.Record) []evidence.Record {
	if source == nil {
		return nil
	}
	cloned := make([]evidence.Record, len(source))
	for index, record := range source {
		cloned[index] = record
		cloned[index].Workload.Parameters = cloneInt64Map(record.Workload.Parameters)
		cloned[index].Faults = append([]evidence.Fault{}, record.Faults...)
		cloned[index].Parameters = cloneInt64Map(record.Parameters)
		cloned[index].Measurements = cloneMeasurementMap(record.Measurements)
		cloned[index].Assertions = append([]evidence.Assertion{}, record.Assertions...)
		cloned[index].Diagnostics = append([]evidence.Diagnostic{}, record.Diagnostics...)
		cloned[index].Limitations = append([]string{}, record.Limitations...)
	}
	return cloned
}

func cloneInt64Map(source map[string]int64) map[string]int64 {
	if source == nil {
		return nil
	}
	cloned := make(map[string]int64, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneMeasurementMap(source map[string]evidence.Measurement) map[string]evidence.Measurement {
	if source == nil {
		return nil
	}
	cloned := make(map[string]evidence.Measurement, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
