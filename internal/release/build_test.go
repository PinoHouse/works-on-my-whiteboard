package release

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

func TestBuildBindsExactCurrentRecord(t *testing.T) {
	expected := []ExpectedCell{testExpectedCell()}
	record := testRecord(t, expected[0], testEvidenceID(1), testRunSetID(1), "b")

	manifest, err := Build(testInputDigest("a"), expected, []evidence.Record{record})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	want := Manifest{
		SchemaVersion: SchemaVersion,
		InputDigest:   testInputDigest("a"),
		Profile:       evidence.ProfileDeep,
		RunSetID:      testRunSetID(1),
		Selections: []Selection{{
			Role:             evidence.RoleBaseline,
			LabID:            expected[0].Cell.LabID,
			RequiredRunID:    expected[0].Cell.RequiredRunID,
			BindingID:        expected[0].Cell.BindingID,
			ClaimID:          expected[0].Cell.ClaimID,
			ImplementationID: expected[0].Cell.ImplementationID,
			AdapterID:        "",
			EvidenceID:       record.ID,
			ContentDigest:    record.ContentDigest,
		}},
	}
	if !reflect.DeepEqual(manifest, want) {
		t.Fatalf("manifest = %#v, want %#v", manifest, want)
	}

	expected[0].Cell.LabID = "mutated"
	expected[0].Workload.Parameters["requests"] = 999
	if manifest.Selections[0].LabID != "lab" {
		t.Fatalf("caller mutation changed manifest: %#v", manifest)
	}
}

func TestBuildRejectsMissingCurrentRecord(t *testing.T) {
	_, err := Build(testInputDigest("a"), []ExpectedCell{testExpectedCell()}, []evidence.Record{})
	if err == nil {
		t.Fatal("Build returned nil error")
	}
}

func TestBuildRejectsUnsafeDynamicRecordText(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*evidence.Record)
	}{
		{name: "assertion message", mutate: func(record *evidence.Record) {
			record.Assertions[0].Message = "observed at /absolute/workspace/secret"
		}},
		{name: "conclusion", mutate: func(record *evidence.Record) {
			record.Conclusion = "attempt " + testEvidenceID(2) + " passed"
		}},
		{name: "limitation", mutate: func(record *evidence.Record) {
			record.Limitations[0] = "host root /absolute/workspace/secret"
		}},
		{name: "diagnostic", mutate: func(record *evidence.Record) {
			record.Diagnostics = []evidence.Diagnostic{{Code: "observation", Message: "host root /absolute/workspace/secret"}}
		}},
		{name: "hypothesis", mutate: func(record *evidence.Record) {
			record.Hypothesis = "host root /absolute/workspace/secret"
		}},
		{name: "workload parameter key", mutate: func(record *evidence.Record) {
			record.Workload.Parameters = map[string]int64{"/private/tmp/host-only": 4}
			record.Parameters = map[string]int64{"/private/tmp/host-only": 4}
		}},
		{name: "measurement unit", mutate: func(record *evidence.Record) {
			measurement := record.Measurements["requests.total"]
			measurement.Unit = "/private/tmp/host-only"
			record.Measurements["requests.total"] = measurement
		}},
		{name: "environment Go version", mutate: func(record *evidence.Record) {
			record.Environment.GoVersion = "go1.26.5 attempt_" + testEvidenceID(2)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expected := []ExpectedCell{testExpectedCell()}
			record := testRecord(t, expected[0], testEvidenceID(1), testRunSetID(1), "a")
			test.mutate(&record)
			record.ContentDigest = ""
			sealed, err := evidence.Seal(record)
			if err != nil {
				t.Fatalf("Seal unsafe legacy-compatible record: %v", err)
			}

			_, err = Build(testInputDigest("a"), expected, []evidence.Record{sealed})
			if !errors.Is(err, ErrUnsafeDynamicText) {
				t.Fatalf("Build error = %v, want ErrUnsafeDynamicText", err)
			}
		})
	}
}

func TestBuildRejectsUnsafeExpectedLimitation(t *testing.T) {
	expected := []ExpectedCell{testExpectedCell()}
	expected[0].Limitations[0] = "host root /absolute/workspace/secret"
	record := testRecord(t, expected[0], testEvidenceID(1), testRunSetID(1), "a")

	_, err := Build(testInputDigest("a"), expected, []evidence.Record{record})
	if !errors.Is(err, ErrUnsafeDynamicText) {
		t.Fatalf("Build error = %v, want ErrUnsafeDynamicText", err)
	}
}

func TestBuildRejectsUnsafeExpectedDefinitionText(t *testing.T) {
	tests := []struct {
		name    string
		hostile string
		mutate  func(*ExpectedCell)
	}{
		{name: "hypothesis", hostile: "/absolute/workspace/secret", mutate: func(expected *ExpectedCell) {
			expected.Hypothesis = "host root /absolute/workspace/secret"
		}},
		{name: "workload parameter key", hostile: "/private/tmp/host-only", mutate: func(expected *ExpectedCell) {
			expected.Workload.Parameters = map[string]int64{"/private/tmp/host-only": 4}
		}},
		{name: "blank workload parameter key", mutate: func(expected *ExpectedCell) {
			expected.Workload.Parameters = map[string]int64{" ": 4}
		}},
		{name: "measurement unit", hostile: "/private/tmp/host-only", mutate: func(expected *ExpectedCell) {
			expected.Measurements[0].Unit = "/private/tmp/host-only"
		}},
		{name: "padded measurement unit", hostile: "/private/tmp/host-only", mutate: func(expected *ExpectedCell) {
			expected.Measurements[0].Unit = " /private/tmp/host-only "
		}},
		{name: "encoded hypothesis path", hostile: "%2Fprivate%2Ftmp%2Fhost-only", mutate: func(expected *ExpectedCell) {
			expected.Hypothesis = "host root %2Fprivate%2Ftmp%2Fhost-only"
		}},
		{name: "encoded workload parameter path", hostile: "%2Fprivate%2Ftmp%2Fhost-only", mutate: func(expected *ExpectedCell) {
			expected.Workload.Parameters = map[string]int64{"%2Fprivate%2Ftmp%2Fhost-only": 4}
		}},
		{name: "encoded measurement unit path", hostile: "file%3A%2Fprivate%2Ftmp%2Fhost-only", mutate: func(expected *ExpectedCell) {
			expected.Measurements[0].Unit = "file%3A%2Fprivate%2Ftmp%2Fhost-only"
		}},
		{name: "mixed malformed and encoded hypothesis path", hostile: "%ZZ", mutate: func(expected *ExpectedCell) {
			expected.Hypothesis = "malformed=%ZZ host root %2Fprivate%2Ftmp%2Fhost-only"
		}},
		{name: "HTML entity hypothesis path", hostile: "&#x2F;", mutate: func(expected *ExpectedCell) {
			expected.Hypothesis = "host root &#x2F;private&#x2F;tmp&#x2F;host-only"
		}},
		{name: "HTML entity attempt identity", hostile: "&#x2D;", mutate: func(expected *ExpectedCell) {
			expected.Hypothesis = "attempt run&#x2D;20260714T010203.004Z&#x2D;00000000000000000000000000000009"
		}},
		{name: "Unicode format hypothesis", hostile: "\u202e", mutate: func(expected *ExpectedCell) {
			expected.Hypothesis = "direction \u202eoverride"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expected := testExpectedCell()
			test.mutate(&expected)
			_, err := Build(testInputDigest("a"), []ExpectedCell{expected}, nil)
			if !errors.Is(err, ErrUnsafeDynamicText) {
				t.Fatalf("Build error = %v, want ErrUnsafeDynamicText", err)
			}
			if test.hostile != "" && strings.Contains(err.Error(), test.hostile) {
				t.Fatalf("Build error echoed hostile dynamic text: %v", err)
			}
		})
	}
}

func TestBuildRejectsDuplicateExtraAndMixedRunSetRecords(t *testing.T) {
	first := testExpectedCell()
	second := testSecondExpectedCell()
	expected := []ExpectedCell{first, second}
	firstRecord := testRecord(t, first, testEvidenceID(1), testRunSetID(1), "a")
	secondRecord := testRecord(t, second, testEvidenceID(2), testRunSetID(1), "a")

	tests := []struct {
		name    string
		current []evidence.Record
	}{
		{name: "missing", current: []evidence.Record{firstRecord}},
		{name: "extra", current: []evidence.Record{firstRecord, secondRecord, secondRecord}},
		{name: "duplicate cell", current: []evidence.Record{firstRecord, resealWithID(t, firstRecord, testEvidenceID(2))}},
		{name: "duplicate evidence ID", current: []evidence.Record{firstRecord, resealWithID(t, secondRecord, firstRecord.ID)}},
		{name: "mixed run set", current: []evidence.Record{firstRecord, resealWithRunSet(t, secondRecord, testRunSetID(2))}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Build(testInputDigest("a"), expected, test.current); err == nil {
				t.Fatal("Build returned nil error")
			}
		})
	}
}

func TestBuildProjectsRecordsInExpectedMatrixOrder(t *testing.T) {
	first := testExpectedCell()
	second := testSecondExpectedCell()
	firstRecord := testRecord(t, first, testEvidenceID(1), testRunSetID(1), "a")
	secondRecord := testRecord(t, second, testEvidenceID(2), testRunSetID(1), "a")
	manifest, err := Build(testInputDigest("a"), []ExpectedCell{first, second}, []evidence.Record{secondRecord, firstRecord})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if got := []string{manifest.Selections[0].LabID, manifest.Selections[1].LabID}; !reflect.DeepEqual(got, []string{"lab", "lab-z"}) {
		t.Fatalf("selection order = %v", got)
	}
}

func TestBuildAllowsEvidenceOnlyHeadAdvance(t *testing.T) {
	expected := []ExpectedCell{testExpectedCell()}
	record := testRecord(t, expected[0], testEvidenceID(1), testRunSetID(1), "a")
	if _, err := Build(testInputDigest("a"), expected, []evidence.Record{record}); err != nil {
		t.Fatalf("Build rejected syntactically valid source commit A: %v", err)
	}
}

func TestBuildRejectsExactDefinitionDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*evidence.Record)
	}{
		{name: "lab", mutate: func(record *evidence.Record) { record.LabID = "other-lab" }},
		{name: "required run", mutate: func(record *evidence.Record) { record.RequiredRunID = "other-run" }},
		{name: "binding", mutate: func(record *evidence.Record) { record.BindingID = "other-binding" }},
		{name: "claim", mutate: func(record *evidence.Record) { record.ClaimID = "other-claim" }},
		{name: "implementation", mutate: func(record *evidence.Record) { record.ImplementationID = "other-implementation" }},
		{name: "adapter", mutate: func(record *evidence.Record) { record.AdapterID = "other-adapter" }},
		{name: "role", mutate: func(record *evidence.Record) { record.Role = evidence.RoleVariant }},
		{name: "profile", mutate: func(record *evidence.Record) { record.Profile = evidence.ProfileSmoke }},
		{name: "start", mutate: func(record *evidence.Record) { record.StartedAt = record.StartedAt.Add(time.Nanosecond) }},
		{name: "workload", mutate: func(record *evidence.Record) { record.Workload.ID = "other-workload" }},
		{name: "parameter value", mutate: func(record *evidence.Record) {
			record.Workload.Parameters["requests"]++
			record.Parameters["requests"]++
		}},
		{name: "fault at", mutate: func(record *evidence.Record) { record.Faults[0].At++ }},
		{name: "fault duration", mutate: func(record *evidence.Record) { record.Faults[0].Duration++ }},
		{name: "seed", mutate: func(record *evidence.Record) { record.Seed++ }},
		{name: "deadline", mutate: func(record *evidence.Record) { record.Deadline++ }},
		{name: "hypothesis", mutate: func(record *evidence.Record) { record.Hypothesis = "different" }},
		{name: "limitations", mutate: func(record *evidence.Record) { record.Limitations[0] = "different" }},
		{name: "metric unit", mutate: func(record *evidence.Record) {
			record.Measurements["requests.total"] = evidence.Measurement{Unit: "bytes", Value: 4}
		}},
		{name: "assertion order", mutate: func(record *evidence.Record) {
			record.Assertions[0], record.Assertions[1] = record.Assertions[1], record.Assertions[0]
		}},
		{name: "assertion false", mutate: func(record *evidence.Record) {
			record.Assertions[0].Passed = false
			record.Status = evidence.StatusFailed
			record.Diagnostics = []evidence.Diagnostic{{Code: "assertion_failed", Message: "failed"}}
		}},
		{name: "events", mutate: func(record *evidence.Record) { record.EventsExecuted++ }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expected := []ExpectedCell{testExpectedCell()}
			record := testRecord(t, expected[0], testEvidenceID(1), testRunSetID(1), "b")
			test.mutate(&record)
			record.ContentDigest = ""
			sealed, sealErr := evidence.Seal(record)
			if sealErr != nil {
				return
			}
			if _, err := Build(testInputDigest("a"), expected, []evidence.Record{sealed}); err == nil {
				t.Fatal("Build returned nil error")
			}
		})
	}
}

func TestBuildDistinguishesNilFromExplicitEmpty(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ExpectedCell)
	}{
		{name: "parameters", mutate: func(expected *ExpectedCell) { expected.Workload.Parameters = nil }},
		{name: "faults", mutate: func(expected *ExpectedCell) { expected.Faults = nil }},
		{name: "cell faults", mutate: func(expected *ExpectedCell) { expected.Cell.Faults = nil }},
		{name: "assertions", mutate: func(expected *ExpectedCell) { expected.Cell.AssertionIDs = nil }},
		{name: "limitations", mutate: func(expected *ExpectedCell) { expected.Limitations = nil }},
		{name: "measurements", mutate: func(expected *ExpectedCell) { expected.Measurements = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expected := testExpectedCell()
			test.mutate(&expected)
			if _, err := Build(testInputDigest("a"), []ExpectedCell{expected}, nil); err == nil {
				t.Fatal("Build returned nil error")
			}
		})
	}
}

func TestBuildRejectsMalformedExpectedDefinition(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ExpectedCell)
	}{
		{name: "blank hypothesis", mutate: func(expected *ExpectedCell) { expected.Hypothesis = " \t" }},
		{name: "zero deadline", mutate: func(expected *ExpectedCell) { expected.Deadline = 0 }},
		{name: "duplicate fault", mutate: func(expected *ExpectedCell) {
			expected.Cell.Faults = append(expected.Cell.Faults, expected.Cell.Faults[0])
			expected.Faults = append(expected.Faults, expected.Faults[0])
		}},
		{name: "duplicate assertion", mutate: func(expected *ExpectedCell) {
			expected.Cell.AssertionIDs = append(expected.Cell.AssertionIDs, expected.Cell.AssertionIDs[0])
		}},
		{name: "duplicate measurement", mutate: func(expected *ExpectedCell) {
			expected.Measurements = append(expected.Measurements, expected.Measurements[0])
		}},
		{name: "blank measurement unit", mutate: func(expected *ExpectedCell) { expected.Measurements[0].Unit = " " }},
		{name: "workload mismatch", mutate: func(expected *ExpectedCell) { expected.Workload.ID = "other-workload" }},
		{name: "fault order mismatch", mutate: func(expected *ExpectedCell) { expected.Cell.Faults[0] = "other-fault" }},
		{name: "unknown role", mutate: func(expected *ExpectedCell) { expected.Cell.Role = "leader" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expected := testExpectedCell()
			test.mutate(&expected)
			if _, err := Build(testInputDigest("a"), []ExpectedCell{expected}, nil); err == nil {
				t.Fatal("Build returned nil error")
			}
		})
	}
}

func testExpectedCell() ExpectedCell {
	return ExpectedCell{
		Cell: validator.MatrixCell{
			LabID:            "lab",
			RequiredRunID:    "required-run",
			BindingID:        "binding",
			ClaimID:          "claim",
			Role:             string(evidence.RoleBaseline),
			ImplementationID: "implementation",
			AdapterID:        "",
			Workload:         "workload",
			Faults:           []string{"network-fault"},
			AssertionIDs:     []string{"assertion-a", "assertion-b"},
		},
		Profile: evidence.ProfileDeep,
		Start:   time.Unix(0, 0).UTC(),
		Workload: evidence.Workload{
			ID:         "workload",
			Parameters: map[string]int64{"requests": 4},
		},
		Faults:         []evidence.Fault{{ID: "network-fault", At: time.Nanosecond, Duration: 2 * time.Nanosecond}},
		Seed:           7,
		Deadline:       time.Second,
		Hypothesis:     "A falsifiable hypothesis.",
		Limitations:    []string{"A limitation."},
		Measurements:   []evidence.MeasurementSpec{{ID: "requests.total", Unit: "requests"}},
		EventsExpected: 2,
	}
}

func testSecondExpectedCell() ExpectedCell {
	expected := testExpectedCell()
	expected.Cell.LabID = "lab-z"
	return expected
}

func testRecord(t *testing.T, expected ExpectedCell, id string, runSetID evidence.RunSetID, commitDigit string) evidence.Record {
	t.Helper()
	record := evidence.Record{
		SchemaVersion:    evidence.SchemaVersion,
		ID:               id,
		RunSetID:         runSetID,
		LabID:            expected.Cell.LabID,
		RequiredRunID:    expected.Cell.RequiredRunID,
		BindingID:        expected.Cell.BindingID,
		ClaimID:          expected.Cell.ClaimID,
		Role:             evidence.Role(expected.Cell.Role),
		ImplementationID: expected.Cell.ImplementationID,
		AdapterID:        expected.Cell.AdapterID,
		Profile:          expected.Profile,
		Hypothesis:       expected.Hypothesis,
		Workload: evidence.Workload{
			ID:         expected.Workload.ID,
			Parameters: cloneTestMap(expected.Workload.Parameters),
		},
		Faults:         append([]evidence.Fault{}, expected.Faults...),
		Status:         evidence.StatusPassed,
		SourceCommit:   strings.Repeat(commitDigit, 40),
		InputDigest:    testInputDigest("a"),
		Environment:    evidence.Environment{GoVersion: "go1.26.5", OS: "linux", Arch: "amd64", CPU: "unknown", LogicalCPUs: 8},
		Seed:           expected.Seed,
		Deadline:       expected.Deadline,
		StartedAt:      expected.Start,
		FinishedAt:     expected.Start.Add(time.Second),
		EventsExecuted: expected.EventsExpected,
		Parameters:     cloneTestMap(expected.Workload.Parameters),
		Measurements:   map[string]evidence.Measurement{"requests.total": {Unit: "requests", Value: 4}},
		Assertions: []evidence.Assertion{
			{ID: "assertion-a", Passed: true, Message: "passed"},
			{ID: "assertion-b", Passed: true, Message: "passed"},
		},
		Diagnostics: []evidence.Diagnostic{},
		Conclusion:  "The evidence supports the hypothesis.",
		Limitations: append([]string{}, expected.Limitations...),
	}
	sealed, err := evidence.Seal(record)
	if err != nil {
		t.Fatalf("Seal fixture: %v", err)
	}
	return sealed
}

func testInputDigest(digit string) inputdigest.Digest {
	return inputdigest.Digest("sha256:" + strings.Repeat(digit, 64))
}

func testEvidenceID(value int) string {
	return "run-20260714T010203.004Z-" + strings.Repeat("0", 31) + string(rune('0'+value))
}

func testRunSetID(value int) evidence.RunSetID {
	return evidence.RunSetID("set-20260714T010203.004Z-" + strings.Repeat("0", 31) + string(rune('0'+value)))
}

func cloneTestMap(source map[string]int64) map[string]int64 {
	cloned := make(map[string]int64, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func resealWithID(t *testing.T, source evidence.Record, id string) evidence.Record {
	t.Helper()
	source.ID = id
	source.ContentDigest = ""
	sealed, err := evidence.Seal(source)
	if err != nil {
		t.Fatalf("reseal ID fixture: %v", err)
	}
	return sealed
}

func resealWithRunSet(t *testing.T, source evidence.Record, id evidence.RunSetID) evidence.Record {
	t.Helper()
	source.RunSetID = id
	source.ContentDigest = ""
	sealed, err := evidence.Seal(source)
	if err != nil {
		t.Fatalf("reseal run-set fixture: %v", err)
	}
	return sealed
}
