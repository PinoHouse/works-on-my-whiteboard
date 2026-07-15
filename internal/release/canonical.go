package release

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/evidence"
	"github.com/PinoHouse/works-on-my-whiteboard/internal/inputdigest"
)

var manifestFields = []string{"schema_version", "input_digest", "profile", "run_set_id", "selections"}
var selectionFields = []string{"role", "lab_id", "required_run_id", "binding_id", "claim_id", "implementation_id", "adapter_id", "evidence_id", "content_digest"}

func Encode(manifest Manifest) ([]byte, error) {
	if err := validateManifest(manifest); err != nil {
		return nil, err
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "schema_version: %d\n", manifest.SchemaVersion)
	writeYAMLField(&builder, "input_digest", string(manifest.InputDigest), 0)
	writeYAMLField(&builder, "profile", string(manifest.Profile), 0)
	writeYAMLField(&builder, "run_set_id", string(manifest.RunSetID), 0)
	builder.WriteString("selections:\n")
	for _, selection := range manifest.Selections {
		builder.WriteString("  - role: ")
		builder.WriteString(canonicalYAMLScalar(string(selection.Role)))
		builder.WriteByte('\n')
		writeYAMLField(&builder, "lab_id", selection.LabID, 4)
		writeYAMLField(&builder, "required_run_id", selection.RequiredRunID, 4)
		writeYAMLField(&builder, "binding_id", selection.BindingID, 4)
		writeYAMLField(&builder, "claim_id", selection.ClaimID, 4)
		writeYAMLField(&builder, "implementation_id", selection.ImplementationID, 4)
		writeYAMLField(&builder, "adapter_id", selection.AdapterID, 4)
		writeYAMLField(&builder, "evidence_id", selection.EvidenceID, 4)
		writeYAMLField(&builder, "content_digest", selection.ContentDigest, 4)
	}
	return []byte(builder.String()), nil
}

func writeYAMLField(builder *strings.Builder, name, value string, indent int) {
	builder.WriteString(strings.Repeat(" ", indent))
	builder.WriteString(name)
	builder.WriteString(": ")
	builder.WriteString(canonicalYAMLScalar(value))
	builder.WriteByte('\n')
}

func canonicalYAMLScalar(value string) string {
	if value == "" {
		return `""`
	}
	switch strings.ToLower(value) {
	case "null", "~", "true", "false", "yes", "no", "on", "off", ".nan", ".inf", "+.inf", "-.inf":
		return strconv.Quote(value)
	default:
		return value
	}
}

func Decode(data []byte) (Manifest, error) {
	if len(data) > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("%w: %d bytes", ErrManifestTooLarge, len(data))
	}
	if !utf8.Valid(data) {
		return Manifest{}, fmt.Errorf("%w: YAML is not valid UTF-8", ErrManifestInvalid)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return Manifest{}, fmt.Errorf("%w: decode YAML: %v", ErrManifestInvalid, err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return Manifest{}, fmt.Errorf("%w: trailing YAML: %v", ErrManifestInvalid, err)
		}
		return Manifest{}, fmt.Errorf("%w: multiple YAML documents", ErrManifestInvalid)
	}
	if err := rejectHostileYAMLNode(&document); err != nil {
		return Manifest{}, err
	}
	root, err := documentRoot(&document)
	if err != nil {
		return Manifest{}, err
	}
	fields, err := exactMapping(root, manifestFields)
	if err != nil {
		return Manifest{}, err
	}
	version, err := scalarUint32(fields["schema_version"])
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: schema_version: %v", ErrManifestInvalid, err)
	}
	inputText, err := scalarString(fields["input_digest"])
	if err != nil {
		return Manifest{}, err
	}
	input, err := inputdigest.Parse(inputText)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: input_digest: %v", ErrManifestInvalid, err)
	}
	profile, err := scalarString(fields["profile"])
	if err != nil {
		return Manifest{}, err
	}
	runSetID, err := scalarString(fields["run_set_id"])
	if err != nil {
		return Manifest{}, err
	}
	selectionNode := fields["selections"]
	if selectionNode.Kind != yaml.SequenceNode || selectionNode.Tag != "!!seq" || len(selectionNode.Content) == 0 {
		return Manifest{}, fmt.Errorf("%w: selections must be a nonempty sequence", ErrManifestInvalid)
	}
	manifest := Manifest{
		SchemaVersion: version,
		InputDigest:   input,
		Profile:       evidence.Profile(profile),
		RunSetID:      evidence.RunSetID(runSetID),
		Selections:    make([]Selection, 0, len(selectionNode.Content)),
	}
	for index, item := range selectionNode.Content {
		selectionFieldsByName, mappingErr := exactMapping(item, selectionFields)
		if mappingErr != nil {
			return Manifest{}, fmt.Errorf("%w: selection %d: %v", ErrManifestInvalid, index, mappingErr)
		}
		values := make(map[string]string, len(selectionFields))
		for _, name := range selectionFields {
			value, scalarErr := scalarString(selectionFieldsByName[name])
			if scalarErr != nil {
				return Manifest{}, fmt.Errorf("%w: selection %d field %s: %v", ErrManifestInvalid, index, name, scalarErr)
			}
			values[name] = value
		}
		manifest.Selections = append(manifest.Selections, Selection{
			Role:             evidence.Role(values["role"]),
			LabID:            values["lab_id"],
			RequiredRunID:    values["required_run_id"],
			BindingID:        values["binding_id"],
			ClaimID:          values["claim_id"],
			ImplementationID: values["implementation_id"],
			AdapterID:        values["adapter_id"],
			EvidenceID:       values["evidence_id"],
			ContentDigest:    values["content_digest"],
		})
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	canonical, err := Encode(manifest)
	if err != nil {
		return Manifest{}, err
	}
	if !bytes.Equal(data, canonical) {
		return Manifest{}, fmt.Errorf("%w: bytes differ from canonical encoding", ErrManifestNonCanonical)
	}
	return cloneManifest(manifest), nil
}

func rejectHostileYAMLNode(node *yaml.Node) error {
	if node == nil {
		return fmt.Errorf("%w: nil YAML node", ErrManifestInvalid)
	}
	if node.Kind == yaml.AliasNode || node.Alias != nil || node.Anchor != "" {
		return fmt.Errorf("%w: aliases and anchors are forbidden", ErrManifestInvalid)
	}
	if node.HeadComment != "" || node.LineComment != "" || node.FootComment != "" {
		return fmt.Errorf("%w: comments are forbidden", ErrManifestInvalid)
	}
	if node.Tag == "!!merge" || node.Value == "<<" {
		return fmt.Errorf("%w: merge keys are forbidden", ErrManifestInvalid)
	}
	if strings.HasPrefix(node.Tag, "!") && node.Tag != "!!map" && node.Tag != "!!seq" && node.Tag != "!!str" && node.Tag != "!!int" {
		return fmt.Errorf("%w: tag %q is forbidden", ErrManifestInvalid, node.Tag)
	}
	if node.Tag == "!!null" {
		return fmt.Errorf("%w: null fields are forbidden", ErrManifestInvalid)
	}
	for _, child := range node.Content {
		if err := rejectHostileYAMLNode(child); err != nil {
			return err
		}
	}
	return nil
}

func documentRoot(document *yaml.Node) (*yaml.Node, error) {
	if document == nil || document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return nil, fmt.Errorf("%w: expected one YAML document", ErrManifestInvalid)
	}
	return document.Content[0], nil
}

func exactMapping(node *yaml.Node, required []string) (map[string]*yaml.Node, error) {
	if node == nil || node.Kind != yaml.MappingNode || node.Tag != "!!map" || len(node.Content)%2 != 0 {
		return nil, fmt.Errorf("%w: expected mapping", ErrManifestInvalid)
	}
	allowed := make(map[string]struct{}, len(required))
	for _, name := range required {
		allowed[name] = struct{}{}
	}
	values := make(map[string]*yaml.Node, len(required))
	for index := 0; index < len(node.Content); index += 2 {
		keyNode := node.Content[index]
		if keyNode.Kind != yaml.ScalarNode || keyNode.Tag != "!!str" {
			return nil, fmt.Errorf("%w: mapping key must be a string", ErrManifestInvalid)
		}
		name := keyNode.Value
		if _, exists := values[name]; exists {
			return nil, fmt.Errorf("%w: duplicate decoded mapping key %q", ErrManifestInvalid, name)
		}
		if _, exists := allowed[name]; !exists {
			return nil, fmt.Errorf("%w: unknown field %q", ErrManifestInvalid, name)
		}
		values[name] = node.Content[index+1]
	}
	for _, name := range required {
		if _, exists := values[name]; !exists {
			return nil, fmt.Errorf("%w: missing field %q", ErrManifestInvalid, name)
		}
	}
	return values, nil
}

func scalarString(node *yaml.Node) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", fmt.Errorf("%w: expected string scalar", ErrManifestInvalid)
	}
	return node.Value, nil
}

func scalarUint32(node *yaml.Node) (uint32, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!int" {
		return 0, fmt.Errorf("expected integer scalar")
	}
	value, err := strconv.ParseUint(node.Value, 10, 32)
	return uint32(value), err
}
