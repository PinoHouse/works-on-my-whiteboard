package evidence

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
)

func TestNewIDsUseUTCMillisecondsAndExactlySixteenEntropyBytes(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 7, 14, 20, 0, 0, 123987654, location)
	entropy := make([]byte, 16)
	for index := range entropy {
		entropy[index] = byte(index)
	}
	id, err := NewID(now, bytes.NewReader(entropy))
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	if id != "run-20260714T120000.123Z-000102030405060708090a0b0c0d0e0f" {
		t.Fatalf("ID = %q", id)
	}
	runSetID, err := NewRunSetID(now, bytes.NewReader(entropy))
	if err != nil {
		t.Fatalf("NewRunSetID: %v", err)
	}
	if runSetID != RunSetID("set-20260714T120000.123Z-000102030405060708090a0b0c0d0e0f") {
		t.Fatalf("run set ID = %q", runSetID)
	}
	if _, err := NewID(now, bytes.NewReader(entropy[:15])); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short entropy error = %v", err)
	}
}

func TestValidateIDsUseTheExactFrozenGrammar(t *testing.T) {
	validators := []struct {
		name        string
		canonical   string
		wrongPrefix string
		validate    func(string) error
	}{
		{
			name:        "attempt",
			canonical:   "run-20260714T120000.123Z-000102030405060708090a0b0c0d0e0f",
			wrongPrefix: "set-20260714T120000.123Z-000102030405060708090a0b0c0d0e0f",
			validate:    ValidateID,
		},
		{
			name:        "run set",
			canonical:   "set-20260714T120000.123Z-000102030405060708090a0b0c0d0e0f",
			wrongPrefix: "run-20260714T120000.123Z-000102030405060708090a0b0c0d0e0f",
			validate: func(value string) error {
				return ValidateRunSetID(RunSetID(value))
			},
		},
	}
	for _, validator := range validators {
		t.Run(validator.name, func(t *testing.T) {
			if err := validator.validate(validator.canonical); err != nil {
				t.Fatalf("canonical literal rejected: %v", err)
			}
			invalid := []struct {
				name  string
				value string
			}{
				{name: "wrong prefix", value: validator.wrongPrefix},
				{name: "uppercase hex", value: validator.canonical[:len(validator.canonical)-1] + "F"},
				{name: "31 hex digits", value: validator.canonical[:len(validator.canonical)-1]},
				{name: "33 hex digits", value: validator.canonical + "0"},
				{name: "invalid date", value: strings.Replace(validator.canonical, "20260714", "20260230", 1)},
				{name: "two millisecond digits", value: strings.Replace(validator.canonical, ".123Z", ".12Z", 1)},
				{name: "non Z timezone", value: strings.Replace(validator.canonical, "Z-", "+00:00-", 1)},
				{name: "leading suffix", value: "x" + validator.canonical},
				{name: "trailing suffix", value: validator.canonical + "x"},
				{name: "path character", value: validator.canonical[:len(validator.canonical)-1] + "/"},
			}
			for _, test := range invalid {
				t.Run(test.name, func(t *testing.T) {
					if err := validator.validate(test.value); err == nil {
						t.Fatalf("invalid literal %q was accepted", test.value)
					}
				})
			}
		})
	}
}

func TestValidateCatalogAndMetricIDsUseTheFrozenGrammar(t *testing.T) {
	tests := []struct {
		name      string
		validate  func(string) error
		canonical []string
		invalid   []string
	}{
		{
			name:      "stable ID",
			validate:  ValidateStableID,
			canonical: []string{"a", "token-bucket", "token-bucket2"},
			invalid:   []string{"", "2token", "Token", "token_bucket", "token.bucket", "token/bucket", " token"},
		},
		{
			name:      "metric ID",
			validate:  ValidateMetricID,
			canonical: []string{"x", "requests.total", "latency_p99", "cache-hit2"},
			invalid:   []string{"", "2xx", "Requests.total", "requests..total", "requests-", "requests/total", " requests"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, value := range test.canonical {
				if err := test.validate(value); err != nil {
					t.Errorf("canonical %q rejected: %v", value, err)
				}
			}
			for _, value := range test.invalid {
				if err := test.validate(value); !errors.Is(err, ErrInvalidRecord) {
					t.Errorf("invalid %q error = %v, want ErrInvalidRecord", value, err)
				}
			}
		})
	}
}

func TestSealDeepCopiesNormalizesAndComputesSelfDigest(t *testing.T) {
	source := validRecord()
	source.Workload.Parameters["capacity"] = 4
	sealed, err := Seal(source)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := inputdigest.Parse(sealed.ContentDigest); err != nil {
		t.Fatalf("content digest = %q: %v", sealed.ContentDigest, err)
	}
	if source.ContentDigest != "" {
		t.Fatalf("Seal mutated source content digest to %q", source.ContentDigest)
	}
	if sealed.StartedAt.Location() != time.UTC || sealed.FinishedAt.Location() != time.UTC {
		t.Fatalf("timestamps were not normalized to UTC: %v / %v", sealed.StartedAt.Location(), sealed.FinishedAt.Location())
	}
	sealed.Parameters["capacity"] = 99
	sealed.Workload.Parameters["capacity"] = 98
	sealed.Measurements["requests.total"] = Measurement{Unit: "changed", Value: 1}
	sealed.Assertions[0].Message = "changed"
	sealed.Limitations[0] = "changed"
	if source.Parameters["capacity"] != 4 || source.Workload.Parameters["capacity"] != 4 || source.Measurements["requests.total"].Unit != "requests" || source.Assertions[0].Message != "" || source.Limitations[0] == "changed" {
		t.Fatal("sealed record aliases caller-owned data")
	}
}

func TestSealRejectsEveryNilCollectionInsteadOfRepairingIt(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Record)
	}{
		{name: "workload parameters", mutate: func(record *Record) { record.Workload.Parameters = nil }},
		{name: "parameters", mutate: func(record *Record) { record.Parameters = nil }},
		{name: "faults", mutate: func(record *Record) { record.Faults = nil }},
		{name: "measurements", mutate: func(record *Record) { record.Measurements = nil }},
		{name: "assertions", mutate: func(record *Record) { record.Assertions = nil }},
		{name: "diagnostics", mutate: func(record *Record) { record.Diagnostics = nil }},
		{name: "limitations", mutate: func(record *Record) { record.Limitations = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := validRecord()
			test.mutate(&record)
			if _, err := Seal(record); !errors.Is(err, ErrInvalidRecord) {
				t.Fatalf("Seal error = %v, want ErrInvalidRecord", err)
			}
		})
	}
}

func TestSealAcceptsClosedDiagnosticCodeGrammarWithoutWeakeningCatalogIDs(t *testing.T) {
	for _, code := range []string{"experiment_execution_failed", "release-record-missing"} {
		t.Run(code, func(t *testing.T) {
			record := validRecord()
			record.Status = StatusFailed
			record.Diagnostics = []Diagnostic{{Code: code, Event: "execute", Message: "the experiment failed"}}
			if _, err := Seal(record); err != nil {
				t.Fatalf("Seal diagnostic code %q: %v", code, err)
			}
		})
	}

	record := validRecord()
	record.LabID = "token_bucket"
	if _, err := Seal(record); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("underscore catalog ID error = %v, want ErrInvalidRecord", err)
	}
}

func TestSealRejectsClosedModelViolations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Record)
	}{
		{name: "schema", mutate: func(record *Record) { record.SchemaVersion = 2 }},
		{name: "attempt id", mutate: func(record *Record) { record.ID = "not-an-attempt" }},
		{name: "run set", mutate: func(record *Record) { record.RunSetID = "set-invalid" }},
		{name: "catalog id", mutate: func(record *Record) { record.LabID = "Bad" }},
		{name: "profile", mutate: func(record *Record) { record.Profile = "fast" }},
		{name: "role", mutate: func(record *Record) { record.Role = "other" }},
		{name: "adapter mismatch", mutate: func(record *Record) { record.AdapterID = "redis" }},
		{name: "status", mutate: func(record *Record) { record.Status = "unknown" }},
		{name: "source commit", mutate: func(record *Record) { record.SourceCommit = "abc" }},
		{name: "input digest", mutate: func(record *Record) { record.InputDigest = "sha256:BAD" }},
		{name: "blank hypothesis", mutate: func(record *Record) { record.Hypothesis = " \t" }},
		{name: "blank conclusion", mutate: func(record *Record) { record.Conclusion = "" }},
		{name: "deadline", mutate: func(record *Record) { record.Deadline = 0 }},
		{name: "fault at", mutate: func(record *Record) { record.Faults = []Fault{{ID: "down", At: -1}} }},
		{name: "fault duplicate", mutate: func(record *Record) { record.Faults = []Fault{{ID: "down"}, {ID: "down"}} }},
		{name: "time order", mutate: func(record *Record) { record.FinishedAt = record.StartedAt.Add(-time.Nanosecond) }},
		{name: "parameter mismatch", mutate: func(record *Record) { record.Parameters["capacity"] = 9 }},
		{name: "measurement id", mutate: func(record *Record) { record.Measurements = map[string]Measurement{"Bad Metric": {Unit: "count"}} }},
		{name: "measurement unit", mutate: func(record *Record) { record.Measurements["requests.total"] = Measurement{} }},
		{name: "assertion duplicate", mutate: func(record *Record) { record.Assertions = append(record.Assertions, record.Assertions[0]) }},
		{name: "passed false assertion", mutate: func(record *Record) { record.Assertions[0].Passed = false }},
		{name: "nonpassed no diagnostic", mutate: func(record *Record) { record.Status = StatusFailed; record.Diagnostics = []Diagnostic{} }},
		{name: "environment cpu", mutate: func(record *Record) { record.Environment.CPU = "" }},
		{name: "environment logical cpus", mutate: func(record *Record) { record.Environment.LogicalCPUs = 0 }},
		{name: "blank limitation", mutate: func(record *Record) { record.Limitations = []string{" "} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := validRecord()
			test.mutate(&record)
			if _, err := Seal(record); !errors.Is(err, ErrInvalidRecord) {
				t.Fatalf("error = %v, want ErrInvalidRecord", err)
			}
		})
	}
}

func TestSealRejectsInvalidUTF8BeforeCanonicalEncodingCanRepairIt(t *testing.T) {
	invalid := string([]byte{0xff})
	tests := []struct {
		name   string
		mutate func(*Record)
	}{
		{name: "hypothesis", mutate: func(record *Record) { record.Hypothesis = invalid }},
		{name: "parameter key", mutate: func(record *Record) {
			record.Workload.Parameters = map[string]int64{invalid: 1}
			record.Parameters = map[string]int64{invalid: 1}
		}},
		{name: "assertion message", mutate: func(record *Record) { record.Assertions[0].Message = invalid }},
		{name: "diagnostic message", mutate: func(record *Record) {
			record.Status = StatusFailed
			record.Diagnostics = []Diagnostic{{Code: "experiment_execution_failed", Message: invalid}}
		}},
		{name: "conclusion", mutate: func(record *Record) { record.Conclusion = invalid }},
		{name: "limitation", mutate: func(record *Record) { record.Limitations = []string{invalid} }},
		{name: "environment go version", mutate: func(record *Record) { record.Environment.GoVersion = invalid }},
		{name: "environment cpu", mutate: func(record *Record) { record.Environment.CPU = invalid }},
		{name: "measurement unit", mutate: func(record *Record) {
			record.Measurements["requests.total"] = Measurement{Unit: invalid, Value: 5}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := validRecord()
			test.mutate(&record)
			if _, err := Seal(record); !errors.Is(err, ErrInvalidRecord) {
				t.Fatalf("Seal error = %v, want ErrInvalidRecord", err)
			}
		})
	}
}

func TestCellKeyUsesExactlySixIdentityFields(t *testing.T) {
	record := validRecord()
	want := CellKey{
		LabID: "token-bucket", RequiredRunID: "burst-boundary", BindingID: "token-bucket-boundary",
		ClaimID: "token-bucket-bounds-burst", ImplementationID: "token-bucket", AdapterID: "",
	}
	if got := record.CellKey(); got != want {
		t.Fatalf("CellKey = %+v, want %+v", got, want)
	}
	record.Role = RoleVariant
	if got := record.CellKey(); got != want {
		t.Fatalf("role changed identity: %+v", got)
	}
}

func TestCurrentEnvironmentContainsOnlyNormalizedDeclaredFields(t *testing.T) {
	environment := CurrentEnvironment()
	if strings.TrimSpace(environment.GoVersion) == "" || strings.TrimSpace(environment.OS) == "" || strings.TrimSpace(environment.Arch) == "" || environment.CPU == "" || environment.LogicalCPUs <= 0 {
		t.Fatalf("environment = %+v", environment)
	}
	encoded := strings.Join([]string{environment.GoVersion, environment.OS, environment.Arch, environment.CPU}, " ")
	if strings.Contains(encoded, "\n") || strings.Contains(encoded, "\t") {
		t.Fatalf("environment contains unnormalized whitespace: %q", encoded)
	}
}

func TestSealAcceptsOnlyClosedPathFreeEnvironmentValues(t *testing.T) {
	for _, version := range []string{"go1.26.5", "go1.27rc1", "devel go1.28-abcdef"} {
		t.Run("valid "+version, func(t *testing.T) {
			record := validRecord()
			record.Environment.GoVersion = version
			record.Environment.CPU = "unknown"
			if _, err := Seal(record); err != nil {
				t.Fatalf("Seal environment version %q: %v", version, err)
			}
		})
	}

	tests := []struct {
		name   string
		mutate func(*Record)
	}{
		{name: "cpu model", mutate: func(record *Record) { record.Environment.CPU = "Apple M4" }},
		{name: "cpu path", mutate: func(record *Record) { record.Environment.CPU = "/tmp/cpu" }},
		{name: "go absolute path", mutate: func(record *Record) { record.Environment.GoVersion = "/tmp/go1.26" }},
		{name: "go windows path", mutate: func(record *Record) { record.Environment.GoVersion = `C:\\Go\\bin` }},
		{name: "go unsafe prefix", mutate: func(record *Record) { record.Environment.GoVersion = "custom compiler" }},
		{name: "go too long", mutate: func(record *Record) { record.Environment.GoVersion = "go1." + strings.Repeat("1", 200) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := validRecord()
			test.mutate(&record)
			if _, err := Seal(record); !errors.Is(err, ErrInvalidRecord) {
				t.Fatalf("Seal error = %v, want ErrInvalidRecord", err)
			}
		})
	}
}

func validRecord() Record {
	started := time.Date(2026, 7, 14, 12, 0, 0, 0, time.FixedZone("zero", 0))
	return Record{
		SchemaVersion:    1,
		ID:               "run-20260714T120000.123Z-000102030405060708090a0b0c0d0e0f",
		RunSetID:         "set-20260714T120000.123Z-101112131415161718191a1b1c1d1e1f",
		LabID:            "token-bucket",
		RequiredRunID:    "burst-boundary",
		BindingID:        "token-bucket-boundary",
		ClaimID:          "token-bucket-bounds-burst",
		Role:             RoleBaseline,
		ImplementationID: "token-bucket",
		AdapterID:        "",
		Profile:          ProfileDeep,
		Hypothesis:       "A burst cannot exceed capacity.",
		Workload: Workload{ID: "burst-boundary", Parameters: map[string]int64{
			"capacity": 4,
		}},
		Faults:       []Fault{},
		Status:       StatusPassed,
		SourceCommit: strings.Repeat("a", 40),
		InputDigest:  inputdigest.Digest("sha256:" + strings.Repeat("b", 64)),
		Environment: Environment{
			GoVersion: "go1.26.5", OS: "darwin", Arch: "arm64", CPU: "unknown", LogicalCPUs: 10,
		},
		Seed:           1,
		Deadline:       2 * time.Second,
		StartedAt:      started,
		FinishedAt:     started.Add(time.Second),
		EventsExecuted: 5,
		Parameters:     map[string]int64{"capacity": 4},
		Measurements: map[string]Measurement{
			"requests.total": {Unit: "requests", Value: 5},
		},
		Assertions:    []Assertion{{ID: "all-requests-decided", Passed: true, Message: ""}},
		Diagnostics:   []Diagnostic{},
		Conclusion:    "The bounded run passed.",
		Limitations:   []string{"local deterministic model"},
		ContentDigest: "",
	}
}
