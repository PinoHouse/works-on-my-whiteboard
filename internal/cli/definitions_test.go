package cli

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/experiments"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/release"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
	"github.com/PinoHouse/works-on-my-whiteboard/labs/harness"
)

func TestResolveDefinitionsBuildsFrozenExpectedCell(t *testing.T) {
	cell := definitionTestCell()
	definition := definitionTestDefinition(cell)
	repository := definitionTestCatalog(cell, definition)
	lookup := func(got validator.MatrixCell) (experiments.Factory, bool) {
		if !reflect.DeepEqual(got, cell) {
			t.Fatalf("lookup cell = %#v; want %#v", got, cell)
		}
		return func(got validator.MatrixCell, profile experiments.Profile) (experiments.Definition, error) {
			if !reflect.DeepEqual(got, cell) || profile != experiments.ProfileSmoke {
				t.Fatalf("factory arguments = %#v, %q", got, profile)
			}
			return definition, nil
		}, true
	}

	resolved, issues := resolveDefinitions(repository, []validator.MatrixCell{cell}, evidence.ProfileSmoke, lookup)
	if len(issues) != 0 {
		t.Fatalf("issues = %#v; want none", issues)
	}
	if len(resolved) != 1 {
		t.Fatalf("resolved count = %d; want 1", len(resolved))
	}
	expected := resolved[0].Expected
	if !reflect.DeepEqual(expected.Cell, cell) || expected.Profile != evidence.ProfileSmoke || expected.Start != definition.Spec.Start || expected.Seed != definition.Spec.Seed || expected.Deadline != definition.Spec.Deadline {
		t.Fatalf("expected identity = %#v", expected)
	}
	if expected.Workload.ID != definition.Workload.ID || !reflect.DeepEqual(expected.Workload.Parameters, definition.Workload.Parameters) {
		t.Fatalf("expected workload = %#v", expected.Workload)
	}
	if !reflect.DeepEqual(expected.Faults, []evidence.Fault{{ID: "fault-one", At: time.Nanosecond, Duration: 2 * time.Nanosecond}}) {
		t.Fatalf("expected faults = %#v", expected.Faults)
	}
	if !reflect.DeepEqual(expected.Measurements, []evidence.MeasurementSpec{{ID: "requests.total", Unit: "count"}}) {
		t.Fatalf("expected measurements = %#v", expected.Measurements)
	}
	if expected.EventsExpected != uint64(len(definition.Spec.Events)) {
		t.Fatalf("events expected = %d; want %d", expected.EventsExpected, len(definition.Spec.Events))
	}
}

func TestResolveDefinitionsRejectsDefinitionContractDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*validator.MatrixCell, *experiments.Definition, *catalog.Catalog)
	}{
		{name: "lab identity", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.LabID = "other-lab"
		}},
		{name: "run identity", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.RequiredRunID = "other-run"
		}},
		{name: "binding identity", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.BindingID = "other-binding"
		}},
		{name: "claim identity", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.ClaimID = "other-claim"
		}},
		{name: "implementation identity", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.ImplementationID = "other-implementation"
		}},
		{name: "adapter identity", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.AdapterID = "other-adapter"
		}},
		{name: "unknown role", mutate: func(cell *validator.MatrixCell, _ *experiments.Definition, _ *catalog.Catalog) {
			cell.Role = "observer"
		}},
		{name: "adapter role missing adapter", mutate: func(cell *validator.MatrixCell, _ *experiments.Definition, _ *catalog.Catalog) { cell.Role = "adapter" }},
		{name: "baseline role with adapter", mutate: func(cell *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			cell.AdapterID = "implementation-one"
			definition.Spec.AdapterID = "implementation-one"
		}},
		{name: "adapter role identity mismatch", mutate: func(cell *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			cell.Role = "adapter"
			cell.AdapterID = "adapter-one"
			definition.Spec.AdapterID = "adapter-one"
		}},
		{name: "invalid stable identity", mutate: func(cell *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			cell.ClaimID = "Claim_One"
			definition.Spec.ClaimID = "Claim_One"
		}},
		{name: "profile", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Profile = experiments.ProfileDeep
		}},
		{name: "workload", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Workload.ID = "other-workload"
		}},
		{name: "spec parameters nil", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.Parameters = nil
		}},
		{name: "workload parameters nil", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Workload.Parameters = nil
		}},
		{name: "parameter key", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Workload.Parameters = map[string]int64{"other": 4}
		}},
		{name: "parameter value", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Workload.Parameters["requests"]++
		}},
		{name: "parameter alias", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Workload.Parameters = definition.Spec.Parameters
		}},
		{name: "invalid parameter key", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.Parameters = map[string]int64{"\xff": 4}
			definition.Workload.Parameters = map[string]int64{"\xff": 4}
		}},
		{name: "unsafe parameter key", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.Parameters = map[string]int64{"/private/tmp/host-only": 4}
			definition.Workload.Parameters = map[string]int64{"/private/tmp/host-only": 4}
		}},
		{name: "encoded unsafe parameter key", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.Parameters = map[string]int64{"%2Fprivate%2Ftmp%2Fhost-only": 4}
			definition.Workload.Parameters = map[string]int64{"%2Fprivate%2Ftmp%2Fhost-only": 4}
		}},
		{name: "Unicode format parameter key", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.Parameters = map[string]int64{"requests\u200bhidden": 4}
			definition.Workload.Parameters = map[string]int64{"requests\u200bhidden": 4}
		}},
		{name: "blank parameter key", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.Parameters = map[string]int64{" ": 4}
			definition.Workload.Parameters = map[string]int64{" ": 4}
		}},
		{name: "faults nil", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Faults = nil
		}},
		{name: "fault ID", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Faults[0].ID = "other-fault"
		}},
		{name: "fault negative at", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Faults[0].At = -1
		}},
		{name: "fault negative duration", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Faults[0].Duration = -1
		}},
		{name: "fault duplicate", mutate: func(cell *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			cell.Faults = append(cell.Faults, cell.Faults[0])
			definition.Faults = append(definition.Faults, definition.Faults[0])
		}},
		{name: "cell faults nil", mutate: func(cell *validator.MatrixCell, _ *experiments.Definition, _ *catalog.Catalog) { cell.Faults = nil }},
		{name: "events nil", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.Events = nil
		}},
		{name: "event action nil", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.Events[0].Apply = nil
		}},
		{name: "assertions nil", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.Assertions = nil
		}},
		{name: "assertion ID", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.Assertions[0].ID = "other-assertion"
		}},
		{name: "assertion check nil", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.Assertions[0].Check = nil
		}},
		{name: "assertion duplicate", mutate: func(cell *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			cell.AssertionIDs = append(cell.AssertionIDs, cell.AssertionIDs[0])
			definition.Spec.Assertions = append(definition.Spec.Assertions, definition.Spec.Assertions[0])
		}},
		{name: "cell assertions nil", mutate: func(cell *validator.MatrixCell, _ *experiments.Definition, _ *catalog.Catalog) {
			cell.AssertionIDs = nil
		}},
		{name: "zero start", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.Start = time.Time{}
		}},
		{name: "non UTC start", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.Start = definition.Spec.Start.In(time.FixedZone("offset", 3600))
		}},
		{name: "deadline", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Spec.Deadline = 0
		}},
		{name: "hypothesis", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Hypothesis = "  "
		}},
		{name: "hypothesis invalid UTF-8", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Hypothesis = "\xff"
		}},
		{name: "hypothesis unsafe dynamic text", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Hypothesis = "host root /absolute/workspace/secret"
		}},
		{name: "hypothesis encoded unsafe dynamic text", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Hypothesis = "host root %2Fprivate%2Ftmp%2Fhost-only"
		}},
		{name: "hypothesis Unicode format text", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Hypothesis = "direction \u202eoverride"
		}},
		{name: "conclusion function", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Conclude = nil
		}},
		{name: "limitations nil", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Limitations = nil
		}},
		{name: "blank limitation", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Limitations[0] = ""
		}},
		{name: "duplicate limitation", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Limitations = append(definition.Limitations, definition.Limitations[0])
		}},
		{name: "unsafe limitation", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Limitations[0] = "host root /absolute/workspace/secret"
		}},
		{name: "measurements nil", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Measurements = nil
		}},
		{name: "measurement duplicate", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, repository *catalog.Catalog) {
			definition.Measurements = append(definition.Measurements, definition.Measurements[0])
			repository.Labs[definition.Spec.LabID] = catalog.LabManifest{ID: definition.Spec.LabID, Metrics: []string{"requests.total", "requests.total"}}
		}},
		{name: "measurement blank unit", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Measurements[0].Unit = ""
		}},
		{name: "measurement padded unit", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Measurements[0].Unit = " count "
		}},
		{name: "measurement unsafe unit", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Measurements[0].Unit = "/private/tmp/host-only"
		}},
		{name: "measurement encoded unsafe unit", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Measurements[0].Unit = "file%3A%2Fprivate%2Ftmp%2Fhost-only"
		}},
		{name: "measurement Unicode format unit", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Measurements[0].Unit = "count\u2066hidden\u2069"
		}},
		{name: "measurement invalid ID", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, repository *catalog.Catalog) {
			definition.Measurements[0].ID = "Requests Total"
			repository.Labs[definition.Spec.LabID] = catalog.LabManifest{ID: definition.Spec.LabID, Metrics: []string{"Requests Total"}}
		}},
		{name: "measurement ID set", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, _ *catalog.Catalog) {
			definition.Measurements[0].ID = "requests.other"
		}},
		{name: "manifest metrics nil", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, repository *catalog.Catalog) {
			repository.Labs[definition.Spec.LabID] = catalog.LabManifest{ID: definition.Spec.LabID, Metrics: nil}
		}},
		{name: "owning lab identity", mutate: func(_ *validator.MatrixCell, definition *experiments.Definition, repository *catalog.Catalog) {
			repository.Labs[definition.Spec.LabID] = catalog.LabManifest{ID: "other-lab", Metrics: []string{"requests.total"}}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cell := definitionTestCell()
			definition := definitionTestDefinition(cell)
			repository := definitionTestCatalog(cell, definition)
			test.mutate(&cell, &definition, repository)
			lookup := func(validator.MatrixCell) (experiments.Factory, bool) {
				return func(validator.MatrixCell, experiments.Profile) (experiments.Definition, error) {
					return definition, nil
				}, true
			}
			resolved, issues := resolveDefinitions(repository, []validator.MatrixCell{cell}, evidence.ProfileSmoke, lookup)
			if len(resolved) != 0 || len(issues) != 1 || issues[0].Code != codeExperimentDefinitionMismatch {
				t.Fatalf("resolved=%#v issues=%#v; want one definition mismatch", resolved, issues)
			}
		})
	}
}

func TestResolveDefinitionsTreatsManifestMetricsAsASet(t *testing.T) {
	cell := definitionTestCell()
	cell.Faults = []string{}
	definition := definitionTestDefinition(cell)
	definition.Faults = []experiments.Fault{}
	definition.Measurements = []experiments.MetricSpec{
		{ID: "requests.allowed", Unit: "count"},
		{ID: "requests.total", Unit: "count"},
	}
	repository := definitionTestCatalog(cell, definition)
	repository.Labs[cell.LabID] = catalog.LabManifest{ID: cell.LabID, Metrics: []string{"requests.total", "requests.allowed"}}
	lookup := func(validator.MatrixCell) (experiments.Factory, bool) {
		return func(validator.MatrixCell, experiments.Profile) (experiments.Definition, error) {
			return definition, nil
		}, true
	}

	resolved, issues := resolveDefinitions(repository, []validator.MatrixCell{cell}, evidence.ProfileSmoke, lookup)
	if len(issues) != 0 || len(resolved) != 1 {
		t.Fatalf("resolved=%#v issues=%#v; metric order must not change the ID set", resolved, issues)
	}
	if got := resolved[0].Expected.Measurements; got[0].ID != "requests.allowed" || got[1].ID != "requests.total" {
		t.Fatalf("definition measurement order was not preserved: %#v", got)
	}
}

func TestResolveDefinitionsDoesNotEchoUnsafeDefinitionText(t *testing.T) {
	tests := []struct {
		name    string
		hostile string
		mutate  func(*experiments.Definition)
	}{
		{name: "hypothesis", hostile: "/private/tmp/hypothesis", mutate: func(definition *experiments.Definition) {
			definition.Hypothesis = "host root /private/tmp/hypothesis"
		}},
		{name: "parameter key", hostile: "/private/tmp/parameter", mutate: func(definition *experiments.Definition) {
			definition.Spec.Parameters = map[string]int64{"/private/tmp/parameter": 4}
			definition.Workload.Parameters = map[string]int64{"/private/tmp/parameter": 4}
		}},
		{name: "measurement unit", hostile: "/private/tmp/unit", mutate: func(definition *experiments.Definition) {
			definition.Measurements[0].Unit = "/private/tmp/unit"
		}},
		{name: "encoded hypothesis", hostile: "%2Fprivate%2Ftmp%2Fhypothesis", mutate: func(definition *experiments.Definition) {
			definition.Hypothesis = "host root %2Fprivate%2Ftmp%2Fhypothesis"
		}},
		{name: "encoded parameter key", hostile: "%2Fprivate%2Ftmp%2Fparameter", mutate: func(definition *experiments.Definition) {
			definition.Spec.Parameters = map[string]int64{"%2Fprivate%2Ftmp%2Fparameter": 4}
			definition.Workload.Parameters = map[string]int64{"%2Fprivate%2Ftmp%2Fparameter": 4}
		}},
		{name: "encoded measurement unit", hostile: "file%3A%2Fprivate%2Ftmp%2Funit", mutate: func(definition *experiments.Definition) {
			definition.Measurements[0].Unit = "file%3A%2Fprivate%2Ftmp%2Funit"
		}},
		{name: "mixed malformed and encoded hypothesis", hostile: "%ZZ", mutate: func(definition *experiments.Definition) {
			definition.Hypothesis = "malformed=%ZZ host root %2Fprivate%2Ftmp%2Fhypothesis"
		}},
		{name: "HTML entity hypothesis", hostile: "&#x2F;", mutate: func(definition *experiments.Definition) {
			definition.Hypothesis = "host root &#x2F;private&#x2F;tmp&#x2F;hypothesis"
		}},
		{name: "HTML entity attempt identity", hostile: "&#x2D;", mutate: func(definition *experiments.Definition) {
			definition.Hypothesis = "attempt run&#x2D;20260714T010203.004Z&#x2D;00000000000000000000000000000009"
		}},
		{name: "Unicode format hypothesis", hostile: "\u202e", mutate: func(definition *experiments.Definition) {
			definition.Hypothesis = "direction \u202eoverride"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cell := definitionTestCell()
			definition := definitionTestDefinition(cell)
			test.mutate(&definition)
			repository := definitionTestCatalog(cell, definition)
			lookup := func(validator.MatrixCell) (experiments.Factory, bool) {
				return func(validator.MatrixCell, experiments.Profile) (experiments.Definition, error) {
					return definition, nil
				}, true
			}
			resolved, issues := resolveDefinitions(repository, []validator.MatrixCell{cell}, evidence.ProfileSmoke, lookup)
			if len(resolved) != 0 || len(issues) != 1 {
				t.Fatalf("resolve = %#v / %#v, want one issue", resolved, issues)
			}
			if strings.Contains(issues[0].Message, test.hostile) {
				t.Fatalf("definition issue echoed hostile text: %#v", issues[0])
			}
		})
	}
}

func TestResolveDefinitionsRequiresOrderedFaultsAndAssertions(t *testing.T) {
	cell := definitionTestCell()
	cell.Faults = []string{"fault-one", "fault-two"}
	cell.AssertionIDs = []string{"assertion-one", "assertion-two"}
	base := definitionTestDefinition(cell)
	base.Faults = []experiments.Fault{
		{ID: "fault-one", At: time.Nanosecond, Duration: 2 * time.Nanosecond},
		{ID: "fault-two", At: 3 * time.Nanosecond, Duration: 4 * time.Nanosecond},
	}
	base.Spec.Assertions = []harness.Assertion{
		{ID: "assertion-one", Check: func(harness.Snapshot) (bool, string) { return true, "ok" }},
		{ID: "assertion-two", Check: func(harness.Snapshot) (bool, string) { return true, "ok" }},
	}
	tests := []struct {
		name   string
		mutate func(*experiments.Definition)
	}{
		{name: "fault order", mutate: func(definition *experiments.Definition) {
			definition.Faults[0], definition.Faults[1] = definition.Faults[1], definition.Faults[0]
		}},
		{name: "assertion order", mutate: func(definition *experiments.Definition) {
			definition.Spec.Assertions[0], definition.Spec.Assertions[1] = definition.Spec.Assertions[1], definition.Spec.Assertions[0]
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := cloneExperimentDefinition(base)
			test.mutate(&definition)
			repository := definitionTestCatalog(cell, definition)
			lookup := func(validator.MatrixCell) (experiments.Factory, bool) {
				return func(validator.MatrixCell, experiments.Profile) (experiments.Definition, error) {
					return definition, nil
				}, true
			}
			resolved, issues := resolveDefinitions(repository, []validator.MatrixCell{cell}, evidence.ProfileSmoke, lookup)
			if len(resolved) != 0 || len(issues) != 1 || issues[0].Code != codeExperimentDefinitionMismatch {
				t.Fatalf("resolved=%#v issues=%#v", resolved, issues)
			}
		})
	}
}

func TestResolveDefinitionsClassifiesLookupAndDefinitionFailures(t *testing.T) {
	cell := definitionTestCell()
	definition := definitionTestDefinition(cell)
	repository := definitionTestCatalog(cell, definition)
	tests := []struct {
		name     string
		profile  evidence.Profile
		lookup   experimentLookup
		wantCode string
	}{
		{name: "nil lookup", profile: evidence.ProfileSmoke, wantCode: codeExperimentRegistryMissing},
		{name: "missing lookup entry", profile: evidence.ProfileSmoke, lookup: func(validator.MatrixCell) (experiments.Factory, bool) {
			return nil, false
		}, wantCode: codeExperimentRegistryMissing},
		{name: "nil factory", profile: evidence.ProfileSmoke, lookup: func(validator.MatrixCell) (experiments.Factory, bool) {
			return nil, true
		}, wantCode: codeExperimentDefinitionMismatch},
		{name: "factory error", profile: evidence.ProfileSmoke, lookup: func(validator.MatrixCell) (experiments.Factory, bool) {
			return func(validator.MatrixCell, experiments.Profile) (experiments.Definition, error) {
				return experiments.Definition{}, errors.New("factory failed")
			}, true
		}, wantCode: codeExperimentDefinitionMismatch},
		{name: "unknown profile", profile: "benchmark", lookup: func(validator.MatrixCell) (experiments.Factory, bool) {
			return func(validator.MatrixCell, experiments.Profile) (experiments.Definition, error) {
				return definition, nil
			}, true
		}, wantCode: codeExperimentDefinitionMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolved, issues := resolveDefinitions(repository, []validator.MatrixCell{cell}, test.profile, test.lookup)
			if len(resolved) != 0 || len(issues) != 1 || issues[0].Code != test.wantCode {
				t.Fatalf("resolved=%#v issues=%#v; want %q", resolved, issues, test.wantCode)
			}
		})
	}
}

func TestDefinitionIssueMessagesDoNotEchoPaths(t *testing.T) {
	cell := definitionTestCell()
	cell.LabID = "/Users/private/workspace"
	resolved, issues := resolveDefinitions(nil, []validator.MatrixCell{cell}, evidence.ProfileSmoke, nil)
	if len(resolved) != 0 || len(issues) != 1 {
		t.Fatalf("resolved=%#v issues=%#v", resolved, issues)
	}
	if strings.Contains(issues[0].Message, "/Users") || strings.Contains(issues[0].Message, "workspace") {
		t.Fatalf("definition issue leaked a path: %q", issues[0].Message)
	}
}

func TestResolveDefinitionsContinuesAfterIndependentFailures(t *testing.T) {
	missingLab := renamedDefinitionTestCell("missing-lab")
	missingRegistry := renamedDefinitionTestCell("missing-registry")
	factoryFailure := renamedDefinitionTestCell("factory-failure")
	valid := renamedDefinitionTestCell("valid")
	repository := &catalog.Catalog{Labs: map[string]catalog.LabManifest{
		missingRegistry.LabID: {ID: missingRegistry.LabID, Metrics: []string{"requests.total"}},
		factoryFailure.LabID:  {ID: factoryFailure.LabID, Metrics: []string{"requests.total"}},
		valid.LabID:           {ID: valid.LabID, Metrics: []string{"requests.total"}},
	}}
	lookup := func(cell validator.MatrixCell) (experiments.Factory, bool) {
		switch cell.LabID {
		case missingRegistry.LabID:
			return nil, false
		case factoryFailure.LabID:
			return func(validator.MatrixCell, experiments.Profile) (experiments.Definition, error) {
				return experiments.Definition{}, errors.New("factory failed at /Users/private/workspace")
			}, true
		case valid.LabID:
			definition := definitionTestDefinition(valid)
			return func(validator.MatrixCell, experiments.Profile) (experiments.Definition, error) {
				return definition, nil
			}, true
		default:
			t.Fatalf("unexpected lookup for %#v", cell)
			return nil, false
		}
	}
	matrix := []validator.MatrixCell{missingLab, missingRegistry, factoryFailure, valid}
	resolved, issues := resolveDefinitions(repository, matrix, evidence.ProfileSmoke, lookup)
	if len(resolved) != 1 || resolved[0].Expected.Cell.LabID != valid.LabID {
		t.Fatalf("resolved = %#v; want only valid cell", resolved)
	}
	wantCodes := []string{codeExperimentDefinitionMismatch, codeExperimentRegistryMissing, codeExperimentDefinitionMismatch}
	if len(issues) != len(wantCodes) {
		t.Fatalf("issues = %#v", issues)
	}
	for index, code := range wantCodes {
		if issues[index].Code != code || strings.Contains(issues[index].Message, "/Users") {
			t.Fatalf("issue %d = %#v; want code %q without path", index, issues[index], code)
		}
	}
}

func TestResolvedDefinitionsIsolateFactoryExpectedAndExecutableState(t *testing.T) {
	cell := definitionTestCell()
	definition := definitionTestDefinition(cell)
	repository := definitionTestCatalog(cell, definition)
	lookup := func(validator.MatrixCell) (experiments.Factory, bool) {
		return func(validator.MatrixCell, experiments.Profile) (experiments.Definition, error) {
			return definition, nil
		}, true
	}
	resolved, issues := resolveDefinitions(repository, []validator.MatrixCell{cell}, evidence.ProfileSmoke, lookup)
	if len(issues) != 0 || len(resolved) != 1 {
		t.Fatalf("resolve = %#v / %#v", resolved, issues)
	}
	value := &resolved[0]
	if mapsAlias(value.Definition.Spec.Parameters, value.Definition.Workload.Parameters) || mapsAlias(value.Definition.Spec.Parameters, value.Expected.Workload.Parameters) || mapsAlias(value.Definition.Workload.Parameters, value.Expected.Workload.Parameters) {
		t.Fatal("resolved parameter maps alias")
	}

	definition.Spec.Parameters["requests"] = 99
	definition.Workload.Parameters["requests"] = 98
	definition.Spec.Events[0].Name = "mutated-source-event"
	definition.Spec.Assertions[0].ID = "mutated-source-assertion"
	definition.Faults[0].At = 99
	definition.Measurements[0].Unit = "mutated-source-unit"
	definition.Limitations[0] = "mutated source limitation"
	cell.Faults[0] = "mutated-source-fault"
	cell.AssertionIDs[0] = "mutated-source-assertion"
	if value.Definition.Spec.Parameters["requests"] != 4 || value.Definition.Workload.Parameters["requests"] != 4 || value.Expected.Workload.Parameters["requests"] != 4 ||
		value.Definition.Spec.Events[0].Name != "request-one" || value.Definition.Spec.Assertions[0].ID != "assertion-one" ||
		value.Expected.Faults[0].At != time.Nanosecond || value.Expected.Measurements[0].Unit != "count" || value.Expected.Limitations[0] != "single deterministic workload" ||
		value.Expected.Cell.Faults[0] != "fault-one" || value.Expected.Cell.AssertionIDs[0] != "assertion-one" {
		t.Fatalf("factory mutation leaked into resolved value: %#v", value)
	}

	executable := cloneExecutableSpec(value.Definition.Spec)
	executable.Parameters["requests"] = 77
	executable.Events[0].Name = "runner-mutated-event"
	executable.Assertions[0].ID = "runner-mutated-assertion"
	if value.Definition.Spec.Parameters["requests"] != 4 || value.Definition.Spec.Events[0].Name != "request-one" || value.Definition.Spec.Assertions[0].ID != "assertion-one" {
		t.Fatalf("executable clone mutation leaked into frozen definition: %#v", value.Definition.Spec)
	}

	value.Definition.Spec.Parameters["requests"] = 66
	value.Definition.Faults[0].At = 66
	value.Definition.Measurements[0].Unit = "requests"
	value.Definition.Limitations[0] = "mutated resolved definition"
	if value.Expected.Workload.Parameters["requests"] != 4 || value.Expected.Faults[0].At != time.Nanosecond || value.Expected.Measurements[0].Unit != "count" || value.Expected.Limitations[0] != "single deterministic workload" {
		t.Fatalf("resolved definition aliases expected: %#v", value.Expected)
	}
}

func TestResolveDefinitionsKeepsPrimitiveAndScenarioUnitAuthoritiesSeparate(t *testing.T) {
	repository, err := catalog.LoadDir(context.Background(), "../..")
	if err != nil {
		t.Fatal(err)
	}
	validation := validator.Validate(repository, validator.ModeDevelopment)
	if len(validation.Diagnostics) != 0 {
		t.Fatalf("development diagnostics = %#v", validation.Diagnostics)
	}
	for _, profile := range []evidence.Profile{evidence.ProfileSmoke, evidence.ProfileDeep} {
		resolved, issues := resolveDefinitions(repository, validation.Matrix, profile, experiments.Lookup)
		if len(issues) != 0 || len(resolved) != 6 {
			t.Fatalf("resolve six %s cells = %d / %#v", profile, len(resolved), issues)
		}
		units := make(map[string]string)
		for _, item := range resolved {
			for _, measurement := range item.Expected.Measurements {
				if measurement.ID == "requests.total" {
					units[item.Expected.Cell.LabID] = measurement.Unit
				}
			}
		}
		if units["token-bucket"] != "count" || units["distributed-rate-limiter"] != "requests" {
			t.Fatalf("%s requests.total units = %#v", profile, units)
		}
	}
}

func renamedDefinitionTestCell(suffix string) validator.MatrixCell {
	cell := definitionTestCell()
	cell.LabID = "lab-" + suffix
	cell.RequiredRunID = "run-" + suffix
	cell.BindingID = "binding-" + suffix
	cell.ClaimID = "claim-" + suffix
	cell.ImplementationID = "implementation-" + suffix
	return cell
}

func TestConvertRunResultSealsPassedEvidence(t *testing.T) {
	resolved := resolveDefinitionTestFixture(t)
	result := definitionTestPassedResult(resolved)
	prepared, err := prepareRunResult(resolved, result, nil)
	if err != nil {
		t.Fatalf("prepareRunResult() error = %v", err)
	}
	identity := definitionTestRecordIdentity()
	record, err := convertPreparedRunResult(resolved, result, prepared, identity)
	if err != nil {
		t.Fatalf("convertPreparedRunResult() error = %v", err)
	}
	if _, err := evidence.Encode(record); err != nil {
		t.Fatalf("converted record is not a strict sealed value: %v", err)
	}
	if record.Status != evidence.StatusPassed || record.EventsExecuted != resolved.Expected.EventsExpected || record.StartedAt != resolved.Expected.Start {
		t.Fatalf("record result identity = %#v", record)
	}
	if record.ID != identity.ID || record.RunSetID != identity.RunSetID || record.SourceCommit != identity.SourceState.SourceCommit || record.InputDigest != identity.SourceState.InputDigest || record.Environment != identity.Environment {
		t.Fatalf("record source state = %q / %q", record.SourceCommit, record.InputDigest)
	}
	cell := resolved.Expected.Cell
	if record.LabID != cell.LabID || record.RequiredRunID != cell.RequiredRunID || record.BindingID != cell.BindingID || record.ClaimID != cell.ClaimID || record.ImplementationID != cell.ImplementationID || record.AdapterID != cell.AdapterID ||
		record.Role != evidence.RoleBaseline || record.Profile != resolved.Expected.Profile {
		t.Fatalf("record cell identity = %#v", record)
	}
	if record.Hypothesis != resolved.Expected.Hypothesis || !reflect.DeepEqual(record.Workload, resolved.Expected.Workload) || !reflect.DeepEqual(record.Faults, resolved.Expected.Faults) ||
		record.Seed != resolved.Expected.Seed || record.Deadline != resolved.Expected.Deadline || record.StartedAt != result.StartedAt || record.FinishedAt != result.FinishedAt ||
		!reflect.DeepEqual(record.Parameters, resolved.Definition.Spec.Parameters) || !reflect.DeepEqual(record.Limitations, resolved.Expected.Limitations) {
		t.Fatalf("record frozen contract = %#v", record)
	}
	if got := record.Measurements["requests.total"]; got != (evidence.Measurement{Unit: "count", Value: 4}) {
		t.Fatalf("measurement = %#v", got)
	}
	if len(record.Assertions) != 1 || record.Assertions[0].ID != "assertion-one" || !record.Assertions[0].Passed || record.Conclusion != "passed conclusion" {
		t.Fatalf("assertions/conclusion = %#v / %q", record.Assertions, record.Conclusion)
	}
}

func TestPrepareThenConvertInvokesConclusionExactlyOnce(t *testing.T) {
	calls := 0
	resolved := resolveDefinitionTestFixtureWithMutation(t, func(definition *experiments.Definition) {
		definition.Conclude = func(result harness.RunResult) string {
			calls++
			if calls != 1 || result.Status != harness.StatusPassed {
				return ""
			}
			return "single conclusion"
		}
	})
	result := definitionTestPassedResult(resolved)
	prepared, err := prepareRunResult(resolved, result, nil)
	if err != nil {
		t.Fatalf("prepareRunResult() error = %v", err)
	}
	record, err := convertPreparedRunResult(resolved, result, prepared, definitionTestRecordIdentity())
	if err != nil {
		t.Fatalf("convertPreparedRunResult() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("Conclude calls = %d; want 1", calls)
	}
	if record.Conclusion != "single conclusion" {
		t.Fatalf("record conclusion = %q", record.Conclusion)
	}
}

func TestConvertRunResultConvenienceInvokesConclusionExactlyOnce(t *testing.T) {
	calls := 0
	resolved := resolveDefinitionTestFixtureWithMutation(t, func(definition *experiments.Definition) {
		definition.Conclude = func(harness.RunResult) string {
			calls++
			if calls != 1 {
				return ""
			}
			return "single conclusion"
		}
	})
	result := definitionTestPassedResult(resolved)
	if _, err := convertRunResult(resolved, result, nil, definitionTestRecordIdentity()); err != nil {
		t.Fatalf("convertRunResult() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("Conclude calls = %d; want 1", calls)
	}
}

func TestPrepareRunResultIsolatesAndRejectsConclusionInputMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*harness.RunResult)
	}{
		{name: "metrics", mutate: func(result *harness.RunResult) { result.Metrics[0].Value = 99 }},
		{name: "assertions", mutate: func(result *harness.RunResult) { result.Assertions[0].Message = "mutated" }},
		{name: "diagnostics", mutate: func(result *harness.RunResult) { result.Diagnostics[0].Message = "mutated" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			resolved := resolveDefinitionTestFixtureWithMutation(t, func(definition *experiments.Definition) {
				definition.Conclude = func(result harness.RunResult) string {
					calls++
					test.mutate(&result)
					return "mutating conclusion"
				}
			})
			result := definitionTestPassedResult(resolved)
			result.Diagnostics = []harness.Diagnostic{{Event: "request-one", Message: "original"}}
			before := cloneRunResultForTest(result)

			if _, err := prepareRunResult(resolved, result, nil); err == nil {
				t.Fatal("Conclude mutation was accepted")
			}
			if calls != 1 {
				t.Fatalf("Conclude calls = %d; want 1", calls)
			}
			if !reflect.DeepEqual(result, before) {
				t.Fatalf("Conclude mutated caller result: got %#v want %#v", result, before)
			}
		})
	}
}

func TestPrepareRunResultRevalidatesDefinitionAfterConclusion(t *testing.T) {
	var target *resolvedDefinition
	calls := 0
	resolved := resolveDefinitionTestFixtureWithMutation(t, func(definition *experiments.Definition) {
		definition.Conclude = func(harness.RunResult) string {
			calls++
			target.Definition.Spec.Events[0].Name = "mutated-by-conclusion"
			return "mutating definition conclusion"
		}
	})
	target = &resolved
	result := definitionTestPassedResult(resolved)
	before := cloneRunResultForTest(result)

	if _, err := prepareRunResult(resolved, result, nil); err == nil {
		t.Fatal("definition mutation performed by Conclude was accepted")
	}
	if calls != 1 {
		t.Fatalf("Conclude calls = %d; want 1", calls)
	}
	if target.Definition.Spec.Events[0].Name != "mutated-by-conclusion" {
		t.Fatal("Conclude did not exercise the shared executable definition")
	}
	if !reflect.DeepEqual(result, before) {
		t.Fatalf("Conclude polluted caller result: got %#v want %#v", result, before)
	}
}

func TestPrepareRunResultRejectsFrozenInvalidConclusion(t *testing.T) {
	tests := []struct {
		name       string
		conclusion string
	}{
		{name: "blank", conclusion: " "},
		{name: "invalid UTF-8", conclusion: "\xff"},
		{name: "absolute path", conclusion: "result at /absolute/workspace/secret"},
		{name: "generated identity", conclusion: "result from run-20260714T010203.004Z-00000000000000000000000000000001"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			resolved := resolveDefinitionTestFixtureWithMutation(t, func(definition *experiments.Definition) {
				definition.Conclude = func(harness.RunResult) string {
					calls++
					return test.conclusion
				}
			})
			_, err := prepareRunResult(resolved, definitionTestPassedResult(resolved), nil)
			if err == nil {
				t.Fatal("invalid frozen conclusion was accepted")
			}
			if test.name == "absolute path" || test.name == "generated identity" {
				if !errors.Is(err, release.ErrUnsafeDynamicText) {
					t.Fatalf("prepareRunResult error = %v, want ErrUnsafeDynamicText", err)
				}
			}
			if calls != 1 {
				t.Fatalf("Conclude calls = %d; want 1", calls)
			}
		})
	}
}

func TestConvertPreparedRunResultRejectsMutationAfterPreparation(t *testing.T) {
	t.Run("result", func(t *testing.T) {
		resolved := resolveDefinitionTestFixture(t)
		result := definitionTestPassedResult(resolved)
		prepared, err := prepareRunResult(resolved, result, nil)
		if err != nil {
			t.Fatal(err)
		}
		result.Metrics[0].Value++
		if _, err := convertPreparedRunResult(resolved, result, prepared, definitionTestRecordIdentity()); err == nil {
			t.Fatal("mutated run result was accepted")
		}
	})

	t.Run("prepared evidence", func(t *testing.T) {
		resolved := resolveDefinitionTestFixture(t)
		result := definitionTestPassedResult(resolved)
		prepared, err := prepareRunResult(resolved, result, nil)
		if err != nil {
			t.Fatal(err)
		}
		prepared.Measurements["requests.total"] = evidence.Measurement{Unit: "count", Value: 99}
		if _, err := convertPreparedRunResult(resolved, result, prepared, definitionTestRecordIdentity()); err == nil {
			t.Fatal("mutated prepared evidence was accepted")
		}
	})
}

func TestConvertPreparedRunResultRejectsInvalidExplicitIdentity(t *testing.T) {
	resolved := resolveDefinitionTestFixture(t)
	result := definitionTestPassedResult(resolved)
	prepared, err := prepareRunResult(resolved, result, nil)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*recordIdentity)
	}{
		{name: "attempt ID", mutate: func(identity *recordIdentity) { identity.ID = "" }},
		{name: "run-set ID", mutate: func(identity *recordIdentity) { identity.RunSetID = "" }},
		{name: "source digest", mutate: func(identity *recordIdentity) { identity.SourceState.InputDigest = "" }},
		{name: "source commit", mutate: func(identity *recordIdentity) { identity.SourceState.SourceCommit = "" }},
		{name: "environment", mutate: func(identity *recordIdentity) { identity.Environment.OS = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity := definitionTestRecordIdentity()
			test.mutate(&identity)
			if _, err := convertPreparedRunResult(resolved, result, prepared, identity); err == nil {
				t.Fatal("invalid explicit identity was accepted")
			}
		})
	}
}

func TestValidateRunResultEnforcesStatusMetricAndAssertionClosure(t *testing.T) {
	baseResolved := resolveDefinitionTestFixture(t)
	baseResult := definitionTestPassedResult(baseResolved)
	tests := []struct {
		name      string
		mutate    func(*resolvedDefinition, *harness.RunResult)
		runnerErr error
		wantError bool
	}{
		{name: "passed", runnerErr: nil},
		{name: "failed assertion", mutate: func(_ *resolvedDefinition, result *harness.RunResult) {
			result.Status = harness.StatusFailed
			result.Assertions[0].Passed = false
			result.Assertions[0].Message = "assertion failed"
			result.Diagnostics = []harness.Diagnostic{{Event: "assertion-one", Message: "assertion failed"}}
		}, runnerErr: errors.New("one or more assertions failed")},
		{name: "failed without diagnostics", mutate: func(_ *resolvedDefinition, result *harness.RunResult) {
			result.Status = harness.StatusFailed
			result.Assertions[0].Passed = false
			result.Diagnostics = []harness.Diagnostic{}
		}, runnerErr: errors.New("runner failed")},
		{name: "passed with explicit diagnostic", mutate: func(_ *resolvedDefinition, result *harness.RunResult) {
			result.Diagnostics = []harness.Diagnostic{{Event: "request-one", Message: "informational diagnostic"}}
		}},
		{name: "passed with error", runnerErr: errors.New("unexpected"), wantError: true},
		{name: "failed without error", mutate: func(_ *resolvedDefinition, result *harness.RunResult) { result.Status = harness.StatusFailed }, wantError: true},
		{name: "unknown status", mutate: func(_ *resolvedDefinition, result *harness.RunResult) { result.Status = "skipped" }, runnerErr: errors.New("unexpected"), wantError: true},
		{name: "passed short event count", mutate: func(_ *resolvedDefinition, result *harness.RunResult) { result.EventsExecuted-- }, wantError: true},
		{name: "failed excess event count", mutate: func(resolved *resolvedDefinition, result *harness.RunResult) {
			result.Status = harness.StatusFailed
			result.EventsExecuted = resolved.Expected.EventsExpected + 1
		}, runnerErr: errors.New("runner failed"), wantError: true},
		{name: "start drift", mutate: func(_ *resolvedDefinition, result *harness.RunResult) {
			result.StartedAt = result.StartedAt.Add(time.Nanosecond)
		}, wantError: true},
		{name: "finish before start", mutate: func(_ *resolvedDefinition, result *harness.RunResult) {
			result.FinishedAt = result.StartedAt.Add(-time.Nanosecond)
		}, wantError: true},
		{name: "finish zero", mutate: func(_ *resolvedDefinition, result *harness.RunResult) {
			result.FinishedAt = time.Time{}
		}, wantError: true},
		{name: "finish non UTC", mutate: func(_ *resolvedDefinition, result *harness.RunResult) {
			result.FinishedAt = result.FinishedAt.In(time.FixedZone("offset", 3600))
		}, wantError: true},
		{name: "metrics nil", mutate: func(_ *resolvedDefinition, result *harness.RunResult) { result.Metrics = nil }, wantError: true},
		{name: "metric missing", mutate: func(_ *resolvedDefinition, result *harness.RunResult) { result.Metrics = []harness.Metric{} }, wantError: true},
		{name: "metric extra", mutate: func(_ *resolvedDefinition, result *harness.RunResult) {
			result.Metrics = append(result.Metrics, harness.Metric{Name: "requests.extra", Unit: "count"})
		}, wantError: true},
		{name: "metric duplicate", mutate: func(_ *resolvedDefinition, result *harness.RunResult) {
			result.Metrics = append(result.Metrics, result.Metrics[0])
		}, wantError: true},
		{name: "metric unit", mutate: func(_ *resolvedDefinition, result *harness.RunResult) { result.Metrics[0].Unit = "requests" }, wantError: true},
		{name: "assertions nil", mutate: func(_ *resolvedDefinition, result *harness.RunResult) { result.Assertions = nil }, wantError: true},
		{name: "assertion missing", mutate: func(_ *resolvedDefinition, result *harness.RunResult) {
			result.Assertions = []harness.AssertionResult{}
		}, wantError: true},
		{name: "assertion extra", mutate: func(_ *resolvedDefinition, result *harness.RunResult) {
			result.Assertions = append(result.Assertions, harness.AssertionResult{ID: "assertion-extra", Passed: true})
		}, wantError: true},
		{name: "assertion duplicate", mutate: func(_ *resolvedDefinition, result *harness.RunResult) {
			result.Assertions = append(result.Assertions, result.Assertions[0])
		}, wantError: true},
		{name: "assertion ID", mutate: func(_ *resolvedDefinition, result *harness.RunResult) { result.Assertions[0].ID = "assertion-other" }, wantError: true},
		{name: "passed false assertion", mutate: func(_ *resolvedDefinition, result *harness.RunResult) { result.Assertions[0].Passed = false }, wantError: true},
		{name: "assertion message invalid UTF-8", mutate: func(_ *resolvedDefinition, result *harness.RunResult) {
			result.Assertions[0].Message = "\xff"
		}, wantError: true},
		{name: "assertion message unsafe dynamic text", mutate: func(_ *resolvedDefinition, result *harness.RunResult) {
			result.Assertions[0].Message = `host result C:\Users\runner\AppData\Local\Temp\result.json`
		}, wantError: true},
		{name: "assertion message HTML entity path", mutate: func(_ *resolvedDefinition, result *harness.RunResult) {
			result.Assertions[0].Message = "host root &#x2F;private&#x2F;tmp&#x2F;result.json"
		}, wantError: true},
		{name: "passed diagnostics nil", mutate: func(_ *resolvedDefinition, result *harness.RunResult) { result.Diagnostics = nil }, wantError: true},
		{name: "diagnostic event invalid", mutate: func(_ *resolvedDefinition, result *harness.RunResult) {
			result.Diagnostics = []harness.Diagnostic{{Event: "Request One", Message: "failed"}}
		}, wantError: true},
		{name: "diagnostic message blank", mutate: func(_ *resolvedDefinition, result *harness.RunResult) {
			result.Diagnostics = []harness.Diagnostic{{Event: "request-one", Message: " "}}
		}, wantError: true},
		{name: "diagnostic message unsafe dynamic text", mutate: func(_ *resolvedDefinition, result *harness.RunResult) {
			result.Diagnostics = []harness.Diagnostic{{Event: "request-one", Message: "host root /absolute/workspace/secret"}}
		}, wantError: true},
		{name: "synthesized runner diagnostic unsafe dynamic text", mutate: func(_ *resolvedDefinition, result *harness.RunResult) {
			result.Status = harness.StatusFailed
			result.Assertions[0].Passed = false
			result.Diagnostics = []harness.Diagnostic{}
		}, runnerErr: errors.New("runner failed at /absolute/workspace/secret"), wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolved := baseResolved
			resolved.Expected = cloneExpectedCellForTest(baseResolved.Expected)
			resolved.Definition = cloneExperimentDefinition(baseResolved.Definition)
			result := cloneRunResultForTest(baseResult)
			if test.mutate != nil {
				test.mutate(&resolved, &result)
			}
			err := validateRunResult(resolved, result, test.runnerErr)
			if (err != nil) != test.wantError {
				t.Fatalf("validateRunResult() error = %v; wantError=%t", err, test.wantError)
			}
		})
	}
}

func TestPrepareRunResultAllowsPortableMultilineDynamicText(t *testing.T) {
	resolved := resolveDefinitionTestFixtureWithMutation(t, func(definition *experiments.Definition) {
		definition.Conclude = func(harness.RunResult) string {
			return "The bound held.\nThe retry path was exercised."
		}
	})
	result := definitionTestPassedResult(resolved)
	result.Assertions[0].Message = "first observation\nsecond observation"
	prepared, err := prepareRunResult(resolved, result, nil)
	if err != nil {
		t.Fatalf("prepareRunResult rejected portable multiline text: %v", err)
	}
	if _, err := convertPreparedRunResult(resolved, result, prepared, definitionTestRecordIdentity()); err != nil {
		t.Fatalf("convertPreparedRunResult rejected portable multiline text: %v", err)
	}
}

func TestValidateRunResultUsesMetricSetsAndOrderedAssertions(t *testing.T) {
	cell := definitionTestCell()
	cell.AssertionIDs = []string{"assertion-one", "assertion-two"}
	definition := definitionTestDefinition(cell)
	definition.Measurements = []experiments.MetricSpec{
		{ID: "requests.total", Unit: "count"},
		{ID: "requests.allowed", Unit: "count"},
	}
	definition.Spec.Assertions = []harness.Assertion{
		{ID: "assertion-one", Check: func(harness.Snapshot) (bool, string) { return true, "ok" }},
		{ID: "assertion-two", Check: func(harness.Snapshot) (bool, string) { return true, "ok" }},
	}
	repository := definitionTestCatalog(cell, definition)
	lookup := func(validator.MatrixCell) (experiments.Factory, bool) {
		return func(validator.MatrixCell, experiments.Profile) (experiments.Definition, error) {
			return definition, nil
		}, true
	}
	values, issues := resolveDefinitions(repository, []validator.MatrixCell{cell}, evidence.ProfileSmoke, lookup)
	if len(issues) != 0 || len(values) != 1 {
		t.Fatalf("resolve = %#v / %#v", values, issues)
	}
	resolved := values[0]
	result := harness.RunResult{
		Status:         harness.StatusPassed,
		StartedAt:      resolved.Expected.Start,
		FinishedAt:     resolved.Expected.Start.Add(time.Nanosecond),
		EventsExecuted: resolved.Expected.EventsExpected,
		Metrics: []harness.Metric{
			{Name: "requests.allowed", Unit: "count", Value: 3},
			{Name: "requests.total", Unit: "count", Value: 4},
		},
		Assertions: []harness.AssertionResult{
			{ID: "assertion-one", Passed: true, Message: "ok"},
			{ID: "assertion-two", Passed: true, Message: "ok"},
		},
		Diagnostics: []harness.Diagnostic{},
	}
	if err := validateRunResult(resolved, result, nil); err != nil {
		t.Fatalf("reordered metric set was rejected: %v", err)
	}

	assertionsReordered := cloneRunResultForTest(result)
	assertionsReordered.Assertions[0], assertionsReordered.Assertions[1] = assertionsReordered.Assertions[1], assertionsReordered.Assertions[0]
	if err := validateRunResult(resolved, assertionsReordered, nil); err == nil {
		t.Fatal("reordered assertions were accepted")
	}
	metricsDuplicated := cloneRunResultForTest(result)
	metricsDuplicated.Metrics[1] = metricsDuplicated.Metrics[0]
	if err := validateRunResult(resolved, metricsDuplicated, nil); err == nil {
		t.Fatal("same-size duplicated metric set was accepted")
	}
}

func TestConvertRunResultSealsFailedAssertionAndSynthesizesDiagnostic(t *testing.T) {
	resolved := resolveDefinitionTestFixture(t)
	result := definitionTestPassedResult(resolved)
	result.Status = harness.StatusFailed
	result.Assertions[0].Passed = false
	result.Assertions[0].Message = "quota assertion failed"
	result.Diagnostics = nil
	runnerErr := errors.New("one or more assertions failed")

	record, err := convertRunResult(resolved, result, runnerErr, definitionTestRecordIdentity())
	if err != nil {
		t.Fatalf("convertRunResult() error = %v", err)
	}
	if _, err := evidence.Encode(record); err != nil {
		t.Fatalf("failed record is not sealed: %v", err)
	}
	if record.Status != evidence.StatusFailed || record.Assertions[0].Passed {
		t.Fatalf("failed record status/assertion = %q / %#v", record.Status, record.Assertions)
	}
	want := []evidence.Diagnostic{{Code: "experiment_execution_failed", Event: "", Message: runnerErr.Error()}}
	if !reflect.DeepEqual(record.Diagnostics, want) || record.Conclusion != "failed conclusion" {
		t.Fatalf("failed record diagnostics/conclusion = %#v / %q", record.Diagnostics, record.Conclusion)
	}
}

func TestValidateRunResultRejectsEarlyRunnerFailureWithoutClosure(t *testing.T) {
	resolved := resolveDefinitionTestFixture(t)
	result := definitionTestPassedResult(resolved)
	result.Status = harness.StatusFailed
	result.EventsExecuted = 1
	result.Metrics = []harness.Metric{}
	result.Assertions = []harness.AssertionResult{}
	result.Diagnostics = []harness.Diagnostic{{Event: "request-one", Message: "event failed"}}
	if err := validateRunResult(resolved, result, errors.New("event failed")); err == nil {
		t.Fatal("early failure without metric/assertion closure was accepted")
	}
}

func TestValidateRunResultRechecksFrozenDefinitionBeforeConversion(t *testing.T) {
	base := resolveDefinitionTestFixture(t)
	mutations := []struct {
		name   string
		mutate func(*resolvedDefinition)
	}{
		{name: "cell identity", mutate: func(value *resolvedDefinition) { value.Expected.Cell.ClaimID = "other-claim" }},
		{name: "role", mutate: func(value *resolvedDefinition) { value.Expected.Cell.Role = "variant" }},
		{name: "profile", mutate: func(value *resolvedDefinition) { value.Expected.Profile = evidence.ProfileDeep }},
		{name: "start", mutate: func(value *resolvedDefinition) { value.Expected.Start = value.Expected.Start.Add(time.Nanosecond) }},
		{name: "workload ID", mutate: func(value *resolvedDefinition) { value.Expected.Workload.ID = "other-workload" }},
		{name: "workload parameter", mutate: func(value *resolvedDefinition) { value.Expected.Workload.Parameters["requests"]++ }},
		{name: "fault", mutate: func(value *resolvedDefinition) { value.Expected.Faults[0].At++ }},
		{name: "seed", mutate: func(value *resolvedDefinition) { value.Expected.Seed++ }},
		{name: "deadline", mutate: func(value *resolvedDefinition) { value.Expected.Deadline++ }},
		{name: "hypothesis", mutate: func(value *resolvedDefinition) { value.Expected.Hypothesis = "other hypothesis" }},
		{name: "limitation", mutate: func(value *resolvedDefinition) { value.Expected.Limitations[0] = "other limitation" }},
		{name: "measurement unit", mutate: func(value *resolvedDefinition) { value.Expected.Measurements[0].Unit = "requests" }},
		{name: "event count", mutate: func(value *resolvedDefinition) { value.Expected.EventsExpected++ }},
		{name: "definition identity", mutate: func(value *resolvedDefinition) { value.Definition.Spec.BindingID = "other-binding" }},
		{name: "definition profile", mutate: func(value *resolvedDefinition) { value.Definition.Profile = experiments.ProfileDeep }},
		{name: "definition parameters", mutate: func(value *resolvedDefinition) { value.Definition.Spec.Parameters["requests"]++ }},
		{name: "definition parameter alias", mutate: func(value *resolvedDefinition) {
			value.Definition.Workload.Parameters = value.Definition.Spec.Parameters
		}},
		{name: "spec aliases expected parameters", mutate: func(value *resolvedDefinition) {
			value.Definition.Spec.Parameters = value.Expected.Workload.Parameters
		}},
		{name: "workload aliases expected parameters", mutate: func(value *resolvedDefinition) {
			value.Definition.Workload.Parameters = value.Expected.Workload.Parameters
		}},
		{name: "definition fault", mutate: func(value *resolvedDefinition) { value.Definition.Faults[0].Duration++ }},
		{name: "definition seed", mutate: func(value *resolvedDefinition) { value.Definition.Spec.Seed++ }},
		{name: "definition assertion", mutate: func(value *resolvedDefinition) { value.Definition.Spec.Assertions[0].ID = "other-assertion" }},
		{name: "definition events", mutate: func(value *resolvedDefinition) {
			value.Definition.Spec.Events = append(value.Definition.Spec.Events, value.Definition.Spec.Events[0])
		}},
		{name: "definition conclusion", mutate: func(value *resolvedDefinition) { value.Definition.Conclude = nil }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			resolved := base
			resolved.Expected = cloneExpectedCellForTest(base.Expected)
			resolved.Definition = cloneExperimentDefinition(base.Definition)
			mutation.mutate(&resolved)
			if err := validateRunResult(resolved, definitionTestPassedResult(base), nil); err == nil {
				t.Fatal("mutated frozen definition was accepted")
			}
		})
	}
}

func TestValidateRunResultRejectsExecutableDefinitionDrift(t *testing.T) {
	base := resolveDefinitionTestFixture(t)
	for _, mutation := range executableDefinitionMutationsForTest() {
		t.Run(mutation.name, func(t *testing.T) {
			resolved := base
			resolved.Definition = cloneExperimentDefinition(base.Definition)
			mutation.mutate(&resolved.Definition)
			if err := validateRunResult(resolved, definitionTestPassedResult(base), nil); err == nil {
				t.Fatal("executable definition drift was accepted during preparation")
			}
		})
	}
}

func TestConvertPreparedRunResultRechecksExecutableDefinition(t *testing.T) {
	for _, mutation := range executableDefinitionMutationsForTest() {
		t.Run(mutation.name, func(t *testing.T) {
			resolved := resolveDefinitionTestFixture(t)
			result := definitionTestPassedResult(resolved)
			prepared, err := prepareRunResult(resolved, result, nil)
			if err != nil {
				t.Fatal(err)
			}
			mutation.mutate(&resolved.Definition)
			if _, err := convertPreparedRunResult(resolved, result, prepared, definitionTestRecordIdentity()); err == nil {
				t.Fatal("executable definition drift was accepted during conversion")
			}
		})
	}
}

func TestValidateRunResultRejectsCoordinatedExpectedAndDefinitionMutation(t *testing.T) {
	base := resolveDefinitionTestFixture(t)
	resolved := base
	resolved.Expected = cloneExpectedCellForTest(base.Expected)
	resolved.Definition = cloneExperimentDefinition(base.Definition)
	resolved.Expected.Cell.Workload = "other-workload"
	resolved.Expected.Workload.ID = "other-workload"
	resolved.Definition.Workload.ID = "other-workload"

	if err := validateRunResult(resolved, definitionTestPassedResult(base), nil); err == nil {
		t.Fatal("coordinated mutation of expected and definition was accepted")
	}
}

func TestValidateResolvedDefinitionRejectsCoordinatedUnsafeDynamicMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*resolvedDefinition)
	}{
		{name: "hypothesis", mutate: func(resolved *resolvedDefinition) {
			const hostile = "host root /private/tmp/hypothesis"
			resolved.Expected.Hypothesis = hostile
			resolved.frozenExpected.Hypothesis = hostile
			resolved.Definition.Hypothesis = hostile
		}},
		{name: "workload parameter key", mutate: func(resolved *resolvedDefinition) {
			hostile := map[string]int64{"/private/tmp/parameter": 4}
			resolved.Expected.Workload.Parameters = cloneDefinitionParameters(hostile)
			resolved.frozenExpected.Workload.Parameters = cloneDefinitionParameters(hostile)
			resolved.Definition.Spec.Parameters = cloneDefinitionParameters(hostile)
			resolved.Definition.Workload.Parameters = cloneDefinitionParameters(hostile)
		}},
		{name: "measurement unit", mutate: func(resolved *resolvedDefinition) {
			const hostile = "/private/tmp/unit"
			resolved.Expected.Measurements[0].Unit = hostile
			resolved.frozenExpected.Measurements[0].Unit = hostile
			resolved.Definition.Measurements[0].Unit = hostile
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := resolveDefinitionTestFixture(t)
			resolved := base
			resolved.Expected = cloneExpectedCellForTest(base.Expected)
			resolved.frozenExpected = cloneExpectedCellForTest(base.frozenExpected)
			resolved.Definition = cloneExperimentDefinition(base.Definition)
			test.mutate(&resolved)
			if err := validateResolvedDefinition(resolved); err == nil {
				t.Fatal("coordinated unsafe dynamic mutation was accepted")
			}
		})
	}
}

type executableDefinitionMutationForTest struct {
	name   string
	mutate func(*experiments.Definition)
}

func executableDefinitionMutationsForTest() []executableDefinitionMutationForTest {
	return []executableDefinitionMutationForTest{
		{name: "event name", mutate: func(definition *experiments.Definition) { definition.Spec.Events[0].Name = "other-event" }},
		{name: "event at", mutate: func(definition *experiments.Definition) { definition.Spec.Events[0].At++ }},
		{name: "event phase", mutate: func(definition *experiments.Definition) { definition.Spec.Events[0].Phase = harness.PhaseObserve }},
		{name: "event sequence", mutate: func(definition *experiments.Definition) { definition.Spec.Events[0].Sequence++ }},
		{name: "event apply", mutate: func(definition *experiments.Definition) {
			definition.Spec.Events[0].Apply = replacementDefinitionAction
		}},
		{name: "assertion ID", mutate: func(definition *experiments.Definition) { definition.Spec.Assertions[0].ID = "other-assertion" }},
		{name: "assertion check", mutate: func(definition *experiments.Definition) {
			definition.Spec.Assertions[0].Check = replacementDefinitionCheck
		}},
		{name: "conclude", mutate: func(definition *experiments.Definition) { definition.Conclude = replacementDefinitionConclusion }},
	}
}

func replacementDefinitionAction(context.Context, *harness.Runtime) error {
	return nil
}

func replacementDefinitionCheck(harness.Snapshot) (bool, string) {
	return true, "replacement"
}

func replacementDefinitionConclusion(harness.RunResult) string {
	return "replacement conclusion"
}

func cloneExpectedCellForTest(source release.ExpectedCell) release.ExpectedCell {
	cloned := source
	cloned.Cell = cloneDefinitionCell(source.Cell)
	cloned.Workload.Parameters = cloneDefinitionParameters(source.Workload.Parameters)
	cloned.Faults = append([]evidence.Fault{}, source.Faults...)
	cloned.Limitations = append([]string{}, source.Limitations...)
	cloned.Measurements = append([]evidence.MeasurementSpec{}, source.Measurements...)
	return cloned
}

func cloneRunResultForTest(source harness.RunResult) harness.RunResult {
	cloned := source
	cloned.Metrics = append([]harness.Metric{}, source.Metrics...)
	cloned.Assertions = append([]harness.AssertionResult{}, source.Assertions...)
	cloned.Diagnostics = append([]harness.Diagnostic{}, source.Diagnostics...)
	return cloned
}

func resolveDefinitionTestFixture(t *testing.T) resolvedDefinition {
	return resolveDefinitionTestFixtureWithMutation(t, nil)
}

func resolveDefinitionTestFixtureWithMutation(t *testing.T, mutate func(*experiments.Definition)) resolvedDefinition {
	t.Helper()
	cell := definitionTestCell()
	definition := definitionTestDefinition(cell)
	if mutate != nil {
		mutate(&definition)
	}
	repository := definitionTestCatalog(cell, definition)
	lookup := func(validator.MatrixCell) (experiments.Factory, bool) {
		return func(validator.MatrixCell, experiments.Profile) (experiments.Definition, error) {
			return definition, nil
		}, true
	}
	resolved, issues := resolveDefinitions(repository, []validator.MatrixCell{cell}, evidence.ProfileSmoke, lookup)
	if len(issues) != 0 || len(resolved) != 1 {
		t.Fatalf("fixture resolve = %#v / %#v", resolved, issues)
	}
	return resolved[0]
}

func definitionTestPassedResult(resolved resolvedDefinition) harness.RunResult {
	return harness.RunResult{
		Status:         harness.StatusPassed,
		StartedAt:      resolved.Definition.Spec.Start,
		FinishedAt:     resolved.Definition.Spec.Start.Add(time.Nanosecond),
		EventsExecuted: uint64(len(resolved.Definition.Spec.Events)),
		Metrics:        []harness.Metric{{Name: "requests.total", Unit: "count", Value: 4}},
		Assertions:     []harness.AssertionResult{{ID: "assertion-one", Passed: true, Message: "ok"}},
		Diagnostics:    []harness.Diagnostic{},
	}
}

func definitionTestRecordIdentity() recordIdentity {
	return recordIdentity{
		ID:       "run-20000101T000000.000Z-00000000000000000000000000000000",
		RunSetID: "set-20000101T000000.000Z-00000000000000000000000000000000",
		SourceState: inputdigest.State{
			InputDigest:  "sha256:0000000000000000000000000000000000000000000000000000000000000000",
			SourceCommit: "0000000000000000000000000000000000000000",
		},
		Environment: evidence.Environment{
			GoVersion: "go1.26.5", OS: "darwin", Arch: "arm64", CPU: "unknown", LogicalCPUs: 8,
		},
	}
}

func definitionTestCell() validator.MatrixCell {
	return validator.MatrixCell{
		LabID:            "lab-one",
		RequiredRunID:    "run-one",
		BindingID:        "binding-one",
		ClaimID:          "claim-one",
		Role:             "baseline",
		ImplementationID: "implementation-one",
		AdapterID:        "",
		Workload:         "workload-one",
		Faults:           []string{"fault-one"},
		AssertionIDs:     []string{"assertion-one"},
	}
}

func definitionTestDefinition(cell validator.MatrixCell) experiments.Definition {
	return experiments.Definition{
		Spec: harness.RunSpec{
			LabID:            cell.LabID,
			RequiredRunID:    cell.RequiredRunID,
			BindingID:        cell.BindingID,
			ClaimID:          cell.ClaimID,
			ImplementationID: cell.ImplementationID,
			AdapterID:        cell.AdapterID,
			Seed:             7,
			Start:            time.Unix(1, 0).UTC(),
			Deadline:         time.Second,
			Parameters:       map[string]int64{"requests": 4},
			Events: []harness.Event{{
				At: time.Nanosecond, Phase: harness.PhaseRequest, Sequence: 0, Name: "request-one",
				Apply: func(_ context.Context, _ *harness.Runtime) error { return nil },
			}},
			Assertions: []harness.Assertion{{
				ID:    "assertion-one",
				Check: func(harness.Snapshot) (bool, string) { return true, "ok" },
			}},
		},
		Profile:    experiments.ProfileSmoke,
		Hypothesis: "the frozen workload satisfies the assertion",
		Workload: experiments.Workload{
			ID:         cell.Workload,
			Parameters: map[string]int64{"requests": 4},
		},
		Faults:       []experiments.Fault{{ID: "fault-one", At: time.Nanosecond, Duration: 2 * time.Nanosecond}},
		Measurements: []experiments.MetricSpec{{ID: "requests.total", Unit: "count"}},
		Limitations:  []string{"single deterministic workload"},
		Conclude: func(result harness.RunResult) string {
			if result.Status == harness.StatusPassed {
				return "passed conclusion"
			}
			return "failed conclusion"
		},
	}
}

func definitionTestCatalog(cell validator.MatrixCell, definition experiments.Definition) *catalog.Catalog {
	metrics := make([]string, len(definition.Measurements))
	for index, measurement := range definition.Measurements {
		metrics[index] = measurement.ID
	}
	return &catalog.Catalog{Labs: map[string]catalog.LabManifest{
		cell.LabID: {ID: cell.LabID, Metrics: metrics},
	}}
}
