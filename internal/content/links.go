package content

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
	"github.com/yuin/goldmark/ast"
)

var skippedMarkdownDirectories = map[string]struct{}{
	".git":         {},
	".superpowers": {},
	"evidence":     {},
	"generated":    {},
}

type repositoryDocument struct {
	path     string
	relative string
	source   []byte
	document ast.Node
}

func ValidateRepository(root string, repository *catalog.Catalog) Result {
	diagnostics := make([]validator.Diagnostic, 0)
	rootAbsolute, err := filepath.Abs(root)
	if err != nil {
		return Result{Diagnostics: []validator.Diagnostic{contentDiagnostic(
			CodeContentReadFailure,
			filepath.ToSlash(root),
			"",
			"repository root cannot be resolved",
		)}}
	}
	rootAbsolute = filepath.Clean(rootAbsolute)
	rootReal, err := filepath.EvalSymlinks(rootAbsolute)
	if err != nil {
		return Result{Diagnostics: []validator.Diagnostic{contentDiagnostic(
			CodeContentReadFailure,
			".",
			"",
			"repository root cannot be resolved",
		)}}
	}

	if repository != nil {
		for _, id := range sortedCaseIDs(repository.Cases) {
			manifest := repository.Cases[id]
			if manifest.Status != catalog.LifecycleStatusComplete {
				continue
			}
			relative := filepath.ToSlash(filepath.Join("cases", manifest.ID, "README.md"))
			source, readErr := os.ReadFile(filepath.Join(rootAbsolute, filepath.FromSlash(relative)))
			if readErr != nil {
				code := CodeContentReadFailure
				if os.IsNotExist(readErr) {
					code = CodeMissingContentFile
				}
				diagnostics = appendReadDiagnostic(diagnostics, code, relative, manifest.ID, readErr)
				continue
			}
			diagnostics = append(diagnostics, ValidateCase(relative, source, manifest, repository).Diagnostics...)
		}
		for _, id := range sortedPrincipleIDs(repository.Principles) {
			manifest := repository.Principles[id]
			if manifest.Status != catalog.LifecycleStatusComplete {
				continue
			}
			relative := filepath.ToSlash(filepath.Join("principles", manifest.ID, "README.md"))
			source, readErr := os.ReadFile(filepath.Join(rootAbsolute, filepath.FromSlash(relative)))
			if readErr != nil {
				code := CodeContentReadFailure
				if os.IsNotExist(readErr) {
					code = CodeMissingContentFile
				}
				diagnostics = appendReadDiagnostic(diagnostics, code, relative, manifest.ID, readErr)
				continue
			}
			diagnostics = append(diagnostics, ValidatePrinciple(relative, source, manifest, repository).Diagnostics...)
		}
	}

	documents, discoveryDiagnostics := discoverMarkdownDocuments(rootAbsolute)
	diagnostics = append(diagnostics, discoveryDiagnostics...)
	cache := make(map[string]*repositoryDocument, len(documents))
	for _, documentPath := range documents {
		document, loadDiagnostics := loadRepositoryDocument(rootAbsolute, documentPath)
		diagnostics = append(diagnostics, loadDiagnostics...)
		if document != nil {
			cache[documentPath] = document
		}
	}
	for _, documentPath := range documents {
		document := cache[documentPath]
		if document == nil {
			continue
		}
		diagnostics = append(diagnostics, validateDocumentLinks(rootAbsolute, rootReal, document, cache)...)
	}
	return Result{Diagnostics: sortContentDiagnostics(diagnostics)}
}

func discoverMarkdownDocuments(root string) ([]string, []validator.Diagnostic) {
	paths := make([]string, 0)
	diagnostics := make([]validator.Diagnostic, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		relative := relativeRepositoryPath(root, path)
		if walkErr != nil {
			diagnostics = append(diagnostics, contentDiagnostic(
				CodeContentReadFailure,
				relative,
				"",
				"Markdown path cannot be inspected",
			))
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path != root && entry.IsDir() && shouldSkipMarkdownDirectory(relative) {
			return filepath.SkipDir
		}
		if entry.Type().IsRegular() && strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			paths = append(paths, filepath.Clean(path))
		}
		return nil
	})
	if err != nil {
		diagnostics = append(diagnostics, contentDiagnostic(
			CodeContentReadFailure,
			".",
			"",
			"repository Markdown cannot be discovered",
		))
	}
	sort.Strings(paths)
	return paths, diagnostics
}

func shouldSkipMarkdownDirectory(relative string) bool {
	relative = filepath.ToSlash(relative)
	first, _, _ := strings.Cut(relative, "/")
	_, skip := skippedMarkdownDirectories[first]
	return skip
}

func loadRepositoryDocument(root, path string) (*repositoryDocument, []validator.Diagnostic) {
	relative := relativeRepositoryPath(root, path)
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, []validator.Diagnostic{contentDiagnostic(
			CodeContentReadFailure,
			relative,
			"",
			"Markdown content cannot be read",
		)}
	}
	if !utf8.Valid(source) {
		return nil, []validator.Diagnostic{contentDiagnostic(
			CodeInvalidUTF8,
			relative,
			"",
			"Markdown content is not valid UTF-8",
		)}
	}
	return &repositoryDocument{
		path:     filepath.Clean(path),
		relative: relative,
		source:   source,
		document: parseMarkdown(source),
	}, []validator.Diagnostic{}
}

func validateDocumentLinks(root, realRoot string, source *repositoryDocument, cache map[string]*repositoryDocument) []validator.Diagnostic {
	diagnostics := make([]validator.Diagnostic, 0)
	for _, destination := range documentLinkDestinations(source.document) {
		diagnostics = append(diagnostics, validateLinkDestination(root, realRoot, source, destination, cache)...)
	}
	return diagnostics
}

func documentLinkDestinations(document ast.Node) []string {
	destinations := make([]string, 0)
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch typed := node.(type) {
		case *ast.Link:
			destinations = append(destinations, string(renderVisibleBytes(typed.Destination)))
		case *ast.Image:
			destinations = append(destinations, string(renderVisibleBytes(typed.Destination)))
		}
		return ast.WalkContinue, nil
	})
	return destinations
}

func validateLinkDestination(root, realRoot string, source *repositoryDocument, destination string, cache map[string]*repositoryDocument) []validator.Diagnostic {
	if strings.ContainsRune(destination, '\x00') || strings.Contains(destination, "\\") {
		return []validator.Diagnostic{invalidLinkDiagnostic(source.relative, destination, "contains a NUL or backslash")}
	}
	parsed, err := url.Parse(destination)
	if err != nil {
		return []validator.Diagnostic{invalidLinkDiagnostic(source.relative, destination, "URL cannot be parsed")}
	}
	if parsed.Scheme != "" || parsed.Host != "" {
		return []validator.Diagnostic{}
	}
	if strings.ContainsRune(parsed.Path, '\x00') || strings.Contains(parsed.Path, "\\") ||
		strings.ContainsRune(parsed.Fragment, '\x00') || strings.Contains(parsed.Fragment, "\\") {
		return []validator.Diagnostic{invalidLinkDiagnostic(source.relative, destination, "decoded path or fragment contains a NUL or backslash")}
	}
	if parsed.Path != "" && (strings.HasPrefix(parsed.Path, "/") || filepath.IsAbs(filepath.FromSlash(parsed.Path))) {
		return []validator.Diagnostic{invalidLinkDiagnostic(source.relative, destination, "absolute repository links are not allowed")}
	}

	target := source.path
	if parsed.Path != "" {
		target = filepath.Clean(filepath.Join(filepath.Dir(source.path), filepath.FromSlash(parsed.Path)))
	}
	if !pathWithinRoot(root, target) {
		return []validator.Diagnostic{invalidLinkDiagnostic(source.relative, destination, "link escapes the repository root")}
	}
	exists, exact, stateErr := exactPathState(root, target)
	if stateErr != nil {
		return []validator.Diagnostic{contentDiagnostic(
			CodeContentReadFailure,
			source.relative,
			"",
			fmt.Sprintf("relative link target %q cannot be inspected", destination),
		)}
	}
	if !exact {
		return []validator.Diagnostic{invalidLinkDiagnostic(source.relative, destination, "target path case does not match the repository entry")}
	}
	if !exists {
		return []validator.Diagnostic{missingLinkDiagnostic(source.relative, destination)}
	}
	targetInfo, statErr := os.Stat(target)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return []validator.Diagnostic{missingLinkDiagnostic(source.relative, destination)}
		}
		return []validator.Diagnostic{contentDiagnostic(
			CodeContentReadFailure,
			source.relative,
			"",
			fmt.Sprintf("relative link target %q cannot be inspected", destination),
		)}
	}
	if !targetInfo.Mode().IsRegular() && !targetInfo.IsDir() {
		return []validator.Diagnostic{invalidLinkDiagnostic(source.relative, destination, "target is not a regular file or directory")}
	}
	targetReal, evalErr := filepath.EvalSymlinks(target)
	if evalErr != nil || !pathWithinRoot(realRoot, targetReal) {
		return []validator.Diagnostic{invalidLinkDiagnostic(source.relative, destination, "target cannot be resolved inside the repository root")}
	}
	if parsed.Fragment == "" {
		return []validator.Diagnostic{}
	}
	if targetInfo.IsDir() {
		target = filepath.Join(target, "README.md")
		exists, exact, stateErr = exactPathState(root, target)
		if stateErr != nil {
			return []validator.Diagnostic{contentDiagnostic(
				CodeContentReadFailure,
				source.relative,
				"",
				fmt.Sprintf("directory README for relative link %q cannot be inspected", destination),
			)}
		}
		if !exact {
			return []validator.Diagnostic{invalidLinkDiagnostic(source.relative, destination, "directory README path case does not match")}
		}
		if !exists {
			return []validator.Diagnostic{missingLinkDiagnostic(source.relative, destination)}
		}
		targetReal, evalErr = filepath.EvalSymlinks(target)
		if evalErr != nil || !pathWithinRoot(realRoot, targetReal) {
			return []validator.Diagnostic{invalidLinkDiagnostic(source.relative, destination, "directory README resolves outside the repository root")}
		}
	}
	if !strings.EqualFold(filepath.Ext(target), ".md") {
		return []validator.Diagnostic{}
	}
	target = filepath.Clean(target)
	targetDocument := cache[target]
	if targetDocument == nil {
		var loadDiagnostics []validator.Diagnostic
		targetDocument, loadDiagnostics = loadRepositoryDocument(root, target)
		if targetDocument == nil {
			return loadDiagnostics
		}
		cache[target] = targetDocument
	}
	if _, exists := markdownHeadingIDs(targetDocument.document, targetDocument.source)[parsed.Fragment]; !exists {
		return []validator.Diagnostic{missingFragmentDiagnostic(source.relative, destination, parsed.Fragment)}
	}
	return []validator.Diagnostic{}
}

func markdownHeadingIDs(document ast.Node, source []byte) map[string]struct{} {
	ids := make(map[string]struct{})
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		heading, ok := node.(*ast.Heading)
		if !ok {
			return ast.WalkContinue, nil
		}
		base := repositoryHeadingSlug(headingVisibleText(heading, source))
		candidate := base
		for suffix := 1; ; suffix++ {
			if _, used := ids[candidate]; !used {
				ids[candidate] = struct{}{}
				break
			}
			candidate = fmt.Sprintf("%s-%d", base, suffix)
		}
		return ast.WalkSkipChildren, nil
	})
	return ids
}

func repositoryHeadingSlug(value string) string {
	var builder strings.Builder
	pendingSpace := false
	for _, char := range strings.ToLower(value) {
		if unicode.IsSpace(char) {
			if builder.Len() != 0 {
				pendingSpace = true
			}
			continue
		}
		if !unicode.IsLetter(char) && !unicode.IsNumber(char) && !unicode.IsMark(char) && char != '-' && char != '_' {
			continue
		}
		if pendingSpace {
			builder.WriteByte('-')
			pendingSpace = false
		}
		builder.WriteRune(char)
	}
	if builder.Len() == 0 {
		return "heading"
	}
	return builder.String()
}

func exactPathState(root, target string) (bool, bool, error) {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false, false, err
	}
	if relative == "." {
		return true, true, nil
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		entries, readErr := os.ReadDir(current)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				return false, true, nil
			}
			return false, true, readErr
		}
		exact := false
		folded := false
		for _, entry := range entries {
			if entry.Name() == component {
				exact = true
				break
			}
			if strings.EqualFold(entry.Name(), component) {
				folded = true
			}
		}
		if !exact {
			if folded {
				return true, false, nil
			}
			return false, true, nil
		}
		current = filepath.Join(current, component)
	}
	return true, true, nil
}

func pathWithinRoot(root, target string) bool {
	if root == "" || target == "" {
		return false
	}
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func invalidLinkDiagnostic(path, destination, reason string) validator.Diagnostic {
	return contentDiagnostic(
		CodeInvalidLinkTarget,
		path,
		"",
		fmt.Sprintf("relative link target %q is invalid: %s", destination, reason),
	)
}

func missingLinkDiagnostic(path, destination string) validator.Diagnostic {
	return contentDiagnostic(
		CodeMissingLinkTarget,
		path,
		"",
		fmt.Sprintf("relative link target %q does not exist", destination),
	)
}

func missingFragmentDiagnostic(path, destination, fragment string) validator.Diagnostic {
	return contentDiagnostic(
		CodeMissingHeadingFragment,
		path,
		"",
		fmt.Sprintf("relative link target %q has no Markdown heading fragment %q", destination, fragment),
	)
}

func relativeRepositoryPath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	if relative == "." {
		return "."
	}
	return filepath.ToSlash(relative)
}

func sortedCaseIDs(values map[string]catalog.CaseManifest) []string {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func sortedPrincipleIDs(values map[string]catalog.PrincipleManifest) []string {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
