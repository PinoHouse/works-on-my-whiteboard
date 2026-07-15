package cli

import (
	"bytes"
	"context"
	"io"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/release"
	reportpkg "github.com/PinoHouse/works-on-my-whiteboard/internal/report"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

func (app application) runDiff(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	flags := newFlagSet("diff", stderr)
	root := "."
	leftEvidenceRoot := trackedString{}
	rightEvidenceRoot := trackedString{}
	releaseInput := trackedString{}
	profile := "deep"
	format := "markdown"
	output := trackedString{}
	flags.StringVar(&root, "root", root, "repository root")
	flags.Var(&leftEvidenceRoot, "left-evidence-root", "left evidence root")
	flags.Var(&rightEvidenceRoot, "right-evidence-root", "right evidence root")
	flags.Var(&releaseInput, "release", "release input: current or sha256:<64 lowercase hex>")
	flags.StringVar(&profile, "profile", profile, "diff profile: deep")
	flags.StringVar(&format, "format", format, "output format: markdown or json")
	flags.Var(&output, "output", "output path")
	if proceed, exitCode := parseFlagSet(flags, args, stderr); !proceed {
		return exitCode
	}
	if !releaseInput.set || !validReleaseInput(releaseInput.value) {
		writeCLIError(stderr, "diff requires --release current or sha256:<64 lowercase hex>")
		return ExitArgumentOrLoadFailure
	}
	if !leftEvidenceRoot.set || leftEvidenceRoot.value == "" {
		writeCLIError(stderr, "diff requires a non-empty --left-evidence-root")
		return ExitArgumentOrLoadFailure
	}
	if !rightEvidenceRoot.set || rightEvidenceRoot.value == "" {
		writeCLIError(stderr, "diff requires a non-empty --right-evidence-root")
		return ExitArgumentOrLoadFailure
	}
	if profile != "deep" {
		writeCLIError(stderr, "diff requires --profile deep")
		return ExitArgumentOrLoadFailure
	}
	if format != "markdown" && format != "json" {
		writeCLIError(stderr, "invalid diff format; want markdown or json")
		return ExitArgumentOrLoadFailure
	}
	if output.set && output.value == "" {
		writeCLIError(stderr, "--output requires a non-empty path")
		return ExitArgumentOrLoadFailure
	}
	state, exitCode, ok := app.verifyProvenance(ctx, root, stderr)
	if !ok {
		return exitCode
	}
	digest, issue, err := resolveReleaseInput(releaseInput.value, state.InputDigest)
	if err != nil {
		writeCLIError(stderr, "invalid release; want current or sha256:<64 lowercase hex>")
		return ExitArgumentOrLoadFailure
	}
	if issue != nil {
		writeCommandIssue(stderr, issue.Code, issue.Message)
		return ExitReleaseFailure
	}
	leftRoot, err := app.dependencies.resolvePolicyPath(leftEvidenceRoot.value)
	if err != nil {
		writeCommandIssue(stderr, "left_evidence_path_unavailable", "left evidence path cannot be resolved safely")
		return ExitArgumentOrLoadFailure
	}
	rightRoot, err := app.dependencies.resolvePolicyPath(rightEvidenceRoot.value)
	if err != nil {
		writeCommandIssue(stderr, "right_evidence_path_unavailable", "right evidence path cannot be resolved safely")
		return ExitArgumentOrLoadFailure
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
	resolved, definitionIssues := resolveDefinitions(repository, validation.Matrix, evidence.ProfileDeep, app.dependencies.lookupExperiment)
	if len(definitionIssues) != 0 {
		for _, definitionIssue := range definitionIssues {
			writeCommandIssue(stderr, definitionIssue.Code, definitionIssue.Message)
		}
		return ExitLabExecutionFailure
	}
	expected := expectedCells(resolved)
	right, found, exitCode := app.loadDiffSnapshot(ctx, rightRoot, digest, expected, false, stderr)
	if !found {
		return exitCode
	}
	left, found, exitCode := app.loadDiffSnapshot(ctx, leftRoot, digest, expected, true, stderr)
	var model reportpkg.DiffModel
	if !found {
		if exitCode != ExitSuccess {
			return exitCode
		}
		model, err = app.dependencies.buildNoBaseline(right)
	} else {
		if exitCode != ExitSuccess {
			return exitCode
		}
		model, err = app.dependencies.buildDiff(left, right)
	}
	if err != nil {
		writeCommandIssue(stderr, "release_diff_invalid", "audited releases cannot be compared")
		return ExitReleaseFailure
	}
	var rendered bytes.Buffer
	switch format {
	case "markdown":
		err = reportpkg.WriteDiffMarkdown(&rendered, model)
	case "json":
		err = reportpkg.WriteDiffJSON(&rendered, model)
	}
	if err != nil {
		writeCommandIssue(stderr, "diff_render_failed", "diff bytes cannot be rendered")
		return ExitArgumentOrLoadFailure
	}
	return app.writeRenderedOutput("diff", rendered.Bytes(), output, false, stdout, stderr)
}

func (app application) loadDiffSnapshot(ctx context.Context, evidenceRoot string, digest inputdigest.Digest, expected []release.ExpectedCell, allowMissing bool, stderr io.Writer) (release.AuditedSnapshot, bool, int) {
	manifest, err := app.dependencies.loadAndSyncManifest(ctx, evidenceRoot, digest)
	if classifyReleaseStorageError(err) == storageMissing {
		if allowMissing {
			return release.AuditedSnapshot{}, false, ExitSuccess
		}
		writeCommandIssue(stderr, release.CodeReleaseSnapshotMissing, "right release snapshot is missing for the current input")
		return release.AuditedSnapshot{}, false, ExitReleaseFailure
	}
	if err != nil {
		return release.AuditedSnapshot{}, false, app.writeSnapshotOperationError(stderr, err)
	}
	if manifest.Profile != evidence.ProfileDeep {
		writeCommandIssue(stderr, release.CodeReleaseProfileMismatch, "release snapshot profile differs from deep")
		return release.AuditedSnapshot{}, false, ExitReleaseFailure
	}
	store, err := app.dependencies.openStoreReadOnly(evidenceRoot)
	if err != nil {
		return release.AuditedSnapshot{}, false, writeReleaseEvidenceStoreError(stderr, err)
	}
	audited, diagnostics, err := app.dependencies.auditSnapshot(ctx, manifest, expected, store)
	if err != nil {
		writeCommandIssue(stderr, "release_audit_io", "release snapshot audit could not complete")
		return release.AuditedSnapshot{}, false, ExitArgumentOrLoadFailure
	}
	if len(diagnostics) != 0 {
		if err := writeDiagnostics(stderr, sanitizeReleaseDiagnostics(diagnostics, nil)); err != nil {
			return release.AuditedSnapshot{}, false, ExitArgumentOrLoadFailure
		}
		return release.AuditedSnapshot{}, false, ExitReleaseFailure
	}
	return audited, true, ExitSuccess
}
