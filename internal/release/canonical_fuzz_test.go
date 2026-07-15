package release

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
)

func FuzzCanonicalManifest(f *testing.F) {
	manifest := testManifestForFuzz(f)
	canonical, err := Encode(manifest)
	if err != nil {
		f.Fatalf("Encode seed: %v", err)
	}
	f.Add(canonical)
	f.Add([]byte("schema_version: 1\n"))
	f.Add([]byte{0xff})

	f.Fuzz(func(t *testing.T, data []byte) {
		decoded, err := Decode(data)
		if err != nil {
			return
		}
		encoded, err := Encode(decoded)
		if err != nil {
			t.Fatalf("Encode accepted decoded manifest: %v", err)
		}
		if !bytes.Equal(data, encoded) {
			t.Fatalf("successful Decode was not byte-canonical")
		}
	})
}

func testManifestForFuzz(t testing.TB) Manifest {
	t.Helper()
	expected := []ExpectedCell{testExpectedCell()}
	record := testRecordForTB(t, expected[0])
	manifest, err := Build(testInputDigest("a"), expected, []evidence.Record{record})
	if err != nil {
		t.Fatalf("Build fuzz fixture: %v", err)
	}
	return manifest
}

func testRecordForTB(t testing.TB, expected ExpectedCell) evidence.Record {
	t.Helper()
	record := evidence.Record{
		SchemaVersion: evidence.SchemaVersion,
		ID:            testEvidenceID(1), RunSetID: testRunSetID(1),
		LabID: expected.Cell.LabID, RequiredRunID: expected.Cell.RequiredRunID,
		BindingID: expected.Cell.BindingID, ClaimID: expected.Cell.ClaimID,
		Role: evidence.Role(expected.Cell.Role), ImplementationID: expected.Cell.ImplementationID,
		AdapterID: expected.Cell.AdapterID, Profile: expected.Profile,
		Hypothesis: expected.Hypothesis,
		Workload:   evidence.Workload{ID: expected.Workload.ID, Parameters: cloneTestMap(expected.Workload.Parameters)},
		Faults:     append([]evidence.Fault{}, expected.Faults...), Status: evidence.StatusPassed,
		SourceCommit: strings.Repeat("a", 40), InputDigest: testInputDigest("a"),
		Environment: evidence.Environment{GoVersion: "go1.26.5", OS: "linux", Arch: "amd64", CPU: "unknown", LogicalCPUs: 8},
		Seed:        expected.Seed, Deadline: expected.Deadline, StartedAt: expected.Start,
		FinishedAt: expected.Start.Add(time.Second), EventsExecuted: expected.EventsExpected,
		Parameters:   cloneTestMap(expected.Workload.Parameters),
		Measurements: map[string]evidence.Measurement{"requests.total": {Unit: "requests", Value: 4}},
		Assertions:   []evidence.Assertion{{ID: "assertion-a", Passed: true, Message: "passed"}, {ID: "assertion-b", Passed: true, Message: "passed"}},
		Diagnostics:  []evidence.Diagnostic{}, Conclusion: "The evidence supports the hypothesis.",
		Limitations: append([]string{}, expected.Limitations...),
	}
	sealed, err := evidence.Seal(record)
	if err != nil {
		t.Fatalf("Seal fuzz fixture: %v", err)
	}
	return sealed
}
