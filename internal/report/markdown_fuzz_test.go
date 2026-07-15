package report

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzMarkdownTableEscaping(f *testing.F) {
	for _, seed := range []string{"plain", "a|b", "a\\b", "a\r\nb", "\x1b[31mred", "中文｜内容", string([]byte{0xff, '|', '\n'})} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		escaped := escapeMarkdownTableCell(source)
		if !utf8.ValidString(escaped) {
			t.Fatalf("escaped output is invalid UTF-8: %q", escaped)
		}
		if strings.ContainsAny(escaped, "\r\n\x1b") {
			t.Fatalf("escaped output contains a raw line break or ANSI escape: %q", escaped)
		}
		for index := 0; index < len(escaped); index++ {
			if escaped[index] != '|' {
				continue
			}
			backslashes := 0
			for previous := index - 1; previous >= 0 && escaped[previous] == '\\'; previous-- {
				backslashes++
			}
			if backslashes%2 == 0 {
				t.Fatalf("table delimiter is not escaped: %q", escaped)
			}
		}
	})
}
