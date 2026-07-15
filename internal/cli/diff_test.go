package cli

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/release"
	reportpkg "github.com/PinoHouse/works-on-my-whiteboard/internal/report"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

func TestDiffAuditsRightBeforeMissingLeftBaseline(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	leftRoot := filepath.Join(t.TempDir(), "left")
	rightRoot := filepath.Join(t.TempDir(), "right")
	leftCanonical, err := canonicalPolicyPath(leftRoot)
	if err != nil {
		t.Fatal(err)
	}
	rightCanonical, err := canonicalPolicyPath(rightRoot)
	if err != nil {
		t.Fatal(err)
	}
	operations := make([]string, 0)
	fixture.dependencies.loadAndSyncManifest = func(_ context.Context, root string, digest inputdigest.Digest) (release.Manifest, error) {
		switch root {
		case rightCanonical:
			operations = append(operations, "load-sync-right")
			return release.Manifest{InputDigest: digest, Profile: evidence.ProfileDeep}, nil
		case leftCanonical:
			operations = append(operations, "load-sync-left")
			return release.Manifest{}, release.ErrSnapshotNotFound
		default:
			t.Fatalf("unexpected evidence root %q", root)
			return release.Manifest{}, errors.New("unreachable")
		}
	}
	fixture.dependencies.openStoreReadOnly = func(root string) (*evidence.Store, error) {
		if root != rightCanonical {
			t.Fatalf("unexpected store root %q", root)
		}
		operations = append(operations, "store-right")
		return nil, nil
	}
	fixture.dependencies.auditSnapshot = func(context.Context, release.Manifest, []release.ExpectedCell, *evidence.Store) (release.AuditedSnapshot, []validator.Diagnostic, error) {
		operations = append(operations, "audit-right")
		return release.AuditedSnapshot{}, []validator.Diagnostic{}, nil
	}
	fixture.dependencies.buildNoBaseline = func(release.AuditedSnapshot) (reportpkg.DiffModel, error) {
		operations = append(operations, "build-no-baseline")
		return reportpkg.DiffModel{Status: reportpkg.DiffStatusNoBaseline, InputDigest: string(fixture.state.InputDigest), Profile: evidence.ProfileDeep, Rows: []reportpkg.DiffRow{}}, nil
	}

	exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
		"diff", "--root", t.TempDir(), "--release", "current", "--left-evidence-root", leftRoot, "--right-evidence-root", rightRoot,
	})
	if exitCode != ExitSuccess || stderr != "" || !strings.Contains(stdout, "no-baseline-for-current-input") {
		t.Fatalf("missing baseline exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	want := "load-sync-right,store-right,audit-right,load-sync-left,build-no-baseline"
	if got := strings.Join(operations, ","); got != want {
		t.Fatalf("operations=%q; want %q", got, want)
	}
}

func TestDiffJoinedMissingAndUnsafeLeftIsInvalidNotNoBaseline(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	leftRoot := filepath.Join(t.TempDir(), "left")
	rightRoot := filepath.Join(t.TempDir(), "right")
	leftCanonical, err := canonicalPolicyPath(leftRoot)
	if err != nil {
		t.Fatal(err)
	}
	rightCanonical, err := canonicalPolicyPath(rightRoot)
	if err != nil {
		t.Fatal(err)
	}
	fixture.dependencies.loadAndSyncManifest = func(_ context.Context, root string, digest inputdigest.Digest) (release.Manifest, error) {
		switch root {
		case rightCanonical:
			return release.Manifest{InputDigest: digest, Profile: evidence.ProfileDeep}, nil
		case leftCanonical:
			return release.Manifest{}, errors.Join(release.ErrSnapshotNotFound, release.ErrSnapshotUnsafePath)
		default:
			t.Fatalf("unexpected evidence root %q", root)
			return release.Manifest{}, errors.New("unreachable")
		}
	}
	fixture.dependencies.openStoreReadOnly = func(root string) (*evidence.Store, error) {
		if root != rightCanonical {
			t.Fatalf("unexpected store root %q", root)
		}
		return nil, nil
	}
	fixture.dependencies.auditSnapshot = func(context.Context, release.Manifest, []release.ExpectedCell, *evidence.Store) (release.AuditedSnapshot, []validator.Diagnostic, error) {
		return release.AuditedSnapshot{}, []validator.Diagnostic{}, nil
	}
	noBaselineCalls := 0
	fixture.dependencies.buildNoBaseline = func(release.AuditedSnapshot) (reportpkg.DiffModel, error) {
		noBaselineCalls++
		return reportpkg.DiffModel{}, nil
	}

	exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
		"diff", "--root", t.TempDir(), "--release", "current", "--left-evidence-root", leftRoot, "--right-evidence-root", rightRoot,
	})
	if exitCode != ExitReleaseFailure || stdout != "" || !strings.Contains(stderr, release.CodeReleaseManifestInvalid) {
		t.Fatalf("joined left failure exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if noBaselineCalls != 0 || strings.Contains(stdout, "no-baseline-for-current-input") {
		t.Fatalf("joined left failure built no-baseline model: calls=%d stdout=%q", noBaselineCalls, stdout)
	}
}

func TestDiffCorruptLeftSnapshotIsReleaseFailureNotNoBaseline(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	leftRoot := filepath.Join(t.TempDir(), "left")
	rightRoot := filepath.Join(t.TempDir(), "right")
	leftCanonical, err := canonicalPolicyPath(leftRoot)
	if err != nil {
		t.Fatal(err)
	}
	rightCanonical, err := canonicalPolicyPath(rightRoot)
	if err != nil {
		t.Fatal(err)
	}
	operations := make([]string, 0)
	fixture.dependencies.loadAndSyncManifest = func(_ context.Context, root string, digest inputdigest.Digest) (release.Manifest, error) {
		switch root {
		case rightCanonical:
			operations = append(operations, "load-sync-right")
			return release.Manifest{InputDigest: digest, Profile: evidence.ProfileDeep}, nil
		case leftCanonical:
			operations = append(operations, "load-sync-left")
			return release.Manifest{}, release.ErrSnapshotCorrupt
		default:
			t.Fatalf("unexpected evidence root %q", root)
			return release.Manifest{}, errors.New("unreachable")
		}
	}
	fixture.dependencies.openStoreReadOnly = func(root string) (*evidence.Store, error) {
		if root != rightCanonical {
			t.Fatalf("unexpected store root %q", root)
		}
		operations = append(operations, "store-right")
		return nil, nil
	}
	fixture.dependencies.auditSnapshot = func(context.Context, release.Manifest, []release.ExpectedCell, *evidence.Store) (release.AuditedSnapshot, []validator.Diagnostic, error) {
		operations = append(operations, "audit-right")
		return release.AuditedSnapshot{}, []validator.Diagnostic{}, nil
	}
	noBaselineCalls := 0
	fixture.dependencies.buildNoBaseline = func(release.AuditedSnapshot) (reportpkg.DiffModel, error) {
		noBaselineCalls++
		return reportpkg.DiffModel{}, nil
	}

	exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
		"diff", "--root", t.TempDir(), "--release", "current", "--left-evidence-root", leftRoot, "--right-evidence-root", rightRoot,
	})
	if exitCode != ExitReleaseFailure || stdout != "" || !strings.Contains(stderr, release.CodeReleaseManifestInvalid) {
		t.Fatalf("corrupt left exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if noBaselineCalls != 0 {
		t.Fatalf("corrupt left built a no-baseline model %d times", noBaselineCalls)
	}
	if got, want := strings.Join(operations, ","), "load-sync-right,store-right,audit-right,load-sync-left"; got != want {
		t.Fatalf("operations=%q; want %q", got, want)
	}
}

func TestDiffRightFailureNeverTouchesLeft(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	leftCalls := 0
	fixture.dependencies.loadAndSyncManifest = func(_ context.Context, root string, _ inputdigest.Digest) (release.Manifest, error) {
		if strings.Contains(root, "left") {
			leftCalls++
			return release.Manifest{}, release.ErrSnapshotNotFound
		}
		return release.Manifest{}, release.ErrSnapshotCorrupt
	}
	exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
		"diff", "--root", t.TempDir(), "--release", "current", "--left-evidence-root", filepath.Join(t.TempDir(), "left"), "--right-evidence-root", filepath.Join(t.TempDir(), "right"),
	})
	if exitCode != ExitReleaseFailure || stdout != "" || stderr == "" || leftCalls != 0 {
		t.Fatalf("right failure exit=%d stdout=%q stderr=%q leftCalls=%d", exitCode, stdout, stderr, leftCalls)
	}
}

func TestDiffRightUnsafeEvidenceStoreIsSemanticAndNeverTouchesLeft(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	leftCalls := 0
	fixture.dependencies.loadAndSyncManifest = func(_ context.Context, root string, digest inputdigest.Digest) (release.Manifest, error) {
		if strings.Contains(root, "left") {
			leftCalls++
		}
		return release.Manifest{InputDigest: digest, Profile: evidence.ProfileDeep}, nil
	}
	fixture.dependencies.openStoreReadOnly = func(string) (*evidence.Store, error) {
		return nil, errors.Join(evidence.ErrEvidenceUnsafePath, errors.New("/absolute/workspace/secret"))
	}
	exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
		"diff", "--root", t.TempDir(), "--release", "current", "--left-evidence-root", filepath.Join(t.TempDir(), "left"), "--right-evidence-root", filepath.Join(t.TempDir(), "right"),
	})
	if exitCode != ExitReleaseFailure || stdout != "" || !strings.Contains(stderr, release.CodeReleaseEvidenceInvalid) || leftCalls != 0 {
		t.Fatalf("unsafe right store exit=%d stdout=%q stderr=%q leftCalls=%d", exitCode, stdout, stderr, leftCalls)
	}
	if strings.Contains(stderr, "/absolute/workspace/secret") {
		t.Fatalf("stderr leaked absolute path: %q", stderr)
	}
}

func TestDiffRightAuditSanitizesDiagnosticsBeforeTouchingLeft(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	secret := "/absolute/workspace/secret\x1b[31m\nforged"
	leftCalls := 0
	fixture.dependencies.loadAndSyncManifest = func(_ context.Context, root string, digest inputdigest.Digest) (release.Manifest, error) {
		if strings.Contains(root, "left") {
			leftCalls++
		}
		return release.Manifest{InputDigest: digest, Profile: evidence.ProfileDeep}, nil
	}
	fixture.dependencies.openStoreReadOnly = func(string) (*evidence.Store, error) { return nil, nil }
	fixture.dependencies.auditSnapshot = func(context.Context, release.Manifest, []release.ExpectedCell, *evidence.Store) (release.AuditedSnapshot, []validator.Diagnostic, error) {
		return release.AuditedSnapshot{}, []validator.Diagnostic{{
			Code: release.CodeReleaseEvidenceInvalid, Severity: "fatal", Path: secret,
			EntityID: runTestEvidenceID(0), Message: secret,
		}}, nil
	}
	exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
		"diff", "--root", t.TempDir(), "--release", "current", "--left-evidence-root", filepath.Join(t.TempDir(), "left"), "--right-evidence-root", filepath.Join(t.TempDir(), "right"),
	})
	if exitCode != ExitReleaseFailure || stdout != "" || !strings.Contains(stderr, release.CodeReleaseEvidenceInvalid) || leftCalls != 0 {
		t.Fatalf("right audit exit=%d stdout=%q stderr=%q leftCalls=%d", exitCode, stdout, stderr, leftCalls)
	}
	if strings.Contains(stderr, "/absolute/workspace/secret") || strings.Contains(stderr, "run-20000101") || strings.ContainsAny(stderr, "\x1b\r") {
		t.Fatalf("diff audit diagnostics leaked untrusted fields: %q", stderr)
	}
}

func TestDiffAuditsBothSidesBeforeBuildingSemanticDiff(t *testing.T) {
	fixture := newRunApplicationFixture(t, 1)
	leftRoot := filepath.Join(t.TempDir(), "left")
	rightRoot := filepath.Join(t.TempDir(), "right")
	leftCanonical, _ := canonicalPolicyPath(leftRoot)
	rightCanonical, _ := canonicalPolicyPath(rightRoot)
	operations := make([]string, 0)
	fixture.dependencies.loadAndSyncManifest = func(_ context.Context, root string, digest inputdigest.Digest) (release.Manifest, error) {
		if root == rightCanonical {
			operations = append(operations, "load-sync-right")
		} else if root == leftCanonical {
			operations = append(operations, "load-sync-left")
		}
		return release.Manifest{InputDigest: digest, Profile: evidence.ProfileDeep}, nil
	}
	fixture.dependencies.openStoreReadOnly = func(root string) (*evidence.Store, error) {
		if root == rightCanonical {
			operations = append(operations, "store-right")
		} else {
			operations = append(operations, "store-left")
		}
		return nil, nil
	}
	audits := 0
	fixture.dependencies.auditSnapshot = func(context.Context, release.Manifest, []release.ExpectedCell, *evidence.Store) (release.AuditedSnapshot, []validator.Diagnostic, error) {
		audits++
		if audits == 1 {
			operations = append(operations, "audit-right")
		} else {
			operations = append(operations, "audit-left")
		}
		return release.AuditedSnapshot{}, []validator.Diagnostic{}, nil
	}
	fixture.dependencies.buildDiff = func(release.AuditedSnapshot, release.AuditedSnapshot) (reportpkg.DiffModel, error) {
		operations = append(operations, "build-diff")
		return reportpkg.DiffModel{Status: reportpkg.DiffStatusNoChanges, InputDigest: string(fixture.state.InputDigest), Profile: evidence.ProfileDeep, Rows: []reportpkg.DiffRow{}}, nil
	}
	exitCode, stdout, stderr := runApplication(t, application{dependencies: fixture.dependencies}, []string{
		"diff", "--root", t.TempDir(), "--release", "current", "--left-evidence-root", leftRoot, "--right-evidence-root", rightRoot, "--format", "json",
	})
	if exitCode != ExitSuccess || stderr != "" || !strings.Contains(stdout, `"status": "no-changes"`) {
		t.Fatalf("diff exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	want := "load-sync-right,store-right,audit-right,load-sync-left,store-left,audit-left,build-diff"
	if got := strings.Join(operations, ","); got != want {
		t.Fatalf("operations=%q; want %q", got, want)
	}
}
