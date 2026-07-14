package harness

import (
	"sync"
	"testing"
	"time"
)

func TestManualClockAdvanceSemanticsAndNormalization(t *testing.T) {
	start := time.Now()
	clock := newManualClock(start)
	wantStart := start.UTC()

	if got := clock.Now(); got != wantStart {
		t.Fatalf("initial time = %#v, want normalized %#v", got, wantStart)
	}
	if err := clock.advance(wantStart); err != nil {
		t.Fatalf("equal advance returned error: %v", err)
	}
	if got := clock.Now(); got != wantStart {
		t.Fatalf("time after equal advance = %#v, want %#v", got, wantStart)
	}

	if err := clock.advance(wantStart.Add(-time.Nanosecond)); err == nil {
		t.Fatal("backward advance returned nil error")
	}
	if got := clock.Now(); got != wantStart {
		t.Fatalf("backward advance mutated clock to %#v, want %#v", got, wantStart)
	}

	foreignLocation := time.FixedZone("foreign", 9*60*60)
	forward := wantStart.Add(5 * time.Nanosecond).In(foreignLocation)
	if err := clock.advance(forward); err != nil {
		t.Fatalf("forward advance returned error: %v", err)
	}
	if got, want := clock.Now(), forward.UTC(); got != want {
		t.Fatalf("forward time = %#v, want normalized %#v", got, want)
	}
}

func TestManualClockSupportsConcurrentReaders(t *testing.T) {
	start := time.Date(2026, time.July, 14, 1, 2, 3, 4, time.UTC)
	clock := newManualClock(start)

	const readers = 16
	const advances = 1_000
	var wait sync.WaitGroup
	wait.Add(readers + 1)
	for reader := 0; reader < readers; reader++ {
		go func() {
			defer wait.Done()
			for index := 0; index < advances; index++ {
				_ = clock.Now()
			}
		}()
	}
	go func() {
		defer wait.Done()
		for index := 1; index <= advances; index++ {
			if err := clock.advance(start.Add(time.Duration(index))); err != nil {
				t.Errorf("advance %d returned error: %v", index, err)
				return
			}
		}
	}()
	wait.Wait()

	if got, want := clock.Now(), start.Add(advances*time.Nanosecond); got != want {
		t.Fatalf("final time = %v, want %v", got, want)
	}
}
