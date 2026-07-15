package integration_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

const task10FakeWhiteboard = `#!/bin/sh
set -eu

command_name=${1:-}
if [ "$#" -gt 0 ]; then
  shift
fi
{
  printf 'CALL %s\n' "$command_name"
  for argument do
    printf 'ARG %s\n' "$argument"
  done
} >>"$TASK10_TEST_LOG"

task10_value_after() {
  task10_want=$1
  shift
  while [ "$#" -gt 0 ]; do
    if [ "$1" = "$task10_want" ]; then
      shift
      if [ "$#" -gt 0 ]; then
        printf '%s\n' "$1"
        return 0
      fi
      return 1
    fi
    shift
  done
  return 1
}

case "$command_name" in
  run)
    if [ "${TASK10_FAIL_BEFORE_WRITE_COMMAND:-}" = run ]; then
      printf 'run failed before evidence\n'
      printf 'preflight rejected the run\n' >&2
      exit "${TASK10_FAIL_STATUS:-6}"
    fi
    task10_evidence_root=$(task10_value_after --evidence-root "$@")
    mkdir -p "$task10_evidence_root/runs" "$task10_evidence_root/releases/current"
    printf 'partial run\n' >"$task10_evidence_root/runs/partial.json"
    if [ "${TASK10_BLOCK_COMMAND:-}" = run ]; then
      printf '%s\n' "$task10_evidence_root" >"$TASK10_READY_FILE"
      while :; do sleep 1; done
    fi
    if [ "${TASK10_SIGNAL_COMMAND:-}" = run ]; then
      kill -s TERM "$$"
    fi
    if [ "${TASK10_FAIL_COMMAND:-}" = run ]; then
      exit "${TASK10_FAIL_STATUS:-5}"
    fi
    printf 'manifest\n' >"$task10_evidence_root/releases/current/manifest.yaml"
    ;;
  report)
    task10_output=$(task10_value_after --output "$@")
    printf 'partial report\n' >"$task10_output"
    if [ "${TASK10_FAIL_COMMAND:-}" = report ]; then
      exit "${TASK10_FAIL_STATUS:-4}"
    fi
    printf '{"report":"complete"}\n' >"$task10_output"
    ;;
  diff)
    task10_output=$(task10_value_after --output "$@")
    if [ "${TASK10_SIGNAL_COMMAND:-}" = diff ]; then
      kill -s TERM "$$"
    fi
    if [ "${TASK10_FAIL_COMMAND:-}" = diff ]; then
      exit "${TASK10_FAIL_STATUS:-4}"
    fi
    printf '# diff\n' >"$task10_output"
    ;;
  validate)
    if [ -n "${TASK10_VALIDATE_STDOUT:-}" ]; then
      printf '%s' "$TASK10_VALIDATE_STDOUT"
    fi
    if [ -n "${TASK10_VALIDATE_JSON_PATH:-}" ]; then
      sed -n '1,$p' "$TASK10_VALIDATE_JSON_PATH" >&2
    fi
    exit "${TASK10_VALIDATE_STATUS:-4}"
    ;;
  *)
    exit 97
    ;;
esac
`

const task10FakeMake = `#!/bin/sh
set -eu
{
  printf 'MAKE\n'
  for argument do
    printf 'ARG %s\n' "$argument"
  done
} >>"$TASK10_TEST_LOG"
if [ "${TASK10_BLOCK_MAKE:-}" = 1 ]; then
  printf 'ready\n' >"$TASK10_READY_FILE"
  while :; do sleep 1; done
fi
if [ "${TASK10_SIGNAL_MAKE:-}" = 1 ]; then
  kill -s HUP "$$"
fi
exit "${TASK10_MAKE_STATUS:-0}"
`

const task10FakeSignalingGo = `#!/bin/sh
set -eu
case "$TASK10_SIGNAL_GO" in
  HUP) exit 129 ;;
  INT) exit 130 ;;
  QUIT) exit 131 ;;
  TERM) exit 143 ;;
  *) exit 99 ;;
esac
`

const task10FakeFailingCopy = `#!/bin/sh
set -eu
exit 1
`

type task10Diagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type task10FamilyCoverage struct {
	ID       string `json:"id"`
	Required int    `json:"required"`
	Complete int    `json:"complete"`
}

type task10Coverage struct {
	BaselineTotal         int                    `json:"baseline_total"`
	CompleteTotal         int                    `json:"complete_total"`
	MissingCaseIDs        []string               `json:"missing_case_ids"`
	UnexpectedCaseIDs     []string               `json:"unexpected_case_ids"`
	Families              []task10FamilyCoverage `json:"families"`
	RequiredPrinciples    []string               `json:"required_principles"`
	RequiredScenarioLabs  []string               `json:"required_scenario_labs"`
	RequiredPrimitiveLabs []string               `json:"required_primitive_labs"`
	RequiredAdapters      []string               `json:"required_adapters"`
}

type task10MatrixCell struct {
	LabID            string   `json:"lab_id"`
	RequiredRunID    string   `json:"required_run_id"`
	BindingID        string   `json:"binding_id"`
	ClaimID          string   `json:"claim_id"`
	Role             string   `json:"role"`
	ImplementationID string   `json:"implementation_id"`
	AdapterID        string   `json:"adapter_id,omitempty"`
	Workload         string   `json:"workload"`
	Faults           []string `json:"faults"`
	AssertionIDs     []string `json:"assertion_ids"`
}

type task10ReleaseReport struct {
	Diagnostics []task10Diagnostic `json:"diagnostics"`
	Coverage    task10Coverage     `json:"coverage"`
	Matrix      []task10MatrixCell `json:"matrix"`
}

func TestTask10VerifyExperimentsRejectsWrongArgumentsAndProfiles(t *testing.T) {
	script := task10ScriptPath(t, "verify-experiments.sh")
	temporary := t.TempDir()
	cases := []struct {
		name string
		args []string
	}{
		{name: "no arguments"},
		{name: "one argument", args: []string{"smoke"}},
		{name: "too many", args: []string{"smoke", "/repo", "/artifacts", "extra"}},
		{name: "unknown profile", args: []string{"fast", "/repo"}},
		{name: "empty profile", args: []string{"", "/repo"}},
		{name: "empty repository", args: []string{"smoke", ""}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			result := task10Run(t, script, testCase.args, []string{"TMPDIR=" + temporary})
			if result.exitCode != 2 {
				t.Fatalf("exit=%d, want 2; stdout=%q stderr=%q", result.exitCode, result.stdout, result.stderr)
			}
			if strings.Contains(result.stderr, temporary) || strings.Contains(result.stdout, temporary) {
				t.Fatalf("diagnostic leaked a temporary path: stdout=%q stderr=%q", result.stdout, result.stderr)
			}
		})
	}
	task10AssertDirectoryEmpty(t, temporary)
}

func TestTask10VerifyExperimentsUsesExternalSnapshotAndPreservesIsolatedArtifacts(t *testing.T) {
	repository, logPath := task10FakeRepository(t)
	temporary := filepath.Join(t.TempDir(), "external temp")
	artifactRoot := filepath.Join(t.TempDir(), "artifact root")
	task10MkdirAll(t, temporary)
	task10MkdirAll(t, artifactRoot)
	task10WriteFile(t, filepath.Join(artifactRoot, "keep.txt"), "keep\n", 0o644)

	result := task10Run(t, task10ScriptPath(t, "verify-experiments.sh"), []string{"smoke", repository, artifactRoot}, []string{
		"TMPDIR=" + temporary,
		"TASK10_TEST_LOG=" + logPath,
	})
	if result.exitCode != 0 || result.stdout != "" || result.stderr != "" {
		t.Fatalf("wrapper=(exit=%d stdout=%q stderr=%q), want clean success", result.exitCode, result.stdout, result.stderr)
	}
	if contents := task10ReadFile(t, filepath.Join(artifactRoot, "keep.txt")); contents != "keep\n" {
		t.Fatalf("pre-existing artifact changed to %q", contents)
	}
	preserved := task10SinglePreservedDirectory(t, artifactRoot)
	for _, relative := range []string{
		"evidence/runs/partial.json",
		"evidence/releases/current/manifest.yaml",
		"report.json",
		"commands/run.stdout",
		"commands/run.stderr",
		"commands/run.exit-status",
		"commands/report.stdout",
		"commands/report.stderr",
		"commands/report.exit-status",
	} {
		if _, err := os.Stat(filepath.Join(preserved, relative)); err != nil {
			t.Fatalf("preserved artifact %q: %v", relative, err)
		}
	}
	if status := task10ReadFile(t, filepath.Join(preserved, "commands", "run.exit-status")); status != "0\n" {
		t.Fatalf("run status=%q, want 0", status)
	}
	if status := task10ReadFile(t, filepath.Join(preserved, "commands", "report.exit-status")); status != "0\n" {
		t.Fatalf("report status=%q, want 0", status)
	}
	if _, err := os.Stat(filepath.Join(preserved, "diff.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("smoke diff artifact exists or has unexpected error: %v", err)
	}
	task10AssertDirectoryEmpty(t, temporary)

	calls := task10ParseCalls(t, logPath)
	if len(calls) != 2 || calls[0][0] != "run" || calls[1][0] != "report" {
		t.Fatalf("calls=%#v, want one run followed by one report", calls)
	}
	task10RequireArguments(t, calls[0][1:], "--required", "--profile", "smoke", "--root", repository, "--snapshot")
	runEvidenceRoot := task10ArgumentValue(t, calls[0][1:], "--evidence-root")
	reportEvidenceRoot := task10ArgumentValue(t, calls[1][1:], "--evidence-root")
	if runEvidenceRoot != reportEvidenceRoot {
		t.Fatalf("run evidence root %q differs from report root %q", runEvidenceRoot, reportEvidenceRoot)
	}
	if !task10PathWithin(runEvidenceRoot, temporary) || task10PathWithin(runEvidenceRoot, filepath.Join(repository, "evidence")) {
		t.Fatalf("evidence root %q is not an external temporary directory", runEvidenceRoot)
	}
	task10RequireArguments(t, calls[1][1:], "--release", "current", "--profile", "smoke", "--format", "json")
}

func TestTask10VerifyExperimentsRejectsRepositoryLocalTemporaryRoot(t *testing.T) {
	repository, logPath := task10FakeRepository(t)
	repositoryTemporary := filepath.Join(repository, "evidence", "temporary")
	task10MkdirAll(t, repositoryTemporary)
	result := task10Run(t, task10ScriptPath(t, "verify-experiments.sh"), []string{"smoke", repository}, []string{
		"TMPDIR=" + repositoryTemporary,
		"TASK10_TEST_LOG=" + logPath,
	})
	if result.exitCode != 2 {
		t.Fatalf("exit=%d, want 2 for repository-local temporary storage", result.exitCode)
	}
	if result.stdout != "" || strings.Contains(result.stderr, repository) {
		t.Fatalf("unsafe local-temporary diagnostic: stdout=%q stderr=%q", result.stdout, result.stderr)
	}
	if calls := task10ParseCalls(t, logPath); len(calls) != 0 {
		t.Fatalf("whiteboard ran with repository-local temporary storage: %#v", calls)
	}
	task10AssertDirectoryEmpty(t, repositoryTemporary)
}

func TestTask10VerifyExperimentsDoesNotLeakFailedArtifactPath(t *testing.T) {
	repository, logPath := task10FakeRepository(t)
	temporary := t.TempDir()
	artifactFile := filepath.Join(t.TempDir(), "artifact target")
	task10WriteFile(t, artifactFile, "keep\n", 0o644)
	result := task10Run(t, task10ScriptPath(t, "verify-experiments.sh"), []string{"smoke", repository, artifactFile}, []string{
		"TMPDIR=" + temporary,
		"TASK10_TEST_LOG=" + logPath,
	})
	if result.exitCode != 2 {
		t.Fatalf("exit=%d, want 2 when artifacts cannot be preserved", result.exitCode)
	}
	if result.stdout != "" || strings.Contains(result.stderr, artifactFile) || strings.Contains(result.stderr, temporary) {
		t.Fatalf("artifact failure leaked a path: stdout=%q stderr=%q", result.stdout, result.stderr)
	}
	if contents := task10ReadFile(t, artifactFile); contents != "keep\n" {
		t.Fatalf("existing artifact target was modified: %q", contents)
	}
	task10AssertDirectoryEmpty(t, temporary)
}

func TestTask10VerifyExperimentsPreservesDiagnosticsBeforeFirstEvidenceWrite(t *testing.T) {
	repository, logPath := task10FakeRepository(t)
	temporary := t.TempDir()
	artifacts := t.TempDir()
	result := task10Run(t, task10ScriptPath(t, "verify-experiments.sh"), []string{"deep", repository, artifacts}, []string{
		"TMPDIR=" + temporary,
		"TASK10_TEST_LOG=" + logPath,
		"TASK10_FAIL_BEFORE_WRITE_COMMAND=run",
		"TASK10_FAIL_STATUS=6",
	})
	if result.exitCode != 6 {
		t.Fatalf("exit=%d, want preflight status 6; stderr=%q", result.exitCode, result.stderr)
	}
	preserved := task10SinglePreservedDirectory(t, artifacts)
	if stdout := task10ReadFile(t, filepath.Join(preserved, "commands", "run.stdout")); stdout != "run failed before evidence\n" {
		t.Fatalf("preserved run stdout=%q", stdout)
	}
	if stderr := task10ReadFile(t, filepath.Join(preserved, "commands", "run.stderr")); stderr != "preflight rejected the run\n" {
		t.Fatalf("preserved run stderr=%q", stderr)
	}
	if status := task10ReadFile(t, filepath.Join(preserved, "commands", "run.exit-status")); status != "6\n" {
		t.Fatalf("preserved run status=%q", status)
	}
	task10AssertDirectoryEmpty(t, filepath.Join(preserved, "evidence"))
	if _, err := os.Stat(filepath.Join(preserved, "commands", "report.exit-status")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("report unexpectedly ran: %v", err)
	}
	task10AssertDirectoryEmpty(t, temporary)
}

func TestTask10VerifyExperimentsRemovesPartialArtifactCopy(t *testing.T) {
	repository, logPath := task10FakeRepository(t)
	temporary := t.TempDir()
	artifacts := t.TempDir()
	fakeBin := filepath.Join(t.TempDir(), "fake copy")
	task10MkdirAll(t, fakeBin)
	task10WriteFile(t, filepath.Join(fakeBin, "cp"), task10FakeFailingCopy, 0o755)
	result := task10Run(t, task10ScriptPath(t, "verify-experiments.sh"), []string{"smoke", repository, artifacts}, []string{
		"PATH=" + fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TMPDIR=" + temporary,
		"TASK10_TEST_LOG=" + logPath,
	})
	if result.exitCode != 2 {
		t.Fatalf("exit=%d, want artifact-copy failure 2", result.exitCode)
	}
	entries, err := os.ReadDir(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("partial artifact copy survived: %v", entries)
	}
	task10AssertDirectoryEmpty(t, temporary)
}

func TestTask10VerifyExperimentsTreatsDeepDiffAsInformational(t *testing.T) {
	repository, logPath := task10FakeRepository(t)
	temporary := t.TempDir()
	artifacts := t.TempDir()
	result := task10Run(t, task10ScriptPath(t, "verify-experiments.sh"), []string{"deep", repository, artifacts}, []string{
		"TMPDIR=" + temporary,
		"TASK10_TEST_LOG=" + logPath,
		"TASK10_FAIL_COMMAND=diff",
		"TASK10_FAIL_STATUS=4",
	})
	if result.exitCode != 0 {
		t.Fatalf("informational diff changed exit to %d; stderr=%q", result.exitCode, result.stderr)
	}
	calls := task10ParseCalls(t, logPath)
	if len(calls) != 3 || calls[0][0] != "run" || calls[1][0] != "report" || calls[2][0] != "diff" {
		t.Fatalf("calls=%#v, want run/report/diff", calls)
	}
	task10RequireArguments(t, calls[0][1:], "--profile", "deep", "--snapshot")
	task10RequireArguments(t, calls[1][1:], "--profile", "deep")
	task10RequireArguments(t, calls[2][1:], "--profile", "deep", "--left-evidence-root", filepath.Join(repository, "evidence"))
	preserved := task10SinglePreservedDirectory(t, artifacts)
	if _, err := os.Stat(filepath.Join(preserved, "report.json")); err != nil {
		t.Fatalf("report not preserved after diff failure: %v", err)
	}
	if status := task10ReadFile(t, filepath.Join(preserved, "commands", "diff.exit-status")); status != "4\n" {
		t.Fatalf("informational diff status=%q, want 4", status)
	}
	task10AssertDirectoryEmpty(t, temporary)
}

func TestTask10VerifyExperimentsPreservesChildStatusAndPartialArtifacts(t *testing.T) {
	tests := []struct {
		command string
		status  int
	}{
		{command: "run", status: 5},
		{command: "report", status: 4},
	}
	for _, testCase := range tests {
		t.Run(testCase.command, func(t *testing.T) {
			repository, logPath := task10FakeRepository(t)
			temporary := t.TempDir()
			artifacts := t.TempDir()
			result := task10Run(t, task10ScriptPath(t, "verify-experiments.sh"), []string{"deep", repository, artifacts}, []string{
				"TMPDIR=" + temporary,
				"TASK10_TEST_LOG=" + logPath,
				"TASK10_FAIL_COMMAND=" + testCase.command,
				fmt.Sprintf("TASK10_FAIL_STATUS=%d", testCase.status),
			})
			if result.exitCode != testCase.status {
				t.Fatalf("exit=%d, want child status %d; stderr=%q", result.exitCode, testCase.status, result.stderr)
			}
			preserved := task10SinglePreservedDirectory(t, artifacts)
			if _, err := os.Stat(filepath.Join(preserved, "evidence", "runs", "partial.json")); err != nil {
				t.Fatalf("partial run was not preserved: %v", err)
			}
			if status := task10ReadFile(t, filepath.Join(preserved, "commands", testCase.command+".exit-status")); status != fmt.Sprintf("%d\n", testCase.status) {
				t.Fatalf("preserved %s status=%q", testCase.command, status)
			}
			if testCase.command == "report" {
				if contents := task10ReadFile(t, filepath.Join(preserved, "report.json")); contents != "partial report\n" {
					t.Fatalf("partial report=%q", contents)
				}
			}
			task10AssertDirectoryEmpty(t, temporary)
		})
	}
}

func TestTask10VerifyExperimentsForwardsAndReraisesTermination(t *testing.T) {
	repository, logPath := task10FakeRepository(t)
	temporary := t.TempDir()
	artifacts := t.TempDir()
	ready := filepath.Join(t.TempDir(), "ready")
	command := exec.Command(task10ScriptPath(t, "verify-experiments.sh"), "deep", repository, artifacts)
	command.Env = append(os.Environ(),
		"TMPDIR="+temporary,
		"TASK10_TEST_LOG="+logPath,
		"TASK10_BLOCK_COMMAND=run",
		"TASK10_READY_FILE="+ready,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	task10WaitForFile(t, ready)
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err := command.Wait()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("wait error=%v, want signal exit", err)
	}
	waitStatus, ok := exitError.Sys().(syscall.WaitStatus)
	if !ok || !waitStatus.Signaled() || waitStatus.Signal() != syscall.SIGTERM {
		t.Fatalf("wait status=%v, want SIGTERM", exitError.Sys())
	}
	if stdout.Len() != 0 || strings.Contains(stderr.String(), temporary) {
		t.Fatalf("signal diagnostics leaked state: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	preserved := task10SinglePreservedDirectory(t, artifacts)
	if _, statErr := os.Stat(filepath.Join(preserved, "evidence", "runs", "partial.json")); statErr != nil {
		t.Fatalf("signal partial artifact missing: %v", statErr)
	}
	task10AssertDirectoryEmpty(t, temporary)
}

func TestTask10VerifyExperimentsReraisesChildTermination(t *testing.T) {
	for _, childCommand := range []string{"run", "diff"} {
		t.Run(childCommand, func(t *testing.T) {
			repository, logPath := task10FakeRepository(t)
			temporary := t.TempDir()
			artifacts := t.TempDir()
			command := exec.Command(task10ScriptPath(t, "verify-experiments.sh"), "deep", repository, artifacts)
			command.Env = append(os.Environ(),
				"TMPDIR="+temporary,
				"TASK10_TEST_LOG="+logPath,
				"TASK10_SIGNAL_COMMAND="+childCommand,
			)
			err := command.Run()
			var exitError *exec.ExitError
			if !errors.As(err, &exitError) {
				t.Fatalf("run error=%v, want signal exit", err)
			}
			waitStatus, ok := exitError.Sys().(syscall.WaitStatus)
			if !ok || !waitStatus.Signaled() || waitStatus.Signal() != syscall.SIGTERM {
				t.Fatalf("wait status=%v, want reraised SIGTERM", exitError.Sys())
			}
			preserved := task10SinglePreservedDirectory(t, artifacts)
			if _, statErr := os.Stat(filepath.Join(preserved, "evidence", "runs", "partial.json")); statErr != nil {
				t.Fatalf("child-signal partial artifact missing: %v", statErr)
			}
			task10AssertDirectoryEmpty(t, temporary)
		})
	}
}

func TestTask10W0ContractRejectsWrongArity(t *testing.T) {
	script := task10ScriptPath(t, "assert-w0-release-contract.sh")
	for _, args := range [][]string{nil, {"one", "two"}, {""}} {
		result := task10Run(t, script, args, nil)
		if result.exitCode != 2 {
			t.Fatalf("args=%q exit=%d, want 2", args, result.exitCode)
		}
	}
}

func TestTask10W0ContractRejectsPhysicallyRepositoryLocalTemporaryRoot(t *testing.T) {
	repository, logPath := task10FakeRepository(t)
	fakeBin := task10FakeMakeDirectory(t)
	repositoryTemporary := filepath.Join(repository, "contract temporary")
	task10MkdirAll(t, repositoryTemporary)
	symlink := filepath.Join(t.TempDir(), "external-looking-link")
	if err := os.Symlink(repositoryTemporary, symlink); err != nil {
		t.Fatal(err)
	}
	result := task10Run(t, task10ScriptPath(t, "assert-w0-release-contract.sh"), []string{repository}, []string{
		"PATH=" + fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TMPDIR=" + symlink,
		"TASK10_TEST_LOG=" + logPath,
	})
	if result.exitCode != 2 {
		t.Fatalf("exit=%d, want 2 for physically repository-local temporary root; stderr=%q", result.exitCode, result.stderr)
	}
	if result.stdout != "" || strings.Contains(result.stderr, repository) || strings.Contains(result.stderr, symlink) {
		t.Fatalf("temporary-root rejection leaked a path: stdout=%q stderr=%q", result.stdout, result.stderr)
	}
	if log := task10ReadFile(t, logPath); log != "" {
		t.Fatalf("positive gates ran before temporary-root rejection:\n%s", log)
	}
	task10AssertDirectoryEmpty(t, repositoryTemporary)
}

func TestTask10W0ContractAcceptsOnlyExactJSONOnStderr(t *testing.T) {
	repository, logPath := task10FakeRepository(t)
	fakeBin := task10FakeMakeDirectory(t)
	jsonPath := filepath.Join(t.TempDir(), "release.json")
	task10WriteFile(t, jsonPath, task10ValidReleaseJSON(t), 0o644)
	temporary := t.TempDir()
	result := task10Run(t, task10ScriptPath(t, "assert-w0-release-contract.sh"), []string{repository}, []string{
		"PATH=" + fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TMPDIR=" + temporary,
		"TASK10_TEST_LOG=" + logPath,
		"TASK10_VALIDATE_JSON_PATH=" + jsonPath,
		"TASK10_VALIDATE_STATUS=4",
	})
	if result.exitCode != 0 || result.stdout != "" || result.stderr != "" {
		t.Fatalf("contract=(exit=%d stdout=%q stderr=%q), want clean success", result.exitCode, result.stdout, result.stderr)
	}
	log := task10ReadFile(t, logPath)
	for _, expected := range []string{"MAKE\n", "ARG --no-print-directory\n", "ARG -C\n", "ARG " + repository + "\n", "ARG verify-fast\n", "ARG verify-deep\n", "ARG audit-evidence\n", "CALL validate\n", "ARG --format\n", "ARG json\n"} {
		if !strings.Contains(log, expected) {
			t.Errorf("log does not contain %q:\n%s", expected, log)
		}
	}
	task10AssertDirectoryEmpty(t, temporary)
}

func TestTask10W0ContractPropagatesPositiveAndValidateFailures(t *testing.T) {
	tests := []struct {
		name           string
		makeStatus     int
		validateStatus int
		want           int
	}{
		{name: "positive gate", makeStatus: 7, validateStatus: 4, want: 7},
		{name: "validate argument", makeStatus: 0, validateStatus: 2, want: 2},
		{name: "validate execution", makeStatus: 0, validateStatus: 5, want: 5},
		{name: "unexpected success", makeStatus: 0, validateStatus: 0, want: 1},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			repository, logPath := task10FakeRepository(t)
			fakeBin := task10FakeMakeDirectory(t)
			jsonPath := filepath.Join(t.TempDir(), "release.json")
			task10WriteFile(t, jsonPath, task10ValidReleaseJSON(t), 0o644)
			temporary := t.TempDir()
			result := task10Run(t, task10ScriptPath(t, "assert-w0-release-contract.sh"), []string{repository}, []string{
				"PATH=" + fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"TMPDIR=" + temporary,
				"TASK10_TEST_LOG=" + logPath,
				fmt.Sprintf("TASK10_MAKE_STATUS=%d", testCase.makeStatus),
				fmt.Sprintf("TASK10_VALIDATE_STATUS=%d", testCase.validateStatus),
				"TASK10_VALIDATE_JSON_PATH=" + jsonPath,
			})
			if result.exitCode != testCase.want {
				t.Fatalf("exit=%d, want %d; stderr=%q", result.exitCode, testCase.want, result.stderr)
			}
			if strings.Contains(result.stderr, temporary) || strings.Contains(result.stdout, temporary) {
				t.Fatalf("failure leaked temporary path: stdout=%q stderr=%q", result.stdout, result.stderr)
			}
			task10AssertDirectoryEmpty(t, temporary)
		})
	}
}

func TestTask10W0ContractRejectsStdoutAndAdversarialJSON(t *testing.T) {
	valid := task10ValidReleaseJSON(t)
	missingID := task10MissingCaseIDs()[0]
	adversarial := []struct {
		name   string
		mutate func(string) string
	}{
		{name: "stdout", mutate: func(value string) string { return value }},
		{name: "duplicate top key", mutate: func(value string) string {
			return strings.Replace(value, `"diagnostics":`, `"diagnostics":[],"diagnostics":`, 1)
		}},
		{name: "duplicate nested key", mutate: func(value string) string {
			return strings.Replace(value, `"baseline_total":75`, `"baseline_total":75,"baseline_total":75`, 1)
		}},
		{name: "unknown top field", mutate: func(value string) string { return strings.Replace(value, "{", `{"forged":true,`, 1) }},
		{name: "unknown nested field", mutate: func(value string) string {
			return strings.Replace(value, `"baseline_total":75`, `"forged":true,"baseline_total":75`, 1)
		}},
		{name: "trailing document", mutate: func(value string) string { return value + "{}\n" }},
		{name: "null", mutate: func(value string) string {
			return strings.Replace(value, `"required_adapters":[]`, `"required_adapters":null`, 1)
		}},
		{name: "wrong type", mutate: func(value string) string {
			return strings.Replace(value, `"complete_total":1`, `"complete_total":"1"`, 1)
		}},
		{name: "extra diagnostic", mutate: func(value string) string {
			return strings.Replace(value, `"diagnostics":[`, `"diagnostics":[{"code":"forged","severity":"error","message":"forged"},`, 1)
		}},
		{name: "missing case id", mutate: func(value string) string { return strings.Replace(value, `"`+missingID+`",`, "", 1) }},
		{name: "unsorted case ids", mutate: func(value string) string {
			ids := task10MissingCaseIDs()
			return strings.Replace(value, `"`+ids[0]+`","`+ids[1]+`"`, `"`+ids[1]+`","`+ids[0]+`"`, 1)
		}},
		{name: "wrong count", mutate: func(value string) string {
			return strings.Replace(value, `"complete_total":1`, `"complete_total":2`, 1)
		}},
		{name: "five matrix cells", mutate: func(value string) string {
			var report task10ReleaseReport
			if err := json.Unmarshal([]byte(value), &report); err != nil {
				panic(err)
			}
			report.Matrix = report.Matrix[:5]
			encoded, err := json.Marshal(report)
			if err != nil {
				panic(err)
			}
			return string(encoded) + "\n"
		}},
		{name: "forged matrix cell", mutate: func(value string) string {
			return strings.Replace(value, `"shared-fail-closed"`, `"forged-implementation"`, 1)
		}},
	}
	for _, testCase := range adversarial {
		t.Run(testCase.name, func(t *testing.T) {
			repository, logPath := task10FakeRepository(t)
			fakeBin := task10FakeMakeDirectory(t)
			jsonPath := filepath.Join(t.TempDir(), "adversarial.json")
			contents := testCase.mutate(valid)
			if testCase.name == "oversize" {
				contents += strings.Repeat(" ", 2<<20)
			}
			task10WriteFile(t, jsonPath, contents, 0o644)
			temporary := t.TempDir()
			extraEnvironment := []string{}
			if testCase.name == "stdout" {
				extraEnvironment = append(extraEnvironment, "TASK10_VALIDATE_STDOUT=forged")
			}
			environment := append([]string{
				"PATH=" + fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"TMPDIR=" + temporary,
				"TASK10_TEST_LOG=" + logPath,
				"TASK10_VALIDATE_STATUS=4",
				"TASK10_VALIDATE_JSON_PATH=" + jsonPath,
			}, extraEnvironment...)
			result := task10Run(t, task10ScriptPath(t, "assert-w0-release-contract.sh"), []string{repository}, environment)
			if result.exitCode != 1 {
				t.Fatalf("exit=%d, want contract mismatch 1; stdout=%q stderr=%q", result.exitCode, result.stdout, result.stderr)
			}
			if result.stdout != "" || strings.Contains(result.stderr, temporary) || strings.Contains(result.stderr, repository) {
				t.Fatalf("unsafe mismatch output: stdout=%q stderr=%q", result.stdout, result.stderr)
			}
			task10AssertDirectoryEmpty(t, temporary)
		})
	}
}

func TestTask10W0ContractRejectsOversizeJSON(t *testing.T) {
	repository, logPath := task10FakeRepository(t)
	fakeBin := task10FakeMakeDirectory(t)
	jsonPath := filepath.Join(t.TempDir(), "oversize.json")
	task10WriteFile(t, jsonPath, task10ValidReleaseJSON(t)+strings.Repeat(" ", 2<<20), 0o644)
	temporary := t.TempDir()
	result := task10Run(t, task10ScriptPath(t, "assert-w0-release-contract.sh"), []string{repository}, []string{
		"PATH=" + fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TMPDIR=" + temporary,
		"TASK10_TEST_LOG=" + logPath,
		"TASK10_VALIDATE_STATUS=4",
		"TASK10_VALIDATE_JSON_PATH=" + jsonPath,
	})
	if result.exitCode != 1 {
		t.Fatalf("exit=%d, want 1; stderr=%q", result.exitCode, result.stderr)
	}
	task10AssertDirectoryEmpty(t, temporary)
}

func TestTask10W0ContractReraisesSignalAndCleansTemporaryState(t *testing.T) {
	repository, logPath := task10FakeRepository(t)
	fakeBin := task10FakeMakeDirectory(t)
	temporary := t.TempDir()
	ready := filepath.Join(t.TempDir(), "ready")
	command := exec.Command(task10ScriptPath(t, "assert-w0-release-contract.sh"), repository)
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"TMPDIR="+temporary,
		"TASK10_TEST_LOG="+logPath,
		"TASK10_BLOCK_MAKE=1",
		"TASK10_READY_FILE="+ready,
	)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	task10WaitForFile(t, ready)
	if err := command.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	err := command.Wait()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("wait error=%v, want signal", err)
	}
	waitStatus, ok := exitError.Sys().(syscall.WaitStatus)
	if !ok || !waitStatus.Signaled() || waitStatus.Signal() != syscall.SIGHUP {
		t.Fatalf("wait status=%v, want SIGHUP", exitError.Sys())
	}
	task10AssertDirectoryEmpty(t, temporary)
}

func TestTask10W0ContractReraisesPositiveGateChildSignal(t *testing.T) {
	repository, logPath := task10FakeRepository(t)
	fakeBin := task10FakeMakeDirectory(t)
	temporary := t.TempDir()
	command := exec.Command(task10ScriptPath(t, "assert-w0-release-contract.sh"), repository)
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"TMPDIR="+temporary,
		"TASK10_TEST_LOG="+logPath,
		"TASK10_SIGNAL_MAKE=1",
	)
	err := command.Run()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("run error=%v, want signal exit", err)
	}
	waitStatus, ok := exitError.Sys().(syscall.WaitStatus)
	if !ok || !waitStatus.Signaled() || waitStatus.Signal() != syscall.SIGHUP {
		t.Fatalf("wait status=%v, want reraised SIGHUP", exitError.Sys())
	}
	task10AssertDirectoryEmpty(t, temporary)
}

func TestTask10W0ContractReraisesParserSignals(t *testing.T) {
	signals := []struct {
		name   string
		value  string
		signal syscall.Signal
	}{
		{name: "hup", value: "HUP", signal: syscall.SIGHUP},
		{name: "int", value: "INT", signal: syscall.SIGINT},
		{name: "quit", value: "QUIT", signal: syscall.SIGQUIT},
		{name: "term", value: "TERM", signal: syscall.SIGTERM},
	}
	for _, testCase := range signals {
		t.Run(testCase.name, func(t *testing.T) {
			repository, logPath := task10FakeRepository(t)
			fakeBin := task10FakeMakeAndSignalingGoDirectory(t)
			jsonPath := filepath.Join(t.TempDir(), "release.json")
			task10WriteFile(t, jsonPath, task10ValidReleaseJSON(t), 0o644)
			temporary := t.TempDir()
			command := exec.Command(task10ScriptPath(t, "assert-w0-release-contract.sh"), repository)
			command.Env = append(os.Environ(),
				"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"TMPDIR="+temporary,
				"TASK10_TEST_LOG="+logPath,
				"TASK10_VALIDATE_STATUS=4",
				"TASK10_VALIDATE_JSON_PATH="+jsonPath,
				"TASK10_SIGNAL_GO="+testCase.value,
			)
			err := command.Run()
			var exitError *exec.ExitError
			if !errors.As(err, &exitError) {
				t.Fatalf("run error=%v, want parser signal", err)
			}
			waitStatus, ok := exitError.Sys().(syscall.WaitStatus)
			if !ok || !waitStatus.Signaled() || waitStatus.Signal() != testCase.signal {
				t.Fatalf("wait status=%v, want reraised %s", exitError.Sys(), testCase.value)
			}
			task10AssertDirectoryEmpty(t, temporary)
		})
	}
}

func TestTask10W0ContractRealCLIExpectedNegativeFromCleanRepository(t *testing.T) {
	repository := task10CopyCurrentRepository(t)
	task10Git(t, repository, "init", "-q")
	task10Git(t, repository, "config", "user.name", "Task10 Test")
	task10Git(t, repository, "config", "user.email", "task10@example.invalid")
	task10Git(t, repository, "add", "-A")
	task10Git(t, repository, "commit", "-qm", "source fixture")

	binary := filepath.Join(repository, "generated", ".bin", "whiteboard")
	task10MkdirAll(t, filepath.Dir(binary))
	task10Command(t, repository, nil, "go", "build", "-buildvcs=true", "-trimpath", "-o", binary, "./cmd/whiteboard")
	sourceRevision := strings.TrimSpace(task10Git(t, repository, "rev-parse", "HEAD"))
	buildInfo := task10Command(t, repository, nil, "go", "version", "-m", binary)
	if !strings.Contains(buildInfo, "vcs.revision="+sourceRevision) || !strings.Contains(buildInfo, "vcs.modified=false") {
		t.Fatalf("source binary is not cleanly stamped:\n%s", buildInfo)
	}
	readmePath := filepath.Join(repository, "README.md")
	readme := task10ReadFile(t, readmePath)
	task10WriteFile(t, readmePath, readme+"\ntracked mutation\n", 0o644)
	preflightArtifacts := t.TempDir()
	preflightTemporary := t.TempDir()
	preflight := task10Run(t, filepath.Join(repository, "scripts", "verify-experiments.sh"), []string{"deep", repository, preflightArtifacts}, []string{
		"TMPDIR=" + preflightTemporary,
	})
	if preflight.exitCode != 2 || strings.Contains(preflight.stderr, repository) || strings.Contains(preflight.stderr, preflightTemporary) {
		t.Fatalf("real preflight=(exit=%d stdout=%q stderr=%q), want sanitized provenance failure", preflight.exitCode, preflight.stdout, preflight.stderr)
	}
	preflightPreserved := task10SinglePreservedDirectory(t, preflightArtifacts)
	preflightDiagnostic := task10ReadFile(t, filepath.Join(preflightPreserved, "commands", "run.stderr"))
	if !strings.Contains(preflightDiagnostic, "source_state_unavailable") || strings.Contains(preflightDiagnostic, repository) || strings.Contains(preflightDiagnostic, preflightTemporary) {
		t.Fatalf("real preflight diagnostic is unsafe or incomplete: %q", preflightDiagnostic)
	}
	if status := task10ReadFile(t, filepath.Join(preflightPreserved, "commands", "run.exit-status")); status != "2\n" {
		t.Fatalf("real preflight status=%q, want 2", status)
	}
	task10AssertDirectoryEmpty(t, filepath.Join(preflightPreserved, "evidence"))
	task10AssertDirectoryEmpty(t, preflightTemporary)
	task10WriteFile(t, readmePath, readme, 0o644)
	if status := task10Git(t, repository, "status", "--porcelain"); status != "" {
		t.Fatalf("restoring the provenance fixture did not recover a clean repository:\n%s", status)
	}

	task10Command(t, repository, nil, binary, "run", "--required", "--profile", "deep", "--root", repository, "--evidence-root", filepath.Join(repository, "evidence"), "--snapshot")
	records, err := filepath.Glob(filepath.Join(repository, "evidence", "runs", "*.json"))
	if err != nil || len(records) != 6 {
		t.Fatalf("deep evidence records=%v err=%v, want six", records, err)
	}
	manifests, err := filepath.Glob(filepath.Join(repository, "evidence", "releases", "sha256-*", "manifest.yaml"))
	if err != nil || len(manifests) != 1 {
		t.Fatalf("release manifests=%v err=%v, want one", manifests, err)
	}

	task10Git(t, repository, "add", "evidence/runs", "evidence/releases")
	task10Git(t, repository, "commit", "-qm", "evidence fixture")
	task10Command(t, repository, nil, "go", "build", "-buildvcs=true", "-trimpath", "-o", binary, "./cmd/whiteboard")
	evidenceRevision := strings.TrimSpace(task10Git(t, repository, "rev-parse", "HEAD"))
	buildInfo = task10Command(t, repository, nil, "go", "version", "-m", binary)
	if !strings.Contains(buildInfo, "vcs.revision="+evidenceRevision) || !strings.Contains(buildInfo, "vcs.modified=false") {
		t.Fatalf("evidence binary is not cleanly stamped:\n%s", buildInfo)
	}
	auditOutput := filepath.Join(t.TempDir(), "report.json")
	task10Command(t, repository, nil, binary, "report", "--root", repository, "--evidence-root", filepath.Join(repository, "evidence"), "--release", "current", "--profile", "deep", "--format", "json", "--output", auditOutput)

	fakeBin := task10FakeMakeDirectory(t)
	logPath := filepath.Join(t.TempDir(), "make.log")
	task10WriteFile(t, logPath, "", 0o644)
	temporary := t.TempDir()
	result := task10Run(t, filepath.Join(repository, "scripts", "assert-w0-release-contract.sh"), []string{repository}, []string{
		"PATH=" + fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TMPDIR=" + temporary,
		"TASK10_TEST_LOG=" + logPath,
	})
	if result.exitCode != 0 || result.stdout != "" || result.stderr != "" {
		t.Fatalf("real contract=(exit=%d stdout=%q stderr=%q), want expected-negative success", result.exitCode, result.stdout, result.stderr)
	}
	if status := task10Git(t, repository, "status", "--porcelain"); status != "" {
		t.Fatalf("real contract dirtied clean repository:\n%s", status)
	}
	task10AssertDirectoryEmpty(t, temporary)
}

type task10CommandResult struct {
	exitCode int
	stdout   string
	stderr   string
}

func task10Run(t *testing.T, executable string, args []string, environment []string) task10CommandResult {
	t.Helper()
	command := exec.Command(executable, args...)
	command.Env = append(os.Environ(), environment...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := task10CommandResult{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return result
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("run %q: %v", executable, err)
	}
	result.exitCode = exitError.ExitCode()
	return result
}

func task10ScriptPath(t *testing.T, name string) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(currentFile), "..", "..", "scripts", name)
}

func task10FakeRepository(t *testing.T) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repository with spaces")
	whiteboard := filepath.Join(root, "generated", ".bin", "whiteboard")
	task10MkdirAll(t, filepath.Dir(whiteboard))
	task10MkdirAll(t, filepath.Join(root, "evidence"))
	task10WriteFile(t, whiteboard, task10FakeWhiteboard, 0o755)
	logPath := filepath.Join(t.TempDir(), "calls.log")
	task10WriteFile(t, logPath, "", 0o644)
	return root, logPath
}

func task10FakeMakeDirectory(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "fake tools")
	task10MkdirAll(t, root)
	task10WriteFile(t, filepath.Join(root, "make"), task10FakeMake, 0o755)
	return root
}

func task10FakeMakeAndSignalingGoDirectory(t *testing.T) string {
	t.Helper()
	root := task10FakeMakeDirectory(t)
	task10WriteFile(t, filepath.Join(root, "go"), task10FakeSignalingGo, 0o755)
	return root
}

func task10CopyCurrentRepository(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	source := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	destination := filepath.Join(t.TempDir(), "clean repository with spaces")
	task10MkdirAll(t, destination)
	listed := task10Command(t, source, nil, "git", "ls-files", "-co", "--exclude-standard", "-z")
	for _, encoded := range bytes.Split([]byte(listed), []byte{0}) {
		if len(encoded) == 0 {
			continue
		}
		relative := string(encoded)
		if task10GeneratedEvidencePath(relative) {
			continue
		}
		task10CopyRepositoryFile(t, filepath.Join(source, relative), filepath.Join(destination, relative))
	}
	return destination
}

func task10GeneratedEvidencePath(path string) bool {
	if path == "evidence/runs/.gitkeep" || path == "evidence/releases/.gitkeep" {
		return false
	}
	return strings.HasPrefix(path, "evidence/runs/") || strings.HasPrefix(path, "evidence/releases/")
}

func task10CopyRepositoryFile(t *testing.T, source, destination string) {
	t.Helper()
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, readErr := os.Readlink(source)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if err := os.Symlink(target, destination); err != nil {
			t.Fatal(err)
		}
		return
	}
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

func task10Git(t *testing.T, directory string, args ...string) string {
	t.Helper()
	return task10Command(t, directory, nil, "git", args...)
}

func task10Command(t *testing.T, directory string, environment []string, executable string, args ...string) string {
	t.Helper()
	command := exec.Command(executable, args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC", "GOFLAGS=-mod=readonly")
	command.Env = append(command.Env, environment...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %q failed: %v\n%s", executable, args, err, output)
	}
	return string(output)
}

func task10MkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func task10WriteFile(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}

func task10ReadFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func task10AssertDirectoryEmpty(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("temporary directory retained entries: %v", names)
	}
}

func task10SinglePreservedDirectory(t *testing.T, artifactRoot string) string {
	t.Helper()
	entries, err := os.ReadDir(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	directories := []string{}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "w0-") {
			directories = append(directories, filepath.Join(artifactRoot, entry.Name()))
		}
	}
	if len(directories) != 1 {
		t.Fatalf("preserved directories=%v, want exactly one", directories)
	}
	return directories[0]
}

func task10ParseCalls(t *testing.T, logPath string) [][]string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(task10ReadFile(t, logPath)), "\n")
	calls := [][]string{}
	for _, line := range lines {
		if strings.HasPrefix(line, "CALL ") {
			calls = append(calls, []string{strings.TrimPrefix(line, "CALL ")})
			continue
		}
		if strings.HasPrefix(line, "ARG ") && len(calls) > 0 {
			calls[len(calls)-1] = append(calls[len(calls)-1], strings.TrimPrefix(line, "ARG "))
		}
	}
	return calls
}

func task10RequireArguments(t *testing.T, actual []string, required ...string) {
	t.Helper()
	for _, value := range required {
		found := false
		for _, argument := range actual {
			if argument == value {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("arguments=%q do not contain %q", actual, value)
		}
	}
}

func task10ArgumentValue(t *testing.T, arguments []string, flag string) string {
	t.Helper()
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == flag {
			return arguments[index+1]
		}
	}
	t.Fatalf("arguments=%q do not contain value for %q", arguments, flag)
	return ""
}

func task10PathWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func task10WaitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for synchronization file")
}

func task10ValidReleaseJSON(t *testing.T) string {
	t.Helper()
	missing := task10MissingCaseIDs()
	report := task10ReleaseReport{
		Diagnostics: []task10Diagnostic{{
			Code:     "release_scope_incomplete",
			Severity: "error",
			Message:  fmt.Sprintf("release scope incomplete: complete=1 baseline=75 missing=74 unexpected=0 missing_ids=[%s] unexpected_ids=[]", strings.Join(missing, " ")),
		}},
		Coverage: task10Coverage{
			BaselineTotal:         75,
			CompleteTotal:         1,
			MissingCaseIDs:        missing,
			UnexpectedCaseIDs:     []string{},
			Families:              task10ExpectedFamilies(),
			RequiredPrinciples:    []string{"token-bucket"},
			RequiredScenarioLabs:  []string{"distributed-rate-limiter"},
			RequiredPrimitiveLabs: []string{"token-bucket"},
			RequiredAdapters:      []string{},
		},
		Matrix: task10ExpectedMatrix(),
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded) + "\n"
}

func task10ExpectedFamilies() []task10FamilyCoverage {
	return []task10FamilyCoverage{
		{ID: "addressing-traffic", Required: 7, Complete: 1},
		{ID: "ai-vector", Required: 7, Complete: 0},
		{ID: "distributed-storage", Required: 7, Complete: 0},
		{ID: "feed-social-ranking", Required: 8, Complete: 0},
		{ID: "media-delivery", Required: 7, Complete: 0},
		{ID: "realtime-collaboration", Required: 8, Complete: 0},
		{ID: "scheduling-control-plane", Required: 8, Complete: 0},
		{ID: "search-crawl-geo", Required: 8, Complete: 0},
		{ID: "streaming-analytics-observability", Required: 7, Complete: 0},
		{ID: "transactions-contention", Required: 8, Complete: 0},
	}
}

func task10ExpectedMatrix() []task10MatrixCell {
	return []task10MatrixCell{
		{LabID: "distributed-rate-limiter", RequiredRunID: "coordinator-outage-policy", BindingID: "distributed-rate-limiter-outage-policy", ClaimID: "distributed-rate-limiter-outage-policy-trades-availability-for-quota", Role: "baseline", ImplementationID: "shared-fail-closed", Workload: "coordinator-outage", Faults: []string{"coordinator-unavailable"}, AssertionIDs: []string{"all-requests-decided", "expected-outage-decision", "expected-outage-availability", "expected-quota-overshoot", "no-unexpected-errors"}},
		{LabID: "distributed-rate-limiter", RequiredRunID: "coordinator-outage-policy", BindingID: "distributed-rate-limiter-outage-policy", ClaimID: "distributed-rate-limiter-outage-policy-trades-availability-for-quota", Role: "variant", ImplementationID: "shared-fail-open", Workload: "coordinator-outage", Faults: []string{"coordinator-unavailable"}, AssertionIDs: []string{"all-requests-decided", "expected-outage-decision", "expected-outage-availability", "expected-quota-overshoot", "no-unexpected-errors"}},
		{LabID: "distributed-rate-limiter", RequiredRunID: "per-node-vs-shared-quota", BindingID: "distributed-rate-limiter-global-quota", ClaimID: "distributed-rate-limiter-per-node-multiplies-global-quota", Role: "variant", ImplementationID: "per-node-token-bucket", Workload: "two-node-burst", Faults: []string{}, AssertionIDs: []string{"all-requests-decided", "expected-allowed-count", "expected-global-quota-overshoot", "no-unexpected-errors"}},
		{LabID: "distributed-rate-limiter", RequiredRunID: "per-node-vs-shared-quota", BindingID: "distributed-rate-limiter-global-quota", ClaimID: "distributed-rate-limiter-per-node-multiplies-global-quota", Role: "baseline", ImplementationID: "shared-token-bucket", Workload: "two-node-burst", Faults: []string{}, AssertionIDs: []string{"all-requests-decided", "expected-allowed-count", "expected-global-quota-overshoot", "no-unexpected-errors"}},
		{LabID: "token-bucket", RequiredRunID: "burst-and-refill-boundary", BindingID: "token-bucket-burst-boundary", ClaimID: "token-bucket-bounds-burst-and-average-rate", Role: "variant", ImplementationID: "token-bucket", Workload: "burst-refill-boundary", Faults: []string{}, AssertionIDs: []string{"initial-burst-bounded", "pre-boundary-denied", "boundary-refills-one", "capacity-never-exceeded", "implementation-matches-reference"}},
		{LabID: "token-bucket", RequiredRunID: "burst-and-refill-boundary", BindingID: "token-bucket-burst-boundary", ClaimID: "token-bucket-bounds-burst-and-average-rate", Role: "baseline", ImplementationID: "token-bucket-reference-model", Workload: "burst-refill-boundary", Faults: []string{}, AssertionIDs: []string{"initial-burst-bounded", "pre-boundary-denied", "boundary-refills-one", "capacity-never-exceeded", "implementation-matches-reference"}},
	}
}

func task10MissingCaseIDs() []string {
	ids := []string{
		"ad-click-aggregation", "ad-serving-ranking", "appointment-booking", "autocomplete", "bank-transfer", "batch-data-pipeline", "cdn", "centralized-logging", "chat-messenger", "ci-runner", "cloud-file-sync", "code-assistant", "collaborative-editor", "comments-reactions", "configuration-feature-flags", "consistent-hash-router", "container-orchestrator", "dag-workflow", "deployment-system", "distributed-cache", "distributed-email-service", "distributed-id", "distributed-log-message-queue", "distributed-sql", "dns-service-discovery", "double-entry-ledger-wallet", "ecommerce-order-inventory", "embedding-index-pipeline", "food-delivery-dispatch", "full-text-search", "gpu-scheduler", "identity-authorization-service", "image-service", "inference-gateway", "job-scheduler", "key-value-store", "large-file-transfer", "leaderboard", "live-comments", "live-streaming", "llm-chat-serving", "load-balancer-api-gateway", "maps-navigation", "metrics-monitoring-alerting", "multi-tenant-cloud-control-plane", "music-podcast-streaming", "nearby-places", "notification-delivery", "object-storage", "online-auction", "pastebin", "payment-system", "photo-sharing", "presence-service", "pubsub", "qa-news-aggregation", "rag-assistant", "recommendation-system", "ride-hailing-dispatch", "social-graph", "social-news-feed", "stream-processing", "ticketing", "time-series-database", "top-k-heavy-hitters", "trading-brokerage", "transcoding-pipeline", "url-shortener", "vector-database", "video-conferencing", "video-on-demand", "web-crawler", "webhook-delivery", "wide-column-document-store",
	}
	if !sort.StringsAreSorted(ids) || len(ids) != 74 {
		panic("invalid W0 missing-case fixture")
	}
	return ids
}
