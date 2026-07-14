package catalog

import (
	"strings"
	"testing"
)

func TestAliasSetResolve(t *testing.T) {
	aliases := []Alias{
		{Kind: EntityKindCase, From: "old-case", To: "middle-case"},
		{Kind: EntityKindCase, From: "middle-case", To: "canonical-case"},
		{Kind: EntityKindPrinciple, From: "old-principle", To: "canonical-principle"},
		{Kind: EntityKindLab, From: "old-lab", To: "canonical-lab"},
		{Kind: EntityKindSource, From: "old-source", To: "canonical-source"},
	}
	set, err := NewAliasSet(aliases)
	if err != nil {
		t.Fatalf("NewAliasSet() error = %v", err)
	}

	canonical := canonicalIDs(
		map[string]struct{}{"canonical-case": {}},
		map[string]struct{}{"canonical-principle": {}},
		map[string]struct{}{"canonical-lab": {}},
		map[string]struct{}{"canonical-source": {}},
	)
	if err := set.ValidateCanonical(canonical); err != nil {
		t.Fatalf("ValidateCanonical() error = %v", err)
	}

	tests := []struct {
		name string
		kind EntityKind
		id   string
		want string
	}{
		{name: "case chain", kind: EntityKindCase, id: "old-case", want: "canonical-case"},
		{name: "case direct", kind: EntityKindCase, id: "middle-case", want: "canonical-case"},
		{name: "principle", kind: EntityKindPrinciple, id: "old-principle", want: "canonical-principle"},
		{name: "lab", kind: EntityKindLab, id: "old-lab", want: "canonical-lab"},
		{name: "source", kind: EntityKindSource, id: "old-source", want: "canonical-source"},
		{name: "canonical unchanged", kind: EntityKindCase, id: "canonical-case", want: "canonical-case"},
		{name: "unknown unchanged", kind: EntityKindCase, id: "unknown-case", want: "unknown-case"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := set.Resolve(test.kind, test.id)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got != test.want {
				t.Errorf("Resolve(%q, %q) = %q, want %q", test.kind, test.id, got, test.want)
			}
		})
	}
}

func TestNewAliasSetRejectsInvalidDefinitions(t *testing.T) {
	tests := []struct {
		name    string
		aliases []Alias
		want    string
	}{
		{
			name: "duplicate kind and from",
			aliases: []Alias{
				{Kind: EntityKindCase, From: "old", To: "first"},
				{Kind: EntityKindCase, From: "old", To: "second"},
			},
			want: "duplicate alias",
		},
		{
			name:    "self alias",
			aliases: []Alias{{Kind: EntityKindCase, From: "same", To: "same"}},
			want:    "self-alias",
		},
		{
			name: "cycle",
			aliases: []Alias{
				{Kind: EntityKindLab, From: "first", To: "second"},
				{Kind: EntityKindLab, From: "second", To: "third"},
				{Kind: EntityKindLab, From: "third", To: "first"},
			},
			want: "alias cycle",
		},
		{
			name:    "adapter kind",
			aliases: []Alias{{Kind: EntityKind("adapter"), From: "old", To: "canonical"}},
			want:    "unsupported alias kind",
		},
		{
			name:    "empty from",
			aliases: []Alias{{Kind: EntityKindSource, From: "", To: "canonical"}},
			want:    "empty alias from",
		},
		{
			name:    "empty to",
			aliases: []Alias{{Kind: EntityKindSource, From: "old", To: ""}},
			want:    "empty alias to",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewAliasSet(test.aliases)
			if err == nil {
				t.Fatal("NewAliasSet() error = nil")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error %q does not include %q", err, test.want)
			}
		})
	}
}

func TestNewAliasSetAllowsSameFromAcrossKinds(t *testing.T) {
	set, err := NewAliasSet([]Alias{
		{Kind: EntityKindCase, From: "legacy", To: "canonical-case"},
		{Kind: EntityKindLab, From: "legacy", To: "canonical-lab"},
	})
	if err != nil {
		t.Fatalf("NewAliasSet() error = %v", err)
	}
	if err := set.ValidateCanonical(canonicalIDs(
		map[string]struct{}{"canonical-case": {}},
		nil,
		map[string]struct{}{"canonical-lab": {}},
		nil,
	)); err != nil {
		t.Fatalf("ValidateCanonical() error = %v", err)
	}
}

func TestAliasSetValidateCanonical(t *testing.T) {
	tests := []struct {
		name      string
		alias     Alias
		canonical map[EntityKind]map[string]struct{}
		want      string
	}{
		{
			name:  "alias shadows canonical ID",
			alias: Alias{Kind: EntityKindCase, From: "canonical-case", To: "other-case"},
			canonical: canonicalIDs(
				map[string]struct{}{"canonical-case": {}, "other-case": {}}, nil, nil, nil,
			),
			want: "shadows canonical ID",
		},
		{
			name:  "missing terminal",
			alias: Alias{Kind: EntityKindPrinciple, From: "old", To: "missing"},
			canonical: canonicalIDs(
				nil, map[string]struct{}{"known-principle": {}}, nil, nil,
			),
			want: "missing terminal",
		},
		{
			name:  "terminal exists under another kind",
			alias: Alias{Kind: EntityKindCase, From: "old", To: "principle-only"},
			canonical: canonicalIDs(
				nil, map[string]struct{}{"principle-only": {}}, nil, nil,
			),
			want: "exists as principle",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			set, err := NewAliasSet([]Alias{test.alias})
			if err != nil {
				t.Fatalf("NewAliasSet() error = %v", err)
			}
			err = set.ValidateCanonical(test.canonical)
			if err == nil {
				t.Fatal("ValidateCanonical() error = nil")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error %q does not include %q", err, test.want)
			}
		})
	}
}

func TestAliasSetResolveRejectsUnsupportedKind(t *testing.T) {
	set, err := NewAliasSet(nil)
	if err != nil {
		t.Fatalf("NewAliasSet() error = %v", err)
	}
	_, err = set.Resolve(EntityKind("adapter"), "adapter-id")
	if err == nil || !strings.Contains(err.Error(), "unsupported alias kind") {
		t.Fatalf("Resolve() error = %v, want unsupported alias kind", err)
	}
}

func canonicalIDs(cases, principles, labs, sources map[string]struct{}) map[EntityKind]map[string]struct{} {
	return map[EntityKind]map[string]struct{}{
		EntityKindCase:      cases,
		EntityKindPrinciple: principles,
		EntityKindLab:       labs,
		EntityKindSource:    sources,
	}
}
