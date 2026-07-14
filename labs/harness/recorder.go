package harness

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
)

type Recorder struct {
	mu      sync.RWMutex
	metrics map[string]Metric
}

func NewRecorder() *Recorder {
	return &Recorder{metrics: make(map[string]Metric)}
}

func (r *Recorder) Add(name, unit string, delta int64) error {
	if err := validateMetricIdentity(name, unit); err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("recorder is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	metric, exists := r.metrics[name]
	if exists && metric.Unit != unit {
		return fmt.Errorf("metric %q already uses unit %q, not %q", name, metric.Unit, unit)
	}
	if !exists {
		metric = Metric{Name: name, Unit: unit}
	}
	if (delta > 0 && metric.Value > math.MaxInt64-delta) ||
		(delta < 0 && metric.Value < math.MinInt64-delta) {
		return fmt.Errorf("adding %d to metric %q overflows int64", delta, name)
	}
	metric.Value += delta
	if r.metrics == nil {
		r.metrics = make(map[string]Metric)
	}
	r.metrics[name] = metric
	return nil
}

func (r *Recorder) Set(name, unit string, value int64) error {
	if err := validateMetricIdentity(name, unit); err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("recorder is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if metric, exists := r.metrics[name]; exists && metric.Unit != unit {
		return fmt.Errorf("metric %q already uses unit %q, not %q", name, metric.Unit, unit)
	}
	if r.metrics == nil {
		r.metrics = make(map[string]Metric)
	}
	r.metrics[name] = Metric{Name: name, Unit: unit, Value: value}
	return nil
}

func (r *Recorder) Snapshot() Snapshot {
	if r == nil {
		return Snapshot{metrics: make([]Metric, 0)}
	}
	r.mu.RLock()
	metrics := make([]Metric, 0, len(r.metrics))
	for _, metric := range r.metrics {
		metrics = append(metrics, metric)
	}
	r.mu.RUnlock()
	sort.Slice(metrics, func(left, right int) bool {
		return metrics[left].Name < metrics[right].Name
	})
	return Snapshot{metrics: metrics}
}

func (s Snapshot) Value(name string) (int64, bool) {
	index := sort.Search(len(s.metrics), func(index int) bool {
		return s.metrics[index].Name >= name
	})
	if index == len(s.metrics) || s.metrics[index].Name != name {
		return 0, false
	}
	return s.metrics[index].Value, true
}

func (s Snapshot) copyMetrics() []Metric {
	metrics := make([]Metric, len(s.metrics))
	copy(metrics, s.metrics)
	return metrics
}

func validateMetricIdentity(name, unit string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("metric name must be nonblank")
	}
	if strings.TrimSpace(unit) == "" {
		return fmt.Errorf("metric %q unit must be nonblank", name)
	}
	return nil
}
