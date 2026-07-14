package tokenbucket

import (
	"errors"
	"math"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

var testEpoch = time.Unix(0, 0).UTC()

func TestNewValidatesConfigurationInOrderAndStartsFull(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		want   error
	}{
		{name: "capacity before interval", config: Config{}, want: ErrInvalidCapacity},
		{name: "zero interval", config: Config{Capacity: 1}, want: ErrInvalidRefillEvery},
		{name: "negative interval", config: Config{Capacity: 1, RefillEvery: -time.Nanosecond}, want: ErrInvalidRefillEvery},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(test.config, testEpoch)
			if !errors.Is(err, test.want) {
				t.Fatalf("New(%#v) error = %v, want errors.Is(_, %v)", test.config, err, test.want)
			}
		})
	}

	bucket, err := New(Config{Capacity: 4, RefillEvery: time.Second}, time.Time{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	available, err := bucket.Available(time.Time{})
	if err != nil {
		t.Fatalf("Available() error = %v", err)
	}
	if available != 4 {
		t.Fatalf("initial available = %d, want 4", available)
	}
}

func TestTakeDepletesExactlyAndValidatesAmountBeforeTime(t *testing.T) {
	bucket := newTestBucket(t, Config{Capacity: 4, RefillEvery: time.Second})

	decision, err := bucket.Take(testEpoch, 4)
	if err != nil {
		t.Fatalf("Take(capacity) error = %v", err)
	}
	wantAllowed := Decision{Allowed: true, Remaining: 0}
	if !reflect.DeepEqual(decision, wantAllowed) {
		t.Fatalf("Take(capacity) = %#v, want %#v", decision, wantAllowed)
	}

	decision, err = bucket.Take(testEpoch, 1)
	if err != nil {
		t.Fatalf("Take(denied) error = %v", err)
	}
	wantDenied := Decision{Allowed: false, Remaining: 0, RetryAfter: time.Second}
	if !reflect.DeepEqual(decision, wantDenied) {
		t.Fatalf("Take(denied) = %#v, want %#v", decision, wantDenied)
	}

	if _, err := bucket.Take(testEpoch.Add(-time.Second), 0); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("zero amount error = %v, want ErrInvalidAmount before time rollback", err)
	}
	if _, err := bucket.Take(testEpoch.Add(-time.Second), 5); !errors.Is(err, ErrAmountExceedsCapacity) {
		t.Fatalf("above-capacity amount error = %v, want ErrAmountExceedsCapacity before time rollback", err)
	}
	available, err := bucket.Available(testEpoch)
	if err != nil {
		t.Fatalf("Available() after invalid amounts error = %v", err)
	}
	if available != 0 {
		t.Fatalf("invalid amounts mutated available to %d, want 0", available)
	}
}

func TestRefillRetainsRemainderAcrossReads(t *testing.T) {
	bucket := newTestBucket(t, Config{Capacity: 4, RefillEvery: time.Second})
	if _, err := bucket.Take(testEpoch, 4); err != nil {
		t.Fatalf("initial Take() error = %v", err)
	}

	decision, err := bucket.Take(testEpoch.Add(time.Second-time.Nanosecond), 1)
	if err != nil {
		t.Fatalf("pre-boundary Take() error = %v", err)
	}
	if want := (Decision{Allowed: false, Remaining: 0, RetryAfter: time.Nanosecond}); !reflect.DeepEqual(decision, want) {
		t.Fatalf("pre-boundary decision = %#v, want %#v", decision, want)
	}

	decision, err = bucket.Take(testEpoch.Add(time.Second), 1)
	if err != nil {
		t.Fatalf("boundary Take() error = %v", err)
	}
	if want := (Decision{Allowed: true, Remaining: 0}); !reflect.DeepEqual(decision, want) {
		t.Fatalf("boundary decision = %#v, want %#v", decision, want)
	}

	checks := []struct {
		at   time.Duration
		want uint64
	}{
		{at: 1500 * time.Millisecond, want: 0},
		{at: 1750 * time.Millisecond, want: 0},
		{at: 2 * time.Second, want: 1},
		{at: 2500 * time.Millisecond, want: 1},
		{at: 3 * time.Second, want: 2},
	}
	for _, check := range checks {
		available, err := bucket.Available(testEpoch.Add(check.at))
		if err != nil {
			t.Fatalf("Available(%s) error = %v", check.at, err)
		}
		if available != check.want {
			t.Fatalf("Available(%s) = %d, want %d", check.at, available, check.want)
		}
	}
}

func TestRollbackWithinIntervalDoesNotMutateState(t *testing.T) {
	bucket := newTestBucket(t, Config{Capacity: 2, RefillEvery: time.Second})
	if _, err := bucket.Take(testEpoch, 2); err != nil {
		t.Fatalf("initial Take() error = %v", err)
	}
	if available, err := bucket.Available(testEpoch.Add(500 * time.Millisecond)); err != nil || available != 0 {
		t.Fatalf("Available(500ms) = %d, %v; want 0, nil", available, err)
	}
	if _, err := bucket.Available(testEpoch.Add(400 * time.Millisecond)); !errors.Is(err, ErrTimeRollback) {
		t.Fatalf("rollback error = %v, want ErrTimeRollback", err)
	}
	if _, err := bucket.Available(testEpoch.Add(450 * time.Millisecond)); !errors.Is(err, ErrTimeRollback) {
		t.Fatalf("rollback changed last observed time: error = %v, want ErrTimeRollback", err)
	}
	available, err := bucket.Available(testEpoch.Add(time.Second))
	if err != nil {
		t.Fatalf("Available(boundary) error = %v", err)
	}
	if available != 1 {
		t.Fatalf("Available(boundary) = %d, want 1", available)
	}
}

func TestDenialCommitsValidTimeObservation(t *testing.T) {
	bucket := newTestBucket(t, Config{Capacity: 2, RefillEvery: time.Second})
	if _, err := bucket.Take(testEpoch, 2); err != nil {
		t.Fatalf("initial Take() error = %v", err)
	}
	decision, err := bucket.Take(testEpoch.Add(1500*time.Millisecond), 2)
	if err != nil {
		t.Fatalf("denied Take() error = %v", err)
	}
	if want := (Decision{Allowed: false, Remaining: 1, RetryAfter: 500 * time.Millisecond}); !reflect.DeepEqual(decision, want) {
		t.Fatalf("denied decision = %#v, want %#v", decision, want)
	}
	if _, err := bucket.Available(testEpoch.Add(1400 * time.Millisecond)); !errors.Is(err, ErrTimeRollback) {
		t.Fatalf("time before denial observation error = %v, want ErrTimeRollback", err)
	}
}

func TestFullBucketDiscardsHistoricalRefillCredit(t *testing.T) {
	bucket := newTestBucket(t, Config{Capacity: 3, RefillEvery: time.Second})
	available, err := bucket.Available(testEpoch.Add(10 * time.Second))
	if err != nil || available != 3 {
		t.Fatalf("Available(10s) = %d, %v; want 3, nil", available, err)
	}
	if decision, err := bucket.Take(testEpoch.Add(10*time.Second), 3); err != nil || !decision.Allowed || decision.Remaining != 0 {
		t.Fatalf("depletion after idle = %#v, %v; want allowed with zero remaining", decision, err)
	}
	decision, err := bucket.Take(testEpoch.Add(10*time.Second), 1)
	if err != nil {
		t.Fatalf("immediate Take() error = %v", err)
	}
	if want := (Decision{Allowed: false, Remaining: 0, RetryAfter: time.Second}); !reflect.DeepEqual(decision, want) {
		t.Fatalf("historical credit was reusable: decision = %#v, want %#v", decision, want)
	}
	decision, err = bucket.Take(testEpoch.Add(11*time.Second), 1)
	if err != nil || !decision.Allowed || decision.Remaining != 0 {
		t.Fatalf("Take(11s) = %#v, %v; want one newly refilled token", decision, err)
	}
}

func TestDenialReportsExactMultiTokenRetryAndSaturatesOverflow(t *testing.T) {
	bucket := newTestBucket(t, Config{Capacity: 5, RefillEvery: time.Second})
	if _, err := bucket.Take(testEpoch, 5); err != nil {
		t.Fatalf("initial Take() error = %v", err)
	}
	decision, err := bucket.Take(testEpoch.Add(1250*time.Millisecond), 3)
	if err != nil {
		t.Fatalf("Take() error = %v", err)
	}
	want := Decision{Allowed: false, Remaining: 1, RetryAfter: 1750 * time.Millisecond}
	if !reflect.DeepEqual(decision, want) {
		t.Fatalf("multi-token denial = %#v, want %#v", decision, want)
	}

	maxDuration := time.Duration(math.MaxInt64)
	largeIntervalBucket, err := New(Config{Capacity: 3, RefillEvery: maxDuration}, testEpoch)
	if err != nil {
		t.Fatalf("New(max interval) error = %v", err)
	}
	if _, err := largeIntervalBucket.Take(testEpoch, 3); err != nil {
		t.Fatalf("initial max-interval Take() error = %v", err)
	}
	decision, err = largeIntervalBucket.Take(testEpoch, 3)
	if err != nil {
		t.Fatalf("max-interval denial error = %v", err)
	}
	if decision.RetryAfter != maxDuration {
		t.Fatalf("overflowing RetryAfter = %s, want saturation at %s", decision.RetryAfter, maxDuration)
	}
}

func TestForwardDurationOverflowAndExtremeCapacityDoNotMutateOrWrap(t *testing.T) {
	maxDuration := time.Duration(math.MaxInt64)
	exactBoundary := newTestBucket(t, Config{Capacity: 2, RefillEvery: maxDuration})
	if _, err := exactBoundary.Take(testEpoch, 2); err != nil {
		t.Fatalf("exact-boundary initial Take() error = %v", err)
	}
	available, err := exactBoundary.Available(testEpoch.Add(maxDuration))
	if err != nil || available != 1 {
		t.Fatalf("representable MaxDuration gap = %d, %v; want one token and no error", available, err)
	}

	bucket := newTestBucket(t, Config{Capacity: 2, RefillEvery: time.Nanosecond})
	if _, err := bucket.Take(testEpoch, 1); err != nil {
		t.Fatalf("initial Take() error = %v", err)
	}
	beyondDuration := testEpoch.Add(time.Duration(math.MaxInt64)).Add(time.Nanosecond)
	if _, err := bucket.Available(beyondDuration); !errors.Is(err, ErrTimeOverflow) {
		t.Fatalf("unrepresentable forward gap error = %v, want ErrTimeOverflow", err)
	}
	available, err = bucket.Available(testEpoch)
	if err != nil || available != 1 {
		t.Fatalf("overflow mutated state: Available(epoch) = %d, %v; want 1, nil", available, err)
	}

	maxCapacity := ^uint64(0)
	extreme := newTestBucket(t, Config{Capacity: maxCapacity, RefillEvery: time.Nanosecond})
	if _, err := extreme.Take(testEpoch, 2); err != nil {
		t.Fatalf("extreme Take() error = %v", err)
	}
	available, err = extreme.Available(testEpoch.Add(10 * time.Nanosecond))
	if err != nil {
		t.Fatalf("extreme Available() error = %v", err)
	}
	if available != maxCapacity {
		t.Fatalf("extreme refill wrapped to %d, want %d", available, maxCapacity)
	}
}

func TestConcurrentSameTimeRequestsAdmitExactlyCapacity(t *testing.T) {
	const (
		capacity = 64
		workers  = 256
	)
	bucket := newTestBucket(t, Config{Capacity: capacity, RefillEvery: time.Second})
	decisions := make([]Decision, workers)
	errorsByWorker := make([]error, workers)
	var wait sync.WaitGroup
	wait.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func(index int) {
			defer wait.Done()
			decisions[index], errorsByWorker[index] = bucket.Take(testEpoch, 1)
		}(worker)
	}
	wait.Wait()

	allowedRemaining := make([]int, 0, capacity)
	denied := 0
	for index, decision := range decisions {
		if errorsByWorker[index] != nil {
			t.Fatalf("worker %d error = %v", index, errorsByWorker[index])
		}
		if decision.Allowed {
			allowedRemaining = append(allowedRemaining, int(decision.Remaining))
			continue
		}
		denied++
		if decision.Remaining != 0 || decision.RetryAfter != time.Second {
			t.Fatalf("denied decision = %#v, want zero remaining and 1s retry", decision)
		}
	}
	if len(allowedRemaining) != capacity || denied != workers-capacity {
		t.Fatalf("allowed=%d denied=%d, want %d and %d", len(allowedRemaining), denied, capacity, workers-capacity)
	}
	sort.Ints(allowedRemaining)
	for index, remaining := range allowedRemaining {
		if remaining != index {
			t.Fatalf("allowed remaining multiset = %#v, want 0..%d", allowedRemaining, capacity-1)
		}
	}
}

func TestAvailableCanRunConcurrentlyWithTake(t *testing.T) {
	const (
		capacity = 100
		takers   = 200
		readers  = 100
	)
	bucket := newTestBucket(t, Config{Capacity: capacity, RefillEvery: time.Second})
	decisions := make([]Decision, takers)
	errorsByTaker := make([]error, takers)
	errorsByReader := make([]error, readers)
	var wait sync.WaitGroup
	wait.Add(takers + readers)
	for index := 0; index < takers; index++ {
		go func(worker int) {
			defer wait.Done()
			decisions[worker], errorsByTaker[worker] = bucket.Take(testEpoch, 1)
		}(index)
	}
	for index := 0; index < readers; index++ {
		go func(worker int) {
			defer wait.Done()
			_, errorsByReader[worker] = bucket.Available(testEpoch)
		}(index)
	}
	wait.Wait()

	allowed := 0
	for index, err := range errorsByTaker {
		if err != nil {
			t.Fatalf("taker %d error = %v", index, err)
		}
		if decisions[index].Allowed {
			allowed++
		}
	}
	for index, err := range errorsByReader {
		if err != nil {
			t.Fatalf("reader %d error = %v", index, err)
		}
	}
	if allowed != capacity {
		t.Fatalf("allowed = %d, want %d", allowed, capacity)
	}
	available, err := bucket.Available(testEpoch)
	if err != nil || available != 0 {
		t.Fatalf("final Available() = %d, %v; want 0, nil", available, err)
	}
}

func newTestBucket(t *testing.T, config Config) *Bucket {
	t.Helper()
	bucket, err := New(config, testEpoch)
	if err != nil {
		t.Fatalf("New(%#v) error = %v", config, err)
	}
	return bucket
}
