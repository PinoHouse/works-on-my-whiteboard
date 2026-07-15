package integration_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type task10Workflow struct {
	On          map[string]any       `yaml:"on"`
	Permissions map[string]string    `yaml:"permissions"`
	Jobs        map[string]task10Job `yaml:"jobs"`
}

type task10Job struct {
	If          string            `yaml:"if"`
	RunsOn      string            `yaml:"runs-on"`
	Permissions map[string]string `yaml:"permissions"`
	Steps       []task10Step      `yaml:"steps"`
}

type task10Step struct {
	Name string         `yaml:"name"`
	If   string         `yaml:"if"`
	Uses string         `yaml:"uses"`
	Run  string         `yaml:"run"`
	With map[string]any `yaml:"with"`
}

func TestTask10CIWorkflowContract(t *testing.T) {
	data := readTask10File(t, ".github", "workflows", "ci.yml")
	if strings.Contains(string(data), "pull_request_target") {
		t.Fatal("CI workflow must never use pull_request_target")
	}

	var workflow task10Workflow
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("parse CI workflow: %v", err)
	}
	assertExactStringKeys(t, "events", workflow.On, []string{"pull_request", "push", "schedule", "workflow_dispatch"})
	schedule, ok := workflow.On["schedule"].([]any)
	if !ok || len(schedule) != 1 {
		t.Fatalf("schedule = %#v, want one cron entry", workflow.On["schedule"])
	}
	cron, ok := schedule[0].(map[string]any)
	if !ok || len(cron) != 1 || fmt.Sprint(cron["cron"]) != "17 3 * * *" {
		t.Fatalf("schedule entry = %#v, want non-zero-minute W0 cron", schedule[0])
	}
	if len(workflow.Permissions) != 1 || workflow.Permissions["contents"] != "read" {
		t.Fatalf("permissions = %#v, want contents: read only", workflow.Permissions)
	}
	assertExactStringKeys(t, "jobs", workflow.Jobs, []string{"verify-deep", "verify-fast", "w0-release-contract"})

	for name, job := range workflow.Jobs {
		if len(job.Permissions) != 0 {
			t.Fatalf("job %s overrides top-level permissions: %#v", name, job.Permissions)
		}
		if job.RunsOn != "ubuntu-latest" {
			t.Fatalf("job %s runs-on = %q, want ubuntu-latest", name, job.RunsOn)
		}
		checkout := requireSingleAction(t, name, job, "actions/checkout@v7")
		assertWorkflowInput(t, name+" checkout", checkout, "persist-credentials", "false")
		setup := requireSingleAction(t, name, job, "actions/setup-go@v6")
		assertWorkflowInput(t, name+" setup-go", setup, "go-version-file", "go.mod")
		assertWorkflowInput(t, name+" setup-go", setup, "cache", "true")
		assertWorkflowInput(t, name+" setup-go", setup, "cache-dependency-path", "go.sum")

		runs := joinedWorkflowRuns(job)
		assertCommandLine(t, name, runs, `test "$(go env GOVERSION)" = "go1.26.5"`, 1)
		assertCommandLine(t, name, runs, "go mod verify", 1)
	}

	developmentCondition := "${{ github.event_name == 'push' || github.event_name == 'pull_request' }}"
	fast := workflow.Jobs["verify-fast"]
	if fast.If != developmentCondition {
		t.Fatalf("verify-fast if = %q, want push/PR only", fast.If)
	}
	assertCommandLine(t, "verify-fast", joinedWorkflowRuns(fast), "make verify-fast", 1)
	assertExactActions(t, "verify-fast", fast, []string{"actions/checkout@v7", "actions/setup-go@v6"})
	if countAction(fast, "actions/upload-artifact@v7") != 0 {
		t.Fatal("verify-fast must not upload generated evidence")
	}

	releaseContract := workflow.Jobs["w0-release-contract"]
	if releaseContract.If != developmentCondition {
		t.Fatalf("w0-release-contract if = %q, want push/PR only", releaseContract.If)
	}
	releaseRuns := joinedWorkflowRuns(releaseContract)
	assertExactActions(t, "w0-release-contract", releaseContract, []string{"actions/checkout@v7", "actions/setup-go@v6"})
	listCommand := "go test ./internal/cli -list '^TestW0ReleaseScopeContract$'"
	runCommand := "go test ./internal/cli -run '^TestW0ReleaseScopeContract$' -count=1 -v"
	assertCommandLine(t, "w0-release-contract", releaseRuns, listCommand, 1)
	assertCommandLine(t, "w0-release-contract", releaseRuns, runCommand, 1)
	if listIndex, runIndex := strings.Index(releaseRuns, listCommand), strings.Index(releaseRuns, runCommand); listIndex < 0 || runIndex < 0 || listIndex >= runIndex {
		t.Fatalf("W0 list guard must precede execution:\n%s", releaseRuns)
	}
	for _, required := range []string{
		`awk '$0 == "TestW0ReleaseScopeContract" { count++ } END { print count+0 }'`,
		`test "$test_count" -eq 1`,
	} {
		if !strings.Contains(releaseRuns, required) {
			t.Fatalf("W0 list guard missing %q:\n%s", required, releaseRuns)
		}
	}
	if countAction(releaseContract, "actions/upload-artifact@v7") != 0 {
		t.Fatal("w0-release-contract must not upload generated evidence")
	}

	deep := workflow.Jobs["verify-deep"]
	if deep.If != "${{ github.event_name == 'schedule' || github.event_name == 'workflow_dispatch' }}" {
		t.Fatalf("verify-deep if = %q, want schedule/manual only", deep.If)
	}
	deepRuns := joinedWorkflowRuns(deep)
	assertExactActions(t, "verify-deep", deep, []string{"actions/checkout@v7", "actions/setup-go@v6", "actions/upload-artifact@v7"})
	for _, command := range []string{
		`mkdir -p "$artifact_root"`,
		"make build",
		`scripts/verify-experiments.sh deep "$GITHUB_WORKSPACE" "$RUNNER_TEMP/w0-deep"`,
		`printf 'exit_code=%s\n' "$status" >"$artifact_root/status.txt"`,
		`exit "$status"`,
	} {
		assertCommandLine(t, "verify-deep", deepRuns, command, 1)
	}
	upload := requireSingleAction(t, "verify-deep", deep, "actions/upload-artifact@v7")
	if upload.If != "${{ always() }}" {
		t.Fatalf("upload if = %q, want always()", upload.If)
	}
	assertWorkflowInput(t, "verify-deep upload", upload, "name", "w0-deep-${{ github.run_id }}-${{ github.run_attempt }}")
	assertWorkflowInput(t, "verify-deep upload", upload, "path", "${{ runner.temp }}/w0-deep")
	assertWorkflowInput(t, "verify-deep upload", upload, "retention-days", "14")
	assertWorkflowInput(t, "verify-deep upload", upload, "overwrite", "false")
	assertWorkflowInput(t, "verify-deep upload", upload, "include-hidden-files", "false")
	assertWorkflowInput(t, "verify-deep upload", upload, "if-no-files-found", "error")
}

func TestTask10DocumentationContract(t *testing.T) {
	readme := string(readTask10File(t, "README.md"))
	for _, required := range []string{
		"Works on My Machine",
		"第一性原理",
		"75 个规范案例",
		"1/75",
		"74",
		"token-bucket-reference-model",
		"token-bucket",
		"shared-token-bucket",
		"per-node-token-bucket",
		"shared-fail-closed",
		"shared-fail-open",
		"go1.26.5",
		"make verify-fast",
		"make verify-deep",
		"make audit-evidence",
		"make verify",
		"docs/methodology/evidence.md",
	} {
		if !strings.Contains(readme, required) {
			t.Errorf("README missing %q", required)
		}
	}
	for _, tuple := range []string{
		"token-bucket` / `burst-and-refill-boundary` | baseline | `token-bucket-burst-boundary` | `token-bucket-bounds-burst-and-average-rate` | `token-bucket-reference-model` | —",
		"token-bucket` / `burst-and-refill-boundary` | variant | `token-bucket-burst-boundary` | `token-bucket-bounds-burst-and-average-rate` | `token-bucket` | —",
		"distributed-rate-limiter` / `per-node-vs-shared-quota` | baseline | `distributed-rate-limiter-global-quota` | `distributed-rate-limiter-per-node-multiplies-global-quota` | `shared-token-bucket` | —",
		"distributed-rate-limiter` / `per-node-vs-shared-quota` | variant | `distributed-rate-limiter-global-quota` | `distributed-rate-limiter-per-node-multiplies-global-quota` | `per-node-token-bucket` | —",
		"distributed-rate-limiter` / `coordinator-outage-policy` | baseline | `distributed-rate-limiter-outage-policy` | `distributed-rate-limiter-outage-policy-trades-availability-for-quota` | `shared-fail-closed` | —",
		"distributed-rate-limiter` / `coordinator-outage-policy` | variant | `distributed-rate-limiter-outage-policy` | `distributed-rate-limiter-outage-policy-trades-availability-for-quota` | `shared-fail-open` | —",
	} {
		if !strings.Contains(readme, tuple) {
			t.Errorf("README missing complete W0 cell tuple %q", tuple)
		}
	}

	methodology := string(readTask10File(t, "docs", "methodology", "evidence.md"))
	for _, required := range []string{
		"ASSUMED",
		"DEDUCED",
		"MEASURED",
		"SOURCED",
		"binding_id",
		"claim_id",
		"run_set_id",
		"lab_id",
		"required_run_id",
		"implementation_id",
		"adapter_id",
		"逻辑时间",
		"固定 seed",
		"整数",
		"Git index",
		"input digest",
		"vcs.modified=false",
		"当前调用",
		"只追加",
		"不可变 release snapshot",
		"source commit A",
		"evidence commit B",
		"git clone --no-local",
		"passed",
		"failed",
		"skipped",
		"flaky",
		"inconclusive",
		"本地环境",
		"1/75",
		"missing=74",
		"expected-negative",
	} {
		if !strings.Contains(methodology, required) {
			t.Errorf("evidence methodology missing %q", required)
		}
	}
}

func TestTask10GeneratedDirectoryAndIgnoreContract(t *testing.T) {
	ignore := string(readTask10File(t, ".gitignore"))
	for _, entry := range []string{"generated/.bin/", "generated/.verify/"} {
		if countExactLine(ignore, entry) != 1 {
			t.Errorf(".gitignore must contain %q exactly once", entry)
		}
	}
	for _, forbidden := range []string{"generated/", "/generated/", "generated/*"} {
		if countExactLine(ignore, forbidden) != 0 {
			t.Errorf(".gitignore must not hide committed generated artifacts with %q", forbidden)
		}
	}
	info, err := os.Stat(task10Path("generated", ".gitkeep"))
	if err != nil {
		t.Fatalf("stat generated/.gitkeep: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("generated/.gitkeep mode = %v, want regular file", info.Mode())
	}
}

func readTask10File(t *testing.T, elements ...string) []byte {
	t.Helper()
	data, err := os.ReadFile(task10Path(elements...))
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Join(elements...), err)
	}
	return data
}

func task10Path(elements ...string) string {
	return filepath.Join(append([]string{"..", ".."}, elements...)...)
}

func assertExactStringKeys[T any](t *testing.T, label string, values map[string]T, want []string) {
	t.Helper()
	got := make([]string, 0, len(values))
	for key := range values {
		got = append(got, key)
	}
	sort.Strings(got)
	sortedWant := append([]string{}, want...)
	sort.Strings(sortedWant)
	if strings.Join(got, "\x00") != strings.Join(sortedWant, "\x00") {
		t.Fatalf("%s = %v, want %v", label, got, sortedWant)
	}
}

func requireSingleAction(t *testing.T, jobName string, job task10Job, action string) task10Step {
	t.Helper()
	var matches []task10Step
	for _, step := range job.Steps {
		if step.Uses == action {
			matches = append(matches, step)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("job %s action %s count = %d, want 1", jobName, action, len(matches))
	}
	return matches[0]
}

func countAction(job task10Job, action string) int {
	count := 0
	for _, step := range job.Steps {
		if step.Uses == action {
			count++
		}
	}
	return count
}

func assertExactActions(t *testing.T, label string, job task10Job, want []string) {
	t.Helper()
	got := make([]string, 0)
	for _, step := range job.Steps {
		if step.Uses != "" {
			got = append(got, step.Uses)
		}
	}
	sort.Strings(got)
	sortedWant := append([]string{}, want...)
	sort.Strings(sortedWant)
	if strings.Join(got, "\x00") != strings.Join(sortedWant, "\x00") {
		t.Fatalf("%s actions = %v, want %v", label, got, sortedWant)
	}
}

func assertWorkflowInput(t *testing.T, label string, step task10Step, key, want string) {
	t.Helper()
	value, exists := step.With[key]
	if !exists || fmt.Sprint(value) != want {
		t.Fatalf("%s input %s = %#v, want %q", label, key, value, want)
	}
}

func joinedWorkflowRuns(job task10Job) string {
	runs := make([]string, 0, len(job.Steps))
	for _, step := range job.Steps {
		if step.Run != "" {
			runs = append(runs, step.Run)
		}
	}
	return strings.Join(runs, "\n")
}

func assertCommandLine(t *testing.T, label, script, command string, want int) {
	t.Helper()
	count := 0
	for _, line := range strings.Split(script, "\n") {
		if strings.TrimSpace(line) == command {
			count++
		}
	}
	if count != want {
		t.Fatalf("%s command %q count = %d, want %d:\n%s", label, command, count, want, script)
	}
}

func countExactLine(content, want string) int {
	count := 0
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == want {
			count++
		}
	}
	return count
}
