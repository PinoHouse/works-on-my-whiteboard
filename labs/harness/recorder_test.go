package harness

import (
	"math"
	"reflect"
	"sync"
	"testing"
)

func TestRecorderAddSetAndUnitMismatchAreAtomic(t *testing.T) {
	recorder := NewRecorder()
	if err := recorder.Add("requests.total", "count", 2); err != nil {
		t.Fatalf("Add create returned error: %v", err)
	}
	if err := recorder.Set("requests.total", "count", 7); err != nil {
		t.Fatalf("Set same unit returned error: %v", err)
	}
	if err := recorder.Add("requests.total", "count", -3); err != nil {
		t.Fatalf("Add after Set returned error: %v", err)
	}

	if err := recorder.Add("requests.total", "bytes", 1); err == nil {
		t.Fatal("Add with a different unit returned nil error")
	}
	if err := recorder.Set("requests.total", "seconds", 99); err == nil {
		t.Fatal("Set with a different unit returned nil error")
	}
	if got, ok := recorder.Snapshot().Value("requests.total"); !ok || got != 4 {
		t.Fatalf("value after unit mismatch = (%d, %t), want (4, true)", got, ok)
	}
}

func TestRecorderRejectsBlankNamesAndUnitsWithoutMutation(t *testing.T) {
	tests := []struct {
		name string
		run  func(*Recorder) error
	}{
		{name: "add blank name", run: func(recorder *Recorder) error { return recorder.Add(" \t", "count", 1) }},
		{name: "add blank unit", run: func(recorder *Recorder) error { return recorder.Add("requests", "\n", 1) }},
		{name: "set blank name", run: func(recorder *Recorder) error { return recorder.Set("", "count", 1) }},
		{name: "set blank unit", run: func(recorder *Recorder) error { return recorder.Set("requests", " ", 1) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := NewRecorder()
			if err := test.run(recorder); err == nil {
				t.Fatal("operation returned nil error")
			}
			if got := recorder.Snapshot().metrics; len(got) != 0 {
				t.Fatalf("invalid operation mutated recorder: %#v", got)
			}
		})
	}
}

func TestRecorderRejectsOverflowAndUnderflowWithoutMutation(t *testing.T) {
	tests := []struct {
		name  string
		value int64
		delta int64
	}{
		{name: "overflow", value: math.MaxInt64, delta: 1},
		{name: "underflow", value: math.MinInt64, delta: -1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := NewRecorder()
			if err := recorder.Set("edge", "count", test.value); err != nil {
				t.Fatalf("Set returned error: %v", err)
			}
			if err := recorder.Add("edge", "count", test.delta); err == nil {
				t.Fatal("overflowing Add returned nil error")
			}
			if got, ok := recorder.Snapshot().Value("edge"); !ok || got != test.value {
				t.Fatalf("value after rejected Add = (%d, %t), want (%d, true)", got, ok, test.value)
			}
		})
	}
}

func TestRecorderSnapshotIsSortedAndDeepCopied(t *testing.T) {
	recorder := NewRecorder()
	for _, metric := range []Metric{
		{Name: "z.requests", Unit: "count", Value: 3},
		{Name: "a.requests", Unit: "count", Value: 1},
		{Name: "m.requests", Unit: "count", Value: 2},
	} {
		if err := recorder.Set(metric.Name, metric.Unit, metric.Value); err != nil {
			t.Fatalf("Set(%q) returned error: %v", metric.Name, err)
		}
	}

	snapshot := recorder.Snapshot()
	want := []Metric{
		{Name: "a.requests", Unit: "count", Value: 1},
		{Name: "m.requests", Unit: "count", Value: 2},
		{Name: "z.requests", Unit: "count", Value: 3},
	}
	if !reflect.DeepEqual(snapshot.metrics, want) {
		t.Fatalf("snapshot metrics = %#v, want %#v", snapshot.metrics, want)
	}

	snapshot.metrics[0].Value = 999
	if got, ok := recorder.Snapshot().Value("a.requests"); !ok || got != 1 {
		t.Fatalf("mutating snapshot changed recorder = (%d, %t), want (1, true)", got, ok)
	}
	if err := recorder.Set("a.requests", "count", 7); err != nil {
		t.Fatalf("Set after snapshot returned error: %v", err)
	}
	if snapshot.metrics[0].Value != 999 {
		t.Fatalf("recorder mutation changed old snapshot to %d", snapshot.metrics[0].Value)
	}
}

func TestZeroSnapshotAndMissingMetric(t *testing.T) {
	var snapshot Snapshot
	if got, ok := snapshot.Value("missing"); ok || got != 0 {
		t.Fatalf("zero snapshot lookup = (%d, %t), want (0, false)", got, ok)
	}
	if got, ok := NewRecorder().Snapshot().Value("missing"); ok || got != 0 {
		t.Fatalf("missing lookup = (%d, %t), want (0, false)", got, ok)
	}
}

func TestRecorderSupportsConcurrentUpdatesAndSnapshots(t *testing.T) {
	recorder := NewRecorder()
	const writers = 16
	const updates = 1_000
	var wait sync.WaitGroup
	wait.Add(writers + 1)
	for writer := 0; writer < writers; writer++ {
		go func() {
			defer wait.Done()
			for update := 0; update < updates; update++ {
				if err := recorder.Add("requests.total", "count", 1); err != nil {
					t.Errorf("Add returned error: %v", err)
					return
				}
			}
		}()
	}
	go func() {
		defer wait.Done()
		for update := 0; update < updates; update++ {
			_ = recorder.Snapshot()
		}
	}()
	wait.Wait()

	if got, ok := recorder.Snapshot().Value("requests.total"); !ok || got != writers*updates {
		t.Fatalf("concurrent total = (%d, %t), want (%d, true)", got, ok, writers*updates)
	}
}
