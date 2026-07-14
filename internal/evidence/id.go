package evidence

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

func NewID(now time.Time, entropy io.Reader) (string, error) {
	return newID("run-", now, entropy)
}

func NewRunSetID(now time.Time, entropy io.Reader) (RunSetID, error) {
	value, err := newID("set-", now, entropy)
	return RunSetID(value), err
}

func NewRandomID(now time.Time) (string, error) {
	return NewID(now, rand.Reader)
}

func NewRandomRunSetID(now time.Time) (RunSetID, error) {
	return NewRunSetID(now, rand.Reader)
}

func ValidateID(value string) error {
	if !validAttemptID(value, "run-", attemptIDPattern) {
		return fmt.Errorf("%w: invalid attempt ID", ErrInvalidRecord)
	}
	return nil
}

func ValidateRunSetID(value RunSetID) error {
	if !validAttemptID(string(value), "set-", runSetIDPattern) {
		return fmt.Errorf("%w: invalid run-set ID", ErrInvalidRecord)
	}
	return nil
}

func newID(prefix string, now time.Time, entropy io.Reader) (string, error) {
	if entropy == nil {
		return "", fmt.Errorf("entropy reader is nil")
	}
	if now.IsZero() {
		return "", fmt.Errorf("ID time is zero")
	}
	var random [16]byte
	if _, err := io.ReadFull(entropy, random[:]); err != nil {
		return "", fmt.Errorf("read ID entropy: %w", err)
	}
	timestamp := now.UTC().Truncate(time.Millisecond).Format("20060102T150405.000Z")
	return prefix + timestamp + "-" + hex.EncodeToString(random[:]), nil
}
