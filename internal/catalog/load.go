package catalog

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"sort"
)

func LoadDir(ctx context.Context, root string) (*Catalog, error) {
	return LoadFS(ctx, os.DirFS(root))
}

func LoadFS(ctx context.Context, fsys fs.FS) (*Catalog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if fsys == nil {
		return nil, fmt.Errorf("catalog filesystem is nil")
	}

	scope, err := loadYAML[Scope](ctx, fsys, "scope.yaml")
	if err != nil {
		return nil, err
	}
	if err := validateScopeIDs(scope); err != nil {
		return nil, fmt.Errorf("scope.yaml: %w", err)
	}

	sourcesFile, err := loadYAML[SourcesFile](ctx, fsys, "sources.yaml")
	if err != nil {
		return nil, err
	}
	sources, err := indexByID("source", "sources.yaml", sourcesFile.Sources, func(source SourceRecord) string {
		return source.ID
	})
	if err != nil {
		return nil, err
	}

	aliasesFile, err := loadYAML[AliasesFile](ctx, fsys, "aliases.yaml")
	if err != nil {
		return nil, err
	}
	aliases, err := NewAliasSet(aliasesFile.Aliases)
	if err != nil {
		return nil, fmt.Errorf("aliases.yaml: %w", err)
	}

	cases, err := loadManifestMap(ctx, fsys, "case", []string{"cases/*/case.yaml"}, func(manifest CaseManifest) string {
		return manifest.ID
	})
	if err != nil {
		return nil, err
	}
	principles, err := loadManifestMap(ctx, fsys, "principle", []string{"principles/*/principle.yaml"}, func(manifest PrincipleManifest) string {
		return manifest.ID
	})
	if err != nil {
		return nil, err
	}
	labs, err := loadManifestMap(ctx, fsys, "lab", []string{
		"labs/primitives/*/lab.yaml",
		"labs/scenarios/*/lab.yaml",
	}, func(manifest LabManifest) string {
		return manifest.ID
	})
	if err != nil {
		return nil, err
	}
	adapters, err := loadManifestMap(ctx, fsys, "adapter", []string{"labs/adapters/*/adapter.yaml"}, func(manifest AdapterManifest) string {
		return manifest.ID
	})
	if err != nil {
		return nil, err
	}

	canonical := map[EntityKind]map[string]struct{}{
		EntityKindCase:      scopeCaseIDs(scope),
		EntityKindPrinciple: mapKeys(principles),
		EntityKindLab:       mapKeys(labs),
		EntityKindSource:    mapKeys(sources),
	}
	if err := aliases.ValidateCanonical(canonical); err != nil {
		return nil, fmt.Errorf("aliases.yaml: %w", err)
	}

	return &Catalog{
		Scope:      scope,
		Sources:    sources,
		Aliases:    aliases,
		Cases:      cases,
		Principles: principles,
		Labs:       labs,
		Adapters:   adapters,
	}, nil
}

func loadYAML[T any](ctx context.Context, fsys fs.FS, path string) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return zero, fmt.Errorf("%s: %w", path, err)
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	return DecodeStrict[T](path, data)
}

func loadManifestMap[T any](ctx context.Context, fsys fs.FS, kind string, patterns []string, id func(T) string) (map[string]T, error) {
	paths := make([]string, 0)
	for _, pattern := range patterns {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		matches, err := fs.Glob(fsys, pattern)
		if err != nil {
			return nil, fmt.Errorf("discover %s manifests with %q: %w", kind, pattern, err)
		}
		paths = append(paths, matches...)
	}
	sort.Strings(paths)

	manifests := make(map[string]T, len(paths))
	seenAt := make(map[string]string, len(paths))
	for _, path := range paths {
		manifest, err := loadYAML[T](ctx, fsys, path)
		if err != nil {
			return nil, err
		}
		manifestID := id(manifest)
		if previous, exists := seenAt[manifestID]; exists {
			return nil, fmt.Errorf("duplicate %s ID %q in %s (already defined in %s)", kind, manifestID, path, previous)
		}
		manifests[manifestID] = manifest
		seenAt[manifestID] = path
	}
	return manifests, nil
}

func indexByID[T any](kind, path string, records []T, id func(T) string) (map[string]T, error) {
	indexed := make(map[string]T, len(records))
	for _, record := range records {
		recordID := id(record)
		if _, exists := indexed[recordID]; exists {
			return nil, fmt.Errorf("%s: duplicate %s ID %q", path, kind, recordID)
		}
		indexed[recordID] = record
	}
	return indexed, nil
}

func validateScopeIDs(scope Scope) error {
	if err := rejectDuplicateIDs("family", scope.Families, func(family ScopeFamily) string {
		return family.ID
	}); err != nil {
		return err
	}
	if err := rejectDuplicateIDs("case", scope.Cases, func(scopeCase ScopeCase) string {
		return scopeCase.ID
	}); err != nil {
		return err
	}
	return rejectDuplicateIDs("exclusion", scope.Exclusions, func(exclusion ScopeExclusion) string {
		return exclusion.ID
	})
}

func rejectDuplicateIDs[T any](kind string, records []T, id func(T) string) error {
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		recordID := id(record)
		if _, exists := seen[recordID]; exists {
			return fmt.Errorf("duplicate %s ID %q", kind, recordID)
		}
		seen[recordID] = struct{}{}
	}
	return nil
}

func scopeCaseIDs(scope Scope) map[string]struct{} {
	ids := make(map[string]struct{}, len(scope.Cases))
	for _, scopeCase := range scope.Cases {
		ids[scopeCase.ID] = struct{}{}
	}
	return ids
}

func mapKeys[T any](values map[string]T) map[string]struct{} {
	keys := make(map[string]struct{}, len(values))
	for key := range values {
		keys[key] = struct{}{}
	}
	return keys
}
