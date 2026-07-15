package release

import (
	"errors"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

const SchemaVersion uint32 = 1
const MaxManifestBytes = 1 << 20

var (
	ErrManifestInvalid      = errors.New("invalid release manifest")
	ErrManifestNonCanonical = errors.New("noncanonical release manifest")
	ErrManifestTooLarge     = errors.New("release manifest is too large")
	ErrSnapshotNotFound     = errors.New("release snapshot not found")
	ErrSnapshotExists       = errors.New("release snapshot already exists")
	ErrSnapshotUnsafePath   = errors.New("unsafe release snapshot path")
	ErrSnapshotCorrupt      = errors.New("corrupt release snapshot")
	ErrSnapshotIO           = errors.New("release snapshot I/O failure")
	ErrSnapshotUnbound      = errors.New("release snapshot is not an intact audited value")
)

type Selection struct {
	Role             evidence.Role `yaml:"role" json:"role"`
	LabID            string        `yaml:"lab_id" json:"lab_id"`
	RequiredRunID    string        `yaml:"required_run_id" json:"required_run_id"`
	BindingID        string        `yaml:"binding_id" json:"binding_id"`
	ClaimID          string        `yaml:"claim_id" json:"claim_id"`
	ImplementationID string        `yaml:"implementation_id" json:"implementation_id"`
	AdapterID        string        `yaml:"adapter_id" json:"adapter_id"`
	EvidenceID       string        `yaml:"evidence_id" json:"evidence_id"`
	ContentDigest    string        `yaml:"content_digest" json:"content_digest"`
}

type Manifest struct {
	SchemaVersion uint32             `yaml:"schema_version" json:"schema_version"`
	InputDigest   inputdigest.Digest `yaml:"input_digest" json:"input_digest"`
	Profile       evidence.Profile   `yaml:"profile" json:"profile"`
	RunSetID      evidence.RunSetID  `yaml:"run_set_id" json:"run_set_id"`
	Selections    []Selection        `yaml:"selections" json:"selections"`
}

type ExpectedCell struct {
	Cell           validator.MatrixCell
	Profile        evidence.Profile
	Start          time.Time
	Workload       evidence.Workload
	Faults         []evidence.Fault
	Seed           int64
	Deadline       time.Duration
	Hypothesis     string
	Limitations    []string
	Measurements   []evidence.MeasurementSpec
	EventsExpected uint64
}

type AuditedSnapshot struct {
	Manifest Manifest
	Expected []ExpectedCell
	Records  []evidence.Record

	bindingSeal [32]byte
}

func matrixCellKey(cell validator.MatrixCell) evidence.CellKey {
	return evidence.CellKey{
		LabID:            cell.LabID,
		RequiredRunID:    cell.RequiredRunID,
		BindingID:        cell.BindingID,
		ClaimID:          cell.ClaimID,
		ImplementationID: cell.ImplementationID,
		AdapterID:        cell.AdapterID,
	}
}

func selectionCellKey(selection Selection) evidence.CellKey {
	return evidence.CellKey{
		LabID:            selection.LabID,
		RequiredRunID:    selection.RequiredRunID,
		BindingID:        selection.BindingID,
		ClaimID:          selection.ClaimID,
		ImplementationID: selection.ImplementationID,
		AdapterID:        selection.AdapterID,
	}
}
