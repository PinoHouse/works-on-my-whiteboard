package evidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

func Decode(data []byte) (Record, error) {
	if len(data) > MaxRecordBytes {
		return Record{}, fmt.Errorf("%w: %d bytes", ErrTooLarge, len(data))
	}
	if !utf8.Valid(data) {
		return Record{}, fmt.Errorf("%w: JSON is not valid UTF-8", ErrInvalidRecord)
	}
	if err := validateJSONTokens(data); err != nil {
		return Record{}, err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var decoded Record
	if err := decoder.Decode(&decoded); err != nil {
		return Record{}, fmt.Errorf("%w: decode JSON: %v", ErrInvalidRecord, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Record{}, fmt.Errorf("%w: trailing JSON value or bytes", ErrInvalidRecord)
	}

	storedDigest := decoded.ContentDigest
	normalized := cloneAndNormalize(decoded)
	if err := validateRecord(normalized, true); err != nil {
		return Record{}, err
	}
	wantDigest, err := calculateContentDigest(normalized)
	if err != nil {
		return Record{}, err
	}
	if storedDigest != string(wantDigest) {
		return Record{}, fmt.Errorf("%w: self-digest mismatch", ErrContentDigest)
	}
	normalized.ContentDigest = storedDigest
	canonical, err := Encode(normalized)
	if err != nil {
		return Record{}, err
	}
	if !bytes.Equal(data, canonical) {
		return Record{}, fmt.Errorf("%w: bytes differ from canonical encoding", ErrNonCanonical)
	}
	return normalized, nil
}

func validateJSONTokens(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing token %v", ErrInvalidRecord, token)
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: invalid JSON: %v", ErrInvalidRecord, err)
	}
	if token == nil {
		return fmt.Errorf("%w: null fields are forbidden", ErrInvalidRecord)
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			if keyErr != nil {
				return fmt.Errorf("%w: invalid object key: %v", ErrInvalidRecord, keyErr)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("%w: object key is not a string", ErrInvalidRecord)
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("%w: duplicate decoded object key %q", ErrInvalidRecord, key)
			}
			seen[key] = struct{}{}
			if valueErr := scanJSONValue(decoder); valueErr != nil {
				return valueErr
			}
		}
		end, endErr := decoder.Token()
		if endErr != nil || end != json.Delim('}') {
			return fmt.Errorf("%w: unterminated object", ErrInvalidRecord)
		}
	case '[':
		for decoder.More() {
			if valueErr := scanJSONValue(decoder); valueErr != nil {
				return valueErr
			}
		}
		end, endErr := decoder.Token()
		if endErr != nil || end != json.Delim(']') {
			return fmt.Errorf("%w: unterminated array", ErrInvalidRecord)
		}
	default:
		return fmt.Errorf("%w: unexpected delimiter %q", ErrInvalidRecord, delimiter)
	}
	return nil
}
