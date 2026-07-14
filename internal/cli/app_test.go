package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
