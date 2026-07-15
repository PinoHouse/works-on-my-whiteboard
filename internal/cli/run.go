package cli

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/release"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

func (app application) runEvidence(ctx context.Context, args []string, _ io.Writer, stderr io.Writer) int {
	flags := newFlagSet("run", stderr)
	root := "."
	evidenceRoot := ""
	required := trackedBool{}
	profile := trackedString{}
	snapshot := false
	flags.Var(&required, "required", "execute the complete required matrix")
	flags.Var(&profile, "profile", "execution profile: smoke or deep")
	flags.StringVar(&root, "root", root, "repository root")
	flags.StringVar(&evidenceRoot, "evidence-root", evidenceRoot, "evidence root (default <root>/evidence)")
	flags.BoolVar(&snapshot, "snapshot", false, "publish an immutable release snapshot")
	if proceed, exitCode := parseFlagSet(flags, args, stderr); !proceed {
		return exitCode
	}
	if !required.set || !required.value {
		writeCLIError(stderr, "run requires explicit true --required")
		return ExitArgumentOrLoadFailure
	}
	if !profile.set || !validProfile(profile.value) {
		writeCLIError(stderr, "run requires --profile smoke or deep")
		return ExitArgumentOrLoadFailure
	}
	state, exitCode, ok := app.verifyProvenance(ctx, root, stderr)
	if !ok {
		return exitCode
	}
	if evidenceRoot == "" {
		evidenceRoot = filepath.Join(root, "evidence")
	}
	resolvedEvidenceRoot, err := app.dependencies.resolvePolicyPath(evidenceRoot)
	if err != nil {
		writeCommandIssue(stderr, "evidence_path_unavailable", "evidence path cannot be resolved safely")
		return ExitArgumentOrLoadFailure
	}
	evidenceRoot = resolvedEvidenceRoot
	profileValue := evidence.Profile(profile.value)
	if snapshot && profileValue == evidence.ProfileSmoke {
		repositoryEvidence, err := app.dependencies.resolvePolicyPath(filepath.Join(root, "evidence"))
		if err != nil {
			writeCommandIssue(stderr, "evidence_path_unavailable", "evidence path cannot be resolved safely")
			return ExitArgumentOrLoadFailure
		}
		sameEvidenceRoot, err := samePolicyPath(repositoryEvidence, evidenceRoot)
		if err != nil {
			writeCommandIssue(stderr, "evidence_path_unavailable", "evidence path cannot be resolved safely")
			return ExitArgumentOrLoadFailure
		}
		if sameEvidenceRoot {
			writeCommandIssue(stderr, "smoke_snapshot_repository_root", "smoke snapshots cannot target the repository evidence root")
			return ExitArgumentOrLoadFailure
		}
	}
	repository, err := app.dependencies.loadCatalog(ctx, root)
	if err != nil {
		writeCommandIssue(stderr, "catalog_load_failed", "catalog cannot be loaded")
		return ExitArgumentOrLoadFailure
	}
	validation := app.dependencies.validateCatalog(repository, validator.ModeDevelopment)
	if len(validation.Diagnostics) != 0 {
		if err := writeDiagnostics(stderr, sanitizeReleaseDiagnostics(validation.Diagnostics, &validation.Coverage)); err != nil {
			return ExitArgumentOrLoadFailure
		}
		return ExitDevelopmentFailure
	}
	resolved, definitionIssues := resolveDefinitions(repository, validation.Matrix, profileValue, app.dependencies.lookupExperiment)
	expected := expectedCells(resolved)
	if snapshot {
		manifest, exists, exitCode := app.inspectSnapshot(ctx, evidenceRoot, state.InputDigest, profileValue, stderr)
		if exitCode != ExitSuccess {
			return exitCode
		}
		if exists {
			if len(definitionIssues) != 0 {
				writeDefinitionIssues(stderr, definitionIssues)
				return ExitLabExecutionFailure
			}
			valid, exitCode := app.auditExistingSnapshot(ctx, evidenceRoot, manifest, expected, stderr)
			if valid {
				return ExitSuccess
			}
			return exitCode
		}
	}

	runSetID, err := app.dependencies.newRunSetID(app.dependencies.now())
	if err != nil {
		writeCommandIssue(stderr, "run_set_id_unavailable", "run-set identity cannot be generated")
		return ExitArgumentOrLoadFailure
	}
	store, err := app.dependencies.newStore(evidenceRoot)
	if err != nil {
		writeCommandIssue(stderr, "evidence_store_unavailable", "evidence store cannot be opened")
		return ExitArgumentOrLoadFailure
	}
	records := make([]evidence.Record, 0, len(resolved))
	executionFailed := false
	for _, definition := range resolved {
		runContext, cancel := context.WithTimeout(ctx, definition.Definition.Spec.Deadline)
		result, runnerErr := app.dependencies.runExperiment(runContext, definition.Definition.Spec)
		cancel()
		prepared, err := prepareRunResult(definition, result, runnerErr)
		if err != nil {
			executionFailed = true
			continue
		}
		evidenceID, err := app.dependencies.newEvidenceID(app.dependencies.now())
		if err != nil {
			writeCommandIssue(stderr, "evidence_id_unavailable", "evidence identity cannot be generated")
			return ExitArgumentOrLoadFailure
		}
		record, err := convertPreparedRunResult(definition, result, prepared, recordIdentity{
			ID: evidenceID, RunSetID: runSetID, SourceState: state, Environment: app.dependencies.currentEnvironment(),
		})
		if err != nil {
			executionFailed = true
			continue
		}
		if err := app.dependencies.putEvidence(ctx, store, record); err != nil {
			writeCommandIssue(stderr, "evidence_write_failed", "evidence attempt cannot be stored")
			return ExitArgumentOrLoadFailure
		}
		records = append(records, record)
		if runnerErr != nil || record.Status != evidence.StatusPassed {
			executionFailed = true
		}
	}
	after, err := app.dependencies.computeState(ctx, root)
	if err != nil {
		writeCommandIssue(stderr, "source_state_unavailable", "current source state is unavailable")
		return ExitArgumentOrLoadFailure
	}
	if after != state {
		writeCommandIssue(stderr, "source_state_changed", "source state changed during execution")
		return ExitLabExecutionFailure
	}
	if len(definitionIssues) != 0 || executionFailed || len(records) != len(validation.Matrix) {
		writeDefinitionIssues(stderr, definitionIssues)
		writeCommandIssue(stderr, "current_run_incomplete", "current run did not produce one passed attempt for every required cell")
		return ExitLabExecutionFailure
	}
	if !snapshot {
		return ExitSuccess
	}
	manifest, err := app.dependencies.buildManifest(state.InputDigest, expected, records)
	if err != nil {
		writeCommandIssue(stderr, "release_build_failed", "current attempts cannot form a release snapshot")
		return ExitLabExecutionFailure
	}
	if err := app.dependencies.writeManifest(ctx, evidenceRoot, manifest); err != nil {
		storageClass := classifyReleaseStorageError(err)
		if storageClass == storageUnknown && release.IsPureSnapshotExists(err) {
			existing, exists, exitCode := app.inspectSnapshot(ctx, evidenceRoot, state.InputDigest, profileValue, stderr)
			if exitCode != ExitSuccess {
				return exitCode
			}
			if exists {
				valid, exitCode := app.auditExistingSnapshot(ctx, evidenceRoot, existing, expected, stderr)
				if valid {
					return ExitSuccess
				}
				return exitCode
			}
		}
		if storageClass == storageInvalid {
			return app.writeSnapshotOperationError(stderr, err)
		}
		writeCommandIssue(stderr, "release_snapshot_write_failed", "release snapshot cannot be durably installed")
		return ExitArgumentOrLoadFailure
	}
	return ExitSuccess
}

func expectedCells(resolved []resolvedDefinition) []release.ExpectedCell {
	expected := make([]release.ExpectedCell, len(resolved))
	for index := range resolved {
		expected[index] = cloneExpectedCell(resolved[index].Expected)
	}
	return expected
}

func writeDefinitionIssues(stderr io.Writer, issues []definitionIssue) {
	for _, issue := range issues {
		writeCommandIssue(stderr, issue.Code, issue.Message)
	}
}

func (app application) inspectSnapshot(ctx context.Context, evidenceRoot string, digest inputdigest.Digest, profile evidence.Profile, stderr io.Writer) (release.Manifest, bool, int) {
	manifest, err := app.dependencies.loadAndSyncManifest(ctx, evidenceRoot, digest)
	if classifyReleaseStorageError(err) == storageMissing {
		return release.Manifest{}, false, ExitSuccess
	}
	if err != nil {
		return release.Manifest{}, false, app.writeSnapshotOperationError(stderr, err)
	}
	if manifest.Profile != profile {
		writeCommandIssue(stderr, release.CodeReleaseProfileMismatch, "release snapshot profile differs from the requested profile")
		return release.Manifest{}, false, ExitReleaseFailure
	}
	return manifest, true, ExitSuccess
}

func (app application) auditExistingSnapshot(ctx context.Context, evidenceRoot string, manifest release.Manifest, expected []release.ExpectedCell, stderr io.Writer) (bool, int) {
	store, err := app.dependencies.openStoreReadOnly(evidenceRoot)
	if err != nil {
		return false, writeReleaseEvidenceStoreError(stderr, err)
	}
	_, diagnostics, err := app.dependencies.auditSnapshot(ctx, manifest, expected, store)
	if err != nil {
		writeCommandIssue(stderr, "release_audit_io", "release snapshot audit could not complete")
		return false, ExitArgumentOrLoadFailure
	}
	if len(diagnostics) != 0 {
		if err := writeDiagnostics(stderr, sanitizeReleaseDiagnostics(diagnostics, nil)); err != nil {
			return false, ExitArgumentOrLoadFailure
		}
		return false, ExitReleaseFailure
	}
	return true, ExitSuccess
}

func (app application) writeSnapshotOperationError(stderr io.Writer, err error) int {
	switch classifyReleaseStorageError(err) {
	case storageInvalid:
		writeCommandIssue(stderr, release.CodeReleaseManifestInvalid, "release snapshot is unsafe or invalid")
		return ExitReleaseFailure
	case storageMissing:
		writeCommandIssue(stderr, release.CodeReleaseSnapshotMissing, "release snapshot is missing for the current input")
		return ExitReleaseFailure
	default:
		writeCommandIssue(stderr, "release_snapshot_io", "release snapshot storage operation failed")
		return ExitArgumentOrLoadFailure
	}
}

func canonicalPolicyPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	cursor := absolute
	suffix := make([]string, 0)
	for {
		_, err := os.Lstat(cursor)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return "", err
		}
		suffix = append(suffix, filepath.Base(cursor))
		cursor = parent
	}
	resolved, err := filepath.EvalSymlinks(cursor)
	if err != nil {
		return "", err
	}
	for index := len(suffix) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, suffix[index])
	}
	return filepath.Clean(resolved), nil
}

func samePolicyPath(left, right string) (bool, error) {
	if left == right {
		return true, nil
	}
	leftInfo, leftErr := os.Lstat(left)
	rightInfo, rightErr := os.Lstat(right)
	if leftErr == nil && rightErr == nil {
		return os.SameFile(leftInfo, rightInfo), nil
	}
	if leftErr != nil && !errors.Is(leftErr, fs.ErrNotExist) {
		return false, leftErr
	}
	if rightErr != nil && !errors.Is(rightErr, fs.ErrNotExist) {
		return false, rightErr
	}
	if (leftErr == nil) != (rightErr == nil) {
		return false, nil
	}

	leftAnchor, leftSuffix, err := policyPathAnchor(left)
	if err != nil {
		return false, err
	}
	rightAnchor, rightSuffix, err := policyPathAnchor(right)
	if err != nil {
		return false, err
	}
	if !os.SameFile(leftAnchor, rightAnchor) || len(leftSuffix) != len(rightSuffix) {
		return false, nil
	}
	for index := range leftSuffix {
		if !strings.EqualFold(leftSuffix[index], rightSuffix[index]) {
			return false, nil
		}
	}
	return true, nil
}

func policyPathAnchor(path string) (fs.FileInfo, []string, error) {
	cursor := filepath.Clean(path)
	suffix := make([]string, 0)
	for {
		info, err := os.Lstat(cursor)
		if err == nil {
			return info, suffix, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, nil, err
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return nil, nil, err
		}
		suffix = append([]string{filepath.Base(cursor)}, suffix...)
		cursor = parent
	}
}

type trackedBool struct {
	value bool
	set   bool
}

func (value *trackedBool) String() string {
	if value.value {
		return "true"
	}
	return "false"
}

func (value *trackedBool) Set(next string) error {
	parsed, err := parseBool(next)
	if err != nil {
		return err
	}
	value.value = parsed
	value.set = true
	return nil
}

func (value *trackedBool) IsBoolFlag() bool {
	return true
}

func parseBool(value string) (bool, error) {
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, &boolValueError{value: value}
	}
}

type boolValueError struct {
	value string
}

func (err *boolValueError) Error() string {
	return "invalid boolean value " + err.value
}

func validProfile(value string) bool {
	return value == "smoke" || value == "deep"
}
