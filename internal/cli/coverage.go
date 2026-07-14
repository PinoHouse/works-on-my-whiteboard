package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

func runCoverage(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	flags := newFlagSet("coverage", stderr)
	root := "."
	format := "text"
	output := trackedString{}
	check := false
	flags.StringVar(&root, "root", root, "repository root")
	flags.StringVar(&format, "format", format, "output format: text, json, or markdown")
	flags.Var(&output, "output", "output path")
	flags.BoolVar(&check, "check", false, "compare rendered bytes with --output without writing")
	if proceed, exitCode := parseFlagSet(flags, args, stderr); !proceed {
		return exitCode
	}
	if format != "text" && format != "json" && format != "markdown" {
		writeCLIError(stderr, "invalid coverage format %q; want text, json, or markdown", format)
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

	repository, err := catalog.LoadDir(ctx, root)
	if err != nil {
		writeCLIError(stderr, "load catalog: %v", err)
		return ExitArgumentOrLoadFailure
	}
	report := validator.Validate(repository, validator.ModeDevelopment)
	if len(report.Diagnostics) != 0 {
		if err := writeDiagnostics(stderr, report.Diagnostics); err != nil {
			writeCLIError(stderr, "write validation diagnostics: %v", err)
			return ExitArgumentOrLoadFailure
		}
		return ExitDevelopmentFailure
	}
	rendered, err := renderCoverage(format, report.Coverage)
	if err != nil {
		writeCLIError(stderr, "render coverage: %v", err)
		return ExitArgumentOrLoadFailure
	}
	if check {
		actual, readErr := os.ReadFile(output.value)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				writeCLIError(stderr, "coverage output %q is missing", output.value)
				return ExitDevelopmentFailure
			}
			writeCLIError(stderr, "read coverage output %q: %v", output.value, readErr)
			return ExitArgumentOrLoadFailure
		}
		if !bytes.Equal(actual, rendered) {
			writeCLIError(stderr, "coverage output %q does not match rendered %s bytes", output.value, format)
			return ExitDevelopmentFailure
		}
		return ExitSuccess
	}
	if !output.set {
		if _, err := stdout.Write(rendered); err != nil {
			writeCLIError(stderr, "write coverage result: %v", err)
			return ExitArgumentOrLoadFailure
		}
		return ExitSuccess
	}
	if err := writeCoverageAtomically(output.value, rendered); err != nil {
		writeCLIError(stderr, "write coverage output %q: %v", output.value, err)
		return ExitArgumentOrLoadFailure
	}
	return ExitSuccess
}

func renderCoverage(format string, coverage validator.Coverage) ([]byte, error) {
	switch format {
	case "json":
		encoded, err := json.MarshalIndent(coverage, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(encoded, '\n'), nil
	case "text":
		return renderCoverageText(coverage), nil
	case "markdown":
		return renderCoverageMarkdown(coverage), nil
	default:
		return nil, fmt.Errorf("unsupported coverage format %q", format)
	}
}

func renderCoverageText(coverage validator.Coverage) []byte {
	var builder strings.Builder
	fmt.Fprintf(&builder, "baseline_total: %d\n", coverage.BaselineTotal)
	fmt.Fprintf(&builder, "complete_total: %d\n", coverage.CompleteTotal)
	writeTextList(&builder, "missing_case_ids", coverage.MissingCaseIDs)
	writeTextList(&builder, "unexpected_case_ids", coverage.UnexpectedCaseIDs)
	builder.WriteString("families:\n")
	for _, family := range coverage.Families {
		fmt.Fprintf(&builder, "  - %s required=%d complete=%d\n", family.ID, family.Required, family.Complete)
	}
	writeTextList(&builder, "required_principles", coverage.RequiredPrinciples)
	writeTextList(&builder, "required_scenario_labs", coverage.RequiredScenarioLabs)
	writeTextList(&builder, "required_primitive_labs", coverage.RequiredPrimitiveLabs)
	writeTextList(&builder, "required_adapters", coverage.RequiredAdapters)
	return []byte(builder.String())
}

func writeTextList(builder *strings.Builder, name string, values []string) {
	builder.WriteString(name)
	builder.WriteString(":\n")
	for _, value := range values {
		builder.WriteString("  - ")
		builder.WriteString(value)
		builder.WriteByte('\n')
	}
}

func renderCoverageMarkdown(coverage validator.Coverage) []byte {
	var builder strings.Builder
	builder.WriteString("# Coverage\n\n")
	fmt.Fprintf(&builder, "- Baseline total: %d\n", coverage.BaselineTotal)
	fmt.Fprintf(&builder, "- Complete total: %d\n", coverage.CompleteTotal)
	fmt.Fprintf(&builder, "- Missing cases: %d\n", len(coverage.MissingCaseIDs))
	fmt.Fprintf(&builder, "- Unexpected cases: %d\n", len(coverage.UnexpectedCaseIDs))
	builder.WriteString("\n## Families\n\n")
	builder.WriteString("| Family | Required | Complete |\n")
	builder.WriteString("| --- | ---: | ---: |\n")
	for _, family := range coverage.Families {
		fmt.Fprintf(&builder, "| `%s` | %d | %d |\n", family.ID, family.Required, family.Complete)
	}
	writeMarkdownList(&builder, "Missing cases", coverage.MissingCaseIDs)
	writeMarkdownList(&builder, "Unexpected cases", coverage.UnexpectedCaseIDs)
	writeMarkdownList(&builder, "Required principles", coverage.RequiredPrinciples)
	writeMarkdownList(&builder, "Required scenario labs", coverage.RequiredScenarioLabs)
	writeMarkdownList(&builder, "Required primitive labs", coverage.RequiredPrimitiveLabs)
	writeMarkdownList(&builder, "Required adapters", coverage.RequiredAdapters)
	return []byte(strings.TrimRight(builder.String(), "\n") + "\n")
}

func writeMarkdownList(builder *strings.Builder, heading string, values []string) {
	builder.WriteString("\n## ")
	builder.WriteString(heading)
	builder.WriteString("\n\n")
	if len(values) == 0 {
		builder.WriteString("- None\n")
		return
	}
	for _, value := range values {
		builder.WriteString("- `")
		builder.WriteString(value)
		builder.WriteString("`\n")
	}
}

func writeCoverageAtomically(path string, data []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".whiteboard-coverage-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		if temporaryPath != "" {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o644); err != nil {
		return err
	}
	written, err := temporary.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	temporaryPath = ""
	return nil
}
