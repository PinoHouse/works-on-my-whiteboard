package report

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

func WriteMarkdown(w io.Writer, model Model) error {
	if w == nil {
		return fmt.Errorf("write report Markdown: nil writer")
	}
	if err := validateReportDynamicText(model); err != nil {
		return fmt.Errorf("write report Markdown: %w", err)
	}
	lines := []string{
		"# Evidence report",
		"",
		"- Input digest: `" + model.InputDigest + "`",
		"- Profile: `" + string(model.Profile) + "`",
		fmt.Sprintf("- Coverage: complete=%d baseline=%d missing=%d unexpected=%d", model.Coverage.CompleteTotal, model.Coverage.BaselineTotal, len(model.Coverage.MissingCaseIDs), len(model.Coverage.UnexpectedCaseIDs)),
		"- Missing case IDs: " + markdownIDList(model.Coverage.MissingCaseIDs),
		"- Unexpected case IDs: " + markdownIDList(model.Coverage.UnexpectedCaseIDs),
		"",
		"## Required cells",
		"",
		"| Cell | Role | Evidence | Status | Workload | Faults | Measurements | Assertions | Environment | Conclusion | Limitations |",
		"| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |",
	}
	for _, row := range model.Rows {
		cells := []string{
			matrixIdentity(row.Cell),
			row.Cell.Role,
			row.EvidenceID,
			string(row.Status),
			formatWorkload(row.Workload),
			formatFaults(row.Faults),
			formatMeasurements(row.Measurements),
			formatAssertions(row.Assertions),
			formatEnvironment(row.Environment),
			row.Conclusion,
			strings.Join(row.Limitations, "; "),
		}
		for index := range cells {
			cells[index] = escapeMarkdownTableCell(cells[index])
		}
		lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
	}
	if len(model.Rows) == 0 {
		lines = append(lines, "| none | - | - | - | - | - | - | - | - | - | - |")
	}

	lines = append(lines, "", "## Sources", "")
	if len(model.Sources) == 0 {
		lines = append(lines, "None.")
	} else {
		lines = append(lines, "| ID | Title | URL |", "| --- | --- | --- |")
		for _, source := range model.Sources {
			lines = append(lines, fmt.Sprintf("| %s | %s | <%s> |", escapeMarkdownTableCell(source.ID), escapeMarkdownTableCell(source.Title), source.URL))
		}
	}

	lines = append(lines, "", "## Diagnostics", "")
	if len(model.Diagnostics) == 0 {
		lines = append(lines, "None.")
	} else {
		lines = append(lines, "| Code | Severity | Path | Entity | Message |", "| --- | --- | --- | --- | --- |")
		for _, diagnostic := range model.Diagnostics {
			cells := []string{diagnostic.Code, diagnostic.Severity, diagnostic.Path, diagnostic.EntityID, diagnostic.Message}
			for index := range cells {
				cells[index] = escapeMarkdownTableCell(cells[index])
			}
			lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
		}
	}

	if err := writeExact(w, []byte(strings.Join(lines, "\n")+"\n")); err != nil {
		return fmt.Errorf("write report Markdown: %w", err)
	}
	return nil
}

func markdownIDList(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}
	quoted := make([]string, len(ids))
	for index, id := range ids {
		quoted[index] = "`" + id + "`"
	}
	return strings.Join(quoted, ", ")
}

func matrixIdentity(cell validator.MatrixCell) string {
	adapter := cell.AdapterID
	if adapter == "" {
		adapter = "-"
	}
	return strings.Join([]string{cell.LabID, cell.RequiredRunID, cell.BindingID, cell.ClaimID, cell.ImplementationID, adapter}, "/")
}

func formatWorkload(workload evidence.Workload) string {
	keys := sortedIntMapKeys(workload.Parameters)
	parts := make([]string, len(keys))
	for index, key := range keys {
		parts[index] = key + "=" + strconv.FormatInt(workload.Parameters[key], 10)
	}
	return workload.ID + " {" + strings.Join(parts, ", ") + "}"
}

func formatFaults(faults []evidence.Fault) string {
	if len(faults) == 0 {
		return "none"
	}
	parts := make([]string, len(faults))
	for index, fault := range faults {
		parts[index] = fmt.Sprintf("%s@%s+%s", fault.ID, fault.At, fault.Duration)
	}
	return strings.Join(parts, "; ")
}

func formatMeasurements(measurements map[string]evidence.Measurement) string {
	keys := make([]string, 0, len(measurements))
	for key := range measurements {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for index, key := range keys {
		measurement := measurements[key]
		parts[index] = fmt.Sprintf("%s=%d %s", key, measurement.Value, measurement.Unit)
	}
	return strings.Join(parts, "; ")
}

func formatAssertions(assertions []evidence.Assertion) string {
	parts := make([]string, len(assertions))
	for index, assertion := range assertions {
		state := "failed"
		if assertion.Passed {
			state = "passed"
		}
		parts[index] = fmt.Sprintf("%s=%s (%s)", assertion.ID, state, assertion.Message)
	}
	return strings.Join(parts, "; ")
}

func formatEnvironment(environment evidence.Environment) string {
	return fmt.Sprintf("go=%s os=%s arch=%s cpu=%s logical_cpus=%d", environment.GoVersion, environment.OS, environment.Arch, environment.CPU, environment.LogicalCPUs)
}

func sortedIntMapKeys(values map[string]int64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func escapeMarkdownTableCell(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	var escaped strings.Builder
	for len(value) != 0 {
		r, size := utf8.DecodeRuneInString(value)
		value = value[size:]
		switch r {
		case '&':
			escaped.WriteString("&amp;")
		case '\\':
			escaped.WriteString(`\\`)
		case '|':
			escaped.WriteString(`\|`)
		case '\n':
			escaped.WriteString("<br>")
		case '\t':
			escaped.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&escaped, `\u%04X`, r)
			} else {
				escaped.WriteRune(r)
			}
		}
	}
	return escaped.String()
}
