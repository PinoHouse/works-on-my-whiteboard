package release

import (
	"fmt"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
)

func Build(input inputdigest.Digest, expected []ExpectedCell, current []evidence.Record) (Manifest, error) {
	if _, err := inputdigest.Parse(string(input)); err != nil {
		return Manifest{}, fmt.Errorf("build release manifest: %w", err)
	}
	if err := validateExpectedCells(expected); err != nil {
		return Manifest{}, fmt.Errorf("build release manifest: %w", err)
	}
	if current == nil || len(current) != len(expected) {
		return Manifest{}, fmt.Errorf("build release manifest: current record count=%d; want %d", len(current), len(expected))
	}

	byKey := make(map[evidence.CellKey]evidence.Record, len(current))
	seenIDs := make(map[string]struct{}, len(current))
	var runSetID evidence.RunSetID
	for index, record := range current {
		if _, err := evidence.Encode(record); err != nil {
			return Manifest{}, fmt.Errorf("build release manifest: current record %d: %w", index, err)
		}
		if record.InputDigest != input {
			return Manifest{}, fmt.Errorf("build release manifest: current record %d has a different input digest", index)
		}
		if index == 0 {
			runSetID = record.RunSetID
		} else if record.RunSetID != runSetID {
			return Manifest{}, fmt.Errorf("build release manifest: current records mix run-set IDs")
		}
		if _, exists := seenIDs[record.ID]; exists {
			return Manifest{}, fmt.Errorf("build release manifest: current records repeat evidence ID %q", record.ID)
		}
		seenIDs[record.ID] = struct{}{}
		key := record.CellKey()
		if _, exists := byKey[key]; exists {
			return Manifest{}, fmt.Errorf("build release manifest: current records repeat cell %#v", key)
		}
		byKey[key] = record
	}

	manifest := Manifest{
		SchemaVersion: SchemaVersion,
		InputDigest:   input,
		Profile:       expected[0].Profile,
		RunSetID:      runSetID,
		Selections:    make([]Selection, 0, len(expected)),
	}
	for index, definition := range expected {
		key := matrixCellKey(definition.Cell)
		record, exists := byKey[key]
		if !exists {
			return Manifest{}, fmt.Errorf("build release manifest: expected cell %d is missing", index)
		}
		if err := recordMatchesExpected(record, definition); err != nil {
			return Manifest{}, fmt.Errorf("build release manifest: expected cell %d: %w", index, err)
		}
		delete(byKey, key)
		manifest.Selections = append(manifest.Selections, Selection{
			Role:             record.Role,
			LabID:            record.LabID,
			RequiredRunID:    record.RequiredRunID,
			BindingID:        record.BindingID,
			ClaimID:          record.ClaimID,
			ImplementationID: record.ImplementationID,
			AdapterID:        record.AdapterID,
			EvidenceID:       record.ID,
			ContentDigest:    record.ContentDigest,
		})
	}
	if len(byKey) != 0 {
		return Manifest{}, fmt.Errorf("build release manifest: current records contain extra cells")
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, fmt.Errorf("build release manifest: %w", err)
	}
	return cloneManifest(manifest), nil
}

func cloneManifest(source Manifest) Manifest {
	cloned := source
	if source.Selections == nil {
		cloned.Selections = nil
	} else {
		cloned.Selections = append([]Selection{}, source.Selections...)
	}
	return cloned
}
