package experiments

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/content"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

func TestDistributedRateLimiterManifestClosureIsExact(t *testing.T) {
	repository, err := catalog.LoadDir(context.Background(), repositoryRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	caseManifest, exists := repository.Cases["distributed-rate-limiter"]
	if !exists {
		t.Fatal("distributed-rate-limiter case manifest is absent")
	}
	if caseManifest.ID != "distributed-rate-limiter" || caseManifest.Title != "分布式限流器" || caseManifest.PrimaryFamily != "addressing-traffic" || !caseManifest.Required || caseManifest.Status != catalog.LifecycleStatusComplete {
		t.Errorf("case identity = %#v", caseManifest)
	}
	if !reflect.DeepEqual(caseManifest.Dimensions, catalog.DimensionIDs) {
		t.Errorf("case dimensions = %#v, want %#v", caseManifest.Dimensions, catalog.DimensionIDs)
	}
	if !reflect.DeepEqual(caseManifest.Principles, []string{"token-bucket"}) || !reflect.DeepEqual(caseManifest.Labs, []string{"distributed-rate-limiter"}) || !reflect.DeepEqual(caseManifest.Sources, []string{"source-rfc-3290-token-bucket"}) {
		t.Errorf("case closure = principles %#v labs %#v sources %#v", caseManifest.Principles, caseManifest.Labs, caseManifest.Sources)
	}
	wantClaimIDs := []string{
		"distributed-rate-limiter-per-node-multiplies-global-quota",
		"distributed-rate-limiter-outage-policy-trades-availability-for-quota",
	}
	if len(caseManifest.Claims) != len(wantClaimIDs) {
		t.Fatalf("claims = %#v", caseManifest.Claims)
	}
	for index, claim := range caseManifest.Claims {
		if claim.ID != wantClaimIDs[index] || strings.TrimSpace(claim.Statement) == "" {
			t.Errorf("claim %d = %#v", index, claim)
		}
	}
	wantEvidence := []catalog.EvidenceRequirement{
		{Claim: wantClaimIDs[0], Lab: "distributed-rate-limiter"},
		{Claim: wantClaimIDs[1], Lab: "distributed-rate-limiter"},
	}
	if !reflect.DeepEqual(caseManifest.EvidenceRequirements, wantEvidence) {
		t.Errorf("evidence requirements = %#v, want %#v", caseManifest.EvidenceRequirements, wantEvidence)
	}

	lab, exists := repository.Labs["distributed-rate-limiter"]
	if !exists {
		t.Fatal("distributed-rate-limiter lab manifest is absent")
	}
	if lab.ID != "distributed-rate-limiter" || lab.Kind != catalog.LabKindScenario || !lab.Required || lab.Status != catalog.LifecycleStatusComplete {
		t.Errorf("lab identity = %#v", lab)
	}
	if !reflect.DeepEqual(lab.Implementations, []string{"shared-token-bucket", "per-node-token-bucket", "shared-fail-closed", "shared-fail-open"}) {
		t.Errorf("implementations = %#v", lab.Implementations)
	}
	wantBindings := []catalog.CaseBinding{
		{
			ID: "distributed-rate-limiter-global-quota", CaseID: "distributed-rate-limiter", Claim: wantClaimIDs[0], Workload: "two-node-burst",
			Assertions: []string{"all-requests-decided", "expected-allowed-count", "expected-global-quota-overshoot", "no-unexpected-errors"},
		},
		{
			ID: "distributed-rate-limiter-outage-policy", CaseID: "distributed-rate-limiter", Claim: wantClaimIDs[1], Workload: "coordinator-outage",
			Assertions: []string{"all-requests-decided", "expected-outage-decision", "expected-outage-availability", "expected-quota-overshoot", "no-unexpected-errors"},
		},
	}
	if !reflect.DeepEqual(lab.CaseBindings, wantBindings) {
		t.Errorf("case bindings = %#v, want %#v", lab.CaseBindings, wantBindings)
	}
	wantRuns := []catalog.RequiredRun{
		{ID: "per-node-vs-shared-quota", Binding: "distributed-rate-limiter-global-quota", Baseline: "shared-token-bucket", Variants: []string{"per-node-token-bucket"}, Workload: "two-node-burst", Faults: []string{}},
		{ID: "coordinator-outage-policy", Binding: "distributed-rate-limiter-outage-policy", Baseline: "shared-fail-closed", Variants: []string{"shared-fail-open"}, Workload: "coordinator-outage", Faults: []string{"coordinator-unavailable"}},
	}
	if !reflect.DeepEqual(lab.RequiredRuns, wantRuns) {
		t.Errorf("required runs = %#v, want %#v", lab.RequiredRuns, wantRuns)
	}
	wantMetrics := measurementIDs(scenarioMeasurementContract)
	if !reflect.DeepEqual(lab.Metrics, wantMetrics) || !reflect.DeepEqual(lab.Sources, []string{"source-rfc-3290-token-bucket"}) {
		t.Errorf("lab metrics/sources = %#v / %#v", lab.Metrics, lab.Sources)
	}
}

func TestDistributedRateLimiterContentSchemaMatrixAndCoverageClose(t *testing.T) {
	root := repositoryRoot(t)
	repository, err := catalog.LoadDir(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	report := validator.Validate(repository, validator.ModeDevelopment)
	if len(report.Diagnostics) != 0 {
		t.Errorf("schema diagnostics = %#v", report.Diagnostics)
	}
	if !reflect.DeepEqual(report.Matrix, expectedMatrixCells()) {
		t.Errorf("matrix = %#v, want %#v", report.Matrix, expectedMatrixCells())
	}
	coverage := report.Coverage
	if coverage.BaselineTotal != 75 || coverage.CompleteTotal != 1 || len(coverage.MissingCaseIDs) != 74 || len(coverage.UnexpectedCaseIDs) != 0 {
		t.Errorf("coverage = %#v", coverage)
	}
	contentResult := content.ValidateRepository(root, repository)
	if len(contentResult.Diagnostics) != 0 {
		t.Errorf("content diagnostics = %#v", contentResult.Diagnostics)
	}
	markdown, err := os.ReadFile(root + "/cases/distributed-rate-limiter/README.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(markdown)
	for _, claimID := range []string{
		"distributed-rate-limiter-per-node-multiplies-global-quota",
		"distributed-rate-limiter-outage-policy-trades-availability-for-quota",
	} {
		marker := "[DEDUCED:" + claimID + "]"
		if strings.Count(text, marker) != 1 {
			t.Errorf("marker %q count = %d, want 1", marker, strings.Count(text, marker))
		}
		for _, forbidden := range []string{"[ASSUMED:" + claimID + "]", "[MEASURED:" + claimID + "]", "[SOURCED:" + claimID + ":"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("README contains forbidden marker prefix %q", forbidden)
			}
		}
	}
}
