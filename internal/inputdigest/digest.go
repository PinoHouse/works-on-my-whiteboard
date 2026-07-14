package inputdigest

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

const digestPrefix = "sha256:"

var (
	ErrInvalidDigest  = errors.New("invalid input digest")
	ErrInvalidEntry   = errors.New("invalid input entry")
	ErrDuplicateEntry = errors.New("duplicate input entry")
	ErrRepository     = errors.New("invalid repository state")
	ErrDirty          = errors.New("repository inputs are dirty")
	ErrUnsafeInput    = errors.New("unsafe repository input")
	ErrStateChanged   = errors.New("repository state changed during inspection")
)

type Digest string

type Entry struct {
	Path string
	Mode fs.FileMode
	Data []byte
}

type State struct {
	InputDigest  Digest
	SourceCommit string
}

func Parse(value string) (Digest, error) {
	if len(value) != len(digestPrefix)+64 || !strings.HasPrefix(value, digestPrefix) {
		return "", fmt.Errorf("%w: want sha256 followed by 64 lowercase hexadecimal characters", ErrInvalidDigest)
	}
	for _, character := range value[len(digestPrefix):] {
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'f' {
			continue
		}
		return "", fmt.Errorf("%w: want sha256 followed by 64 lowercase hexadecimal characters", ErrInvalidDigest)
	}
	return Digest(value), nil
}

func ComputeEntries(source []Entry) (Digest, error) {
	entries := make([]Entry, len(source))
	for index, entry := range source {
		if err := validateEntry(entry); err != nil {
			return "", err
		}
		entries[index] = Entry{
			Path: entry.Path,
			Mode: entry.Mode,
			Data: append([]byte(nil), entry.Data...),
		}
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Path < entries[right].Path
	})
	for index := 1; index < len(entries); index++ {
		if entries[index-1].Path == entries[index].Path {
			return "", fmt.Errorf("%w: path %q", ErrDuplicateEntry, entries[index].Path)
		}
	}

	hasher := sha256.New()
	_, _ = hasher.Write([]byte("works-on-my-whiteboard-input-digest\x00v1\x00"))
	writeLength(hasher, uint64(len(entries)))
	for _, entry := range entries {
		mode := canonicalMode(entry.Mode)
		writeBytes(hasher, []byte(entry.Path))
		writeBytes(hasher, []byte(mode))
		writeBytes(hasher, entry.Data)
	}
	return Digest(fmt.Sprintf("%s%x", digestPrefix, hasher.Sum(nil))), nil
}

type byteWriter interface {
	Write([]byte) (int, error)
}

func writeLength(writer byteWriter, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = writer.Write(encoded[:])
}

func writeBytes(writer byteWriter, value []byte) {
	writeLength(writer, uint64(len(value)))
	_, _ = writer.Write(value)
}

func validateEntry(entry Entry) error {
	if !validInputPath(entry.Path) {
		return fmt.Errorf("%w: path %q is not canonical", ErrInvalidEntry, entry.Path)
	}
	if canonicalMode(entry.Mode) == "" {
		return fmt.Errorf("%w: path %q has mode %v", ErrInvalidEntry, entry.Path, entry.Mode)
	}
	return nil
}

func validInputPath(value string) bool {
	if value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') || strings.ContainsRune(value, '\\') || strings.HasPrefix(value, "/") {
		return false
	}
	if path.Clean(value) != value {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

func canonicalMode(mode fs.FileMode) string {
	switch mode {
	case 0o644:
		return "100644"
	case 0o755:
		return "100755"
	default:
		return ""
	}
}
