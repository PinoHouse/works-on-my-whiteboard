package evidence

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
)

var (
	ErrInvalidRecord = errors.New("invalid evidence record")
	ErrContentDigest = errors.New("invalid evidence content digest")
	ErrNonCanonical  = errors.New("noncanonical evidence record")
	ErrTooLarge      = errors.New("evidence record is too large")
)

const SchemaVersion uint32 = 1
const MaxRecordBytes = 1 << 20

type Profile string

const (
	ProfileSmoke Profile = "smoke"
	ProfileDeep  Profile = "deep"
)

type Role string

const (
	RoleBaseline Role = "baseline"
	RoleVariant  Role = "variant"
	RoleAdapter  Role = "adapter"
)

type Status string

const (
	StatusPassed       Status = "passed"
	StatusFailed       Status = "failed"
	StatusSkipped      Status = "skipped"
	StatusFlaky        Status = "flaky"
	StatusInconclusive Status = "inconclusive"
)

type RunSetID string

type CellKey struct {
	LabID            string
	RequiredRunID    string
	BindingID        string
	ClaimID          string
	ImplementationID string
	AdapterID        string
}

type Workload struct {
	ID         string           `json:"id"`
	Parameters map[string]int64 `json:"parameters"`
}

type Fault struct {
	ID       string        `json:"id"`
	At       time.Duration `json:"at"`
	Duration time.Duration `json:"duration"`
}

type Environment struct {
	GoVersion   string `json:"go_version"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	CPU         string `json:"cpu"`
	LogicalCPUs int    `json:"logical_cpus"`
}

type Measurement struct {
	Unit  string `json:"unit"`
	Value int64  `json:"value"`
}

type MeasurementSpec struct {
	ID   string
	Unit string
}

type Assertion struct {
	ID      string `json:"id"`
	Passed  bool   `json:"passed"`
	Message string `json:"message"`
}

type Diagnostic struct {
	Code    string `json:"code"`
	Event   string `json:"event"`
	Message string `json:"message"`
}

type Record struct {
	SchemaVersion    uint32                 `json:"schema_version"`
	ID               string                 `json:"id"`
	RunSetID         RunSetID               `json:"run_set_id"`
	LabID            string                 `json:"lab_id"`
	RequiredRunID    string                 `json:"required_run_id"`
	BindingID        string                 `json:"binding_id"`
	ClaimID          string                 `json:"claim_id"`
	Role             Role                   `json:"role"`
	ImplementationID string                 `json:"implementation_id"`
	AdapterID        string                 `json:"adapter_id"`
	Profile          Profile                `json:"profile"`
	Hypothesis       string                 `json:"hypothesis"`
	Workload         Workload               `json:"workload"`
	Faults           []Fault                `json:"faults"`
	Status           Status                 `json:"status"`
	SourceCommit     string                 `json:"source_commit"`
	InputDigest      inputdigest.Digest     `json:"input_digest"`
	Environment      Environment            `json:"environment"`
	Seed             int64                  `json:"seed"`
	Deadline         time.Duration          `json:"deadline"`
	StartedAt        time.Time              `json:"started_at"`
	FinishedAt       time.Time              `json:"finished_at"`
	EventsExecuted   uint64                 `json:"events_executed"`
	Parameters       map[string]int64       `json:"parameters"`
	Measurements     map[string]Measurement `json:"measurements"`
	Assertions       []Assertion            `json:"assertions"`
	Diagnostics      []Diagnostic           `json:"diagnostics"`
	Conclusion       string                 `json:"conclusion"`
	Limitations      []string               `json:"limitations"`
	ContentDigest    string                 `json:"content_digest"`
}

func (record Record) CellKey() CellKey {
	return CellKey{
		LabID:            record.LabID,
		RequiredRunID:    record.RequiredRunID,
		BindingID:        record.BindingID,
		ClaimID:          record.ClaimID,
		ImplementationID: record.ImplementationID,
		AdapterID:        record.AdapterID,
	}
}

var (
	stableIDPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	diagnosticPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[_-][a-z0-9]+)*$`)
	metricIDPattern   = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
	commitPattern     = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	attemptIDPattern  = regexp.MustCompile(`^run-[0-9]{8}T[0-9]{6}\.[0-9]{3}Z-[0-9a-f]{32}$`)
	runSetIDPattern   = regexp.MustCompile(`^set-[0-9]{8}T[0-9]{6}\.[0-9]{3}Z-[0-9a-f]{32}$`)
	platformIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	goVersionPattern  = regexp.MustCompile(`^(?:go[0-9][0-9A-Za-z ._:+-]*|devel go[0-9A-Za-z][0-9A-Za-z ._:+-]*)$`)
)

func ValidateStableID(value string) error {
	if !stableIDPattern.MatchString(value) {
		return fmt.Errorf("%w: stable ID %q is invalid", ErrInvalidRecord, value)
	}
	return nil
}

func ValidateMetricID(value string) error {
	if !metricIDPattern.MatchString(value) {
		return fmt.Errorf("%w: metric ID %q is invalid", ErrInvalidRecord, value)
	}
	return nil
}

func Seal(source Record) (Record, error) {
	record := cloneAndNormalize(source)
	record.ContentDigest = ""
	if err := validateRecord(record, false); err != nil {
		return Record{}, err
	}
	digest, err := calculateContentDigest(record)
	if err != nil {
		return Record{}, err
	}
	record.ContentDigest = string(digest)
	return record, nil
}

func cloneAndNormalize(source Record) Record {
	record := source
	record.StartedAt = canonicalTime(source.StartedAt)
	record.FinishedAt = canonicalTime(source.FinishedAt)
	record.Environment = Environment{
		GoVersion:   normalizeWhitespace(source.Environment.GoVersion),
		OS:          normalizeWhitespace(source.Environment.OS),
		Arch:        normalizeWhitespace(source.Environment.Arch),
		CPU:         normalizeWhitespace(source.Environment.CPU),
		LogicalCPUs: source.Environment.LogicalCPUs,
	}
	record.Workload.Parameters = cloneIntMap(source.Workload.Parameters)
	record.Parameters = cloneIntMap(source.Parameters)
	record.Measurements = cloneMeasurements(source.Measurements)
	record.Faults = cloneSlice(source.Faults)
	record.Assertions = cloneSlice(source.Assertions)
	record.Diagnostics = cloneSlice(source.Diagnostics)
	record.Limitations = cloneSlice(source.Limitations)
	return record
}

func canonicalTime(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return value.UTC().Round(0)
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

func cloneMeasurements(source map[string]Measurement) map[string]Measurement {
	if source == nil {
		return nil
	}
	cloned := make(map[string]Measurement, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneSlice[T any](source []T) []T {
	if source == nil {
		return nil
	}
	return append([]T{}, source...)
}

func normalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func validateRecord(record Record, requireDigest bool) error {
	invalid := func(format string, values ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalidRecord, fmt.Sprintf(format, values...))
	}
	if record.SchemaVersion != SchemaVersion {
		return invalid("schema_version=%d; want %d", record.SchemaVersion, SchemaVersion)
	}
	if err := ValidateID(record.ID); err != nil {
		return invalid("invalid attempt ID")
	}
	if err := ValidateRunSetID(record.RunSetID); err != nil {
		return invalid("invalid run-set ID")
	}
	identities := []struct {
		name     string
		value    string
		required bool
	}{
		{name: "lab_id", value: record.LabID, required: true},
		{name: "required_run_id", value: record.RequiredRunID, required: true},
		{name: "binding_id", value: record.BindingID, required: true},
		{name: "claim_id", value: record.ClaimID, required: true},
		{name: "implementation_id", value: record.ImplementationID, required: true},
		{name: "adapter_id", value: record.AdapterID, required: false},
		{name: "workload.id", value: record.Workload.ID, required: true},
	}
	for _, identity := range identities {
		if identity.value == "" && !identity.required {
			continue
		}
		if err := ValidateStableID(identity.value); err != nil {
			return invalid("%s is invalid", identity.name)
		}
	}
	if record.Profile != ProfileSmoke && record.Profile != ProfileDeep {
		return invalid("profile %q is not closed", record.Profile)
	}
	switch record.Role {
	case RoleBaseline, RoleVariant:
		if record.AdapterID != "" {
			return invalid("non-adapter role has adapter_id")
		}
	case RoleAdapter:
		if record.AdapterID == "" || record.AdapterID != record.ImplementationID {
			return invalid("adapter role identity is inconsistent")
		}
	default:
		return invalid("role %q is not closed", record.Role)
	}
	switch record.Status {
	case StatusPassed, StatusFailed, StatusSkipped, StatusFlaky, StatusInconclusive:
	default:
		return invalid("status %q is not closed", record.Status)
	}
	if !commitPattern.MatchString(record.SourceCommit) {
		return invalid("source_commit is invalid")
	}
	if _, err := inputdigest.Parse(string(record.InputDigest)); err != nil {
		return invalid("input_digest is invalid")
	}
	if !utf8.ValidString(record.Hypothesis) || !utf8.ValidString(record.Conclusion) || strings.TrimSpace(record.Hypothesis) == "" || strings.TrimSpace(record.Conclusion) == "" {
		return invalid("hypothesis and conclusion must be nonblank")
	}
	if record.Deadline <= 0 {
		return invalid("deadline must be positive")
	}
	if record.StartedAt.IsZero() || record.FinishedAt.IsZero() || record.StartedAt.Location() != time.UTC || record.FinishedAt.Location() != time.UTC || record.FinishedAt.Before(record.StartedAt) {
		return invalid("timestamps must be ordered canonical UTC values")
	}
	if record.Workload.Parameters == nil || record.Parameters == nil || !reflect.DeepEqual(record.Workload.Parameters, record.Parameters) {
		return invalid("parameters must be equal explicit maps")
	}
	for key := range record.Workload.Parameters {
		if !utf8.ValidString(key) {
			return invalid("workload parameter key is not valid UTF-8")
		}
	}
	for key := range record.Parameters {
		if !utf8.ValidString(key) {
			return invalid("parameter key is not valid UTF-8")
		}
	}
	if record.Faults == nil || record.Measurements == nil || record.Assertions == nil || record.Diagnostics == nil || record.Limitations == nil {
		return invalid("maps and slices must be explicit")
	}
	seenFaults := make(map[string]struct{}, len(record.Faults))
	for _, fault := range record.Faults {
		if ValidateStableID(fault.ID) != nil || fault.At < 0 || fault.Duration < 0 {
			return invalid("fault is invalid")
		}
		if _, exists := seenFaults[fault.ID]; exists {
			return invalid("fault ID is duplicated")
		}
		seenFaults[fault.ID] = struct{}{}
	}
	for id, measurement := range record.Measurements {
		if ValidateMetricID(id) != nil || !utf8.ValidString(measurement.Unit) || strings.TrimSpace(measurement.Unit) == "" || measurement.Unit != strings.TrimSpace(measurement.Unit) {
			return invalid("measurement %q is invalid", id)
		}
	}
	seenAssertions := make(map[string]struct{}, len(record.Assertions))
	for _, assertion := range record.Assertions {
		if ValidateStableID(assertion.ID) != nil || !utf8.ValidString(assertion.Message) {
			return invalid("assertion ID is invalid")
		}
		if _, exists := seenAssertions[assertion.ID]; exists {
			return invalid("assertion ID is duplicated")
		}
		seenAssertions[assertion.ID] = struct{}{}
		if record.Status == StatusPassed && !assertion.Passed {
			return invalid("passed record contains a false assertion")
		}
	}
	if record.Status != StatusPassed && len(record.Diagnostics) == 0 {
		return invalid("non-passed record needs a diagnostic")
	}
	for _, diagnostic := range record.Diagnostics {
		if !diagnosticPattern.MatchString(diagnostic.Code) || diagnostic.Event != "" && ValidateStableID(diagnostic.Event) != nil || !utf8.ValidString(diagnostic.Message) || strings.TrimSpace(diagnostic.Message) == "" {
			return invalid("diagnostic is invalid")
		}
	}
	seenLimitations := make(map[string]struct{}, len(record.Limitations))
	for _, limitation := range record.Limitations {
		if !utf8.ValidString(limitation) || strings.TrimSpace(limitation) == "" {
			return invalid("limitation is blank")
		}
		if _, exists := seenLimitations[limitation]; exists {
			return invalid("limitation is duplicated")
		}
		seenLimitations[limitation] = struct{}{}
	}
	if !utf8.ValidString(record.Environment.GoVersion) || len(record.Environment.GoVersion) > 128 || !goVersionPattern.MatchString(record.Environment.GoVersion) || normalizeWhitespace(record.Environment.GoVersion) != record.Environment.GoVersion ||
		record.Environment.CPU != "unknown" ||
		!platformIDPattern.MatchString(record.Environment.OS) || !platformIDPattern.MatchString(record.Environment.Arch) || record.Environment.LogicalCPUs <= 0 {
		return invalid("environment is invalid")
	}
	if requireDigest {
		if _, err := inputdigest.Parse(record.ContentDigest); err != nil {
			return fmt.Errorf("%w: content_digest is invalid", ErrContentDigest)
		}
	} else if record.ContentDigest != "" {
		return invalid("unsealed content_digest must be empty")
	}
	return nil
}

func validAttemptID(value, prefix string, pattern *regexp.Regexp) bool {
	if !pattern.MatchString(value) || len(value) != len(prefix)+20+1+32 {
		return false
	}
	if value[len(prefix)+20] != '-' {
		return false
	}
	_, err := time.Parse("20060102T150405.000Z", value[len(prefix):len(prefix)+20])
	return err == nil
}
