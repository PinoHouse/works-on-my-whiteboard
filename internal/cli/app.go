package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strconv"
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
	trackedStdout := &trackingWriter{destination: stdout}
	trackedStderr := &trackingWriter{destination: stderr}
	exitCode := ExitArgumentOrLoadFailure
	if len(args) == 0 {
		writeCLIError(trackedStderr, "usage: whiteboard <validate|coverage>")
	} else {
		switch args[0] {
		case "validate":
			exitCode = runValidate(ctx, args[1:], trackedStdout, trackedStderr)
		case "coverage":
			exitCode = runCoverage(ctx, args[1:], trackedStdout, trackedStderr)
		default:
			writeCLIError(trackedStderr, "unknown command %q; want validate or coverage", args[0])
		}
	}
	if trackedStdout.Err() != nil || trackedStderr.Err() != nil {
		return ExitArgumentOrLoadFailure
	}
	return exitCode
}

type trackingWriter struct {
	destination io.Writer
	err         error
}

func (writer *trackingWriter) Write(value []byte) (int, error) {
	if writer.err != nil {
		return 0, writer.err
	}
	if writer.destination == nil {
		writer.err = errors.New("nil writer")
		return 0, writer.err
	}
	written, err := writer.destination.Write(value)
	if err == nil && written != len(value) {
		err = io.ErrShortWrite
	}
	if err != nil {
		writer.err = err
	}
	return written, err
}

func (writer *trackingWriter) Err() error {
	return writer.err
}

func writeFull(writer io.Writer, value []byte) error {
	written, err := writer.Write(value)
	if err != nil {
		return err
	}
	if written != len(value) {
		return io.ErrShortWrite
	}
	return nil
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
		var line strings.Builder
		fmt.Fprintf(&line, "%s [%s]", diagnostic.Severity, diagnostic.Code)
		if diagnostic.Path != "" {
			fmt.Fprintf(&line, " path=%s", quoteDiagnosticField(diagnostic.Path))
		}
		if diagnostic.EntityID != "" {
			fmt.Fprintf(&line, " entity=%s", quoteDiagnosticField(diagnostic.EntityID))
		}
		fmt.Fprintf(&line, ": %s\n", quoteDiagnosticField(diagnostic.Message))
		if err := writeFull(writer, []byte(line.String())); err != nil {
			return err
		}
	}
	return nil
}

func quoteDiagnosticField(value string) string {
	return strconv.Quote(value)
}

func writeCLIError(stderr io.Writer, format string, values ...any) {
	_, _ = fmt.Fprintf(stderr, format+"\n", values...)
}
