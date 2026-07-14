package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/content"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

func runValidate(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	flags := newFlagSet("validate", stderr)
	root := "."
	format := "text"
	contentEnabled := false
	release := trackedString{}
	flags.StringVar(&root, "root", root, "repository root")
	flags.BoolVar(&contentEnabled, "content", false, "validate Markdown content and internal links")
	flags.Var(&release, "release", "release input: current or sha256:<64 lowercase hex>")
	flags.StringVar(&format, "format", format, "output format: text or json")
	if proceed, exitCode := parseFlagSet(flags, args, stderr); !proceed {
		return exitCode
	}
	if format != "text" && format != "json" {
		writeCLIError(stderr, "invalid validate format %q; want text or json", format)
		return ExitArgumentOrLoadFailure
	}
	if release.set && !validReleaseInput(release.value) {
		writeCLIError(stderr, "invalid release %q; want current or sha256:<64 lowercase hex>", release.value)
		return ExitArgumentOrLoadFailure
	}

	repository, err := catalog.LoadDir(ctx, root)
	if err != nil {
		writeCLIError(stderr, "load catalog: %v", err)
		return ExitArgumentOrLoadFailure
	}
	mode := validator.ModeDevelopment
	if release.set {
		mode = validator.ModeRelease
		contentEnabled = true
	}
	report := validator.Validate(repository, mode)
	diagnostics := append([]validator.Diagnostic{}, report.Diagnostics...)
	if contentEnabled {
		diagnostics = append(diagnostics, content.ValidateRepository(root, repository).Diagnostics...)
	}
	report.Diagnostics = sortDiagnostics(diagnostics)

	destination := stdout
	exitCode := ExitSuccess
	if len(report.Diagnostics) != 0 {
		destination = stderr
		if release.set {
			exitCode = ExitReleaseFailure
		} else {
			exitCode = ExitDevelopmentFailure
		}
	}
	if err := renderValidation(destination, format, report); err != nil {
		writeCLIError(stderr, "write validation result: %v", err)
		return ExitArgumentOrLoadFailure
	}
	return exitCode
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
		_, err = writer.Write(encoded)
		return err
	}
	if len(report.Diagnostics) == 0 {
		_, err := fmt.Fprintln(writer, "validation passed")
		return err
	}
	return writeDiagnostics(writer, report.Diagnostics)
}
