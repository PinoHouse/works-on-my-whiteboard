package cli

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/release"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

func (app application) runValidate(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	flags := newFlagSet("validate", stderr)
	root := "."
	format := "text"
	contentEnabled := false
	evidenceRoot := ""
	releaseInput := trackedString{}
	flags.StringVar(&root, "root", root, "repository root")
	flags.StringVar(&evidenceRoot, "evidence-root", evidenceRoot, "evidence root (default <root>/evidence)")
	flags.BoolVar(&contentEnabled, "content", false, "validate Markdown content and internal links")
	flags.Var(&releaseInput, "release", "release input: current or sha256:<64 lowercase hex>")
	flags.StringVar(&format, "format", format, "output format: text or json")
	if proceed, exitCode := parseFlagSet(flags, args, stderr); !proceed {
		return exitCode
	}
	if format != "text" && format != "json" {
		writeCLIError(stderr, "invalid validate format; want text or json")
		return ExitArgumentOrLoadFailure
	}
	if releaseInput.set && !validReleaseInput(releaseInput.value) {
		writeCLIError(stderr, "invalid release; want current or sha256:<64 lowercase hex>")
		return ExitArgumentOrLoadFailure
	}
	if releaseInput.set {
		return app.runReleaseValidation(ctx, root, evidenceRoot, releaseInput.value, format, stdout, stderr)
	}

	repository, err := app.dependencies.loadCatalog(ctx, root)
	if err != nil {
		writeCLIError(stderr, "load catalog: %v", err)
		return ExitArgumentOrLoadFailure
	}
	report := app.dependencies.validateCatalog(repository, validator.ModeDevelopment)
	diagnostics := append([]validator.Diagnostic{}, report.Diagnostics...)
	if contentEnabled {
		diagnostics = append(diagnostics, app.dependencies.validateContent(root, repository).Diagnostics...)
	}
	report.Diagnostics = sortDiagnostics(diagnostics)

	destination := stdout
	exitCode := ExitSuccess
	if len(report.Diagnostics) != 0 {
		destination = stderr
		exitCode = ExitDevelopmentFailure
	}
	if err := renderValidation(destination, format, report); err != nil {
		writeCLIError(stderr, "write validation result: %v", err)
		return ExitArgumentOrLoadFailure
	}
	return exitCode
}

func (app application) runReleaseValidation(ctx context.Context, root, evidenceRoot, releaseInput, format string, stdout, stderr io.Writer) int {
	state, exitCode, ok := app.verifyProvenance(ctx, root, stderr)
	if !ok {
		return exitCode
	}
	digest, issue, err := resolveReleaseInput(releaseInput, state.InputDigest)
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
	report := app.dependencies.validateCatalog(repository, validator.ModeRelease)
	diagnostics := append([]validator.Diagnostic{}, report.Diagnostics...)
	diagnostics = append(diagnostics, app.dependencies.validateContent(root, repository).Diagnostics...)
	resolved, definitionIssues := resolveDefinitions(repository, report.Matrix, evidence.ProfileDeep, app.dependencies.lookupExperiment)
	for _, definitionIssue := range definitionIssues {
		diagnostics = append(diagnostics, validator.Diagnostic{
			Code: definitionIssue.Code, Severity: "error", EntityID: definitionIssue.Cell.LabID, Message: definitionIssue.Message,
		})
	}

	manifest, loadErr := app.dependencies.loadAndSyncManifest(ctx, evidenceRoot, digest)
	if loadErr != nil {
		diagnostic, semantic := releaseStorageDiagnostic(loadErr)
		if !semantic {
			writeCommandIssue(stderr, "release_snapshot_io", "release snapshot storage operation failed")
			return ExitArgumentOrLoadFailure
		}
		diagnostics = append(diagnostics, diagnostic)
	} else {
		store, storeErr := app.dependencies.openStoreReadOnly(evidenceRoot)
		if storeErr != nil {
			if diagnostic, semantic := releaseEvidenceStoreDiagnostic(storeErr); semantic {
				diagnostics = append(diagnostics, diagnostic)
			} else {
				writeCommandIssue(stderr, "release_evidence_store_io", "release evidence store cannot be opened")
				return ExitArgumentOrLoadFailure
			}
		} else {
			_, auditDiagnostics, auditErr := app.dependencies.auditSnapshot(ctx, manifest, expectedCells(resolved), store)
			if auditErr != nil {
				writeCommandIssue(stderr, "release_audit_io", "release snapshot audit could not complete")
				return ExitArgumentOrLoadFailure
			}
			diagnostics = append(diagnostics, auditDiagnostics...)
		}
	}
	report.Diagnostics = diagnostics
	report = sanitizeReleaseValidationReport(report)
	destination := stdout
	exitCode = ExitSuccess
	if len(report.Diagnostics) != 0 {
		destination = stderr
		exitCode = ExitReleaseFailure
	}
	if err := renderValidation(destination, format, report); err != nil {
		writeCommandIssue(stderr, "validation_output_write_failed", "validation result cannot be written")
		return ExitArgumentOrLoadFailure
	}
	return exitCode
}

func releaseStorageDiagnostic(err error) (validator.Diagnostic, bool) {
	switch classifyReleaseStorageError(err) {
	case storageInvalid:
		return validator.Diagnostic{Code: release.CodeReleaseManifestInvalid, Severity: "error", Message: "release snapshot manifest is invalid"}, true
	case storageMissing:
		return validator.Diagnostic{Code: release.CodeReleaseSnapshotMissing, Severity: "error", Message: "release snapshot is missing for the current input"}, true
	default:
		return validator.Diagnostic{}, false
	}
}

func validReleaseInput(value string) bool {
	if value == "current" {
		return true
	}
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	for _, char := range value[len(prefix):] {
		if char >= '0' && char <= '9' || char >= 'a' && char <= 'f' {
			continue
		}
		return false
	}
	return true
}

func renderValidation(writer io.Writer, format string, report validator.Report) error {
	if format == "json" {
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		encoded = append(encoded, '\n')
		return writeFull(writer, encoded)
	}
	if len(report.Diagnostics) == 0 {
		return writeFull(writer, []byte("validation passed\n"))
	}
	return writeDiagnostics(writer, report.Diagnostics)
}
