package validator

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
)

func TestComputeCoverageUsesExactSetsAndDependencyClosure(t *testing.T) {
	c := validCatalog()
	coverage := ComputeCoverage(c)

	if coverage.BaselineTotal != 1 || coverage.CompleteTotal != 1 {
		t.Fatalf("coverage totals = baseline %d complete %d, want 1 and 1", coverage.BaselineTotal, coverage.CompleteTotal)
	}
	if len(coverage.MissingCaseIDs) != 0 || len(coverage.UnexpectedCaseIDs) != 0 {
		t.Fatalf("coverage differences = missing %#v unexpected %#v, want empty", coverage.MissingCaseIDs, coverage.UnexpectedCaseIDs)
	}
	wantFamilies := []FamilyCoverage{{ID: "addressing-traffic", Required: 1, Complete: 1}}
	if !reflect.DeepEqual(coverage.Families, wantFamilies) {
		t.Fatalf("family coverage = %#v, want %#v", coverage.Families, wantFamilies)
	}
	if !reflect.DeepEqual(coverage.RequiredPrinciples, []string{"principle-a"}) {
		t.Fatalf("required principles = %#v", coverage.RequiredPrinciples)
	}
	if !reflect.DeepEqual(coverage.RequiredScenarioLabs, []string{"scenario-lab-a"}) {
		t.Fatalf("required scenario labs = %#v", coverage.RequiredScenarioLabs)
	}
	if !reflect.DeepEqual(coverage.RequiredPrimitiveLabs, []string{"primitive-lab-a"}) {
		t.Fatalf("required primitive labs = %#v", coverage.RequiredPrimitiveLabs)
	}
	if !reflect.DeepEqual(coverage.RequiredAdapters, []string{"adapter-a"}) {
		t.Fatalf("required adapters = %#v", coverage.RequiredAdapters)
	}
}

func TestScopeMembershipDrivesRequiredClosure(t *testing.T) {
	c := validCatalog()
	caseManifest := c.Cases["case-a"]
	caseManifest.Required = false
	c.Cases[caseManifest.ID] = caseManifest
	principle := c.Principles["principle-a"]
	principle.Required = false
	c.Principles[principle.ID] = principle
	for id, lab := range c.Labs {
		lab.Required = false
		c.Labs[id] = lab
	}

	coverage := ComputeCoverage(c)
	if !reflect.DeepEqual(coverage.RequiredPrinciples, []string{"principle-a"}) ||
		!reflect.DeepEqual(coverage.RequiredScenarioLabs, []string{"scenario-lab-a"}) ||
		!reflect.DeepEqual(coverage.RequiredPrimitiveLabs, []string{"primitive-lab-a"}) ||
		!reflect.DeepEqual(coverage.RequiredAdapters, []string{"adapter-a"}) {
		t.Fatalf("scope-derived required closure = %#v", coverage)
	}
	if diagnostics := Validate(c, ModeRelease).Diagnostics; len(diagnostics) != 0 {
		t.Fatalf("required:false escaped or invalidated scope semantics: %#v", diagnostics)
	}
}

func TestComputeCoverageComparesIDsRatherThanCounts(t *testing.T) {
	c := validCatalog()
	manifest := c.Cases["case-a"]
	delete(c.Cases, "case-a")
	manifest.ID = "case-unexpected"
	c.Cases[manifest.ID] = manifest

	coverage := ComputeCoverage(c)
	if coverage.BaselineTotal != 1 || coverage.CompleteTotal != 1 {
		t.Fatalf("coverage totals = baseline %d complete %d, want equal counts", coverage.BaselineTotal, coverage.CompleteTotal)
	}
	if !reflect.DeepEqual(coverage.MissingCaseIDs, []string{"case-a"}) {
		t.Fatalf("missing cases = %#v, want [case-a]", coverage.MissingCaseIDs)
	}
	if !reflect.DeepEqual(coverage.UnexpectedCaseIDs, []string{"case-unexpected"}) {
		t.Fatalf("unexpected cases = %#v, want [case-unexpected]", coverage.UnexpectedCaseIDs)
	}
	wantFamilies := []FamilyCoverage{{ID: "addressing-traffic", Required: 1, Complete: 0}}
	if !reflect.DeepEqual(coverage.Families, wantFamilies) {
		t.Fatalf("family coverage = %#v, want scope-member intersection %#v", coverage.Families, wantFamilies)
	}
	assertDiagnosticCode(t, Validate(c, ModeRelease).Diagnostics, CodeReleaseScopeIncomplete)
}

func TestOnlyDistributedRateLimiterHasExactW0Coverage(t *testing.T) {
	c := onlyDistributedRateLimiterCatalog(t)
	wantMissing := []string{
		"ad-click-aggregation",
		"ad-serving-ranking",
		"appointment-booking",
		"autocomplete",
		"bank-transfer",
		"batch-data-pipeline",
		"cdn",
		"centralized-logging",
		"chat-messenger",
		"ci-runner",
		"cloud-file-sync",
		"code-assistant",
		"collaborative-editor",
		"comments-reactions",
		"configuration-feature-flags",
		"consistent-hash-router",
		"container-orchestrator",
		"dag-workflow",
		"deployment-system",
		"distributed-cache",
		"distributed-email-service",
		"distributed-id",
		"distributed-log-message-queue",
		"distributed-sql",
		"dns-service-discovery",
		"double-entry-ledger-wallet",
		"ecommerce-order-inventory",
		"embedding-index-pipeline",
		"food-delivery-dispatch",
		"full-text-search",
		"gpu-scheduler",
		"identity-authorization-service",
		"image-service",
		"inference-gateway",
		"job-scheduler",
		"key-value-store",
		"large-file-transfer",
		"leaderboard",
		"live-comments",
		"live-streaming",
		"llm-chat-serving",
		"load-balancer-api-gateway",
		"maps-navigation",
		"metrics-monitoring-alerting",
		"multi-tenant-cloud-control-plane",
		"music-podcast-streaming",
		"nearby-places",
		"notification-delivery",
		"object-storage",
		"online-auction",
		"pastebin",
		"payment-system",
		"photo-sharing",
		"presence-service",
		"pubsub",
		"qa-news-aggregation",
		"rag-assistant",
		"recommendation-system",
		"ride-hailing-dispatch",
		"social-graph",
		"social-news-feed",
		"stream-processing",
		"ticketing",
		"time-series-database",
		"top-k-heavy-hitters",
		"trading-brokerage",
		"transcoding-pipeline",
		"url-shortener",
		"vector-database",
		"video-conferencing",
		"video-on-demand",
		"web-crawler",
		"webhook-delivery",
		"wide-column-document-store",
	}

	coverage := ComputeCoverage(c)
	if coverage.BaselineTotal != 75 || coverage.CompleteTotal != 1 {
		t.Fatalf("coverage totals = baseline %d complete %d, want 75 and 1", coverage.BaselineTotal, coverage.CompleteTotal)
	}
	if !reflect.DeepEqual(coverage.MissingCaseIDs, wantMissing) {
		t.Fatalf("missing cases = %#v, want exact sorted W0 set %#v", coverage.MissingCaseIDs, wantMissing)
	}
	if len(coverage.UnexpectedCaseIDs) != 0 {
		t.Fatalf("unexpected cases = %#v, want none", coverage.UnexpectedCaseIDs)
	}
	wantFamilyCounts := map[string][2]int{
		"addressing-traffic":                {7, 1},
		"distributed-storage":               {7, 0},
		"feed-social-ranking":               {8, 0},
		"realtime-collaboration":            {8, 0},
		"search-crawl-geo":                  {8, 0},
		"media-delivery":                    {7, 0},
		"transactions-contention":           {8, 0},
		"streaming-analytics-observability": {7, 0},
		"scheduling-control-plane":          {8, 0},
		"ai-vector":                         {7, 0},
	}
	if len(coverage.Families) != len(wantFamilyCounts) {
		t.Fatalf("family coverage length = %d, want %d", len(coverage.Families), len(wantFamilyCounts))
	}
	for _, family := range coverage.Families {
		want, exists := wantFamilyCounts[family.ID]
		if !exists || family.Required != want[0] || family.Complete != want[1] {
			t.Errorf("family coverage = %#v, want required/complete %#v", family, want)
		}
	}

	development := Validate(c, ModeDevelopment)
	if len(development.Diagnostics) != 0 {
		t.Fatalf("development diagnostics = %#v, want none", development.Diagnostics)
	}

	release := Validate(c, ModeRelease)
	if len(release.Diagnostics) != 1 || release.Diagnostics[0].Code != CodeReleaseScopeIncomplete {
		t.Fatalf("release diagnostics = %#v, want sole release scope gap", release.Diagnostics)
	}
	for _, summary := range []string{"complete=1", "baseline=75", "missing=74"} {
		if !strings.Contains(release.Diagnostics[0].Message, summary) {
			t.Errorf("release diagnostic message %q does not contain %q", release.Diagnostics[0].Message, summary)
		}
	}
}

func TestReleaseFamilyValidationComparesMemberSetsNotCounts(t *testing.T) {
	c := validCatalog()
	c.Scope.Families = append(c.Scope.Families, catalog.ScopeFamily{ID: "distributed-storage", Title: "Distributed Storage"})
	c.Scope.Cases = append(c.Scope.Cases, catalog.ScopeCase{ID: "case-b", Title: "Case B", PrimaryFamily: "distributed-storage"})

	caseA := c.Cases["case-a"]
	caseA.PrimaryFamily = "distributed-storage"
	c.Cases[caseA.ID] = caseA

	caseB := caseA
	caseB.ID = "case-b"
	caseB.Title = "Case B"
	caseB.PrimaryFamily = "addressing-traffic"
	caseB.Claims = []catalog.Claim{{ID: "claim-case-b", Statement: "Case B claim."}}
	caseB.Labs = []string{"scenario-lab-b"}
	caseB.EvidenceRequirements = []catalog.EvidenceRequirement{{Claim: "claim-case-b", Lab: "scenario-lab-b"}}
	c.Cases[caseB.ID] = caseB

	labB := c.Labs["scenario-lab-a"]
	labB.ID = "scenario-lab-b"
	labB.CaseBindings = []catalog.CaseBinding{{
		ID:         "binding-case-b",
		CaseID:     "case-b",
		Claim:      "claim-case-b",
		Workload:   "workload-b",
		Assertions: []string{"assertion-case-b"},
	}}
	labB.RequiredRuns = []catalog.RequiredRun{{
		ID:       "run-case-b",
		Binding:  "binding-case-b",
		Baseline: "implementation-a",
		Variants: []string{"implementation-b"},
		Workload: "workload-b",
		Faults:   []string{"fault-b"},
		Adapters: []catalog.AdapterRequirement{{ID: "adapter-a", Required: true}},
	}}
	c.Labs[labB.ID] = labB

	report := Validate(c, ModeRelease)
	assertNoDiagnosticCode(t, report.Diagnostics, CodeReleaseScopeIncomplete)
	assertDiagnosticCode(t, report.Diagnostics, CodeReleaseFamilyMismatch)
	wantFamilies := []FamilyCoverage{
		{ID: "addressing-traffic", Required: 1, Complete: 0},
		{ID: "distributed-storage", Required: 1, Complete: 0},
	}
	if !reflect.DeepEqual(report.Coverage.Families, wantFamilies) {
		t.Fatalf("family coverage = %#v, want member-set intersections %#v", report.Coverage.Families, wantFamilies)
	}
}

func onlyDistributedRateLimiterCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	root, err := catalog.LoadDir(context.Background(), "../..")
	if err != nil {
		t.Fatalf("LoadDir(repository root) error = %v", err)
	}
	c := validCatalog()
	c.Scope = root.Scope
	c.Sources = root.Sources

	const sourceID = "source-bytebytego-system-design-interview"
	manifest := c.Cases["case-a"]
	delete(c.Cases, "case-a")
	manifest.ID = "distributed-rate-limiter"
	manifest.Title = "Distributed Rate Limiter"
	manifest.PrimaryFamily = "addressing-traffic"
	manifest.Sources = []string{sourceID}
	c.Cases[manifest.ID] = manifest

	principle := c.Principles["principle-a"]
	principle.Sources = []string{sourceID}
	c.Principles[principle.ID] = principle

	for id, lab := range c.Labs {
		lab.Sources = []string{sourceID}
		if lab.Kind == catalog.LabKindScenario {
			lab.CaseBindings[0].CaseID = manifest.ID
		}
		c.Labs[id] = lab
	}
	adapter := c.Adapters["adapter-a"]
	adapter.Sources = []string{sourceID}
	c.Adapters[adapter.ID] = adapter
	return c
}
