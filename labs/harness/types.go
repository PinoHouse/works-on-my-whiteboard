package harness

import (
	"context"
	"math/rand"
	"time"
)

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

type Snapshot struct {
	metrics []Metric
}

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
