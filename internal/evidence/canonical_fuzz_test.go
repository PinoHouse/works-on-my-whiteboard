package evidence

import (
	"bytes"
	"testing"
)

func FuzzCanonicalRecord(f *testing.F) {
	sealed, err := Seal(validRecord())
	if err != nil {
		f.Fatalf("Seal seed: %v", err)
	}
	canonical, err := Encode(sealed)
	if err != nil {
		f.Fatalf("Encode seed: %v", err)
	}
	f.Add(canonical)
	f.Add([]byte(`{"schema_version":null}` + "\n"))
	f.Add([]byte(`{"schema_version":1,"schema_\u0076ersion":1}` + "\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		decoded, decodeErr := Decode(data)
		if decodeErr != nil {
			return
		}
		canonical := append([]byte(nil), data...)
		if len(data) > 0 {
			data[0] ^= 0xff
		}
		reencoded, encodeErr := Encode(decoded)
		if encodeErr != nil {
			t.Fatalf("accepted record cannot be re-encoded: %v", encodeErr)
		}
		if !bytes.Equal(canonical, reencoded) {
			t.Fatalf("accepted bytes are not a fixed point or decoded record aliases input bytes")
		}
	})
}
