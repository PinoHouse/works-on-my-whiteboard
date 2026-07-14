package cli

import (
	"context"
	"strings"
	"testing"
)

func TestValidateCommandDevelopmentSuccessAndJSONRendering(t *testing.T) {
	root := writeEmptyCatalogRoot(t)
	exitCode, stdout, stderr := runCLI(context.Background(), []string{"validate", "--root", root})
	if exitCode != 0 || stdout != "validation passed\n" || stderr != "" {
		t.Fatalf("validate exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}

	exitCode, stdout, stderr = runCLI(context.Background(), []string{"validate", "--root", root, "--format", "json"})
	if exitCode != 0 || stderr != "" {
		t.Fatalf("validate json exit=%d stderr=%q", exitCode, stderr)
	}
	if !strings.HasPrefix(stdout, "{\n  \"diagnostics\": []") || !strings.HasSuffix(stdout, "\n") || strings.HasSuffix(stdout, "\n\n") {
		t.Fatalf("validate json stdout = %q; want two-space JSON and one newline", stdout)
	}
}

func TestValidateCommandContentIsNeverANoOpAndReleaseImpliesContent(t *testing.T) {
	root := writeInvalidCompleteCaseRoot(t)

	exitCode, stdout, stderr := runCLI(context.Background(), []string{"validate", "--root", root})
	if exitCode != 3 || stdout != "" {
		t.Fatalf("plain validate exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if !strings.Contains(stderr, "complete_contract_empty") || strings.Contains(stderr, "heading_contract_mismatch") {
		t.Fatalf("plain validate stderr = %q; want semantic diagnostics only", stderr)
	}

	exitCode, stdout, stderr = runCLI(context.Background(), []string{"validate", "--root", root, "--content"})
	if exitCode != 3 || stdout != "" || !strings.Contains(stderr, "heading_contract_mismatch") {
		t.Fatalf("content validate exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}

	exitCode, stdout, stderr = runCLI(context.Background(), []string{"validate", "--root", root, "--content=false", "--release", "current"})
	if exitCode != 4 || stdout != "" || !strings.Contains(stderr, "heading_contract_mismatch") {
		t.Fatalf("release validate exit=%d stdout=%q stderr=%q; release must imply content", exitCode, stdout, stderr)
	}
}

func TestValidateCommandLocksArgumentsAndReleaseDigest(t *testing.T) {
	root := writeEmptyCatalogRoot(t)
	validDigest := "sha256:" + strings.Repeat("a", 64)
	tests := []struct {
		name string
		args []string
	}{
		{name: "unknown_flag", args: []string{"validate", "--unknown"}},
		{name: "positional", args: []string{"validate", "extra"}},
		{name: "bad_format", args: []string{"validate", "--format", "markdown"}},
		{name: "empty_release", args: []string{"validate", "--release="}},
		{name: "bad_release", args: []string{"validate", "--release", "latest"}},
		{name: "uppercase_digest", args: []string{"validate", "--release", "sha256:" + strings.Repeat("A", 64)}},
		{name: "short_digest", args: []string{"validate", "--release", "sha256:abc"}},
		{name: "invalid_before_load", args: []string{"validate", "--root", "missing", "--release", "invalid"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCLI(context.Background(), test.args)
			if exitCode != 2 || stdout != "" || stderr == "" {
				t.Fatalf("Run(%q) exit=%d stdout=%q stderr=%q", test.args, exitCode, stdout, stderr)
			}
		})
	}

	for _, release := range []string{"current", validDigest} {
		exitCode, _, stderr := runCLI(context.Background(), []string{"validate", "--root", root, "--release", release})
		if exitCode != 0 || stderr != "" {
			t.Fatalf("release %q exit=%d stderr=%q", release, exitCode, stderr)
		}
	}
}

func TestValidateCommandScalarFlagsUseLastValue(t *testing.T) {
	root := writeEmptyCatalogRoot(t)
	exitCode, stdout, stderr := runCLI(context.Background(), []string{
		"validate",
		"--root", "missing",
		"--root", root,
		"--format", "invalid",
		"--format", "json",
	})
	if exitCode != 0 || stderr != "" || !strings.HasPrefix(stdout, "{\n") {
		t.Fatalf("last-value flags exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}

func TestValidateCommandLoadAndContextFailuresUseExitTwo(t *testing.T) {
	exitCode, stdout, stderr := runCLI(context.Background(), []string{"validate", "--root", t.TempDir()})
	if exitCode != 2 || stdout != "" || stderr == "" {
		t.Fatalf("load failure exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}

	root := writeEmptyCatalogRoot(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	exitCode, stdout, stderr = runCLI(ctx, []string{"validate", "--root", root})
	if exitCode != 2 || stdout != "" || !strings.Contains(stderr, "context canceled") {
		t.Fatalf("context failure exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}
