package content

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
)

func TestAllScopedCasesHaveAuthoredContent(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	repository, err := catalog.LoadDir(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(repository.Cases), len(repository.Scope.Cases); got != want {
		t.Errorf("authored case manifests = %d, want %d", got, want)
	}
	for _, scoped := range repository.Scope.Cases {
		if _, exists := repository.Cases[scoped.ID]; !exists {
			t.Errorf("scope case %q has no authored manifest", scoped.ID)
		}
	}
}

func TestPresentCasesMeetFullContentContract(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	repository, err := catalog.LoadDir(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	scopeByID := make(map[string]catalog.ScopeCase, len(repository.Scope.Cases))
	for _, scoped := range repository.Scope.Cases {
		scopeByID[scoped.ID] = scoped
	}
	for id, manifest := range repository.Cases {
		t.Run(id, func(t *testing.T) {
			scoped, exists := scopeByID[id]
			if !exists {
				t.Fatalf("authored case %q is outside scope", id)
			}
			if manifest.Title != scoped.Title || manifest.PrimaryFamily != scoped.PrimaryFamily {
				t.Errorf("identity = title %q family %q; want %q and %q", manifest.Title, manifest.PrimaryFamily, scoped.Title, scoped.PrimaryFamily)
			}
			if !manifest.Required {
				t.Error("authored case is not required")
			}
			if len(manifest.Dimensions) == 0 {
				t.Error("authored case has no dimensions")
			}
			if len(manifest.Claims) < 2 {
				t.Errorf("authored case claims = %d, want at least 2", len(manifest.Claims))
			}
			for _, claim := range manifest.Claims {
				if !strings.HasPrefix(claim.ID, manifest.ID+"-") {
					t.Errorf("claim %q does not use case ID prefix", claim.ID)
				}
			}
			relative := filepath.Join("cases", id, "README.md")
			markdown, err := os.ReadFile(filepath.Join(root, relative))
			if err != nil {
				t.Fatal(err)
			}
			strict := manifest
			strict.Status = catalog.LifecycleStatusComplete
			result := ValidateCase(filepath.ToSlash(relative), markdown, strict, repository)
			for _, diagnostic := range result.Diagnostics {
				t.Errorf("%s: %s", diagnostic.Code, diagnostic.Message)
			}
		})
	}
}
