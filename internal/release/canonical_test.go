package release

import (
	"bytes"
	"strings"
	"testing"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
)

func TestCanonicalManifestGolden(t *testing.T) {
	manifest := testManifest(t)
	encoded, err := Encode(manifest)
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	want := "schema_version: 1\n" +
		"input_digest: sha256:" + strings.Repeat("a", 64) + "\n" +
		"profile: deep\n" +
		"run_set_id: " + string(testRunSetID(1)) + "\n" +
		"selections:\n" +
		"  - role: baseline\n" +
		"    lab_id: lab\n" +
		"    required_run_id: required-run\n" +
		"    binding_id: binding\n" +
		"    claim_id: claim\n" +
		"    implementation_id: implementation\n" +
		"    adapter_id: \"\"\n" +
		"    evidence_id: " + testEvidenceID(1) + "\n" +
		"    content_digest: " + manifest.Selections[0].ContentDigest + "\n"
	if string(encoded) != want {
		t.Fatalf("canonical YAML differs\n--- got ---\n%s--- want ---\n%s", encoded, want)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode canonical bytes: %v", err)
	}
	reencoded, err := Encode(decoded)
	if err != nil {
		t.Fatalf("re-Encode: %v", err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Fatalf("round trip bytes differ: %q != %q", encoded, reencoded)
	}
}

func TestDecodeRejectsNoncanonicalAndHostileYAML(t *testing.T) {
	canonical, err := Encode(testManifest(t))
	if err != nil {
		t.Fatalf("Encode fixture: %v", err)
	}
	tests := []struct {
		name string
		data []byte
	}{
		{name: "invalid UTF-8", data: append(append([]byte{}, canonical...), 0xff)},
		{name: "extra newline", data: append(append([]byte{}, canonical...), '\n')},
		{name: "document marker", data: append([]byte("---\n"), canonical...)},
		{name: "comment", data: append([]byte("# comment\n"), canonical...)},
		{name: "directive", data: append([]byte("%YAML 1.2\n---\n"), canonical...)},
		{name: "multiple documents", data: append(append([]byte{}, canonical...), []byte("---\n{}\n")...)},
		{name: "unknown field", data: append(append([]byte{}, canonical...), []byte("unknown: value\n")...)},
		{name: "duplicate decoded key", data: bytes.Replace(canonical, []byte("schema_version: 1\n"), []byte("schema_version: 1\nschema_version: 1\n"), 1)},
		{name: "duplicate escaped decoded key", data: bytes.Replace(canonical, []byte("profile: deep\n"), []byte("\"pro\\u0066ile\": deep\nprofile: deep\n"), 1)},
		{name: "missing field", data: bytes.Replace(canonical, []byte("profile: deep\n"), nil, 1)},
		{name: "null profile", data: bytes.Replace(canonical, []byte("profile: deep"), []byte("profile: null"), 1)},
		{name: "anchor", data: bytes.Replace(canonical, []byte("profile: deep"), []byte("profile: &profile deep"), 1)},
		{name: "alias", data: bytes.Replace(canonical, []byte("profile: deep\nrun_set_id: "+string(testRunSetID(1))), []byte("profile: &profile deep\nrun_set_id: *profile"), 1)},
		{name: "merge", data: bytes.Replace(canonical, []byte("profile: deep\n"), []byte("<<: {profile: deep}\nprofile: deep\n"), 1)},
		{name: "custom tag", data: bytes.Replace(canonical, []byte("profile: deep"), []byte("profile: !custom deep"), 1)},
		{name: "alternate quoting", data: bytes.Replace(canonical, []byte("profile: deep"), []byte("profile: \"deep\""), 1)},
		{name: "reordered", data: bytes.Replace(canonical, []byte("profile: deep\nrun_set_id: "+string(testRunSetID(1))+"\n"), []byte("run_set_id: "+string(testRunSetID(1))+"\nprofile: deep\n"), 1)},
		{name: "unsupported schema", data: bytes.Replace(canonical, []byte("schema_version: 1"), []byte("schema_version: 2"), 1)},
		{name: "empty selections", data: bytes.Replace(canonical, canonical[bytes.Index(canonical, []byte("selections:")):], []byte("selections: []\n"), 1)},
		{name: "trailing bytes", data: append(append([]byte{}, canonical...), []byte("not-yaml")...)},
		{name: "oversized", data: bytes.Repeat([]byte("x"), MaxManifestBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Decode(test.data); err == nil {
				t.Fatal("Decode returned nil error")
			}
		})
	}
}

func TestEncodeRejectsDuplicateAndNoncanonicalSelections(t *testing.T) {
	first := testExpectedCell()
	second := testSecondExpectedCell()
	firstRecord := testRecord(t, first, testEvidenceID(1), testRunSetID(1), "a")
	secondRecord := testRecord(t, second, testEvidenceID(2), testRunSetID(1), "a")
	manifest, err := Build(testInputDigest("a"), []ExpectedCell{first, second}, []evidence.Record{firstRecord, secondRecord})
	if err != nil {
		t.Fatalf("Build fixture: %v", err)
	}

	t.Run("order", func(t *testing.T) {
		mutated := cloneManifest(manifest)
		mutated.Selections[0], mutated.Selections[1] = mutated.Selections[1], mutated.Selections[0]
		if _, err := Encode(mutated); err == nil {
			t.Fatal("Encode returned nil error")
		}
	})
	t.Run("cell", func(t *testing.T) {
		mutated := cloneManifest(manifest)
		mutated.Selections[1].LabID = mutated.Selections[0].LabID
		if _, err := Encode(mutated); err == nil {
			t.Fatal("Encode returned nil error")
		}
	})
	t.Run("evidence ID", func(t *testing.T) {
		mutated := cloneManifest(manifest)
		mutated.Selections[1].EvidenceID = mutated.Selections[0].EvidenceID
		if _, err := Encode(mutated); err == nil {
			t.Fatal("Encode returned nil error")
		}
	})
}

func testManifest(t *testing.T) Manifest {
	t.Helper()
	expected := []ExpectedCell{testExpectedCell()}
	record := testRecord(t, expected[0], testEvidenceID(1), testRunSetID(1), "b")
	manifest, err := Build(testInputDigest("a"), expected, []evidence.Record{record})
	if err != nil {
		t.Fatalf("Build fixture: %v", err)
	}
	return manifest
}
