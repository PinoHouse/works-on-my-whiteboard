package validator

import "testing"

func TestDependencyCycleUsesFrozenDiagnostic(t *testing.T) {
	graph := dependencyGraph{
		"case:a":      {"principle:b"},
		"principle:b": {"lab:c"},
		"lab:c":       {"case:a"},
	}
	diagnostics := dependencyCycleDiagnostics(graph)
	if len(diagnostics) != 1 || diagnostics[0].Code != CodeDependencyIncomplete {
		t.Fatalf("dependency cycle diagnostics = %#v, want sole %q", diagnostics, CodeDependencyIncomplete)
	}
}
