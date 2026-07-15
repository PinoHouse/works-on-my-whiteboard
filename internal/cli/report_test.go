package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/release"
	reportpkg "github.com/PinoHouse/works-on-my-whiteboard/internal/report"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

func TestReportUsesDefaultsAndAuditsBeforeRendering(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	root := t.TempDir()
	operations := make([]string, 0)
	fixture.dependencies.computeState = func(context.Context, string) (inputdigest.State, error) {
		operations = append(operations, "state")
		return fixture.state, nil
	}
	fixture.dependencies.readBuildInfo = func() (*debug.BuildInfo, bool) {
		operations = append(operations, "build")
		return matchingBuildInfoForApp(fixture.state.SourceCommit), true
	}
	fixture.dependencies.loadCatalog = func(context.Context, string) (*catalog.Catalog, error) {
		operations = append(operations, "catalog")
		return fixture.repository, nil
	}
	fixture.dependencies.lookupExperiment = fixture.lookup(&operations)
	fixture.dependencies.loadAndSyncManifest = func(_ context.Context, evidenceRoot string, digest inputdigest.Digest) (release.Manifest, error) {
		operations = append(operations, "manifest")
		wantRoot, err := canonicalPolicyPath(filepath.Join(root, "evidence"))
		if err != nil {
			t.Fatal(err)
		}
		if evidenceRoot != wantRoot || digest != fixture.state.InputDigest {
			t.Fatalf("load manifest root=%q digest=%q; want %q/%q", evidenceRoot, digest, wantRoot, fixture.state.InputDigest)
		}
		return release.Manifest{InputDigest: digest, Profile: evidence.ProfileDeep}, nil
	}
	fixture.dependencies.openStoreReadOnly = func(string) (*evidence.Store, error) {
		operations = append(operations, "store")
		return nil, nil
	}
	fixture.dependencies.auditSnapshot = func(context.Context, release.Manifest, []release.ExpectedCell, *evidence.Store) (release.AuditedSnapshot, []validator.Diagnostic, error) {
		operations = append(operations, "audit")
		return release.AuditedSnapshot{}, []validator.Diagnostic{}, nil
	}
	fixture.dependencies.buildReport = func(*catalog.Catalog, validator.Report, release.AuditedSnapshot) (reportpkg.Model, error) {
		operations = append(operations, "model")
		return reportpkg.Model{
			InputDigest: string(fixture.state.InputDigest), Profile: evidence.ProfileDeep,
			Coverage: validator.Coverage{MissingCaseIDs: []string{}, UnexpectedCaseIDs: []string{}, Families: []validator.FamilyCoverage{}, RequiredPrinciples: []string{}, RequiredScenarioLabs: []string{}, RequiredPrimitiveLabs: []string{}, RequiredAdapters: []string{}},
			Rows:     []reportpkg.Row{}, Sources: []reportpkg.SourceLink{}, Diagnostics: []validator.Diagnostic{},
		}, nil
	}

	exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{"report", "--root", root, "--release", "current"})
	if exitCode != ExitSuccess || stderr != "" || !strings.HasPrefix(stdout, "# Evidence report\n") {
		t.Fatalf("report exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	joined := strings.Join(operations, ",")
	for _, sequence := range []string{"state,build,catalog", "lookup-lab-one", "manifest,store,audit,model"} {
		if !strings.Contains(joined, sequence) {
			t.Fatalf("operations=%q; missing ordered segment %q", joined, sequence)
		}
	}
}

func TestReportCheckIsExactReadOnlyAndMismatchIsExitThree(t *testing.T) {
	fixture, model := reportCommandFixture(t)
	var expected bytes.Buffer
	if err := reportpkg.WriteJSON(&expected, model); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(output, expected.Bytes(), 0o400); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	writes := 0
	fixture.dependencies.writeAtomic = func(string, []byte) error { writes++; return nil }
	exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
		"report", "--root", t.TempDir(), "--release", "current", "--format", "json", "--output", output, "--check",
	})
	if exitCode != ExitSuccess || stdout != "" || stderr != "" || writes != 0 {
		t.Fatalf("matching check exit=%d stdout=%q stderr=%q writes=%d", exitCode, stdout, stderr, writes)
	}
	after, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("check mutated output before=%v/%v after=%v/%v", before.Mode(), before.ModTime(), after.Mode(), after.ModTime())
	}
	if err := os.Chmod(output, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	staleTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(output, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}
	exitCode, stdout, stderr = runApplication(t, application{dependencies: fixture.dependencies}, []string{
		"report", "--root", t.TempDir(), "--release", "current", "--format", "json", "--output", output, "--check",
	})
	if exitCode != ExitDevelopmentFailure || stdout != "" || !strings.Contains(stderr, "report_check_mismatch") || writes != 0 {
		t.Fatalf("mismatch check exit=%d stdout=%q stderr=%q writes=%d", exitCode, stdout, stderr, writes)
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != "stale\n" {
		t.Fatalf("mismatch mutated output data=%q err=%v", data, err)
	}
}

func TestReportAuditDiagnosticsExitFourWithoutRendering(t *testing.T) {
	fixture, _ := reportCommandFixture(t)
	modelCalls := 0
	secret := "/absolute/workspace/secret\x1b[31m\nforged"
	fixture.dependencies.auditSnapshot = func(context.Context, release.Manifest, []release.ExpectedCell, *evidence.Store) (release.AuditedSnapshot, []validator.Diagnostic, error) {
		return release.AuditedSnapshot{}, []validator.Diagnostic{{
			Code: release.CodeReleaseEvidenceMissing, Severity: "fatal", Path: secret,
			EntityID: "run-20000101T000000.000Z-00000000000000000000000000000000", Message: secret,
		}}, nil
	}
	fixture.dependencies.buildReport = func(*catalog.Catalog, validator.Report, release.AuditedSnapshot) (reportpkg.Model, error) {
		modelCalls++
		return reportpkg.Model{}, nil
	}
	exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{"report", "--root", t.TempDir(), "--release", "current"})
	if exitCode != ExitReleaseFailure || stdout != "" || !strings.Contains(stderr, release.CodeReleaseEvidenceMissing) || modelCalls != 0 {
		t.Fatalf("audit failure exit=%d stdout=%q stderr=%q modelCalls=%d", exitCode, stdout, stderr, modelCalls)
	}
	if strings.Contains(stderr, "/absolute/workspace/secret") || strings.Contains(stderr, "run-20000101") || strings.ContainsAny(stderr, "\x1b\r") {
		t.Fatalf("audit diagnostics leaked untrusted fields: %q", stderr)
	}
}

func TestReportRenderedShortWriteForcesExitTwo(t *testing.T) {
	fixture, _ := reportCommandFixture(t)
	var stderr bytes.Buffer
	exitCode := application{dependencies: fixture.dependencies}.run(
		context.Background(),
		[]string{"report", "--root", t.TempDir(), "--release", "current"},
		shortWriter{},
		&stderr,
	)
	if exitCode != ExitArgumentOrLoadFailure {
		t.Fatalf("short report write exit=%d stderr=%q; want 2", exitCode, stderr.String())
	}
}

func TestReportClassifiesEvidenceStoreFailuresWithoutLeakingPaths(t *testing.T) {
	tests := []struct {
		name     string
		cause    error
		wantExit int
		wantCode string
	}{
		{name: "unsafe", cause: evidence.ErrEvidenceUnsafePath, wantExit: ExitReleaseFailure, wantCode: release.CodeReleaseEvidenceInvalid},
		{name: "corrupt", cause: evidence.ErrEvidenceCorrupt, wantExit: ExitReleaseFailure, wantCode: release.CodeReleaseEvidenceInvalid},
		{name: "too large", cause: evidence.ErrEvidenceTooLarge, wantExit: ExitReleaseFailure, wantCode: release.CodeReleaseEvidenceInvalid},
		{name: "missing", cause: evidence.ErrEvidenceNotFound, wantExit: ExitReleaseFailure, wantCode: release.CodeReleaseEvidenceMissing},
		{name: "missing plus unsafe", cause: errors.Join(evidence.ErrEvidenceNotFound, evidence.ErrEvidenceUnsafePath), wantExit: ExitReleaseFailure, wantCode: release.CodeReleaseEvidenceInvalid},
		{name: "io", cause: evidence.ErrEvidenceIO, wantExit: ExitArgumentOrLoadFailure, wantCode: "release_evidence_store_io"},
		{name: "semantic plus io", cause: errors.Join(evidence.ErrEvidenceUnsafePath, evidence.ErrEvidenceIO), wantExit: ExitArgumentOrLoadFailure, wantCode: "release_evidence_store_io"},
		{name: "semantic plus context", cause: errors.Join(evidence.ErrEvidenceCorrupt, context.Canceled), wantExit: ExitArgumentOrLoadFailure, wantCode: "release_evidence_store_io"},
		{name: "unclassified", cause: errors.New("opaque"), wantExit: ExitArgumentOrLoadFailure, wantCode: "release_evidence_store_io"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, _ := reportCommandFixture(t)
			fixture.dependencies.openStoreReadOnly = func(string) (*evidence.Store, error) {
				return nil, errors.Join(test.cause, errors.New("/absolute/workspace/secret"))
			}
			exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{"report", "--root", t.TempDir(), "--release", "current"})
			if exitCode != test.wantExit || stdout != "" || !strings.Contains(stderr, test.wantCode) {
				t.Fatalf("store failure exit=%d stdout=%q stderr=%q; want exit=%d code=%q", exitCode, stdout, stderr, test.wantExit, test.wantCode)
			}
			if strings.Contains(stderr, "/absolute/workspace/secret") {
				t.Fatalf("stderr leaked absolute path: %q", stderr)
			}
		})
	}
}

func TestReportTreatsOperationalManifestSentinelsAsHigherPrecedence(t *testing.T) {
	tests := []struct {
		name  string
		cause error
	}{
		{name: "unclassified", cause: errors.New("opaque")},
		{name: "semantic plus snapshot io", cause: errors.Join(release.ErrSnapshotCorrupt, release.ErrSnapshotIO)},
		{name: "semantic plus evidence io", cause: errors.Join(release.ErrSnapshotUnsafePath, evidence.ErrEvidenceIO)},
		{name: "semantic plus canceled", cause: errors.Join(release.ErrManifestInvalid, context.Canceled)},
		{name: "semantic plus deadline", cause: errors.Join(release.ErrManifestTooLarge, context.DeadlineExceeded)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, _ := reportCommandFixture(t)
			fixture.dependencies.loadAndSyncManifest = func(context.Context, string, inputdigest.Digest) (release.Manifest, error) {
				return release.Manifest{}, errors.Join(test.cause, errors.New("opaque failure at /absolute/workspace/secret"))
			}
			exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{"report", "--root", t.TempDir(), "--release", "current"})
			if exitCode != ExitArgumentOrLoadFailure || stdout != "" || !strings.Contains(stderr, "release_snapshot_io") {
				t.Fatalf("operational storage exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
			}
			if strings.Contains(stderr, "/absolute/workspace/secret") {
				t.Fatalf("stderr leaked absolute path: %q", stderr)
			}
		})
	}
}

func reportCommandFixture(t *testing.T) (runApplicationFixture, reportpkg.Model) {
	t.Helper()
	fixture := newRunApplicationFixture(t, 1)
	fixture.dependencies.loadAndSyncManifest = func(context.Context, string, inputdigest.Digest) (release.Manifest, error) {
		return release.Manifest{InputDigest: fixture.state.InputDigest, Profile: evidence.ProfileDeep}, nil
	}
	fixture.dependencies.openStoreReadOnly = func(string) (*evidence.Store, error) { return nil, nil }
	fixture.dependencies.auditSnapshot = func(context.Context, release.Manifest, []release.ExpectedCell, *evidence.Store) (release.AuditedSnapshot, []validator.Diagnostic, error) {
		return release.AuditedSnapshot{}, []validator.Diagnostic{}, nil
	}
	model := reportpkg.Model{
		InputDigest: string(fixture.state.InputDigest), Profile: evidence.ProfileDeep,
		Coverage: validator.Coverage{MissingCaseIDs: []string{}, UnexpectedCaseIDs: []string{}, Families: []validator.FamilyCoverage{}, RequiredPrinciples: []string{}, RequiredScenarioLabs: []string{}, RequiredPrimitiveLabs: []string{}, RequiredAdapters: []string{}},
		Rows:     []reportpkg.Row{}, Sources: []reportpkg.SourceLink{}, Diagnostics: []validator.Diagnostic{},
	}
	fixture.dependencies.buildReport = func(*catalog.Catalog, validator.Report, release.AuditedSnapshot) (reportpkg.Model, error) {
		return model, nil
	}
	return fixture, model
}
