package content

import (
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
)

func TestValidateRepositoryChecksRelativeTargetsAndHeadingFragments(t *testing.T) {
	t.Run("valid_target_and_fragment", func(t *testing.T) {
		root, repository := repositoryWithCaseDocument(t, "[目标](../../docs/target.md#existing-heading) [本节](#表面题目)")
		writeTestFile(t, filepath.Join(root, "docs", "target.md"), "# Existing Heading\n")

		result := ValidateRepository(root, repository)
		assertNoDiagnosticCode(t, result, "missing_link_target")
		assertNoDiagnosticCode(t, result, "missing_heading_fragment")
	})

	t.Run("missing_target", func(t *testing.T) {
		root, repository := repositoryWithCaseDocument(t, "[目标](../../docs/missing.md)")
		result := ValidateRepository(root, repository)
		assertHasDiagnosticCode(t, result, "missing_link_target")
	})

	t.Run("missing_fragment", func(t *testing.T) {
		root, repository := repositoryWithCaseDocument(t, "[目标](../../docs/target.md#missing-heading)")
		writeTestFile(t, filepath.Join(root, "docs", "target.md"), "# Existing Heading\n")

		result := ValidateRepository(root, repository)
		assertHasDiagnosticCode(t, result, "missing_heading_fragment")
	})
}

func TestValidateRepositoryUsesUnicodeHeadingSlugs(t *testing.T) {
	fragment := url.PathEscape("中文-mixed标题")
	root, repository := repositoryWithCaseDocument(t,
		"[inline](../../docs/target.md#"+fragment+") "+
			"[duplicate](../../docs/target.md#"+url.PathEscape("重复-标题-1")+")",
	)
	writeTestFile(t, filepath.Join(root, "docs", "target.md"), `
# 中文 *Mixed*，标题

# 重复 标题

# 重复 标题
`)

	result := ValidateRepository(root, repository)
	assertNoDiagnosticCode(t, result, "missing_heading_fragment")
}

func TestValidateRepositoryUsesAllHeadingLevelsAndGlobalSlugCollisions(t *testing.T) {
	root, repository := repositoryWithCaseDocument(t,
		"[deep](../../docs/target.md#deep-heading) "+
			"[collision](../../docs/target.md#a-1-1)",
	)
	writeTestFile(t, filepath.Join(root, "docs", "target.md"), `
### Deep Heading

# A

## A

#### A-1
`)

	result := ValidateRepository(root, repository)
	assertNoDiagnosticCode(t, result, "missing_heading_fragment")
}

func TestValidateRepositoryResolvesDirectoryFragmentsThroughReadme(t *testing.T) {
	root, repository := repositoryWithCaseDocument(t, "[目录](../../docs/topic/#中文-标题)")
	writeTestFile(t, filepath.Join(root, "docs", "topic", "README.md"), "# 中文 标题\n")

	result := ValidateRepository(root, repository)
	assertNoDiagnosticCode(t, result, "missing_link_target")
	assertNoDiagnosticCode(t, result, "missing_heading_fragment")
}

func TestValidateRepositoryRejectsUnsafeRelativeTargets(t *testing.T) {
	tests := []struct {
		name        string
		destination string
		prepare     func(t *testing.T, root string)
	}{
		{name: "absolute", destination: "/etc/passwd"},
		{name: "backslash", destination: `..\..\outside.md`},
		{name: "nul", destination: "../../docs/target.md%00"},
		{name: "encoded_dot_dot", destination: "../../%2e%2e/outside.md"},
		{name: "encoded_slash_escape", destination: "../../docs%2F..%2F..%2Foutside.md"},
		{name: "invalid_percent", destination: "../../docs/target%zz.md"},
		{name: "lexical_escape", destination: "../../../outside.md"},
		{
			name:        "symlink_escape",
			destination: "../../docs/escape/secret.md",
			prepare: func(t *testing.T, root string) {
				outside := t.TempDir()
				writeTestFile(t, filepath.Join(outside, "secret.md"), "# Secret\n")
				if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "docs", "escape")); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, repository := repositoryWithCaseDocument(t, "[目标]("+test.destination+")")
			if test.prepare != nil {
				test.prepare(t, root)
			}
			result := ValidateRepository(root, repository)
			assertHasDiagnosticCode(t, result, "invalid_link_target")
		})
	}
}

func TestValidateRepositoryDecodesDestinationsExactlyOnce(t *testing.T) {
	root, repository := repositoryWithCaseDocument(t,
		"[once](../../docs/%252e%252e/target.md?ignored=yes#target-heading)",
	)
	writeTestFile(t, filepath.Join(root, "docs", "%2e%2e", "target.md"), "# Target Heading\n")

	result := ValidateRepository(root, repository)
	assertNoDiagnosticCode(t, result, "missing_link_target")
	assertNoDiagnosticCode(t, result, "missing_heading_fragment")
}

func TestValidateRepositoryAppliesCommonMarkDestinationEscapes(t *testing.T) {
	root, repository := repositoryWithCaseDocument(t, "[目标](../../docs&#x2F;target.md)")
	writeTestFile(t, filepath.Join(root, "docs", "target.md"), "# Target\n")

	result := ValidateRepository(root, repository)
	assertNoDiagnosticCode(t, result, "missing_link_target")
}

func TestValidateRepositoryRequiresExactPathCase(t *testing.T) {
	root, repository := repositoryWithCaseDocument(t, "[目标](../../docs/target.md)")
	writeTestFile(t, filepath.Join(root, "docs", "Target.md"), "# Target\n")

	result := ValidateRepository(root, repository)
	assertHasDiagnosticCode(t, result, "invalid_link_target")
}

func TestValidateRepositoryScansAuthoredMarkdownAndSkipsStateDirectories(t *testing.T) {
	root, repository := repositoryWithCaseDocument(t, "")
	writeTestFile(t, filepath.Join(root, "README.md"), "[broken](docs/missing.md)\n")
	for _, directory := range []string{".git", ".superpowers", "evidence", "generated"} {
		writeTestFile(t, filepath.Join(root, directory, "ignored.md"), "[broken](missing.md)\n")
	}

	result := ValidateRepository(root, repository)
	count := 0
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == "missing_link_target" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("diagnostics = %#v; want exactly one authored Markdown link failure", result.Diagnostics)
	}
}

func TestValidateRepositoryRequiresCanonicalReadmeOnlyForCompleteOwners(t *testing.T) {
	root := t.TempDir()
	repository := emptyCatalog()
	repository.Cases["complete"] = catalog.CaseManifest{
		ID: "complete", Status: catalog.LifecycleStatusComplete,
	}
	repository.Cases["draft"] = catalog.CaseManifest{
		ID: "draft", Status: catalog.LifecycleStatusDraft,
	}

	result := ValidateRepository(root, repository)
	count := 0
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == "missing_content_file" {
			count++
			if strings.Contains(diagnostic.Message, root) {
				t.Fatalf("missing content diagnostic leaks absolute root: %#v", diagnostic)
			}
			if diagnostic.EntityID != "complete" {
				t.Fatalf("missing content diagnostic = %#v; want complete owner", diagnostic)
			}
		}
	}
	if count != 1 {
		t.Fatalf("diagnostics = %#v; want one missing complete README", result.Diagnostics)
	}
}

func TestValidateRepositoryRejectsSpecialFileTargets(t *testing.T) {
	root, repository := repositoryWithCaseDocument(t, "[socket](../../docs/service.sock)")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(root, "docs", "service.sock"))
	if err != nil {
		t.Skipf("Unix sockets unavailable: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	result := ValidateRepository(root, repository)
	assertHasDiagnosticCode(t, result, "invalid_link_target")
}

func TestValidateRepositoryDoesNotInterpretFragmentsOnNonMarkdownFiles(t *testing.T) {
	root, repository := repositoryWithCaseDocument(t, "[data](../../docs/data.bin#not-a-heading)")
	writeTestFile(t, filepath.Join(root, "docs", "data.bin"), "binary-ish data")

	result := ValidateRepository(root, repository)
	assertNoDiagnosticCode(t, result, "missing_heading_fragment")
}

func TestValidateRepositoryIgnoresExternalSchemes(t *testing.T) {
	root, repository := repositoryWithCaseDocument(t, `
[HTTPS](https://example.com/TODO#missing)
[mail](mailto:owner@example.com)
[data](data:text/plain,missing)
[protocol relative](//example.com/missing)
`)
	result := ValidateRepository(root, repository)
	assertNoDiagnosticCode(t, result, "missing_link_target")
	assertNoDiagnosticCode(t, result, "missing_heading_fragment")
	assertNoDiagnosticCode(t, result, "unfinished_marker")
}

func repositoryWithCaseDocument(t *testing.T, extra string) (string, *catalog.Catalog) {
	t.Helper()
	root := t.TempDir()
	markdown := validCaseMarkdown(map[string]string{
		"表面题目": longProse(140) + "\n\n" + extra,
	})
	writeTestFile(t, filepath.Join(root, "cases", "case-one", "README.md"), string(markdown))
	repository := emptyCatalog()
	repository.Cases["case-one"] = catalog.CaseManifest{ID: "case-one", Status: catalog.LifecycleStatusComplete}
	return root, repository
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
