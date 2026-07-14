package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

const supportedSchemaVersion = 1

func DecodeStrict[T any](path string, data []byte) (T, error) {
	var document T
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return document, fmt.Errorf("%s: %w", path, err)
	}

	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return document, fmt.Errorf("%s: %w", path, err)
		}
		return document, fmt.Errorf("%s: line %d: multiple YAML documents are not allowed", path, nodeLine(&trailing))
	}

	var source yaml.Node
	if err := yaml.Unmarshal(data, &source); err != nil {
		return document, fmt.Errorf("%s: %w", path, err)
	}

	versionNode := mappingNodeValue(resolvedYAMLNode(&source), "schema_version")
	version, ok := schemaVersionFromNode(versionNode)
	if !ok || version != supportedSchemaVersion {
		return document, fmt.Errorf("%s: line %d: unsupported schema_version %s", path, mappingValueLine(&source, "schema_version"), schemaVersionText(versionNode))
	}
	if line, ok := emptyStableIDLine(document, &source); ok {
		return document, fmt.Errorf("%s: line %d: empty stable ID", path, line)
	}

	return document, nil
}

func schemaVersionFromNode(node *yaml.Node) (uint64, bool) {
	valueNode := resolvedYAMLNode(node)
	if valueNode == nil || valueNode.Kind != yaml.ScalarNode || valueNode.Tag != "!!int" {
		return 0, false
	}

	plain := strings.ReplaceAll(valueNode.Value, "_", "")
	value, err := strconv.ParseUint(plain, 0, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func schemaVersionText(node *yaml.Node) string {
	valueNode := resolvedYAMLNode(node)
	if valueNode == nil {
		return "0"
	}
	return valueNode.Value
}

func mappingValueLine(document *yaml.Node, key string) int {
	node := document
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	if node.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(node.Content); index += 2 {
			if node.Content[index].Value == key {
				return nodeLine(node.Content[index+1])
			}
		}
	}
	return nodeLine(node)
}

func emptyStableIDLine(document any, source *yaml.Node) (int, bool) {
	return emptyStableIDValue(reflect.ValueOf(document), source)
}

func emptyStableIDValue(value reflect.Value, node *yaml.Node) (int, bool) {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return 0, false
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return 0, false
	}

	switch value.Kind() {
	case reflect.Struct:
		mapping := resolvedYAMLNode(node)
		typeOfValue := value.Type()
		for index := 0; index < value.NumField(); index++ {
			fieldType := typeOfValue.Field(index)
			if fieldType.PkgPath != "" {
				continue
			}
			fieldName, include := yamlFieldName(fieldType)
			if !include {
				continue
			}
			fieldValue := value.Field(index)
			fieldNode := mappingNodeValue(mapping, fieldName)
			if fieldType.Name == "ID" && fieldName == "id" && fieldValue.Kind() == reflect.String && fieldValue.String() == "" {
				if fieldNode != nil {
					return nodeLine(fieldNode), true
				}
				return nodeLine(mapping), true
			}
			if line, ok := emptyStableIDValue(fieldValue, fieldNode); ok {
				return line, true
			}
		}
	case reflect.Slice, reflect.Array:
		sequence := resolvedYAMLNode(node)
		for index := 0; index < value.Len(); index++ {
			var itemNode *yaml.Node
			if sequence != nil && sequence.Kind == yaml.SequenceNode && index < len(sequence.Content) {
				itemNode = sequence.Content[index]
			}
			if line, ok := emptyStableIDValue(value.Index(index), itemNode); ok {
				return line, true
			}
		}
	}
	return 0, false
}

func yamlFieldName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("yaml")
	name, _, _ := strings.Cut(tag, ",")
	if name == "-" {
		return "", false
	}
	if name == "" {
		name = strings.ToLower(field.Name)
	}
	return name, true
}

func mappingNodeValue(mapping *yaml.Node, fieldName string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == fieldName {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func resolvedYAMLNode(node *yaml.Node) *yaml.Node {
	seen := make(map[*yaml.Node]struct{})
	for node != nil {
		if _, exists := seen[node]; exists {
			return node
		}
		seen[node] = struct{}{}
		switch {
		case node.Kind == yaml.DocumentNode && len(node.Content) > 0:
			node = node.Content[0]
		case node.Kind == yaml.AliasNode && node.Alias != nil:
			node = node.Alias
		default:
			return node
		}
	}
	return nil
}

func nodeLine(node *yaml.Node) int {
	if node == nil {
		return 1
	}
	if node.Line > 0 {
		return node.Line
	}
	if len(node.Content) > 0 {
		return nodeLine(node.Content[0])
	}
	return 1
}
