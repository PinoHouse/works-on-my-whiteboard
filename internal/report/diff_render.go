package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func WriteDiffJSON(w io.Writer, model DiffModel) error {
	if w == nil {
		return fmt.Errorf("write diff JSON: nil writer")
	}
	if err := validateDiffDynamicText(model); err != nil {
		return fmt.Errorf("write diff JSON: %w", err)
	}
	encoded, err := json.MarshalIndent(model, "", "  ")
	if err != nil {
		return fmt.Errorf("write diff JSON: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := writeExact(w, encoded); err != nil {
		return fmt.Errorf("write diff JSON: %w", err)
	}
	return nil
}

func WriteDiffMarkdown(w io.Writer, model DiffModel) error {
	if w == nil {
		return fmt.Errorf("write diff Markdown: nil writer")
	}
	if err := validateDiffDynamicText(model); err != nil {
		return fmt.Errorf("write diff Markdown: %w", err)
	}
	lines := []string{
		"# Evidence diff",
		"",
		"- Status: `" + string(model.Status) + "`",
		"- Input digest: `" + model.InputDigest + "`",
		"- Profile: `" + string(model.Profile) + "`",
		"",
		"## Changes",
		"",
	}
	if len(model.Rows) == 0 {
		lines = append(lines, "None.")
	} else {
		lines = append(lines,
			"| Cell | Metrics | Environment | Assertion messages | Conclusion | Limitations |",
			"| --- | --- | --- | --- | --- | --- |",
		)
		for _, row := range model.Rows {
			cells := []string{
				matrixIdentity(row.Cell),
				formatMetricChanges(row.MetricChanges),
				formatEnvironmentChanges(row.EnvironmentChanges),
				formatAssertionMessageChanges(row.AssertionMessageChanges),
				formatTextChange(row.Conclusion),
				formatStringSliceChange(row.Limitations),
			}
			for index := range cells {
				cells[index] = escapeMarkdownTableCell(cells[index])
			}
			lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
		}
	}
	if err := writeExact(w, []byte(strings.Join(lines, "\n")+"\n")); err != nil {
		return fmt.Errorf("write diff Markdown: %w", err)
	}
	return nil
}

func formatMetricChanges(changes []MetricChange) string {
	if len(changes) == 0 {
		return "none"
	}
	parts := make([]string, len(changes))
	for index, change := range changes {
		left := "absent"
		if change.Left != nil {
			left = fmt.Sprintf("%d %s", change.Left.Value, change.Left.Unit)
		}
		right := "absent"
		if change.Right != nil {
			right = fmt.Sprintf("%d %s", change.Right.Value, change.Right.Unit)
		}
		parts[index] = fmt.Sprintf("%s: %s -> %s", change.ID, left, right)
		if change.Delta != "" {
			parts[index] += " (delta=" + change.Delta + ")"
		}
	}
	return strings.Join(parts, "; ")
}

func formatEnvironmentChanges(changes []EnvironmentChange) string {
	if len(changes) == 0 {
		return "none"
	}
	parts := make([]string, len(changes))
	for index, change := range changes {
		parts[index] = fmt.Sprintf("%s: %s -> %s", change.Field, change.Left, change.Right)
	}
	return strings.Join(parts, "; ")
}

func formatAssertionMessageChanges(changes []AssertionMessageChange) string {
	if len(changes) == 0 {
		return "none"
	}
	parts := make([]string, len(changes))
	for index, change := range changes {
		parts[index] = fmt.Sprintf("%s: %s -> %s", change.ID, change.Left, change.Right)
	}
	return strings.Join(parts, "; ")
}

func formatTextChange(change *TextChange) string {
	if change == nil {
		return "none"
	}
	return change.Left + " -> " + change.Right
}

func formatStringSliceChange(change *StringSliceChange) string {
	if change == nil {
		return "none"
	}
	return "[" + strings.Join(change.Left, "; ") + "] -> [" + strings.Join(change.Right, "; ") + "]"
}
