package cli

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/content"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/experiments"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/release"
	reportpkg "github.com/PinoHouse/works-on-my-whiteboard/internal/report"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
	"github.com/PinoHouse/works-on-my-whiteboard/labs/harness"
)

const (
	ExitSuccess               = 0
	ExitArgumentOrLoadFailure = 2
	ExitDevelopmentFailure    = 3
	ExitReleaseFailure        = 4
	ExitLabExecutionFailure   = 5
)

func Run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	return newApplication().run(ctx, args, stdout, stderr)
}

type application struct {
	dependencies applicationDependencies
}

type applicationDependencies struct {
	computeState        func(context.Context, string) (inputdigest.State, error)
	readBuildInfo       func() (*debug.BuildInfo, bool)
	loadCatalog         func(context.Context, string) (*catalog.Catalog, error)
	validateCatalog     func(*catalog.Catalog, validator.Mode) validator.Report
	validateContent     func(string, *catalog.Catalog) content.Result
	lookupExperiment    experimentLookup
	newRunSetID         func(time.Time) (evidence.RunSetID, error)
	newEvidenceID       func(time.Time) (string, error)
	now                 func() time.Time
	currentEnvironment  func() evidence.Environment
	newStore            func(string) (*evidence.Store, error)
	openStoreReadOnly   func(string) (*evidence.Store, error)
	putEvidence         func(context.Context, *evidence.Store, evidence.Record) error
	runExperiment       func(context.Context, harness.RunSpec) (harness.RunResult, error)
	buildManifest       func(inputdigest.Digest, []release.ExpectedCell, []evidence.Record) (release.Manifest, error)
	loadAndSyncManifest func(context.Context, string, inputdigest.Digest) (release.Manifest, error)
	writeManifest       func(context.Context, string, release.Manifest) error
	auditSnapshot       func(context.Context, release.Manifest, []release.ExpectedCell, *evidence.Store) (release.AuditedSnapshot, []validator.Diagnostic, error)
	buildReport         func(*catalog.Catalog, validator.Report, release.AuditedSnapshot) (reportpkg.Model, error)
	buildDiff           func(release.AuditedSnapshot, release.AuditedSnapshot) (reportpkg.DiffModel, error)
	buildNoBaseline     func(release.AuditedSnapshot) (reportpkg.DiffModel, error)
	resolvePolicyPath   func(string) (string, error)
	readFile            func(string) ([]byte, error)
	writeAtomic         func(string, []byte) error
}

func newApplication() application {
	return application{dependencies: applicationDependencies{
		computeState:       inputdigest.ComputeState,
		readBuildInfo:      debug.ReadBuildInfo,
		loadCatalog:        catalog.LoadDir,
		validateCatalog:    validator.Validate,
		validateContent:    content.ValidateRepository,
		lookupExperiment:   experiments.Lookup,
		newRunSetID:        func(now time.Time) (evidence.RunSetID, error) { return evidence.NewRunSetID(now, cryptorand.Reader) },
		newEvidenceID:      func(now time.Time) (string, error) { return evidence.NewID(now, cryptorand.Reader) },
		now:                time.Now,
		currentEnvironment: evidence.CurrentEnvironment,
		newStore:           evidence.NewStore,
		openStoreReadOnly:  evidence.OpenStoreReadOnly,
		putEvidence: func(ctx context.Context, store *evidence.Store, record evidence.Record) error {
			return store.Put(ctx, record)
		},
		runExperiment:       harness.NewRunner().Run,
		buildManifest:       release.Build,
		loadAndSyncManifest: release.LoadAndSyncManifest,
		writeManifest:       release.WriteManifest,
		auditSnapshot:       release.AuditSnapshot,
		buildReport:         reportpkg.Build,
		buildDiff:           reportpkg.BuildDiff,
		buildNoBaseline:     reportpkg.BuildNoBaselineDiff,
		resolvePolicyPath:   canonicalPolicyPath,
		readFile:            os.ReadFile,
		writeAtomic:         writeCoverageAtomically,
	}}
}

func (app application) run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	if ctx == nil {
		ctx = context.Background()
	}
	app.dependencies = app.dependencies.withDefaults()
	trackedStdout := &trackingWriter{destination: stdout}
	trackedStderr := &trackingWriter{destination: stderr}
	exitCode := ExitArgumentOrLoadFailure
	if len(args) == 0 {
		writeCLIError(trackedStderr, "usage: whiteboard <validate|coverage|run|report|diff>")
	} else {
		switch args[0] {
		case "validate":
			exitCode = app.runValidate(ctx, args[1:], trackedStdout, trackedStderr)
		case "coverage":
			exitCode = runCoverage(ctx, args[1:], trackedStdout, trackedStderr)
		case "run":
			exitCode = app.runEvidence(ctx, args[1:], trackedStdout, trackedStderr)
		case "report":
			exitCode = app.runReport(ctx, args[1:], trackedStdout, trackedStderr)
		case "diff":
			exitCode = app.runDiff(ctx, args[1:], trackedStdout, trackedStderr)
		default:
			writeCLIError(trackedStderr, "unknown command; want validate, coverage, run, report, or diff")
		}
	}
	if trackedStdout.Err() != nil || trackedStderr.Err() != nil {
		return ExitArgumentOrLoadFailure
	}
	return exitCode
}

func (dependencies applicationDependencies) withDefaults() applicationDependencies {
	defaults := newApplication().dependencies
	if dependencies.computeState == nil {
		dependencies.computeState = defaults.computeState
	}
	if dependencies.readBuildInfo == nil {
		dependencies.readBuildInfo = defaults.readBuildInfo
	}
	if dependencies.loadCatalog == nil {
		dependencies.loadCatalog = defaults.loadCatalog
	}
	if dependencies.validateCatalog == nil {
		dependencies.validateCatalog = defaults.validateCatalog
	}
	if dependencies.validateContent == nil {
		dependencies.validateContent = defaults.validateContent
	}
	if dependencies.lookupExperiment == nil {
		dependencies.lookupExperiment = defaults.lookupExperiment
	}
	if dependencies.newRunSetID == nil {
		dependencies.newRunSetID = defaults.newRunSetID
	}
	if dependencies.newEvidenceID == nil {
		dependencies.newEvidenceID = defaults.newEvidenceID
	}
	if dependencies.now == nil {
		dependencies.now = defaults.now
	}
	if dependencies.currentEnvironment == nil {
		dependencies.currentEnvironment = defaults.currentEnvironment
	}
	if dependencies.newStore == nil {
		dependencies.newStore = defaults.newStore
	}
	if dependencies.openStoreReadOnly == nil {
		dependencies.openStoreReadOnly = defaults.openStoreReadOnly
	}
	if dependencies.putEvidence == nil {
		dependencies.putEvidence = defaults.putEvidence
	}
	if dependencies.runExperiment == nil {
		dependencies.runExperiment = defaults.runExperiment
	}
	if dependencies.buildManifest == nil {
		dependencies.buildManifest = defaults.buildManifest
	}
	if dependencies.loadAndSyncManifest == nil {
		dependencies.loadAndSyncManifest = defaults.loadAndSyncManifest
	}
	if dependencies.writeManifest == nil {
		dependencies.writeManifest = defaults.writeManifest
	}
	if dependencies.auditSnapshot == nil {
		dependencies.auditSnapshot = defaults.auditSnapshot
	}
	if dependencies.buildReport == nil {
		dependencies.buildReport = defaults.buildReport
	}
	if dependencies.buildDiff == nil {
		dependencies.buildDiff = defaults.buildDiff
	}
	if dependencies.buildNoBaseline == nil {
		dependencies.buildNoBaseline = defaults.buildNoBaseline
	}
	if dependencies.resolvePolicyPath == nil {
		dependencies.resolvePolicyPath = defaults.resolvePolicyPath
	}
	if dependencies.readFile == nil {
		dependencies.readFile = defaults.readFile
	}
	if dependencies.writeAtomic == nil {
		dependencies.writeAtomic = defaults.writeAtomic
	}
	return dependencies
}

func (app application) verifyProvenance(ctx context.Context, root string, stderr io.Writer) (inputdigest.State, int, bool) {
	state, issue, err := verifyExecutableProvenance(ctx, root, provenanceDependencies{
		computeState:  app.dependencies.computeState,
		readBuildInfo: app.dependencies.readBuildInfo,
	})
	if err != nil {
		writeCommandIssue(stderr, "source_state_unavailable", "current source state is unavailable")
		return inputdigest.State{}, ExitArgumentOrLoadFailure, false
	}
	if issue != nil {
		writeCommandIssue(stderr, issue.Code, issue.Message)
		return inputdigest.State{}, ExitArgumentOrLoadFailure, false
	}
	return state, ExitSuccess, true
}

func writeCommandIssue(stderr io.Writer, code, message string) {
	writeCLIError(stderr, "error [%s]: %s", code, quoteDiagnosticField(message))
}

type storageErrorClass uint8

const (
	storageUnknown storageErrorClass = iota
	storageOperational
	storageInvalid
	storageMissing
)

func classifyReleaseStorageError(err error) storageErrorClass {
	switch {
	case isReleaseOperationalError(err):
		return storageOperational
	case errors.Is(err, release.ErrSnapshotCorrupt),
		errors.Is(err, release.ErrSnapshotUnsafePath),
		errors.Is(err, release.ErrManifestInvalid),
		errors.Is(err, release.ErrManifestNonCanonical),
		errors.Is(err, release.ErrManifestTooLarge):
		return storageInvalid
	case errors.Is(err, release.ErrSnapshotNotFound):
		return storageMissing
	default:
		return storageUnknown
	}
}

func classifyEvidenceStorageError(err error) storageErrorClass {
	switch {
	case isReleaseOperationalError(err):
		return storageOperational
	case errors.Is(err, evidence.ErrEvidenceUnsafePath),
		errors.Is(err, evidence.ErrEvidenceCorrupt),
		errors.Is(err, evidence.ErrEvidenceTooLarge),
		errors.Is(err, evidence.ErrEvidenceInvalid):
		return storageInvalid
	case errors.Is(err, evidence.ErrEvidenceNotFound):
		return storageMissing
	default:
		return storageUnknown
	}
}

func releaseEvidenceStoreDiagnostic(err error) (validator.Diagnostic, bool) {
	switch classifyEvidenceStorageError(err) {
	case storageInvalid:
		return validator.Diagnostic{Code: release.CodeReleaseEvidenceInvalid, Severity: "error", Message: "release evidence storage is unsafe or invalid"}, true
	case storageMissing:
		return validator.Diagnostic{Code: release.CodeReleaseEvidenceMissing, Severity: "error", Message: "release evidence storage is missing"}, true
	default:
		return validator.Diagnostic{}, false
	}
}

func isReleaseOperationalError(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, release.ErrSnapshotIO) ||
		errors.Is(err, evidence.ErrEvidenceIO)
}

func writeReleaseEvidenceStoreError(stderr io.Writer, err error) int {
	if diagnostic, semantic := releaseEvidenceStoreDiagnostic(err); semantic {
		writeCommandIssue(stderr, diagnostic.Code, diagnostic.Message)
		return ExitReleaseFailure
	}
	writeCommandIssue(stderr, "release_evidence_store_io", "release evidence store cannot be opened")
	return ExitArgumentOrLoadFailure
}

type trackingWriter struct {
	destination io.Writer
	err         error
}

func (writer *trackingWriter) Write(value []byte) (int, error) {
	if writer.err != nil {
		return 0, writer.err
	}
	if writer.destination == nil {
		writer.err = errors.New("nil writer")
		return 0, writer.err
	}
	written, err := writer.destination.Write(value)
	if err == nil && written != len(value) {
		err = io.ErrShortWrite
	}
	if err != nil {
		writer.err = err
	}
	return written, err
}

func (writer *trackingWriter) Err() error {
	return writer.err
}

func writeFull(writer io.Writer, value []byte) error {
	written, err := writer.Write(value)
	if err != nil {
		return err
	}
	if written != len(value) {
		return io.ErrShortWrite
	}
	return nil
}

func newFlagSet(command string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet("whiteboard "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "Usage of whiteboard %s:\n", command)
		flags.SetOutput(stderr)
		flags.PrintDefaults()
		flags.SetOutput(io.Discard)
	}
	return flags
}

func parseFlagSet(flags *flag.FlagSet, args []string, stderr io.Writer) (bool, int) {
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return false, ExitSuccess
		}
		writeCLIError(stderr, "invalid command arguments")
		return false, ExitArgumentOrLoadFailure
	}
	if flags.NArg() != 0 {
		writeCLIError(stderr, "unexpected positional arguments")
		return false, ExitArgumentOrLoadFailure
	}
	return true, ExitSuccess
}

type trackedString struct {
	value string
	set   bool
}

func (value *trackedString) String() string {
	return value.value
}

func (value *trackedString) Set(next string) error {
	value.value = next
	value.set = true
	return nil
}

func sortDiagnostics(diagnostics []validator.Diagnostic) []validator.Diagnostic {
	if diagnostics == nil {
		diagnostics = []validator.Diagnostic{}
	}
	sort.Slice(diagnostics, func(left, right int) bool {
		if diagnostics[left].Code != diagnostics[right].Code {
			return diagnostics[left].Code < diagnostics[right].Code
		}
		if diagnostics[left].Path != diagnostics[right].Path {
			return diagnostics[left].Path < diagnostics[right].Path
		}
		if diagnostics[left].EntityID != diagnostics[right].EntityID {
			return diagnostics[left].EntityID < diagnostics[right].EntityID
		}
		return diagnostics[left].Message < diagnostics[right].Message
	})
	return diagnostics
}

func sanitizeReleaseDiagnostics(source []validator.Diagnostic, coverage *validator.Coverage) []validator.Diagnostic {
	diagnostics := make([]validator.Diagnostic, 0, len(source))
	for _, input := range source {
		code, message := stableReleaseDiagnostic(input.Code, coverage)
		diagnostics = append(diagnostics, validator.Diagnostic{
			Code:     code,
			Severity: "error",
			EntityID: stableReleaseEntityID(input.EntityID),
			Message:  message,
		})
	}
	return sortDiagnostics(diagnostics)
}

func stableReleaseDiagnostic(code string, coverage *validator.Coverage) (string, string) {
	if code == validator.CodeReleaseScopeIncomplete {
		if coverage == nil {
			return code, "release scope is incomplete"
		}
		missing := stableReleaseIDs(coverage.MissingCaseIDs)
		unexpected := stableReleaseIDs(coverage.UnexpectedCaseIDs)
		return code, fmt.Sprintf(
			"release scope incomplete: complete=%d baseline=%d missing=%d unexpected=%d missing_ids=%v unexpected_ids=%v",
			coverage.CompleteTotal,
			coverage.BaselineTotal,
			len(missing),
			len(unexpected),
			missing,
			unexpected,
		)
	}
	if isKnownReleaseDiagnosticCode(code) {
		return code, "release validation failed: " + code
	}
	return release.CodeReleaseManifestInvalid, "release diagnostic code is unsupported"
}

func sanitizeReleaseValidationReport(report validator.Report) validator.Report {
	report.Coverage = sanitizeReleaseCoverage(report.Coverage)
	report.Diagnostics = sanitizeReleaseDiagnostics(report.Diagnostics, &report.Coverage)
	report.Matrix = sanitizeReleaseMatrix(report.Matrix)
	return report
}

func sanitizeReleaseCoverage(source validator.Coverage) validator.Coverage {
	coverage := validator.Coverage{
		BaselineTotal:         source.BaselineTotal,
		CompleteTotal:         source.CompleteTotal,
		MissingCaseIDs:        stableReleaseIDs(source.MissingCaseIDs),
		UnexpectedCaseIDs:     stableReleaseIDs(source.UnexpectedCaseIDs),
		Families:              make([]validator.FamilyCoverage, 0, len(source.Families)),
		RequiredPrinciples:    stableReleaseIDs(source.RequiredPrinciples),
		RequiredScenarioLabs:  stableReleaseIDs(source.RequiredScenarioLabs),
		RequiredPrimitiveLabs: stableReleaseIDs(source.RequiredPrimitiveLabs),
		RequiredAdapters:      stableReleaseIDs(source.RequiredAdapters),
	}
	for _, family := range source.Families {
		if evidence.ValidateStableID(family.ID) != nil {
			continue
		}
		coverage.Families = append(coverage.Families, family)
	}
	sort.Slice(coverage.Families, func(left, right int) bool {
		return coverage.Families[left].ID < coverage.Families[right].ID
	})
	return coverage
}

func sanitizeReleaseMatrix(source []validator.MatrixCell) []validator.MatrixCell {
	matrix := make([]validator.MatrixCell, 0, len(source))
	for _, cell := range source {
		if !safeReleaseMatrixCell(cell) {
			continue
		}
		cell.Faults = append([]string{}, cell.Faults...)
		cell.AssertionIDs = append([]string{}, cell.AssertionIDs...)
		matrix = append(matrix, cell)
	}
	return matrix
}

func safeReleaseMatrixCell(cell validator.MatrixCell) bool {
	identities := []string{
		cell.LabID,
		cell.RequiredRunID,
		cell.BindingID,
		cell.ClaimID,
		cell.ImplementationID,
		cell.Workload,
	}
	if cell.AdapterID != "" {
		identities = append(identities, cell.AdapterID)
	}
	identities = append(identities, cell.Faults...)
	identities = append(identities, cell.AssertionIDs...)
	for _, identity := range identities {
		if evidence.ValidateStableID(identity) != nil {
			return false
		}
	}
	switch evidence.Role(cell.Role) {
	case evidence.RoleBaseline, evidence.RoleVariant, evidence.RoleAdapter:
		return true
	default:
		return false
	}
}

func stableReleaseIDs(source []string) []string {
	identities := make([]string, 0, len(source))
	for _, identity := range source {
		if evidence.ValidateStableID(identity) == nil {
			identities = append(identities, identity)
		}
	}
	sort.Strings(identities)
	return identities
}

func isKnownReleaseDiagnosticCode(code string) bool {
	switch code {
	case release.CodeReleaseSnapshotMissing,
		release.CodeReleaseManifestInvalid,
		release.CodeReleaseInputDigestMismatch,
		release.CodeReleaseProfileMismatch,
		release.CodeReleaseSelectionOrderMismatch,
		release.CodeReleaseCellMissing,
		release.CodeReleaseCellDuplicate,
		release.CodeReleaseCellMismatch,
		release.CodeReleaseEvidenceMissing,
		release.CodeReleaseEvidenceInvalid,
		release.CodeReleaseEvidenceReused,
		release.CodeReleaseContentDigestMismatch,
		release.CodeReleaseDefinitionMismatch,
		release.CodeReleaseStatusNotPassed,
		release.CodeReleaseAssertionMismatch,
		release.CodeReleaseRunSetMismatch,
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
		codeExperimentRegistryMissing,
		codeExperimentDefinitionMismatch:
		return true
	default:
		return false
	}
}

func stableReleaseEntityID(entityID string) string {
	if entityID == "" {
		return ""
	}
	if evidence.ValidateID(entityID) == nil || evidence.ValidateRunSetID(evidence.RunSetID(entityID)) == nil {
		return ""
	}
	if evidence.ValidateStableID(entityID) == nil {
		return entityID
	}
	return ""
}

func writeDiagnostics(writer io.Writer, diagnostics []validator.Diagnostic) error {
	for _, diagnostic := range diagnostics {
		var line strings.Builder
		fmt.Fprintf(&line, "%s [%s]", diagnostic.Severity, diagnostic.Code)
		if diagnostic.Path != "" {
			fmt.Fprintf(&line, " path=%s", quoteDiagnosticField(diagnostic.Path))
		}
		if diagnostic.EntityID != "" {
			fmt.Fprintf(&line, " entity=%s", quoteDiagnosticField(diagnostic.EntityID))
		}
		fmt.Fprintf(&line, ": %s\n", quoteDiagnosticField(diagnostic.Message))
		if err := writeFull(writer, []byte(line.String())); err != nil {
			return err
		}
	}
	return nil
}

func quoteDiagnosticField(value string) string {
	return strconv.Quote(value)
}

func writeCLIError(stderr io.Writer, format string, values ...any) {
	_, _ = fmt.Fprintf(stderr, format+"\n", values...)
}
