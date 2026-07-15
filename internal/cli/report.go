package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"path/filepath"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/release"
	reportpkg "github.com/PinoHouse/works-on-my-whiteboard/internal/report"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

func (app application) runReport(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	flags := newFlagSet("report", stderr)
	root := "."
	evidenceRoot := ""
	releaseInput := trackedString{}
	profile := "deep"
	format := "markdown"
	output := trackedString{}
	check := false
	flags.StringVar(&root, "root", root, "repository root")
	flags.StringVar(&evidenceRoot, "evidence-root", evidenceRoot, "evidence root (default <root>/evidence)")
	flags.Var(&releaseInput, "release", "release input: current or sha256:<64 lowercase hex>")
	flags.StringVar(&profile, "profile", profile, "report profile: smoke or deep")
	flags.StringVar(&format, "format", format, "output format: markdown or json")
	flags.Var(&output, "output", "output path")
	flags.BoolVar(&check, "check", false, "compare rendered bytes with --output without writing")
	if proceed, exitCode := parseFlagSet(flags, args, stderr); !proceed {
		return exitCode
	}
	if !releaseInput.set || !validReleaseInput(releaseInput.value) {
		writeCLIError(stderr, "report requires --release current or sha256:<64 lowercase hex>")
		return ExitArgumentOrLoadFailure
	}
	if !validProfile(profile) {
		writeCLIError(stderr, "invalid report profile; want smoke or deep")
		return ExitArgumentOrLoadFailure
	}
	if format != "markdown" && format != "json" {
		writeCLIError(stderr, "invalid report format; want markdown or json")
		return ExitArgumentOrLoadFailure
	}
	if output.set && output.value == "" {
		writeCLIError(stderr, "--output requires a non-empty path")
		return ExitArgumentOrLoadFailure
	}
	if check && !output.set {
		writeCLIError(stderr, "--check requires --output")
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
	if evidenceRoot == "" {
		evidenceRoot = filepath.Join(root, "evidence")
	}
	evidenceRoot, err = app.dependencies.resolvePolicyPath(evidenceRoot)
	if err != nil {
		writeCommandIssue(stderr, "evidence_path_unavailable", "evidence path cannot be resolved safely")
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
	profileValue := evidence.Profile(profile)
	resolved, definitionIssues := resolveDefinitions(repository, validation.Matrix, profileValue, app.dependencies.lookupExperiment)
	if len(definitionIssues) != 0 {
		for _, definitionIssue := range definitionIssues {
			writeCommandIssue(stderr, definitionIssue.Code, definitionIssue.Message)
		}
		return ExitLabExecutionFailure
	}
	manifest, err := app.dependencies.loadAndSyncManifest(ctx, evidenceRoot, digest)
	if err != nil {
		return app.writeSnapshotOperationError(stderr, err)
	}
	if manifest.Profile != profileValue {
		writeCommandIssue(stderr, release.CodeReleaseProfileMismatch, "release snapshot profile differs from the requested profile")
		return ExitReleaseFailure
	}
	store, err := app.dependencies.openStoreReadOnly(evidenceRoot)
	if err != nil {
		return writeReleaseEvidenceStoreError(stderr, err)
	}
	audited, diagnostics, err := app.dependencies.auditSnapshot(ctx, manifest, expectedCells(resolved), store)
	if err != nil {
		writeCommandIssue(stderr, "release_audit_io", "release snapshot audit could not complete")
		return ExitArgumentOrLoadFailure
	}
	if len(diagnostics) != 0 {
		if err := writeDiagnostics(stderr, sanitizeReleaseDiagnostics(diagnostics, nil)); err != nil {
			return ExitArgumentOrLoadFailure
		}
		return ExitReleaseFailure
	}
	model, err := app.dependencies.buildReport(repository, validation, audited)
	if err != nil {
		writeCommandIssue(stderr, "report_model_invalid", "audited release cannot be projected into a report")
		return ExitReleaseFailure
	}
	var rendered bytes.Buffer
	switch format {
	case "markdown":
		err = reportpkg.WriteMarkdown(&rendered, model)
	case "json":
		err = reportpkg.WriteJSON(&rendered, model)
	}
	if err != nil {
		writeCommandIssue(stderr, "report_render_failed", "report bytes cannot be rendered")
		return ExitArgumentOrLoadFailure
	}
	return app.writeRenderedOutput("report", rendered.Bytes(), output, check, stdout, stderr)
}

func (app application) writeRenderedOutput(kind string, rendered []byte, output trackedString, check bool, stdout io.Writer, stderr io.Writer) int {
	if check {
		actual, err := app.dependencies.readFile(output.value)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				writeCommandIssue(stderr, kind+"_check_missing", kind+" check output is missing")
				return ExitDevelopmentFailure
			}
			writeCommandIssue(stderr, kind+"_check_read_failed", kind+" check output cannot be read")
			return ExitArgumentOrLoadFailure
		}
		if !bytes.Equal(actual, rendered) {
			writeCommandIssue(stderr, kind+"_check_mismatch", kind+" check output differs from rendered bytes")
			return ExitDevelopmentFailure
		}
		return ExitSuccess
	}
	if !output.set {
		if err := writeFull(stdout, rendered); err != nil {
			writeCommandIssue(stderr, kind+"_output_write_failed", kind+" output cannot be written")
			return ExitArgumentOrLoadFailure
		}
		return ExitSuccess
	}
	if err := app.dependencies.writeAtomic(output.value, rendered); err != nil {
		writeCommandIssue(stderr, kind+"_output_write_failed", kind+" output cannot be written atomically")
		return ExitArgumentOrLoadFailure
	}
	return ExitSuccess
}
