package report

import (
	"bytes"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/release"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

func TestBuildDiffComparesOnlyAllowedSemanticFields(t *testing.T) {
	leftEnvironment := evidence.Environment{GoVersion: "go1.26.5", OS: "linux", Arch: "amd64", CPU: "unknown", LogicalCPUs: 8}
	rightEnvironment := evidence.Environment{GoVersion: "go1.26.5", OS: "darwin", Arch: "arm64", CPU: "unknown", LogicalCPUs: 10}
	_, _, left := testReportFixture(t, testRecordOptions{
		id:               "run-20260714T010203.004Z-00000000000000000000000000000001",
		runSetID:         "set-20260714T010203.004Z-00000000000000000000000000000001",
		sourceCommit:     strings.Repeat("c", 40),
		finishedAt:       time.Date(2000, 1, 1, 0, 0, 1, 0, time.UTC),
		metricValue:      10,
		assertionMessage: "left message",
		environment:      leftEnvironment,
		conclusion:       "left conclusion",
		limitations:      []string{"shared limitation"},
	})
	_, _, right := testReportFixture(t, testRecordOptions{
		id:               "run-20260714T010203.004Z-00000000000000000000000000000002",
		runSetID:         "set-20260714T010203.004Z-00000000000000000000000000000002",
		sourceCommit:     strings.Repeat("d", 40),
		finishedAt:       time.Date(2000, 1, 1, 0, 0, 2, 0, time.UTC),
		metricValue:      15,
		assertionMessage: "right message",
		environment:      rightEnvironment,
		conclusion:       "right conclusion",
		limitations:      []string{"shared limitation"},
	})

	diff, err := BuildDiff(left, right)
	if err != nil {
		t.Fatalf("BuildDiff returned error: %v", err)
	}
	if diff.Status != DiffStatusChanges || len(diff.Rows) != 1 {
		t.Fatalf("diff = %#v, want one changed row", diff)
	}
	row := diff.Rows[0]
	if len(row.MetricChanges) != 1 || row.MetricChanges[0].ID != "requests.total" || row.MetricChanges[0].Delta != "5" || row.MetricChanges[0].Left == nil || row.MetricChanges[0].Right == nil || row.MetricChanges[0].Left.Value != 10 || row.MetricChanges[0].Right.Value != 15 {
		t.Fatalf("metric changes = %#v", row.MetricChanges)
	}
	wantEnvironment := []EnvironmentChange{
		{Field: "arch", Left: "amd64", Right: "arm64"},
		{Field: "logical_cpus", Left: "8", Right: "10"},
		{Field: "os", Left: "linux", Right: "darwin"},
	}
	if !reflect.DeepEqual(row.EnvironmentChanges, wantEnvironment) {
		t.Fatalf("environment changes = %#v, want %#v", row.EnvironmentChanges, wantEnvironment)
	}
	if !reflect.DeepEqual(row.AssertionMessageChanges, []AssertionMessageChange{{ID: "assert-a", Left: "left message", Right: "right message"}}) {
		t.Fatalf("assertion message changes = %#v", row.AssertionMessageChanges)
	}
	if row.Conclusion == nil || row.Conclusion.Left != "left conclusion" || row.Conclusion.Right != "right conclusion" || row.Limitations != nil {
		t.Fatalf("text changes = (%#v, %#v)", row.Conclusion, row.Limitations)
	}

	var encoded bytes.Buffer
	if err := WriteDiffJSON(&encoded, diff); err != nil {
		t.Fatalf("WriteDiffJSON: %v", err)
	}
	for _, forbidden := range []string{left.Records[0].ID, right.Records[0].ID, string(left.Records[0].RunSetID), string(right.Records[0].RunSetID), left.Records[0].SourceCommit, right.Records[0].SourceCommit, left.Records[0].ContentDigest, right.Records[0].ContentDigest, "finished_at", "started_at"} {
		if strings.Contains(encoded.String(), forbidden) {
			t.Fatalf("diff leaked ignored metadata %q: %s", forbidden, encoded.String())
		}
	}
	assertGolden(t, "diff.json.golden", encoded.Bytes())

	var markdown bytes.Buffer
	if err := WriteDiffMarkdown(&markdown, diff); err != nil {
		t.Fatalf("WriteDiffMarkdown: %v", err)
	}
	assertGolden(t, "diff.md.golden", markdown.Bytes())
}

func TestBuildDiffIgnoresAttemptRunSetCommitDigestAndTimestamps(t *testing.T) {
	_, _, left := testReportFixture(t, testRecordOptions{
		id:           "run-20260714T010203.004Z-00000000000000000000000000000001",
		runSetID:     "set-20260714T010203.004Z-00000000000000000000000000000001",
		sourceCommit: strings.Repeat("a", 40),
		finishedAt:   time.Date(2000, 1, 1, 0, 0, 1, 0, time.UTC),
		metricValue:  10,
	})
	_, _, right := testReportFixture(t, testRecordOptions{
		id:           "run-20260714T010203.004Z-00000000000000000000000000000002",
		runSetID:     "set-20260714T010203.004Z-00000000000000000000000000000002",
		sourceCommit: strings.Repeat("b", 40),
		finishedAt:   time.Date(2000, 1, 1, 0, 0, 9, 0, time.UTC),
		metricValue:  10,
	})
	diff, err := BuildDiff(left, right)
	if err != nil {
		t.Fatalf("BuildDiff returned error: %v", err)
	}
	if diff.Status != DiffStatusNoChanges || len(diff.Rows) != 0 {
		t.Fatalf("diff = %#v, want no changes", diff)
	}
	var markdown bytes.Buffer
	if err := WriteDiffMarkdown(&markdown, diff); err != nil {
		t.Fatalf("WriteDiffMarkdown: %v", err)
	}
	assertGolden(t, "diff-empty.md.golden", markdown.Bytes())
	var jsonOutput bytes.Buffer
	if err := WriteDiffJSON(&jsonOutput, diff); err != nil {
		t.Fatalf("WriteDiffJSON: %v", err)
	}
	assertGolden(t, "diff-empty.json.golden", jsonOutput.Bytes())
}

func TestValidatedDiffModelRejectsRendererInvalidModel(t *testing.T) {
	_, _, left := testReportFixture(t, testRecordOptions{assertionMessage: "left"})
	_, _, right := testReportFixture(t, testRecordOptions{assertionMessage: "right"})
	model, err := BuildDiff(left, right)
	if err != nil {
		t.Fatalf("BuildDiff fixture: %v", err)
	}
	model.Rows[0].AssertionMessageChanges[0].Right = "attempt_run-20260714T010203.004Z-00000000000000000000000000000009"

	validated, err := validatedDiffModel(model)
	if !errors.Is(err, ErrModelInvalid) {
		t.Fatalf("validatedDiffModel error = %v, want ErrModelInvalid", err)
	}
	if !reflect.DeepEqual(validated, DiffModel{}) {
		t.Fatalf("validatedDiffModel returned partial model: %#v", validated)
	}
}

func TestBuildDiffUsesExactNonWrappingIntegerDelta(t *testing.T) {
	_, _, left := testReportFixture(t, testRecordOptions{metricValue: math.MinInt64})
	_, _, right := testReportFixture(t, testRecordOptions{metricValue: math.MaxInt64})
	diff, err := BuildDiff(left, right)
	if err != nil {
		t.Fatalf("BuildDiff returned error: %v", err)
	}
	if got := diff.Rows[0].MetricChanges[0].Delta; got != "18446744073709551615" {
		t.Fatalf("delta = %q, want exact checked delta", got)
	}
}

func TestBuildDiffRejectsExpectedDefinitionDrift(t *testing.T) {
	_, _, baseline := testReportFixture(t, testRecordOptions{})
	tests := []struct {
		name    string
		options testRecordOptions
	}{
		{name: "logical start", options: testRecordOptions{start: time.Date(2000, 1, 1, 0, 0, 0, 1, time.UTC)}},
		{name: "limitations", options: testRecordOptions{limitations: []string{"different definition limitation"}}},
		{name: "measurement unit", options: testRecordOptions{measurementUnit: "operations"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, drifted := testReportFixture(t, test.options)
			if _, err := BuildDiff(baseline, drifted); !errors.Is(err, ErrDiffIncompatible) {
				t.Fatalf("BuildDiff error = %v, want ErrDiffIncompatible", err)
			}
		})
	}
}

func TestBuildDiffUsesTypedSixFieldJoinAndSeparateRole(t *testing.T) {
	_, _, left := testReportFixture(t, testRecordOptions{})
	tests := []struct {
		name   string
		mutate func(*validator.MatrixCell)
	}{
		{name: "lab", mutate: func(cell *validator.MatrixCell) { cell.LabID = "lab-b" }},
		{name: "required run", mutate: func(cell *validator.MatrixCell) { cell.RequiredRunID = "run-b" }},
		{name: "binding", mutate: func(cell *validator.MatrixCell) { cell.BindingID = "binding-b" }},
		{name: "claim", mutate: func(cell *validator.MatrixCell) { cell.ClaimID = "claim-b" }},
		{name: "implementation", mutate: func(cell *validator.MatrixCell) { cell.ImplementationID = "implementation-b" }},
		{name: "role", mutate: func(cell *validator.MatrixCell) { cell.Role = string(evidence.RoleVariant) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, right := testReportFixture(t, testRecordOptions{mutateCell: test.mutate})
			if _, err := BuildDiff(left, right); !errors.Is(err, ErrDiffIncompatible) {
				t.Fatalf("BuildDiff error = %v, want ErrDiffIncompatible", err)
			}
		})
	}
}

func TestDiffBuildersRejectUnboundAndTamperedSnapshots(t *testing.T) {
	_, _, valid := testReportFixture(t, testRecordOptions{})
	if _, err := BuildDiff(release.AuditedSnapshot{}, valid); !errors.Is(err, release.ErrSnapshotUnbound) {
		t.Fatalf("BuildDiff left error = %v, want ErrSnapshotUnbound", err)
	}
	if _, err := BuildDiff(valid, release.AuditedSnapshot{}); !errors.Is(err, release.ErrSnapshotUnbound) {
		t.Fatalf("BuildDiff right error = %v, want ErrSnapshotUnbound", err)
	}
	if _, err := BuildNoBaselineDiff(release.AuditedSnapshot{}); !errors.Is(err, release.ErrSnapshotUnbound) {
		t.Fatalf("BuildNoBaselineDiff error = %v, want ErrSnapshotUnbound", err)
	}
	valid.Records[0].Conclusion = "tampered"
	if _, err := BuildNoBaselineDiff(valid); !errors.Is(err, release.ErrSnapshotUnbound) {
		t.Fatalf("tampered BuildNoBaselineDiff error = %v, want ErrSnapshotUnbound", err)
	}
}

func TestBuildDiffRejectsDistinctIndependentlyAuditedAdapterCells(t *testing.T) {
	adapterCell := func(id string) func(*validator.MatrixCell) {
		return func(cell *validator.MatrixCell) {
			cell.Role = string(evidence.RoleAdapter)
			cell.ImplementationID = id
			cell.AdapterID = id
		}
	}
	_, _, left := testReportFixture(t, testRecordOptions{mutateCell: adapterCell("adapter-a")})
	_, _, right := testReportFixture(t, testRecordOptions{mutateCell: adapterCell("adapter-b")})
	if err := release.ValidateAuditedSnapshot(left); err != nil {
		t.Fatalf("left adapter snapshot is not independently bound: %v", err)
	}
	if err := release.ValidateAuditedSnapshot(right); err != nil {
		t.Fatalf("right adapter snapshot is not independently bound: %v", err)
	}
	if _, err := BuildDiff(left, right); !errors.Is(err, ErrDiffIncompatible) {
		t.Fatalf("BuildDiff error = %v, want ErrDiffIncompatible", err)
	}
}

func TestTypedDiffCellKeyIncludesAdapterIDIndependently(t *testing.T) {
	// A valid adapter role requires AdapterID == ImplementationID, so two valid
	// audited snapshots cannot differ in only AdapterID. This pure key check
	// isolates the sixth coordinate while the test above covers valid snapshots.
	left := evidence.CellKey{
		LabID:            "lab-a",
		RequiredRunID:    "run-a",
		BindingID:        "binding-a",
		ClaimID:          "claim-a",
		ImplementationID: "adapter-a",
		AdapterID:        "adapter-a",
	}
	right := left
	right.AdapterID = "adapter-b"
	if typedDiffCellKey(left) == typedDiffCellKey(right) {
		t.Fatal("typed diff key ignored an independent AdapterID change")
	}
}

func TestBuildNoBaselineDiffLiteralGoldens(t *testing.T) {
	_, _, right := testReportFixture(t, testRecordOptions{})
	diff, err := BuildNoBaselineDiff(right)
	if err != nil {
		t.Fatalf("BuildNoBaselineDiff returned error: %v", err)
	}
	if diff.Status != DiffStatusNoBaseline || len(diff.Rows) != 0 {
		t.Fatalf("diff = %#v, want no baseline", diff)
	}
	var jsonOutput bytes.Buffer
	if err := WriteDiffJSON(&jsonOutput, diff); err != nil {
		t.Fatalf("WriteDiffJSON: %v", err)
	}
	assertGolden(t, "diff-no-baseline.json.golden", jsonOutput.Bytes())
	var markdownOutput bytes.Buffer
	if err := WriteDiffMarkdown(&markdownOutput, diff); err != nil {
		t.Fatalf("WriteDiffMarkdown: %v", err)
	}
	assertGolden(t, "diff-no-baseline.md.golden", markdownOutput.Bytes())
}

func TestDiffRenderersAreStableAndPropagateShortWrites(t *testing.T) {
	_, _, snapshot := testReportFixture(t, testRecordOptions{})
	diff, err := BuildNoBaselineDiff(snapshot)
	if err != nil {
		t.Fatalf("BuildNoBaselineDiff: %v", err)
	}
	for _, render := range []struct {
		name string
		fn   func(io.Writer, DiffModel) error
	}{{name: "markdown", fn: WriteDiffMarkdown}, {name: "json", fn: WriteDiffJSON}} {
		t.Run(render.name, func(t *testing.T) {
			var first bytes.Buffer
			if err := render.fn(&first, diff); err != nil {
				t.Fatalf("first render: %v", err)
			}
			for iteration := 0; iteration < 100; iteration++ {
				var next bytes.Buffer
				if err := render.fn(&next, diff); err != nil {
					t.Fatalf("render %d: %v", iteration, err)
				}
				if !bytes.Equal(first.Bytes(), next.Bytes()) {
					t.Fatalf("render %d differs", iteration)
				}
			}
			if err := render.fn(shortWriter{}, diff); !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("short write error = %v, want io.ErrShortWrite", err)
			}
		})
	}
}

func TestDiffRenderersRejectTamperedUnsafeDynamicTextWithoutWriting(t *testing.T) {
	_, _, left := testReportFixture(t, testRecordOptions{assertionMessage: "left", conclusion: "left conclusion"})
	_, _, right := testReportFixture(t, testRecordOptions{assertionMessage: "right", conclusion: "right conclusion"})
	base, err := BuildDiff(left, right)
	if err != nil {
		t.Fatalf("BuildDiff: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*DiffModel)
	}{
		{name: "status", mutate: func(model *DiffModel) {
			model.Status = "unknown"
		}},
		{name: "input digest", mutate: func(model *DiffModel) {
			model.InputDigest = "not-a-digest"
		}},
		{name: "profile", mutate: func(model *DiffModel) {
			model.Profile = "benchmark"
		}},
		{name: "cell ID", mutate: func(model *DiffModel) {
			model.Rows[0].Cell.LabID = "/absolute/workspace/secret"
		}},
		{name: "metric ID", mutate: func(model *DiffModel) {
			left := evidence.Measurement{Unit: "count", Value: 1}
			model.Rows[0].MetricChanges = []MetricChange{{ID: "/absolute/workspace/secret", Left: &left}}
		}},
		{name: "metric unit", mutate: func(model *DiffModel) {
			left := evidence.Measurement{Unit: "/absolute/workspace/secret", Value: 1}
			model.Rows[0].MetricChanges = []MetricChange{{ID: "requests.total", Left: &left}}
		}},
		{name: "metric delta", mutate: func(model *DiffModel) {
			left := evidence.Measurement{Unit: "count", Value: 1}
			right := evidence.Measurement{Unit: "count", Value: 2}
			model.Rows[0].MetricChanges = []MetricChange{{ID: "requests.total", Left: &left, Right: &right, Delta: "/absolute/workspace/secret"}}
		}},
		{name: "environment field", mutate: func(model *DiffModel) {
			model.Rows[0].EnvironmentChanges = []EnvironmentChange{{Field: "/absolute/workspace/secret", Left: "linux", Right: "darwin"}}
		}},
		{name: "environment value", mutate: func(model *DiffModel) {
			model.Rows[0].EnvironmentChanges = []EnvironmentChange{{Field: "os", Left: "linux", Right: "/absolute/workspace/secret"}}
		}},
		{name: "assertion ID", mutate: func(model *DiffModel) {
			model.Rows[0].AssertionMessageChanges[0].ID = "/absolute/workspace/secret"
		}},
		{name: "assertion message", mutate: func(model *DiffModel) {
			model.Rows[0].AssertionMessageChanges[0].Left = `C:\Users\runner\AppData\Local\Temp\result.json`
		}},
		{name: "conclusion", mutate: func(model *DiffModel) {
			model.Rows[0].Conclusion.Right = "colored \x1b[31mresult"
		}},
		{name: "limitation", mutate: func(model *DiffModel) {
			model.Rows[0].Limitations = &StringSliceChange{Left: []string{"portable"}, Right: []string{"host root /absolute/workspace/secret"}}
		}},
	}
	for _, test := range tests {
		for _, render := range []struct {
			name string
			fn   func(io.Writer, DiffModel) error
		}{{name: "markdown", fn: WriteDiffMarkdown}, {name: "json", fn: WriteDiffJSON}} {
			t.Run(test.name+"/"+render.name, func(t *testing.T) {
				model := cloneDiffModelForDynamicTextTest(base)
				test.mutate(&model)
				var output bytes.Buffer
				err := render.fn(&output, model)
				if !errors.Is(err, ErrModelInvalid) {
					t.Fatalf("render error = %v, want ErrModelInvalid", err)
				}
				if output.Len() != 0 {
					t.Fatalf("renderer wrote unsafe output: %q", output.Bytes())
				}
			})
		}
	}
}

func cloneDiffModelForDynamicTextTest(source DiffModel) DiffModel {
	cloned := source
	cloned.Rows = append([]DiffRow{}, source.Rows...)
	for index := range cloned.Rows {
		cloned.Rows[index].AssertionMessageChanges = append([]AssertionMessageChange{}, source.Rows[index].AssertionMessageChanges...)
		if source.Rows[index].Conclusion != nil {
			conclusion := *source.Rows[index].Conclusion
			cloned.Rows[index].Conclusion = &conclusion
		}
	}
	return cloned
}
