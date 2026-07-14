package catalog

import (
	"slices"
	"testing"
)

func TestFrozenCatalogConstants(t *testing.T) {
	expectedFamilyIDs := []FamilyID{
		"addressing-traffic",
		"distributed-storage",
		"feed-social-ranking",
		"realtime-collaboration",
		"search-crawl-geo",
		"media-delivery",
		"transactions-contention",
		"streaming-analytics-observability",
		"scheduling-control-plane",
		"ai-vector",
	}
	expectedDimensionIDs := []DimensionID{
		"problem-slo",
		"capacity-cost-skew",
		"contracts-data-invariants",
		"placement-routing-sharding",
		"consistency-ordering-time",
		"concurrency-transactions-idempotency",
		"cache-index-amplification",
		"async-backpressure-fairness",
		"failure-recovery-disaster",
		"observability-release-evolution",
		"security-privacy-multitenancy",
		"evidence-validation",
	}

	assertFrozenIDs(t, "family IDs", FamilyIDs, expectedFamilyIDs)
	assertFrozenIDs(t, "dimension IDs", DimensionIDs, expectedDimensionIDs)
	assertMembershipMap(t, "family ID membership", familyIDSet, expectedFamilyIDs)
	assertMembershipMap(t, "dimension ID membership", dimensionIDSet, expectedDimensionIDs)
}

func assertFrozenIDs[T comparable](t *testing.T, name string, got, want []T) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Errorf("%s order = %v, want %v", name, got, want)
	}

	gotSet := make(map[T]struct{}, len(got))
	for _, id := range got {
		gotSet[id] = struct{}{}
	}
	wantSet := make(map[T]struct{}, len(want))
	for _, id := range want {
		wantSet[id] = struct{}{}
	}
	if len(gotSet) != len(wantSet) {
		t.Errorf("%s set size = %d, want %d", name, len(gotSet), len(wantSet))
		return
	}
	for id := range wantSet {
		if _, ok := gotSet[id]; !ok {
			t.Errorf("%s set is missing %v", name, id)
		}
	}
}

func assertMembershipMap[T comparable](t *testing.T, name string, got map[T]struct{}, want []T) {
	t.Helper()

	if len(got) != len(want) {
		t.Errorf("%s size = %d, want %d", name, len(got), len(want))
		return
	}
	for _, id := range want {
		if _, ok := got[id]; !ok {
			t.Errorf("%s is missing %v", name, id)
		}
	}
}
