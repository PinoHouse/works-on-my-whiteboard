package report

import (
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"strconv"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/release"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

type DiffStatus string

const (
	DiffStatusChanges    DiffStatus = "changes"
	DiffStatusNoChanges  DiffStatus = "no-changes"
	DiffStatusNoBaseline DiffStatus = "no-baseline-for-current-input"
)

var ErrDiffIncompatible = errors.New("incompatible audited snapshots")

type MetricChange struct {
	ID    string                `json:"id"`
	Left  *evidence.Measurement `json:"left"`
	Right *evidence.Measurement `json:"right"`
	Delta string                `json:"delta,omitempty"`
}

type EnvironmentChange struct {
	Field string `json:"field"`
	Left  string `json:"left"`
	Right string `json:"right"`
}

type AssertionMessageChange struct {
	ID    string `json:"id"`
	Left  string `json:"left"`
	Right string `json:"right"`
}

type TextChange struct {
	Left  string `json:"left"`
	Right string `json:"right"`
}

type StringSliceChange struct {
	Left  []string `json:"left"`
	Right []string `json:"right"`
}

type DiffRow struct {
	Cell                    validator.MatrixCell     `json:"cell"`
	MetricChanges           []MetricChange           `json:"metric_changes"`
	EnvironmentChanges      []EnvironmentChange      `json:"environment_changes"`
	AssertionMessageChanges []AssertionMessageChange `json:"assertion_message_changes"`
	Conclusion              *TextChange              `json:"conclusion,omitempty"`
	Limitations             *StringSliceChange       `json:"limitations,omitempty"`
}

type DiffModel struct {
	Status      DiffStatus       `json:"status"`
	InputDigest string           `json:"input_digest"`
	Profile     evidence.Profile `json:"profile"`
	Rows        []DiffRow        `json:"rows"`
}

type diffCellKey struct {
	LabID            string
	RequiredRunID    string
	BindingID        string
	ClaimID          string
	ImplementationID string
	AdapterID        string
}

func typedDiffCellKey(source evidence.CellKey) diffCellKey {
	return diffCellKey{
		LabID:            source.LabID,
		RequiredRunID:    source.RequiredRunID,
		BindingID:        source.BindingID,
		ClaimID:          source.ClaimID,
		ImplementationID: source.ImplementationID,
		AdapterID:        source.AdapterID,
	}
}

func BuildNoBaselineDiff(right release.AuditedSnapshot) (DiffModel, error) {
	if err := release.ValidateAuditedSnapshot(right); err != nil {
		return DiffModel{}, fmt.Errorf("build no-baseline diff: %w", err)
	}
	return validatedDiffModel(DiffModel{
		Status:      DiffStatusNoBaseline,
		InputDigest: string(right.Manifest.InputDigest),
		Profile:     right.Manifest.Profile,
		Rows:        []DiffRow{},
	})
}

func BuildDiff(left, right release.AuditedSnapshot) (DiffModel, error) {
	if err := release.ValidateAuditedSnapshot(left); err != nil {
		return DiffModel{}, fmt.Errorf("build diff left snapshot: %w", err)
	}
	if err := release.ValidateAuditedSnapshot(right); err != nil {
		return DiffModel{}, fmt.Errorf("build diff right snapshot: %w", err)
	}
	if left.Manifest.InputDigest != right.Manifest.InputDigest || left.Manifest.Profile != right.Manifest.Profile || len(left.Records) != len(right.Records) {
		return DiffModel{}, fmt.Errorf("%w: snapshot headers or cell counts differ", ErrDiffIncompatible)
	}

	leftByCell := make(map[diffCellKey]int, len(left.Records))
	for index, record := range left.Records {
		key := typedDiffCellKey(record.CellKey())
		if _, exists := leftByCell[key]; exists {
			return DiffModel{}, fmt.Errorf("%w: left snapshot repeats a six-field cell", ErrDiffIncompatible)
		}
		leftByCell[key] = index
	}

	rows := make([]DiffRow, 0, len(right.Records))
	matched := make(map[diffCellKey]struct{}, len(right.Records))
	for rightIndex, rightRecord := range right.Records {
		key := typedDiffCellKey(rightRecord.CellKey())
		leftIndex, exists := leftByCell[key]
		if !exists {
			return DiffModel{}, fmt.Errorf("%w: right six-field cell has no left match", ErrDiffIncompatible)
		}
		if _, duplicate := matched[key]; duplicate {
			return DiffModel{}, fmt.Errorf("%w: right snapshot repeats a six-field cell", ErrDiffIncompatible)
		}
		matched[key] = struct{}{}
		leftRecord := left.Records[leftIndex]
		if leftRecord.Role != rightRecord.Role {
			return DiffModel{}, fmt.Errorf("%w: role differs for a matched six-field cell", ErrDiffIncompatible)
		}
		if !comparableExpected(left.Expected[leftIndex], right.Expected[rightIndex]) {
			return DiffModel{}, fmt.Errorf("%w: static expected definitions differ", ErrDiffIncompatible)
		}
		row, changed, err := buildDiffRow(right.Expected[rightIndex].Cell, leftRecord, rightRecord)
		if err != nil {
			return DiffModel{}, err
		}
		if changed {
			rows = append(rows, row)
		}
	}
	if len(matched) != len(leftByCell) {
		return DiffModel{}, fmt.Errorf("%w: left snapshot has an unmatched six-field cell", ErrDiffIncompatible)
	}

	status := DiffStatusNoChanges
	if len(rows) != 0 {
		status = DiffStatusChanges
	}
	return validatedDiffModel(DiffModel{
		Status:      status,
		InputDigest: string(right.Manifest.InputDigest),
		Profile:     right.Manifest.Profile,
		Rows:        rows,
	})
}

func validatedDiffModel(model DiffModel) (DiffModel, error) {
	if err := validateDiffDynamicText(model); err != nil {
		return DiffModel{}, fmt.Errorf("build diff: %w", err)
	}
	return model, nil
}

func comparableExpected(left, right release.ExpectedCell) bool {
	return reflect.DeepEqual(left.Cell, right.Cell) &&
		left.Profile == right.Profile &&
		left.Start == right.Start &&
		reflect.DeepEqual(left.Workload, right.Workload) &&
		reflect.DeepEqual(left.Faults, right.Faults) &&
		left.Seed == right.Seed &&
		left.Deadline == right.Deadline &&
		left.Hypothesis == right.Hypothesis &&
		reflect.DeepEqual(left.Limitations, right.Limitations) &&
		reflect.DeepEqual(left.Measurements, right.Measurements) &&
		left.EventsExpected == right.EventsExpected
}

func buildDiffRow(cell validator.MatrixCell, left, right evidence.Record) (DiffRow, bool, error) {
	metricChanges := diffMeasurements(left.Measurements, right.Measurements)
	environmentChanges := diffEnvironment(left.Environment, right.Environment)
	assertionChanges, err := diffAssertionMessages(left.Assertions, right.Assertions)
	if err != nil {
		return DiffRow{}, false, err
	}
	row := DiffRow{
		Cell:                    cloneMatrixCell(cell),
		MetricChanges:           metricChanges,
		EnvironmentChanges:      environmentChanges,
		AssertionMessageChanges: assertionChanges,
	}
	if left.Conclusion != right.Conclusion {
		row.Conclusion = &TextChange{Left: left.Conclusion, Right: right.Conclusion}
	}
	if !reflect.DeepEqual(left.Limitations, right.Limitations) {
		row.Limitations = &StringSliceChange{Left: append([]string{}, left.Limitations...), Right: append([]string{}, right.Limitations...)}
	}
	changed := len(row.MetricChanges) != 0 || len(row.EnvironmentChanges) != 0 || len(row.AssertionMessageChanges) != 0 || row.Conclusion != nil || row.Limitations != nil
	return row, changed, nil
}

func diffMeasurements(left, right map[string]evidence.Measurement) []MetricChange {
	ids := make(map[string]struct{}, len(left)+len(right))
	for id := range left {
		ids[id] = struct{}{}
	}
	for id := range right {
		ids[id] = struct{}{}
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	changes := make([]MetricChange, 0, len(ordered))
	for _, id := range ordered {
		leftValue, leftExists := left[id]
		rightValue, rightExists := right[id]
		if leftExists && rightExists && leftValue == rightValue {
			continue
		}
		change := MetricChange{ID: id}
		if leftExists {
			value := leftValue
			change.Left = &value
		}
		if rightExists {
			value := rightValue
			change.Right = &value
		}
		if leftExists && rightExists {
			change.Delta = exactIntegerDelta(leftValue.Value, rightValue.Value)
		}
		changes = append(changes, change)
	}
	return changes
}

func exactIntegerDelta(left, right int64) string {
	leftValue := big.NewInt(left)
	rightValue := big.NewInt(right)
	return new(big.Int).Sub(rightValue, leftValue).String()
}

func diffEnvironment(left, right evidence.Environment) []EnvironmentChange {
	leftValues := map[string]string{
		"arch":         left.Arch,
		"cpu":          left.CPU,
		"go_version":   left.GoVersion,
		"logical_cpus": strconv.Itoa(left.LogicalCPUs),
		"os":           left.OS,
	}
	rightValues := map[string]string{
		"arch":         right.Arch,
		"cpu":          right.CPU,
		"go_version":   right.GoVersion,
		"logical_cpus": strconv.Itoa(right.LogicalCPUs),
		"os":           right.OS,
	}
	fields := []string{"arch", "cpu", "go_version", "logical_cpus", "os"}
	changes := make([]EnvironmentChange, 0, len(fields))
	for _, field := range fields {
		if leftValues[field] != rightValues[field] {
			changes = append(changes, EnvironmentChange{Field: field, Left: leftValues[field], Right: rightValues[field]})
		}
	}
	return changes
}

func diffAssertionMessages(left, right []evidence.Assertion) ([]AssertionMessageChange, error) {
	if len(left) != len(right) {
		return nil, fmt.Errorf("%w: assertion sets differ", ErrDiffIncompatible)
	}
	changes := make([]AssertionMessageChange, 0, len(left))
	for index := range left {
		if left[index].ID != right[index].ID || !left[index].Passed || !right[index].Passed {
			return nil, fmt.Errorf("%w: assertion identity/order/pass state differs", ErrDiffIncompatible)
		}
		if left[index].Message != right[index].Message {
			changes = append(changes, AssertionMessageChange{ID: left[index].ID, Left: left[index].Message, Right: right[index].Message})
		}
	}
	return changes, nil
}
