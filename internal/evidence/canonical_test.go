package evidence

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCanonicalEncodeDecodeRoundTripAndLiteralGolden(t *testing.T) {
	sealed, err := Seal(validRecord())
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	encoded, err := Encode(sealed)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !bytes.HasSuffix(encoded, []byte{'\n'}) || bytes.HasSuffix(encoded, []byte("\n\n")) {
		t.Fatalf("canonical bytes must have exactly one LF: %q", encoded)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !reflect.DeepEqual(decoded, sealed) {
		t.Fatalf("decoded record differs from sealed record:\n%#v\nwant:\n%#v", decoded, sealed)
	}
	reencoded, err := Encode(decoded)
	if err != nil || !bytes.Equal(reencoded, encoded) {
		t.Fatalf("re-encode mismatch: %v\n%s\n%s", err, encoded, reencoded)
	}

	// These literals were calculated independently with Node's JSON.stringify
	// and crypto SHA-256 over the same object with content_digest empty.
	const wantDigest = "sha256:f2f7303b1c3393a95755b02c050104635d860dda7528a5d2b94fcdfbce350b12"
	const wantCanonical = `{"schema_version":1,"id":"run-20260714T120000.123Z-000102030405060708090a0b0c0d0e0f","run_set_id":"set-20260714T120000.123Z-101112131415161718191a1b1c1d1e1f","lab_id":"token-bucket","required_run_id":"burst-boundary","binding_id":"token-bucket-boundary","claim_id":"token-bucket-bounds-burst","role":"baseline","implementation_id":"token-bucket","adapter_id":"","profile":"deep","hypothesis":"A burst cannot exceed capacity.","workload":{"id":"burst-boundary","parameters":{"capacity":4}},"faults":[],"status":"passed","source_commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","input_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","environment":{"go_version":"go1.26.5","os":"darwin","arch":"arm64","cpu":"unknown","logical_cpus":10},"seed":1,"deadline":2000000000,"started_at":"2026-07-14T12:00:00Z","finished_at":"2026-07-14T12:00:01Z","events_executed":5,"parameters":{"capacity":4},"measurements":{"requests.total":{"unit":"requests","value":5}},"assertions":[{"id":"all-requests-decided","passed":true,"message":""}],"diagnostics":[],"conclusion":"The bounded run passed.","limitations":["local deterministic model"],"content_digest":"sha256:f2f7303b1c3393a95755b02c050104635d860dda7528a5d2b94fcdfbce350b12"}` + "\n"
	if sealed.ContentDigest != wantDigest {
		t.Fatalf("content digest = %q, want independent literal %q", sealed.ContentDigest, wantDigest)
	}
	if string(encoded) != wantCanonical {
		t.Fatalf("canonical bytes differ from independent literal:\n%s\nwant:\n%s", encoded, wantCanonical)
	}
}

func TestDecodeRejectsHostileAndNoncanonicalJSON(t *testing.T) {
	sealed, err := Seal(validRecord())
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	canonical, err := Encode(sealed)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	base := string(canonical)
	reordered := strings.Replace(
		base,
		`{"schema_version":1,"id":"run-20260714T120000.123Z-000102030405060708090a0b0c0d0e0f"`,
		`{"id":"run-20260714T120000.123Z-000102030405060708090a0b0c0d0e0f","schema_version":1`,
		1,
	)
	tests := map[string][]byte{
		"missing final LF":        bytes.TrimSuffix(canonical, []byte{'\n'}),
		"extra final LF":          append(append([]byte{}, canonical...), '\n'),
		"leading whitespace":      append([]byte(" "), canonical...),
		"unknown field":           []byte(strings.Replace(base, `"content_digest":`, `"unknown":"x","content_digest":`, 1)),
		"missing field":           []byte(strings.Replace(base, `"adapter_id":"",`, "", 1)),
		"null field":              []byte(strings.Replace(base, `"adapter_id":""`, `"adapter_id":null`, 1)),
		"duplicate raw key":       []byte(strings.Replace(base, `"adapter_id":""`, `"adapter_id":"","adapter_id":""`, 1)),
		"duplicate escaped key":   []byte(strings.Replace(base, `"adapter_id":""`, `"adapter_id":"","adapter_\u0069d":""`, 1)),
		"reordered fields":        []byte(reordered),
		"alternate time spelling": []byte(strings.Replace(base, `2026-07-14T12:00:00Z`, `2026-07-14T12:00:00+00:00`, 1)),
		"trailing value":          append(append([]byte{}, canonical...), []byte(`{}`)...),
		"invalid UTF-8":           append([]byte{'{', '"', 0xff}, canonical...),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, decodeErr := Decode(data); decodeErr == nil {
				t.Fatalf("Decode accepted hostile bytes: %q", data)
			}
		})
	}
}

func TestDecodeRejectsContentDigestTampering(t *testing.T) {
	sealed, err := Seal(validRecord())
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	encoded, err := Encode(sealed)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	tampered := bytes.Replace(encoded, []byte(`"value":5`), []byte(`"value":6`), 1)
	if _, err := Decode(tampered); !errors.Is(err, ErrContentDigest) {
		t.Fatalf("error = %v, want ErrContentDigest", err)
	}
}

func TestEncodeRequiresAlreadySealedValidRecord(t *testing.T) {
	if _, err := Encode(validRecord()); !errors.Is(err, ErrContentDigest) {
		t.Fatalf("unsealed Encode error = %v", err)
	}
	sealed, err := Seal(validRecord())
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	sealed.Parameters["capacity"] = 9
	if _, err := Encode(sealed); !errors.Is(err, ErrInvalidRecord) && !errors.Is(err, ErrContentDigest) {
		t.Fatalf("mutated sealed Encode error = %v", err)
	}
}
