# W0 Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the repository contracts, validator CLI, deterministic experiment harness, distributed-rate-limiter golden slice, immutable evidence pipeline, and development CI required to start the 75-case program without weakening the v1.0 release gate.

**Architecture:** YAML manifests load through a strict typed catalog, then a semantic validator computes cross-references, required dependency closure, coverage, and the required run matrix. A deterministic discrete-event harness executes one binding/claim per matrix cell; immutable evidence records and an input digest connect those runs to generated reports. Development checks pass with one complete case, while release validation intentionally fails until all 75 scope entries are complete.

**Tech Stack:** Go 1.26.5; module `github.com/PinoHouse/works-on-my-whiteboard`; `go.yaml.in/yaml/v3 v3.0.4`; `github.com/yuin/goldmark v1.8.4`; GNU-compatible Make; GitHub Actions; Docker only for later required adapters.

---

## Global Constraints

- [ ] Keep explanatory documents in Simplified Chinese. Keep code, identifiers, commit messages, workflow names, and machine-readable diagnostics in English.
- [ ] Model mechanisms with the Go standard library first. A mature product may appear only behind an adapter contract after the mechanism is explicit.
- [ ] Preserve the invariant: one independently recorded required run proves exactly one binding and exactly one claim. Every baseline implementation, required variant, and required adapter gets its own evidence record.
- [ ] Use logical time, stable ordering, fixed seeds, and integer correctness metrics. Wall-clock latency may be diagnostic, never a correctness gate.
- [ ] Let development validation pass with the one completed W0 case. Keep release validation strict: it must fail while 74 scope entries remain incomplete.
- [ ] Treat `scope.yaml`, manifests, Chinese prose, and source records as authored facts. Treat coverage, graph indexes, release snapshots, and reports as generated artifacts.
- [ ] Evidence is append-only and immutable. Never overwrite an existing evidence ID or select an old historical run as a substitute for the current run set.
- [ ] Use Conventional Commits in English. Run the task-local tests before every task commit.

## Locked File Map

```text
.
├── .github/workflows/ci.yml
├── .gitignore
├── LICENSE
├── Makefile
├── README.md
├── aliases.yaml
├── go.mod
├── go.sum
├── scope.yaml
├── sources.yaml
├── cmd/whiteboard/main.go
├── cases/distributed-rate-limiter/{README.md,case.yaml}
├── docs/methodology/evidence.md
├── generated/{coverage.md,.gitkeep}
├── internal/catalog/*.go
├── internal/cli/*.go
├── internal/content/*.go
├── internal/evidence/*.go
├── internal/inputdigest/*.go
├── internal/release/*.go
├── internal/report/*.go
├── internal/validator/*.go
├── labs/harness/*.go
├── labs/primitives/token-bucket/{lab.yaml,bucket.go,run.go}
├── labs/scenarios/distributed-rate-limiter/{lab.yaml,limiter.go,scenario.go}
├── principles/token-bucket/{README.md,principle.yaml}
├── scripts/{verify-experiments.sh,assert-w0-release-contract.sh}
└── evidence/{runs/.gitkeep,releases/.gitkeep}
```

Test files sit beside their implementation. Test fixtures go in package-local `testdata/`. No package may import `internal/cli`; `internal/cli` is the composition edge.

## Task 1: Bootstrap the Toolchain and Repository Contracts

**Files:**

- Create: `go.mod`, `go.sum`, `.gitignore`, `LICENSE`, `README.md`
- Create: `scope.yaml`, `sources.yaml`, `aliases.yaml`
- Create: `internal/catalog/constants.go`
- Test: `internal/catalog/constants_test.go`

- [ ] **Step 1: Install and link the pinned Go toolchain**

Run:

```bash
brew upgrade go
brew link --overwrite go
go version
```

Expected final output contains:

```text
go version go1.26.5 darwin/arm64
```

If Homebrew publishes a newer patch while this plan is executing, keep `go 1.26.0` and update only the `toolchain` patch plus this expected line in the same commit.

- [ ] **Step 2: Write the module contract**

Create `go.mod` exactly as:

```go
module github.com/PinoHouse/works-on-my-whiteboard

go 1.26.0

toolchain go1.26.5

require (
	github.com/yuin/goldmark v1.8.4
	go.yaml.in/yaml/v3 v3.0.4
)
```

Run:

```bash
go mod download
```

Expected: `go.sum` is created and `go mod download` exits 0. Do not run `go mod tidy` until Task 4 has introduced imports for both declared direct dependencies; an earlier tidy would remove the not-yet-used Goldmark requirement.

- [ ] **Step 3: Freeze the catalog constants with a failing test**

In `internal/catalog/constants_test.go`, assert exact set equality and order for these ten family IDs:

```text
addressing-traffic
distributed-storage
feed-social-ranking
realtime-collaboration
search-crawl-geo
media-delivery
transactions-contention
streaming-analytics-observability
scheduling-control-plane
ai-vector
```

Assert the exact twelve dimension IDs:

```text
problem-slo
capacity-cost-skew
contracts-data-invariants
placement-routing-sharding
consistency-ordering-time
concurrency-transactions-idempotency
cache-index-amplification
async-backpressure-fairness
failure-recovery-disaster
observability-release-evolution
security-privacy-multitenancy
evidence-validation
```

Run:

```bash
go test ./internal/catalog -run TestFrozenCatalogConstants
```

Expected: FAIL because `FamilyIDs` and `DimensionIDs` do not exist.

- [ ] **Step 4: Implement the frozen constants**

In `internal/catalog/constants.go`, define typed `FamilyID` and `DimensionID` strings, the ordered exported slices `FamilyIDs` and `DimensionIDs`, and unexported membership maps built without mutation after package initialization.

Run:

```bash
go test ./internal/catalog -run TestFrozenCatalogConstants
```

Expected: PASS.

- [ ] **Step 5: Author the complete scope contract**

Create `scope.yaml` with `schema_version: 1`, a `families` array of ten `{id,title}` records, a `cases` array of 75 `{id,title,primary_family}` records, and an `exclusions` array. The following compact view is the normative primary-family mapping, not the literal YAML shape:

```text
  addressing-traffic: [url-shortener, pastebin, distributed-id, dns-service-discovery, load-balancer-api-gateway, distributed-rate-limiter, consistent-hash-router]
  distributed-storage: [key-value-store, distributed-cache, distributed-sql, wide-column-document-store, object-storage, cloud-file-sync, time-series-database]
  feed-social-ranking: [social-news-feed, photo-sharing, qa-news-aggregation, top-k-heavy-hitters, leaderboard, comments-reactions, recommendation-system, ad-serving-ranking]
  realtime-collaboration: [chat-messenger, notification-delivery, distributed-email-service, webhook-delivery, presence-service, collaborative-editor, live-comments, video-conferencing]
  search-crawl-geo: [web-crawler, autocomplete, full-text-search, social-graph, nearby-places, maps-navigation, ride-hailing-dispatch, food-delivery-dispatch]
  media-delivery: [video-on-demand, live-streaming, image-service, cdn, large-file-transfer, transcoding-pipeline, music-podcast-streaming]
  transactions-contention: [ticketing, appointment-booking, online-auction, payment-system, double-entry-ledger-wallet, trading-brokerage, ecommerce-order-inventory, bank-transfer]
  streaming-analytics-observability: [distributed-log-message-queue, pubsub, ad-click-aggregation, metrics-monitoring-alerting, centralized-logging, stream-processing, batch-data-pipeline]
  scheduling-control-plane: [job-scheduler, dag-workflow, ci-runner, deployment-system, container-orchestrator, configuration-feature-flags, multi-tenant-cloud-control-plane, identity-authorization-service]
  ai-vector: [llm-chat-serving, rag-assistant, code-assistant, vector-database, embedding-index-pipeline, inference-gateway, gpu-scheduler]
```

Represent each list item as a full object with stable ID and Chinese title when implementing the schema; the compact mapping above is the normative ID set. Add exclusions for `bitly`, `twitter`, `whatsapp`, `dropbox`, `youtube`, `uber`, `ticketmaster`, `robinhood`, and `chatgpt`, pointing respectively to canonical cases `url-shortener`, `social-news-feed`, `chat-messenger`, `cloud-file-sync`, `video-on-demand`, `ride-hailing-dispatch`, `ticketing`, `trading-brokerage`, and `llm-chat-serving`; every exclusion includes a Chinese rationale that it is a brand prompt covered by the canonical objective model.

- [ ] **Step 6: Author source and alias roots**

Create the six direct coverage-source records required by design spec section 16: ByteByteGo System Design Interview, Educative Grokking Modern System Design Interview, Hello Interview Problem Breakdowns, System Design Primer, Google SRE Book table of contents, and AWS Well-Architected Six Pillars. Each record has `id`, `title`, direct `https` URL, `kind`, `license_note`, and `accessed_at: 2026-07-14`. Add mechanism-specific primary sources later when a claim first cites them; do not substitute them for this coverage baseline.

Create `aliases.yaml` with `schema_version: 1` and an empty alias list. This file is reserved for future canonical ID renames; brand prompts belong to `scope.yaml.exclusions` and must not be duplicated here. Alias behavior is exercised through Task 2 fixtures.

- [ ] **Step 7: Add repository metadata and verify the baseline**

Use the Apache License 2.0 text in `LICENSE`. Ignore build outputs and temporary evidence, but keep committed release evidence and `.gitkeep` sentinels. Make `README.md` explain the first-principles thesis, 75-case scope, evidence statuses, and that W0 release validation is intentionally incomplete.

Run:

```bash
go test ./...
git diff --check
```

Expected: both exit 0.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum .gitignore LICENSE README.md scope.yaml sources.yaml aliases.yaml internal/catalog
git commit -m "chore: bootstrap repository contracts"
```

## Task 2: Build the Strict Typed Catalog Loader

**Files:**

- Create: `internal/catalog/types.go`, `internal/catalog/decode.go`, `internal/catalog/load.go`, `internal/catalog/alias.go`
- Test: `internal/catalog/decode_test.go`, `internal/catalog/load_test.go`, `internal/catalog/alias_test.go`
- Fixtures: `internal/catalog/testdata/{valid,unknown-field,duplicate-key,multiple-docs,alias-cycle}/**`

- [ ] **Step 1: Specify strict decoding failures**

Write table tests proving that decoding rejects: unknown fields, duplicate YAML keys, multiple documents in one file, unsupported `schema_version`, empty stable IDs, and trailing non-document content. Require diagnostics to include the source path and YAML line when available.

Run:

```bash
go test ./internal/catalog -run 'TestDecodeStrict|TestLoadFS'
```

Expected: FAIL because the loader API does not exist.

- [ ] **Step 2: Define the complete typed model**

Define these public types with YAML tags and explicit fields rather than maps of `any`:

```go
type SchemaVersion uint32
type LifecycleStatus string
type EntityKind string
type LabKind string

type Scope struct {
	SchemaVersion SchemaVersion    `yaml:"schema_version"`
	Families      []ScopeFamily    `yaml:"families"`
	Cases         []ScopeCase      `yaml:"cases"`
	Exclusions    []ScopeExclusion `yaml:"exclusions"`
}

type ScopeFamily struct { ID string `yaml:"id"`; Title string `yaml:"title"` }
type ScopeCase struct { ID string `yaml:"id"`; Title string `yaml:"title"`; PrimaryFamily string `yaml:"primary_family"` }
type ScopeExclusion struct { ID string `yaml:"id"`; CanonicalCaseID string `yaml:"canonical_case_id"`; Rationale string `yaml:"rationale"` }

type SourcesFile struct { SchemaVersion SchemaVersion `yaml:"schema_version"`; Sources []SourceRecord `yaml:"sources"` }
type SourceRecord struct { ID string `yaml:"id"`; Title string `yaml:"title"`; URL string `yaml:"url"`; AccessedAt string `yaml:"accessed_at"`; Kind string `yaml:"kind"`; LicenseNote string `yaml:"license_note"` }
type AliasesFile struct { SchemaVersion SchemaVersion `yaml:"schema_version"`; Aliases []Alias `yaml:"aliases"` }
type Alias struct { Kind EntityKind `yaml:"kind"`; From string `yaml:"from"`; To string `yaml:"to"` }

type Claim struct { ID string `yaml:"id"`; Statement string `yaml:"statement"` }
type EvidenceRequirement struct { Claim string `yaml:"claim"`; Lab string `yaml:"lab"` }

type CaseManifest struct {
	SchemaVersion SchemaVersion `yaml:"schema_version"`; ID string `yaml:"id"`; Title string `yaml:"title"`
	PrimaryFamily string `yaml:"primary_family"`; SecondaryFamilies []string `yaml:"secondary_families,omitempty"`
	Required bool `yaml:"required"`; Status LifecycleStatus `yaml:"status"`; Dimensions []DimensionID `yaml:"dimensions,omitempty"`
	Principles []string `yaml:"principles,omitempty"`; Claims []Claim `yaml:"claims,omitempty"`; Labs []string `yaml:"labs,omitempty"`
	EvidenceRequirements []EvidenceRequirement `yaml:"evidence_requirements,omitempty"`; Sources []string `yaml:"sources,omitempty"`
}

type PrincipleManifest struct {
	SchemaVersion SchemaVersion `yaml:"schema_version"`; ID string `yaml:"id"`; Title string `yaml:"title"`
	Required bool `yaml:"required"`; Status LifecycleStatus `yaml:"status"`; Dimensions []DimensionID `yaml:"dimensions,omitempty"`
	Claims []Claim `yaml:"claims,omitempty"`; Labs []string `yaml:"labs,omitempty"`
	EvidenceRequirements []EvidenceRequirement `yaml:"evidence_requirements,omitempty"`; Sources []string `yaml:"sources,omitempty"`
}

type CaseBinding struct { ID string `yaml:"id"`; CaseID string `yaml:"case_id"`; Claim string `yaml:"claim"`; Workload string `yaml:"workload"`; Assertions []string `yaml:"assertions"` }
type PrincipleBinding struct { ID string `yaml:"id"`; PrincipleID string `yaml:"principle_id"`; Claim string `yaml:"claim"`; Workload string `yaml:"workload"`; Assertions []string `yaml:"assertions"` }
type AdapterRequirement struct { ID string `yaml:"id"`; Required bool `yaml:"required"` }
type RequiredRun struct { ID string `yaml:"id"`; Binding string `yaml:"binding"`; Baseline string `yaml:"baseline"`; Variants []string `yaml:"variants"`; Workload string `yaml:"workload"`; Faults []string `yaml:"faults"`; Adapters []AdapterRequirement `yaml:"adapters,omitempty"` }

type LabManifest struct {
	SchemaVersion SchemaVersion `yaml:"schema_version"`; ID string `yaml:"id"`; Kind LabKind `yaml:"kind"`
	Required bool `yaml:"required"`; Status LifecycleStatus `yaml:"status"`; Implementations []string `yaml:"implementations,omitempty"`
	CaseBindings []CaseBinding `yaml:"case_bindings,omitempty"`; PrincipleBindings []PrincipleBinding `yaml:"principle_bindings,omitempty"`
	RequiredRuns []RequiredRun `yaml:"required_runs,omitempty"`; Metrics []string `yaml:"metrics,omitempty"`; Sources []string `yaml:"sources,omitempty"`
}

type AdapterManifest struct { SchemaVersion SchemaVersion `yaml:"schema_version"`; ID string `yaml:"id"`; Title string `yaml:"title"`; Status LifecycleStatus `yaml:"status"`; Interface string `yaml:"interface"`; Runtime string `yaml:"runtime"`; Sources []string `yaml:"sources,omitempty"` }

type Catalog struct {
	Scope      Scope
	Sources    map[string]SourceRecord
	Aliases    AliasSet
	Cases      map[string]CaseManifest
	Principles map[string]PrincipleManifest
	Labs       map[string]LabManifest
	Adapters   map[string]AdapterManifest
}
```

For readable implementation, place one field per line even though the compact plan declaration groups fields. Freeze lifecycle values to `draft|complete`, lab kinds to `scenario|primitive`, and entity kinds to `case|principle|lab|source`; do not add extension bags. Complete-entry conditional requirements are semantic validation, not pointer tricks in the decoder.

- [ ] **Step 3: Implement strict single-document YAML decoding**

Expose:

```go
func DecodeStrict[T any](path string, data []byte) (T, error)
func LoadDir(ctx context.Context, root string) (*Catalog, error)
func LoadFS(ctx context.Context, fsys fs.FS) (*Catalog, error)
```

Use `yaml.Decoder.KnownFields(true)`, then attempt a second decode and require `io.EOF`. Let YAML duplicate-key errors propagate with path context. Validate `schema_version == 1` before returning a document.

- [ ] **Step 4: Implement deterministic repository discovery**

Load the three root manifests plus `cases/*/case.yaml`, `principles/*/principle.yaml`, `labs/{primitives,scenarios}/*/lab.yaml`, and `labs/adapters/*/adapter.yaml`. Sort paths before decoding. Reject duplicate IDs across the same kind; return empty maps when optional directories do not exist.

- [ ] **Step 5: Implement aliases across all entity kinds**

Expose:

```go
func NewAliasSet(aliases []Alias) (AliasSet, error)
func (a AliasSet) ValidateCanonical(canonical map[EntityKind]map[string]struct{}) error
func (a AliasSet) Resolve(kind EntityKind, id string) (string, error)
```

Support the four alias kinds approved by the design: `case`, `principle`, `lab`, and `source`. Adapter IDs remain strict canonical IDs until a later ADR changes the schema. `NewAliasSet` rejects duplicate `(kind,from)` entries, cycles, and self-aliases, then caches terminal strings. Build the canonical case set from `scope.yaml`, so aliases remain valid before all case manifests exist. After loading canonical IDs, `ValidateCanonical` rejects an alias that shadows a canonical ID, a terminal missing from the same kind, or a target that exists only under another kind. `Resolve` returns the input unchanged when it is already canonical or unknown; reference validation decides whether that final ID exists.

- [ ] **Step 6: Verify and commit**

Run:

```bash
go test ./internal/catalog
go test ./...
git diff --check
```

Expected: all pass.

```bash
git add internal/catalog
git commit -m "feat: add strict catalog loader"
```

## Task 3: Validate Semantics, Coverage, and the Required Run Matrix

**Files:**

- Create: `internal/validator/diagnostic.go`, `internal/validator/validate.go`, `internal/validator/schema.go`, `internal/validator/references.go`, `internal/validator/graph.go`, `internal/validator/coverage.go`, `internal/validator/matrix.go`
- Test: matching `_test.go` files and `internal/validator/testdata/**`

- [ ] **Step 1: Write semantic contract tests**

Test exact diagnostic codes for: invalid stable ID, duplicate scope family ID, duplicate scope case ID, a case manifest outside scope, unknown family/reference, globally duplicate claim ID, invalid source URL/date, dangling source, a complete entity with conditionally required fields empty, a case listing a scenario lab without a matching case binding, a principle listing a primitive lab without a matching principle binding, duplicate binding ID, duplicate required-run ID within a lab, a required run with an empty or ambiguous scalar binding, a binding whose claim is absent or owned by another entity, a binding unused by every required run, a run workload differing from its resolved binding workload, baseline/variant outside implementations, required principle without a primitive lab, missing required adapter, orphaned lab, lifecycle/run-status vocabulary mixing, and alias cycle. Test that legitimate reciprocal owner/lab references are not classified as dependency cycles.

Freeze these machine codes in `diagnostic.go` and assert them literally: `invalid_stable_id`, `duplicate_scope_family`, `duplicate_scope_case`, `case_outside_scope`, `unknown_family`, `unknown_reference`, `duplicate_claim_id`, `invalid_source_url`, `invalid_source_date`, `dangling_source`, `complete_contract_empty`, `missing_case_binding`, `missing_principle_binding`, `duplicate_binding_id`, `duplicate_required_run`, `invalid_run_binding`, `foreign_claim`, `unused_binding`, `binding_workload_mismatch`, `unknown_implementation`, `missing_required_principle_lab`, `missing_required_adapter`, `dependency_incomplete`, `orphaned_lab`, `status_vocabulary_mismatch`, `alias_cycle`, `release_scope_incomplete`, and `release_family_mismatch`.

Run:

```bash
go test ./internal/validator -run TestValidate
```

Expected: FAIL because `Validate` does not exist.

- [ ] **Step 2: Define stable diagnostics and validation modes**

Implement:

```go
type Mode string
const (
	ModeDevelopment Mode = "development"
	ModeRelease     Mode = "release"
)

type Diagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Path     string `json:"path,omitempty"`
	EntityID string `json:"entity_id,omitempty"`
	Message  string `json:"message"`
}

type Report struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
	Coverage    Coverage     `json:"coverage"`
	Matrix      []MatrixCell `json:"matrix"`
}

func Validate(c *catalog.Catalog, mode Mode) Report
```

Sort diagnostics by `(code, path, entity_id, message)`. Development mode permits absent case manifests but not malformed present content. Release mode requires exact equality between baseline scope IDs and complete case IDs.

- [ ] **Step 3: Implement reference and graph validation**

Require every stable ID to match `^[a-z][a-z0-9-]*$`; scope family IDs and scope case IDs are each unique before any set conversion; claim IDs are globally unique; required-run IDs are unique within each lab so registry keys stay unambiguous. Membership in scope itself means required. Source URLs must be direct `https` URLs with a host; `accessed_at` must parse as `YYYY-MM-DD`. Resolve aliases before checking references. A case-to-principle reference contributes to the required-principle closure; it is not a direct binding and needs no reverse case list. Require reciprocal owner/lab edges instead: each case-listed scenario lab has a matching `case_binding`, and each principle-listed primitive lab has a matching `principle_binding`, with exact owner and claim IDs. A scenario lab rejects principle bindings; a primitive lab rejects case bindings. Every binding is consumed by at least one required run; `requiredRun.Workload` must exactly equal its resolved binding's workload; every baseline/variant appears in the lab implementation set. Enforce conditional non-empty fields for complete cases, principles, labs, and adapters rather than accepting empty arrays. In every mode, a complete case or principle must have its entire required principle/lab/adapter dependency closure present and complete; development mode relaxes only absent scope cases. Check source citations and adapter requirements. Only the directed required-dependency graph participates in cycle detection; reciprocal owner/lab pairs are validated as pairs, not traversed twice as a DAG.

- [ ] **Step 4: Compute exact coverage**

Expose:

```go
type Coverage struct {
	BaselineTotal      int      `json:"baseline_total"`
	CompleteTotal      int      `json:"complete_total"`
	MissingCaseIDs     []string `json:"missing_case_ids"`
	UnexpectedCaseIDs []string `json:"unexpected_case_ids"`
	Families           []FamilyCoverage `json:"families"`
	RequiredPrinciples []string `json:"required_principles"`
	RequiredScenarioLabs []string `json:"required_scenario_labs"`
	RequiredPrimitiveLabs []string `json:"required_primitive_labs"`
	RequiredAdapters   []string `json:"required_adapters"`
}

type FamilyCoverage struct {
	ID       string `json:"id"`
	Required int    `json:"required"`
	Complete int    `json:"complete"`
}

func ComputeCoverage(c *catalog.Catalog) Coverage
```

Compare sets, not counts. Dependency closure completeness is enforced in both modes for every present complete owner. Release mode additionally requires every case manifest's `primary_family` to equal its scope record and exact scope/per-family membership equality across all 75 cases. Add a fixture with only `distributed-rate-limiter` complete and assert `BaselineTotal == 75`, `CompleteTotal == 1`, and exactly 74 sorted missing IDs; its complete vertical dependencies must not add a second diagnostic.

- [ ] **Step 5: Build the required run matrix**

Expose:

```go
type MatrixCell struct {
	LabID            string `json:"lab_id"`
	RequiredRunID    string `json:"required_run_id"`
	BindingID        string `json:"binding_id"`
	ClaimID          string `json:"claim_id"`
	Role              string `json:"role"`
	ImplementationID string `json:"implementation_id"`
	AdapterID        string `json:"adapter_id,omitempty"`
	Workload         string `json:"workload"`
	Faults           []string `json:"faults"`
	AssertionIDs     []string `json:"assertion_ids"`
}

func BuildRequiredMatrix(c *catalog.Catalog) ([]MatrixCell, []Diagnostic)
```

Create one cell for the baseline, one per required variant, and one per required adapter. Set `Role` to `baseline`, `variant`, or `adapter`; it is derived metadata and not an additional identity coordinate. `ClaimID` is derived from the run's single scalar binding; required runs never duplicate claim IDs. Copy the required run's workload ID, ordered fault schedule, and binding's required assertion IDs into every cell. For an adapter cell set both `ImplementationID` and `AdapterID` to the adapter ID. An ordered list of faults is one schedule inside a cell, not extra cells. Reject a baseline repeated as a variant, duplicate variants, duplicate adapters, and any duplicate six-field cell key. Sort by all six identity fields, then role as a defensive tie-breaker.

- [ ] **Step 6: Verify and commit**

Run:

```bash
go test ./internal/validator
go test ./...
git diff --check
```

Expected: all pass.

```bash
git add internal/validator
git commit -m "feat: validate catalog coverage and run matrix"
```

## Task 4: Validate Chinese Case Content and Expose Catalog CLI Commands

**Files:**

- Create: `internal/content/headings.go`, `internal/content/tags.go`, `internal/content/links.go`, `internal/content/validate.go`
- Test: `internal/content/headings_test.go`, `internal/content/tags_test.go`, `internal/content/links_test.go`, `internal/content/validate_test.go`
- Create: `internal/cli/app.go`, `internal/cli/validate.go`, `internal/cli/coverage.go`
- Test: `internal/cli/app_test.go`, `internal/cli/validate_test.go`, `internal/cli/coverage_test.go`
- Create: `cmd/whiteboard/main.go`

- [ ] **Step 1: Specify the Markdown contract with failing tests**

Parse Markdown with Goldmark AST, not regular expressions. Require these eight level-two headings exactly once and in this order:

```text
表面题目
反问与边界
客观模型
必然约束
从简单方案演进
设计决定
运行与演进
面试考察本质
```

Test missing, duplicated, renamed, and out-of-order headings. For every section require at least one non-empty paragraph, list, table, indented code block, or fenced code block before the next H2; H3 headings and HTML alone do not satisfy the body rule. Assert diagnostic code `empty_section_body` for a heading-only section even if nested heading text is long. Test that claim markers appearing in prose have one of these exact forms and reference a declared claim:

```text
[ASSUMED:<claim-id>]
[DEDUCED:<claim-id>]
[MEASURED:<claim-id>]
[SOURCED:<claim-id>:<source-id>]
```

Count Unicode runes from prose text nodes after collapsing whitespace; ignore code, HTML comments, and link destinations. Freeze minimum non-code counts by section as: `表面题目=120`, `反问与边界=180`, `客观模型=240`, `必然约束=240`, `从简单方案演进=300`, `设计决定=240`, `运行与演进=240`, `面试考察本质=240`. Reject the unfinished markers `TODO`, `TBD`, `FIXME`, `XXX`, `待补充`, and `待完善` outside ignored nodes. Ignore marker-like text inside fenced code, inline code, HTML comments, and link destinations.

Run:

```bash
go test ./internal/content
```

Expected: FAIL because content validation is absent.

- [ ] **Step 2: Implement AST-based content validation**

Expose:

```go
type Result struct { Diagnostics []validator.Diagnostic }
func ValidateCase(path string, markdown []byte, manifest catalog.CaseManifest, repository *catalog.Catalog) Result
func ValidatePrinciple(path string, markdown []byte, manifest catalog.PrincipleManifest, repository *catalog.Catalog) Result
func ValidateRepository(root string, repository *catalog.Catalog) Result
```

Case validation enforces the eight sections, at least one allowed non-empty body block per section, the frozen minimum non-code character count per section, unfinished-marker rejection, and claim markers. Principle validation enforces the same body-block rule and exactly ordered H2 sections `定义`, `不变量`, `推导`, `失败边界`, `可复用实验`, and `关联题目`, with minimum non-code rune counts `120`, `180`, `180`, `180`, `120`, and `80`. Require every declared claim to appear at least once and never with conflicting classes. An `ASSUMED` marker's containing block must include the literal labels `原因：` and `变化影响：`. Resolve the owner manifest's evidence requirements through `repository.Labs`; every `MEASURED` claim must reach a binding and a required run. Every `SOURCED` marker must name a source that both exists in `repository.Sources` and is listed by the owner manifest. `ValidateRepository` rejects missing relative link targets and missing Markdown heading fragments while ignoring external schemes. Plain `validate` runs structural/semantic checks; `--content` adds content/link checks; `--release` implies content checks. It never accepts `--content` as a no-op.

- [ ] **Step 3: Specify CLI behavior with failing tests**

Use in-process tests with `bytes.Buffer`; do not shell out. Lock these commands and exit codes:

```text
whiteboard validate [--root DIR] [--content] [--release current|INPUT_DIGEST] [--format text|json]
whiteboard coverage [--root DIR] [--format text|json|markdown] [--output PATH] [--check]
```

Exit codes:

```text
0 success
2 argument or load failure
3 development validation failure
4 release validation or audit failure
5 lab execution failure
```

`--check` renders in memory, compares byte-for-byte with `--output`, and never writes. Without `--check`, `--output` uses temp-file-plus-rename. With no output path, write to stdout. Text and Markdown output is deterministic and ends with one newline; JSON uses two-space indentation and one newline.

Run:

```bash
go test ./internal/cli -run 'TestValidateCommand|TestCoverageCommand'
```

Expected: FAIL because `Run` does not exist.

- [ ] **Step 4: Implement CLI composition**

Expose:

```go
func Run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int
```

Use `flag.FlagSet` with `ContinueOnError`; direct parser output to `stderr`. Load the catalog once, always run semantic validation, and run content/link validation only for `--content` or release mode. Print every diagnostic before returning its stable exit code. `cmd/whiteboard/main.go` calls `os.Exit(cli.Run(...))` and contains no business logic.

- [ ] **Step 5: Verify repository-level development behavior**

Run:

```bash
go mod tidy
go test ./internal/content ./internal/cli ./cmd/whiteboard
go run ./cmd/whiteboard validate --root .
go run ./cmd/whiteboard coverage --root . --format json
```

Expected: `go mod tidy` preserves both direct dependencies; the tests pass; validation exits 0 because no present manifest is malformed; coverage reports baseline 75, complete 0, missing 75.

- [ ] **Step 6: Commit**

```bash
git add internal/content internal/cli cmd/whiteboard
git commit -m "feat: add validation and coverage CLI"
```

## Task 5: Build the Deterministic Experiment Harness

**Files:**

- Create: `labs/harness/types.go`, `labs/harness/clock.go`, `labs/harness/recorder.go`, `labs/harness/runner.go`
- Test: `labs/harness/clock_test.go`, `labs/harness/recorder_test.go`, `labs/harness/runner_test.go`

- [ ] **Step 1: Write event ordering and determinism tests**

Test a deliberately shuffled schedule. At equal logical time require event phase order `fault`, `request`, `observe`, then ascending sequence number. Test that two runs with the same seed produce byte-identical normalized observations. Test deadline rejection, duplicate sequence rejection within a phase/time pair, and backward clock movement rejection.

Run:

```bash
go test ./labs/harness
```

Expected: FAIL because the harness types do not exist.

- [ ] **Step 2: Define the run identity and result model**

Implement:

```go
type Phase uint8
const (
	PhaseFault Phase = iota
	PhaseRequest
	PhaseObserve
)

type Clock interface {
	Now() time.Time
}

type Runtime struct {
	Clock    Clock
	Recorder *Recorder
	Random   *rand.Rand
}

type Action func(context.Context, *Runtime) error

type RunSpec struct {
	LabID            string
	RequiredRunID    string
	BindingID        string
	ClaimID          string
	ImplementationID string
	AdapterID        string
	Seed             int64
	Start            time.Time
	Deadline         time.Duration
	Parameters       map[string]int64
	Events           []Event
	Assertions       []Assertion
}

type Event struct {
	At       time.Duration
	Phase    Phase
	Sequence uint64
	Name     string
	Apply    Action
}

type Metric struct {
	Name  string
	Unit  string
	Value int64
}

type Snapshot struct { /* immutable, metrics sorted by name */ }
func (s Snapshot) Value(name string) (int64, bool)

type Recorder struct { /* private metric map and observation log */ }
func NewRecorder() *Recorder
func (r *Recorder) Add(name, unit string, delta int64) error
func (r *Recorder) Set(name, unit string, value int64) error
func (r *Recorder) Snapshot() Snapshot

type Assertion struct {
	ID    string
	Check func(Snapshot) (bool, string)
}

type AssertionResult struct {
	ID      string
	Passed  bool
	Message string
}

type Diagnostic struct {
	Event   string
	Message string
}

type RunStatus string
const (
	StatusPassed RunStatus = "passed"
	StatusFailed RunStatus = "failed"
)

type RunResult struct {
	Status         RunStatus
	StartedAt      time.Time
	FinishedAt     time.Time
	EventsExecuted uint64
	Metrics        []Metric
	Assertions     []AssertionResult
	Diagnostics    []Diagnostic
}

type Runner struct{}
func NewRunner() *Runner
func (r *Runner) Run(context.Context, RunSpec) (RunResult, error)
```

Keep callbacks synchronous. A callback may read logical time, use its injected seeded RNG, and update the recorder, but may not advance the clock.

- [ ] **Step 3: Implement logical time and stable recording**

`ManualClock` starts at a fixed UTC instant, implements the read-only `Clock` interface, and advances only through an unexported runner capability. `Recorder` accepts integer counters, gauges, and ordered observations; it rejects one metric name reused with different units. Its normalized representation sorts metric names and canonicalizes map keys before serialization.

- [ ] **Step 4: Implement the runner**

Validate identity fields, positive deadline, non-negative event offsets, duplicate event keys, and that the last logical event is within the deadline before executing. A parent context or wall-clock timeout is only a hang safeguard, never a performance assertion. Stable-sort a copy of events by `(At, Phase, Sequence)`, advance logical time, execute synchronously, stop on the first error, then evaluate assertions when execution reached that phase. Assertion false returns `StatusFailed` plus an assertion error. Event, context, or internal errors also return `StatusFailed`, a Go error, and diagnostics. The harness never emits status `error`; `skipped`, `flaky`, and `inconclusive` are typed evidence/adapter-layer decisions and never map to passed.

Prohibit `time.Sleep`, tickers, goroutine scheduling, floating-point correctness assertions, and ambient randomness in harness packages. Pass a `rand.Rand` seeded from `RunSpec.Seed` to code that explicitly requests randomness.

- [ ] **Step 5: Verify and commit**

Run:

```bash
go test ./labs/harness -count=100
go test -race ./labs/harness
git diff --check
```

Expected: all pass without flaky ordering.

```bash
git add labs/harness
git commit -m "feat: add deterministic experiment harness"
```

## Task 6: Implement the Reusable Token Bucket Primitive

**Files:**

- Create: `labs/primitives/token-bucket/bucket.go`, `labs/primitives/token-bucket/run.go`, `labs/primitives/token-bucket/lab.yaml`
- Test: `labs/primitives/token-bucket/bucket_test.go`, `labs/primitives/token-bucket/bucket_fuzz_test.go`, `labs/primitives/token-bucket/run_test.go`
- Create: `principles/token-bucket/README.md`, `principles/token-bucket/principle.yaml`

- [ ] **Step 1: Lock token bucket boundary behavior with failing tests**

Test initial capacity, exact depletion, denial with exact `RetryAfter`, refill just before/on/after an interval, multiple elapsed intervals, capacity clamping, a request larger than capacity, zero amount, non-positive interval, zero capacity, backward logical time, and concurrent calls under the race detector. Add `FuzzBucketInvariant`, asserting `0 <= available <= capacity` and admitted tokens never exceed initial capacity plus generated refill for arbitrary monotonic schedules.

Use a fixed UTC epoch in every test. Expected refill is integer interval refill with elapsed remainder retained; no floating point and no fractional token.

Run:

```bash
go test ./labs/primitives/token-bucket
```

Expected: FAIL because `Bucket` is undefined.

- [ ] **Step 2: Implement the primitive API**

Implement exactly:

```go
type Config struct {
	Capacity    uint64
	RefillEvery time.Duration
}

type Decision struct {
	Allowed    bool
	Remaining  uint64
	RetryAfter time.Duration
}

func New(config Config, now time.Time) (*Bucket, error)
func (b *Bucket) Take(now time.Time, amount uint64) (Decision, error)
func (b *Bucket) Available(now time.Time) (uint64, error)
```

Use `sync.Mutex` around bucket state. Reject invalid configuration, zero amount, amount above capacity, and time rollback with named sentinel errors. Refill on every read/write operation and retain the sub-interval remainder by advancing the last-refill timestamp only by whole intervals. Avoid unsigned overflow by saturating before addition: if `refill >= capacity-available`, set `available = capacity`; otherwise add `refill`.

- [ ] **Step 3: Add the primitive lab run**

The required run `burst-and-refill-boundary` binds `token-bucket-burst-boundary`, which resolves to claim `token-bucket-bounds-burst-and-average-rate`. Freeze workload ID `burst-refill-boundary` on both binding and run, implementations `[token-bucket-reference-model, token-bucket]`, baseline `token-bucket-reference-model`, variant `[token-bucket]`, and assertions `[initial-burst-bounded, pre-boundary-denied, boundary-refills-one, capacity-never-exceeded, implementation-matches-reference]`. It must consume capacity, deny the next request, advance to one nanosecond before refill, observe denial, advance one nanosecond, then observe one allowed token. The registry in Task 7 must expose both matrix cells. Record integer metrics only.

- [ ] **Step 4: Author reciprocal principle/lab manifests**

Use IDs:

```text
principle: token-bucket
lab: token-bucket
binding: token-bucket-burst-boundary
claim: token-bucket-bounds-burst-and-average-rate
```

Mark the primitive lab required. The principle lists the primitive lab, and the primitive lab has the reciprocal `principle_binding` to the principle-owned claim. The Chinese README uses the six exact principle H2 sections and minimum counts from Task 4. Mention `distributed-rate-limiter` as a consumer in prose; the machine graph gets its case-to-principle edge when Task 7 adds the case manifest.

- [ ] **Step 5: Verify and commit**

Run:

```bash
go test ./labs/primitives/token-bucket -count=100
go test -race ./labs/primitives/token-bucket
go run ./cmd/whiteboard validate --root .
```

Expected: the tests and repository-wide development validation pass. A complete principle and its complete primitive lab are a closed valid slice even before a case references the principle.

```bash
git add labs/primitives/token-bucket principles/token-bucket
git commit -m "feat: add token bucket primitive"
```

## Task 7: Complete the Distributed Rate Limiter Golden Slice

**Files:**

- Create: `labs/scenarios/distributed-rate-limiter/limiter.go`, `labs/scenarios/distributed-rate-limiter/scenario.go`, `labs/scenarios/distributed-rate-limiter/lab.yaml`
- Test: `labs/scenarios/distributed-rate-limiter/limiter_test.go`, `labs/scenarios/distributed-rate-limiter/scenario_test.go`
- Create: `cases/distributed-rate-limiter/README.md`, `cases/distributed-rate-limiter/case.yaml`
- Create: `internal/experiments/registry.go`
- Test: `internal/experiments/registry_test.go`

- [ ] **Step 1: Write coordinator and policy tests**

Define a coordinator interface and test shared quota, independent per-node quota, unavailable coordinator, fail-open, fail-closed, tenant isolation, stable node ordering, and context cancellation. Only `ErrCoordinatorUnavailable` invokes an outage policy; invalid requests and internal errors propagate unchanged.

Run:

```bash
go test ./labs/scenarios/distributed-rate-limiter
```

Expected: FAIL because `Limiter` is undefined.

- [ ] **Step 2: Implement the limiter mechanism**

Implement:

```go
type Request struct {
	TenantID string
	NodeID   string
	Amount   uint64
}

type Decision struct {
	Allowed    bool
	Degraded   bool
	Reason     string
	Remaining  uint64
	RetryAfter time.Duration
}

type Limiter interface {
	Allow(context.Context, Request) (Decision, error)
}
```

Inject the read-only harness `Clock` into limiter/coordinator constructors; requests never carry an independent timestamp that could diverge from logical event time. Define `Coordinator.Take(ctx, tenantID, amount)` to return a token-bucket decision, plus sentinel `ErrCoordinatorUnavailable`. Provide a shared in-memory coordinator with one bucket per tenant and a per-node implementation with one bucket per `(tenant,node)`. Add fail-open and fail-closed policies. Fail-open returns allowed/degraded with reason `coordinator-unavailable-fail-open`; fail-closed returns denied/degraded with reason `coordinator-unavailable-fail-closed`. Only `ErrCoordinatorUnavailable` invokes a policy; cancellation, invalid input, and internal errors propagate. Do not encode degradation as a silent success.

- [ ] **Step 3: Prove the quota multiplication claim**

Create required run `per-node-vs-shared-quota`, binding `distributed-rate-limiter-global-quota`, derived claim `distributed-rate-limiter-per-node-multiplies-global-quota`, and workload ID `two-node-burst` on both binding and run.

At the same logical time, capacity is 4; nodes `a` and `b` each send four unit requests. Assert:

```text
shared:   requests.total=8, requests.allowed=4, requests.denied=4,
          requests.outage=0, requests.degraded=0, decisions.errors=0,
          quota.nominal_limit=4, quota.overshoot=0
per-node: requests.total=8, requests.allowed=8, requests.denied=0,
          requests.outage=0, requests.degraded=0, decisions.errors=0,
          quota.nominal_limit=4, quota.overshoot=4
```

Because a required run proves one binding/claim, represent shared and per-node as two matrix cells with the same run identity and different implementation IDs, producing two evidence records.

- [ ] **Step 4: Prove the outage-policy trade-off claim**

Create required run `coordinator-outage-policy`, binding `distributed-rate-limiter-outage-policy`, derived claim `distributed-rate-limiter-outage-policy-trades-availability-for-quota`, workload ID `coordinator-outage` on both binding and run, and ordered fault list `[coordinator-unavailable]`.

Use capacity 2 and `refill_every=10s`. At `t=0`, issue two healthy unit requests to exhaust the shared bucket. At `t=100ms`, apply the coordinator-down fault before the same-time request; issue four outage requests at `100ms`, `200ms`, `300ms`, and `400ms`; restore the coordinator at `900ms`, making the outage interval `[100ms,900ms)`. This run proves the outage-policy trade-off, not post-recovery behavior; record that limitation instead of claiming unprobed recovery. Assert every metric below:

```text
fail-closed: requests.total=6, requests.allowed=2, requests.denied=4,
             requests.outage=4, requests.outage_allowed=0,
             requests.outage_denied=4, requests.degraded=4,
             decisions.errors=0, quota.nominal_limit=2, quota.overshoot=0
fail-open:   requests.total=6, requests.allowed=6, requests.denied=0,
             requests.outage=4, requests.outage_allowed=4,
             requests.outage_denied=0, requests.degraded=4,
             decisions.errors=0, quota.nominal_limit=2, quota.overshoot=4
```

Record the exact metric vocabulary:

```text
requests.total
requests.allowed
requests.denied
requests.outage
requests.outage_allowed
requests.outage_denied
requests.degraded
decisions.errors
quota.nominal_limit
quota.overshoot
```

- [ ] **Step 5: Register executable experiments**

Create an explicit registry keyed by `(lab_id, required_run_id, implementation_id, adapter_id)`. Reject duplicate keys at construction. The W0 registry contains exactly six no-adapter keys: two token-bucket cells, two global-quota cells, and two outage-policy cells. It does not read YAML or global process state.

Expose:

```go
type Profile string
const (
	ProfileSmoke Profile = "smoke"
	ProfileDeep  Profile = "deep"
)

type Workload struct {
	ID         string
	Parameters map[string]int64
}

type Fault struct {
	ID       string
	At       time.Duration
	Duration time.Duration
}

type MetricSpec struct {
	ID   string
	Unit string
}

type Definition struct {
	Spec         harness.RunSpec
	Profile      Profile
	Hypothesis   string
	Workload     Workload
	Faults       []Fault
	Measurements []MetricSpec
	Limitations  []string
	Conclude     func(harness.RunResult) string
}

type Factory func(validator.MatrixCell, Profile) (Definition, error)
func Lookup(validator.MatrixCell) (Factory, bool)
```

Every factory sets the requested profile and validates that its generated `RunSpec` exactly matches the cell's six identity fields, workload, ordered faults, and assertion IDs. `Measurements` declares the exact metric ID/unit contract for that definition and must agree with both its manifest and normalized run result; downstream evidence conversion must not infer units. Smoke may use smaller numeric workload parameters but never a different workload/fault/assertion identity; deep parameters are the release contract. Task 9 converts `Definition` and `RunResult` to evidence, requires a non-empty falsifiable hypothesis, calls `Conclude` for both passed and failed outcomes, and preserves limitations and diagnostics.

- [ ] **Step 6: Author the complete manifest and Chinese case file**

`case.yaml` status is `complete`, primary family is `addressing-traffic`, and all twelve applicable dimension IDs are present. It references principle `token-bucket`, declares the two case-owned claims, lists scenario lab `distributed-rate-limiter`, and maps both claims to that lab in `evidence_requirements`. The scenario `lab.yaml` reciprocates with bindings `distributed-rate-limiter-global-quota` and `distributed-rate-limiter-outage-policy`. The global binding assertions are `[all-requests-decided, expected-allowed-count, expected-global-quota-overshoot, no-unexpected-errors]`; the outage binding assertions are `[all-requests-decided, expected-outage-decision, expected-outage-availability, expected-quota-overshoot, no-unexpected-errors]`. Declare implementations `[shared-token-bucket, per-node-token-bucket, shared-fail-closed, shared-fail-open]`; global quota uses baseline `shared-token-bucket` and variant `[per-node-token-bucket]`, while outage policy uses baseline `shared-fail-closed` and variant `[shared-fail-open]`. The complete W0 matrix is therefore exactly six cells including the two primitive cells.

The Chinese README contains the eight exact H2 headings from Task 4. It starts from quota invariant and SLO, derives why local buckets multiply a global budget, compares shared coordination with hierarchical/local allocation, states switching conditions, cites source IDs, labels all claims, explains failure semantics, and includes interview follow-ups. Do not mention Redis as the starting assumption.

- [ ] **Step 7: Verify golden-slice coverage**

Run:

```bash
go test ./labs/scenarios/distributed-rate-limiter ./internal/experiments -count=100
go test -race ./labs/scenarios/distributed-rate-limiter
go run ./cmd/whiteboard validate --root . --content
go run ./cmd/whiteboard coverage --root . --format json
```

Expected: tests and development validation pass; coverage reports `baseline_total: 75`, `complete_total: 1`, and exactly 74 missing IDs.

- [ ] **Step 8: Commit**

```bash
git add cases/distributed-rate-limiter labs/scenarios/distributed-rate-limiter internal/experiments
git commit -m "feat: add distributed rate limiter golden slice"
```

## Task 8: Compute Source Input Digests and Store Immutable Evidence

**Files:**

- Create: `internal/inputdigest/digest.go`, `internal/inputdigest/gitfiles.go`
- Test: `internal/inputdigest/digest_test.go`, `internal/inputdigest/gitfiles_test.go`
- Create: `internal/evidence/model.go`, `internal/evidence/decode.go`, `internal/evidence/id.go`, `internal/evidence/content_digest.go`, `internal/evidence/environment.go`, `internal/evidence/store.go`
- Test: matching `_test.go` files plus `internal/evidence/canonical_fuzz_test.go` containing `FuzzCanonicalRecord`
- Create: `evidence/runs/.gitkeep`, `evidence/releases/.gitkeep`

- [ ] **Step 1: Specify the input digest algorithm**

Write tests with a synthetic entry list proving that digest output is independent of input order, changes on path/index-mode/content changes, distinguishes ambiguous concatenations through length prefixes, and rejects duplicate normalized paths. Add a Git fixture proving changes under `.git/`, `evidence/`, and `generated/` do not affect the digest while tracked source changes do; an untracked source file outside excluded roots, unmerged index stage, tracked symlink, and submodule must be rejected. Prove that changing only working-tree permission bits leaves the digest stable, while `git update-index --chmod=+x` changes it.

Run:

```bash
go test ./internal/inputdigest
```

Expected: FAIL because `ComputeEntries` does not exist.

- [ ] **Step 2: Implement canonical source-tree hashing**

Expose:

```go
type Digest string

type Entry struct {
	Path string
	Mode fs.FileMode
	Data []byte
}

func Compute(ctx context.Context, root string) (Digest, error)
func ComputeEntries(entries []Entry) (Digest, error)
func Parse(value string) (Digest, error)
```

`Compute` runs `git ls-files --stage -z`, parses the index mode and stage for each path, filters exact normalized path prefixes `.git/`, `evidence/`, and `generated/`, accepts only stage 0 modes `100644` and `100755`, and explicitly rejects symlink mode `120000`, submodule mode `160000`, and unmerged stages. The canonical executable bit comes from the Git index, never host filesystem permissions; working-tree bytes provide content. It separately runs `git ls-files -z --others --exclude-standard` and rejects every untracked path outside `evidence/` and `generated/`; otherwise an executable Go input could evade the digest. Before evidence generation, parse porcelain status and reject staged or unstaged changes outside those two excluded roots, so `source_commit` and hashed bytes describe the same source state while first-run/retry evidence files remain allowed. Sort slash-normalized paths, then hash a version prefix plus length-prefixed path, index mode, content length, and bytes using SHA-256. Render as `sha256:<64 lowercase hex>`.

- [ ] **Step 3: Specify evidence identity and immutability**

Test stable matrix-cell key generation and changes for each of the six matrix fields; separately test unique attempt-ID generation with injected clock/entropy, content-digest self-exclusion, environment normalization, first-write success, second-write conflict even when bytes are identical, partial-file cleanup, concurrent writers with one winner, and load-time digest verification. Fuzz canonical JSON encoding and store path validation.

Run:

```bash
go test ./internal/evidence
```

Expected: FAIL because the evidence model and store do not exist.

- [ ] **Step 4: Implement the full evidence record**

Define a closed typed record:

```go
type Status string
type Workload struct { ID string `json:"id"`; Parameters map[string]int64 `json:"parameters"` }
type Fault struct { ID string `json:"id"`; At time.Duration `json:"at"`; Duration time.Duration `json:"duration"` }
type Environment struct { GoVersion string `json:"go_version"`; OS string `json:"os"`; Arch string `json:"arch"`; CPU string `json:"cpu"`; LogicalCPUs int `json:"logical_cpus"` }
type Assertion struct { ID string `json:"id"`; Passed bool `json:"passed"`; Message string `json:"message,omitempty"` }
type Diagnostic struct { Code string `json:"code"`; Event string `json:"event,omitempty"`; Message string `json:"message"` }

type Record struct {
	SchemaVersion    uint32                 `json:"schema_version"`
	ID               string                 `json:"id"`
	LabID            string                 `json:"lab_id"`
	RequiredRunID    string                 `json:"required_run_id"`
	BindingID        string                 `json:"binding_id"`
	ClaimID          string                 `json:"claim_id"`
	ImplementationID string                 `json:"implementation_id"`
	AdapterID        string                 `json:"adapter_id,omitempty"`
	Profile          string                 `json:"profile"`
	Hypothesis       string                 `json:"hypothesis"`
	Workload         Workload               `json:"workload"`
	Faults           []Fault                `json:"faults"`
	Status           Status                 `json:"status"`
	SourceCommit     string                 `json:"source_commit"`
	InputDigest      inputdigest.Digest     `json:"input_digest"`
	Environment      Environment            `json:"environment"`
	Seed             int64                  `json:"seed"`
	Deadline         time.Duration          `json:"deadline"`
	StartedAt        time.Time              `json:"started_at"`
	FinishedAt       time.Time              `json:"finished_at"`
	Parameters       map[string]int64       `json:"parameters"`
	Measurements     map[string]int64       `json:"measurements"`
	Assertions       []Assertion            `json:"assertions"`
	Diagnostics      []Diagnostic           `json:"diagnostics"`
	Conclusion       string                 `json:"conclusion"`
	Limitations      []string               `json:"limitations"`
	ContentDigest    string                 `json:"content_digest"`
}
```

Define `Status` as a closed enum containing only `passed`, `failed`, `skipped`, `flaky`, and `inconclusive`; restrict `Profile` to `smoke|deep`; define `Workload`, `Fault`, `Environment`, `Assertion`, and structured `Diagnostic{Code,Event,Message}` with explicit fields. Normalize UTC timestamps, sorted maps, OS/architecture/Go version, and tool versions. Keep the full six-field matrix identity in every record and expose a deterministic cell-key helper for joins. Generate each attempt ID as `run-<UTC-milliseconds>-<128-bit-random-hex>` through `NewID(now time.Time, entropy io.Reader)` using `crypto/rand.Reader` and `io.ReadFull` in production; tests inject both inputs and cover same-millisecond distinct entropy plus identical-ID store conflict. Repeated and failed attempts therefore coexist instead of colliding. Compute `ContentDigest` over canonical JSON with `ContentDigest` empty; verify it after load to avoid self-reference.

Strict record decoding rejects unknown fields, duplicate JSON object keys at every nesting depth, trailing values, unsupported schema versions, non-UTC timestamps, invalid status/profile/ID/digest values, `NaN`/`Inf` representations, `passed` with any included false assertion, and non-passed records without diagnostics. Release audit checks required assertion completeness. `Store.Get` always runs this decoder and content-digest verification before returning.

- [ ] **Step 5: Implement append-only storage**

Expose:

```go
type Store struct { /* root and filesystem capabilities */ }
func NewStore(root string) (*Store, error)
func (s *Store) Put(ctx context.Context, record Record) error
func (s *Store) Get(ctx context.Context, id string) (Record, error)
func (s *Store) List(ctx context.Context) ([]Record, error)
```

Validate IDs before creating paths. Write canonical JSON to a same-directory temporary file, `fsync` the file, atomically install it with no-replace semantics, `fsync` the directory, and remove the temp file on every failed path. Return `ErrEvidenceExists` whenever the target exists, including byte-identical content. Sort `List` by full matrix identity then evidence ID.

- [ ] **Step 6: Verify and commit**

Run:

```bash
go test ./internal/inputdigest ./internal/evidence -count=20
go test -race ./internal/inputdigest ./internal/evidence
git diff --check
```

Expected: all pass.

```bash
git add internal/inputdigest internal/evidence evidence
git commit -m "feat: add immutable experiment evidence"
```

## Task 9: Run Experiments, Freeze Release Snapshots, and Generate Reports

**Files:**

- Create: `internal/release/model.go`, `internal/release/decode.go`, `internal/release/manifest.go`, `internal/release/audit.go`
- Test: matching `_test.go` files
- Create: `internal/report/model.go`, `internal/report/build.go`, `internal/report/markdown.go`, `internal/report/json.go`, `internal/report/diff.go`
- Test: matching `_test.go` files plus `internal/report/testdata/*.golden`
- Create: `internal/cli/run.go`, `internal/cli/report.go`, `internal/cli/diff.go`
- Test: `internal/cli/run_test.go`, `internal/cli/report_test.go`, `internal/cli/diff_test.go`
- Modify/Test: `internal/cli/validate_test.go`

- [ ] **Step 1: Specify release selection and audit failures**

Test that a release requires exactly one passed deep-profile evidence record for every current matrix cell. Reject missing cells, duplicate selections, failed/inconclusive evidence, smoke profile in a deep snapshot, any mismatch in the six identity fields, workload ID or exact parameter map, ordered fault IDs/times/durations, seed, deadline, or exact required assertion-ID set, missing/extra/duplicate/false required assertions, stale input digest, content-digest mismatch, missing evidence files, one evidence ID reused by multiple cells, unselected extra current-run records, and historical records silently substituted for the current run set. Strict manifest decoding rejects unknown fields, duplicate YAML keys, multiple documents, unsupported schema versions, invalid roles/profiles/digests, and selections outside deterministic matrix order.

Run:

```bash
go test ./internal/release
```

Expected: FAIL because release snapshots are absent.

- [ ] **Step 2: Implement immutable release manifests**

Define:

```go
type Role string
const (
	RoleBaseline Role = "baseline"
	RoleVariant  Role = "variant"
	RoleAdapter  Role = "adapter"
)

type Selection struct {
	Role              Role   `yaml:"role" json:"role"`
	LabID             string `yaml:"lab_id" json:"lab_id"`
	RequiredRunID     string `yaml:"required_run_id" json:"required_run_id"`
	BindingID         string `yaml:"binding_id" json:"binding_id"`
	ClaimID           string `yaml:"claim_id" json:"claim_id"`
	ImplementationID  string `yaml:"implementation_id" json:"implementation_id"`
	AdapterID         string `yaml:"adapter_id,omitempty" json:"adapter_id,omitempty"`
	EvidenceID        string `yaml:"evidence_id" json:"evidence_id"`
	ContentDigest     string `yaml:"content_digest" json:"content_digest"`
}

type ExpectedCell struct {
	Cell     validator.MatrixCell
	Profile  string
	Workload evidence.Workload
	Faults   []evidence.Fault
	Seed     int64
	Deadline time.Duration
}

type Manifest struct {
	SchemaVersion uint32             `yaml:"schema_version" json:"schema_version"`
	InputDigest   inputdigest.Digest `yaml:"input_digest" json:"input_digest"`
	Profile       string             `yaml:"profile" json:"profile"`
	Selections    []Selection        `yaml:"selections" json:"selections"`
}

func Build(input inputdigest.Digest, expected []ExpectedCell, current []evidence.Record) (Manifest, error)
func WriteManifest(root string, manifest Manifest) error
func LoadManifest(root string, digest inputdigest.Digest) (Manifest, error)
func AuditManifest(manifest Manifest, expected []ExpectedCell, store *evidence.Store) []validator.Diagnostic
```

The CLI builds `ExpectedCell` values by resolving each matrix cell through the registry for the requested profile without executing it. `Build` receives records produced by the current command invocation; it never scans history to choose “latest.” A release-root snapshot must have profile `deep`; a smoke snapshot is valid only in an isolated development evidence root and can never satisfy `validate --release`. Store one snapshot per evidence root/input digest at `evidence/releases/sha256-<64-hex>/manifest.yaml` and refuse replacement or profile switching. Use the same temp-file, file `fsync`, atomic no-replace install, and directory `fsync` protocol as evidence records. Test concurrent writers, temp cleanup after partial failure, existing valid snapshot idempotent audit without rewrite, and existing corrupt snapshot rejection. Render a deterministic YAML document with selections in matrix order.

- [ ] **Step 3: Specify deterministic reports**

Build a small catalog/matrix/evidence fixture and golden-test Markdown and JSON. The report contains input digest, coverage, required matrix, selected record statuses, measurements, assertions, conclusions, limitations, and source links. It contains no generation timestamp, absolute workspace path, random map order, ANSI color, or host-specific temporary path.

Run:

```bash
go test ./internal/report
```

Expected: FAIL because rendering is absent.

- [ ] **Step 4: Implement report models and renderers**

Expose:

```go
type Row struct {
	Cell         validator.MatrixCell `json:"cell"`
	EvidenceID   string               `json:"evidence_id"`
	Status       evidence.Status      `json:"status"`
	Measurements map[string]int64     `json:"measurements"`
	Assertions   []evidence.Assertion `json:"assertions"`
	Conclusion   string               `json:"conclusion"`
	Limitations  []string             `json:"limitations"`
}

type SourceLink struct { ID string `json:"id"`; Title string `json:"title"`; URL string `json:"url"` }

type Model struct {
	InputDigest string                 `json:"input_digest"`
	Profile     string                 `json:"profile"`
	Coverage    validator.Coverage     `json:"coverage"`
	Rows        []Row                  `json:"rows"`
	Sources     []SourceLink           `json:"sources"`
	Diagnostics []validator.Diagnostic `json:"diagnostics"`
}

func Build(c *catalog.Catalog, validation validator.Report, manifest release.Manifest, records []evidence.Record) (Model, error)
func WriteMarkdown(w io.Writer, model Model) error
func WriteJSON(w io.Writer, model Model) error
```

Sort all collections by stable IDs. Escape Markdown table cells and serialize JSON with two-space indentation plus one trailing newline.

- [ ] **Step 5: Specify and implement the run command**

Add:

```text
whiteboard run --required --profile smoke|deep --root DIR --evidence-root DIR [--snapshot]
```

The command loads and validates the catalog, computes the current input digest and Git source commit, builds the required matrix, resolves each cell in the explicit experiment registry for the requested profile, executes it, converts `experiments.Definition` plus normalized `RunResult` to an evidence record, and stores every record. The converter verifies profile/identity/workload-parameter/fault/assertion agreement and carries hypothesis, dynamic conclusion, limitations, environment, seed, logical times, and diagnostics. It accumulates the exact current invocation records in memory. With `--snapshot`, first check whether the current snapshot exists: if valid for that profile, audit and exit 0 without running or writing; if corrupt or a different profile, exit 4; if absent, call `release.Build` only on the current invocation slice and write the immutable manifest after all cells pass. Return exit 5 on missing registry entries or any non-passed cell; never write a snapshot after partial failure.

- [ ] **Step 6: Specify and implement report/release CLI behavior**

Add:

```text
whiteboard report --root DIR --evidence-root DIR --release current|INPUT_DIGEST --profile smoke|deep --format markdown|json [--output PATH] [--check]
whiteboard diff --root DIR --left-evidence-root DIR --right-evidence-root DIR --release current|INPUT_DIGEST --profile deep --format markdown|json [--output PATH]
whiteboard validate --root DIR --evidence-root DIR --release current|INPUT_DIGEST
```

Default `--root` to `.`, `--evidence-root` to `<root>/evidence`, and report profile to `deep`. Resolve `current` to the freshly computed input digest. `report` builds expected definitions for the requested profile and audits the exact snapshot, including workload parameters and fault times, before rendering; it can therefore validate a temporary smoke/deep root without enforcing 75-case scope completion. `--check` compares rendered bytes with the named output and does not write. `validate --release` always requires deep profile, runs catalog/content/release-scope validation and evidence audit independently whenever loading succeeded, then emits the complete stably sorted diagnostic set rather than fail-fast masking one side; any release diagnostic uses exit 4.

`diff` always requires the right/current run to audit successfully. If the left committed evidence root has no snapshot for the current input digest, emit a deterministic `no-baseline-for-current-input` report and exit 0; a source-only commit must not fail deep correctness merely because evidence has not entered its second commit yet. Otherwise audit both snapshots against the same deep expected definitions, join records by six-field cell key, strip attempt ID and timestamps, and emit correctness/status/assertion changes plus integer metric deltas and environment changes. Corrupt existing left evidence still fails. Performance deltas never become pass/fail; the artifact is for human regression review. Add golden tests for missing left baseline, stable ordering, added/removed metrics, assertion regression, and ignored attempt metadata.

Add `TestW0ReleaseScopeContract` to `internal/cli/validate_test.go`. Its fixture contains the real 75-ID scope, only the distributed-rate-limiter vertical slice complete, a valid six-cell matrix, six passed records, and a valid temporary snapshot. Invoke `Run` in release mode and assert exit 4, exactly one error diagnostic, code `release_scope_incomplete`, count summary `complete=1 baseline=75 missing=74`, and the exact sorted missing-ID set.

- [ ] **Step 7: Verify end-to-end evidence determinism**

Run the required matrix twice into two temporary evidence roots with the same input digest and seed. Attempt IDs and wall-clock metadata may differ. After projecting each record to matrix identity, logical measurements, assertions, status, conclusion, and limitations, the two normalized result sets must be byte-identical. Separately render a report twice from the same fixed snapshot and require byte-identical JSON and Markdown.

Also integration-test that a smoke snapshot audits only with `--profile smoke`, fails a deep audit, and can never satisfy `validate --release`; a deep snapshot must audit with exact deep parameters, faults, seed, and deadline.

Run:

```bash
go test ./internal/release ./internal/report ./internal/cli -count=20
go test -race ./internal/release ./internal/report ./internal/cli
```

Expected: all pass.

- [ ] **Step 8: Commit**

```bash
git add internal/release internal/report internal/cli
git commit -m "feat: add evidence snapshots and reports"
```

## Task 10: Wire Make Targets, CI, Evidence Documentation, and W0 Verification

**Files:**

- Create: `Makefile`, `.github/workflows/ci.yml`, `docs/methodology/evidence.md`
- Create: `scripts/verify-experiments.sh`, `scripts/assert-w0-release-contract.sh`
- Create: `generated/.gitkeep`
- Modify: `README.md`
- Generate: `generated/coverage.md`
- Generate: `evidence/runs/*.json`, `evidence/releases/*/manifest.yaml`

- [ ] **Step 1: Write the evidence methodology before automation**

Document in Chinese: the four claim classes; one-run/one-binding/one-claim identity; logical time; integer correctness metrics; fixed seeds; input digest exclusions; immutable record and release snapshot rules; current-run-only release selection; status semantics; local environment limitations; and why W0 development validation passes while formal release validation remains red.

- [ ] **Step 2: Add explicit Make targets**

Use shell commands on separate recipe lines. `scripts/verify-experiments.sh` accepts profile, repository root, and an optional artifact directory; it creates a temporary evidence root with `mktemp -d`, installs an EXIT trap that optionally preserves the complete root, runs the six-cell matrix with `--snapshot`, then invokes `whiteboard report` with the same explicit profile to audit it independently of 75-case scope. Deep CI also generates a normalized diff against committed evidence. Required targets:

```make
WHITEBOARD := generated/.bin/whiteboard

.PHONY: build fmt vet unit fuzz race content validate coverage smoke verify-fast verify-deep evidence audit-evidence verify clean

build:
	mkdir -p generated/.bin
	go build -o $(WHITEBOARD) ./cmd/whiteboard

fmt:
	test -z "$$(gofmt -l .)"

vet:
	go vet ./...

unit:
	go test ./...

fuzz:
	go test ./internal/evidence -run '^$$' -fuzz FuzzCanonicalRecord -fuzztime 2s
	go test ./labs/primitives/token-bucket -run '^$$' -fuzz FuzzBucketInvariant -fuzztime 2s

race:
	go test -race ./...

content: build
	$(WHITEBOARD) validate --root . --content

validate: content

coverage: build
	$(WHITEBOARD) coverage --root . --format markdown --output generated/coverage.md --check

smoke: build
	./scripts/verify-experiments.sh smoke "$(CURDIR)"

verify-fast: fmt vet unit fuzz race content coverage smoke

verify-deep: build
	./scripts/verify-experiments.sh deep "$(CURDIR)"

evidence: build
	$(WHITEBOARD) run --required --profile deep --root . --evidence-root evidence --snapshot

audit-evidence: build
	mkdir -p generated/.verify
	$(WHITEBOARD) report --root . --evidence-root evidence --release current --format json --output generated/.verify/report.json
	rm -rf generated/.verify

verify: verify-fast verify-deep audit-evidence
	$(WHITEBOARD) validate --root . --evidence-root evidence --release current --format text

clean:
	rm -rf generated/.bin
	rm -rf generated/.verify
```

Implement the experiment wrapper with this control flow:

```sh
#!/bin/sh
set -eu

profile=$1
repo_root=$2
artifact_root=${3:-}
case "$profile" in
  smoke|deep) ;;
  *) exit 2 ;;
esac

evidence_root=$(mktemp -d)
cleanup() {
  status=$?
  trap - EXIT HUP INT TERM
  if [ -n "$artifact_root" ]; then
    mkdir -p "$artifact_root"
    cp -R "$evidence_root/." "$artifact_root/"
  fi
  rm -rf "$evidence_root"
  exit "$status"
}
trap cleanup EXIT HUP INT TERM

"$repo_root/generated/.bin/whiteboard" run --required --profile "$profile" --root "$repo_root" --evidence-root "$evidence_root" --snapshot
"$repo_root/generated/.bin/whiteboard" report --root "$repo_root" --evidence-root "$evidence_root" --release current --profile "$profile" --format json --output "$evidence_root/report.json"
if [ "$profile" = deep ] && [ -n "$artifact_root" ]; then
  "$repo_root/generated/.bin/whiteboard" diff --root "$repo_root" --left-evidence-root "$repo_root/evidence" --right-evidence-root "$evidence_root" --release current --profile deep --format markdown --output "$evidence_root/diff.md"
fi
```

Ignore `generated/.bin/` in Git; using the built executable preserves CLI exit codes that `go run` may wrap. `verify-experiments.sh` never uses checked-in history to substitute for the current execution; it reads committed evidence only for the informational diff. `evidence` relies on the run command's current-snapshot preflight: valid existing snapshot means audit/no-op, corrupt existing snapshot means failure, absent snapshot means execute and immutably create records/snapshot. `verify` first audits committed current evidence through `report`, then invokes strict release validation; in W0 the final executable command returns 4 with one `release_scope_incomplete` diagnostic containing `complete=1 baseline=75 missing=74` and the 74 sorted IDs. GNU Make itself only promises nonzero because it wraps a failing recipe status.

`scripts/assert-w0-release-contract.sh <repo-root>` first runs `make -C <repo-root> verify-fast verify-deep audit-evidence`, then directly invokes `<repo-root>/generated/.bin/whiteboard validate --root <repo-root> --evidence-root <repo-root>/evidence --release current --format text`. It requires executable exit 4, exactly one error diagnostic, the exact code/count summary above, and all 74 expected IDs; it returns 0 only when that expected-negative contract holds.

Run `chmod +x scripts/verify-experiments.sh scripts/assert-w0-release-contract.sh` and assert both executable bits are tracked; the input digest includes that mode.

- [ ] **Step 3: Generate the initial coverage artifact**

Run once without `--check`:

```bash
go run ./cmd/whiteboard coverage --root . --format markdown --output generated/coverage.md
```

Then run:

```bash
make coverage
```

Expected: both exit 0 and the checked file reports 1/75.

- [ ] **Step 4: Add GitHub Actions development gates**

Create three jobs:

1. `verify-fast` on pull requests and pushes: checkout, setup Go from `go.mod`, then `make verify-fast`.
2. `verify-deep` on scheduled and manual runs: build the CLI, call `scripts/verify-experiments.sh deep "$GITHUB_WORKSPACE" "$RUNNER_TEMP/w0-deep"`, and upload that directory with `if: always()`. The artifact always preserves all records and diagnostics produced before exit; successful runs additionally contain the audited report and normalized diff against committed evidence.
3. `w0-release-contract` on pull requests and pushes: first require `go test ./internal/cli -list '^TestW0ReleaseScopeContract$'` to print that exact test name once, then run `go test ./internal/cli -run '^TestW0ReleaseScopeContract$' -count=1 -v`. The explicit list guard prevents Go's zero-matching-tests success from turning the gate green. That fixed fixture contains one complete case and a valid six-cell temporary snapshot, then asserts the sole release error is `release_scope_incomplete` with 74 IDs. It does not depend on a PR's current committed snapshot, which is naturally stale until the two-phase evidence commit is made.

Do not commit CI-generated evidence. Pin official action major versions and enable Go module caching. The diff is informational: correctness assertion changes are visible, while performance metric deltas never become absolute cross-machine thresholds.

- [ ] **Step 5: Run pre-commit checks that do not create evidence**

Run:

```bash
make fmt
make vet
make unit
make fuzz
make race
make content
make coverage
```

Expected: every target exits 0 with the generated coverage file byte-for-byte current. The smoke evidence target intentionally waits until after the source commit because evidence generation rejects uncommitted source inputs.

- [ ] **Step 6: Commit the source state that evidence will identify**

Run:

```bash
git add Makefile .github/workflows/ci.yml docs/methodology/evidence.md README.md generated scripts
git commit -m "ci: add W0 verification gates"
git status --short
```

Expected: the commit succeeds and status is clean. The evidence command must reject a dirty tracked source or index so that `source_commit` and `input_digest` describe the same source state.

- [ ] **Step 7: Run deep verification and generate current immutable evidence**

Run:

```bash
make verify-fast
make verify-deep
make evidence
```

Expected: each exits 0. `make evidence` writes current immutable W0 records under `evidence/runs/` and one `evidence/releases/sha256-<64-hex>/manifest.yaml`. On a retry for an already complete current digest, the target audits the existing snapshot and exits 0 without invoking `Store.Put`, deleting, or overwriting anything.

- [ ] **Step 8: Audit and commit the generated evidence**

Run:

```bash
make audit-evidence
```

Expected: exit 0, proving the uncommitted snapshot has exactly one passed record for all six current cells with matching workload, faults, assertions, input digest, and content digests.

Run:

```bash
git add evidence
git commit -m "chore: record W0 foundation evidence"
```

Expected: only immutable run records and the matching release snapshot are committed; generated/source inputs are unchanged and `git status --short` is clean.

- [ ] **Step 9: Verify the expected-negative release contract from a clean detached checkout, then push**

Run:

```bash
git diff --check
git status --short
verify_parent=$(mktemp -d)
verify_root="$verify_parent/checkout"
git worktree add --detach "$verify_root" HEAD
"$verify_root/scripts/assert-w0-release-contract.sh" "$verify_root"
git worktree remove "$verify_root"
rmdir "$verify_parent"
git push -u origin feature/w0-foundation
```

Expected: no whitespace errors; status is clean; the detached checkout's snapshot remains current after the evidence-only commit; all positive gates pass there; the sole final release error is `release_scope_incomplete` with 74 IDs; the remote feature branch matches local HEAD.

## Plan Self-Review Checklist

- [x] Every public interface named by one task is implemented before a later task consumes it.
- [x] Every implementation task begins with a failing behavioral test and names the expected failure.
- [x] Every command has a concrete expected outcome; no step says “add validation,” “handle edge cases,” or “fill in later.”
- [x] Development and release modes remain distinct; no test lowers the 75-case release contract.
- [x] The two scenario claims and one primitive claim each have stable binding, claim, lab, run, and implementation identities; W0 emits exactly six matrix cells.
- [x] Baseline, variant, and adapter cells produce independent evidence records.
- [x] Coverage and reports are deterministic for fixed inputs and checked byte-for-byte; unique evidence attempt IDs are intentionally not reproducible.
- [x] Evidence writes and release snapshot writes are immutable under retries and concurrent writers.
- [x] Fast CI uses no Docker or networked adapter. Deep CI exercises every W0 matrix cell in isolation.
- [x] Final positive verification and expected-negative release verification are both recorded before completion is claimed.
