package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/content"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/experiments"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/release"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
	"github.com/PinoHouse/works-on-my-whiteboard/labs/harness"
)

func TestRunRejectsUnknownCommandsAndMissingCommands(t *testing.T) {
	for _, args := range [][]string{nil, {}, {"unknown"}} {
		exitCode, stdout, stderr := runCLI(context.Background(), args)
		if exitCode != 2 {
			t.Fatalf("Run(%q) exit = %d; want 2", args, exitCode)
		}
		if stdout != "" {
			t.Fatalf("Run(%q) stdout = %q; want empty", args, stdout)
		}
		if stderr == "" {
			t.Fatalf("Run(%q) stderr is empty; want argument error", args)
		}
	}
}

func TestSubcommandHelpUsesStderrAndSucceeds(t *testing.T) {
	for _, command := range []string{"validate", "coverage", "run", "report", "diff"} {
		exitCode, stdout, stderr := runCLI(context.Background(), []string{command, "-h"})
		if exitCode != 0 {
			t.Fatalf("%s -h exit = %d; want 0; stderr=%q", command, exitCode, stderr)
		}
		if stdout != "" {
			t.Fatalf("%s -h stdout = %q; want empty", command, stdout)
		}
		if !strings.Contains(stderr, "Usage of whiteboard "+command) {
			t.Fatalf("%s -h stderr = %q; want usage", command, stderr)
		}
	}
}

func TestEvidenceCommandArgumentsAreClosedBeforeProjectAccess(t *testing.T) {
	validDigest := "sha256:" + strings.Repeat("a", 64)
	tests := []struct {
		name string
		args []string
	}{
		{name: "run requires required", args: []string{"run", "--profile", "deep"}},
		{name: "run required must be true", args: []string{"run", "--required=false", "--profile", "deep"}},
		{name: "run requires explicit profile", args: []string{"run", "--required"}},
		{name: "run profile is closed", args: []string{"run", "--required", "--profile", "quick"}},
		{name: "report requires release", args: []string{"report"}},
		{name: "report rejects malformed release", args: []string{"report", "--release", "latest"}},
		{name: "report profile is closed", args: []string{"report", "--release", "current", "--profile", "quick"}},
		{name: "report format is closed", args: []string{"report", "--release", "current", "--format", "yaml"}},
		{name: "report rejects empty output", args: []string{"report", "--release", validDigest, "--output="}},
		{name: "report check requires output", args: []string{"report", "--release", "current", "--check"}},
		{name: "diff requires release", args: []string{"diff", "--left-evidence-root", "left", "--right-evidence-root", "right"}},
		{name: "diff requires left root", args: []string{"diff", "--release", "current", "--right-evidence-root", "right"}},
		{name: "diff requires right root", args: []string{"diff", "--release", "current", "--left-evidence-root", "left"}},
		{name: "diff is deep only", args: []string{"diff", "--release", "current", "--left-evidence-root", "left", "--right-evidence-root", "right", "--profile", "smoke"}},
		{name: "diff rejects empty output", args: []string{"diff", "--release", "current", "--left-evidence-root", "left", "--right-evidence-root", "right", "--output="}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCLI(context.Background(), test.args)
			if exitCode != ExitArgumentOrLoadFailure || stdout != "" || stderr == "" {
				t.Fatalf("Run(%q) exit=%d stdout=%q stderr=%q; want argument failure", test.args, exitCode, stdout, stderr)
			}
		})
	}
}

func TestEvidenceCommandsCompleteProvenanceBeforeProjectAccess(t *testing.T) {
	state := inputdigest.State{
		InputDigest:  inputdigest.Digest("sha256:" + strings.Repeat("a", 64)),
		SourceCommit: strings.Repeat("b", 40),
	}
	buildCases := []struct {
		name      string
		buildInfo func() (*debug.BuildInfo, bool)
		wantCode  string
	}{
		{
			name:      "unavailable",
			buildInfo: func() (*debug.BuildInfo, bool) { return nil, false },
			wantCode:  codeToolRevisionUnavailable,
		},
		{
			name: "stale",
			buildInfo: func() (*debug.BuildInfo, bool) {
				return matchingBuildInfoForApp(strings.Repeat("c", 40)), true
			},
			wantCode: codeToolRevisionMismatch,
		},
		{
			name: "modified",
			buildInfo: func() (*debug.BuildInfo, bool) {
				return buildInfoReplacing(matchingBuildInfoForApp(state.SourceCommit), "vcs.modified", "true"), true
			},
			wantCode: codeToolRevisionMismatch,
		},
		{
			name: "duplicate",
			buildInfo: func() (*debug.BuildInfo, bool) {
				return buildInfoWith(matchingBuildInfoForApp(state.SourceCommit), "vcs.revision", state.SourceCommit), true
			},
			wantCode: codeToolRevisionMismatch,
		},
	}
	commands := [][]string{
		{"run", "--required", "--profile", "deep", "--root", "repo", "--evidence-root", "evidence"},
		{"report", "--root", "repo", "--release", "current"},
		{"diff", "--root", "repo", "--release", "current", "--left-evidence-root", "left", "--right-evidence-root", "right"},
		{"validate", "--root", "repo", "--release", "current"},
	}
	for _, args := range commands {
		for _, buildCase := range buildCases {
			t.Run(args[0]+"/"+buildCase.name, func(t *testing.T) {
				operations := make([]string, 0)
				app := application{dependencies: applicationDependencies{
					computeState: func(context.Context, string) (inputdigest.State, error) {
						operations = append(operations, "state")
						return state, nil
					},
					readBuildInfo: func() (*debug.BuildInfo, bool) {
						operations = append(operations, "build")
						return buildCase.buildInfo()
					},
					loadCatalog: func(context.Context, string) (*catalog.Catalog, error) {
						operations = append(operations, "catalog")
						return nil, errors.New("must not load")
					},
					lookupExperiment: func(validator.MatrixCell) (experiments.Factory, bool) {
						operations = append(operations, "registry")
						return nil, false
					},
					newStore: func(string) (*evidence.Store, error) {
						operations = append(operations, "store")
						return nil, errors.New("must not open store")
					},
					newRunSetID: func(time.Time) (evidence.RunSetID, error) {
						operations = append(operations, "entropy")
						return "", errors.New("must not allocate identity")
					},
					runExperiment: func(context.Context, harness.RunSpec) (harness.RunResult, error) {
						operations = append(operations, "runner")
						return harness.RunResult{}, errors.New("must not run")
					},
				}}
				exitCode, stdout, stderr := runApplication(t, app, args)
				if exitCode != ExitArgumentOrLoadFailure || stdout != "" || !strings.Contains(stderr, buildCase.wantCode) {
					t.Fatalf("app.run(%q) exit=%d stdout=%q stderr=%q", args, exitCode, stdout, stderr)
				}
				if got, want := strings.Join(operations, ","), "state,build"; got != want {
					t.Fatalf("operations=%q; want %q", got, want)
				}
			})
		}
	}
}

func TestExplicitStaleReleaseStopsBeforeCatalogAccess(t *testing.T) {
	state := inputdigest.State{
		InputDigest:  inputdigest.Digest("sha256:" + strings.Repeat("a", 64)),
		SourceCommit: strings.Repeat("b", 40),
	}
	operations := make([]string, 0)
	app := application{dependencies: applicationDependencies{
		computeState: func(context.Context, string) (inputdigest.State, error) {
			operations = append(operations, "state")
			return state, nil
		},
		readBuildInfo: func() (*debug.BuildInfo, bool) {
			operations = append(operations, "build")
			return matchingBuildInfoForApp(state.SourceCommit), true
		},
		loadCatalog: func(context.Context, string) (*catalog.Catalog, error) {
			operations = append(operations, "catalog")
			return nil, errors.New("must not load")
		},
	}}
	stale := "sha256:" + strings.Repeat("c", 64)
	exitCode, stdout, stderr := runApplication(t, app, []string{"report", "--root", "repo", "--release", stale})
	if exitCode != ExitReleaseFailure || stdout != "" || !strings.Contains(stderr, codeReleaseInputDigestMismatch) {
		t.Fatalf("stale release exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if got, want := strings.Join(operations, ","), "state,build"; got != want {
		t.Fatalf("operations=%q; want %q", got, want)
	}
}

func TestEvidenceCommandArgumentErrorsDoNotEchoPathsOrControls(t *testing.T) {
	secret := "/absolute/workspace/secret\x1b[31m"
	commands := [][]string{
		{"run", "--required=" + secret, "--profile", "deep"},
		{"run", "--required", "--profile", secret},
		{"report", "--release", secret},
		{"report", "--release", "current", "--profile", secret},
		{"report", "--release", "current", "--format", secret},
		{"report", "--release", "current", secret},
		{"diff", "--release", secret, "--left-evidence-root", "left", "--right-evidence-root", "right"},
		{"diff", "--release", "current", "--left-evidence-root", "left", "--right-evidence-root", "right", "--profile", secret},
		{"diff", "--release", "current", "--left-evidence-root", "left", "--right-evidence-root", "right", "--format", secret},
		{"diff", "--release", "current", "--left-evidence-root", "left", "--right-evidence-root", "right", secret},
		{"validate", "--release", secret},
		{"validate", "--release", "current", "--format", secret},
		{secret},
	}
	for _, args := range commands {
		exitCode, stdout, stderr := runCLI(context.Background(), args)
		if exitCode != ExitArgumentOrLoadFailure || stdout != "" || stderr == "" {
			t.Fatalf("Run(%q) exit=%d stdout=%q stderr=%q", args, exitCode, stdout, stderr)
		}
		if strings.Contains(stderr, "/absolute/workspace/secret") || strings.ContainsAny(stderr, "\x1b\r") {
			t.Fatalf("argument error leaked path or controls: %q", stderr)
		}
	}
}

func TestEvidenceCommandPathResolutionErrorsDoNotEchoPathsOrControls(t *testing.T) {
	secret := "/absolute/workspace/secret\x1b[31m\nforged"
	state := runTestState("a", "b")
	app := application{dependencies: applicationDependencies{
		computeState:  func(context.Context, string) (inputdigest.State, error) { return state, nil },
		readBuildInfo: func() (*debug.BuildInfo, bool) { return matchingBuildInfoForApp(state.SourceCommit), true },
		resolvePolicyPath: func(string) (string, error) {
			return "", errors.New(secret)
		},
	}}
	commands := [][]string{
		{"run", "--required", "--profile", "deep", "--evidence-root", secret},
		{"report", "--release", "current", "--evidence-root", secret},
		{"diff", "--release", "current", "--left-evidence-root", secret, "--right-evidence-root", secret},
		{"validate", "--release", "current", "--evidence-root", secret},
	}
	for _, args := range commands {
		exitCode, stdout, stderr := runApplication(t, app, args)
		if exitCode != ExitArgumentOrLoadFailure || stdout != "" || stderr == "" {
			t.Fatalf("app.run(%q) exit=%d stdout=%q stderr=%q", args, exitCode, stdout, stderr)
		}
		if strings.Contains(stderr, "/absolute/workspace/secret") || strings.ContainsAny(stderr, "\x1b\r") {
			t.Fatalf("path resolution error leaked path or controls: %q", stderr)
		}
	}
}

func TestEvidenceCommandsGiveOperationalErrorsPrecedenceOverSemanticStorageErrors(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	fixture.dependencies.loadAndSyncManifest = func(context.Context, string, inputdigest.Digest) (release.Manifest, error) {
		return release.Manifest{}, errors.Join(
			release.ErrSnapshotNotFound,
			release.ErrSnapshotUnsafePath,
			release.ErrSnapshotCorrupt,
			release.ErrSnapshotIO,
			context.Canceled,
			errors.New("/absolute/workspace/secret"),
		)
	}
	storeCalls := 0
	fixture.dependencies.openStoreReadOnly = func(string) (*evidence.Store, error) {
		storeCalls++
		return nil, nil
	}
	commands := [][]string{
		{"run", "--required", "--profile", "deep", "--snapshot", "--evidence-root", "external"},
		{"report", "--release", "current", "--evidence-root", "external"},
		{"diff", "--release", "current", "--left-evidence-root", "left", "--right-evidence-root", "right"},
		{"validate", "--release", "current", "--evidence-root", "external"},
	}
	for _, args := range commands {
		exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, args)
		if exitCode != ExitArgumentOrLoadFailure || stdout != "" || !strings.Contains(stderr, "release_snapshot_io") {
			t.Fatalf("app.run(%q) exit=%d stdout=%q stderr=%q", args, exitCode, stdout, stderr)
		}
		if strings.Contains(stderr, release.CodeReleaseManifestInvalid) || strings.Contains(stderr, "/absolute/workspace/secret") {
			t.Fatalf("operational precedence failed or leaked path: %q", stderr)
		}
	}
	if storeCalls != 0 {
		t.Fatalf("operational manifest failures opened evidence store %d times", storeCalls)
	}
}

func TestEvidenceCommandsGiveSemanticInvalidPrecedenceOverMissingStorage(t *testing.T) {
	commands := [][]string{
		{"run", "--required", "--profile", "deep", "--snapshot", "--evidence-root", "external"},
		{"report", "--release", "current", "--evidence-root", "external"},
		{"diff", "--release", "current", "--left-evidence-root", "left", "--right-evidence-root", "right"},
		{"validate", "--release", "current", "--evidence-root", "external"},
	}
	for _, args := range commands {
		fixture := newRunApplicationFixture(t, 1)
		fixture.dependencies.loadAndSyncManifest = func(context.Context, string, inputdigest.Digest) (release.Manifest, error) {
			return release.Manifest{}, errors.Join(
				release.ErrSnapshotNotFound,
				release.ErrSnapshotUnsafePath,
				errors.New("/absolute/workspace/secret"),
			)
		}
		storeCalls := 0
		fixture.dependencies.newStore = func(string) (*evidence.Store, error) {
			storeCalls++
			return nil, errors.New("new store must not be reached")
		}
		exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, args)
		if exitCode != ExitReleaseFailure || stdout != "" || !strings.Contains(stderr, release.CodeReleaseManifestInvalid) {
			t.Fatalf("app.run(%q) exit=%d stdout=%q stderr=%q", args, exitCode, stdout, stderr)
		}
		if strings.Contains(stderr, release.CodeReleaseSnapshotMissing) || strings.Contains(stderr, "/absolute/workspace/secret") {
			t.Fatalf("semantic invalid precedence failed or leaked path: %q", stderr)
		}
		if storeCalls != 0 {
			t.Fatalf("app.run(%q) opened mutable evidence store %d times", args, storeCalls)
		}
	}
}

func TestReadOnlyEvidenceCommandsDoNotCreateMissingRunsDirectory(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	fixture.dependencies.runExperiment = func(_ context.Context, spec harness.RunSpec) (harness.RunResult, error) {
		return runResultForSpec(spec, true), nil
	}
	fixture.dependencies.validateContent = func(string, *catalog.Catalog) content.Result {
		return content.Result{Diagnostics: []validator.Diagnostic{}}
	}
	seedRoot := filepath.Join(t.TempDir(), "seed-evidence")
	seedArgs := []string{"run", "--required", "--profile", "deep", "--snapshot", "--root", "repo", "--evidence-root", seedRoot}
	if exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, seedArgs); exitCode != ExitSuccess || stdout != "" || stderr != "" {
		t.Fatalf("seed snapshot exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	canonicalSeed, err := canonicalPolicyPath(seedRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := release.LoadManifest(context.Background(), canonicalSeed, fixture.state.InputDigest)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args func(string) []string
	}{
		{name: "run preflight", args: func(root string) []string {
			return []string{"run", "--required", "--profile", "deep", "--snapshot", "--root", "repo", "--evidence-root", root}
		}},
		{name: "report", args: func(root string) []string {
			return []string{"report", "--root", "repo", "--release", "current", "--evidence-root", root}
		}},
		{name: "diff right", args: func(root string) []string {
			return []string{"diff", "--root", "repo", "--release", "current", "--left-evidence-root", filepath.Join(t.TempDir(), "left"), "--right-evidence-root", root}
		}},
		{name: "validate", args: func(root string) []string {
			return []string{"validate", "--root", "repo", "--release", "current", "--evidence-root", root}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidenceRoot := filepath.Join(t.TempDir(), "evidence")
			canonicalRoot, err := canonicalPolicyPath(evidenceRoot)
			if err != nil {
				t.Fatal(err)
			}
			if err := release.WriteManifest(context.Background(), canonicalRoot, manifest); err != nil {
				t.Fatal(err)
			}
			runs := filepath.Join(canonicalRoot, "runs")
			if _, err := os.Lstat(runs); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("fixture runs exists before command: %v", err)
			}

			exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, test.args(evidenceRoot))
			if exitCode != ExitReleaseFailure || stdout != "" || !strings.Contains(stderr, release.CodeReleaseEvidenceMissing) {
				t.Fatalf("command exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
			}
			if _, err := os.Lstat(runs); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("read-only command created runs directory: %v", err)
			}
		})
	}
}

func TestEvidenceCommandsSanitizeCatalogDiagnosticsBeforeReleaseOperations(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	secret := "/absolute/workspace/secret\x1b[31m\nforged"
	fixture.dependencies.validateCatalog = func(*catalog.Catalog, validator.Mode) validator.Report {
		return validator.Report{
			Diagnostics: []validator.Diagnostic{{
				Code: validator.CodeInvalidStableID, Severity: secret, Path: secret, EntityID: secret, Message: secret,
			}},
			Coverage: validator.Coverage{MissingCaseIDs: []string{}, UnexpectedCaseIDs: []string{}},
			Matrix:   fixture.cells,
		}
	}
	commands := [][]string{
		{"run", "--required", "--profile", "deep", "--evidence-root", "external"},
		{"report", "--release", "current", "--evidence-root", "external"},
		{"diff", "--release", "current", "--left-evidence-root", "left", "--right-evidence-root", "right"},
	}
	for _, args := range commands {
		exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, args)
		if exitCode != ExitDevelopmentFailure || stdout != "" || !strings.Contains(stderr, validator.CodeInvalidStableID) {
			t.Fatalf("app.run(%q) exit=%d stdout=%q stderr=%q", args, exitCode, stdout, stderr)
		}
		if strings.Contains(stderr, "/absolute/workspace/secret") || strings.ContainsAny(stderr, "\x1b\r") {
			t.Fatalf("catalog diagnostic leaked path or controls: %q", stderr)
		}
	}
}

func TestSanitizeReleaseDiagnosticsProjectsOnlyStableSafeFields(t *testing.T) {
	secret := "/absolute/workspace/secret\x1b[31m\nforged"
	runID := "run-20000101T000000.000Z-00000000000000000000000000000000"
	runSetID := "set-20000101T000000.000Z-00000000000000000000000000000000"
	coverage := validator.Coverage{
		BaselineTotal:     75,
		CompleteTotal:     1,
		MissingCaseIDs:    []string{"case-a", "case-b", secret},
		UnexpectedCaseIDs: []string{},
	}
	source := []validator.Diagnostic{
		{Code: release.CodeReleaseEvidenceMissing, Severity: "warning", Path: secret, EntityID: runID, Message: secret},
		{Code: release.CodeReleaseRunSetMismatch, Severity: "fatal", Path: secret, EntityID: runSetID, Message: secret},
		{Code: release.CodeReleaseCellMismatch, Severity: "warning", Path: secret, EntityID: "lab-one", Message: secret},
		{Code: validator.CodeReleaseScopeIncomplete, Severity: "warning", Path: secret, EntityID: secret, Message: secret},
		{Code: "attacker_controlled", Severity: secret, Path: secret, EntityID: secret, Message: secret},
	}
	got := sanitizeReleaseDiagnostics(source, &coverage)
	if len(got) != len(source) {
		t.Fatalf("sanitized diagnostics=%#v; want %d entries", got, len(source))
	}
	allowed := map[string]bool{
		release.CodeReleaseEvidenceMissing:   true,
		release.CodeReleaseRunSetMismatch:    true,
		release.CodeReleaseCellMismatch:      true,
		validator.CodeReleaseScopeIncomplete: true,
		release.CodeReleaseManifestInvalid:   true,
	}
	for _, diagnostic := range got {
		if !allowed[diagnostic.Code] || diagnostic.Severity != "error" || diagnostic.Path != "" || diagnostic.Message == "" {
			t.Fatalf("unsafe diagnostic projection: %#v", diagnostic)
		}
		if strings.Contains(diagnostic.Message, "/absolute/workspace/secret") || strings.ContainsAny(diagnostic.Message, "\x1b\r\n") {
			t.Fatalf("diagnostic message leaked attacker input: %#v", diagnostic)
		}
		if diagnostic.EntityID == runID || diagnostic.EntityID == runSetID || strings.Contains(diagnostic.EntityID, "/") {
			t.Fatalf("diagnostic entity leaked ephemeral identity: %#v", diagnostic)
		}
		if diagnostic.Code == release.CodeReleaseCellMismatch && diagnostic.EntityID != "lab-one" {
			t.Fatalf("stable lab entity was not preserved: %#v", diagnostic)
		}
		if diagnostic.Code == validator.CodeReleaseScopeIncomplete {
			if !strings.Contains(diagnostic.Message, "complete=1 baseline=75 missing=2 unexpected=0 missing_ids=[case-a case-b]") || strings.Contains(diagnostic.Message, "/absolute/workspace/secret") {
				t.Fatalf("scope message did not project trusted stable coverage IDs: %#v", diagnostic)
			}
		}
	}
	if evidence.ValidateID(runID) != nil || evidence.ValidateRunSetID(evidence.RunSetID(runSetID)) != nil {
		t.Fatal("test fixture IDs are invalid")
	}
}

func matchingBuildInfoForApp(revision string) *debug.BuildInfo {
	return &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: revision},
		{Key: "vcs.modified", Value: "false"},
	}}
}

func runApplication(t *testing.T, app application, args []string) (int, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := app.run(context.Background(), args, &stdout, &stderr)
	return exitCode, stdout.String(), stderr.String()
}

func TestRunMapsEveryAttemptedWriterFailureToExitTwo(t *testing.T) {
	emptyRoot := writeEmptyCatalogRoot(t)
	invalidRoot := writeInvalidCompleteCaseRoot(t)
	missingOutput := filepath.Join(t.TempDir(), "missing.txt")
	staleOutput := filepath.Join(t.TempDir(), "coverage.txt")
	writeCLIFile(t, staleOutput, "stale\n")

	scenarios := []struct {
		name   string
		args   []string
		target string
	}{
		{name: "missing command", args: nil, target: "stderr"},
		{name: "help", args: []string{"validate", "-h"}, target: "stderr"},
		{name: "flag parse", args: []string{"coverage", "--unknown"}, target: "stderr"},
		{name: "validate stdout", args: []string{"validate", "--root", emptyRoot}, target: "stdout"},
		{name: "validate diagnostics", args: []string{"validate", "--root", invalidRoot}, target: "stderr"},
		{name: "coverage stdout", args: []string{"coverage", "--root", emptyRoot}, target: "stdout"},
		{name: "coverage diagnostics", args: []string{"coverage", "--root", invalidRoot}, target: "stderr"},
		{name: "check missing", args: []string{"coverage", "--root", emptyRoot, "--output", missingOutput, "--check"}, target: "stderr"},
		{name: "check mismatch", args: []string{"coverage", "--root", emptyRoot, "--output", staleOutput, "--check"}, target: "stderr"},
	}
	writers := []struct {
		name string
		new  func() io.Writer
	}{
		{name: "short", new: func() io.Writer { return shortWriter{} }},
		{name: "error", new: func() io.Writer { return failingWriter{} }},
	}
	for _, writer := range writers {
		for _, scenario := range scenarios {
			t.Run(writer.name+"/"+scenario.name, func(t *testing.T) {
				stdout := io.Writer(io.Discard)
				stderr := io.Writer(io.Discard)
				if scenario.target == "stdout" {
					stdout = writer.new()
				} else {
					stderr = writer.new()
				}
				if exitCode := Run(context.Background(), scenario.args, stdout, stderr); exitCode != ExitArgumentOrLoadFailure {
					t.Fatalf("Run(%q) exit=%d; want %d after %s writer failure", scenario.args, exitCode, ExitArgumentOrLoadFailure, scenario.target)
				}
			})
		}
	}
}

func TestTextDiagnosticsEscapeRepositoryControlledFields(t *testing.T) {
	diagnostic := validator.Diagnostic{
		Code:     "missing_link_target",
		Severity: "error",
		Path:     "bad\n\x1b[31merror [forged].md",
		EntityID: "entity\r\x1b[32m",
		Message:  "missing\n\x1b[33mtarget",
	}
	var rendered bytes.Buffer
	if err := writeDiagnostics(&rendered, []validator.Diagnostic{diagnostic}); err != nil {
		t.Fatal(err)
	}
	text := rendered.String()
	if strings.Count(text, "\n") != 1 || strings.ContainsAny(text, "\r\x1b") {
		t.Fatalf("text diagnostic contains injectable controls: %q", text)
	}
	for _, escaped := range []string{`\n`, `\r`, `\x1b`} {
		if !strings.Contains(text, escaped) {
			t.Fatalf("text diagnostic %q does not visibly escape %q", text, escaped)
		}
	}
	physicalLines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(physicalLines) != 1 || !strings.HasPrefix(physicalLines[0], "error [") {
		t.Fatalf("text diagnostic forged a physical diagnostic line: %q", text)
	}

	root := writeEmptyCatalogRoot(t)
	maliciousName := "bad\n\x1b[31merror [forged].md"
	writeCLIFile(t, filepath.Join(root, maliciousName), "[broken](missing.md)\n")
	exitCode, stdout, stderr := runCLI(context.Background(), []string{"validate", "--root", root, "--content"})
	if exitCode != ExitDevelopmentFailure || stdout != "" {
		t.Fatalf("validate exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	physicalLines = strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
	if len(physicalLines) != 1 || !strings.HasPrefix(physicalLines[0], "error [") || strings.ContainsAny(stderr, "\r\x1b") {
		t.Fatalf("CLI text diagnostic is injectable: %q", stderr)
	}

	var jsonOutput bytes.Buffer
	if err := renderValidation(&jsonOutput, "json", validator.Report{Diagnostics: []validator.Diagnostic{diagnostic}}); err != nil {
		t.Fatal(err)
	}
	var decoded validator.Report
	if err := json.Unmarshal(jsonOutput.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Diagnostics) != 1 || decoded.Diagnostics[0] != diagnostic {
		t.Fatalf("JSON diagnostic=%#v; want structured raw value %#v", decoded.Diagnostics, diagnostic)
	}
}

type shortWriter struct{}

func (shortWriter) Write(value []byte) (int, error) {
	if len(value) == 0 {
		return 0, nil
	}
	return len(value) - 1, nil
}

func runCLI(ctx context.Context, args []string) (int, string, string) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(ctx, args, &stdout, &stderr)
	return exitCode, stdout.String(), stderr.String()
}

func writeEmptyCatalogRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeCLIFile(t, filepath.Join(root, "scope.yaml"), `schema_version: 1
families: []
cases: []
exclusions: []
`)
	writeCLIFile(t, filepath.Join(root, "sources.yaml"), `schema_version: 1
sources: []
`)
	writeCLIFile(t, filepath.Join(root, "aliases.yaml"), `schema_version: 1
aliases: []
`)
	return root
}

func writeInvalidCompleteCaseRoot(t *testing.T) string {
	t.Helper()
	root := writeEmptyCatalogRoot(t)
	writeCLIFile(t, filepath.Join(root, "scope.yaml"), `schema_version: 1
families:
  - id: addressing-traffic
    title: Addressing and traffic
cases:
  - id: case-one
    title: Case One
    primary_family: addressing-traffic
exclusions: []
`)
	writeCLIFile(t, filepath.Join(root, "cases", "case-one", "case.yaml"), `schema_version: 1
id: case-one
title: Case One
primary_family: addressing-traffic
required: true
status: complete
`)
	writeCLIFile(t, filepath.Join(root, "cases", "case-one", "README.md"), "# 空壳\n")
	return root
}

func writeCLIFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
