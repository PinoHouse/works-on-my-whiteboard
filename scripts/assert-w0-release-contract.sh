#!/bin/sh
set -eu

task10_contract_error() {
  printf '%s\n' "$1" >&2
}

task10_contract_physical_directory() (
  CDPATH=
  case "$1" in
    /*) cd -P "$1" 2>/dev/null ;;
    *) cd -P "./$1" 2>/dev/null ;;
  esac
  pwd -P
)

if [ "$#" -ne 1 ]; then
  task10_contract_error "usage: assert-w0-release-contract.sh <repo-root>"
  exit 2
fi

task10_contract_repo_root=$1
if [ -z "$task10_contract_repo_root" ] || [ ! -d "$task10_contract_repo_root" ]; then
  task10_contract_error "repository root is unavailable"
  exit 2
fi

task10_contract_tmp_parent=${TMPDIR:-/tmp}
if [ ! -d "$task10_contract_tmp_parent" ]; then
  task10_contract_error "temporary storage is unavailable"
  exit 2
fi
task10_contract_temporary=$(mktemp -d "$task10_contract_tmp_parent/w0-contract.XXXXXX" 2>/dev/null) || {
  task10_contract_error "temporary contract storage cannot be created"
  exit 2
}
task10_contract_repo_physical=$(task10_contract_physical_directory "$task10_contract_repo_root") || {
  rm -rf "$task10_contract_temporary" 2>/dev/null || :
  task10_contract_error "repository root cannot be resolved safely"
  exit 2
}
task10_contract_temporary_physical=$(task10_contract_physical_directory "$task10_contract_temporary") || {
  rm -rf "$task10_contract_temporary" 2>/dev/null || :
  task10_contract_error "temporary contract storage cannot be resolved safely"
  exit 2
}
case "$task10_contract_temporary_physical/" in
  "$task10_contract_repo_physical/"*)
    rm -rf "$task10_contract_temporary" 2>/dev/null || :
    task10_contract_error "temporary contract storage must be external"
    exit 2
    ;;
esac
task10_contract_child_pid=

task10_contract_remove_temporary() {
  if [ -n "$task10_contract_temporary" ] && ! rm -rf "$task10_contract_temporary" 2>/dev/null; then
    task10_contract_error "temporary contract storage cannot be cleaned"
    return 1
  fi
  task10_contract_temporary=
  return 0
}

task10_contract_on_exit() {
  task10_contract_status=$?
  trap - 0 1 2 3 15
  task10_contract_exit_signal=
  task10_contract_exit_fallback=$task10_contract_status
  case "$task10_contract_status" in
    129) task10_contract_exit_signal=HUP ;;
    130) task10_contract_exit_signal=INT ;;
    131) task10_contract_exit_signal=QUIT ;;
    143) task10_contract_exit_signal=TERM ;;
  esac
  if ! task10_contract_remove_temporary && [ "$task10_contract_status" -eq 0 ]; then
    task10_contract_status=2
  fi
  if [ -n "$task10_contract_exit_signal" ]; then
    kill -s "$task10_contract_exit_signal" "$$"
    exit "$task10_contract_exit_fallback"
  fi
  exit "$task10_contract_status"
}

task10_contract_on_signal() {
  task10_contract_signal=$1
  task10_contract_fallback=$2
  trap - 0 1 2 3 15
  task10_contract_signaled_pid=$task10_contract_child_pid
  task10_contract_child_pid=
  if [ -n "$task10_contract_signaled_pid" ]; then
    kill -s "$task10_contract_signal" "$task10_contract_signaled_pid" 2>/dev/null || :
    wait "$task10_contract_signaled_pid" 2>/dev/null || :
  fi
  task10_contract_remove_temporary || :
  trap - "$task10_contract_signal"
  kill -s "$task10_contract_signal" "$$"
  exit "$task10_contract_fallback"
}

task10_contract_run_child() {
  "$@" &
  task10_contract_child_pid=$!
  task10_contract_child_status=0
  wait "$task10_contract_child_pid" || task10_contract_child_status=$?
  task10_contract_child_pid=
  return "$task10_contract_child_status"
}

trap 'task10_contract_on_exit' 0
trap 'task10_contract_on_signal HUP 129' 1
trap 'task10_contract_on_signal INT 130' 2
trap 'task10_contract_on_signal QUIT 131' 3
trap 'task10_contract_on_signal TERM 143' 15

task10_contract_gate_status=0
task10_contract_run_child make --no-print-directory -C "$task10_contract_repo_root" verify-fast verify-deep audit-evidence || task10_contract_gate_status=$?
if [ "$task10_contract_gate_status" -ne 0 ]; then
  exit "$task10_contract_gate_status"
fi

task10_contract_binary=$task10_contract_repo_root/generated/.bin/whiteboard
if [ ! -x "$task10_contract_binary" ]; then
  task10_contract_error "verified whiteboard executable is unavailable"
  exit 2
fi

task10_contract_stdout=$task10_contract_temporary/validate.stdout
task10_contract_stderr=$task10_contract_temporary/validate.stderr
task10_contract_validate_status=0
task10_contract_run_child "$task10_contract_binary" validate \
  --root "$task10_contract_repo_root" \
  --evidence-root "$task10_contract_repo_root/evidence" \
  --release current \
  --format json \
  >"$task10_contract_stdout" 2>"$task10_contract_stderr" || task10_contract_validate_status=$?

if [ "$task10_contract_validate_status" -eq 0 ]; then
  task10_contract_error "formal release unexpectedly passed"
  exit 1
fi
if [ "$task10_contract_validate_status" -ne 4 ]; then
  exit "$task10_contract_validate_status"
fi
if [ -s "$task10_contract_stdout" ]; then
  task10_contract_error "release validation wrote unexpected standard output"
  exit 1
fi

task10_contract_parser=$task10_contract_temporary/verify_contract.go
if ! cat >"$task10_contract_parser" 2>/dev/null <<'TASK10_CONTRACT_GO'
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
)

const maximumJSONSize = 1 << 20

type diagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type familyCoverage struct {
	ID       string `json:"id"`
	Required int    `json:"required"`
	Complete int    `json:"complete"`
}

type coverage struct {
	BaselineTotal         int              `json:"baseline_total"`
	CompleteTotal         int              `json:"complete_total"`
	MissingCaseIDs        []string         `json:"missing_case_ids"`
	UnexpectedCaseIDs     []string         `json:"unexpected_case_ids"`
	Families              []familyCoverage `json:"families"`
	RequiredPrinciples    []string         `json:"required_principles"`
	RequiredScenarioLabs  []string         `json:"required_scenario_labs"`
	RequiredPrimitiveLabs []string         `json:"required_primitive_labs"`
	RequiredAdapters      []string         `json:"required_adapters"`
}

type matrixCell struct {
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

type report struct {
	Diagnostics []diagnostic `json:"diagnostics"`
	Coverage    coverage     `json:"coverage"`
	Matrix      []matrixCell `json:"matrix"`
}

func main() {
	if len(os.Args) != 2 || verify(os.Args[1]) != nil {
		os.Exit(1)
	}
}

func verify(path string) error {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= 0 || info.Size() > maximumJSONSize {
		return errors.New("invalid size")
	}
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(input)
	decoder.UseNumber()
	if err := scanValue(decoder, 0); err != nil {
		_ = input.Close()
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		_ = input.Close()
		return errors.New("trailing JSON")
	}
	if err := input.Close(); err != nil {
		return err
	}

	input, err = os.Open(path)
	if err != nil {
		return err
	}
	defer input.Close()
	decoder = json.NewDecoder(input)
	decoder.DisallowUnknownFields()
	var actual report
	if err := decoder.Decode(&actual); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	expected := expectedReport()
	if !reflect.DeepEqual(actual, expected) {
		return errors.New("contract mismatch")
	}
	return nil
}

func scanValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("JSON nesting is too deep")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return errors.New("null is forbidden")
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return errors.New("duplicate object key")
			}
			seen[key] = struct{}{}
			if err := scanValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("invalid object close")
		}
	case '[':
		for decoder.More() {
			if err := scanValue(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("invalid array close")
		}
	default:
		return errors.New("unexpected delimiter")
	}
	return nil
}

func expectedReport() report {
	missing := []string{
		"ad-click-aggregation", "ad-serving-ranking", "appointment-booking", "autocomplete", "bank-transfer", "batch-data-pipeline", "cdn", "centralized-logging", "chat-messenger", "ci-runner", "cloud-file-sync", "code-assistant", "collaborative-editor", "comments-reactions", "configuration-feature-flags", "consistent-hash-router", "container-orchestrator", "dag-workflow", "deployment-system", "distributed-cache", "distributed-email-service", "distributed-id", "distributed-log-message-queue", "distributed-sql", "dns-service-discovery", "double-entry-ledger-wallet", "ecommerce-order-inventory", "embedding-index-pipeline", "food-delivery-dispatch", "full-text-search", "gpu-scheduler", "identity-authorization-service", "image-service", "inference-gateway", "job-scheduler", "key-value-store", "large-file-transfer", "leaderboard", "live-comments", "live-streaming", "llm-chat-serving", "load-balancer-api-gateway", "maps-navigation", "metrics-monitoring-alerting", "multi-tenant-cloud-control-plane", "music-podcast-streaming", "nearby-places", "notification-delivery", "object-storage", "online-auction", "pastebin", "payment-system", "photo-sharing", "presence-service", "pubsub", "qa-news-aggregation", "rag-assistant", "recommendation-system", "ride-hailing-dispatch", "social-graph", "social-news-feed", "stream-processing", "ticketing", "time-series-database", "top-k-heavy-hitters", "trading-brokerage", "transcoding-pipeline", "url-shortener", "vector-database", "video-conferencing", "video-on-demand", "web-crawler", "webhook-delivery", "wide-column-document-store",
	}
	message := fmt.Sprintf("release scope incomplete: complete=1 baseline=75 missing=74 unexpected=0 missing_ids=[%s] unexpected_ids=[]", strings.Join(missing, " "))
	return report{
		Diagnostics: []diagnostic{{Code: "release_scope_incomplete", Severity: "error", Message: message}},
		Coverage: coverage{
			BaselineTotal: 75, CompleteTotal: 1, MissingCaseIDs: missing, UnexpectedCaseIDs: []string{},
			Families: []familyCoverage{
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
			},
			RequiredPrinciples: []string{"token-bucket"}, RequiredScenarioLabs: []string{"distributed-rate-limiter"},
			RequiredPrimitiveLabs: []string{"token-bucket"}, RequiredAdapters: []string{},
		},
		Matrix: []matrixCell{
			{LabID: "distributed-rate-limiter", RequiredRunID: "coordinator-outage-policy", BindingID: "distributed-rate-limiter-outage-policy", ClaimID: "distributed-rate-limiter-outage-policy-trades-availability-for-quota", Role: "baseline", ImplementationID: "shared-fail-closed", Workload: "coordinator-outage", Faults: []string{"coordinator-unavailable"}, AssertionIDs: []string{"all-requests-decided", "expected-outage-decision", "expected-outage-availability", "expected-quota-overshoot", "no-unexpected-errors"}},
			{LabID: "distributed-rate-limiter", RequiredRunID: "coordinator-outage-policy", BindingID: "distributed-rate-limiter-outage-policy", ClaimID: "distributed-rate-limiter-outage-policy-trades-availability-for-quota", Role: "variant", ImplementationID: "shared-fail-open", Workload: "coordinator-outage", Faults: []string{"coordinator-unavailable"}, AssertionIDs: []string{"all-requests-decided", "expected-outage-decision", "expected-outage-availability", "expected-quota-overshoot", "no-unexpected-errors"}},
			{LabID: "distributed-rate-limiter", RequiredRunID: "per-node-vs-shared-quota", BindingID: "distributed-rate-limiter-global-quota", ClaimID: "distributed-rate-limiter-per-node-multiplies-global-quota", Role: "variant", ImplementationID: "per-node-token-bucket", Workload: "two-node-burst", Faults: []string{}, AssertionIDs: []string{"all-requests-decided", "expected-allowed-count", "expected-global-quota-overshoot", "no-unexpected-errors"}},
			{LabID: "distributed-rate-limiter", RequiredRunID: "per-node-vs-shared-quota", BindingID: "distributed-rate-limiter-global-quota", ClaimID: "distributed-rate-limiter-per-node-multiplies-global-quota", Role: "baseline", ImplementationID: "shared-token-bucket", Workload: "two-node-burst", Faults: []string{}, AssertionIDs: []string{"all-requests-decided", "expected-allowed-count", "expected-global-quota-overshoot", "no-unexpected-errors"}},
			{LabID: "token-bucket", RequiredRunID: "burst-and-refill-boundary", BindingID: "token-bucket-burst-boundary", ClaimID: "token-bucket-bounds-burst-and-average-rate", Role: "variant", ImplementationID: "token-bucket", Workload: "burst-refill-boundary", Faults: []string{}, AssertionIDs: []string{"initial-burst-bounded", "pre-boundary-denied", "boundary-refills-one", "capacity-never-exceeded", "implementation-matches-reference"}},
			{LabID: "token-bucket", RequiredRunID: "burst-and-refill-boundary", BindingID: "token-bucket-burst-boundary", ClaimID: "token-bucket-bounds-burst-and-average-rate", Role: "baseline", ImplementationID: "token-bucket-reference-model", Workload: "burst-refill-boundary", Faults: []string{}, AssertionIDs: []string{"initial-burst-bounded", "pre-boundary-denied", "boundary-refills-one", "capacity-never-exceeded", "implementation-matches-reference"}},
		},
	}
}
TASK10_CONTRACT_GO
then
  task10_contract_error "contract parser cannot be prepared"
  exit 2
fi

task10_contract_parser_status=0
task10_contract_run_child go run "$task10_contract_parser" "$task10_contract_stderr" >/dev/null 2>&1 || task10_contract_parser_status=$?
case "$task10_contract_parser_status" in
  0) ;;
  129|130|131|143) exit "$task10_contract_parser_status" ;;
  *)
    task10_contract_error "release output does not match the exact W0 contract"
    exit 1
    ;;
esac
