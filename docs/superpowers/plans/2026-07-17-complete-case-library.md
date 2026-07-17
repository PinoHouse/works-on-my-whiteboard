# Complete System Design Case Library Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add rigorous first-principles interview analysis for all 74 currently missing canonical cases while preserving the existing 1/75 experiment-and-evidence closure semantics.

**Architecture:** Every new case is an authored YAML manifest plus an eight-section Chinese Markdown analysis. New manifests remain `draft` because the existing `complete` lifecycle means full principle, lab, source, and evidence closure; a repository test temporarily evaluates draft manifests through the complete content validator so prose quality is enforced without changing runtime schema. Human navigation reports 75/75 authored content, while generated machine coverage continues to report 1/75 strict closure.

**Tech Stack:** Markdown, YAML schema version 1, Go 1.26.5 repository tests, existing `internal/content` and `internal/catalog` packages.

## Global Constraints

- Write user-facing prose in Simplified Chinese; keep stable IDs, commit messages, and code identifiers in English.
- Do not modify lifecycle, coverage, release, CLI, schema, lab, adapter, or evidence behavior.
- Do not add principle, lab, adapter, source, or evidence entities for the 74 new cases.
- New case manifests use `required: true` and `status: draft`.
- Every README has exactly the eight ordered H2 headings from the design specification.
- Every README meets the existing per-section minimum prose lengths: `120, 180, 240, 240, 300, 240, 240, 240` runes.
- Every manifest declares at least two case-owned claims; every declared claim appears in prose with one consistent marker class.
- Use only `DEDUCED` for derived claims and fully explained `ASSUMED` markers; do not claim measurements or sources that do not exist.
- Start from invariants, information boundaries, resource limits, and switching conditions; product names may only appear as later implementation mappings.
- Each case must pass the twelve anti-template quality checks in `docs/superpowers/specs/2026-07-17-complete-case-library-design.md`.

---

### Task 1: Add the authored-content regression contract

**Files:**
- Create: `internal/content/library_content_test.go`

**Interfaces:**
- Consumes: `catalog.LoadDir`, `content.ValidateCase`, `scope.yaml`, and every present `cases/*/{case.yaml,README.md}`.
- Produces: one test proving all 75 scope cases exist and one test applying the strict content contract to every present case regardless of lifecycle status.

- [ ] **Step 1: Add the failing repository tests**

```go
package content

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
)

func TestAllScopedCasesHaveAuthoredContent(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	repository, err := catalog.LoadDir(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(repository.Cases), len(repository.Scope.Cases); got != want {
		t.Errorf("authored case manifests = %d, want %d", got, want)
	}
	for _, scoped := range repository.Scope.Cases {
		if _, exists := repository.Cases[scoped.ID]; !exists {
			t.Errorf("scope case %q has no authored manifest", scoped.ID)
		}
	}
}

func TestPresentCasesMeetFullContentContract(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	repository, err := catalog.LoadDir(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	scopeByID := make(map[string]catalog.ScopeCase, len(repository.Scope.Cases))
	for _, scoped := range repository.Scope.Cases {
		scopeByID[scoped.ID] = scoped
	}
	for id, manifest := range repository.Cases {
		t.Run(id, func(t *testing.T) {
			scoped, exists := scopeByID[id]
			if !exists {
				t.Fatalf("authored case %q is outside scope", id)
			}
			if manifest.Title != scoped.Title || manifest.PrimaryFamily != scoped.PrimaryFamily {
				t.Errorf("identity = title %q family %q; want %q and %q", manifest.Title, manifest.PrimaryFamily, scoped.Title, scoped.PrimaryFamily)
			}
			if !manifest.Required {
				t.Error("authored case is not required")
			}
			if len(manifest.Dimensions) == 0 {
				t.Error("authored case has no dimensions")
			}
			if len(manifest.Claims) < 2 {
				t.Errorf("authored case claims = %d, want at least 2", len(manifest.Claims))
			}
			for _, claim := range manifest.Claims {
				if !strings.HasPrefix(claim.ID, manifest.ID+"-") {
					t.Errorf("claim %q does not use case ID prefix", claim.ID)
				}
			}
			relative := filepath.Join("cases", id, "README.md")
			markdown, err := os.ReadFile(filepath.Join(root, relative))
			if err != nil {
				t.Fatal(err)
			}
			strict := manifest
			strict.Status = catalog.LifecycleStatusComplete
			result := ValidateCase(filepath.ToSlash(relative), markdown, strict, repository)
			for _, diagnostic := range result.Diagnostics {
				t.Errorf("%s: %s", diagnostic.Code, diagnostic.Message)
			}
		})
	}
}
```

- [ ] **Step 2: Run the completeness test and confirm the intended red state**

Run:

```sh
go test ./internal/content -run TestAllScopedCasesHaveAuthoredContent -count=1
```

Expected: FAIL with `authored case manifests = 1, want 75` and missing case messages.

- [ ] **Step 3: Run the strict test against the existing golden slice**

Run:

```sh
go test ./internal/content -run TestPresentCasesMeetFullContentContract -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit the regression contract**

```sh
git add internal/content/library_content_test.go
git -c commit.gpgsign=false commit -m "test: require authored content for every scoped case"
```

### Task 2: Calibrate four distinct case shapes

**Files:**
- Create: `cases/url-shortener/case.yaml`
- Create: `cases/url-shortener/README.md`
- Create: `cases/key-value-store/case.yaml`
- Create: `cases/key-value-store/README.md`
- Create: `cases/ticketing/case.yaml`
- Create: `cases/ticketing/README.md`
- Create: `cases/llm-chat-serving/case.yaml`
- Create: `cases/llm-chat-serving/README.md`

**Interfaces:**
- Consumes: the manifest and prose contracts in the design specification.
- Produces: four reference-quality content-only cases spanning addressing, storage, contention, and probabilistic AI serving.

- [ ] **Step 1: Author the four manifests**

Use the exact identity and family from `scope.yaml`. Each manifest must contain applicable registered dimensions and two or three unique claims. The claims must cover:

- `url-shortener`: keyspace/collision capacity and immutable-versus-mutable redirect semantics;
- `key-value-store`: replica acknowledgement versus declared durability and partition ownership/versioning;
- `ticketing`: single allocation authority and the utilization cost of expiring holds;
- `llm-chat-serving`: token-budget queueing and versioned probabilistic output reproducibility.

- [ ] **Step 2: Author the four eight-section analyses**

Each analysis must include its own state owner, invariants, formula, numerical or event-order counterexample, two switching metrics, fault timeline, rejected alternative, and case-specific interview essence. Marker IDs must match the manifest exactly.

- [ ] **Step 3: Run the strict present-case contract**

Run:

```sh
go test ./internal/content -run TestPresentCasesMeetFullContentContract -count=1
```

Expected: PASS for the existing case and all four calibration cases.

- [ ] **Step 4: Perform the cross-shape anti-template review**

Replace each title mentally with one of the other three. Any paragraph that remains correct without substantive changes must be rewritten around the case's unique state, invariant, or bottleneck.

- [ ] **Step 5: Commit the calibration batch**

```sh
git add cases/url-shortener cases/key-value-store cases/ticketing cases/llm-chat-serving
git -c commit.gpgsign=false commit -m "docs: add calibrated system design case analyses"
```

### Task 3: Complete addressing and traffic content

**Files:**
- Create: `cases/pastebin/{case.yaml,README.md}`
- Create: `cases/distributed-id/{case.yaml,README.md}`
- Create: `cases/dns-service-discovery/{case.yaml,README.md}`
- Create: `cases/load-balancer-api-gateway/{case.yaml,README.md}`
- Create: `cases/consistent-hash-router/{case.yaml,README.md}`

**Interfaces:**
- Consumes: the addressing-family core and the calibrated URL-shortener style.
- Produces: five non-overlapping analyses; `distributed-rate-limiter` and `url-shortener` already cover the other family members.

- [ ] **Step 1: Author manifests with case-specific claims**

Cover respectively: expiry and abuse boundary; uniqueness/order/clock rollback; stale mapping and health authority; admission/routing/policy ordering; minimum remapping and skew under membership change.

- [ ] **Step 2: Author the five complete analyses**

Differentiate the family six-tuples. In particular, do not turn DNS discovery, load balancing, and consistent hashing into three generic routing articles.

- [ ] **Step 3: Validate every present case**

Run:

```sh
go test ./internal/content -run TestPresentCasesMeetFullContentContract -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit the family**

```sh
git add cases/pastebin cases/distributed-id cases/dns-service-discovery cases/load-balancer-api-gateway cases/consistent-hash-router
git -c commit.gpgsign=false commit -m "docs: complete addressing and traffic case content"
```

### Task 4: Complete distributed storage content

**Files:**
- Create: `cases/distributed-cache/{case.yaml,README.md}`
- Create: `cases/distributed-sql/{case.yaml,README.md}`
- Create: `cases/wide-column-document-store/{case.yaml,README.md}`
- Create: `cases/object-storage/{case.yaml,README.md}`
- Create: `cases/cloud-file-sync/{case.yaml,README.md}`
- Create: `cases/time-series-database/{case.yaml,README.md}`

**Interfaces:**
- Consumes: the calibrated key-value-store analysis.
- Produces: the remaining six storage cases.

- [ ] **Step 1: Author manifests with distinct storage invariants**

Cover respectively: bounded staleness and stampede; serializable transaction visibility; partition-local query shape and schema flexibility; immutable object version/metadata authority; convergent file versions and conflicts; timestamp/order/cardinality and retention.

- [ ] **Step 2: Author the six complete analyses**

Each article must identify what a successful write means and which component may make that declaration. Use different amplification and failure models for cache, SQL, object, sync, and time-series workloads.

- [ ] **Step 3: Validate and review the storage six-tuples**

Run:

```sh
go test ./internal/content -run TestPresentCasesMeetFullContentContract -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit the family**

```sh
git add cases/distributed-cache cases/distributed-sql cases/wide-column-document-store cases/object-storage cases/cloud-file-sync cases/time-series-database
git -c commit.gpgsign=false commit -m "docs: complete distributed storage case content"
```

### Task 5: Complete transactions and contention content

**Files:**
- Create: `cases/appointment-booking/{case.yaml,README.md}`
- Create: `cases/online-auction/{case.yaml,README.md}`
- Create: `cases/payment-system/{case.yaml,README.md}`
- Create: `cases/double-entry-ledger-wallet/{case.yaml,README.md}`
- Create: `cases/trading-brokerage/{case.yaml,README.md}`
- Create: `cases/ecommerce-order-inventory/{case.yaml,README.md}`
- Create: `cases/bank-transfer/{case.yaml,README.md}`

**Interfaces:**
- Consumes: the calibrated ticketing analysis.
- Produces: the remaining seven contention cases.

- [ ] **Step 1: Author manifests around conserved assets**

Cover respectively: provider-slot ownership; close-time ordering and highest valid bid; payment intent/idempotency/external uncertainty; balanced immutable postings; order acceptance and market sequencing; reservation/saga/compensation; debit-credit conservation and ambiguous completion.

- [ ] **Step 2: Author the seven complete analyses**

Every case must distinguish business intent, authorization, settlement, ledger, and externally observed state where applicable. At least one event timeline per article must expose a retry or partial-failure ambiguity.

- [ ] **Step 3: Validate and compare all eight family members**

Run:

```sh
go test ./internal/content -run TestPresentCasesMeetFullContentContract -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit the family**

```sh
git add cases/appointment-booking cases/online-auction cases/payment-system cases/double-entry-ledger-wallet cases/trading-brokerage cases/ecommerce-order-inventory cases/bank-transfer
git -c commit.gpgsign=false commit -m "docs: complete transaction and contention case content"
```

### Task 6: Complete feed, social, and ranking content

**Files:**
- Create: `cases/social-news-feed/{case.yaml,README.md}`
- Create: `cases/photo-sharing/{case.yaml,README.md}`
- Create: `cases/qa-news-aggregation/{case.yaml,README.md}`
- Create: `cases/top-k-heavy-hitters/{case.yaml,README.md}`
- Create: `cases/leaderboard/{case.yaml,README.md}`
- Create: `cases/comments-reactions/{case.yaml,README.md}`
- Create: `cases/recommendation-system/{case.yaml,README.md}`
- Create: `cases/ad-serving-ranking/{case.yaml,README.md}`

**Interfaces:**
- Consumes: the feed-family first-principles core.
- Produces: all eight feed and ranking cases.

- [ ] **Step 1: Author manifests around materialization and ranking semantics**

Differentiate fan-out skew, media metadata, community moderation, approximate frequency, score ordering, threaded interaction counters, offline/online recommendation freshness, and budget-constrained ad auctions.

- [ ] **Step 2: Author the eight complete analyses**

Each article must state whether order is authoritative or derived, where personalization is materialized, and which skew dominates cost. Approximation errors must be explicit for Top-K and ranking cases.

- [ ] **Step 3: Validate and run the sibling-title test**

Run:

```sh
go test ./internal/content -run TestPresentCasesMeetFullContentContract -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit the family**

```sh
git add cases/social-news-feed cases/photo-sharing cases/qa-news-aggregation cases/top-k-heavy-hitters cases/leaderboard cases/comments-reactions cases/recommendation-system cases/ad-serving-ranking
git -c commit.gpgsign=false commit -m "docs: complete feed and ranking case content"
```

### Task 7: Complete realtime communication and collaboration content

**Files:**
- Create: `cases/chat-messenger/{case.yaml,README.md}`
- Create: `cases/notification-delivery/{case.yaml,README.md}`
- Create: `cases/distributed-email-service/{case.yaml,README.md}`
- Create: `cases/webhook-delivery/{case.yaml,README.md}`
- Create: `cases/presence-service/{case.yaml,README.md}`
- Create: `cases/collaborative-editor/{case.yaml,README.md}`
- Create: `cases/live-comments/{case.yaml,README.md}`
- Create: `cases/video-conferencing/{case.yaml,README.md}`

**Interfaces:**
- Consumes: the realtime-family first-principles core.
- Produces: all eight realtime cases.

- [ ] **Step 1: Author manifests around delivery, ordering, and convergence**

Differentiate per-conversation order, channel preference, mail transfer custody, at-least-once callback delivery, expiring liveness hints, multi-writer convergence, broadcast fan-out, and latency/jitter/media topology.

- [ ] **Step 2: Author the eight complete analyses**

Each article must name what can be exactly guaranteed across disconnects and what remains a best-effort hint. Do not apply message-queue semantics unchanged to presence, collaborative editing, or media transport.

- [ ] **Step 3: Validate and review failure timelines**

Run:

```sh
go test ./internal/content -run TestPresentCasesMeetFullContentContract -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit the family**

```sh
git add cases/chat-messenger cases/notification-delivery cases/distributed-email-service cases/webhook-delivery cases/presence-service cases/collaborative-editor cases/live-comments cases/video-conferencing
git -c commit.gpgsign=false commit -m "docs: complete realtime collaboration case content"
```

### Task 8: Complete streaming, analytics, and observability content

**Files:**
- Create: `cases/distributed-log-message-queue/{case.yaml,README.md}`
- Create: `cases/pubsub/{case.yaml,README.md}`
- Create: `cases/ad-click-aggregation/{case.yaml,README.md}`
- Create: `cases/metrics-monitoring-alerting/{case.yaml,README.md}`
- Create: `cases/centralized-logging/{case.yaml,README.md}`
- Create: `cases/stream-processing/{case.yaml,README.md}`
- Create: `cases/batch-data-pipeline/{case.yaml,README.md}`

**Interfaces:**
- Consumes: the unbounded-stream family core.
- Produces: all seven streaming and observability cases.

- [ ] **Step 1: Author manifests around bounded progress**

Differentiate durable offsets, subscription fan-out, deduplicated window aggregation, high-cardinality metric budgets, searchable log retention, event-time state/checkpoints, and replayable batch lineage.

- [ ] **Step 2: Author the seven complete analyses**

Every case must make ordering domain, replay boundary, backpressure location, state ownership, and loss/duplication semantics explicit.

- [ ] **Step 3: Validate and compare stream versus batch guarantees**

Run:

```sh
go test ./internal/content -run TestPresentCasesMeetFullContentContract -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit the family**

```sh
git add cases/distributed-log-message-queue cases/pubsub cases/ad-click-aggregation cases/metrics-monitoring-alerting cases/centralized-logging cases/stream-processing cases/batch-data-pipeline
git -c commit.gpgsign=false commit -m "docs: complete streaming and observability case content"
```

### Task 9: Complete search, crawl, graph, and geo content

**Files:**
- Create: `cases/web-crawler/{case.yaml,README.md}`
- Create: `cases/autocomplete/{case.yaml,README.md}`
- Create: `cases/full-text-search/{case.yaml,README.md}`
- Create: `cases/social-graph/{case.yaml,README.md}`
- Create: `cases/nearby-places/{case.yaml,README.md}`
- Create: `cases/maps-navigation/{case.yaml,README.md}`
- Create: `cases/ride-hailing-dispatch/{case.yaml,README.md}`
- Create: `cases/food-delivery-dispatch/{case.yaml,README.md}`

**Interfaces:**
- Consumes: the derived-index family core.
- Produces: all eight search and geo cases.

- [ ] **Step 1: Author manifests around approximation of changing reality**

Differentiate crawl frontier/politeness, prefix popularity, inverted-index freshness, graph traversal/privacy, spatial candidate recall, route-cost updates, driver-rider matching, and multi-stop courier assignment.

- [ ] **Step 2: Author the eight complete analyses**

Every article must define its source of truth, derived index lag, recall or optimality boundary, and the cost of refreshing reality faster.

- [ ] **Step 3: Validate and inspect search-versus-dispatch differences**

Run:

```sh
go test ./internal/content -run TestPresentCasesMeetFullContentContract -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit the family**

```sh
git add cases/web-crawler cases/autocomplete cases/full-text-search cases/social-graph cases/nearby-places cases/maps-navigation cases/ride-hailing-dispatch cases/food-delivery-dispatch
git -c commit.gpgsign=false commit -m "docs: complete search and geo case content"
```

### Task 10: Complete media processing and delivery content

**Files:**
- Create: `cases/video-on-demand/{case.yaml,README.md}`
- Create: `cases/live-streaming/{case.yaml,README.md}`
- Create: `cases/image-service/{case.yaml,README.md}`
- Create: `cases/cdn/{case.yaml,README.md}`
- Create: `cases/large-file-transfer/{case.yaml,README.md}`
- Create: `cases/transcoding-pipeline/{case.yaml,README.md}`
- Create: `cases/music-podcast-streaming/{case.yaml,README.md}`

**Interfaces:**
- Consumes: the media-byte-flow family core.
- Produces: all seven media cases.

- [ ] **Step 1: Author manifests around bytes, variants, and latency**

Differentiate segmented playback, live latency, deterministic image variants, cache invalidation, resumable integrity, dependency-aware transcode jobs, and audio continuity/offline rights.

- [ ] **Step 2: Author the seven complete analyses**

Every article must quantify bandwidth or storage amplification, state the immutable object/version boundary, and define overload degradation from least to most user-visible.

- [ ] **Step 3: Validate and compare live versus on-demand paths**

Run:

```sh
go test ./internal/content -run TestPresentCasesMeetFullContentContract -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit the family**

```sh
git add cases/video-on-demand cases/live-streaming cases/image-service cases/cdn cases/large-file-transfer cases/transcoding-pipeline cases/music-podcast-streaming
git -c commit.gpgsign=false commit -m "docs: complete media delivery case content"
```

### Task 11: Complete scheduling and control-plane content

**Files:**
- Create: `cases/job-scheduler/{case.yaml,README.md}`
- Create: `cases/dag-workflow/{case.yaml,README.md}`
- Create: `cases/ci-runner/{case.yaml,README.md}`
- Create: `cases/deployment-system/{case.yaml,README.md}`
- Create: `cases/container-orchestrator/{case.yaml,README.md}`
- Create: `cases/configuration-feature-flags/{case.yaml,README.md}`
- Create: `cases/multi-tenant-cloud-control-plane/{case.yaml,README.md}`
- Create: `cases/identity-authorization-service/{case.yaml,README.md}`

**Interfaces:**
- Consumes: the desired-state reconciliation family core.
- Produces: all eight scheduling and control-plane cases.

- [ ] **Step 1: Author manifests around ownership, leases, and convergence**

Differentiate timed job claims, DAG dependency state, untrusted build isolation, rollout safety, placement/reconciliation, configuration evaluation consistency, tenant operation sagas, and authorization policy freshness.

- [ ] **Step 2: Author the eight complete analyses**

Every article must identify desired state, observed state, reconciliation owner, fencing mechanism, and what happens when an old worker returns after its lease expires.

- [ ] **Step 3: Validate and compare control-plane authority boundaries**

Run:

```sh
go test ./internal/content -run TestPresentCasesMeetFullContentContract -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit the family**

```sh
git add cases/job-scheduler cases/dag-workflow cases/ci-runner cases/deployment-system cases/container-orchestrator cases/configuration-feature-flags cases/multi-tenant-cloud-control-plane cases/identity-authorization-service
git -c commit.gpgsign=false commit -m "docs: complete scheduling and control plane content"
```

### Task 12: Complete AI and vector content

**Files:**
- Create: `cases/rag-assistant/{case.yaml,README.md}`
- Create: `cases/code-assistant/{case.yaml,README.md}`
- Create: `cases/vector-database/{case.yaml,README.md}`
- Create: `cases/embedding-index-pipeline/{case.yaml,README.md}`
- Create: `cases/inference-gateway/{case.yaml,README.md}`
- Create: `cases/gpu-scheduler/{case.yaml,README.md}`

**Interfaces:**
- Consumes: the calibrated LLM chat serving analysis.
- Produces: the remaining six AI and vector cases.

- [ ] **Step 1: Author manifests around versioned probabilistic systems**

Differentiate grounded retrieval/citation, repository context/privacy, approximate nearest-neighbor recall, embedding-index compatibility, multi-model admission/batching, and topology-aware GPU allocation.

- [ ] **Step 2: Author the six complete analyses**

Every article must version the relevant model, data, prompt or index inputs; define a quality metric alongside latency and cost; and explain why deterministic infrastructure does not make model output deterministic.

- [ ] **Step 3: Validate and compare the seven AI six-tuples**

Run:

```sh
go test ./internal/content -run TestPresentCasesMeetFullContentContract -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit the family**

```sh
git add cases/rag-assistant cases/code-assistant cases/vector-database cases/embedding-index-pipeline cases/inference-gateway cases/gpu-scheduler
git -c commit.gpgsign=false commit -m "docs: complete AI and vector case content"
```

### Task 13: Add human navigation and dual progress reporting

**Files:**
- Create: `cases/README.md`
- Modify: `README.md`
- Modify: `docs/methodology/evidence.md`

**Interfaces:**
- Consumes: all 75 case directories and the existing generated strict coverage report.
- Produces: a ten-family reader index and unambiguous 75/75 authored versus 1/75 evidence-closed progress.

- [ ] **Step 1: Create the 75-case navigation index**

List the ten families in `scope.yaml` order. Under each family, link every canonical title to its relative `cases/<id>/README.md` path and label only `distributed-rate-limiter` as evidence-closed.

- [ ] **Step 2: Rewrite the root completion section**

Lead readers to `cases/README.md`. State exactly:

```text
题目内容覆盖：75/75
实验与证据闭环：1/75
```

Explain that the first number means a complete first-principles article exists, while the second retains the strict manifest dependency closure. Preserve the existing six-cell W0 evidence table and its limitations.

- [ ] **Step 3: Align the evidence methodology wording**

Clarify that content availability does not imply measured production validity and that the current evidence snapshot certifies only the rate-limiter golden slice.

- [ ] **Step 4: Validate all Markdown links**

Run:

```sh
go run ./cmd/whiteboard validate --root . --content
```

Expected: exit 0 with no diagnostics.

- [ ] **Step 5: Commit navigation and progress wording**

```sh
git add cases/README.md README.md docs/methodology/evidence.md
git -c commit.gpgsign=false commit -m "docs: publish full case library navigation"
```

### Task 14: Run full content, schema, and coverage verification

**Files:**
- Verify: `cases/**`
- Verify: `internal/content/library_content_test.go`
- Verify: `generated/coverage.md`

**Interfaces:**
- Consumes: the complete authored library.
- Produces: verified 75-case content presence with unchanged strict evidence closure.

- [ ] **Step 1: Prove all 75 manifests and strict prose contracts pass**

Run:

```sh
go test ./internal/content -run 'Test(AllScopedCasesHaveAuthoredContent|PresentCasesMeetFullContentContract)' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run the complete Go test suite**

Run:

```sh
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run repository content and link validation**

Run:

```sh
go run ./cmd/whiteboard validate --root . --content
```

Expected: exit 0 with no diagnostics.

- [ ] **Step 4: Confirm strict coverage remains honest**

Run:

```sh
go run ./cmd/whiteboard coverage --root . --format json
```

Expected: `baseline_total` is 75, `complete_total` is 1, `missing_case_ids` has 74 entries, and `unexpected_case_ids` is empty.

- [ ] **Step 5: Confirm the checked-in generated report is current**

Run:

```sh
go run ./cmd/whiteboard coverage --root . --format markdown --output generated/coverage.md --check
```

Expected: exit 0.

### Task 15: Final anti-template audit and source commit

**Files:**
- Review: `cases/*/case.yaml`
- Review: `cases/*/README.md`
- Review: `cases/README.md`
- Review: `README.md`

**Interfaces:**
- Consumes: every authored case and verification result.
- Produces: a review-ready source commit containing the entire content library.

- [ ] **Step 1: Audit exact scope equality**

Compare scope IDs, manifest directory names, and index links. The three sets must each contain exactly 75 identical IDs.

- [ ] **Step 2: Audit claim integrity**

Confirm every manifest has two or more unique case-prefixed claims and each claim appears in its README with one marker class.

- [ ] **Step 3: Audit sibling differentiation**

For every family, compare the six-tuples `核心状态 / 不变量 / 主导放大 / 协调点 / 标志性故障 / 切换指标`. Rewrite cases that share more than three fields without a substantive domain reason.

- [ ] **Step 4: Audit evidence honesty**

Confirm new cases contain no `MEASURED` or `SOURCED` marker and do not reference nonexistent principles, labs, evidence, or sources.

- [ ] **Step 5: Run diff hygiene checks**

Run:

```sh
git diff --check
git status --short
```

Expected: no whitespace errors; status contains only intended content-library changes if earlier task commits were intentionally squashed, otherwise it is clean.

- [ ] **Step 6: Commit any final editorial fixes**

```sh
git add cases README.md docs/methodology/evidence.md internal/content/library_content_test.go
git -c commit.gpgsign=false commit -m "docs: finalize first-principles system design library"
```
