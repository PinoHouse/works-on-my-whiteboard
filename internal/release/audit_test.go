package release

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

func TestAuditSnapshotReturnsAlignedBoundDeepCopy(t *testing.T) {
	ctx := context.Background()
	expected := []ExpectedCell{testExpectedCell()}
	record := testRecord(t, expected[0], testEvidenceID(1), testRunSetID(1), "a")
	manifest, err := Build(testInputDigest("a"), expected, []evidence.Record{record})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	store := newTestStore(t)
	if err := store.Put(ctx, record); err != nil {
		t.Fatalf("Put: %v", err)
	}

	snapshot, diagnostics, err := AuditSnapshot(ctx, manifest, expected, store)
	if err != nil {
		t.Fatalf("AuditSnapshot returned error: %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want empty", diagnostics)
	}
	if err := ValidateAuditedSnapshot(snapshot); err != nil {
		t.Fatalf("ValidateAuditedSnapshot returned error: %v", err)
	}
	if len(snapshot.Records) != 1 || snapshot.Records[0].ID != record.ID || len(snapshot.Expected) != 1 || snapshot.Manifest.Selections[0].EvidenceID != record.ID {
		t.Fatalf("snapshot is not aligned: %#v", snapshot)
	}

	manifest.Selections[0].EvidenceID = testEvidenceID(2)
	expected[0].Workload.Parameters["requests"] = 999
	record.Measurements["requests.total"] = evidence.Measurement{Unit: "requests", Value: 999}
	if snapshot.Manifest.Selections[0].EvidenceID == testEvidenceID(2) || snapshot.Expected[0].Workload.Parameters["requests"] == 999 || snapshot.Records[0].Measurements["requests.total"].Value == 999 {
		t.Fatal("snapshot aliases caller-owned inputs")
	}
}

func TestValidateAuditedSnapshotRejectsConsistentThreeWayMutation(t *testing.T) {
	snapshot := testAuditedSnapshot(t)
	snapshot.Manifest.Selections[0].LabID = "mutated-lab"
	snapshot.Expected[0].Cell.LabID = "mutated-lab"
	snapshot.Records[0].LabID = "mutated-lab"
	snapshot.Records[0].ContentDigest = ""
	sealed, err := evidence.Seal(snapshot.Records[0])
	if err != nil {
		t.Fatalf("Seal mutated record: %v", err)
	}
	snapshot.Records[0] = sealed
	snapshot.Manifest.Selections[0].ContentDigest = sealed.ContentDigest

	if err := ValidateAuditedSnapshot(snapshot); !errors.Is(err, ErrSnapshotUnbound) {
		t.Fatalf("ValidateAuditedSnapshot error = %v, want ErrSnapshotUnbound", err)
	}
}

func TestAuditSnapshotRejectsLegacyStoredUnsafeDynamicText(t *testing.T) {
	tests := []struct {
		name    string
		hostile string
		mutate  func(*evidence.Record)
	}{
		{name: "assertion", mutate: func(record *evidence.Record) {
			record.Assertions[0].Message = "host result: C:\\Users\\runner\\AppData\\Local\\Temp\\result.json"
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
		{name: "mixed malformed and encoded assertion", hostile: "%ZZ host root %2Fprivate%2Ftmp%2Fhost-only", mutate: func(record *evidence.Record) {
			record.Assertions[0].Message = "malformed=%ZZ host root %2Fprivate%2Ftmp%2Fhost-only"
		}},
		{name: "HTML entity assertion", hostile: "&#x2F;private", mutate: func(record *evidence.Record) {
			record.Assertions[0].Message = "host root &#x2F;private&#x2F;tmp&#x2F;host-only"
		}},
		{name: "HTML entity control diagnostic", hostile: "&#x1B;", mutate: func(record *evidence.Record) {
			record.Diagnostics = []evidence.Diagnostic{{Code: "observation", Message: "colored &#x1B;[31mred"}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			expected := []ExpectedCell{testExpectedCell()}
			safe := testRecord(t, expected[0], testEvidenceID(1), testRunSetID(1), "a")
			manifest, err := Build(testInputDigest("a"), expected, []evidence.Record{safe})
			if err != nil {
				t.Fatalf("Build safe manifest: %v", err)
			}
			legacy := safe
			test.mutate(&legacy)
			legacy.ContentDigest = ""
			legacy, err = evidence.Seal(legacy)
			if err != nil {
				t.Fatalf("Seal legacy-compatible record: %v", err)
			}
			manifest.Selections[0].ContentDigest = legacy.ContentDigest
			store := newTestStore(t)
			if err := store.Put(ctx, legacy); err != nil {
				t.Fatalf("Store.Put legacy record: %v", err)
			}

			snapshot, diagnostics, err := AuditSnapshot(ctx, manifest, expected, store)
			if err != nil {
				t.Fatalf("AuditSnapshot operational error: %v", err)
			}
			if !hasDiagnosticCode(diagnostics, CodeReleaseEvidenceInvalid) {
				t.Fatalf("diagnostics = %#v, want %s", diagnostics, CodeReleaseEvidenceInvalid)
			}
			for _, diagnostic := range diagnostics {
				if test.hostile != "" && strings.Contains(diagnostic.Message, test.hostile) {
					t.Fatalf("audit diagnostic echoed hostile dynamic text: %#v", diagnostic)
				}
			}
			if err := ValidateAuditedSnapshot(snapshot); !errors.Is(err, ErrSnapshotUnbound) {
				t.Fatalf("unsafe AuditSnapshot returned a usable snapshot: %v", err)
			}
		})
	}
}

func TestValidateAuditedSnapshotRejectsLegacyBoundUnsafeDynamicText(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AuditedSnapshot)
	}{
		{name: "conclusion", mutate: func(snapshot *AuditedSnapshot) {
			snapshot.Records[0].Conclusion = "result from /private/tmp/host-specific-output"
		}},
		{name: "limitation", mutate: func(snapshot *AuditedSnapshot) {
			snapshot.Expected[0].Limitations[0] = "host root /absolute/workspace/secret"
			snapshot.Records[0].Limitations[0] = snapshot.Expected[0].Limitations[0]
		}},
		{name: "diagnostic", mutate: func(snapshot *AuditedSnapshot) {
			snapshot.Records[0].Diagnostics = []evidence.Diagnostic{{Code: "observation", Message: "host root /absolute/workspace/secret"}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := testAuditedSnapshot(t)
			test.mutate(&snapshot)
			snapshot.Records[0].ContentDigest = ""
			sealed, err := evidence.Seal(snapshot.Records[0])
			if err != nil {
				t.Fatalf("Seal legacy-compatible record: %v", err)
			}
			snapshot.Records[0] = sealed
			snapshot.Manifest.Selections[0].ContentDigest = sealed.ContentDigest
			seal, err := calculateBindingSeal(snapshot.Manifest, snapshot.Expected, snapshot.Records)
			if err != nil {
				t.Fatalf("calculate legacy binding seal: %v", err)
			}
			snapshot.bindingSeal = seal

			if err := ValidateAuditedSnapshot(snapshot); !errors.Is(err, ErrSnapshotUnbound) || !errors.Is(err, ErrUnsafeDynamicText) {
				t.Fatalf("ValidateAuditedSnapshot error = %v, want ErrSnapshotUnbound and ErrUnsafeDynamicText", err)
			}
		})
	}
}

func TestAuditSnapshotReportsMissingSelectedEvidenceWithoutHistorySubstitution(t *testing.T) {
	ctx := context.Background()
	expected := []ExpectedCell{testExpectedCell()}
	selected := testRecord(t, expected[0], testEvidenceID(1), testRunSetID(1), "a")
	manifest, err := Build(testInputDigest("a"), expected, []evidence.Record{selected})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	store := newTestStore(t)
	historical := testRecord(t, expected[0], testEvidenceID(2), testRunSetID(2), "a")
	if err := store.Put(ctx, historical); err != nil {
		t.Fatalf("Put history: %v", err)
	}

	_, diagnostics, err := AuditSnapshot(ctx, manifest, expected, store)
	if err != nil {
		t.Fatalf("AuditSnapshot returned error: %v", err)
	}
	if !hasDiagnosticCode(diagnostics, CodeReleaseEvidenceMissing) {
		t.Fatalf("diagnostics = %#v, want %s", diagnostics, CodeReleaseEvidenceMissing)
	}
}

func TestAuditSnapshotClassifiesSelectedEvidenceStorageFailuresAsDiagnostics(t *testing.T) {
	tests := []struct {
		name     string
		wantCode string
		create   func(*testing.T, string)
	}{
		{
			name:     "missing",
			wantCode: CodeReleaseEvidenceMissing,
			create:   func(*testing.T, string) {},
		},
		{
			name:     "corrupt",
			wantCode: CodeReleaseEvidenceInvalid,
			create: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
					t.Fatalf("write corrupt selected record: %v", err)
				}
			},
		},
		{
			name:     "unsafe",
			wantCode: CodeReleaseEvidenceInvalid,
			create: func(t *testing.T, path string) {
				t.Helper()
				outside := filepath.Join(filepath.Dir(filepath.Dir(path)), "outside-record")
				if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
					t.Fatalf("write symlink target: %v", err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
		},
		{
			name:     "oversized",
			wantCode: CodeReleaseEvidenceInvalid,
			create: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, make([]byte, evidence.MaxRecordBytes+1), 0o644); err != nil {
					t.Fatalf("write oversized selected record: %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expected := []ExpectedCell{testExpectedCell()}
			selected := testRecord(t, expected[0], testEvidenceID(1), testRunSetID(1), "a")
			manifest, err := Build(testInputDigest("a"), expected, []evidence.Record{selected})
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			store, runs := newTestStoreWithRuns(t)
			test.create(t, filepath.Join(runs, selected.ID+".json"))

			_, diagnostics, auditErr := AuditSnapshot(context.Background(), manifest, expected, store)
			if auditErr != nil {
				t.Fatalf("AuditSnapshot operational error = %v, want nil", auditErr)
			}
			if len(diagnostics) == 0 {
				t.Fatalf("diagnostics = %#v, want %s", diagnostics, test.wantCode)
			}
			selectedDiagnostic := false
			for index, diagnostic := range diagnostics {
				if diagnostic.Code != test.wantCode {
					t.Fatalf("diagnostics = %#v, want only %s", diagnostics, test.wantCode)
				}
				if diagnostic.EntityID == selected.ID {
					selectedDiagnostic = true
				}
				if index > 0 && compareDiagnostic(diagnostics[index-1], diagnostic) > 0 {
					t.Fatalf("diagnostics are not sorted: %#v", diagnostics)
				}
			}
			if !selectedDiagnostic {
				t.Fatalf("diagnostics = %#v, want %s for selected record %s", diagnostics, test.wantCode, selected.ID)
			}
		})
	}
}

func TestEvidenceStorageClassificationPreservesOperationalIOPriority(t *testing.T) {
	injected := errors.New("injected identity verification I/O failure")
	tests := []struct {
		name     string
		classify func(error) (*validator.Diagnostic, error)
		semantic error
		wantCode string
	}{
		{
			name: "selected corrupt plus I/O",
			classify: func(err error) (*validator.Diagnostic, error) {
				return classifySelectedEvidenceError(testEvidenceID(1), err)
			},
			semantic: evidence.ErrEvidenceCorrupt,
			wantCode: CodeReleaseEvidenceInvalid,
		},
		{
			name: "selected unsafe plus I/O",
			classify: func(err error) (*validator.Diagnostic, error) {
				return classifySelectedEvidenceError(testEvidenceID(1), err)
			},
			semantic: evidence.ErrEvidenceUnsafePath,
			wantCode: CodeReleaseEvidenceInvalid,
		},
		{
			name:     "list corrupt plus I/O",
			classify: classifyListEvidenceError,
			semantic: evidence.ErrEvidenceCorrupt,
			wantCode: CodeReleaseEvidenceInvalid,
		},
		{
			name:     "list unsafe plus I/O",
			classify: classifyListEvidenceError,
			semantic: evidence.ErrEvidenceUnsafePath,
			wantCode: CodeReleaseEvidenceInvalid,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			diagnostic, operation := test.classify(errors.Join(test.semantic, evidence.ErrEvidenceIO, injected))
			if diagnostic == nil || diagnostic.Code != test.wantCode {
				t.Fatalf("diagnostic = %#v, want %s", diagnostic, test.wantCode)
			}
			if !errors.Is(operation, ErrSnapshotIO) || !errors.Is(operation, evidence.ErrEvidenceIO) || !errors.Is(operation, injected) {
				t.Fatalf("operation = %v, want ErrSnapshotIO preserving evidence I/O and cause", operation)
			}
		})
	}
}

func TestClassifySelectedEvidenceSemanticJoinPrefersUnsafeOverMissing(t *testing.T) {
	diagnostic, operation := classifySelectedEvidenceError(testEvidenceID(1), errors.Join(
		evidence.ErrEvidenceNotFound,
		evidence.ErrEvidenceUnsafePath,
	))
	if operation != nil {
		t.Fatalf("operation = %v, want nil semantic result", operation)
	}
	if diagnostic == nil || diagnostic.Code != CodeReleaseEvidenceInvalid {
		t.Fatalf("diagnostic = %#v, want %s", diagnostic, CodeReleaseEvidenceInvalid)
	}
}

func TestAuditSnapshotRejectsUnselectedSameRunSetRecordAndIgnoresOtherRunSet(t *testing.T) {
	ctx := context.Background()
	expected := []ExpectedCell{testExpectedCell()}
	selected := testRecord(t, expected[0], testEvidenceID(1), testRunSetID(1), "a")
	manifest, err := Build(testInputDigest("a"), expected, []evidence.Record{selected})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	t.Run("same run set", func(t *testing.T) {
		store := newTestStore(t)
		if err := store.Put(ctx, selected); err != nil {
			t.Fatalf("Put selected: %v", err)
		}
		extra := testRecord(t, expected[0], testEvidenceID(2), testRunSetID(1), "a")
		if err := store.Put(ctx, extra); err != nil {
			t.Fatalf("Put extra: %v", err)
		}
		_, diagnostics, auditErr := AuditSnapshot(ctx, manifest, expected, store)
		if auditErr != nil {
			t.Fatalf("AuditSnapshot returned error: %v", auditErr)
		}
		if !hasDiagnosticCode(diagnostics, CodeReleaseCellDuplicate) {
			t.Fatalf("diagnostics = %#v, want %s", diagnostics, CodeReleaseCellDuplicate)
		}
	})

	t.Run("other run set", func(t *testing.T) {
		store := newTestStore(t)
		if err := store.Put(ctx, selected); err != nil {
			t.Fatalf("Put selected: %v", err)
		}
		historical := testRecord(t, expected[0], testEvidenceID(2), testRunSetID(2), "b")
		if err := store.Put(ctx, historical); err != nil {
			t.Fatalf("Put history: %v", err)
		}
		snapshot, diagnostics, auditErr := AuditSnapshot(ctx, manifest, expected, store)
		if auditErr != nil || len(diagnostics) != 0 {
			t.Fatalf("AuditSnapshot = (%#v, %v), want clean", diagnostics, auditErr)
		}
		if err := ValidateAuditedSnapshot(snapshot); err != nil {
			t.Fatalf("ValidateAuditedSnapshot: %v", err)
		}
	})
}

func TestAuditSnapshotClassifiesRecordDrift(t *testing.T) {
	tests := []struct {
		name       string
		wantCode   string
		mutate     func(*evidence.Record)
		mutateMeta func(*Manifest)
	}{
		{name: "input digest", wantCode: CodeReleaseInputDigestMismatch, mutate: func(record *evidence.Record) { record.InputDigest = testInputDigest("b") }},
		{name: "run set", wantCode: CodeReleaseRunSetMismatch, mutate: func(record *evidence.Record) { record.RunSetID = testRunSetID(2) }},
		{name: "profile", wantCode: CodeReleaseProfileMismatch, mutate: func(record *evidence.Record) { record.Profile = evidence.ProfileSmoke }},
		{name: "status", wantCode: CodeReleaseStatusNotPassed, mutate: func(record *evidence.Record) {
			record.Status = evidence.StatusFailed
			record.Diagnostics = []evidence.Diagnostic{{Code: "run_failed", Message: "failed"}}
		}},
		{name: "assertion", wantCode: CodeReleaseAssertionMismatch, mutate: func(record *evidence.Record) {
			record.Status = evidence.StatusFailed
			record.Assertions[0].Passed = false
			record.Diagnostics = []evidence.Diagnostic{{Code: "assertion_failed", Message: "failed"}}
		}},
		{name: "events", wantCode: CodeReleaseDefinitionMismatch, mutate: func(record *evidence.Record) { record.EventsExecuted++ }},
		{name: "metric unit", wantCode: CodeReleaseDefinitionMismatch, mutate: func(record *evidence.Record) {
			record.Measurements["requests.total"] = evidence.Measurement{Unit: "bytes", Value: 4}
		}},
		{name: "content digest", wantCode: CodeReleaseContentDigestMismatch, mutate: func(record *evidence.Record) {}, mutateMeta: func(manifest *Manifest) {
			manifest.Selections[0].ContentDigest = string(testInputDigest("c"))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			expected := []ExpectedCell{testExpectedCell()}
			original := testRecord(t, expected[0], testEvidenceID(1), testRunSetID(1), "a")
			manifest, err := Build(testInputDigest("a"), expected, []evidence.Record{original})
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			mutated := original
			test.mutate(&mutated)
			mutated.ContentDigest = ""
			sealed, err := evidence.Seal(mutated)
			if err != nil {
				t.Fatalf("Seal mutated record: %v", err)
			}
			if test.mutateMeta == nil {
				manifest.Selections[0].ContentDigest = sealed.ContentDigest
			} else {
				test.mutateMeta(&manifest)
			}
			store := newTestStore(t)
			if err := store.Put(ctx, sealed); err != nil {
				t.Fatalf("Put: %v", err)
			}
			_, diagnostics, err := AuditSnapshot(ctx, manifest, expected, store)
			if err != nil {
				t.Fatalf("AuditSnapshot returned error: %v", err)
			}
			if !hasDiagnosticCode(diagnostics, test.wantCode) {
				t.Fatalf("diagnostics = %#v, want %s", diagnostics, test.wantCode)
			}
		})
	}
}

func TestAuditSnapshotDoesNotCompareRecordSourceCommitToCurrentHead(t *testing.T) {
	ctx := context.Background()
	expected := []ExpectedCell{testExpectedCell()}
	record := testRecord(t, expected[0], testEvidenceID(1), testRunSetID(1), "a")
	manifest, err := Build(testInputDigest("a"), expected, []evidence.Record{record})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	store := newTestStore(t)
	if err := store.Put(ctx, record); err != nil {
		t.Fatalf("Put: %v", err)
	}
	snapshot, diagnostics, err := AuditSnapshot(ctx, manifest, expected, store)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("AuditSnapshot = (%#v, %v), want evidence-only HEAD advance to remain legal", diagnostics, err)
	}
	if err := ValidateAuditedSnapshot(snapshot); err != nil {
		t.Fatalf("ValidateAuditedSnapshot: %v", err)
	}
}

func TestAuditSnapshotDiagnosticsAreSorted(t *testing.T) {
	expected := []ExpectedCell{testExpectedCell()}
	record := testRecord(t, expected[0], testEvidenceID(1), testRunSetID(1), "a")
	manifest, err := Build(testInputDigest("a"), expected, []evidence.Record{record})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	manifest.Profile = evidence.ProfileSmoke
	manifest.InputDigest = testInputDigest("c")
	store := newTestStore(t)
	if err := store.Put(context.Background(), record); err != nil {
		t.Fatalf("Put selected: %v", err)
	}

	_, diagnostics, err := AuditSnapshot(context.Background(), manifest, expected, store)
	if err != nil {
		t.Fatalf("AuditSnapshot returned error: %v", err)
	}
	if len(diagnostics) < 3 {
		t.Fatalf("diagnostics = %#v, want independent findings", diagnostics)
	}
	for index := 1; index < len(diagnostics); index++ {
		left := diagnostics[index-1]
		right := diagnostics[index]
		if compareDiagnostic(left, right) > 0 {
			t.Fatalf("diagnostics are not sorted: %#v", diagnostics)
		}
	}
}

func TestAuditSnapshotReturnsContextCancellationAsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := AuditSnapshot(ctx, Manifest{}, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("AuditSnapshot error = %v, want context.Canceled", err)
	}
}

func testAuditedSnapshot(t *testing.T) AuditedSnapshot {
	t.Helper()
	ctx := context.Background()
	expected := []ExpectedCell{testExpectedCell()}
	record := testRecord(t, expected[0], testEvidenceID(1), testRunSetID(1), "a")
	manifest, err := Build(testInputDigest("a"), expected, []evidence.Record{record})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	store := newTestStore(t)
	if err := store.Put(ctx, record); err != nil {
		t.Fatalf("Put: %v", err)
	}
	snapshot, diagnostics, err := AuditSnapshot(ctx, manifest, expected, store)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("AuditSnapshot = (%#v, %v)", diagnostics, err)
	}
	return snapshot
}

func newTestStore(t *testing.T) *evidence.Store {
	t.Helper()
	store, _ := newTestStoreWithRuns(t)
	return store
}

func newTestStoreWithRuns(t *testing.T) (*evidence.Store, string) {
	t.Helper()
	realRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks temporary root: %v", err)
	}
	store, err := evidence.NewStore(realRoot)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, filepath.Join(realRoot, "runs")
}

func hasDiagnosticCode(diagnostics []validator.Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}
