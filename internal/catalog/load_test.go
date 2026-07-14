package catalog

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoadFS(t *testing.T) {
	catalog, err := LoadFS(context.Background(), os.DirFS("testdata/valid"))
	if err != nil {
		t.Fatalf("LoadFS() error = %v", err)
	}

	if catalog.Scope.SchemaVersion != 1 || len(catalog.Scope.Cases) != 2 {
		t.Errorf("Scope = %+v", catalog.Scope)
	}
	assertMapKeys(t, "sources", catalog.Sources, "source-id")
	assertMapKeys(t, "cases", catalog.Cases, "case-id")
	assertMapKeys(t, "principles", catalog.Principles, "principle-id")
	assertMapKeys(t, "labs", catalog.Labs, "primitive-lab", "scenario-lab")
	assertMapKeys(t, "adapters", catalog.Adapters, "adapter-id")

	resolved, err := catalog.Aliases.Resolve(EntityKindCase, "legacy-case")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved != "scope-only-case" {
		t.Errorf("Resolve() = %q, want scope-only-case", resolved)
	}
}

func TestLoadDir(t *testing.T) {
	catalog, err := LoadDir(context.Background(), "testdata/valid")
	if err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}
	if _, exists := catalog.Cases["case-id"]; !exists {
		t.Error("LoadDir() catalog is missing case-id")
	}
}

func TestLoadFSReturnsNonNilEmptyOptionalMaps(t *testing.T) {
	catalog, err := LoadFS(context.Background(), minimalCatalogFS())
	if err != nil {
		t.Fatalf("LoadFS() error = %v", err)
	}

	if catalog.Sources == nil || catalog.Cases == nil || catalog.Principles == nil || catalog.Labs == nil || catalog.Adapters == nil {
		t.Fatalf("LoadFS() returned a nil map: %+v", catalog)
	}
	if len(catalog.Sources)+len(catalog.Cases)+len(catalog.Principles)+len(catalog.Labs)+len(catalog.Adapters) != 0 {
		t.Fatalf("LoadFS() optional maps are not empty: %+v", catalog)
	}
}

func TestLoadFSRejectsDuplicateIDsInSortedPathOrder(t *testing.T) {
	fsys := minimalCatalogFS()
	fsys["cases/z-last/case.yaml"] = yamlFile(`
schema_version: 1
id: duplicate-case
title: Last
primary_family: family-id
required: false
status: draft
`)
	fsys["cases/a-first/case.yaml"] = yamlFile(`
schema_version: 1
id: duplicate-case
title: First
primary_family: family-id
required: false
status: draft
`)

	_, err := LoadFS(context.Background(), fsys)
	if err == nil {
		t.Fatal("LoadFS() error = nil")
	}
	for _, want := range []string{"duplicate case ID", "cases/z-last/case.yaml", "cases/a-first/case.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not include %q", err, want)
		}
	}
}

func TestLoadFSRejectsDuplicateSourceIDs(t *testing.T) {
	fsys := minimalCatalogFS()
	fsys["sources.yaml"] = yamlFile(`
schema_version: 1
sources:
  - id: duplicate-source
    title: First
    url: https://example.com/first
    accessed_at: 2026-07-14
    kind: guide
    license_note: first
  - id: duplicate-source
    title: Second
    url: https://example.com/second
    accessed_at: 2026-07-14
    kind: guide
    license_note: second
`)

	_, err := LoadFS(context.Background(), fsys)
	if err == nil {
		t.Fatal("LoadFS() error = nil")
	}
	for _, want := range []string{"sources.yaml", "duplicate source ID", "duplicate-source"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not include %q", err, want)
		}
	}
}

func TestLoadFSRejectsDuplicateLabIDsAcrossDirectories(t *testing.T) {
	fsys := minimalCatalogFS()
	fsys["labs/scenarios/example/lab.yaml"] = yamlFile(`
schema_version: 1
id: duplicate-lab
kind: scenario
required: false
status: draft
`)
	fsys["labs/primitives/example/lab.yaml"] = yamlFile(`
schema_version: 1
id: duplicate-lab
kind: primitive
required: false
status: draft
`)

	_, err := LoadFS(context.Background(), fsys)
	if err == nil {
		t.Fatal("LoadFS() error = nil")
	}
	for _, want := range []string{
		"duplicate lab ID",
		"labs/scenarios/example/lab.yaml",
		"labs/primitives/example/lab.yaml",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not include %q", err, want)
		}
	}
}

func TestLoadFSUsesStrictDecoderForDiscoveredFiles(t *testing.T) {
	_, err := LoadFS(context.Background(), os.DirFS("testdata/unknown-field"))
	if err == nil {
		t.Fatal("LoadFS() error = nil")
	}
	for _, want := range []string{"cases/bad/case.yaml", "line 7", "field unexpected not found"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not include %q", err, want)
		}
	}
}

func TestLoadFSRejectsAliasCycle(t *testing.T) {
	_, err := LoadFS(context.Background(), os.DirFS("testdata/alias-cycle"))
	if err == nil {
		t.Fatal("LoadFS() error = nil")
	}
	for _, want := range []string{"aliases.yaml", "alias cycle"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not include %q", err, want)
		}
	}
}

func TestLoadFSHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := LoadFS(ctx, minimalCatalogFS())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadFS() error = %v, want context.Canceled", err)
	}
}

func minimalCatalogFS() fstest.MapFS {
	return fstest.MapFS{
		"scope.yaml": yamlFile(`
schema_version: 1
families:
  - id: family-id
    title: Family
cases: []
exclusions: []
`),
		"sources.yaml": yamlFile(`
schema_version: 1
sources: []
`),
		"aliases.yaml": yamlFile(`
schema_version: 1
aliases: []
`),
	}
}

func yamlFile(content string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(strings.TrimPrefix(content, "\n"))}
}

func assertMapKeys[T any](t *testing.T, name string, got map[string]T, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s map size = %d, want %d", name, len(got), len(want))
	}
	for _, key := range want {
		if _, exists := got[key]; !exists {
			t.Errorf("%s map is missing %q", name, key)
		}
	}
}
