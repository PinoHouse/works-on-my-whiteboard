package release

import (
	"errors"
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
)

var ErrUnsafeDynamicText = errors.New("unsafe dynamic release text")

var (
	generatedIdentityPattern = regexp.MustCompile(`(?:run|set)-[0-9]{8}T[0-9]{6}\.[0-9]{3}Z-[0-9a-f]{32}`)
	uriSchemePattern         = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*:`)
)

// ValidateDynamicText rejects runtime text that would make a release artifact
// host- or attempt-specific. It never includes the rejected value in its error.
func ValidateDynamicText(value string) error {
	if !safeDynamicTextForm(value) {
		return ErrUnsafeDynamicText
	}
	percentNormalized := decodeValidPercentTripletsOnce(value)
	if percentNormalized != value && !safeDynamicTextForm(percentNormalized) {
		return ErrUnsafeDynamicText
	}
	entityNormalized := html.UnescapeString(value)
	if entityNormalized != value && !safeDynamicTextForm(entityNormalized) {
		return ErrUnsafeDynamicText
	}
	return nil
}

func decodeValidPercentTripletsOnce(value string) string {
	first := strings.IndexByte(value, '%')
	if first < 0 {
		return value
	}
	var decoded strings.Builder
	decoded.Grow(len(value))
	decoded.WriteString(value[:first])
	for index := first; index < len(value); index++ {
		if value[index] == '%' && index+2 < len(value) {
			high, highOK := percentHexValue(value[index+1])
			low, lowOK := percentHexValue(value[index+2])
			if highOK && lowOK {
				decoded.WriteByte(high<<4 | low)
				index += 2
				continue
			}
		}
		decoded.WriteByte(value[index])
	}
	return decoded.String()
}

func percentHexValue(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}

func safeDynamicTextForm(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' || unicode.Is(unicode.Cf, character) {
			return false
		}
	}
	if containsAbsolutePath(value) || containsGeneratedIdentity(value) {
		return false
	}
	return true
}

func containsGeneratedIdentity(value string) bool {
	for _, match := range generatedIdentityPattern.FindAllStringIndex(value, -1) {
		candidate := value[match[0]:match[1]]
		if strings.HasPrefix(candidate, "run-") {
			if evidence.ValidateID(candidate) == nil {
				return true
			}
			continue
		}
		if evidence.ValidateRunSetID(evidence.RunSetID(candidate)) == nil {
			return true
		}
	}
	return false
}

func containsAbsolutePath(value string) bool {
	if strings.TrimSpace(value) == "/" {
		return true
	}
	for _, match := range uriSchemePattern.FindAllStringIndex(value, -1) {
		scheme := value[match[0] : match[1]-1]
		if unsafeURIScheme(scheme, value[match[1]:]) {
			return true
		}
	}
	for index := 0; index < len(value); index++ {
		if value[index] == '~' && index+1 < len(value) && (value[index+1] == '/' || value[index+1] == '\\') && dynamicTextBoundary(value, index) {
			return true
		}
		if isASCIIAlpha(value[index]) && index+2 < len(value) && value[index+1] == ':' && (value[index+2] == '/' || value[index+2] == '\\') && dynamicTextBoundary(value, index) {
			return true
		}
		if value[index] == '\\' && dynamicTextBoundary(value, index) {
			if index+1 < len(value) && !unicode.IsSpace(rune(value[index+1])) {
				return true
			}
		}
		if value[index] != '/' || !dynamicTextBoundary(value, index) {
			continue
		}
		if index > 0 && index+1 < len(value) && value[index-1] == ':' && value[index+1] == '/' {
			continue
		}
		if index+1 < len(value) && unicode.IsSpace(rune(value[index+1])) {
			continue
		}
		return true
	}
	return false
}

func unsafeURIScheme(scheme, remainder string) bool {
	if strings.EqualFold(scheme, "https") {
		return false
	}
	switch strings.ToLower(scheme) {
	case "file", "smb", "nfs", "afp":
		return true
	}
	lowerRemainder := strings.ToLower(remainder)
	return strings.HasPrefix(remainder, "/") || strings.HasPrefix(remainder, `\`) ||
		strings.HasPrefix(lowerRemainder, "%2f") || strings.HasPrefix(lowerRemainder, "%5c")
}

func dynamicTextBoundary(value string, index int) bool {
	if index == 0 {
		return true
	}
	previous, _ := utf8.DecodeLastRuneInString(value[:index])
	if previous == '/' || previous == '\\' {
		return false
	}
	return unicode.IsSpace(previous) || unicode.IsPunct(previous) || unicode.IsSymbol(previous)
}

func isASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}
