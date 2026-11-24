package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"polynux/disgoroq/config"
)

type configFieldKind string

const (
	fieldKindString      configFieldKind = "string"
	fieldKindText        configFieldKind = "text"
	fieldKindBool        configFieldKind = "bool"
	fieldKindInt         configFieldKind = "int"
	fieldKindFloat       configFieldKind = "float"
	fieldKindStringSlice configFieldKind = "string_slice"
)

// configFieldSpec describes a single leaf setting discovered from config.Config.
type configFieldSpec struct {
	Path     string // dotted YAML path, e.g. "ai.groq.model"
	Section  string // top-level YAML key, e.g. "ai"
	Subgroup string // nested path within section, e.g. "groq" or "tools.web"
	Key      string // leaf YAML key, e.g. "model"
	Kind     configFieldKind
	Default  any
	HotApply bool
	Secret   bool
}

// configSectionSpec groups fields belonging to one top-level config.Config section.
type configSectionSpec struct {
	Name   string
	Label  string
	Fields []configFieldSpec
}

var (
	pureEnvPlaceholderRe = regexp.MustCompile(`^\$\{[a-zA-Z_][a-zA-Z0-9_]*\}$`)

	longTextPaths = map[string]struct{}{
		"bot.default_prompt":        {},
		"voice.voice_system_prompt": {},
		"reengage.reengage_message": {},
	}

	explicitSecretPaths = map[string]struct{}{
		"discord.token":          {},
		"database.url":           {},
		"database.token":         {},
		"memory.summary_api_key": {},
	}

	sectionLabels = map[string]string{
		"discord":       "Discord",
		"database":      "Database",
		"ai":            "AI",
		"logging":       "Logging",
		"memory":        "Memory",
		"emoji":         "Emoji",
		"horoscope":     "Horoscope",
		"reengage":      "Reengage",
		"votes":         "Votes",
		"media_library": "Media Library",
		"bot":           "Bot",
		"voice":         "Voice",
		"web":           "Web",
	}
)

const maskedSecretDisplay = "••••••••"

// buildConfigSchema reflects config.Config and config.DefaultConfig() to build
// the ordered list of sections and leaf field specifications.
func buildConfigSchema() []configSectionSpec {
	defaults := config.DefaultConfig()
	rootVal := reflect.ValueOf(defaults).Elem()
	rootType := rootVal.Type()

	sections := make([]configSectionSpec, 0, rootType.NumField())
	for i := 0; i < rootType.NumField(); i++ {
		sf := rootType.Field(i)
		if !sf.IsExported() {
			continue
		}
		tag := yamlTagName(sf)
		if tag == "" || tag == "-" {
			continue
		}
		fv := rootVal.Field(i)
		if fv.Kind() != reflect.Struct {
			continue
		}
		sec := configSectionSpec{
			Name:  tag,
			Label: sectionLabel(tag),
		}
		collectSectionFields(&sec, tag, "", fv)
		sections = append(sections, sec)
	}
	return sections
}

func collectSectionFields(sec *configSectionSpec, section, prefix string, v reflect.Value) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		tag := yamlTagName(sf)
		if tag == "" || tag == "-" {
			continue
		}

		fv := v.Field(i)
		relPath := tag
		if prefix != "" {
			relPath = prefix + "." + tag
		}
		fullPath := section + "." + relPath

		if fv.Kind() == reflect.Struct {
			collectSectionFields(sec, section, relPath, fv)
			continue
		}

		kind, defVal, ok := reflectFieldKindAndDefault(fullPath, fv)
		if !ok {
			continue
		}

		sec.Fields = append(sec.Fields, configFieldSpec{
			Path:     fullPath,
			Section:  section,
			Subgroup: prefix,
			Key:      tag,
			Kind:     kind,
			Default:  defVal,
			HotApply: config.IsHotApply(fullPath),
			Secret:   isSecretPath(fullPath, tag),
		})
	}
}

func reflectFieldKindAndDefault(fullPath string, fv reflect.Value) (configFieldKind, any, bool) {
	switch fv.Kind() {
	case reflect.String:
		s := fv.String()
		if isLongTextField(fullPath, s) {
			return fieldKindText, s, true
		}
		return fieldKindString, s, true
	case reflect.Bool:
		return fieldKindBool, fv.Bool(), true
	case reflect.Int, reflect.Int64:
		return fieldKindInt, fv.Int(), true
	case reflect.Float64:
		return fieldKindFloat, fv.Float(), true
	case reflect.Slice:
		if fv.Type().Elem().Kind() == reflect.String {
			out := make([]string, fv.Len())
			for i := 0; i < fv.Len(); i++ {
				out[i] = fv.Index(i).String()
			}
			return fieldKindStringSlice, out, true
		}
	}
	return "", nil, false
}

func isLongTextField(path, defaultVal string) bool {
	if _, ok := longTextPaths[path]; ok {
		return true
	}
	return strings.Contains(defaultVal, "\n") || len(defaultVal) > 100
}

func isSecretPath(fullPath, leafKey string) bool {
	if _, ok := explicitSecretPaths[fullPath]; ok {
		return true
	}
	if strings.HasPrefix(fullPath, "ai.") && strings.HasSuffix(fullPath, ".api_key") {
		return true
	}
	lower := strings.ToLower(leafKey)
	return strings.HasSuffix(lower, "token") ||
		strings.HasSuffix(lower, "api_key") ||
		strings.HasSuffix(lower, "password")
}

func yamlTagName(sf reflect.StructField) string {
	tag := sf.Tag.Get("yaml")
	if tag == "" {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	return name
}

func sectionLabel(name string) string {
	if lbl, ok := sectionLabels[name]; ok {
		return lbl
	}
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func computeFileHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// parseYAMLDocument parses raw YAML bytes into a yaml.Node document.
// An empty file produces a DocumentNode containing an empty MappingNode.
func parseYAMLDocument(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if len(bytes.TrimSpace(data)) == 0 {
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
		return &doc, nil
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("yaml root must be a mapping")
	}
	return &doc, nil
}

// marshalYAMLDocument encodes a yaml.Node document with 2-space indentation,
// preserving comments and placeholders.
func marshalYAMLDocument(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		_ = enc.Close()
		return nil, fmt.Errorf("marshal yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close yaml encoder: %w", err)
	}
	return buf.Bytes(), nil
}

// getYAMLNode looks up a dotted path in a DocumentNode or MappingNode.
func getYAMLNode(doc *yaml.Node, dottedPath string) (*yaml.Node, bool) {
	root := documentMapping(doc)
	if root == nil {
		return nil, false
	}
	parts := strings.Split(dottedPath, ".")
	curr := root
	for i, part := range parts {
		if curr.Kind != yaml.MappingNode {
			return nil, false
		}
		var found *yaml.Node
		for j := 0; j+1 < len(curr.Content); j += 2 {
			if curr.Content[j].Value == part {
				found = curr.Content[j+1]
				break
			}
		}
		if found == nil {
			return nil, false
		}
		if i == len(parts)-1 {
			return found, true
		}
		curr = found
	}
	return nil, false
}

// setYAMLValue sets or inserts a scalar or []string sequence at dottedPath,
// creating missing intermediate mapping nodes while preserving existing comments.
func setYAMLValue(doc *yaml.Node, dottedPath string, kind configFieldKind, val any) error {
	root := documentMapping(doc)
	if root == nil {
		return fmt.Errorf("yaml document has no root mapping")
	}
	parts := strings.Split(dottedPath, ".")
	curr := root
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		if curr.Kind != yaml.MappingNode {
			return fmt.Errorf("path %q traverses non-mapping node at %q", dottedPath, part)
		}
		var next *yaml.Node
		for j := 0; j+1 < len(curr.Content); j += 2 {
			if curr.Content[j].Value == part {
				next = curr.Content[j+1]
				break
			}
		}
		if next == nil {
			keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: part}
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			curr.Content = append(curr.Content, keyNode, next)
		} else if next.Kind != yaml.MappingNode {
			return fmt.Errorf("path %q expects mapping at %q", dottedPath, part)
		}
		curr = next
	}

	leafKey := parts[len(parts)-1]
	if curr.Kind != yaml.MappingNode {
		return fmt.Errorf("path %q parent is not a mapping", dottedPath)
	}

	for j := 0; j+1 < len(curr.Content); j += 2 {
		if curr.Content[j].Value == leafKey {
			return applyValueToNode(curr.Content[j+1], kind, val)
		}
	}

	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: leafKey}
	valNode := &yaml.Node{}
	if err := applyValueToNode(valNode, kind, val); err != nil {
		return err
	}
	curr.Content = append(curr.Content, keyNode, valNode)
	return nil
}

func applyValueToNode(node *yaml.Node, kind configFieldKind, val any) error {
	switch kind {
	case fieldKindString, fieldKindText:
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("expected string value, got %T", val)
		}
		node.Kind = yaml.ScalarNode
		node.Tag = "!!str"
		node.Style = 0
		node.Value = s
		node.Content = nil
		return nil
	case fieldKindBool:
		b, ok := val.(bool)
		if !ok {
			return fmt.Errorf("expected bool value, got %T", val)
		}
		node.Kind = yaml.ScalarNode
		node.Tag = "!!bool"
		node.Style = 0
		node.Value = strconv.FormatBool(b)
		node.Content = nil
		return nil
	case fieldKindInt:
		n, ok := val.(int64)
		if !ok {
			return fmt.Errorf("expected int64 value, got %T", val)
		}
		node.Kind = yaml.ScalarNode
		node.Tag = "!!int"
		node.Style = 0
		node.Value = strconv.FormatInt(n, 10)
		node.Content = nil
		return nil
	case fieldKindFloat:
		f, ok := val.(float64)
		if !ok {
			return fmt.Errorf("expected float64 value, got %T", val)
		}
		formatted := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(formatted, ".") {
			formatted += ".0"
		}
		node.Kind = yaml.ScalarNode
		node.Tag = "!!float"
		node.Style = 0
		node.Value = formatted
		node.Content = nil
		return nil
	case fieldKindStringSlice:
		items, ok := val.([]string)
		if !ok {
			return fmt.Errorf("expected []string value, got %T", val)
		}
		node.Kind = yaml.SequenceNode
		node.Tag = "!!seq"
		node.Style = 0
		node.Value = ""
		content := make([]*yaml.Node, 0, len(items))
		for _, item := range items {
			content = append(content, &yaml.Node{
				Kind:  yaml.ScalarNode,
				Tag:   "!!str",
				Value: item,
			})
		}
		node.Content = content
		return nil
	default:
		return fmt.Errorf("unsupported config field kind %q", kind)
	}
}

func documentMapping(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return nil
		}
		return doc.Content[0]
	}
	if doc.Kind == yaml.MappingNode {
		return doc
	}
	return nil
}

// nodeHasEnvPlaceholder reports whether a scalar or sequence node contains a "${" placeholder.
func nodeHasEnvPlaceholder(node *yaml.Node) bool {
	if node == nil {
		return false
	}
	if node.Kind == yaml.ScalarNode {
		return strings.Contains(node.Value, "${")
	}
	if node.Kind == yaml.SequenceNode {
		for _, item := range node.Content {
			if nodeHasEnvPlaceholder(item) {
				return true
			}
		}
	}
	return false
}

// writeConfigAtomically copies the current bytes to <path>.bak and atomically
// writes newData to path via a temporary file in the same directory + fsync + rename,
// preserving the original file mode.
func writeConfigAtomically(path string, origData, newData []byte, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o600
	}

	bakPath := path + ".bak"
	if err := os.WriteFile(bakPath, origData, mode); err != nil {
		return fmt.Errorf("write backup %s: %w", bakPath, err)
	}

	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, ".config.yaml.tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(newData); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Chmod(mode); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp file over %s: %w", path, err)
	}
	cleanup = false
	return nil
}
