package content

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
	"github.com/yuin/goldmark/ast"
	extensionast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/util"
)

type sectionSpec struct {
	name         string
	minimumRunes int
}

type sectionOccurrence struct {
	name      string
	bodyNodes []ast.Node
}

type proseBlock struct {
	text string
}

func validateSectionContract(path, entityID string, document ast.Node, source []byte, expected []sectionSpec) []validator.Diagnostic {
	occurrences := documentSections(document, source)
	diagnostics := make([]validator.Diagnostic, 0)
	expectedNames := make(map[string]struct{}, len(expected))
	expectedOrder := make([]string, 0, len(expected))
	for _, section := range expected {
		expectedNames[section.name] = struct{}{}
		expectedOrder = append(expectedOrder, section.name)
	}

	actualOrder := make([]string, 0, len(occurrences))
	byName := make(map[string][]sectionOccurrence, len(occurrences))
	for _, occurrence := range occurrences {
		actualOrder = append(actualOrder, occurrence.name)
		byName[occurrence.name] = append(byName[occurrence.name], occurrence)
		if _, exists := expectedNames[occurrence.name]; !exists {
			diagnostics = append(diagnostics, contentDiagnostic(
				CodeHeadingContractMismatch,
				path,
				entityID,
				fmt.Sprintf("unexpected level-two section heading %q", occurrence.name),
			))
		}
	}

	for _, section := range expected {
		matches := byName[section.name]
		switch len(matches) {
		case 0:
			diagnostics = append(diagnostics, contentDiagnostic(
				CodeHeadingContractMismatch,
				path,
				entityID,
				fmt.Sprintf("missing required level-two section heading %q", section.name),
			))
		case 1:
			occurrence := matches[0]
			if !hasAllowedSectionBody(occurrence.bodyNodes, source) {
				diagnostics = append(diagnostics, contentDiagnostic(
					CodeEmptySectionBody,
					path,
					entityID,
					fmt.Sprintf("section %q has no non-empty paragraph, list, table, or code block", section.name),
				))
			}
			prose := sectionProse(occurrence.bodyNodes, source)
			count := utf8.RuneCountInString(prose)
			if count < section.minimumRunes {
				diagnostics = append(diagnostics, contentDiagnostic(
					CodeSectionTooShort,
					path,
					entityID,
					fmt.Sprintf("section %q has %d non-code prose runes; want at least %d", section.name, count, section.minimumRunes),
				))
			}
		default:
			diagnostics = append(diagnostics, contentDiagnostic(
				CodeHeadingContractMismatch,
				path,
				entityID,
				fmt.Sprintf("required level-two section heading %q appears %d times", section.name, len(matches)),
			))
		}
	}

	if !equalStrings(actualOrder, expectedOrder) {
		diagnostics = append(diagnostics, contentDiagnostic(
			CodeHeadingContractMismatch,
			path,
			entityID,
			fmt.Sprintf("document level-two headings are %q; want exactly %q", actualOrder, expectedOrder),
		))
	}
	return diagnostics
}

func documentSections(document ast.Node, source []byte) []sectionOccurrence {
	sections := make([]sectionOccurrence, 0)
	var current *sectionOccurrence
	for node := document.FirstChild(); node != nil; node = node.NextSibling() {
		if heading, ok := node.(*ast.Heading); ok && heading.Level == 2 {
			sections = append(sections, sectionOccurrence{name: headingVisibleText(heading, source)})
			current = &sections[len(sections)-1]
			continue
		}
		if current != nil {
			current.bodyNodes = append(current.bodyNodes, node)
		}
	}
	return sections
}

func hasAllowedSectionBody(nodes []ast.Node, source []byte) bool {
	for _, node := range nodes {
		if hasAllowedBodyNode(node, source) {
			return true
		}
	}
	return false
}

func hasAllowedBodyNode(node ast.Node, source []byte) bool {
	switch node.Kind() {
	case ast.KindHeading, ast.KindHTMLBlock, ast.KindRawHTML:
		return false
	case ast.KindParagraph, ast.KindTextBlock:
		return strings.TrimSpace(proseText(node, source)) != "" || subtreeHasInlineCode(node, source)
	case ast.KindCodeBlock:
		block := node.(*ast.CodeBlock)
		return strings.TrimSpace(string(block.Lines().Value(source))) != ""
	case ast.KindFencedCodeBlock:
		block := node.(*ast.FencedCodeBlock)
		return strings.TrimSpace(string(block.Lines().Value(source))) != ""
	case ast.KindList, extensionast.KindTable:
		return subtreeHasContent(node, source)
	}
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if hasAllowedBodyNode(child, source) {
			return true
		}
	}
	return false
}

func subtreeHasContent(node ast.Node, source []byte) bool {
	if strings.TrimSpace(proseText(node, source)) != "" {
		return true
	}
	var found bool
	_ = ast.Walk(node, func(current ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || found {
			return ast.WalkContinue, nil
		}
		switch current.Kind() {
		case ast.KindHeading, ast.KindHTMLBlock, ast.KindRawHTML, ast.KindAutoLink, ast.KindImage:
			return ast.WalkSkipChildren, nil
		case ast.KindCodeSpan:
			found = strings.TrimSpace(renderedNodeText(current, source)) != ""
			return ast.WalkSkipChildren, nil
		case ast.KindCodeBlock:
			block := current.(*ast.CodeBlock)
			found = strings.TrimSpace(string(block.Lines().Value(source))) != ""
			return ast.WalkSkipChildren, nil
		case ast.KindFencedCodeBlock:
			block := current.(*ast.FencedCodeBlock)
			found = strings.TrimSpace(string(block.Lines().Value(source))) != ""
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return found
}

func subtreeHasInlineCode(node ast.Node, source []byte) bool {
	var found bool
	_ = ast.Walk(node, func(current ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || found {
			return ast.WalkContinue, nil
		}
		switch current.Kind() {
		case ast.KindHeading, ast.KindHTMLBlock, ast.KindRawHTML, ast.KindAutoLink, ast.KindImage:
			return ast.WalkSkipChildren, nil
		case ast.KindCodeSpan:
			found = strings.TrimSpace(renderedNodeText(current, source)) != ""
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return found
}

func sectionProse(nodes []ast.Node, source []byte) string {
	parts := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node.Kind() == ast.KindHeading || node.Kind() == ast.KindHTMLBlock {
			continue
		}
		if prose := proseText(node, source); prose != "" {
			parts = append(parts, prose)
		}
	}
	return normalizeWhitespace(strings.Join(parts, " "))
}

func collectProseBlocks(document ast.Node, source []byte) []proseBlock {
	blocks := make([]proseBlock, 0)
	var visit func(ast.Node)
	visit = func(node ast.Node) {
		switch node.Kind() {
		case ast.KindHeading, ast.KindHTMLBlock, ast.KindRawHTML, ast.KindCodeBlock, ast.KindFencedCodeBlock, ast.KindCodeSpan, ast.KindAutoLink, ast.KindImage:
			return
		case extensionast.KindTableCell, ast.KindParagraph, ast.KindTextBlock:
			if text := proseText(node, source); text != "" {
				blocks = append(blocks, proseBlock{text: text})
			}
			return
		}
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			visit(child)
		}
	}
	visit(document)
	return blocks
}

func collectUnfinishedBlocks(document ast.Node, source []byte) []proseBlock {
	blocks := make([]proseBlock, 0)
	var visit func(ast.Node)
	visit = func(node ast.Node) {
		switch node.Kind() {
		case ast.KindHTMLBlock, ast.KindRawHTML, ast.KindCodeBlock, ast.KindFencedCodeBlock, ast.KindCodeSpan, ast.KindAutoLink:
			return
		case ast.KindHeading, extensionast.KindTableCell, ast.KindParagraph, ast.KindTextBlock:
			if text := visibleText(node, source, true, false); text != "" {
				blocks = append(blocks, proseBlock{text: text})
			}
			return
		}
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			visit(child)
		}
	}
	visit(document)
	return blocks
}

func proseText(node ast.Node, source []byte) string {
	return visibleText(node, source, false, true)
}

func visibleText(node ast.Node, source []byte, includeImages, skipHeadings bool) string {
	var builder strings.Builder
	_ = ast.Walk(node, func(current ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if current != node && skipHeadings && current.Kind() == ast.KindHeading {
			writeTextBoundary(&builder)
			return ast.WalkSkipChildren, nil
		}
		if current != node {
			switch current.Kind() {
			case ast.KindHTMLBlock, ast.KindCodeBlock, ast.KindFencedCodeBlock, ast.KindCodeSpan, ast.KindAutoLink:
				writeTextBoundary(&builder)
				return ast.WalkSkipChildren, nil
			case ast.KindRawHTML:
				return ast.WalkSkipChildren, nil
			case ast.KindImage:
				if !includeImages {
					writeTextBoundary(&builder)
					return ast.WalkSkipChildren, nil
				}
			}
		}
		switch typed := current.(type) {
		case *ast.Text:
			builder.Write(renderVisibleBytes(typed.Value(source)))
			if typed.SoftLineBreak() || typed.HardLineBreak() {
				writeTextBoundary(&builder)
			}
		case *ast.String:
			builder.Write(renderVisibleBytes(typed.Value))
		}
		return ast.WalkContinue, nil
	})
	return normalizeWhitespace(builder.String())
}

func headingVisibleText(heading *ast.Heading, source []byte) string {
	var builder strings.Builder
	_ = ast.Walk(heading, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if node != heading && node.Kind() == ast.KindRawHTML {
			return ast.WalkSkipChildren, nil
		}
		switch typed := node.(type) {
		case *ast.Text:
			builder.Write(renderVisibleBytes(typed.Value(source)))
		case *ast.String:
			builder.Write(renderVisibleBytes(typed.Value))
		case *ast.AutoLink:
			builder.Write(typed.Label(source))
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return normalizeWhitespace(builder.String())
}

func renderedNodeText(node ast.Node, source []byte) string {
	var builder strings.Builder
	_ = ast.Walk(node, func(current ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch typed := current.(type) {
		case *ast.Text:
			builder.Write(renderVisibleBytes(typed.Value(source)))
		case *ast.String:
			builder.Write(renderVisibleBytes(typed.Value))
		}
		return ast.WalkContinue, nil
	})
	return normalizeWhitespace(builder.String())
}

func renderVisibleBytes(value []byte) []byte {
	value = util.UnescapePunctuations(value)
	value = util.ResolveNumericReferences(value)
	return util.ResolveEntityNames(value)
}

func writeTextBoundary(builder *strings.Builder) {
	value := builder.String()
	if value == "" {
		return
	}
	last := value[len(value)-1]
	if last != ' ' && last != '\n' && last != '\r' && last != '\t' {
		builder.WriteByte(' ')
	}
}

func normalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
