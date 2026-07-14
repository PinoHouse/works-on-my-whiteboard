package evidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
)

func Encode(record Record) ([]byte, error) {
	if _, err := inputdigest.Parse(record.ContentDigest); err != nil {
		return nil, fmt.Errorf("%w: record is not sealed", ErrContentDigest)
	}
	if err := validateRecord(record, true); err != nil {
		return nil, err
	}
	want, err := calculateContentDigest(record)
	if err != nil {
		return nil, err
	}
	if record.ContentDigest != string(want) {
		return nil, fmt.Errorf("%w: self-digest mismatch", ErrContentDigest)
	}
	compact, err := canonicalJSON(record)
	if err != nil {
		return nil, err
	}
	return append(compact, '\n'), nil
}

func calculateContentDigest(record Record) (inputdigest.Digest, error) {
	record.ContentDigest = ""
	compact, err := canonicalJSON(record)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(compact)
	return inputdigest.Digest(fmt.Sprintf("sha256:%x", digest[:])), nil
}

func canonicalJSON(record Record) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(record); err != nil {
		return nil, fmt.Errorf("%w: encode canonical JSON: %v", ErrInvalidRecord, err)
	}
	encoded := buffer.Bytes()
	if len(encoded) == 0 || encoded[len(encoded)-1] != '\n' {
		return nil, fmt.Errorf("%w: JSON encoder omitted terminator", ErrInvalidRecord)
	}
	return append([]byte(nil), encoded[:len(encoded)-1]...), nil
}
