package report

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/content"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/release"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

func TestBuildCreatesDeterministicDeepCopiedAuthorityModel(t *testing.T) {
	catalogFixture, validation, snapshot := testBuildFixture(t)

	model, err := Build(catalogFixture, validation, snapshot)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if model.InputDigest != string(snapshot.Manifest.InputDigest) || model.Profile != evidence.ProfileDeep {
		t.Fatalf("model header = (%q, %q), want snapshot header", model.InputDigest, model.Profile)
	}
	if model.Coverage.BaselineTotal != 75 || model.Coverage.CompleteTotal != 1 || len(model.Coverage.MissingCaseIDs) != 74 {
		t.Fatalf("coverage = %#v, want 75/1/74", model.Coverage)
	}
	if len(model.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(model.Rows))
	}
	row := model.Rows[0]
	if !reflect.DeepEqual(row.Cell, validation.Matrix[0]) || row.EvidenceID != snapshot.Records[0].ID || row.Status != evidence.StatusPassed {
		t.Fatalf("row identity = %#v, want aligned selected record", row)
	}
	wantSources := []SourceLink{
		{ID: "source-lab", Title: "Lab source", URL: "https://example.com/lab"},
		{ID: "source-owner", Title: "Owner source", URL: "https://example.com/owner"},
		{ID: "source-shared", Title: "Shared source", URL: "https://example.com/shared"},
	}
	if !reflect.DeepEqual(model.Sources, wantSources) {
		t.Fatalf("sources = %#v, want deduplicated sorted %#v", model.Sources, wantSources)
	}
	if model.Diagnostics == nil || len(model.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want explicit empty slice", model.Diagnostics)
	}

	// All reference-bearing collections must be detached from every input.
	catalogFixture.Sources["source-lab"] = catalog.SourceRecord{ID: "source-lab", Title: "mutated"}
	validation.Coverage.MissingCaseIDs[0] = "mutated"
	validation.Matrix[0].Faults[0] = "mutated"
	snapshot.Records[0].Workload.Parameters["requests"] = 999
	snapshot.Records[0].Measurements["requests.total"] = evidence.Measurement{Unit: "requests", Value: 999}
	snapshot.Records[0].Assertions[0].Message = "mutated"
	snapshot.Records[0].Limitations[0] = "mutated"
	if model.Sources[0].Title != "Lab source" || model.Coverage.MissingCaseIDs[0] == "mutated" || model.Rows[0].Cell.Faults[0] == "mutated" || model.Rows[0].Workload.Parameters["requests"] == 999 || model.Rows[0].Measurements["requests.total"].Value == 999 || model.Rows[0].Assertions[0].Message == "mutated" || model.Rows[0].Limitations[0] == "mutated" {
		t.Fatal("model aliases caller-owned catalog, validation, or snapshot data")
	}
}

func TestBuildRejectsUnboundOrMutatedSnapshots(t *testing.T) {
	catalogFixture, validation, snapshot := testBuildFixture(t)

	if _, err := Build(catalogFixture, validation, release.AuditedSnapshot{}); !errors.Is(err, release.ErrSnapshotUnbound) {
		t.Fatalf("unbound Build error = %v, want ErrSnapshotUnbound", err)
	}

	snapshot.Manifest.Selections[0].LabID = "mutated-lab"
	snapshot.Expected[0].Cell.LabID = "mutated-lab"
	snapshot.Records[0].LabID = "mutated-lab"
	if _, err := Build(catalogFixture, validation, snapshot); !errors.Is(err, release.ErrSnapshotUnbound) {
		t.Fatalf("mutated Build error = %v, want ErrSnapshotUnbound", err)
	}
}

func TestBuildRejectsValidationMatrixDrift(t *testing.T) {
	catalogFixture, validation, snapshot := testBuildFixture(t)
	validation.Matrix[0].AdapterID = "adapter-drift"
	if _, err := Build(catalogFixture, validation, snapshot); !errors.Is(err, ErrModelInvalid) {
		t.Fatalf("Build error = %v, want ErrModelInvalid", err)
	}
}

func TestBuildRejectsForgedValidationCoverage(t *testing.T) {
	catalogFixture, validation, snapshot := testBuildFixture(t)
	validation.Coverage.BaselineTotal = 750
	validation.Coverage.CompleteTotal = 750
	validation.Coverage.MissingCaseIDs = []string{}
	if _, err := Build(catalogFixture, validation, snapshot); !errors.Is(err, ErrModelInvalid) {
		t.Fatalf("Build error = %v, want ErrModelInvalid for forged coverage", err)
	}
}

func TestBuildPreservesSortedClosedDiagnostics(t *testing.T) {
	catalogFixture, validation, snapshot := testBuildFixture(t)
	validation.Diagnostics = []validator.Diagnostic{
		{Code: "missing_content_file", Severity: "error", Path: "z.md", EntityID: "case-z", Message: "missing content"},
		{Code: "invalid_stable_id", Severity: "error", EntityID: "case-a", Message: "invalid ID"},
	}
	model, err := Build(catalogFixture, validation, snapshot)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if got := []string{model.Diagnostics[0].Code, model.Diagnostics[1].Code}; !reflect.DeepEqual(got, []string{"invalid_stable_id", "missing_content_file"}) {
		t.Fatalf("diagnostic order = %#v", got)
	}

	validation.Diagnostics[0].Severity = "warning"
	if _, err := Build(catalogFixture, validation, snapshot); !errors.Is(err, ErrModelInvalid) {
		t.Fatalf("Build error = %v, want ErrModelInvalid for open severity", err)
	}
}

func TestValidateDiagnosticsUsesValidatorAndContentCodeClosure(t *testing.T) {
	known := []string{
		validator.CodeInvalidStableID,
		validator.CodeDuplicateScopeFamily,
		validator.CodeDuplicateScopeCase,
		validator.CodeCaseOutsideScope,
		validator.CodeUnknownFamily,
		validator.CodeUnknownReference,
		validator.CodeDuplicateClaimID,
		validator.CodeInvalidSourceURL,
		validator.CodeInvalidSourceDate,
		validator.CodeDanglingSource,
		validator.CodeCompleteContractEmpty,
		validator.CodeMissingCaseBinding,
		validator.CodeMissingPrincipleBinding,
		validator.CodeDuplicateBindingID,
		validator.CodeDuplicateRequiredRun,
		validator.CodeInvalidRunBinding,
		validator.CodeForeignClaim,
		validator.CodeUnusedBinding,
		validator.CodeBindingWorkloadMismatch,
		validator.CodeUnknownImplementation,
		validator.CodeMissingRequiredPrincipleLab,
		validator.CodeMissingRequiredAdapter,
		validator.CodeDependencyIncomplete,
		validator.CodeOrphanedLab,
		validator.CodeStatusVocabularyMismatch,
		validator.CodeAliasCycle,
		validator.CodeReleaseScopeIncomplete,
		validator.CodeReleaseFamilyMismatch,
		content.CodeHeadingContractMismatch,
		content.CodeEmptySectionBody,
		content.CodeSectionTooShort,
		content.CodeUnfinishedMarker,
		content.CodeInvalidClaimMarker,
		content.CodeUnknownClaim,
		content.CodeMissingClaimMarker,
		content.CodeConflictingClaimClass,
		content.CodeAssumptionContextMissing,
		content.CodeMeasuredClaimUnbound,
		content.CodeSourcedClaimInvalid,
		content.CodeMissingContentFile,
		content.CodeInvalidLinkTarget,
		content.CodeMissingLinkTarget,
		content.CodeMissingHeadingFragment,
		content.CodeInvalidUTF8,
		content.CodeContentReadFailure,
	}
	seen := make(map[string]struct{}, len(known))
	for _, code := range known {
		if _, duplicate := seen[code]; duplicate {
			t.Fatalf("known diagnostic code %q is duplicated in the closure", code)
		}
		seen[code] = struct{}{}
		if err := validateDiagnostics([]validator.Diagnostic{{Code: code, Severity: "error", Message: "closed diagnostic"}}); err != nil {
			t.Fatalf("known diagnostic code %q rejected: %v", code, err)
		}
	}
	if err := validateDiagnostics([]validator.Diagnostic{{Code: "totally_fabricated", Severity: "error", Message: "must fail closed"}}); !errors.Is(err, ErrModelInvalid) {
		t.Fatalf("unknown diagnostic error = %v, want ErrModelInvalid", err)
	}
}

func TestBuildResolvesBindingOwnerAndSourceAliases(t *testing.T) {
	catalogFixture, validation, snapshot := testBuildFixture(t)
	aliases, err := catalog.NewAliasSet([]catalog.Alias{
		{Kind: catalog.EntityKindCase, From: "case-old", To: "case-a"},
		{Kind: catalog.EntityKindSource, From: "source-owner-old", To: "source-owner"},
		{Kind: catalog.EntityKindSource, From: "source-lab-old", To: "source-lab"},
		{Kind: catalog.EntityKindSource, From: "source-shared-old", To: "source-shared"},
	})
	if err != nil {
		t.Fatalf("NewAliasSet: %v", err)
	}
	catalogFixture.Aliases = aliases
	owner := catalogFixture.Cases["case-a"]
	owner.Sources = []string{"source-owner-old", "source-shared-old"}
	catalogFixture.Cases[owner.ID] = owner
	lab := catalogFixture.Labs["lab-a"]
	lab.CaseBindings[0].CaseID = "case-old"
	lab.Sources = []string{"source-shared-old", "source-lab-old"}
	catalogFixture.Labs[lab.ID] = lab

	model, err := Build(catalogFixture, validation, snapshot)
	if err != nil {
		t.Fatalf("Build returned error for valid aliases: %v", err)
	}
	want := []SourceLink{
		{ID: "source-lab", Title: "Lab source", URL: "https://example.com/lab"},
		{ID: "source-owner", Title: "Owner source", URL: "https://example.com/owner"},
		{ID: "source-shared", Title: "Shared source", URL: "https://example.com/shared"},
	}
	if !reflect.DeepEqual(model.Sources, want) {
		t.Fatalf("sources = %#v, want canonical aliases %#v", model.Sources, want)
	}
}

func TestBuildRejectsInvalidAliasTopology(t *testing.T) {
	tests := []struct {
		name      string
		alias     catalog.Alias
		principle bool
		want      string
	}{
		{name: "missing terminal", alias: catalog.Alias{Kind: catalog.EntityKindCase, From: "case-old", To: "case-missing"}, want: "missing terminal"},
		{name: "cross kind terminal", alias: catalog.Alias{Kind: catalog.EntityKindCase, From: "case-old", To: "principle-a"}, principle: true, want: "exists as principle"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalogFixture, validation, snapshot := testBuildFixture(t)
			if test.principle {
				catalogFixture.Principles["principle-a"] = catalog.PrincipleManifest{ID: "principle-a", Status: catalog.LifecycleStatusDraft}
			}
			aliases, err := catalog.NewAliasSet([]catalog.Alias{test.alias})
			if err != nil {
				t.Fatalf("NewAliasSet: %v", err)
			}
			catalogFixture.Aliases = aliases
			buildErr := func() error {
				_, err := Build(catalogFixture, validation, snapshot)
				return err
			}()
			if !errors.Is(buildErr, ErrModelInvalid) || !strings.Contains(buildErr.Error(), test.want) {
				t.Fatalf("Build error = %v, want ErrModelInvalid containing %q", buildErr, test.want)
			}
		})
	}

	if _, err := catalog.NewAliasSet([]catalog.Alias{
		{Kind: catalog.EntityKindSource, From: "source-a", To: "source-b"},
		{Kind: catalog.EntityKindSource, From: "source-b", To: "source-a"},
	}); err == nil || !strings.Contains(err.Error(), "alias cycle") {
		t.Fatalf("cyclic AliasSet error = %v, want alias cycle before Catalog construction", err)
	}
}

func TestBuildRejectsHTTPSURLWithoutHostname(t *testing.T) {
	catalogFixture, validation, snapshot := testBuildFixture(t)
	source := catalogFixture.Sources["source-lab"]
	source.URL = "https://:443/path"
	catalogFixture.Sources[source.ID] = source
	if _, err := Build(catalogFixture, validation, snapshot); !errors.Is(err, ErrModelInvalid) {
		t.Fatalf("Build error = %v, want ErrModelInvalid", err)
	}
}

func TestBuildRejectsRendererInvalidSourceTextAndURL(t *testing.T) {
	tests := []struct {
		name    string
		hostile string
		mutate  func(*catalog.SourceRecord)
	}{
		{name: "attempt identity in title", hostile: "attempt_run-20260714T010203.004Z-00000000000000000000000000000009", mutate: func(source *catalog.SourceRecord) {
			source.Title = "attempt_run-20260714T010203.004Z-00000000000000000000000000000009"
		}},
		{name: "raw Markdown terminator in URL", hostile: "https://example.com/>forged", mutate: func(source *catalog.SourceRecord) {
			source.URL = "https://example.com/>forged"
		}},
		{name: "raw whitespace in URL", hostile: "https://example.com/path with-space", mutate: func(source *catalog.SourceRecord) {
			source.URL = "https://example.com/path with-space"
		}},
		{name: "raw table separator in URL", hostile: "https://example.com/a|b", mutate: func(source *catalog.SourceRecord) {
			source.URL = "https://example.com/a|b"
		}},
		{name: "raw backslash in URL", hostile: `https://example.com/a\b`, mutate: func(source *catalog.SourceRecord) {
			source.URL = `https://example.com/a\b`
		}},
		{name: "Unicode format title", hostile: "\u202e", mutate: func(source *catalog.SourceRecord) {
			source.Title = "Source \u202eoverride"
		}},
		{name: "Unicode format URL", hostile: "\u200b", mutate: func(source *catalog.SourceRecord) {
			source.URL = "https://example.com/a\u200bhidden"
		}},
		{name: "HTML entity format title", hostile: "&#x202E;", mutate: func(source *catalog.SourceRecord) {
			source.Title = "Source &#x202E;override"
		}},
		{name: "mixed malformed and encoded title", hostile: "%ZZ", mutate: func(source *catalog.SourceRecord) {
			source.Title = "malformed=%ZZ host root %2Fprivate%2Ftmp%2Fsource"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalogFixture, validation, snapshot := testBuildFixture(t)
			source := catalogFixture.Sources["source-lab"]
			test.mutate(&source)
			catalogFixture.Sources[source.ID] = source
			model, err := Build(catalogFixture, validation, snapshot)
			if !errors.Is(err, ErrModelInvalid) {
				t.Fatalf("Build error = %v, want ErrModelInvalid", err)
			}
			if !reflect.DeepEqual(model, Model{}) {
				t.Fatalf("Build returned partial model: %#v", model)
			}
			if strings.Contains(err.Error(), test.hostile) {
				t.Fatalf("Build error echoed hostile source text: %v", err)
			}
		})
	}
}

func TestValidHTTPSURLRejectsRawMarkdownAndWhitespace(t *testing.T) {
	for _, value := range []string{
		"https://example.com/<forged",
		"https://example.com/>forged",
		"https://example.com/with space",
		"https://example.com/with\nnewline",
		"https://example.com/a|b",
		`https://example.com/a\b`,
		"https://example.com/a\u202ehidden",
	} {
		if validHTTPSURL(value) {
			t.Fatalf("validHTTPSURL(%q) = true, want false", value)
		}
	}
	for _, value := range []string{"https://example.com/safe?q=value", "https://example.com/a%7Cb", "https://example.com/a%5Cb"} {
		if !validHTTPSURL(value) {
			t.Fatalf("validHTTPSURL(%q) = false, want true", value)
		}
	}
}

type testRecordOptions struct {
	id               string
	runSetID         evidence.RunSetID
	sourceCommit     string
	finishedAt       time.Time
	metricValue      int64
	assertionMessage string
	environment      evidence.Environment
	conclusion       string
	limitations      []string
	mutateCell       func(*validator.MatrixCell)
	start            time.Time
	measurementUnit  string
}

func testBuildFixture(t *testing.T) (*catalog.Catalog, validator.Report, release.AuditedSnapshot) {
	t.Helper()
	_, _, single := testReportFixture(t, testRecordOptions{})
	firstExpected := cloneTestExpected(single.Expected[0])
	secondExpected := cloneTestExpected(single.Expected[0])
	secondExpected.Cell.Role = string(evidence.RoleVariant)
	secondExpected.Cell.ImplementationID = "implementation-b"
	firstRecord := single.Records[0]
	secondRecord := testReportRecord(t, secondExpected, testRecordOptions{
		id:       "run-20260714T010203.004Z-00000000000000000000000000000002",
		runSetID: firstRecord.RunSetID,
	})
	expected := []release.ExpectedCell{firstExpected, secondExpected}
	records := []evidence.Record{firstRecord, secondRecord}
	manifest, err := release.Build(testInputDigest(), expected, records)
	if err != nil {
		t.Fatalf("release.Build report fixture: %v", err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks report fixture: %v", err)
	}
	store, err := evidence.NewStore(root)
	if err != nil {
		t.Fatalf("NewStore report fixture: %v", err)
	}
	for _, record := range records {
		if err := store.Put(context.Background(), record); err != nil {
			t.Fatalf("Store.Put report fixture: %v", err)
		}
	}
	snapshot, diagnostics, err := release.AuditSnapshot(context.Background(), manifest, expected, store)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("AuditSnapshot report fixture = (%#v, %v)", diagnostics, err)
	}

	missing := testMissingCaseIDs()
	scopeCases := make([]catalog.ScopeCase, 0, 75)
	scopeCases = append(scopeCases, catalog.ScopeCase{ID: "case-a", Title: "Case A", PrimaryFamily: "coordination"})
	for _, id := range missing {
		scopeCases = append(scopeCases, catalog.ScopeCase{ID: id, Title: id, PrimaryFamily: "coordination"})
	}
	catalogFixture := &catalog.Catalog{
		Scope: catalog.Scope{
			Families: []catalog.ScopeFamily{{ID: "coordination", Title: "Coordination"}},
			Cases:    scopeCases,
		},
		Sources: map[string]catalog.SourceRecord{
			"source-owner":  {ID: "source-owner", Title: "Owner source", URL: "https://example.com/owner"},
			"source-lab":    {ID: "source-lab", Title: "Lab source", URL: "https://example.com/lab"},
			"source-shared": {ID: "source-shared", Title: "Shared source", URL: "https://example.com/shared"},
		},
		Cases: map[string]catalog.CaseManifest{
			"case-a": {
				ID:            "case-a",
				PrimaryFamily: "coordination",
				Status:        catalog.LifecycleStatusComplete,
				Principles:    []string{"principle-a"},
				Claims:        []catalog.Claim{{ID: "claim-a", Statement: "A falsifiable claim."}},
				Labs:          []string{"lab-a"},
				Sources:       []string{"source-owner", "source-shared"},
			},
		},
		Principles: map[string]catalog.PrincipleManifest{},
		Labs: map[string]catalog.LabManifest{
			"lab-a": {
				ID:              "lab-a",
				Kind:            catalog.LabKindScenario,
				Status:          catalog.LifecycleStatusComplete,
				Implementations: []string{"implementation-a", "implementation-b"},
				CaseBindings: []catalog.CaseBinding{{
					ID: "binding-a", CaseID: "case-a", Claim: "claim-a", Workload: "workload-a", Assertions: []string{"assert-a"},
				}},
				RequiredRuns: []catalog.RequiredRun{{
					ID: "run-a", Binding: "binding-a", Baseline: "implementation-a", Variants: []string{"implementation-b"}, Workload: "workload-a", Faults: []string{"fault-a"},
				}},
				Metrics: []string{"latency.millis", "requests.total"},
				Sources: []string{"source-shared", "source-lab"},
			},
		},
		Adapters: map[string]catalog.AdapterManifest{},
	}
	matrix, matrixDiagnostics := validator.BuildRequiredMatrix(catalogFixture)
	if len(matrixDiagnostics) != 0 {
		t.Fatalf("BuildRequiredMatrix fixture diagnostics = %#v", matrixDiagnostics)
	}
	validation := validator.Report{
		Diagnostics: []validator.Diagnostic{},
		Coverage:    validator.ComputeCoverage(catalogFixture),
		Matrix:      matrix,
	}
	return catalogFixture, validation, snapshot
}

func cloneTestExpected(source release.ExpectedCell) release.ExpectedCell {
	cloned := source
	cloned.Cell = cloneTestCell(source.Cell)
	cloned.Workload.Parameters = cloneTestMap(source.Workload.Parameters)
	cloned.Faults = append([]evidence.Fault{}, source.Faults...)
	cloned.Limitations = append([]string{}, source.Limitations...)
	cloned.Measurements = append([]evidence.MeasurementSpec{}, source.Measurements...)
	return cloned
}

func testReportFixture(t *testing.T, options testRecordOptions) (*catalog.Catalog, validator.Report, release.AuditedSnapshot) {
	t.Helper()
	cell := validator.MatrixCell{
		LabID:            "lab-a",
		RequiredRunID:    "run-a",
		BindingID:        "binding-a",
		ClaimID:          "claim-a",
		Role:             string(evidence.RoleBaseline),
		ImplementationID: "implementation-a",
		AdapterID:        "",
		Workload:         "workload-a",
		Faults:           []string{"fault-a"},
		AssertionIDs:     []string{"assert-a"},
	}
	if options.mutateCell != nil {
		options.mutateCell(&cell)
	}
	limitations := []string{"Synthetic workload only."}
	if options.limitations != nil {
		limitations = append([]string{}, options.limitations...)
	}
	start := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if !options.start.IsZero() {
		start = options.start
	}
	measurementUnit := "requests"
	if options.measurementUnit != "" {
		measurementUnit = options.measurementUnit
	}
	expected := release.ExpectedCell{
		Cell:           cloneTestCell(cell),
		Profile:        evidence.ProfileDeep,
		Start:          start,
		Workload:       evidence.Workload{ID: "workload-a", Parameters: map[string]int64{"requests": 10}},
		Faults:         []evidence.Fault{{ID: "fault-a", At: time.Nanosecond, Duration: 2 * time.Nanosecond}},
		Seed:           7,
		Deadline:       10 * time.Second,
		Hypothesis:     "The limiter preserves its bound.",
		Limitations:    limitations,
		Measurements:   []evidence.MeasurementSpec{{ID: "latency.millis", Unit: "ms"}, {ID: "requests.total", Unit: measurementUnit}},
		EventsExpected: 10,
	}
	record := testReportRecord(t, expected, options)
	manifest, err := release.Build(testInputDigest(), []release.ExpectedCell{expected}, []evidence.Record{record})
	if err != nil {
		t.Fatalf("release.Build fixture: %v", err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	store, err := evidence.NewStore(root)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Put(context.Background(), record); err != nil {
		t.Fatalf("Store.Put: %v", err)
	}
	snapshot, diagnostics, err := release.AuditSnapshot(context.Background(), manifest, []release.ExpectedCell{expected}, store)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("AuditSnapshot = (%#v, %v), want clean", diagnostics, err)
	}

	missing := testMissingCaseIDs()
	validation := validator.Report{
		Diagnostics: []validator.Diagnostic{},
		Coverage: validator.Coverage{
			BaselineTotal:         75,
			CompleteTotal:         1,
			MissingCaseIDs:        missing,
			UnexpectedCaseIDs:     []string{},
			Families:              []validator.FamilyCoverage{{ID: "coordination", Required: 75, Complete: 1}},
			RequiredPrinciples:    []string{"principle-a"},
			RequiredScenarioLabs:  []string{"lab-a"},
			RequiredPrimitiveLabs: []string{},
			RequiredAdapters:      []string{},
		},
		Matrix: []validator.MatrixCell{cloneTestCell(cell)},
	}
	catalogFixture := &catalog.Catalog{
		Sources: map[string]catalog.SourceRecord{
			"source-owner":  {ID: "source-owner", Title: "Owner source", URL: "https://example.com/owner"},
			"source-lab":    {ID: "source-lab", Title: "Lab source", URL: "https://example.com/lab"},
			"source-shared": {ID: "source-shared", Title: "Shared source", URL: "https://example.com/shared"},
		},
		Cases: map[string]catalog.CaseManifest{
			"case-a": {ID: "case-a", Claims: []catalog.Claim{{ID: "claim-a"}}, Sources: []string{"source-owner", "source-shared"}},
		},
		Principles: map[string]catalog.PrincipleManifest{},
		Labs: map[string]catalog.LabManifest{
			"lab-a": {
				ID:           "lab-a",
				Kind:         catalog.LabKindScenario,
				CaseBindings: []catalog.CaseBinding{{ID: "binding-a", CaseID: "case-a", Claim: "claim-a"}},
				Sources:      []string{"source-shared", "source-lab"},
			},
		},
		Adapters: map[string]catalog.AdapterManifest{},
	}
	return catalogFixture, validation, snapshot
}

func testMissingCaseIDs() []string {
	missing := make([]string, 74)
	for index := range missing {
		missing[index] = "missing-case-" + leftPad(index+1, 2)
	}
	return missing
}

func testReportRecord(t *testing.T, expected release.ExpectedCell, options testRecordOptions) evidence.Record {
	t.Helper()
	if options.id == "" {
		options.id = "run-20260714T010203.004Z-00000000000000000000000000000001"
	}
	if options.runSetID == "" {
		options.runSetID = "set-20260714T010203.004Z-00000000000000000000000000000001"
	}
	if options.sourceCommit == "" {
		options.sourceCommit = strings.Repeat("a", 40)
	}
	if options.finishedAt.IsZero() {
		options.finishedAt = expected.Start.Add(time.Second)
	}
	if options.assertionMessage == "" {
		options.assertionMessage = "bound held"
	}
	if options.environment.GoVersion == "" {
		options.environment = evidence.Environment{GoVersion: "go1.26.5", OS: "linux", Arch: "amd64", CPU: "unknown", LogicalCPUs: 8}
	}
	if options.conclusion == "" {
		options.conclusion = "The evidence supports the hypothesis."
	}
	if options.limitations == nil {
		options.limitations = append([]string{}, expected.Limitations...)
	}
	record := evidence.Record{
		SchemaVersion:    evidence.SchemaVersion,
		ID:               options.id,
		RunSetID:         options.runSetID,
		LabID:            expected.Cell.LabID,
		RequiredRunID:    expected.Cell.RequiredRunID,
		BindingID:        expected.Cell.BindingID,
		ClaimID:          expected.Cell.ClaimID,
		Role:             evidence.Role(expected.Cell.Role),
		ImplementationID: expected.Cell.ImplementationID,
		AdapterID:        expected.Cell.AdapterID,
		Profile:          expected.Profile,
		Hypothesis:       expected.Hypothesis,
		Workload:         evidence.Workload{ID: expected.Workload.ID, Parameters: cloneTestMap(expected.Workload.Parameters)},
		Faults:           append([]evidence.Fault{}, expected.Faults...),
		Status:           evidence.StatusPassed,
		SourceCommit:     options.sourceCommit,
		InputDigest:      testInputDigest(),
		Environment:      options.environment,
		Seed:             expected.Seed,
		Deadline:         expected.Deadline,
		StartedAt:        expected.Start,
		FinishedAt:       options.finishedAt,
		EventsExecuted:   expected.EventsExpected,
		Parameters:       cloneTestMap(expected.Workload.Parameters),
		Measurements: map[string]evidence.Measurement{
			"latency.millis": {Unit: "ms", Value: 5},
			"requests.total": {Unit: expected.Measurements[1].Unit, Value: options.metricValue},
		},
		Assertions:  []evidence.Assertion{{ID: "assert-a", Passed: true, Message: options.assertionMessage}},
		Diagnostics: []evidence.Diagnostic{},
		Conclusion:  options.conclusion,
		Limitations: append([]string{}, options.limitations...),
	}
	sealed, err := evidence.Seal(record)
	if err != nil {
		t.Fatalf("Seal fixture: %v", err)
	}
	return sealed
}

func testInputDigest() inputdigest.Digest {
	return inputdigest.Digest("sha256:" + strings.Repeat("a", 64))
}

func cloneTestCell(source validator.MatrixCell) validator.MatrixCell {
	cloned := source
	cloned.Faults = append([]string{}, source.Faults...)
	cloned.AssertionIDs = append([]string{}, source.AssertionIDs...)
	return cloned
}

func cloneTestMap(source map[string]int64) map[string]int64 {
	cloned := make(map[string]int64, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func leftPad(value, width int) string {
	text := strings.Repeat("0", width) + string(rune('0'+value%10))
	if value >= 10 {
		text = strings.Repeat("0", width) + string(rune('0'+(value/10)%10)) + string(rune('0'+value%10))
	}
	return text[len(text)-width:]
}
