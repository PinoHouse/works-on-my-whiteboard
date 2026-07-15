package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
	"github.com/yuin/goldmark"
)

func TestWriteMarkdownLiteralGoldenAndEscaping(t *testing.T) {
	model := testRenderModel()
	var output bytes.Buffer
	if err := WriteMarkdown(&output, model); err != nil {
		t.Fatalf("WriteMarkdown returned error: %v", err)
	}
	assertGolden(t, "report.md.golden", output.Bytes())
	if bytes.Contains(output.Bytes(), []byte{0x1b}) || !bytes.HasSuffix(output.Bytes(), []byte("\n")) || bytes.HasSuffix(output.Bytes(), []byte("\n\n")) {
		t.Fatalf("Markdown contains ANSI or does not have exactly one final LF: %q", output.Bytes())
	}
	if !strings.Contains(output.String(), `a\|b<br>c\\slash`) {
		t.Fatalf("Markdown table escaping missing from %q", output.String())
	}
}

func TestWriteJSONLiteralGoldenAndOneLF(t *testing.T) {
	var output bytes.Buffer
	if err := WriteJSON(&output, testRenderModel()); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}
	assertGolden(t, "report.json.golden", output.Bytes())
	if !bytes.HasSuffix(output.Bytes(), []byte("\n")) || bytes.HasSuffix(output.Bytes(), []byte("\n\n")) {
		t.Fatalf("JSON does not have exactly one final LF: %q", output.Bytes())
	}
}

func TestReportRenderersAreByteStable(t *testing.T) {
	model := testRenderModel()
	for _, render := range []struct {
		name string
		fn   func(io.Writer, Model) error
	}{{name: "markdown", fn: WriteMarkdown}, {name: "json", fn: WriteJSON}} {
		t.Run(render.name, func(t *testing.T) {
			var first bytes.Buffer
			if err := render.fn(&first, model); err != nil {
				t.Fatalf("first render: %v", err)
			}
			for iteration := 0; iteration < 100; iteration++ {
				var next bytes.Buffer
				if err := render.fn(&next, model); err != nil {
					t.Fatalf("render %d: %v", iteration, err)
				}
				if !bytes.Equal(first.Bytes(), next.Bytes()) {
					t.Fatalf("render %d differs\nfirst: %q\nnext: %q", iteration, first.Bytes(), next.Bytes())
				}
			}
		})
	}
}

func TestReportRenderersPropagateShortWrites(t *testing.T) {
	for _, render := range []struct {
		name string
		fn   func(io.Writer, Model) error
	}{{name: "markdown", fn: WriteMarkdown}, {name: "json", fn: WriteJSON}} {
		t.Run(render.name, func(t *testing.T) {
			err := render.fn(shortWriter{}, testRenderModel())
			if !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("error = %v, want io.ErrShortWrite", err)
			}
		})
	}
}

func TestReportRenderersRejectTamperedUnsafeDynamicTextWithoutWriting(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Model)
	}{
		{name: "input digest", mutate: func(model *Model) {
			model.InputDigest = "not-a-digest"
		}},
		{name: "profile", mutate: func(model *Model) {
			model.Profile = "benchmark"
		}},
		{name: "coverage ID", mutate: func(model *Model) {
			model.Coverage.MissingCaseIDs[0] = "/absolute/workspace/secret"
		}},
		{name: "cell ID", mutate: func(model *Model) {
			model.Rows[0].Cell.LabID = "/absolute/workspace/secret"
		}},
		{name: "evidence ID", mutate: func(model *Model) {
			model.Rows[0].EvidenceID = "not-an-evidence-id"
		}},
		{name: "status", mutate: func(model *Model) {
			model.Rows[0].Status = "unknown"
		}},
		{name: "workload ID", mutate: func(model *Model) {
			model.Rows[0].Workload.ID = "/absolute/workspace/secret"
		}},
		{name: "workload parameter key", mutate: func(model *Model) {
			model.Rows[0].Workload.Parameters["/absolute/workspace/secret"] = 1
		}},
		{name: "fault ID", mutate: func(model *Model) {
			model.Rows[0].Faults[0].ID = "/absolute/workspace/secret"
		}},
		{name: "measurement ID", mutate: func(model *Model) {
			model.Rows[0].Measurements["/absolute/workspace/secret"] = evidence.Measurement{Unit: "count", Value: 1}
		}},
		{name: "measurement unit", mutate: func(model *Model) {
			model.Rows[0].Measurements["requests.total"] = evidence.Measurement{Unit: "/absolute/workspace/secret", Value: 10}
		}},
		{name: "assertion ID", mutate: func(model *Model) {
			model.Rows[0].Assertions[0].ID = "/absolute/workspace/secret"
		}},
		{name: "assertion message", mutate: func(model *Model) {
			model.Rows[0].Assertions[0].Message = "result at /absolute/workspace/secret"
		}},
		{name: "environment Go version", mutate: func(model *Model) {
			model.Rows[0].Environment.GoVersion = "go1.26.5\t/absolute/workspace/secret"
		}},
		{name: "environment OS", mutate: func(model *Model) {
			model.Rows[0].Environment.OS = "/absolute/workspace/secret"
		}},
		{name: "environment CPU", mutate: func(model *Model) {
			model.Rows[0].Environment.CPU = "/absolute/workspace/secret"
		}},
		{name: "conclusion", mutate: func(model *Model) {
			model.Rows[0].Conclusion = "attempt run-20260714T010203.004Z-00000000000000000000000000000009 passed"
		}},
		{name: "conclusion mixed malformed and encoded path", mutate: func(model *Model) {
			model.Rows[0].Conclusion = "malformed=%ZZ host root %2Fprivate%2Ftmp%2Fresult"
		}},
		{name: "conclusion HTML entity path", mutate: func(model *Model) {
			model.Rows[0].Conclusion = "host root &#x2F;private&#x2F;tmp&#x2F;result"
		}},
		{name: "limitation", mutate: func(model *Model) {
			model.Rows[0].Limitations[0] = "host root /absolute/workspace/secret"
		}},
		{name: "source ID", mutate: func(model *Model) {
			model.Sources[0].ID = "/absolute/workspace/secret"
		}},
		{name: "source title", mutate: func(model *Model) {
			model.Sources[0].Title = "source at /absolute/workspace/secret"
		}},
		{name: "source URL", mutate: func(model *Model) {
			model.Sources[0].URL = "file://server/share/source"
		}},
		{name: "source URL table separator", mutate: func(model *Model) {
			model.Sources[0].URL = "https://example.com/a|b"
		}},
		{name: "source URL backslash", mutate: func(model *Model) {
			model.Sources[0].URL = `https://example.com/a\b`
		}},
		{name: "source title Unicode format", mutate: func(model *Model) {
			model.Sources[0].Title = "Source \u2066hidden\u2069"
		}},
		{name: "source URL Unicode format", mutate: func(model *Model) {
			model.Sources[0].URL = "https://example.com/a\u200bhidden"
		}},
		{name: "diagnostic code", mutate: func(model *Model) {
			model.Diagnostics[0].Code = "attacker_controlled"
		}},
		{name: "diagnostic severity", mutate: func(model *Model) {
			model.Diagnostics[0].Severity = "warning"
		}},
		{name: "diagnostic path", mutate: func(model *Model) {
			model.Diagnostics[0].Path = "/absolute/workspace/secret"
		}},
		{name: "diagnostic entity", mutate: func(model *Model) {
			model.Diagnostics[0].EntityID = "/absolute/workspace/secret"
		}},
		{name: "diagnostic message", mutate: func(model *Model) {
			model.Diagnostics[0].Message = "host root /absolute/workspace/secret"
		}},
	}
	for _, test := range tests {
		for _, render := range []struct {
			name string
			fn   func(io.Writer, Model) error
		}{{name: "markdown", fn: WriteMarkdown}, {name: "json", fn: WriteJSON}} {
			t.Run(test.name+"/"+render.name, func(t *testing.T) {
				model := testRenderModel()
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

func TestWriteMarkdownPreservesAcceptedPercentEncodedSourceURLHref(t *testing.T) {
	model := testRenderModel()
	model.Sources[0].URL = "https://example.com/a%7Cb%5Cc"
	var markdown bytes.Buffer
	if err := WriteMarkdown(&markdown, model); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}
	var html bytes.Buffer
	if err := goldmark.Convert(markdown.Bytes(), &html); err != nil {
		t.Fatalf("Goldmark conversion: %v", err)
	}
	if !strings.Contains(html.String(), `href="https://example.com/a%7Cb%5Cc"`) {
		t.Fatalf("Markdown changed accepted source URL href: %s", html.String())
	}
}

func TestWriteMarkdownPreservesAmpersandSemanticsThroughGoldmark(t *testing.T) {
	tests := []struct {
		name      string
		title     string
		url       string
		wantTitle string
		wantHref  string
	}{
		{
			name:      "raw ampersands",
			title:     "Research & Development",
			url:       "https://example.com/results?a=1&b=2",
			wantTitle: "Research &amp; Development",
			wantHref:  `href="https://example.com/results?a=1&amp;b=2"`,
		},
		{
			name:      "literal entity text",
			title:     "Research &amp; Development",
			url:       "https://example.com/results?a=1&amp;b=2",
			wantTitle: "Research &amp;amp; Development",
			wantHref:  `href="https://example.com/results?a=1&amp;amp;b=2"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := testRenderModel()
			model.Sources[0].Title = test.title
			model.Sources[0].URL = test.url
			var jsonOutput bytes.Buffer
			if err := WriteJSON(&jsonOutput, model); err != nil {
				t.Fatalf("WriteJSON: %v", err)
			}
			var decoded Model
			if err := json.Unmarshal(jsonOutput.Bytes(), &decoded); err != nil {
				t.Fatalf("decode report JSON: %v", err)
			}
			if decoded.Sources[0].Title != test.title || decoded.Sources[0].URL != test.url {
				t.Fatalf("JSON changed source semantics: %#v", decoded.Sources[0])
			}

			var markdown bytes.Buffer
			if err := WriteMarkdown(&markdown, model); err != nil {
				t.Fatalf("WriteMarkdown: %v", err)
			}
			var rendered bytes.Buffer
			if err := goldmark.Convert(markdown.Bytes(), &rendered); err != nil {
				t.Fatalf("Goldmark conversion: %v", err)
			}
			if !strings.Contains(rendered.String(), test.wantTitle) {
				t.Fatalf("Goldmark changed title semantics: %s", rendered.String())
			}
			if !strings.Contains(rendered.String(), test.wantHref) {
				t.Fatalf("Goldmark changed URL semantics: %s", rendered.String())
			}
		})
	}
}

func testRenderModel() Model {
	return Model{
		InputDigest: string(testInputDigest()),
		Profile:     evidence.ProfileDeep,
		Coverage: validator.Coverage{
			BaselineTotal:         3,
			CompleteTotal:         1,
			MissingCaseIDs:        []string{"case-b", "case-c"},
			UnexpectedCaseIDs:     []string{},
			Families:              []validator.FamilyCoverage{{ID: "coordination", Required: 3, Complete: 1}},
			RequiredPrinciples:    []string{"principle-a"},
			RequiredScenarioLabs:  []string{"lab-a"},
			RequiredPrimitiveLabs: []string{},
			RequiredAdapters:      []string{},
		},
		Rows: []Row{{
			Cell: validator.MatrixCell{
				LabID:            "lab-a",
				RequiredRunID:    "run-a",
				BindingID:        "binding-a",
				ClaimID:          "claim-a",
				Role:             "baseline",
				ImplementationID: "implementation-a",
				AdapterID:        "",
				Workload:         "workload-a",
				Faults:           []string{"fault-a"},
				AssertionIDs:     []string{"assert-a"},
			},
			EvidenceID: "run-20260714T010203.004Z-00000000000000000000000000000001",
			Status:     evidence.StatusPassed,
			Workload:   evidence.Workload{ID: "workload-a", Parameters: map[string]int64{"shards": 2, "requests": 10}},
			Faults:     []evidence.Fault{{ID: "fault-a", At: time.Nanosecond, Duration: 2 * time.Nanosecond}},
			Measurements: map[string]evidence.Measurement{
				"requests.total": {Unit: "requests", Value: 10},
				"latency.millis": {Unit: "ms", Value: 5},
			},
			Assertions:  []evidence.Assertion{{ID: "assert-a", Passed: true, Message: "a|b\nc\\slash"}},
			Environment: evidence.Environment{GoVersion: "go1.26.5", OS: "linux", Arch: "amd64", CPU: "unknown", LogicalCPUs: 8},
			Conclusion:  "Held | bounded\nwithout \\ drift.",
			Limitations: []string{"Synthetic | only.", "No \\ hardware."},
		}},
		Sources:     []SourceLink{{ID: "source-a", Title: "Source A", URL: "https://example.com/source-a"}},
		Diagnostics: []validator.Diagnostic{{Code: validator.CodeInvalidStableID, Severity: "error", Path: "cases/a|b", EntityID: "case-a", Message: "line one\nline two"}},
	}
}

type shortWriter struct{}

func (shortWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	return len(data) - 1, nil
}

func assertGolden(t *testing.T, name string, actual []byte) {
	t.Helper()
	expected, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	if !bytes.Equal(actual, expected) {
		t.Fatalf("%s mismatch\nactual:\n%s\nexpected:\n%s", name, actual, expected)
	}
}
