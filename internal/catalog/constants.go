package catalog

type FamilyID string

type DimensionID string

var FamilyIDs = []FamilyID{
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

var DimensionIDs = []DimensionID{
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

var familyIDSet = newMembershipSet(FamilyIDs)

var dimensionIDSet = newMembershipSet(DimensionIDs)

func newMembershipSet[T comparable](ids []T) map[T]struct{} {
	set := make(map[T]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}
