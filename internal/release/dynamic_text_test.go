package release

import (
	"errors"
	"strings"
	"testing"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
)

func TestValidateDynamicTextAllowsPortableProseAndLineBreaks(t *testing.T) {
	values := []string{
		"The bounded run passed.",
		"Observed 10/10 requests.\nRetry remained bounded.",
		"Ordinary slash prose such as read/write and 10/10 remains portable.",
		"A read / write split is ordinary prose, not a filesystem path.",
		"Reference: https://example.com/design/consistency",
		"Ordinary labeled value ratio:10 remains portable.",
		"Safe artifact reference artifact=https://example.com/results/summary.json remains portable.",
		"Percent-encoded spacing result%20remains%20portable.",
		"Percent-encoded HTTPS path https://example.com/a%20b remains portable.",
		"Percent-encoded HTTPS separator https://example.com/design%2Fconsistency remains portable.",
		"Encoded HTTPS URL https%3A%2F%2Fexample.com%2Fdesign remains portable.",
		"A doubly encoded token path=%252Fprivate%252Ftmp is not recursively decoded.",
		"A doubly encoded scheme file%253A%252Fprivate is not recursively decoded.",
		"A doubly encoded entity path=&amp;#x2F;private remains literal.",
		"A mixed encoding token path=%26%23x2F%3Bprivate is not recursively decoded.",
		"Emoji and script remain ordinary text: 👩💻 数据一致。",
		"Deterministic content token 0123456789abcdef0123456789abcdef is semantic data.",
		"Document UUID 123e4567-e89b-12d3-a456-426614174000 is semantic data.",
		"Malformed run shape run-20260230T010203.004Z-0123456789abcdef0123456789abcdef is semantic data.",
		"Malformed set shape set-20260714T250203.004Z-0123456789abcdef0123456789abcdef is semantic data.",
		"",
	}
	for _, value := range values {
		if err := ValidateDynamicText(value); err != nil {
			t.Fatalf("ValidateDynamicText(%q) = %v, want nil", value, err)
		}
	}
}

func TestValidateDynamicTextRejectsSingleEncodedHostSpecificContent(t *testing.T) {
	values := []string{
		"artifact=file%3A%2Fprivate%2Ftmp%2Fhost-only.json",
		"path=%2Fprivate%2Ftmp%2Fhost-only.json",
		"artifact=C%3A%5CUsers%5Crunner%5Cresult.json",
	}
	for _, value := range values {
		err := ValidateDynamicText(value)
		if !errors.Is(err, ErrUnsafeDynamicText) {
			t.Fatalf("ValidateDynamicText(%q) = %v, want ErrUnsafeDynamicText", value, err)
		}
		if strings.Contains(err.Error(), value) {
			t.Fatalf("error echoed unsafe input %q: %v", value, err)
		}
	}
}

func TestValidateDynamicTextRejectsHostSpecificContentAfterMixedPercentEncoding(t *testing.T) {
	values := []string{
		"malformed=%ZZ path=%2Fprivate%2Ftmp%2Fhost-only.json",
		"malformed=%ZZ direction=%E2%80%AEoverride",
		"malformed=%ZZ attempt=run%2D20260714T010203.004Z%2D00000000000000000000000000000009",
	}
	for _, value := range values {
		err := ValidateDynamicText(value)
		if !errors.Is(err, ErrUnsafeDynamicText) {
			t.Fatalf("ValidateDynamicText(%q) = %v, want ErrUnsafeDynamicText", value, err)
		}
		if strings.Contains(err.Error(), value) {
			t.Fatalf("error echoed unsafe input %q: %v", value, err)
		}
	}
}

func TestValidateDynamicTextAllowsSafeMalformedPercentProse(t *testing.T) {
	for _, value := range []string{
		"The upstream returned malformed token %ZZ and continued.",
		"A trailing percent % remains ordinary prose.",
		"An incomplete escape %2 remains ordinary prose.",
	} {
		if err := ValidateDynamicText(value); err != nil {
			t.Fatalf("ValidateDynamicText(%q) = %v, want nil", value, err)
		}
	}
}

func TestValidateDynamicTextRejectsUnsafeHTMLCharacterReferences(t *testing.T) {
	values := []string{
		"host root &#x2F;private&#x2F;tmp&#x2F;host-only",
		"direction &#x202E;override",
		"attempt run&#x2D;20260714T010203.004Z&#x2D;00000000000000000000000000000009",
		"colored &#x1B;[31mred",
	}
	for _, value := range values {
		err := ValidateDynamicText(value)
		if !errors.Is(err, ErrUnsafeDynamicText) {
			t.Fatalf("ValidateDynamicText(%q) = %v, want ErrUnsafeDynamicText", value, err)
		}
		if strings.Contains(err.Error(), value) {
			t.Fatalf("error echoed unsafe input %q: %v", value, err)
		}
	}
}

func TestValidateDynamicTextAllowsSafeLiteralAmpersandsAndEntities(t *testing.T) {
	for _, value := range []string{
		"R&D remains literal prose.",
		"The literal entity text R&amp;D remains portable.",
		"Copyright marker &copy; is portable semantic text.",
	} {
		if err := ValidateDynamicText(value); err != nil {
			t.Fatalf("ValidateDynamicText(%q) = %v, want nil", value, err)
		}
	}
}

func TestValidateDynamicTextRejectsUnicodeFormatCharacters(t *testing.T) {
	values := []string{
		"right-to-left \u202eoverride",
		"isolate \u2066payload\u2069",
		"zero\u200bwidth",
		"joined \u200dtext",
		"encoded format %E2%80%AEoverride",
	}
	for _, value := range values {
		err := ValidateDynamicText(value)
		if !errors.Is(err, ErrUnsafeDynamicText) {
			t.Fatalf("ValidateDynamicText(%q) = %v, want ErrUnsafeDynamicText", value, err)
		}
		if strings.Contains(err.Error(), value) {
			t.Fatalf("error echoed unsafe input %q: %v", value, err)
		}
	}
}

func TestValidateDynamicTextRejectsHostAndInvocationSpecificContent(t *testing.T) {
	values := []string{
		"/",
		"/absolute/workspace/secret",
		"saved at /var/folders/zz/host-temp/result.json",
		"path=/tmp/TestRun/001/result.json",
		"路径：/private/tmp/x",
		`路径：\\server\share\x`,
		"）/tmp/x",
		"］~/x",
		`C:\Users\runner\AppData\Local\Temp\result.json`,
		`C:/Users/runner/AppData/Local/Temp/result.json`,
		`\\server\share\result.json`,
		"file:///private/tmp/result.json",
		"file://server/share/result.json",
		"artifact=file:%2Fprivate%2Ftmp%2Fhost-only.json",
		"smb://server/share/result.json",
		"~/host-only/result.json",
		`\Users\runner\AppData\Local\Temp\result.json`,
		"attempt " + testEvidenceID(2),
		"attempt_" + testEvidenceID(2),
		"set " + string(testRunSetID(2)),
		"colored \x1b[31mred\x1b[0m",
		"colored \u009b31mred",
		"nul\x00byte",
		"carriage\rreturn",
		"back\bspace",
		"delete\x7fbyte",
		"tab\tseparated",
	}
	for _, value := range values {
		err := ValidateDynamicText(value)
		if !errors.Is(err, ErrUnsafeDynamicText) {
			t.Fatalf("ValidateDynamicText(%q) = %v, want ErrUnsafeDynamicText", value, err)
		}
		if strings.Contains(err.Error(), value) {
			t.Fatalf("error echoed unsafe input %q: %v", value, err)
		}
	}
}

func TestBuildAllowsPortableMultilineDynamicText(t *testing.T) {
	expected := []ExpectedCell{testExpectedCell()}
	record := testRecord(t, expected[0], testEvidenceID(1), testRunSetID(1), "a")
	record.Assertions[0].Message = "first observation\nsecond observation"
	record.Conclusion = "The bound held.\nThe retry path was exercised."
	record.ContentDigest = ""
	sealed, err := evidence.Seal(record)
	if err != nil {
		t.Fatalf("Seal multiline record: %v", err)
	}
	if _, err := Build(testInputDigest("a"), expected, []evidence.Record{sealed}); err != nil {
		t.Fatalf("Build rejected portable multiline dynamic text: %v", err)
	}
}
