package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/experiments"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/release"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
	"github.com/PinoHouse/works-on-my-whiteboard/labs/harness"
)

func TestRunRejectsRepositorySmokeSnapshotThroughSymlinkBeforeSideEffects(t *testing.T) {
	root := t.TempDir()
	repositoryEvidence := filepath.Join(root, "evidence")
	if err := os.Mkdir(repositoryEvidence, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(repositoryEvidence, alias); err != nil {
		t.Fatal(err)
	}
	state := runTestState("a", "b")
	downstreamCalls := 0
	app := application{dependencies: applicationDependencies{
		computeState:  func(context.Context, string) (inputdigest.State, error) { return state, nil },
		readBuildInfo: func() (*debug.BuildInfo, bool) { return matchingBuildInfoForApp(state.SourceCommit), true },
		loadCatalog: func(context.Context, string) (*catalog.Catalog, error) {
			downstreamCalls++
			return nil, errors.New("must not load")
		},
		newRunSetID: func(time.Time) (evidence.RunSetID, error) {
			downstreamCalls++
			return "", errors.New("must not allocate")
		},
		newStore: func(string) (*evidence.Store, error) {
			downstreamCalls++
			return nil, errors.New("must not create")
		},
		runExperiment: func(context.Context, harness.RunSpec) (harness.RunResult, error) {
			downstreamCalls++
			return harness.RunResult{}, errors.New("must not run")
		},
	}}
	exitCode, stdout, stderr := runApplication(t, app, []string{
		"run", "--required", "--profile", "smoke", "--snapshot", "--root", root, "--evidence-root", alias,
	})
	if exitCode != ExitArgumentOrLoadFailure || stdout != "" || !strings.Contains(stderr, "smoke_snapshot_repository_root") {
		t.Fatalf("smoke snapshot exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if downstreamCalls != 0 {
		t.Fatalf("downstream calls=%d; want zero", downstreamCalls)
	}
}

func TestRunRejectsRepositorySmokeSnapshotThroughCaseAliasBeforeSideEffects(t *testing.T) {
	root := t.TempDir()
	repositoryEvidence := filepath.Join(root, "evidence")
	if err := os.Mkdir(repositoryEvidence, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "EVIDENCE")
	repositoryInfo, err := os.Stat(repositoryEvidence)
	if err != nil {
		t.Fatal(err)
	}
	aliasInfo, err := os.Stat(alias)
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("filesystem is case-sensitive")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(repositoryInfo, aliasInfo) {
		t.Skip("case variant is a distinct filesystem object")
	}

	state := runTestState("a", "b")
	downstreamCalls := 0
	app := application{dependencies: applicationDependencies{
		computeState:  func(context.Context, string) (inputdigest.State, error) { return state, nil },
		readBuildInfo: func() (*debug.BuildInfo, bool) { return matchingBuildInfoForApp(state.SourceCommit), true },
		loadCatalog: func(context.Context, string) (*catalog.Catalog, error) {
			downstreamCalls++
			return nil, errors.New("must not load")
		},
		newRunSetID: func(time.Time) (evidence.RunSetID, error) {
			downstreamCalls++
			return "", errors.New("must not allocate")
		},
		newStore: func(string) (*evidence.Store, error) {
			downstreamCalls++
			return nil, errors.New("must not create")
		},
		runExperiment: func(context.Context, harness.RunSpec) (harness.RunResult, error) {
			downstreamCalls++
			return harness.RunResult{}, errors.New("must not run")
		},
	}}
	exitCode, stdout, stderr := runApplication(t, app, []string{
		"run", "--required", "--profile", "smoke", "--snapshot", "--root", root, "--evidence-root", alias,
	})
	if exitCode != ExitArgumentOrLoadFailure || stdout != "" || !strings.Contains(stderr, "smoke_snapshot_repository_root") {
		t.Fatalf("smoke snapshot exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if downstreamCalls != 0 {
		t.Fatalf("downstream calls=%d; want zero", downstreamCalls)
	}
}

func TestSamePolicyPathTreatsCaseVariantsOfMissingReservedLocationAsSame(t *testing.T) {
	root := t.TempDir()
	same, err := samePolicyPath(filepath.Join(root, "evidence"), filepath.Join(root, "EVIDENCE"))
	if err != nil {
		t.Fatal(err)
	}
	if !same {
		t.Fatal("case variants below the same existing ancestor must be reserved as one policy location")
	}
}

func TestRunContinuesAllResolvableCellsPersistsAttemptsAndRechecksState(t *testing.T) {
	fixture := newRunApplicationFixture(t, 2)
	operations := make([]string, 0)
	records := make([]evidence.Record, 0)
	stateReads := 0
	fixture.dependencies.computeState = func(context.Context, string) (inputdigest.State, error) {
		stateReads++
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
	fixture.dependencies.newRunSetID = func(time.Time) (evidence.RunSetID, error) {
		operations = append(operations, "run-set")
		return runTestRunSetID(), nil
	}
	fixture.dependencies.newEvidenceID = fixture.idGenerator(&operations)
	fixture.dependencies.newStore = func(string) (*evidence.Store, error) {
		operations = append(operations, "store")
		return nil, nil
	}
	runIndex := 0
	fixture.dependencies.runExperiment = func(_ context.Context, spec harness.RunSpec) (harness.RunResult, error) {
		operations = append(operations, "runner-"+spec.LabID)
		result := runResultForSpec(spec, runIndex != 0)
		runIndex++
		if result.Status == harness.StatusFailed {
			return result, errors.New("intentional runner failure")
		}
		return result, nil
	}
	fixture.dependencies.putEvidence = func(_ context.Context, _ *evidence.Store, record evidence.Record) error {
		operations = append(operations, "put-"+record.LabID)
		records = append(records, record)
		return nil
	}

	exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
		"run", "--required", "--profile", "smoke", "--root", "repo", "--evidence-root", "external",
	})
	if exitCode != ExitLabExecutionFailure || stdout != "" || !strings.Contains(stderr, "current_run_incomplete") {
		t.Fatalf("run exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if stateReads != 2 {
		t.Fatalf("state reads=%d; want two", stateReads)
	}
	if len(records) != 2 || records[0].Status != evidence.StatusFailed || records[1].Status != evidence.StatusPassed {
		t.Fatalf("records=%#v; want failed then passed attempts", records)
	}
	if records[0].RunSetID != runTestRunSetID() || records[1].RunSetID != runTestRunSetID() {
		t.Fatalf("run-set IDs=%q/%q", records[0].RunSetID, records[1].RunSetID)
	}
	joined := strings.Join(operations, ",")
	for _, required := range []string{"runner-lab-one", "put-lab-one", "runner-lab-two", "put-lab-two"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("operations=%q; missing %q", joined, required)
		}
	}
	if strings.LastIndex(joined, "state") <= strings.Index(joined, "put-lab-two") {
		t.Fatalf("second source-state read was not after the execution loop: %q", joined)
	}
}

func TestRunRejectsUnsafeDynamicTextBeforePutWithoutSnapshot(t *testing.T) {
	tests := []struct {
		name   string
		field  string
		unsafe string
	}{
		{name: "assertion absolute path", field: "assertion", unsafe: "result at /absolute/workspace/secret"},
		{name: "conclusion generated identity", field: "conclusion", unsafe: "attempt run-20260714T010203.004Z-00000000000000000000000000000009 passed"},
		{name: "limitation host path", field: "limitation", unsafe: "host root /absolute/workspace/secret"},
		{name: "diagnostic host path", field: "diagnostic", unsafe: "host root /absolute/workspace/secret"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRunApplicationFixture(t, 1)
			if test.field == "conclusion" || test.field == "limitation" {
				definition := fixture.definitions["lab-one"]
				if test.field == "conclusion" {
					definition.Conclude = func(harness.RunResult) string { return test.unsafe }
				} else {
					definition.Limitations[0] = test.unsafe
				}
				fixture.definitions["lab-one"] = definition
			}
			fixture.dependencies.newStore = func(string) (*evidence.Store, error) { return nil, nil }
			fixture.dependencies.runExperiment = func(_ context.Context, spec harness.RunSpec) (harness.RunResult, error) {
				result := runResultForSpec(spec, true)
				if test.field == "assertion" {
					result.Assertions[0].Message = test.unsafe
				} else if test.field == "diagnostic" {
					result.Diagnostics = []harness.Diagnostic{{Event: "request-one", Message: test.unsafe}}
				}
				return result, nil
			}
			idCalls := 0
			putCalls := 0
			buildCalls := 0
			fixture.dependencies.newEvidenceID = func(time.Time) (string, error) {
				idCalls++
				return runTestEvidenceID(0), nil
			}
			fixture.dependencies.putEvidence = func(context.Context, *evidence.Store, evidence.Record) error {
				putCalls++
				return nil
			}
			fixture.dependencies.buildManifest = func(inputdigest.Digest, []release.ExpectedCell, []evidence.Record) (release.Manifest, error) {
				buildCalls++
				return release.Manifest{}, nil
			}

			exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
				"run", "--required", "--profile", "smoke", "--root", "repo", "--evidence-root", "external",
			})
			if exitCode != ExitLabExecutionFailure || stdout != "" || !strings.Contains(stderr, "current_run_incomplete") {
				t.Fatalf("run exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
			}
			if idCalls != 0 || putCalls != 0 || buildCalls != 0 {
				t.Fatalf("id=%d put=%d build=%d; want zero before persistence/publication", idCalls, putCalls, buildCalls)
			}
			if strings.Contains(stderr, test.unsafe) || strings.ContainsAny(stderr, "\x1b\r") {
				t.Fatalf("stderr leaked unsafe dynamic text: %q", stderr)
			}
		})
	}
}

func TestRunSourceChangeLeavesAttemptsAndNeverPublishesSnapshot(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	changed := fixture.state
	changed.SourceCommit = strings.Repeat("c", 40)
	reads := 0
	puts := 0
	writes := 0
	fixture.dependencies.computeState = func(context.Context, string) (inputdigest.State, error) {
		reads++
		if reads == 1 {
			return fixture.state, nil
		}
		return changed, nil
	}
	fixture.dependencies.readBuildInfo = func() (*debug.BuildInfo, bool) { return matchingBuildInfoForApp(fixture.state.SourceCommit), true }
	fixture.dependencies.newStore = func(string) (*evidence.Store, error) { return nil, nil }
	fixture.dependencies.runExperiment = func(_ context.Context, spec harness.RunSpec) (harness.RunResult, error) {
		return runResultForSpec(spec, true), nil
	}
	fixture.dependencies.putEvidence = func(context.Context, *evidence.Store, evidence.Record) error { puts++; return nil }
	fixture.dependencies.writeManifest = func(context.Context, string, release.Manifest) error { writes++; return nil }

	exitCode, _, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
		"run", "--required", "--profile", "smoke", "--snapshot", "--root", "repo", "--evidence-root", "external",
	})
	if exitCode != ExitLabExecutionFailure || !strings.Contains(stderr, "source_state_changed") {
		t.Fatalf("source change exit=%d stderr=%q", exitCode, stderr)
	}
	if puts != 1 || writes != 0 {
		t.Fatalf("puts=%d writes=%d; want persisted attempt and no snapshot", puts, writes)
	}
}

func TestRunExistingSnapshotPreflightSkipsExecutionEntropyAndWrites(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	manifest := release.Manifest{InputDigest: fixture.state.InputDigest, Profile: evidence.ProfileDeep}
	runnerCalls := 0
	entropyCalls := 0
	putCalls := 0
	writeCalls := 0
	fixture.dependencies.loadAndSyncManifest = func(context.Context, string, inputdigest.Digest) (release.Manifest, error) { return manifest, nil }
	fixture.dependencies.openStoreReadOnly = func(string) (*evidence.Store, error) { return nil, nil }
	fixture.dependencies.auditSnapshot = func(context.Context, release.Manifest, []release.ExpectedCell, *evidence.Store) (release.AuditedSnapshot, []validator.Diagnostic, error) {
		return release.AuditedSnapshot{}, []validator.Diagnostic{}, nil
	}
	fixture.dependencies.runExperiment = func(context.Context, harness.RunSpec) (harness.RunResult, error) {
		runnerCalls++
		return harness.RunResult{}, nil
	}
	fixture.dependencies.newRunSetID = func(time.Time) (evidence.RunSetID, error) { entropyCalls++; return runTestRunSetID(), nil }
	fixture.dependencies.newEvidenceID = func(time.Time) (string, error) { entropyCalls++; return runTestEvidenceID(0), nil }
	fixture.dependencies.putEvidence = func(context.Context, *evidence.Store, evidence.Record) error { putCalls++; return nil }
	fixture.dependencies.writeManifest = func(context.Context, string, release.Manifest) error { writeCalls++; return nil }

	exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
		"run", "--required", "--profile", "deep", "--snapshot", "--root", "repo", "--evidence-root", "external",
	})
	if exitCode != ExitSuccess || stdout != "" || stderr != "" {
		t.Fatalf("preflight exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if runnerCalls != 0 || entropyCalls != 0 || putCalls != 0 || writeCalls != 0 {
		t.Fatalf("runner=%d entropy=%d put=%d write=%d; want all zero", runnerCalls, entropyCalls, putCalls, writeCalls)
	}
}

func TestRunSnapshotRejectsManifestReplacedDuringDurabilitySync(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	evidenceRoot := filepath.Join(t.TempDir(), "evidence")
	fixture.dependencies.runExperiment = func(_ context.Context, spec harness.RunSpec) (harness.RunResult, error) {
		return runResultForSpec(spec, true), nil
	}
	args := []string{
		"run", "--required", "--profile", "deep", "--snapshot", "--root", "repo", "--evidence-root", evidenceRoot,
	}
	if exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, args); exitCode != ExitSuccess || stdout != "" || stderr != "" {
		t.Fatalf("seed snapshot exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	canonicalRoot, err := canonicalPolicyPath(evidenceRoot)
	if err != nil {
		t.Fatal(err)
	}
	deepManifest, err := release.LoadManifest(context.Background(), canonicalRoot, fixture.state.InputDigest)
	if err != nil {
		t.Fatal(err)
	}
	smokeManifest := deepManifest
	smokeManifest.Profile = evidence.ProfileSmoke

	auditCalls := 0
	fixture.dependencies.loadAndSyncManifest = func(ctx context.Context, root string, digest inputdigest.Digest) (release.Manifest, error) {
		stale, err := release.LoadManifest(ctx, root, digest)
		if err != nil {
			return release.Manifest{}, err
		}
		if stale.Profile != evidence.ProfileDeep {
			return release.Manifest{}, errors.New("fixture did not observe initial deep manifest")
		}
		path := filepath.Join(root, "releases", "sha256-"+strings.TrimPrefix(string(digest), "sha256:"), "manifest.yaml")
		if err := os.Remove(path); err != nil {
			return release.Manifest{}, err
		}
		if err := release.WriteManifest(ctx, root, smokeManifest); err != nil {
			return release.Manifest{}, err
		}
		return release.LoadAndSyncManifest(ctx, root, digest)
	}
	fixture.dependencies.auditSnapshot = func(context.Context, release.Manifest, []release.ExpectedCell, *evidence.Store) (release.AuditedSnapshot, []validator.Diagnostic, error) {
		auditCalls++
		return release.AuditedSnapshot{}, []validator.Diagnostic{}, nil
	}

	exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, args)
	if exitCode != ExitReleaseFailure || stdout != "" || !strings.Contains(stderr, release.CodeReleaseProfileMismatch) {
		t.Fatalf("replaced snapshot exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if auditCalls != 0 {
		t.Fatalf("audit calls=%d; want zero for replacement profile drift", auditCalls)
	}
}

func TestRunExistingSnapshotSanitizesAuditDiagnostics(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	secret := "/absolute/workspace/secret\x1b[31m\nforged"
	fixture.dependencies.loadAndSyncManifest = func(context.Context, string, inputdigest.Digest) (release.Manifest, error) {
		return release.Manifest{InputDigest: fixture.state.InputDigest, Profile: evidence.ProfileDeep}, nil
	}
	fixture.dependencies.openStoreReadOnly = func(string) (*evidence.Store, error) { return nil, nil }
	fixture.dependencies.auditSnapshot = func(context.Context, release.Manifest, []release.ExpectedCell, *evidence.Store) (release.AuditedSnapshot, []validator.Diagnostic, error) {
		return release.AuditedSnapshot{}, []validator.Diagnostic{{
			Code: release.CodeReleaseRunSetMismatch, Severity: "fatal", Path: secret,
			EntityID: string(runTestRunSetID()), Message: secret,
		}}, nil
	}
	exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
		"run", "--required", "--profile", "deep", "--snapshot", "--root", "repo", "--evidence-root", "external",
	})
	if exitCode != ExitReleaseFailure || stdout != "" || !strings.Contains(stderr, release.CodeReleaseRunSetMismatch) {
		t.Fatalf("audit failure exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if strings.Contains(stderr, "/absolute/workspace/secret") || strings.Contains(stderr, "set-20000101") || strings.ContainsAny(stderr, "\x1b\r") {
		t.Fatalf("run audit diagnostics leaked untrusted fields: %q", stderr)
	}
}

func TestRunSnapshotInspectsStorageBeforeDefinitionFailureSideEffects(t *testing.T) {
	tests := []struct {
		name       string
		load       func(inputdigest.Digest) (release.Manifest, error)
		wantExit   int
		wantCode   string
		wantRunSet int
		wantStore  int
	}{
		{name: "corrupt existing", load: func(inputdigest.Digest) (release.Manifest, error) {
			return release.Manifest{}, release.ErrSnapshotCorrupt
		}, wantExit: ExitReleaseFailure, wantCode: release.CodeReleaseManifestInvalid},
		{name: "valid existing", load: func(digest inputdigest.Digest) (release.Manifest, error) {
			return release.Manifest{InputDigest: digest, Profile: evidence.ProfileDeep}, nil
		}, wantExit: ExitLabExecutionFailure, wantCode: codeExperimentRegistryMissing},
		{name: "absent", load: func(inputdigest.Digest) (release.Manifest, error) {
			return release.Manifest{}, release.ErrSnapshotNotFound
		}, wantExit: ExitLabExecutionFailure, wantCode: codeExperimentRegistryMissing, wantRunSet: 1, wantStore: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRunApplicationFixture(t, 1)
			fixture.dependencies.lookupExperiment = func(validator.MatrixCell) (experiments.Factory, bool) { return nil, false }
			loadCalls := 0
			runSetCalls := 0
			storeCalls := 0
			runnerCalls := 0
			putCalls := 0
			auditCalls := 0
			fixture.dependencies.loadAndSyncManifest = func(_ context.Context, _ string, digest inputdigest.Digest) (release.Manifest, error) {
				loadCalls++
				return test.load(digest)
			}
			fixture.dependencies.newRunSetID = func(time.Time) (evidence.RunSetID, error) { runSetCalls++; return runTestRunSetID(), nil }
			fixture.dependencies.newStore = func(string) (*evidence.Store, error) { storeCalls++; return nil, nil }
			fixture.dependencies.runExperiment = func(context.Context, harness.RunSpec) (harness.RunResult, error) {
				runnerCalls++
				return harness.RunResult{}, nil
			}
			fixture.dependencies.putEvidence = func(context.Context, *evidence.Store, evidence.Record) error { putCalls++; return nil }
			fixture.dependencies.auditSnapshot = func(context.Context, release.Manifest, []release.ExpectedCell, *evidence.Store) (release.AuditedSnapshot, []validator.Diagnostic, error) {
				auditCalls++
				return release.AuditedSnapshot{}, []validator.Diagnostic{}, nil
			}
			exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
				"run", "--required", "--profile", "deep", "--snapshot", "--root", "repo", "--evidence-root", "external",
			})
			if exitCode != test.wantExit || stdout != "" || !strings.Contains(stderr, test.wantCode) {
				t.Fatalf("snapshot state exit=%d stdout=%q stderr=%q; want exit=%d code=%q", exitCode, stdout, stderr, test.wantExit, test.wantCode)
			}
			if loadCalls != 1 || runSetCalls != test.wantRunSet || storeCalls != test.wantStore || runnerCalls != 0 || putCalls != 0 || auditCalls != 0 {
				t.Fatalf("calls load-sync=%d runset=%d store=%d runner=%d put=%d audit=%d", loadCalls, runSetCalls, storeCalls, runnerCalls, putCalls, auditCalls)
			}
		})
	}
}

func TestRunPostInstallOperationalErrorNeverUsesExistsPreflight(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	loadCalls := 0
	auditCalls := 0
	fixture.dependencies.loadAndSyncManifest = func(_ context.Context, _ string, digest inputdigest.Digest) (release.Manifest, error) {
		loadCalls++
		if loadCalls == 1 {
			return release.Manifest{}, release.ErrSnapshotNotFound
		}
		return release.Manifest{InputDigest: digest, Profile: evidence.ProfileDeep}, nil
	}
	fixture.dependencies.openStoreReadOnly = func(string) (*evidence.Store, error) { return nil, nil }
	fixture.dependencies.runExperiment = func(_ context.Context, spec harness.RunSpec) (harness.RunResult, error) {
		return runResultForSpec(spec, true), nil
	}
	fixture.dependencies.putEvidence = func(context.Context, *evidence.Store, evidence.Record) error { return nil }
	fixture.dependencies.auditSnapshot = func(context.Context, release.Manifest, []release.ExpectedCell, *evidence.Store) (release.AuditedSnapshot, []validator.Diagnostic, error) {
		auditCalls++
		return release.AuditedSnapshot{}, []validator.Diagnostic{}, nil
	}
	fixture.dependencies.writeManifest = func(context.Context, string, release.Manifest) error {
		return errors.Join(release.ErrSnapshotExists, release.ErrSnapshotIO, errors.New("/absolute/workspace/secret"))
	}
	exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
		"run", "--required", "--profile", "deep", "--snapshot", "--root", "repo", "--evidence-root", "external",
	})
	if exitCode != ExitArgumentOrLoadFailure || stdout != "" || !strings.Contains(stderr, "release_snapshot_write_failed") {
		t.Fatalf("post-install operational exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if loadCalls != 1 || auditCalls != 0 || strings.Contains(stderr, "/absolute/workspace/secret") {
		t.Fatalf("operational error triggered preflight or leaked path: load=%d audit=%d stderr=%q", loadCalls, auditCalls, stderr)
	}
}

func TestRunPostInstallUnprovenExistsNeverAuditsWinner(t *testing.T) {
	opaque := errors.New("/absolute/workspace/secret")
	tests := []struct {
		name    string
		failure error
	}{
		{name: "exists with opaque sibling", failure: errors.Join(release.ErrSnapshotExists, opaque)},
		{name: "custom Is-only exists leaf", failure: customReleaseSnapshotExistsLeaf{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRunApplicationFixture(t, 1)
			loadCalls := 0
			auditCalls := 0
			fixture.dependencies.loadAndSyncManifest = func(_ context.Context, _ string, digest inputdigest.Digest) (release.Manifest, error) {
				loadCalls++
				if loadCalls == 1 {
					return release.Manifest{}, release.ErrSnapshotNotFound
				}
				return release.Manifest{InputDigest: digest, Profile: evidence.ProfileDeep}, nil
			}
			fixture.dependencies.openStoreReadOnly = func(string) (*evidence.Store, error) { return nil, nil }
			fixture.dependencies.runExperiment = func(_ context.Context, spec harness.RunSpec) (harness.RunResult, error) {
				return runResultForSpec(spec, true), nil
			}
			fixture.dependencies.putEvidence = func(context.Context, *evidence.Store, evidence.Record) error { return nil }
			fixture.dependencies.auditSnapshot = func(context.Context, release.Manifest, []release.ExpectedCell, *evidence.Store) (release.AuditedSnapshot, []validator.Diagnostic, error) {
				auditCalls++
				return release.AuditedSnapshot{}, []validator.Diagnostic{}, nil
			}
			fixture.dependencies.writeManifest = func(context.Context, string, release.Manifest) error {
				return test.failure
			}

			exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
				"run", "--required", "--profile", "deep", "--snapshot", "--root", "repo", "--evidence-root", "external",
			})
			if exitCode != ExitArgumentOrLoadFailure || stdout != "" || !strings.Contains(stderr, "release_snapshot_write_failed") {
				t.Fatalf("unproven exists exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
			}
			if loadCalls != 1 || auditCalls != 0 || strings.Contains(stderr, opaque.Error()) {
				t.Fatalf("unproven exists audited winner or leaked path: load=%d audit=%d stderr=%q", loadCalls, auditCalls, stderr)
			}
		})
	}
}

type customReleaseSnapshotExistsLeaf struct{}

func (customReleaseSnapshotExistsLeaf) Error() string {
	return "custom release snapshot exists"
}

func (customReleaseSnapshotExistsLeaf) Is(target error) bool {
	return target == release.ErrSnapshotExists
}

func TestRunPostInstallSemanticErrorNeverUsesExistsPreflight(t *testing.T) {
	tests := []struct {
		name  string
		cause error
	}{
		{name: "unsafe", cause: release.ErrSnapshotUnsafePath},
		{name: "corrupt", cause: release.ErrSnapshotCorrupt},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRunApplicationFixture(t, 1)
			loadCalls := 0
			auditCalls := 0
			fixture.dependencies.loadAndSyncManifest = func(_ context.Context, _ string, digest inputdigest.Digest) (release.Manifest, error) {
				loadCalls++
				if loadCalls == 1 {
					return release.Manifest{}, release.ErrSnapshotNotFound
				}
				return release.Manifest{InputDigest: digest, Profile: evidence.ProfileDeep}, nil
			}
			fixture.dependencies.openStoreReadOnly = func(string) (*evidence.Store, error) { return nil, nil }
			fixture.dependencies.runExperiment = func(_ context.Context, spec harness.RunSpec) (harness.RunResult, error) {
				return runResultForSpec(spec, true), nil
			}
			fixture.dependencies.putEvidence = func(context.Context, *evidence.Store, evidence.Record) error { return nil }
			fixture.dependencies.auditSnapshot = func(context.Context, release.Manifest, []release.ExpectedCell, *evidence.Store) (release.AuditedSnapshot, []validator.Diagnostic, error) {
				auditCalls++
				return release.AuditedSnapshot{}, []validator.Diagnostic{}, nil
			}
			fixture.dependencies.writeManifest = func(context.Context, string, release.Manifest) error {
				return errors.Join(release.ErrSnapshotExists, test.cause, errors.New("/absolute/workspace/secret"))
			}

			exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
				"run", "--required", "--profile", "deep", "--snapshot", "--root", "repo", "--evidence-root", "external",
			})
			if exitCode != ExitReleaseFailure || stdout != "" || !strings.Contains(stderr, release.CodeReleaseManifestInvalid) {
				t.Fatalf("post-install semantic exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
			}
			if loadCalls != 1 || auditCalls != 0 || strings.Contains(stderr, "/absolute/workspace/secret") {
				t.Fatalf("semantic error triggered preflight or leaked path: load=%d audit=%d stderr=%q", loadCalls, auditCalls, stderr)
			}
		})
	}
}

func TestRunConcurrentSnapshotLoserAuditsWinner(t *testing.T) {
	tests := []struct {
		name      string
		winner    func(inputdigest.Digest) (release.Manifest, error)
		wantExit  int
		wantCode  string
		wantAudit int
	}{
		{
			name: "valid winner",
			winner: func(digest inputdigest.Digest) (release.Manifest, error) {
				return release.Manifest{InputDigest: digest, Profile: evidence.ProfileDeep}, nil
			},
			wantExit: ExitSuccess, wantAudit: 1,
		},
		{
			name: "corrupt winner",
			winner: func(inputdigest.Digest) (release.Manifest, error) {
				return release.Manifest{}, release.ErrSnapshotCorrupt
			},
			wantExit: ExitReleaseFailure, wantCode: release.CodeReleaseManifestInvalid,
		},
		{
			name: "profile mismatched winner",
			winner: func(digest inputdigest.Digest) (release.Manifest, error) {
				return release.Manifest{InputDigest: digest, Profile: evidence.ProfileSmoke}, nil
			},
			wantExit: ExitReleaseFailure, wantCode: release.CodeReleaseProfileMismatch,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRunApplicationFixture(t, 1)
			loadCalls := 0
			auditCalls := 0
			fixture.dependencies.loadAndSyncManifest = func(_ context.Context, _ string, digest inputdigest.Digest) (release.Manifest, error) {
				loadCalls++
				if loadCalls == 1 {
					return release.Manifest{}, release.ErrSnapshotNotFound
				}
				return test.winner(digest)
			}
			fixture.dependencies.openStoreReadOnly = func(string) (*evidence.Store, error) { return nil, nil }
			fixture.dependencies.runExperiment = func(_ context.Context, spec harness.RunSpec) (harness.RunResult, error) {
				return runResultForSpec(spec, true), nil
			}
			fixture.dependencies.putEvidence = func(context.Context, *evidence.Store, evidence.Record) error { return nil }
			fixture.dependencies.writeManifest = func(_ context.Context, _ string, manifest release.Manifest) error {
				return certifiedSnapshotExistsError(t, manifest)
			}
			fixture.dependencies.auditSnapshot = func(context.Context, release.Manifest, []release.ExpectedCell, *evidence.Store) (release.AuditedSnapshot, []validator.Diagnostic, error) {
				auditCalls++
				return release.AuditedSnapshot{}, []validator.Diagnostic{}, nil
			}

			exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
				"run", "--required", "--profile", "deep", "--snapshot", "--root", "repo", "--evidence-root", "external",
			})
			if exitCode != test.wantExit || stdout != "" {
				t.Fatalf("concurrent loser exit=%d stdout=%q stderr=%q; want exit=%d", exitCode, stdout, stderr, test.wantExit)
			}
			if test.wantCode == "" {
				if stderr != "" {
					t.Fatalf("successful concurrent loser stderr=%q; want empty", stderr)
				}
			} else if !strings.Contains(stderr, test.wantCode) {
				t.Fatalf("concurrent loser stderr=%q; want code %q", stderr, test.wantCode)
			}
			if loadCalls != 2 || auditCalls != test.wantAudit {
				t.Fatalf("calls load-sync=%d audit=%d; want 2/%d", loadCalls, auditCalls, test.wantAudit)
			}
		})
	}
}

func TestRunExistingSnapshotClassifiesUnsafeEvidenceStoreAsReleaseFailure(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	fixture.dependencies.loadAndSyncManifest = func(context.Context, string, inputdigest.Digest) (release.Manifest, error) {
		return release.Manifest{InputDigest: fixture.state.InputDigest, Profile: evidence.ProfileDeep}, nil
	}
	fixture.dependencies.openStoreReadOnly = func(string) (*evidence.Store, error) {
		return nil, errors.Join(evidence.ErrEvidenceUnsafePath, errors.New("/absolute/workspace/secret"))
	}
	runnerCalls := 0
	fixture.dependencies.runExperiment = func(context.Context, harness.RunSpec) (harness.RunResult, error) {
		runnerCalls++
		return harness.RunResult{}, nil
	}
	exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
		"run", "--required", "--profile", "deep", "--snapshot", "--root", "repo", "--evidence-root", "external",
	})
	if exitCode != ExitReleaseFailure || stdout != "" || !strings.Contains(stderr, release.CodeReleaseEvidenceInvalid) || runnerCalls != 0 {
		t.Fatalf("unsafe preflight exit=%d stdout=%q stderr=%q runner=%d", exitCode, stdout, stderr, runnerCalls)
	}
	if strings.Contains(stderr, "/absolute/workspace/secret") {
		t.Fatalf("stderr leaked absolute path: %q", stderr)
	}
}

func TestRunExternalSmokePublishesAuditableSnapshotAndRerunsIdempotently(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	evidenceRoot := filepath.Join(t.TempDir(), "external-evidence")
	runnerCalls := 0
	entropyCalls := 0
	fixture.dependencies.newRunSetID = func(time.Time) (evidence.RunSetID, error) {
		entropyCalls++
		return runTestRunSetID(), nil
	}
	fixture.dependencies.newEvidenceID = func(time.Time) (string, error) {
		entropyCalls++
		return runTestEvidenceID(0), nil
	}
	fixture.dependencies.runExperiment = func(_ context.Context, spec harness.RunSpec) (harness.RunResult, error) {
		runnerCalls++
		return runResultForSpec(spec, true), nil
	}
	args := []string{"run", "--required", "--profile", "smoke", "--snapshot", "--root", "repo", "--evidence-root", evidenceRoot}
	app := application{dependencies: fixture.dependencies}
	firstExit, firstStdout, firstStderr := runApplication(t, app, args)
	if firstExit != ExitSuccess || firstStdout != "" || firstStderr != "" {
		t.Fatalf("first run exit=%d stdout=%q stderr=%q", firstExit, firstStdout, firstStderr)
	}
	canonicalEvidenceRoot, err := canonicalPolicyPath(evidenceRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := release.LoadManifest(context.Background(), canonicalEvidenceRoot, fixture.state.InputDigest)
	if err != nil {
		t.Fatal(err)
	}
	store, err := evidence.NewStore(canonicalEvidenceRoot)
	if err != nil {
		t.Fatal(err)
	}
	resolved, issues := resolveDefinitions(fixture.repository, fixture.cells, evidence.ProfileSmoke, fixture.dependencies.lookupExperiment)
	if len(issues) != 0 {
		t.Fatalf("definition issues=%#v", issues)
	}
	if _, diagnostics, err := release.AuditSnapshot(context.Background(), manifest, expectedCells(resolved), store); err != nil || len(diagnostics) != 0 {
		t.Fatalf("audit diagnostics=%#v err=%v", diagnostics, err)
	}
	secondExit, secondStdout, secondStderr := runApplication(t, app, args)
	if secondExit != ExitSuccess || secondStdout != "" || secondStderr != "" {
		t.Fatalf("second run exit=%d stdout=%q stderr=%q", secondExit, secondStdout, secondStderr)
	}
	if runnerCalls != 1 || entropyCalls != 2 {
		t.Fatalf("runner=%d entropy=%d; want first invocation only", runnerCalls, entropyCalls)
	}
}

func TestRunRealSixCellMatrixIsLogicallyDeterministicAcrossIndependentRoots(t *testing.T) {
	repositoryRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	state := runTestState("a", "b")
	firstBytes, firstManifest := runRealSixCellSnapshot(t, repositoryRoot, state, 1)
	secondBytes, secondManifest := runRealSixCellSnapshot(t, repositoryRoot, state, 2)
	if firstManifest.RunSetID == secondManifest.RunSetID {
		t.Fatalf("independent snapshots reused run-set ID %q", firstManifest.RunSetID)
	}
	if len(firstManifest.Selections) != 6 || len(secondManifest.Selections) != 6 {
		t.Fatalf("selection counts=%d/%d; want six each", len(firstManifest.Selections), len(secondManifest.Selections))
	}
	for index := range firstManifest.Selections {
		if firstManifest.Selections[index].EvidenceID == secondManifest.Selections[index].EvidenceID {
			t.Fatalf("independent snapshots reused attempt ID at index %d", index)
		}
	}
	if string(firstBytes) != string(secondBytes) {
		t.Fatalf("normalized logical records differ across independent evidence roots:\nfirst=%s\nsecond=%s", firstBytes, secondBytes)
	}
}

func runRealSixCellSnapshot(t *testing.T, repositoryRoot string, state inputdigest.State, identityGroup int) ([]byte, release.Manifest) {
	t.Helper()
	evidenceRoot := filepath.Join(t.TempDir(), "evidence")
	evidenceIndex := 0
	dependencies := applicationDependencies{
		computeState:  func(context.Context, string) (inputdigest.State, error) { return state, nil },
		readBuildInfo: func() (*debug.BuildInfo, bool) { return matchingBuildInfoForApp(state.SourceCommit), true },
		now: func() time.Time {
			return time.Date(2000, 1, identityGroup, 0, 0, 0, 0, time.UTC)
		},
		newRunSetID: func(time.Time) (evidence.RunSetID, error) {
			return evidence.RunSetID(fmt.Sprintf("set-200001%02dT000000.000Z-%032x", identityGroup, identityGroup)), nil
		},
		newEvidenceID: func(time.Time) (string, error) {
			evidenceIndex++
			return fmt.Sprintf("run-200001%02dT000000.000Z-%032x", identityGroup, identityGroup*100+evidenceIndex), nil
		},
	}
	exitCode, stdout, stderr := runApplication(t, application{dependencies: dependencies}, []string{
		"run", "--required", "--profile", "deep", "--snapshot", "--root", repositoryRoot, "--evidence-root", evidenceRoot,
	})
	if exitCode != ExitSuccess || stdout != "" || stderr != "" {
		t.Fatalf("six-cell run %d exit=%d stdout=%q stderr=%q", identityGroup, exitCode, stdout, stderr)
	}
	if evidenceIndex != 6 {
		t.Fatalf("six-cell run %d allocated %d attempt IDs; want six", identityGroup, evidenceIndex)
	}
	canonicalRoot, err := canonicalPolicyPath(evidenceRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := release.LoadManifest(context.Background(), canonicalRoot, state.InputDigest)
	if err != nil {
		t.Fatal(err)
	}
	store, err := evidence.NewStore(canonicalRoot)
	if err != nil {
		t.Fatal(err)
	}
	records := make([]evidence.Record, 0, len(manifest.Selections))
	for _, selection := range manifest.Selections {
		record, err := store.Get(context.Background(), selection.EvidenceID)
		if err != nil {
			t.Fatal(err)
		}
		record.ID = ""
		record.RunSetID = ""
		record.SourceCommit = ""
		record.ContentDigest = ""
		records = append(records, record)
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	return encoded, manifest
}

type runApplicationFixture struct {
	state        inputdigest.State
	repository   *catalog.Catalog
	cells        []validator.MatrixCell
	definitions  map[string]experiments.Definition
	dependencies applicationDependencies
}

func newRunApplicationFixture(t *testing.T, count int) runApplicationFixture {
	t.Helper()
	state := runTestState("a", "b")
	repository := &catalog.Catalog{Labs: map[string]catalog.LabManifest{}}
	cells := make([]validator.MatrixCell, 0, count)
	definitions := make(map[string]experiments.Definition, count)
	for index := 0; index < count; index++ {
		cell := definitionTestCell()
		suffix := "one"
		if index == 1 {
			suffix = "two"
			cell.LabID = "lab-two"
			cell.RequiredRunID = "run-two"
			cell.BindingID = "binding-two"
			cell.ClaimID = "claim-two"
			cell.ImplementationID = "implementation-two"
		}
		definition := definitionTestDefinition(cell)
		originalConclusion := definition.Conclude
		definition.Conclude = func(result harness.RunResult) string { return originalConclusion(result) + " " + suffix }
		cells = append(cells, cell)
		definitions[cell.LabID] = definition
		repository.Labs[cell.LabID] = catalog.LabManifest{ID: cell.LabID, Metrics: []string{"requests.total"}}
	}
	dependencies := applicationDependencies{
		computeState:  func(context.Context, string) (inputdigest.State, error) { return state, nil },
		readBuildInfo: func() (*debug.BuildInfo, bool) { return matchingBuildInfoForApp(state.SourceCommit), true },
		loadCatalog:   func(context.Context, string) (*catalog.Catalog, error) { return repository, nil },
		validateCatalog: func(*catalog.Catalog, validator.Mode) validator.Report {
			return validator.Report{Diagnostics: []validator.Diagnostic{}, Matrix: cells}
		},
		lookupExperiment: func(cell validator.MatrixCell) (experiments.Factory, bool) {
			definition, exists := definitions[cell.LabID]
			if !exists {
				return nil, false
			}
			return func(_ validator.MatrixCell, profile experiments.Profile) (experiments.Definition, error) {
				definition.Profile = profile
				return definition, nil
			}, true
		},
		newRunSetID:   func(time.Time) (evidence.RunSetID, error) { return runTestRunSetID(), nil },
		newEvidenceID: func(time.Time) (string, error) { return runTestEvidenceID(0), nil },
		now:           func() time.Time { return time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC) },
		currentEnvironment: func() evidence.Environment {
			return evidence.Environment{GoVersion: "go1.26.5", OS: "darwin", Arch: "arm64", CPU: "unknown", LogicalCPUs: 8}
		},
	}
	return runApplicationFixture{state: state, repository: repository, cells: cells, definitions: definitions, dependencies: dependencies}
}

func (fixture runApplicationFixture) lookup(operations *[]string) experimentLookup {
	return func(cell validator.MatrixCell) (experiments.Factory, bool) {
		*operations = append(*operations, "lookup-"+cell.LabID)
		return fixture.dependencies.lookupExperiment(cell)
	}
}

func (fixture runApplicationFixture) idGenerator(operations *[]string) func(time.Time) (string, error) {
	index := 0
	return func(time.Time) (string, error) {
		*operations = append(*operations, "id")
		id := runTestEvidenceID(index)
		index++
		return id, nil
	}
}

func runResultForSpec(spec harness.RunSpec, passed bool) harness.RunResult {
	result := harness.RunResult{
		Status:         harness.StatusPassed,
		StartedAt:      spec.Start,
		FinishedAt:     spec.Start.Add(time.Nanosecond),
		EventsExecuted: uint64(len(spec.Events)),
		Metrics:        []harness.Metric{{Name: "requests.total", Unit: "count", Value: 4}},
		Assertions:     []harness.AssertionResult{{ID: "assertion-one", Passed: true, Message: "ok"}},
		Diagnostics:    []harness.Diagnostic{},
	}
	if !passed {
		result.Status = harness.StatusFailed
		result.Assertions[0].Passed = false
		result.Assertions[0].Message = "failed"
	}
	return result
}

func certifiedSnapshotExistsError(t *testing.T, manifest release.Manifest) error {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve conflict root: %v", err)
	}
	if err := release.WriteManifest(context.Background(), root, manifest); err != nil {
		t.Fatalf("install conflict fixture: %v", err)
	}
	err = release.WriteManifest(context.Background(), root, manifest)
	if !errors.Is(err, release.ErrSnapshotExists) || !release.IsPureSnapshotExists(err) {
		t.Fatalf("second WriteManifest error = %v, want certified snapshot exists", err)
	}
	return err
}

func runTestState(digestHex, commitHex string) inputdigest.State {
	return inputdigest.State{
		InputDigest:  inputdigest.Digest("sha256:" + strings.Repeat(digestHex, 64)),
		SourceCommit: strings.Repeat(commitHex, 40),
	}
}

func runTestRunSetID() evidence.RunSetID {
	return "set-20000101T000000.000Z-00000000000000000000000000000000"
}

func runTestEvidenceID(index int) string {
	hex := "00000000000000000000000000000000"
	if index == 1 {
		hex = "11111111111111111111111111111111"
	}
	return "run-20000101T000000.000Z-" + hex
}
