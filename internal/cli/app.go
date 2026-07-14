package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/validator"
)

const (
	ExitSuccess               = 0
	ExitArgumentOrLoadFailure = 2
	ExitDevelopmentFailure    = 3
	ExitReleaseFailure        = 4
	ExitLabExecutionFailure   = 5
)

func Run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: whiteboard <validate|coverage>")
		return ExitArgumentOrLoadFailure
	}
	switch args[0] {
	case "validate":
		return runValidate(ctx, args[1:], stdout, stderr)
	case "coverage":
		return runCoverage(ctx, args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q; want validate or coverage\n", args[0])
		return ExitArgumentOrLoadFailure
	}
}

func newFlagSet(command string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet("whiteboard "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "Usage of whiteboard %s:\n", command)
		flags.PrintDefaults()
	}
	return flags
}

func parseFlagSet(flags *flag.FlagSet, args []string, stderr io.Writer) (bool, int) {
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return false, ExitSuccess
		}
		return false, ExitArgumentOrLoadFailure
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected positional arguments: %s\n", strings.Join(flags.Args(), " "))
		return false, ExitArgumentOrLoadFailure
	}
	return true, ExitSuccess
}

type trackedString struct {
	value string
	set   bool
}

func (value *trackedString) String() string {
	return value.value
}

func (value *trackedString) Set(next string) error {
	value.value = next
	value.set = true
	return nil
}

func sortDiagnostics(diagnostics []validator.Diagnostic) []validator.Diagnostic {
	if diagnostics == nil {
		diagnostics = []validator.Diagnostic{}
	}
	sort.Slice(diagnostics, func(left, right int) bool {
		if diagnostics[left].Code != diagnostics[right].Code {
			return diagnostics[left].Code < diagnostics[right].Code
		}
		if diagnostics[left].Path != diagnostics[right].Path {
			return diagnostics[left].Path < diagnostics[right].Path
		}
		if diagnostics[left].EntityID != diagnostics[right].EntityID {
			return diagnostics[left].EntityID < diagnostics[right].EntityID
		}
		return diagnostics[left].Message < diagnostics[right].Message
	})
	return diagnostics
}

func writeDiagnostics(writer io.Writer, diagnostics []validator.Diagnostic) error {
	for _, diagnostic := range diagnostics {
		if _, err := fmt.Fprintf(writer, "%s [%s]", diagnostic.Severity, diagnostic.Code); err != nil {
			return err
		}
		if diagnostic.Path != "" {
			if _, err := fmt.Fprintf(writer, " path=%s", diagnostic.Path); err != nil {
				return err
			}
		}
		if diagnostic.EntityID != "" {
			if _, err := fmt.Fprintf(writer, " entity=%s", diagnostic.EntityID); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(writer, ": %s\n", diagnostic.Message); err != nil {
			return err
		}
	}
	return nil
}

func writeCLIError(stderr io.Writer, format string, values ...any) {
	_, _ = fmt.Fprintf(stderr, format+"\n", values...)
}
