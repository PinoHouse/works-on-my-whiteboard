package integration_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

const task10MakeModulePath = "github.com/PinoHouse/works-on-my-whiteboard"

func TestTask10MakeBinaryCacheLifecycle(t *testing.T) {
	repository := task10MakeNewRepository(t)
	binary := filepath.Join(repository, "generated", ".bin", "whiteboard")

	task10MakeRun(t, repository, nil, "make", "build")
	firstRevision := task10MakeGit(t, repository, "rev-parse", "HEAD")
	task10MakeRequireBuildInfo(t, binary, firstRevision)

	oldTime := time.Unix(946684800, 0)
	if err := os.Chtimes(binary, oldTime, oldTime); err != nil {
		t.Fatalf("set cached binary timestamp: %v", err)
	}
	task10MakeRun(t, repository, nil, "make", "build")
	task10MakeRequireModTime(t, binary, oldTime)

	task10MakeWrite(t, filepath.Join(repository, "evidence", "runs", "record.json"), []byte("{}\n"), 0o644)
	task10MakeRun(t, repository, nil, "make", "build")
	task10MakeRequireModTime(t, binary, oldTime)
	if err := os.RemoveAll(filepath.Join(repository, "evidence")); err != nil {
		t.Fatalf("remove temporary evidence: %v", err)
	}

	task10MakeWrite(t, filepath.Join(repository, "cmd", "whiteboard", "main.go"), []byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Print(\"two\") }\n"), 0o644)
	secondRevision := task10MakeCommit(t, repository, "change source")
	task10MakeRun(t, repository, nil, "make", "build")
	if secondRevision == firstRevision {
		t.Fatal("source commit did not change HEAD")
	}
	task10MakeRequireBuildInfo(t, binary, secondRevision)

	goodBytes := task10MakeRead(t, binary)
	task10MakeWrite(t, filepath.Join(repository, "cmd", "whiteboard", "main.go"), []byte("package main\n\nfunc main() { missingSymbol }\n"), 0o644)
	task10MakeCommit(t, repository, "break source")
	output, err := task10MakeCommand(repository, nil, "make", "build")
	if err == nil {
		t.Fatalf("broken build unexpectedly succeeded: %s", output)
	}
	if got := task10MakeRead(t, binary); !bytes.Equal(got, goodBytes) {
		t.Fatal("failed replacement changed the last verified binary")
	}
	entries, err := os.ReadDir(filepath.Dir(binary))
	if err != nil {
		t.Fatalf("read binary directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "whiteboard" {
		t.Fatalf("failed build left temporary files: %#v", task10MakeEntryNames(entries))
	}
}

func TestTask10MakeRejectsSymlinkBinaryCache(t *testing.T) {
	repository := task10MakeNewRepository(t)
	binary := filepath.Join(repository, "generated", ".bin", "whiteboard")
	task10MakeRun(t, repository, nil, "make", "build")

	external := filepath.Join(t.TempDir(), "external-whiteboard")
	if err := os.Rename(binary, external); err != nil {
		t.Fatalf("move verified binary outside repository: %v", err)
	}
	externalBytes := task10MakeRead(t, external)
	externalTime := time.Unix(978307200, 0)
	if err := os.Chtimes(external, externalTime, externalTime); err != nil {
		t.Fatalf("set external binary timestamp: %v", err)
	}
	if err := os.Symlink(external, binary); err != nil {
		t.Fatalf("install symlink cache: %v", err)
	}

	task10MakeRun(t, repository, nil, "make", "build")
	info, err := os.Lstat(binary)
	if err != nil {
		t.Fatalf("lstat rebuilt binary: %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		t.Fatalf("rebuilt cache mode = %s; want regular executable", info.Mode())
	}
	if got := task10MakeRead(t, external); !bytes.Equal(got, externalBytes) {
		t.Fatal("rebuilding a symlink cache changed its external target")
	}
	task10MakeRequireModTime(t, external, externalTime)
}

func TestTask10MakeDoesNotFollowSymlinkCacheIntoExternalDirectory(t *testing.T) {
	repository := task10MakeNewRepository(t)
	binary := filepath.Join(repository, "generated", ".bin", "whiteboard")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatalf("create binary cache directory: %v", err)
	}
	externalDirectory := filepath.Join(t.TempDir(), "external-directory")
	if err := os.MkdirAll(externalDirectory, 0o755); err != nil {
		t.Fatalf("create external directory: %v", err)
	}
	if err := os.Symlink(externalDirectory, binary); err != nil {
		t.Fatalf("install directory symlink cache: %v", err)
	}

	task10MakeRun(t, repository, nil, "make", "build")
	info, err := os.Lstat(binary)
	if err != nil {
		t.Fatalf("lstat rebuilt binary: %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		t.Fatalf("rebuilt cache mode = %s; want regular executable", info.Mode())
	}
	entries, err := os.ReadDir(externalDirectory)
	if err != nil {
		t.Fatalf("read external directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("build followed cache symlink into external directory: %#v", task10MakeEntryNames(entries))
	}
}

func TestTask10MakeRejectsSymlinkBinaryCacheDirectory(t *testing.T) {
	repository := task10MakeNewRepository(t)
	generated := filepath.Join(repository, "generated")
	if err := os.MkdirAll(generated, 0o755); err != nil {
		t.Fatalf("create generated directory: %v", err)
	}
	externalDirectory := filepath.Join(t.TempDir(), "external-bin")
	if err := os.MkdirAll(externalDirectory, 0o755); err != nil {
		t.Fatalf("create external binary directory: %v", err)
	}
	if err := os.Symlink(externalDirectory, filepath.Join(generated, ".bin")); err != nil {
		t.Fatalf("install binary directory symlink: %v", err)
	}
	task10MakeCommit(t, repository, "track binary directory symlink")

	output, err := task10MakeCommand(repository, nil, "make", "build")
	if err == nil {
		t.Fatalf("build through symlink cache directory unexpectedly succeeded: %s", output)
	}
	entries, err := os.ReadDir(externalDirectory)
	if err != nil {
		t.Fatalf("read external binary directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("build wrote through symlink cache directory: %#v", task10MakeEntryNames(entries))
	}
}

func TestTask10MakeCandidateCannotBePreplacedAsExternalSymlink(t *testing.T) {
	repository := task10MakeNewRepository(t)
	externalSentinel := filepath.Join(t.TempDir(), "external-candidate-target")
	task10MakeWrite(t, externalSentinel, []byte("external sentinel\n"), 0o644)
	task10MakeInstallCandidateAttackGo(t, repository)
	environment := []string{
		"PATH=" + filepath.Join(repository, "fake-bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
		"EXTERNAL_SENTINEL=" + externalSentinel,
	}

	task10MakeRun(t, repository, environment, "make", "build")
	if got := string(task10MakeRead(t, externalSentinel)); got != "external sentinel\n" {
		t.Fatalf("candidate build followed preplaced external symlink: %q", got)
	}
	binary := filepath.Join(repository, "generated", ".bin", "whiteboard")
	info, err := os.Lstat(binary)
	if err != nil {
		t.Fatalf("lstat installed binary: %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		t.Fatalf("installed binary mode = %s; want regular executable", info.Mode())
	}
}

func TestTask10MakeParallelGoalsFollowExplicitGateOrder(t *testing.T) {
	repository := task10MakeNewRepository(t)
	trace := filepath.Join(repository, "trace.log")
	task10MakeInstallFakeTools(t, repository)
	environment := []string{
		"PATH=" + filepath.Join(repository, "fake-bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TRACE=" + trace,
	}

	output, err := task10MakeCommand(repository, environment, "make", "-j8", "evidence", "verify")
	if err == nil {
		t.Fatalf("formal W0 verify unexpectedly succeeded: %s", output)
	}
	lines := task10MakeTraceLines(t, trace)
	task10MakeRequireOrdered(t, lines,
		"go env GOVERSION",
		"go mod verify",
		"gofmt -l .",
		"go vet ./...",
		"go test -count=1 ./...",
		"go test ./internal/evidence -run ^$ -fuzz ^FuzzCanonicalRecord$ -fuzztime=2s",
		"go test ./labs/primitives/token-bucket -run ^$ -fuzz ^FuzzBucketInvariant$ -fuzztime=2s",
		"go test -race -count=1 ./...",
		"whiteboard validate --root . --content",
		"whiteboard coverage --root . --format markdown --output generated/coverage.md --check",
		"script smoke",
		"script deep",
		"whiteboard run --required --profile deep --root . --evidence-root evidence --snapshot",
		"whiteboard report --root . --evidence-root evidence --release current --profile deep --format json --output generated/.verify/<scratch>/report.json",
		"whiteboard validate --root . --evidence-root evidence --release current --format text",
	)
}

func TestTask10MakeAuditDoesNotCreateEvidence(t *testing.T) {
	repository := task10MakeNewRepository(t)
	trace := filepath.Join(repository, "trace.log")
	task10MakeInstallFakeTools(t, repository)
	environment := []string{
		"PATH=" + filepath.Join(repository, "fake-bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TRACE=" + trace,
	}

	task10MakeRun(t, repository, environment, "make", "-j8", "audit-evidence")
	if _, err := os.Stat(filepath.Join(repository, "evidence")); !os.IsNotExist(err) {
		t.Fatalf("audit created or could inspect evidence directory: %v", err)
	}
	lines := task10MakeTraceLines(t, trace)
	for _, line := range lines {
		if strings.HasPrefix(line, "whiteboard run ") {
			t.Fatalf("audit invoked evidence generation: %q", line)
		}
	}
	if _, err := os.Stat(filepath.Join(repository, "generated", ".verify")); !os.IsNotExist(err) {
		t.Fatalf("audit did not clean verification scratch directory: %v", err)
	}
}

func TestTask10MakeAuditRejectsSymlinkVerifyDirectory(t *testing.T) {
	repository := task10MakeNewRepository(t)
	trace := filepath.Join(repository, "trace.log")
	task10MakeInstallFakeTools(t, repository)
	environment := []string{
		"PATH=" + filepath.Join(repository, "fake-bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TRACE=" + trace,
	}
	externalDirectory := filepath.Join(t.TempDir(), "external-verify")
	externalReport := filepath.Join(externalDirectory, "report.json")
	task10MakeWrite(t, externalReport, []byte("external sentinel\n"), 0o644)
	verifyPath := filepath.Join(repository, "generated", ".verify")
	if err := os.Symlink(externalDirectory, verifyPath); err != nil {
		t.Fatalf("install verify directory symlink: %v", err)
	}

	output, err := task10MakeCommand(repository, environment, "make", "audit-evidence")
	if err == nil {
		t.Fatalf("audit through verify symlink unexpectedly succeeded: %s", output)
	}
	if got := string(task10MakeRead(t, externalReport)); got != "external sentinel\n" {
		t.Fatalf("audit changed external report through symlink: %q", got)
	}
	info, err := os.Lstat(verifyPath)
	if err != nil {
		t.Fatalf("audit removed rejected verify symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("rejected verify path mode = %s; want unchanged symlink", info.Mode())
	}
}

func TestTask10MakeCleanPreservesEvidenceAndTrackedGeneratedFiles(t *testing.T) {
	repository := task10MakeNewRepository(t)
	evidenceRun := filepath.Join(repository, "evidence", "runs", "record.json")
	evidenceManifest := filepath.Join(repository, "evidence", "releases", "sha256-test", "manifest.yaml")
	coverage := filepath.Join(repository, "generated", "coverage.md")
	keep := filepath.Join(repository, "generated", ".gitkeep")
	task10MakeWrite(t, evidenceRun, []byte("record\n"), 0o644)
	task10MakeWrite(t, evidenceManifest, []byte("manifest\n"), 0o644)
	task10MakeWrite(t, coverage, []byte("coverage\n"), 0o644)
	task10MakeWrite(t, keep, nil, 0o644)
	task10MakeWrite(t, filepath.Join(repository, "generated", ".bin", "whiteboard"), []byte("binary\n"), 0o755)
	task10MakeWrite(t, filepath.Join(repository, "generated", ".verify", "report.json"), []byte("{}\n"), 0o644)

	task10MakeRun(t, repository, nil, "make", "clean")
	for _, path := range []string{evidenceRun, evidenceManifest, coverage, keep} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("clean removed preserved path %q: %v", path, err)
		}
	}
	for _, path := range []string{filepath.Join(repository, "generated", ".bin"), filepath.Join(repository, "generated", ".verify")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("clean did not remove %q: %v", path, err)
		}
	}
}

func TestTask10MakeCleanRejectsSymlinkGeneratedDirectory(t *testing.T) {
	repository := task10MakeNewRepository(t)
	externalDirectory := filepath.Join(t.TempDir(), "external-generated")
	binSentinel := filepath.Join(externalDirectory, ".bin", "keep")
	verifySentinel := filepath.Join(externalDirectory, ".verify", "keep")
	task10MakeWrite(t, binSentinel, []byte("bin sentinel\n"), 0o644)
	task10MakeWrite(t, verifySentinel, []byte("verify sentinel\n"), 0o644)
	if err := os.Symlink(externalDirectory, filepath.Join(repository, "generated")); err != nil {
		t.Fatalf("install generated directory symlink: %v", err)
	}

	output, err := task10MakeCommand(repository, nil, "make", "clean")
	if err == nil {
		t.Fatalf("clean through generated symlink unexpectedly succeeded: %s", output)
	}
	for _, path := range []string{binSentinel, verifySentinel} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("clean changed external sentinel %q: %v", path, err)
		}
	}
}

func task10MakeNewRepository(t *testing.T) string {
	t.Helper()
	task10MakeRequireExecutable(t, "git")
	task10MakeRequireExecutable(t, "go")
	task10MakeRequireExecutable(t, "make")

	repository := filepath.Join(t.TempDir(), "repository with spaces")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatalf("create repository: %v", err)
	}
	root := task10MakeSourceRoot(t)
	task10MakeWrite(t, filepath.Join(repository, "Makefile"), task10MakeRead(t, filepath.Join(root, "Makefile")), 0o644)
	task10MakeWrite(t, filepath.Join(repository, "go.mod"), []byte("module "+task10MakeModulePath+"\n\ngo 1.26.0\n\ntoolchain go1.26.5\n"), 0o644)
	task10MakeWrite(t, filepath.Join(repository, "cmd", "whiteboard", "main.go"), []byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Print(\"one\") }\n"), 0o644)
	task10MakeWrite(t, filepath.Join(repository, ".gitignore"), []byte("/generated/.bin/\n/generated/.verify/\n"), 0o644)
	task10MakeRun(t, repository, nil, "git", "init", "-q")
	task10MakeRun(t, repository, nil, "git", "config", "user.name", "Task10 Test")
	task10MakeRun(t, repository, nil, "git", "config", "user.email", "task10@example.invalid")
	task10MakeCommit(t, repository, "initial")
	return repository
}

func task10MakeInstallFakeTools(t *testing.T, repository string) {
	t.Helper()
	head := task10MakeGit(t, repository, "rev-parse", "HEAD")
	fakeBin := filepath.Join(repository, "fake-bin")
	goScript := fmt.Sprintf(`#!/bin/sh
set -eu
task10_lock="${TRACE}.lock"
if ! mkdir "$task10_lock" 2>/dev/null; then
  printf 'OVERLAP go\n' >> "$TRACE"
  exit 98
fi
trap 'rmdir "$task10_lock"' 0 1 2 3 15
sleep 0.02
printf 'go' >> "$TRACE"
for argument in "$@"; do printf ' %%s' "$argument" >> "$TRACE"; done
printf '\n' >> "$TRACE"
case "${1-} ${2-}" in
  "env GOVERSION") printf 'go1.26.5\n' ;;
  "env GOMOD") printf '%%s/go.mod\n' "$(pwd)" ;;
  "env GOWORK") printf '\n' ;;
  "env GOFLAGS") printf '%%s\n' '-mod=readonly' ;;
  "version -m")
    printf '%%s: go1.26.5\n' "${3-}"
    printf '\tpath\t%s/cmd/whiteboard\n'
    printf '\tmod\t%s\t(devel)\t\n'
    printf '\tbuild\t-trimpath=true\n'
    printf '\tbuild\tvcs=git\n'
    printf '\tbuild\tvcs.revision=%s\n'
    printf '\tbuild\tvcs.modified=false\n'
    ;;
esac
`, task10MakeModulePath, task10MakeModulePath, head)
	task10MakeWrite(t, filepath.Join(fakeBin, "go"), []byte(goScript), 0o755)
	task10MakeWrite(t, filepath.Join(fakeBin, "gofmt"), []byte("#!/bin/sh\nset -eu\ntask10_lock=\"${TRACE}.lock\"\nif ! mkdir \"$task10_lock\" 2>/dev/null; then printf 'OVERLAP gofmt\\n' >> \"$TRACE\"; exit 98; fi\ntrap 'rmdir \"$task10_lock\"' 0 1 2 3 15\nsleep 0.02\nprintf 'gofmt' >> \"$TRACE\"\nfor argument in \"$@\"; do printf ' %s' \"$argument\" >> \"$TRACE\"; done\nprintf '\\n' >> \"$TRACE\"\n"), 0o755)

	whiteboard := `#!/bin/sh
set -eu
task10_lock="${TRACE}.lock"
if ! mkdir "$task10_lock" 2>/dev/null; then
  printf 'OVERLAP whiteboard\n' >> "$TRACE"
  exit 98
fi
trap 'rmdir "$task10_lock"' 0 1 2 3 15
sleep 0.02
printf 'whiteboard' >> "$TRACE"
for argument in "$@"; do
  shown=$argument
  case "$shown" in
    generated/.verify/audit.*/report.json) shown='generated/.verify/<scratch>/report.json' ;;
  esac
  printf ' %s' "$shown" >> "$TRACE"
done
printf '\n' >> "$TRACE"
arguments=" $* "
if [ "${1-}" = report ]; then
  shift
  output=
  while [ "$#" -gt 0 ]; do
    if [ "$1" = --output ]; then
      shift
      output=${1-}
    fi
    shift
  done
  if [ -n "$output" ]; then
    printf 'generated report\n' > "$output"
  fi
fi
case "$arguments" in
  *" --release current --format text "*) exit 4 ;;
esac
`
	task10MakeWrite(t, filepath.Join(repository, "generated", ".bin", "whiteboard"), []byte(whiteboard), 0o755)
	verifyScript := "#!/bin/sh\nset -eu\ntask10_lock=\"${TRACE}.lock\"\nif ! mkdir \"$task10_lock\" 2>/dev/null; then printf 'OVERLAP script\\n' >> \"$TRACE\"; exit 98; fi\ntrap 'rmdir \"$task10_lock\"' 0 1 2 3 15\nsleep 0.02\nprintf 'script %s\\n' \"${1-}\" >> \"$TRACE\"\n"
	task10MakeWrite(t, filepath.Join(repository, "scripts", "verify-experiments.sh"), []byte(verifyScript), 0o755)
}

func task10MakeInstallCandidateAttackGo(t *testing.T, repository string) {
	t.Helper()
	head := task10MakeGit(t, repository, "rev-parse", "HEAD")
	goScript := fmt.Sprintf(`#!/bin/sh
set -eu
case "${1-} ${2-}" in
  "env GOVERSION") printf 'go1.26.5\n'; exit 0 ;;
  "env GOMOD") printf '%%s/go.mod\n' "$(pwd)"; exit 0 ;;
  "env GOWORK") printf '\n'; exit 0 ;;
  "env GOFLAGS") printf '%%s\n' '-mod=readonly'; exit 0 ;;
  "version -m")
    printf '%%s: go1.26.5\n' "${3-}"
    printf '\tpath\t%s/cmd/whiteboard\n'
    printf '\tmod\t%s\t(devel)\t\n'
    printf '\tbuild\t-trimpath=true\n'
    printf '\tbuild\tvcs=git\n'
    printf '\tbuild\tvcs.revision=%s\n'
    printf '\tbuild\tvcs.modified=false\n'
    exit 0
    ;;
esac
if [ "${1-}" = build ]; then
  output=
  while [ "$#" -gt 0 ]; do
    if [ "$1" = -o ]; then
      shift
      output=${1-}
    fi
    shift
  done
  if [ -z "$output" ]; then exit 2; fi
  if [ ! -e "$output" ] && [ ! -L "$output" ]; then
    ln -s "$EXTERNAL_SENTINEL" "$output"
  fi
  printf 'candidate bytes\n' > "$output"
  chmod 755 "$output"
fi
`, task10MakeModulePath, task10MakeModulePath, head)
	task10MakeWrite(t, filepath.Join(repository, "fake-bin", "go"), []byte(goScript), 0o755)
}

func task10MakeRequireBuildInfo(t *testing.T, binary, revision string) {
	t.Helper()
	output := task10MakeRun(t, filepath.Dir(binary), nil, "go", "version", "-m", binary)
	checks := []string{
		"\tpath\t" + task10MakeModulePath + "/cmd/whiteboard\n",
		"\tmod\t" + task10MakeModulePath + "\t",
		"\tbuild\t-trimpath=true\n",
		"\tbuild\tvcs=git\n",
		"\tbuild\tvcs.revision=" + revision + "\n",
		"\tbuild\tvcs.modified=false\n",
	}
	for _, check := range checks {
		if strings.Count(output, check) != 1 {
			t.Fatalf("build info missing unique %q:\n%s", check, output)
		}
	}
}

func task10MakeRequireModTime(t *testing.T, path string, want time.Time) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat cached binary: %v", err)
	}
	if !info.ModTime().Equal(want) {
		t.Fatalf("cached binary timestamp = %s; want unchanged %s", info.ModTime(), want)
	}
}

func task10MakeRequireOrdered(t *testing.T, lines []string, expected ...string) {
	t.Helper()
	position := -1
	for _, want := range expected {
		count := 0
		for _, line := range lines {
			if line == want {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("trace count for %q = %d; want exactly one:\n%s", want, count, strings.Join(lines, "\n"))
		}
		found := -1
		for index := position + 1; index < len(lines); index++ {
			if lines[index] == want {
				found = index
				break
			}
		}
		if found < 0 {
			t.Fatalf("trace does not contain %q after index %d:\n%s", want, position, strings.Join(lines, "\n"))
		}
		position = found
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "OVERLAP ") {
			t.Fatalf("parallel gates overlapped: %q", line)
		}
	}
}

func task10MakeTraceLines(t *testing.T, path string) []string {
	t.Helper()
	text := strings.TrimSpace(string(task10MakeRead(t, path)))
	if text == "" {
		t.Fatal("trace is empty")
	}
	return strings.Split(text, "\n")
}

func task10MakeCommit(t *testing.T, repository, message string) string {
	t.Helper()
	task10MakeRun(t, repository, nil, "git", "add", "--all")
	task10MakeRun(t, repository, nil, "git", "commit", "-q", "-m", message)
	return task10MakeGit(t, repository, "rev-parse", "HEAD")
}

func task10MakeGit(t *testing.T, repository string, arguments ...string) string {
	t.Helper()
	return strings.TrimSpace(task10MakeRun(t, repository, nil, "git", arguments...))
}

func task10MakeRun(t *testing.T, directory string, environment []string, name string, arguments ...string) string {
	t.Helper()
	output, err := task10MakeCommand(directory, environment, name, arguments...)
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(arguments, " "), err, output)
	}
	return output
}

func task10MakeCommand(directory string, environment []string, name string, arguments ...string) (string, error) {
	command := exec.Command(name, arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), environment...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func task10MakeSourceRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve integration test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func task10MakeWrite(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent for %q: %v", path, err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func task10MakeRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return data
}

func task10MakeEntryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

func task10MakeRequireExecutable(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Fatalf("required executable %q is unavailable: %v", name, err)
	}
}
