package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
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
	for _, command := range []string{"validate", "coverage"} {
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
