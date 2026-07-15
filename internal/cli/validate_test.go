package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
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

func TestValidateCommandDevelopmentSuccessAndJSONRendering(t *testing.T) {
	root := writeEmptyCatalogRoot(t)
	exitCode, stdout, stderr := runCLI(context.Background(), []string{"validate", "--root", root})
	if exitCode != 0 || stdout != "validation passed\n" || stderr != "" {
		t.Fatalf("validate exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}

	exitCode, stdout, stderr = runCLI(context.Background(), []string{"validate", "--root", root, "--format", "json"})
	if exitCode != 0 || stderr != "" {
		t.Fatalf("validate json exit=%d stderr=%q", exitCode, stderr)
	}
	if !strings.HasPrefix(stdout, "{\n  \"diagnostics\": []") || !strings.HasSuffix(stdout, "\n") || strings.HasSuffix(stdout, "\n\n") {
		t.Fatalf("validate json stdout = %q; want two-space JSON and one newline", stdout)
	}
}

func TestValidateCommandContentIsNeverANoOpAndReleaseImpliesContent(t *testing.T) {
	root := writeInvalidCompleteCaseRoot(t)

	exitCode, stdout, stderr := runCLI(context.Background(), []string{"validate", "--root", root})
	if exitCode != 3 || stdout != "" {
		t.Fatalf("plain validate exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if !strings.Contains(stderr, "complete_contract_empty") || strings.Contains(stderr, "heading_contract_mismatch") {
		t.Fatalf("plain validate stderr = %q; want semantic diagnostics only", stderr)
	}

	exitCode, stdout, stderr = runCLI(context.Background(), []string{"validate", "--root", root, "--content"})
	if exitCode != 3 || stdout != "" || !strings.Contains(stderr, "heading_contract_mismatch") {
		t.Fatalf("content validate exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}

	state := runTestState("a", "b")
	app := application{dependencies: applicationDependencies{
		computeState:  func(context.Context, string) (inputdigest.State, error) { return state, nil },
		readBuildInfo: func() (*debug.BuildInfo, bool) { return matchingBuildInfoForApp(state.SourceCommit), true },
	}}
	exitCode, stdout, stderr = runApplication(t, app, []string{"validate", "--root", root, "--content=false", "--release", "current"})
	if exitCode != 4 || stdout != "" || !strings.Contains(stderr, "heading_contract_mismatch") {
		t.Fatalf("release validate exit=%d stdout=%q stderr=%q; release must imply content", exitCode, stdout, stderr)
	}
}

func TestValidateCommandLocksArgumentsAndReleaseDigest(t *testing.T) {
	root := writeEmptyCatalogRoot(t)
	validDigest := "sha256:" + strings.Repeat("a", 64)
	tests := []struct {
		name string
		args []string
	}{
		{name: "unknown_flag", args: []string{"validate", "--unknown"}},
		{name: "positional", args: []string{"validate", "extra"}},
		{name: "bad_format", args: []string{"validate", "--format", "markdown"}},
		{name: "empty_release", args: []string{"validate", "--release="}},
		{name: "bad_release", args: []string{"validate", "--release", "latest"}},
		{name: "uppercase_digest", args: []string{"validate", "--release", "sha256:" + strings.Repeat("A", 64)}},
		{name: "short_digest", args: []string{"validate", "--release", "sha256:abc"}},
		{name: "invalid_before_load", args: []string{"validate", "--root", "missing", "--release", "invalid"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCLI(context.Background(), test.args)
			if exitCode != 2 || stdout != "" || stderr == "" {
				t.Fatalf("Run(%q) exit=%d stdout=%q stderr=%q", test.args, exitCode, stdout, stderr)
			}
		})
	}

	state := inputdigest.State{InputDigest: inputdigest.Digest(validDigest), SourceCommit: strings.Repeat("b", 40)}
	app := application{dependencies: applicationDependencies{
		computeState:  func(context.Context, string) (inputdigest.State, error) { return state, nil },
		readBuildInfo: func() (*debug.BuildInfo, bool) { return matchingBuildInfoForApp(state.SourceCommit), true },
	}}
	for _, releaseInput := range []string{"current", validDigest} {
		exitCode, stdout, stderr := runApplication(t, app, []string{"validate", "--root", root, "--release", releaseInput})
		if exitCode != ExitReleaseFailure || stdout != "" || !strings.Contains(stderr, release.CodeReleaseSnapshotMissing) {
			t.Fatalf("release %q exit=%d stdout=%q stderr=%q", releaseInput, exitCode, stdout, stderr)
		}
	}
}

func TestValidateReleaseMergesScopeContentAndAuditDiagnostics(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	secret := "/absolute/workspace/secret\x1b[31m\nforged"
	fixture.dependencies.validateCatalog = func(_ *catalog.Catalog, mode validator.Mode) validator.Report {
		if mode != validator.ModeRelease {
			t.Fatalf("validation mode=%q; want release", mode)
		}
		return validator.Report{
			Diagnostics: []validator.Diagnostic{{Code: validator.CodeReleaseScopeIncomplete, Severity: "error", Message: "complete=1 baseline=75 missing=74 unexpected=0"}},
			Coverage:    validator.Coverage{BaselineTotal: 75, CompleteTotal: 1, MissingCaseIDs: []string{"case-missing"}, UnexpectedCaseIDs: []string{}, Families: []validator.FamilyCoverage{}, RequiredPrinciples: []string{}, RequiredScenarioLabs: []string{}, RequiredPrimitiveLabs: []string{}, RequiredAdapters: []string{}},
			Matrix:      fixture.cells,
		}
	}
	fixture.dependencies.validateContent = func(string, *catalog.Catalog) content.Result {
		return content.Result{Diagnostics: []validator.Diagnostic{{Code: content.CodeHeadingContractMismatch, Severity: "error", Message: "heading differs"}}}
	}
	fixture.dependencies.loadAndSyncManifest = func(context.Context, string, inputdigest.Digest) (release.Manifest, error) {
		return release.Manifest{InputDigest: fixture.state.InputDigest, Profile: evidence.ProfileDeep}, nil
	}
	fixture.dependencies.openStoreReadOnly = func(string) (*evidence.Store, error) { return nil, nil }
	fixture.dependencies.auditSnapshot = func(context.Context, release.Manifest, []release.ExpectedCell, *evidence.Store) (release.AuditedSnapshot, []validator.Diagnostic, error) {
		return release.AuditedSnapshot{}, []validator.Diagnostic{{
			Code: release.CodeReleaseEvidenceMissing, Severity: "fatal", Path: secret,
			EntityID: runTestEvidenceID(0), Message: secret,
		}}, nil
	}
	exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
		"validate", "--root", t.TempDir(), "--release", "current", "--format", "text",
	})
	if exitCode != ExitReleaseFailure || stdout != "" {
		t.Fatalf("release validation exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	codes := []string{content.CodeHeadingContractMismatch, release.CodeReleaseEvidenceMissing, validator.CodeReleaseScopeIncomplete}
	previous := -1
	for _, code := range codes {
		index := strings.Index(stderr, "["+code+"]")
		if index < 0 || index <= previous {
			t.Fatalf("diagnostics are absent or unsorted in %q; code=%q index=%d previous=%d", stderr, code, index, previous)
		}
		previous = index
	}
	if strings.Count(strings.TrimSpace(stderr), "\n") != 2 {
		t.Fatalf("stderr=%q; want exactly three diagnostics", stderr)
	}
	if strings.Contains(stderr, "/absolute/workspace/secret") || strings.Contains(stderr, "run-20000101") || strings.ContainsAny(stderr, "\x1b\r") {
		t.Fatalf("release validation leaked untrusted diagnostic fields: %q", stderr)
	}
}

func TestValidateReleaseJSONProjectsCoverageAndMatrixWithoutLeakingInvalidIDs(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	secret := "/absolute/workspace/secret\x1b[31m\nforged"
	fixture.dependencies.validateCatalog = func(_ *catalog.Catalog, mode validator.Mode) validator.Report {
		if mode != validator.ModeRelease {
			t.Fatalf("validation mode=%q; want release", mode)
		}
		return validator.Report{
			Diagnostics: []validator.Diagnostic{
				{Code: validator.CodeInvalidStableID, Severity: secret, Path: secret, EntityID: secret, Message: secret},
				{Code: validator.CodeReleaseScopeIncomplete, Severity: "error", Message: secret},
			},
			Coverage: validator.Coverage{
				BaselineTotal:         75,
				CompleteTotal:         1,
				MissingCaseIDs:        []string{"case-safe", secret},
				UnexpectedCaseIDs:     []string{secret},
				Families:              []validator.FamilyCoverage{{ID: secret, Required: 1, Complete: 0}},
				RequiredPrinciples:    []string{"principle-safe", secret},
				RequiredScenarioLabs:  []string{secret},
				RequiredPrimitiveLabs: []string{"lab-safe"},
				RequiredAdapters:      []string{secret},
			},
			Matrix: []validator.MatrixCell{{
				LabID: secret, RequiredRunID: secret, BindingID: secret, ClaimID: secret,
				Role: secret, ImplementationID: secret, AdapterID: secret, Workload: secret,
				Faults: []string{secret}, AssertionIDs: []string{secret},
			}},
		}
	}
	fixture.dependencies.validateContent = func(string, *catalog.Catalog) content.Result {
		return content.Result{Diagnostics: []validator.Diagnostic{}}
	}
	fixture.dependencies.loadAndSyncManifest = func(context.Context, string, inputdigest.Digest) (release.Manifest, error) {
		return release.Manifest{}, release.ErrSnapshotNotFound
	}
	exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
		"validate", "--root", t.TempDir(), "--release", "current", "--format", "json",
	})
	if exitCode != ExitReleaseFailure || stdout != "" || !strings.HasPrefix(stderr, "{\n") {
		t.Fatalf("release JSON exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if strings.Contains(stderr, "/absolute/workspace/secret") || strings.ContainsAny(stderr, "\x1b\r") {
		t.Fatalf("release JSON leaked invalid coverage or matrix fields: %q", stderr)
	}
	if !strings.Contains(stderr, "case-safe") || !strings.Contains(stderr, "principle-safe") || !strings.Contains(stderr, "lab-safe") {
		t.Fatalf("release JSON discarded safe stable IDs: %q", stderr)
	}
}

func TestValidateReleaseMergesUnsafeEvidenceStoreAsSemanticDiagnostic(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	fixture.dependencies.validateCatalog = func(_ *catalog.Catalog, _ validator.Mode) validator.Report {
		return validator.Report{
			Diagnostics: []validator.Diagnostic{{Code: validator.CodeReleaseScopeIncomplete, Severity: "error", Message: "scope incomplete"}},
			Coverage:    validator.Coverage{MissingCaseIDs: []string{}, UnexpectedCaseIDs: []string{}, Families: []validator.FamilyCoverage{}, RequiredPrinciples: []string{}, RequiredScenarioLabs: []string{}, RequiredPrimitiveLabs: []string{}, RequiredAdapters: []string{}},
			Matrix:      fixture.cells,
		}
	}
	fixture.dependencies.validateContent = func(string, *catalog.Catalog) content.Result {
		return content.Result{Diagnostics: []validator.Diagnostic{}}
	}
	fixture.dependencies.loadAndSyncManifest = func(context.Context, string, inputdigest.Digest) (release.Manifest, error) {
		return release.Manifest{InputDigest: fixture.state.InputDigest, Profile: evidence.ProfileDeep}, nil
	}
	fixture.dependencies.openStoreReadOnly = func(string) (*evidence.Store, error) {
		return nil, errors.Join(evidence.ErrEvidenceUnsafePath, errors.New("/absolute/workspace/secret"))
	}
	exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{"validate", "--root", t.TempDir(), "--release", "current"})
	if exitCode != ExitReleaseFailure || stdout != "" || !strings.Contains(stderr, release.CodeReleaseEvidenceInvalid) || !strings.Contains(stderr, validator.CodeReleaseScopeIncomplete) {
		t.Fatalf("unsafe store validation exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if strings.Contains(stderr, "/absolute/workspace/secret") {
		t.Fatalf("stderr leaked absolute path: %q", stderr)
	}
}

func TestW0ReleaseScopeContract(t *testing.T) {
	valid := newW0ReleaseFixture(t)
	exitCode, stdout, stderr := runApplication(t, valid.app, valid.validateArgs)
	if exitCode != ExitReleaseFailure || stdout != "" {
		t.Fatalf("W0 release validation exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if strings.Count(strings.TrimSuffix(stderr, "\n"), "\n") != 0 || !strings.Contains(stderr, "["+validator.CodeReleaseScopeIncomplete+"]") {
		t.Fatalf("W0 release diagnostics=%q; want sole scope diagnostic", stderr)
	}
	for _, summary := range []string{"complete=1 baseline=75 missing=74 unexpected=0", "missing_ids=[" + strings.Join(w0MissingCaseIDs(), " ") + "]"} {
		if !strings.Contains(stderr, summary) {
			t.Fatalf("W0 scope diagnostic does not contain %q: %q", summary, stderr)
		}
	}

	defective := newW0ReleaseFixture(t)
	canonicalRoot, err := canonicalPolicyPath(defective.evidenceRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := release.LoadManifest(context.Background(), canonicalRoot, defective.state.InputDigest)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Selections) != 6 {
		t.Fatalf("snapshot selections=%d; want six", len(manifest.Selections))
	}
	missingID := manifest.Selections[0].EvidenceID
	if err := os.Remove(filepath.Join(canonicalRoot, "runs", missingID+".json")); err != nil {
		t.Fatal(err)
	}
	exitCode, stdout, stderr = runApplication(t, defective.app, defective.validateArgs)
	if exitCode != ExitReleaseFailure || stdout != "" || !strings.Contains(stderr, validator.CodeReleaseScopeIncomplete) || !strings.Contains(stderr, release.CodeReleaseEvidenceMissing) {
		t.Fatalf("W0 defective evidence validation exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}

type w0ReleaseFixture struct {
	repositoryRoot string
	evidenceRoot   string
	state          inputdigest.State
	app            application
	validateArgs   []string
}

func newW0ReleaseFixture(t *testing.T) w0ReleaseFixture {
	t.Helper()
	repositoryRoot := newCommittedW0Repository(t)
	state, err := inputdigest.ComputeState(context.Background(), repositoryRoot)
	if err != nil {
		t.Fatalf("compute committed W0 source state: %v", err)
	}
	evidenceRoot := filepath.Join(t.TempDir(), "evidence")
	evidenceIndex := 0
	app := application{dependencies: applicationDependencies{
		readBuildInfo: func() (*debug.BuildInfo, bool) { return matchingBuildInfoForApp(state.SourceCommit), true },
		now: func() time.Time {
			return time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		},
		newRunSetID: func(time.Time) (evidence.RunSetID, error) {
			return runTestRunSetID(), nil
		},
		newEvidenceID: func(time.Time) (string, error) {
			evidenceIndex++
			return fmt.Sprintf("run-20000101T000000.000Z-%032x", evidenceIndex), nil
		},
	}}
	runExit, runStdout, runStderr := runApplication(t, app, []string{
		"run", "--required", "--profile", "deep", "--snapshot", "--root", repositoryRoot, "--evidence-root", evidenceRoot,
	})
	if runExit != ExitSuccess || runStdout != "" || runStderr != "" {
		t.Fatalf("W0 fixture run exit=%d stdout=%q stderr=%q", runExit, runStdout, runStderr)
	}
	return w0ReleaseFixture{
		repositoryRoot: repositoryRoot,
		evidenceRoot:   evidenceRoot,
		state:          state,
		app:            app,
		validateArgs:   []string{"validate", "--root", repositoryRoot, "--evidence-root", evidenceRoot, "--release", "current"},
	}
}

func newCommittedW0Repository(t *testing.T) string {
	t.Helper()
	sourceRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve source fixture root: %v", err)
	}
	repositoryRoot := filepath.Join(t.TempDir(), "committed-w0")
	if err := os.Mkdir(repositoryRoot, 0o755); err != nil {
		t.Fatalf("create W0 fixture root: %v", err)
	}
	for _, relative := range []string{"scope.yaml", "sources.yaml", "aliases.yaml", "cases", "principles", "labs"} {
		copyW0FixturePath(t, filepath.Join(sourceRoot, relative), filepath.Join(repositoryRoot, relative))
	}
	runW0FixtureGit(t, repositoryRoot, "init", "--quiet")
	runW0FixtureGit(t, repositoryRoot, "add", "--all")
	runW0FixtureGit(
		t,
		repositoryRoot,
		"-c", "user.name=W0 Fixture",
		"-c", "user.email=w0-fixture@example.invalid",
		"-c", "commit.gpgsign=false",
		"commit", "--quiet", "-m", "test: commit W0 source fixture",
	)
	return repositoryRoot
}

func copyW0FixturePath(t *testing.T, source, destination string) {
	t.Helper()
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatalf("inspect W0 fixture source: %v", err)
	}
	if !info.IsDir() {
		copyW0FixtureFile(t, source, destination, info)
		return
	}
	if err := filepath.Walk(source, func(path string, current os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("fixture source contains symlink")
		}
		relative, relErr := filepath.Rel(source, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(destination, relative)
		if current.IsDir() {
			return os.MkdirAll(target, current.Mode().Perm())
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(target, data, current.Mode().Perm())
	}); err != nil {
		t.Fatalf("copy W0 fixture tree: %v", err)
	}
}

func copyW0FixtureFile(t *testing.T, source, destination string, info os.FileInfo) {
	t.Helper()
	if !info.Mode().IsRegular() {
		t.Fatal("W0 fixture source must be a regular file")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read W0 fixture file: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatalf("create W0 fixture parent: %v", err)
	}
	if err := os.WriteFile(destination, data, info.Mode().Perm()); err != nil {
		t.Fatalf("write W0 fixture file: %v", err)
	}
}

func runW0FixtureGit(t *testing.T, root string, arguments ...string) {
	t.Helper()
	commandArguments := append([]string{"-C", root}, arguments...)
	command := exec.Command("git", commandArguments...)
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git fixture command failed: %v: %s", err, output)
	}
}

func w0MissingCaseIDs() []string {
	return []string{
		"ad-click-aggregation", "ad-serving-ranking", "appointment-booking", "autocomplete", "bank-transfer", "batch-data-pipeline", "cdn", "centralized-logging", "chat-messenger", "ci-runner", "cloud-file-sync", "code-assistant", "collaborative-editor", "comments-reactions", "configuration-feature-flags", "consistent-hash-router", "container-orchestrator", "dag-workflow", "deployment-system", "distributed-cache", "distributed-email-service", "distributed-id", "distributed-log-message-queue", "distributed-sql", "dns-service-discovery", "double-entry-ledger-wallet", "ecommerce-order-inventory", "embedding-index-pipeline", "food-delivery-dispatch", "full-text-search", "gpu-scheduler", "identity-authorization-service", "image-service", "inference-gateway", "job-scheduler", "key-value-store", "large-file-transfer", "leaderboard", "live-comments", "live-streaming", "llm-chat-serving", "load-balancer-api-gateway", "maps-navigation", "metrics-monitoring-alerting", "multi-tenant-cloud-control-plane", "music-podcast-streaming", "nearby-places", "notification-delivery", "object-storage", "online-auction", "pastebin", "payment-system", "photo-sharing", "presence-service", "pubsub", "qa-news-aggregation", "rag-assistant", "recommendation-system", "ride-hailing-dispatch", "social-graph", "social-news-feed", "stream-processing", "ticketing", "time-series-database", "top-k-heavy-hitters", "trading-brokerage", "transcoding-pipeline", "url-shortener", "vector-database", "video-conferencing", "video-on-demand", "web-crawler", "webhook-delivery", "wide-column-document-store",
	}
}

func TestValidateCommandScalarFlagsUseLastValue(t *testing.T) {
	root := writeEmptyCatalogRoot(t)
	exitCode, stdout, stderr := runCLI(context.Background(), []string{
		"validate",
		"--root", "missing",
		"--root", root,
		"--format", "invalid",
		"--format", "json",
	})
	if exitCode != 0 || stderr != "" || !strings.HasPrefix(stdout, "{\n") {
		t.Fatalf("last-value flags exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}

func TestValidateCommandLoadAndContextFailuresUseExitTwo(t *testing.T) {
	exitCode, stdout, stderr := runCLI(context.Background(), []string{"validate", "--root", t.TempDir()})
	if exitCode != 2 || stdout != "" || stderr == "" {
		t.Fatalf("load failure exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}

	root := writeEmptyCatalogRoot(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	exitCode, stdout, stderr = runCLI(ctx, []string{"validate", "--root", root})
	if exitCode != 2 || stdout != "" || !strings.Contains(stderr, "context canceled") {
		t.Fatalf("context failure exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}
