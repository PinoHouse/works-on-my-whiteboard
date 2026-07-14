package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const emptyCoverageJSON = `{
  "baseline_total": 0,
  "complete_total": 0,
  "missing_case_ids": [],
  "unexpected_case_ids": [],
  "families": [],
  "required_principles": [],
  "required_scenario_labs": [],
  "required_primitive_labs": [],
  "required_adapters": []
}
`

func TestCoverageCommandRendersDeterministicFormats(t *testing.T) {
	root := writeEmptyCatalogRoot(t)
	exitCode, stdout, stderr := runCLI(context.Background(), []string{"coverage", "--root", root, "--format", "json"})
	if exitCode != 0 || stdout != emptyCoverageJSON || stderr != "" {
		t.Fatalf("coverage json exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}

	for _, format := range []string{"text", "markdown"} {
		firstExit, first, firstErr := runCLI(context.Background(), []string{"coverage", "--root", root, "--format", format})
		secondExit, second, secondErr := runCLI(context.Background(), []string{"coverage", "--root", root, "--format", format})
		if firstExit != 0 || secondExit != 0 || firstErr != "" || secondErr != "" || first != second {
			t.Fatalf("coverage %s is not deterministic: first=(%d,%q,%q) second=(%d,%q,%q)", format, firstExit, first, firstErr, secondExit, second, secondErr)
		}
		if !strings.HasSuffix(first, "\n") || strings.HasSuffix(first, "\n\n") {
			t.Fatalf("coverage %s output = %q; want exactly one trailing newline", format, first)
		}
	}
}

func TestCoverageCommandWritesAtomicallyWithModeAndNoStdout(t *testing.T) {
	root := writeEmptyCatalogRoot(t)
	output := filepath.Join(t.TempDir(), "coverage.json")
	exitCode, stdout, stderr := runCLI(context.Background(), []string{"coverage", "--root", root, "--format", "json", "--output", output})
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("coverage output exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != emptyCoverageJSON {
		t.Fatalf("output = %q; want exact JSON", data)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("output mode = %o; want 0644", info.Mode().Perm())
	}
}

func TestCoverageCommandCheckIsStrictAndReadOnly(t *testing.T) {
	root := writeEmptyCatalogRoot(t)
	directory := t.TempDir()
	output := filepath.Join(directory, "coverage.json")
	writeCLIFile(t, output, emptyCoverageJSON)
	before, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(output, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(output, 0o600) })

	exitCode, stdout, stderr := runCLI(context.Background(), []string{"coverage", "--root", root, "--format", "json", "--output", output, "--check"})
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("matching check exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	after, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) || after.Mode().Perm() != 0o400 {
		t.Fatalf("check mutated output: before=%v/%o after=%v/%o", before.ModTime(), before.Mode().Perm(), after.ModTime(), after.Mode().Perm())
	}

	if err := os.Chmod(output, 0o600); err != nil {
		t.Fatal(err)
	}
	writeCLIFile(t, output, "stale\n")
	staleTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(output, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}
	exitCode, stdout, stderr = runCLI(context.Background(), []string{"coverage", "--root", root, "--format", "json", "--output", output, "--check"})
	if exitCode != 3 || stdout != "" || !strings.Contains(stderr, "does not match") {
		t.Fatalf("mismatch check exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != "stale\n" {
		t.Fatalf("mismatch check mutated output: data=%q err=%v", data, err)
	}
}

func TestCoverageCommandCheckMissingIsExitThreeWithoutWrites(t *testing.T) {
	root := writeEmptyCatalogRoot(t)
	missingParent := filepath.Join(t.TempDir(), "missing")
	output := filepath.Join(missingParent, "coverage.json")
	exitCode, stdout, stderr := runCLI(context.Background(), []string{"coverage", "--root", root, "--output", output, "--check"})
	if exitCode != 3 || stdout != "" || stderr == "" {
		t.Fatalf("missing check exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if _, err := os.Stat(missingParent); !os.IsNotExist(err) {
		t.Fatalf("check created missing parent: err=%v", err)
	}
}

func TestCoverageCommandOutputRequiresExistingParentAndCleansTempOnFailure(t *testing.T) {
	root := writeEmptyCatalogRoot(t)
	missingParent := filepath.Join(t.TempDir(), "missing")
	output := filepath.Join(missingParent, "coverage.json")
	exitCode, stdout, stderr := runCLI(context.Background(), []string{"coverage", "--root", root, "--output", output})
	if exitCode != 2 || stdout != "" || stderr == "" {
		t.Fatalf("missing parent exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if _, err := os.Stat(missingParent); !os.IsNotExist(err) {
		t.Fatalf("normal output created missing parent: err=%v", err)
	}

	directory := t.TempDir()
	targetDirectory := filepath.Join(directory, "coverage.md")
	writeCLIFile(t, filepath.Join(targetDirectory, "sentinel"), "keep")
	exitCode, _, _ = runCLI(context.Background(), []string{"coverage", "--root", root, "--output", targetDirectory})
	if exitCode != 2 {
		t.Fatalf("rename failure exit=%d; want 2", exitCode)
	}
	if data, err := os.ReadFile(filepath.Join(targetDirectory, "sentinel")); err != nil || string(data) != "keep" {
		t.Fatalf("rename failure damaged old target: data=%q err=%v", data, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".whiteboard-coverage-") {
			t.Fatalf("temporary file leaked after failure: %s", entry.Name())
		}
	}
}

func TestCoverageCommandTreatsDashAsRelativeFile(t *testing.T) {
	root := writeEmptyCatalogRoot(t)
	workingDirectory := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workingDirectory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	exitCode, stdout, stderr := runCLI(context.Background(), []string{"coverage", "--root", root, "--format", "json", "--output", "-"})
	if exitCode != 0 || stdout != "" || stderr != "" {
		t.Fatalf("dash output exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	data, err := os.ReadFile(filepath.Join(workingDirectory, "-"))
	if err != nil || string(data) != emptyCoverageJSON {
		t.Fatalf("dash file data=%q err=%v", data, err)
	}
}

func TestCoverageCommandRejectsArgumentsAndSemanticFailures(t *testing.T) {
	root := writeEmptyCatalogRoot(t)
	for _, args := range [][]string{
		{"coverage", "--root", root, "--format", "yaml"},
		{"coverage", "--root", root, "extra"},
		{"coverage", "--root", root, "--check"},
		{"coverage", "--unknown"},
	} {
		exitCode, stdout, stderr := runCLI(context.Background(), args)
		if exitCode != 2 || stdout != "" || stderr == "" {
			t.Fatalf("Run(%q) exit=%d stdout=%q stderr=%q", args, exitCode, stdout, stderr)
		}
	}

	invalidRoot := writeInvalidCompleteCaseRoot(t)
	exitCode, stdout, stderr := runCLI(context.Background(), []string{"coverage", "--root", invalidRoot})
	if exitCode != 3 || stdout != "" || !strings.Contains(stderr, "complete_contract_empty") {
		t.Fatalf("semantic failure exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}

func TestCoverageCommandScalarFlagsUseLastValue(t *testing.T) {
	root := writeEmptyCatalogRoot(t)
	exitCode, stdout, stderr := runCLI(context.Background(), []string{
		"coverage",
		"--root", "missing",
		"--root", root,
		"--format", "invalid",
		"--format", "json",
	})
	if exitCode != 0 || stdout != emptyCoverageJSON || stderr != "" {
		t.Fatalf("last-value flags exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}

func TestCoverageCommandDiagnosticWriterFailureUsesExitTwo(t *testing.T) {
	root := writeInvalidCompleteCaseRoot(t)
	exitCode := Run(context.Background(), []string{"coverage", "--root", root}, io.Discard, failingWriter{})
	if exitCode != 2 {
		t.Fatalf("diagnostic writer failure exit=%d; want 2", exitCode)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("writer failed")
}
