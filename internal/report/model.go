package report

import (
	"errors"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

var ErrModelInvalid = errors.New("invalid report model")

type Row struct {
	Cell         validator.MatrixCell            `json:"cell"`
	EvidenceID   string                          `json:"evidence_id"`
	Status       evidence.Status                 `json:"status"`
	Workload     evidence.Workload               `json:"workload"`
	Faults       []evidence.Fault                `json:"faults"`
	Measurements map[string]evidence.Measurement `json:"measurements"`
	Assertions   []evidence.Assertion            `json:"assertions"`
	Environment  evidence.Environment            `json:"environment"`
	Conclusion   string                          `json:"conclusion"`
	Limitations  []string                        `json:"limitations"`
}

type SourceLink struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

type Model struct {
	InputDigest string                 `json:"input_digest"`
	Profile     evidence.Profile       `json:"profile"`
	Coverage    validator.Coverage     `json:"coverage"`
	Rows        []Row                  `json:"rows"`
	Sources     []SourceLink           `json:"sources"`
	Diagnostics []validator.Diagnostic `json:"diagnostics"`
}
