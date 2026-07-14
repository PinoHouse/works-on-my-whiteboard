package tokenbucket

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func FuzzBucketInvariant(f *testing.F) {
	f.Add(uint8(4), uint16(1000), []byte{0, 4, 3, 1, 7, 2, 255, 3})
	f.Add(uint8(1), uint16(1), []byte{1, 1, 1, 1, 1, 1})
	f.Add(uint8(16), uint16(65535), []byte{255, 16, 0, 1, 128, 15})
	f.Add(uint8(5), uint16(17), []byte{4, 0, 8, 1, 12, 2, 16, 3})

	f.Fuzz(func(t *testing.T, capacitySeed uint8, refillSeed uint16, operations []byte) {
		capacity := uint64(capacitySeed%16) + 1
		refillNanos := int64(refillSeed%10000) + 1
		refillEvery := time.Duration(refillNanos)
		bucket, err := New(Config{Capacity: capacity, RefillEvery: refillEvery}, testEpoch)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		model := integerBucketModel{
			capacity:    capacity,
			available:   capacity,
			refillNanos: refillNanos,
		}

		var nowNanos int64
		var admitted uint64
		steps := len(operations) / 2
		if steps > 64 {
			steps = 64
		}
		for index := 0; index < steps; index++ {
			timeByte := operations[index*2]
			amountByte := operations[index*2+1]
			wholeIntervals := int64(timeByte % 4)
			fraction := int64(timeByte^0x5a) % refillNanos
			nowNanos += wholeIntervals*refillNanos + fraction
			amount := uint64(amountByte)%capacity + 1
			switch amountByte % 8 {
			case 0:
				if _, err := bucket.Take(testEpoch.Add(time.Duration(nowNanos)), 0); !errors.Is(err, ErrInvalidAmount) {
					t.Fatalf("step %d zero amount error = %v, want ErrInvalidAmount", index, err)
				}
				assertFuzzAvailableMatchesModel(t, bucket, model, index)
				continue
			case 1:
				if _, err := bucket.Take(testEpoch.Add(time.Duration(nowNanos)), capacity+1); !errors.Is(err, ErrAmountExceedsCapacity) {
					t.Fatalf("step %d above-capacity error = %v, want ErrAmountExceedsCapacity", index, err)
				}
				assertFuzzAvailableMatchesModel(t, bucket, model, index)
				continue
			case 2:
				rollbackNanos := model.lastObservedNanos - 1
				if _, err := bucket.Take(testEpoch.Add(time.Duration(rollbackNanos)), amount); !errors.Is(err, ErrTimeRollback) {
					t.Fatalf("step %d rollback error = %v, want ErrTimeRollback", index, err)
				}
				assertFuzzAvailableMatchesModel(t, bucket, model, index)
				continue
			}

			want := model.take(nowNanos, amount)
			got, err := bucket.Take(testEpoch.Add(time.Duration(nowNanos)), amount)
			if err != nil {
				t.Fatalf("step %d Take(now=%dns, amount=%d) error = %v", index, nowNanos, amount, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("step %d Take(now=%dns, amount=%d) = %#v, want independent model %#v", index, nowNanos, amount, got, want)
			}
			if got.Remaining > capacity {
				t.Fatalf("step %d remaining = %d, capacity = %d", index, got.Remaining, capacity)
			}
			if got.Allowed {
				admitted += amount
			}
			generatedBudget := capacity + uint64(nowNanos/refillNanos)
			if admitted > generatedBudget {
				t.Fatalf("step %d admitted = %d, initial-plus-refill budget = %d", index, admitted, generatedBudget)
			}

			assertFuzzAvailableMatchesModel(t, bucket, model, index)
		}
	})
}

// integerBucketModel is an intentionally independent, bounded arithmetic oracle.
// It stores logical time as integer nanoseconds and does not call production code.
type integerBucketModel struct {
	capacity          uint64
	available         uint64
	refillNanos       int64
	anchorNanos       int64
	lastObservedNanos int64
}

func (m *integerBucketModel) take(nowNanos int64, amount uint64) Decision {
	elapsed := nowNanos - m.anchorNanos
	whole := uint64(elapsed / m.refillNanos)
	remainder := elapsed % m.refillNanos
	if whole >= m.capacity-m.available {
		m.available = m.capacity
	} else {
		m.available += whole
	}
	m.anchorNanos += int64(whole) * m.refillNanos
	m.lastObservedNanos = nowNanos

	if m.available >= amount {
		m.available -= amount
		return Decision{Allowed: true, Remaining: m.available}
	}
	need := amount - m.available
	retryNanos := (m.refillNanos - remainder) + int64(need-1)*m.refillNanos
	return Decision{
		Allowed:    false,
		Remaining:  m.available,
		RetryAfter: time.Duration(retryNanos),
	}
}

func assertFuzzAvailableMatchesModel(t *testing.T, bucket *Bucket, model integerBucketModel, step int) {
	t.Helper()
	available, err := bucket.Available(testEpoch.Add(time.Duration(model.lastObservedNanos)))
	if err != nil {
		t.Fatalf("step %d Available(last observed) error = %v", step, err)
	}
	if available != model.available {
		t.Fatalf("step %d Available(last observed) = %d, want model %d", step, available, model.available)
	}
}
