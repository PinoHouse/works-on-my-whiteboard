package catalog

type SchemaVersion uint32

type LifecycleStatus string

const (
	LifecycleStatusDraft    LifecycleStatus = "draft"
	LifecycleStatusComplete LifecycleStatus = "complete"
)

type EntityKind string

const (
	EntityKindCase      EntityKind = "case"
	EntityKindPrinciple EntityKind = "principle"
	EntityKindLab       EntityKind = "lab"
	EntityKindSource    EntityKind = "source"
)

type LabKind string

const (
	LabKindScenario  LabKind = "scenario"
	LabKindPrimitive LabKind = "primitive"
)

type Scope struct {
	SchemaVersion SchemaVersion    `yaml:"schema_version"`
	Families      []ScopeFamily    `yaml:"families"`
	Cases         []ScopeCase      `yaml:"cases"`
	Exclusions    []ScopeExclusion `yaml:"exclusions"`
}

type ScopeFamily struct {
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
}

type ScopeCase struct {
	ID            string `yaml:"id"`
	Title         string `yaml:"title"`
	PrimaryFamily string `yaml:"primary_family"`
}

type ScopeExclusion struct {
	ID              string `yaml:"id"`
	CanonicalCaseID string `yaml:"canonical_case_id"`
	Rationale       string `yaml:"rationale"`
}

type SourcesFile struct {
	SchemaVersion SchemaVersion  `yaml:"schema_version"`
	Sources       []SourceRecord `yaml:"sources"`
}

type SourceRecord struct {
	ID          string `yaml:"id"`
	Title       string `yaml:"title"`
	URL         string `yaml:"url"`
	AccessedAt  string `yaml:"accessed_at"`
	Kind        string `yaml:"kind"`
	LicenseNote string `yaml:"license_note"`
}

type AliasesFile struct {
	SchemaVersion SchemaVersion `yaml:"schema_version"`
	Aliases       []Alias       `yaml:"aliases"`
}

type Alias struct {
	Kind EntityKind `yaml:"kind"`
	From string     `yaml:"from"`
	To   string     `yaml:"to"`
}

type Claim struct {
	ID        string `yaml:"id"`
	Statement string `yaml:"statement"`
}

type EvidenceRequirement struct {
	Claim string `yaml:"claim"`
	Lab   string `yaml:"lab"`
}

type CaseManifest struct {
	SchemaVersion        SchemaVersion         `yaml:"schema_version"`
	ID                   string                `yaml:"id"`
	Title                string                `yaml:"title"`
	PrimaryFamily        string                `yaml:"primary_family"`
	SecondaryFamilies    []string              `yaml:"secondary_families,omitempty"`
	Required             bool                  `yaml:"required"`
	Status               LifecycleStatus       `yaml:"status"`
	Dimensions           []DimensionID         `yaml:"dimensions,omitempty"`
	Principles           []string              `yaml:"principles,omitempty"`
	Claims               []Claim               `yaml:"claims,omitempty"`
	Labs                 []string              `yaml:"labs,omitempty"`
	EvidenceRequirements []EvidenceRequirement `yaml:"evidence_requirements,omitempty"`
	Sources              []string              `yaml:"sources,omitempty"`
}

type PrincipleManifest struct {
	SchemaVersion        SchemaVersion         `yaml:"schema_version"`
	ID                   string                `yaml:"id"`
	Title                string                `yaml:"title"`
	Required             bool                  `yaml:"required"`
	Status               LifecycleStatus       `yaml:"status"`
	Dimensions           []DimensionID         `yaml:"dimensions,omitempty"`
	Claims               []Claim               `yaml:"claims,omitempty"`
	Labs                 []string              `yaml:"labs,omitempty"`
	EvidenceRequirements []EvidenceRequirement `yaml:"evidence_requirements,omitempty"`
	Sources              []string              `yaml:"sources,omitempty"`
}

type CaseBinding struct {
	ID         string   `yaml:"id"`
	CaseID     string   `yaml:"case_id"`
	Claim      string   `yaml:"claim"`
	Workload   string   `yaml:"workload"`
	Assertions []string `yaml:"assertions"`
}

type PrincipleBinding struct {
	ID          string   `yaml:"id"`
	PrincipleID string   `yaml:"principle_id"`
	Claim       string   `yaml:"claim"`
	Workload    string   `yaml:"workload"`
	Assertions  []string `yaml:"assertions"`
}

type AdapterRequirement struct {
	ID       string `yaml:"id"`
	Required bool   `yaml:"required"`
}

type RequiredRun struct {
	ID       string               `yaml:"id"`
	Binding  string               `yaml:"binding"`
	Baseline string               `yaml:"baseline"`
	Variants []string             `yaml:"variants"`
	Workload string               `yaml:"workload"`
	Faults   []string             `yaml:"faults"`
	Adapters []AdapterRequirement `yaml:"adapters,omitempty"`
}

type LabManifest struct {
	SchemaVersion     SchemaVersion      `yaml:"schema_version"`
	ID                string             `yaml:"id"`
	Kind              LabKind            `yaml:"kind"`
	Required          bool               `yaml:"required"`
	Status            LifecycleStatus    `yaml:"status"`
	Implementations   []string           `yaml:"implementations,omitempty"`
	CaseBindings      []CaseBinding      `yaml:"case_bindings,omitempty"`
	PrincipleBindings []PrincipleBinding `yaml:"principle_bindings,omitempty"`
	RequiredRuns      []RequiredRun      `yaml:"required_runs,omitempty"`
	Metrics           []string           `yaml:"metrics,omitempty"`
	Sources           []string           `yaml:"sources,omitempty"`
}

type AdapterManifest struct {
	SchemaVersion SchemaVersion   `yaml:"schema_version"`
	ID            string          `yaml:"id"`
	Title         string          `yaml:"title"`
	Status        LifecycleStatus `yaml:"status"`
	Interface     string          `yaml:"interface"`
	Runtime       string          `yaml:"runtime"`
	Sources       []string        `yaml:"sources,omitempty"`
}

type Catalog struct {
	Scope      Scope
	Sources    map[string]SourceRecord
	Aliases    AliasSet
	Cases      map[string]CaseManifest
	Principles map[string]PrincipleManifest
	Labs       map[string]LabManifest
	Adapters   map[string]AdapterManifest
}
