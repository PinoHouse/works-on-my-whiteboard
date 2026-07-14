package inputdigest

import (
	"errors"
	"io/fs"
	"testing"
)

func TestComputeEntriesFrozenGolden(t *testing.T) {
	entries := []Entry{
		{Path: "README.md", Mode: 0o644, Data: []byte("hello\n")},
		{Path: "scripts/run.sh", Mode: 0o755, Data: []byte("#!/bin/sh\nexit 0\n")},
	}

	got, err := ComputeEntries(entries)
	if err != nil {
		t.Fatalf("ComputeEntries: %v", err)
	}
	const want = Digest("sha256:7985c74be03783034436e0c7ee76f9956016f151ceca6e94770c59575ac2a332")
	if got != want {
		t.Fatalf("digest = %q, want %q", got, want)
	}
}

func TestComputeEntriesIsOrderIndependentAndDoesNotAliasData(t *testing.T) {
	firstData := []byte("a")
	first := []Entry{
		{Path: "z", Mode: 0o644, Data: []byte("z")},
		{Path: "a", Mode: 0o644, Data: firstData},
	}
	second := []Entry{
		{Path: "a", Mode: 0o644, Data: []byte("a")},
		{Path: "z", Mode: 0o644, Data: []byte("z")},
	}

	want, err := ComputeEntries(first)
	if err != nil {
		t.Fatalf("first digest: %v", err)
	}
	firstData[0] = 'x'
	got, err := ComputeEntries(second)
	if err != nil {
		t.Fatalf("second digest: %v", err)
	}
	if got != want {
		t.Fatalf("reordered digest = %q, want %q", got, want)
	}
}

func TestComputeEntriesSeparatesEveryFramedField(t *testing.T) {
	base := []Entry{{Path: "ab", Mode: 0o644, Data: []byte("c")}}
	baseDigest, err := ComputeEntries(base)
	if err != nil {
		t.Fatalf("base digest: %v", err)
	}
	cases := map[string][]Entry{
		"path":    {{Path: "a", Mode: 0o644, Data: []byte("c")}},
		"mode":    {{Path: "ab", Mode: 0o755, Data: []byte("c")}},
		"content": {{Path: "ab", Mode: 0o644, Data: []byte("bc")}},
		"count": {
			{Path: "a", Mode: 0o644, Data: []byte("b")},
			{Path: "c", Mode: 0o644, Data: []byte{}},
		},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			got, computeErr := ComputeEntries(entries)
			if computeErr != nil {
				t.Fatalf("ComputeEntries: %v", computeErr)
			}
			if got == baseDigest {
				t.Fatalf("changed %s retained digest %q", name, got)
			}
		})
	}
}

func TestComputeEntriesPathAndModeValidation(t *testing.T) {
	invalidPaths := []string{"", "/absolute", "a\\b", ".", "..", "a/./b", "a/../b", "a//b", "a\x00b", "\xff"}
	for _, path := range invalidPaths {
		t.Run(path, func(t *testing.T) {
			_, err := ComputeEntries([]Entry{{Path: path, Mode: 0o644, Data: []byte("x")}})
			if !errors.Is(err, ErrInvalidEntry) {
				t.Fatalf("error = %v, want ErrInvalidEntry", err)
			}
		})
	}

	for _, mode := range []fs.FileMode{0, 0o600, 0o777, fs.ModeSymlink | 0o777} {
		_, err := ComputeEntries([]Entry{{Path: "a", Mode: mode, Data: []byte("x")}})
		if !errors.Is(err, ErrInvalidEntry) {
			t.Fatalf("mode %v error = %v, want ErrInvalidEntry", mode, err)
		}
	}

	_, err := ComputeEntries([]Entry{
		{Path: "same", Mode: 0o644, Data: []byte("a")},
		{Path: "same", Mode: 0o755, Data: []byte("b")},
	})
	if !errors.Is(err, ErrDuplicateEntry) {
		t.Fatalf("duplicate error = %v, want ErrDuplicateEntry", err)
	}

	if _, err := ComputeEntries([]Entry{{Path: "tab\tline\nname", Mode: 0o644, Data: []byte("ok")}}); err != nil {
		t.Fatalf("TAB/LF path rejected: %v", err)
	}
}

func TestParseRequiresExactLowercaseSHA256(t *testing.T) {
	valid := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	got, err := Parse(valid)
	if err != nil || got != Digest(valid) {
		t.Fatalf("Parse(valid) = %q, %v", got, err)
	}

	invalid := []string{
		"", "sha256:", "SHA256:" + valid[7:], "sha256:" + valid[7:len(valid)-1],
		"sha256:0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef",
		"sha256:g123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		valid + "\n",
	}
	for _, value := range invalid {
		if _, parseErr := Parse(value); !errors.Is(parseErr, ErrInvalidDigest) {
			t.Fatalf("Parse(%q) error = %v, want ErrInvalidDigest", value, parseErr)
		}
	}
}

func FuzzComputeEntries(f *testing.F) {
	f.Add("path", []byte("payload"), false)
	f.Add("tab\tline\nname", []byte{0, 1, 2, 255}, true)
	f.Add("../invalid", []byte("x"), false)
	f.Fuzz(func(t *testing.T, path string, data []byte, executable bool) {
		mode := fs.FileMode(0o644)
		if executable {
			mode = 0o755
		}
		entries := []Entry{{Path: path, Mode: mode, Data: data}}
		first, err := ComputeEntries(entries)
		if err != nil {
			return
		}
		if _, err := Parse(string(first)); err != nil {
			t.Fatalf("ComputeEntries returned an invalid digest %q: %v", first, err)
		}
		second, err := ComputeEntries(entries)
		if err != nil || second != first {
			t.Fatalf("ComputeEntries is nondeterministic: first=%q second=%q err=%v", first, second, err)
		}
	})
}
