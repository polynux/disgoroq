package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

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

// yamlPatch describes a single dotted-path value update to apply in place.
type yamlPatch struct {
	Path string
	Kind configFieldKind
	Val  any
}

// patchUserError represents a user-actionable YAML structure error (such as a
// flow-style section or flow sequence with interior comments) that should be
// returned as HTTP 422 rather than an internal 500 error.
type patchUserError struct {
	msg string
}

func (e *patchUserError) Error() string {
	return e.msg
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

	yamlReservedPlainWords = map[string]struct{}{
		"true": {}, "false": {},
		"yes": {}, "no": {},
		"on": {}, "off": {},
		"y": {}, "n": {},
		"null": {}, "~": {},
		".inf": {}, "+.inf": {}, "-.inf": {}, ".nan": {},
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

// patchYAMLBytes applies each patch in place to orig without re-marshalling the
// document, preserving all untouched lines, comments, blank lines, quoting, and
// indentation. After patching, it re-parses the result and asserts that only
// the intended paths changed.
func patchYAMLBytes(orig []byte, patches []yamlPatch) ([]byte, error) {
	if len(patches) == 0 {
		return append([]byte(nil), orig...), nil
	}

	origDoc, err := parseYAMLDocument(orig)
	if err != nil {
		return nil, err
	}

	cur := append([]byte(nil), orig...)
	eol := detectLineEnding(orig)
	for _, p := range patches {
		cur, err = patchSinglePath(cur, p, eol)
		if err != nil {
			var userErr *patchUserError
			if errors.As(err, &userErr) {
				return nil, err
			}
			return nil, fmt.Errorf("patch %s: %w", p.Path, err)
		}
	}

	patchedDoc, err := parseYAMLDocument(cur)
	if err != nil {
		return nil, fmt.Errorf("re-parse patched yaml: %w", err)
	}
	if err := verifyPatchedDocument(origDoc, patchedDoc, patches); err != nil {
		return nil, err
	}

	return cur, nil
}

func detectLineEnding(src []byte) string {
	crlf := bytes.Count(src, []byte("\r\n"))
	lf := bytes.Count(src, []byte("\n")) - crlf
	if crlf > lf {
		return "\r\n"
	}
	return "\n"
}

type lineIndex struct {
	src    []byte
	starts []int // 0-based index -> byte offset of (i+1)-th line
}

func buildLineIndex(src []byte) lineIndex {
	starts := []int{0}
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return lineIndex{src: src, starts: starts}
}

func (li lineIndex) lineCount() int {
	return len(li.starts)
}

func (li lineIndex) lineStart(line int) int {
	if line <= 1 {
		return 0
	}
	if line > len(li.starts) {
		return len(li.src)
	}
	return li.starts[line-1]
}

// lineEnd returns the byte offset of the '\n' (or '\r' before '\n') ending 1-based line,
// or len(src) for the final line without a trailing newline.
func (li lineIndex) lineEnd(line int) int {
	var end int
	if line < len(li.starts) {
		end = li.starts[line] - 1 // position of '\n'
	} else {
		end = len(li.src)
	}
	if end > li.lineStart(line) && li.src[end-1] == '\r' {
		end--
	}
	return end
}

// lineEndWithNewline returns the byte offset immediately after 1-based line's '\n' (or len(src)).
func (li lineIndex) lineEndWithNewline(line int) int {
	if line < len(li.starts) {
		return li.starts[line]
	}
	return len(li.src)
}

// offsetAt converts a 1-based (line, runeColumn) pair into a byte offset.
func (li lineIndex) offsetAt(line, col int) int {
	pos := li.lineStart(line)
	if col <= 1 {
		return pos
	}
	end := li.lineEnd(line)
	rem := col - 1
	for rem > 0 && pos < end {
		_, size := utf8.DecodeRune(li.src[pos:end])
		pos += size
		rem--
	}
	return pos
}

func patchSinglePath(src []byte, patch yamlPatch, eol string) ([]byte, error) {
	doc, err := parseYAMLDocument(src)
	if err != nil {
		return nil, err
	}
	root := documentMapping(doc)
	if root == nil {
		return nil, fmt.Errorf("yaml document has no root mapping")
	}

	parts := strings.Split(patch.Path, ".")
	curr := root
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		if curr.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("path %q traverses non-mapping node at %q", patch.Path, part)
		}

		var keyNode, valNode *yaml.Node
		for j := 0; j+1 < len(curr.Content); j += 2 {
			if curr.Content[j].Value == part {
				keyNode = curr.Content[j]
				valNode = curr.Content[j+1]
				break
			}
		}

		if i == len(parts)-1 {
			if keyNode != nil {
				return replaceExistingValue(src, keyNode, valNode, patch, eol)
			}
			return insertMissingKeys(src, curr, i == 0, nil, nil, parts[i:], patch, eol)
		}

		if keyNode == nil {
			return insertMissingKeys(src, curr, i == 0, nil, nil, parts[i:], patch, eol)
		}
		if valNode.Kind == yaml.MappingNode {
			if valNode.Style == yaml.FlowStyle {
				if len(valNode.Content) == 0 {
					// Empty flow mapping (e.g. "votes: {}") -> replace with block mapping.
					return insertMissingKeys(src, nil, false, keyNode, valNode, parts[i+1:], patch, eol)
				}
				return nil, &patchUserError{
					msg: fmt.Sprintf("section %q uses flow style; convert it to block style to edit it here", part),
				}
			}
			curr = valNode
			continue
		}
		if valNode.Kind == yaml.ScalarNode && valNode.Tag == "!!null" {
			// Empty/null section (e.g. "votes:", "votes: null", "votes: ~") -> convert to block mapping.
			return insertMissingKeys(src, nil, false, keyNode, valNode, parts[i+1:], patch, eol)
		}
		return nil, fmt.Errorf("path %q expects mapping at %q", patch.Path, part)
	}

	return nil, fmt.Errorf("empty patch path")
}

func replaceExistingValue(src []byte, keyNode, valNode *yaml.Node, patch yamlPatch, eol string) ([]byte, error) {
	li := buildLineIndex(src)
	keyIndent := keyNode.Column - 1
	if keyIndent < 0 {
		keyIndent = 0
	}

	colonPos, ok := findKeyColonOffset(li, keyNode)
	if !ok {
		return nil, fmt.Errorf("could not locate ':' for key %q on line %d", keyNode.Value, keyNode.Line)
	}

	if patch.Kind == fieldKindStringSlice {
		items, ok := patch.Val.([]string)
		if !ok {
			return nil, fmt.Errorf("expected []string, got %T", patch.Val)
		}
		return replaceSequenceValue(src, li, keyNode, valNode, colonPos, keyIndent, items, eol)
	}

	emitted, err := emitScalarValue(patch.Kind, patch.Val, keyIndent, eol)
	if err != nil {
		return nil, err
	}
	isBlockReplacement := strings.HasPrefix(emitted, "|-")

	// Empty / implicit null value on the key line (e.g. "foo:" or "foo: # comment").
	if valNode.Kind == yaml.ScalarNode && valNode.Tag == "!!null" && valNode.Value == "" && valNode.Style == 0 {
		insertPos := colonPos + 1
		lineEnd := li.lineEnd(keyNode.Line)
		rest := src[insertPos:lineEnd]
		if idx := findInlineCommentStart(rest); idx >= 0 {
			commentStart := insertPos + idx
			if isBlockReplacement {
				comment := string(bytes.TrimRight(src[commentStart:lineEnd], " \t"))
				replacement := " " + attachBlockHeaderComment(emitted, comment, eol)
				return spliceBytes(src, insertPos, lineEnd, []byte(replacement)), nil
			}
			replacement := " " + emitted + " "
			return spliceBytes(src, insertPos, commentStart, []byte(replacement)), nil
		}
		replacement := " " + emitted
		return spliceBytes(src, insertPos, lineEnd, []byte(replacement)), nil
	}

	start, end, err := valueByteSpan(src, li, keyNode, valNode)
	if err != nil {
		return nil, err
	}

	// Preserve header-line comment on an existing block scalar (e.g. "key: |- # keep me").
	if valNode.Kind == yaml.ScalarNode && (valNode.Style == yaml.LiteralStyle || valNode.Style == yaml.FoldedStyle) {
		headerLineEnd := li.lineEnd(valNode.Line)
		headerRest := src[start:headerLineEnd]
		if idx := findInlineCommentStart(headerRest); idx >= 0 {
			comment := string(bytes.TrimRight(headerRest[idx:], " \t"))
			if isBlockReplacement {
				emitted = attachBlockHeaderComment(emitted, comment, eol)
			} else {
				emitted = emitted + " " + comment
			}
		}
		return spliceBytes(src, start, end, []byte(emitted)), nil
	}

	// If replacing a non-block scalar with a block scalar ("|-"), move any trailing
	// same-line comment onto the "|-" header line so it doesn't end up inside the block.
	if isBlockReplacement {
		endLine := offsetToLine(li, end)
		lineEnd := li.lineEnd(endLine)
		trailing := src[end:lineEnd]
		if idx := findInlineCommentStart(trailing); idx >= 0 {
			comment := string(bytes.TrimRight(trailing[idx:], " \t"))
			emitted = attachBlockHeaderComment(emitted, comment, eol)
			end = lineEnd
		}
	}

	return spliceBytes(src, start, end, []byte(emitted)), nil
}

// attachBlockHeaderComment inserts " <comment>" immediately after the "|-" indicator
// on the first line of a literal block scalar.
func attachBlockHeaderComment(blockScalar, comment, eol string) string {
	if comment == "" {
		return blockScalar
	}
	if firstEOL := strings.Index(blockScalar, eol); firstEOL >= 0 {
		return blockScalar[:firstEOL] + " " + comment + blockScalar[firstEOL:]
	}
	return blockScalar + " " + comment
}

func replaceSequenceValue(src []byte, li lineIndex, keyNode, valNode *yaml.Node, colonPos, keyIndent int, items []string, eol string) ([]byte, error) {
	// Empty slice -> emit "[]" inline.
	if len(items) == 0 {
		if valNode.Kind == yaml.SequenceNode && valNode.Style == yaml.FlowStyle {
			start, end, err := valueByteSpan(src, li, keyNode, valNode)
			if err != nil {
				return nil, err
			}
			if flowContainsComment(src[start:end]) {
				return nil, &patchUserError{
					msg: fmt.Sprintf("field %q uses a flow sequence with interior comments; convert it to block style to edit it here", keyNode.Value),
				}
			}
			return spliceBytes(src, start, end, []byte("[]")), nil
		}
		if valNode.Kind == yaml.SequenceNode && len(valNode.Content) > 0 {
			lastLine := maxNodeLine(src, li, valNode)
			end := li.lineEnd(lastLine)
			return spliceBytes(src, colonPos+1, end, []byte(" []"+keyLineComment(src, li, keyNode, colonPos))), nil
		}
		if valNode.Kind == yaml.ScalarNode && valNode.Tag == "!!null" && valNode.Value == "" && valNode.Style == 0 {
			// Implicit null ("key:" or "key: # comment"): yaml.v3 positions it
			// past the end of the key line, so don't derive a span from it.
			return spliceBytes(src, colonPos+1, li.lineEnd(keyNode.Line), []byte(" []"+keyLineComment(src, li, keyNode, colonPos))), nil
		}
		// Null or scalar previously -> put " []" right after colon.
		start, end, err := valueByteSpan(src, li, keyNode, valNode)
		if err != nil {
			return spliceBytes(src, colonPos+1, li.lineEnd(keyNode.Line), []byte(" []")), nil
		}
		return spliceBytes(src, start, end, []byte("[]")), nil
	}

	// Preserve flow style if the existing node is flow style (e.g. trigger_words: ["feun", "feunboy"]).
	if valNode.Kind == yaml.SequenceNode && valNode.Style == yaml.FlowStyle {
		start, end, err := valueByteSpan(src, li, keyNode, valNode)
		if err != nil {
			return nil, err
		}
		if flowContainsComment(src[start:end]) {
			return nil, &patchUserError{
				msg: fmt.Sprintf("field %q uses a flow sequence with interior comments; convert it to block style to edit it here", keyNode.Value),
			}
		}
		emitted := emitFlowStringSlice(items)
		return spliceBytes(src, start, end, []byte(emitted)), nil
	}

	// Block sequence replacement.
	itemIndent := keyIndent + 2
	if valNode.Kind == yaml.SequenceNode && len(valNode.Content) > 0 {
		// Detect existing dash column on the first sequence item's line.
		firstItemLine := valNode.Content[0].Line
		lineText := src[li.lineStart(firstItemLine):li.lineEnd(firstItemLine)]
		if dashIdx := bytes.IndexByte(lineText, '-'); dashIdx >= 0 {
			itemIndent = dashIdx
		}
	}
	block := emitBlockStringSlice(items, itemIndent, eol)

	if valNode.Kind == yaml.SequenceNode && len(valNode.Content) > 0 {
		firstLine := valNode.Content[0].Line
		lastLine := maxNodeLine(src, li, valNode)
		start := li.lineStart(firstLine)
		end := li.lineEnd(lastLine)
		// Drop leading EOL from block because start is already at lineStart(firstLine).
		return spliceBytes(src, start, end, []byte(strings.TrimPrefix(block, eol))), nil
	}

	// Replacing a non-block node (e.g. empty/null) with a block sequence.
	lineEnd := li.lineEnd(keyNode.Line)
	comment := keyLineComment(src, li, keyNode, colonPos)
	if comment == "" && valNode.Kind == yaml.ScalarNode && valNode.Line == keyNode.Line {
		// Explicit value on the key line ("null # keep", "'' # keep"): keep a
		// comment that follows the scalar's exact end, so a "#" inside quotes
		// is never mistaken for one.
		if _, end, err := valueByteSpan(src, li, keyNode, valNode); err == nil && end <= lineEnd {
			after := src[end:lineEnd]
			if trimmed := bytes.TrimSpace(after); len(trimmed) > 0 && trimmed[0] == '#' {
				comment = " " + string(bytes.TrimRight(trimmed, " \t\r"))
			}
		}
	}
	return spliceBytes(src, colonPos+1, lineEnd, []byte(comment+block)), nil
}

// keyLineComment returns " # comment" when the rest of the key's line after the
// colon holds only a comment (no inline value), so it survives a replacement
// that rewrites that line. Otherwise it returns "".
func keyLineComment(src []byte, li lineIndex, keyNode *yaml.Node, colonPos int) string {
	rest := src[colonPos+1 : li.lineEnd(keyNode.Line)]
	idx := findInlineCommentStart(rest)
	if idx < 0 || len(bytes.TrimSpace(rest[:idx])) != 0 {
		return ""
	}
	return " " + string(bytes.TrimRight(rest[idx:], " \t\r"))
}

func insertMissingKeys(src []byte, parentMap *yaml.Node, isRoot bool, emptySectionKey, emptySectionVal *yaml.Node, remaining []string, patch yamlPatch, eol string) ([]byte, error) {
	li := buildLineIndex(src)

	var (
		baseIndent int
		spliceFrom int
		spliceTo   int
		prefix     string
		suffix     string
	)

	if emptySectionKey != nil {
		// Turning an empty/null/empty-flow section ("votes:", "votes: null", "votes: ~", "votes: {}")
		// into a block mapping while preserving any trailing comment on the section header line.
		baseIndent = (emptySectionKey.Column - 1) + 2
		colonPos, ok := findKeyColonOffset(li, emptySectionKey)
		if !ok {
			return nil, fmt.Errorf("could not locate ':' for section %q on line %d", emptySectionKey.Value, emptySectionKey.Line)
		}
		lineEnd := li.lineEnd(emptySectionKey.Line)
		// Remove any explicit null / ~ / {} value between ':' and comment/EOL.
		if emptySectionVal != nil && (emptySectionVal.Kind == yaml.MappingNode || emptySectionVal.Value != "") {
			valStart := colonPos + 1
			valEnd := lineEnd
			if emptySectionVal.Kind == yaml.MappingNode && emptySectionVal.Style == yaml.FlowStyle {
				if end, ok := scanFlowEnd(src, li.offsetAt(emptySectionVal.Line, emptySectionVal.Column), '{', '}'); ok {
					valEnd = end
				}
			} else if idx := findInlineCommentStart(src[valStart:lineEnd]); idx >= 0 {
				valEnd = valStart + idx
			}
			// Strip the old value (e.g. " {}", " null", " ~") and preserve any trailing comment on the header line.
			trailingComment := ""
			if valEnd < lineEnd {
				rest := src[valEnd:lineEnd]
				if idx := findInlineCommentStart(rest); idx >= 0 {
					trailingComment = " " + string(bytes.TrimRight(rest[idx:], " \t"))
				}
			}
			spliceFrom = valStart
			spliceTo = lineEnd
			prefix = trailingComment + eol
		} else {
			spliceFrom = lineEnd
			spliceTo = lineEnd
			prefix = eol
		}
	} else if isRoot {
		baseIndent = 0
		spliceFrom = len(src)
		spliceTo = len(src)
		if len(bytes.TrimSpace(src)) > 0 {
			if len(src) > 0 && src[len(src)-1] != '\n' {
				prefix = eol + eol
			} else if bytes.HasSuffix(src, []byte(eol+eol)) || (len(src) >= 2 && src[len(src)-2] == '\n') {
				prefix = ""
			} else {
				prefix = eol
			}
		}
		suffix = eol
	} else {
		if len(parentMap.Content) == 0 {
			return nil, fmt.Errorf("cannot determine indentation for empty parent mapping")
		}
		baseIndent = parentMap.Content[0].Column - 1
		if baseIndent < 0 {
			baseIndent = 0
		}
		lastLine := maxNodeLine(src, li, parentMap)
		spliceFrom = li.lineEnd(lastLine)
		spliceTo = spliceFrom
		prefix = eol
	}

	var b strings.Builder
	b.WriteString(prefix)
	for i, key := range remaining {
		indent := baseIndent + i*2
		b.WriteString(strings.Repeat(" ", indent))
		b.WriteString(key)
		b.WriteByte(':')
		if i < len(remaining)-1 {
			b.WriteString(eol)
			continue
		}
		// Leaf value.
		if patch.Kind == fieldKindStringSlice {
			items, ok := patch.Val.([]string)
			if !ok {
				return nil, fmt.Errorf("expected []string, got %T", patch.Val)
			}
			if len(items) == 0 {
				b.WriteString(" []")
			} else {
				b.WriteString(emitBlockStringSlice(items, indent+2, eol))
			}
		} else {
			emitted, err := emitScalarValue(patch.Kind, patch.Val, indent, eol)
			if err != nil {
				return nil, err
			}
			b.WriteByte(' ')
			b.WriteString(emitted)
		}
	}
	b.WriteString(suffix)

	return spliceBytes(src, spliceFrom, spliceTo, []byte(b.String())), nil
}

func findKeyColonOffset(li lineIndex, keyNode *yaml.Node) (int, bool) {
	start := li.offsetAt(keyNode.Line, keyNode.Column)
	lineEnd := li.lineEnd(keyNode.Line)
	if start >= lineEnd {
		return 0, false
	}
	// Advance past quoted key if quoted.
	pos := start
	switch li.src[pos] {
	case '"':
		end, ok := scanDoubleQuotedEnd(li.src, pos)
		if !ok || end > lineEnd {
			return 0, false
		}
		pos = end
	case '\'':
		end, ok := scanSingleQuotedEnd(li.src, pos)
		if !ok || end > lineEnd {
			return 0, false
		}
		pos = end
	}
	for pos < lineEnd {
		if li.src[pos] == ':' {
			return pos, true
		}
		pos++
	}
	return 0, false
}

// valueByteSpan returns the exact [start, end) byte offsets of valNode in src,
// excluding any trailing inline comment or blank separator lines.
func valueByteSpan(src []byte, li lineIndex, keyNode, valNode *yaml.Node) (int, int, error) {
	start := li.offsetAt(valNode.Line, valNode.Column)
	if start > len(src) {
		return 0, 0, fmt.Errorf("value start out of bounds")
	}

	if valNode.Kind == yaml.SequenceNode && valNode.Style == yaml.FlowStyle {
		end, ok := scanFlowEnd(src, start, '[', ']')
		if !ok {
			return 0, 0, fmt.Errorf("unterminated flow sequence at line %d", valNode.Line)
		}
		return start, end, nil
	}

	if valNode.Kind != yaml.ScalarNode {
		return 0, 0, fmt.Errorf("unexpected node kind %d at line %d", valNode.Kind, valNode.Line)
	}

	switch valNode.Style {
	case yaml.DoubleQuotedStyle:
		end, ok := scanDoubleQuotedEnd(src, start)
		if !ok {
			return 0, 0, fmt.Errorf("unterminated double-quoted string at line %d", valNode.Line)
		}
		return start, end, nil
	case yaml.SingleQuotedStyle:
		end, ok := scanSingleQuotedEnd(src, start)
		if !ok {
			return 0, 0, fmt.Errorf("unterminated single-quoted string at line %d", valNode.Line)
		}
		return start, end, nil
	case yaml.LiteralStyle, yaml.FoldedStyle:
		keyIndent := keyNode.Column - 1
		if keyIndent < 0 {
			keyIndent = 0
		}
		endLine := scanBlockScalarEndLine(src, li, valNode.Line, keyIndent)
		return start, li.lineEnd(endLine), nil
	default:
		// Plain scalar (single-line or multi-line).
		keyIndent := keyNode.Column - 1
		if keyIndent < 0 {
			keyIndent = 0
		}
		end, _ := scanPlainScalarEnd(src, li, start, valNode.Line, keyIndent)
		return start, end, nil
	}
}

// scanPlainScalarEnd returns the exclusive end byte offset and 1-based end line
// of a plain scalar starting at (startLine, start), spanning continuation lines
// indented deeper than keyIndent until a comment, dedented line, or EOF.
func scanPlainScalarEnd(src []byte, li lineIndex, start, startLine, keyIndent int) (int, int) {
	firstLineEnd := li.lineEnd(startLine)
	rest := src[start:firstLineEnd]
	if cIdx := findInlineCommentStart(rest); cIdx >= 0 {
		end := trimRightSpaces(src, start, start+cIdx)
		return end, startLine
	}

	lastEnd := trimRightSpaces(src, start, firstLineEnd)
	lastLine := startLine

	for line := startLine + 1; line <= li.lineCount(); line++ {
		ls := li.lineStart(line)
		le := li.lineEnd(line)
		lineBytes := src[ls:le]
		if len(bytes.TrimSpace(lineBytes)) == 0 {
			// Blank lines inside a multi-line plain scalar are only part of it if
			// followed by another indented continuation line.
			continue
		}
		indent := countLeadingSpaces(lineBytes)
		if indent <= keyIndent {
			break
		}
		trimmed := lineBytes[indent:]
		if trimmed[0] == '#' {
			break
		}
		if cIdx := findInlineCommentStart(trimmed); cIdx >= 0 {
			lastEnd = trimRightSpaces(src, ls+indent, ls+indent+cIdx)
			lastLine = line
			break
		}
		lastEnd = trimRightSpaces(src, ls+indent, le)
		lastLine = line
	}

	return lastEnd, lastLine
}

func trimRightSpaces(src []byte, start, end int) int {
	for end > start && (src[end-1] == ' ' || src[end-1] == '\t') {
		end--
	}
	return end
}

// flowContainsComment reports whether a flow sequence/mapping slice contains
// any YAML comment outside quoted strings.
func flowContainsComment(flow []byte) bool {
	for i := 0; i < len(flow); i++ {
		switch flow[i] {
		case '"':
			if end, ok := scanDoubleQuotedEnd(flow, i); ok {
				i = end - 1
			}
		case '\'':
			if end, ok := scanSingleQuotedEnd(flow, i); ok {
				i = end - 1
			}
		case '#':
			if i == 0 || flow[i-1] == ' ' || flow[i-1] == '\t' || flow[i-1] == '\n' || flow[i-1] == '\r' {
				return true
			}
		}
	}
	return false
}

func scanDoubleQuotedEnd(src []byte, start int) (int, bool) {
	if start >= len(src) || src[start] != '"' {
		return 0, false
	}
	for i := start + 1; i < len(src); i++ {
		if src[i] == '\\' {
			i++
			continue
		}
		if src[i] == '"' {
			return i + 1, true
		}
	}
	return 0, false
}

func scanSingleQuotedEnd(src []byte, start int) (int, bool) {
	if start >= len(src) || src[start] != '\'' {
		return 0, false
	}
	for i := start + 1; i < len(src); i++ {
		if src[i] == '\'' {
			if i+1 < len(src) && src[i+1] == '\'' {
				i++
				continue
			}
			return i + 1, true
		}
	}
	return 0, false
}

func scanFlowEnd(src []byte, start int, openCh, closeCh byte) (int, bool) {
	if start >= len(src) || src[start] != openCh {
		return 0, false
	}
	depth := 0
	for i := start; i < len(src); i++ {
		switch src[i] {
		case '"':
			end, ok := scanDoubleQuotedEnd(src, i)
			if !ok {
				return 0, false
			}
			i = end - 1
		case '\'':
			end, ok := scanSingleQuotedEnd(src, i)
			if !ok {
				return 0, false
			}
			i = end - 1
		case openCh:
			depth++
		case closeCh:
			depth--
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}

// blockIndentIndicatorRe matches a block scalar header ("|", ">-", "|2",
// "|+2", ...) and captures an explicit indentation indicator digit.
var blockIndentIndicatorRe = regexp.MustCompile(`(?:^|[\s:-])[|>][+-]?([1-9]?)[+-]?(?:\s|$)`)

// scanBlockScalarEndLine returns the 1-based line number of the last non-blank
// content line belonging to a block scalar starting on headerLine.
//
// The scalar ends at the first non-blank line indented less than its content
// indentation (the explicit indicator, or the first content line's indent). A
// comment indented between the key and the content is therefore outside the
// scalar, as YAML parses it, and is not swallowed by an edit.
func scanBlockScalarEndLine(src []byte, li lineIndex, headerLine, keyIndent int) int {
	contentIndent := -1
	header := src[li.lineStart(headerLine):li.lineEnd(headerLine)]
	if m := blockIndentIndicatorRe.FindSubmatch(header); m != nil && len(m[1]) == 1 {
		contentIndent = keyIndent + int(m[1][0]-'0')
	}

	lastContentLine := headerLine
	for line := headerLine + 1; line <= li.lineCount(); line++ {
		lineBytes := src[li.lineStart(line):li.lineEnd(line)]
		if len(bytes.TrimSpace(lineBytes)) == 0 {
			continue
		}
		indent := countLeadingSpaces(lineBytes)
		if contentIndent < 0 {
			if indent <= keyIndent {
				break // empty block scalar
			}
			contentIndent = indent
		}
		if indent >= contentIndent {
			lastContentLine = line
			continue
		}
		break
	}
	return lastContentLine
}

func countLeadingSpaces(b []byte) int {
	n := 0
	for n < len(b) && b[n] == ' ' {
		n++
	}
	return n
}

func findInlineCommentStart(b []byte) int {
	for i := 0; i < len(b); i++ {
		if b[i] == '#' && (i == 0 || b[i-1] == ' ' || b[i-1] == '\t') {
			return i
		}
	}
	return -1
}

// maxNodeLine returns the 1-based last line occupied by node (including block
// and multi-line quoted scalars), excluding trailing blank separator lines.
func maxNodeLine(src []byte, li lineIndex, node *yaml.Node) int {
	if node == nil {
		return 1
	}
	maxL := node.Line
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			k := node.Content[i]
			v := node.Content[i+1]
			if k.Line > maxL {
				maxL = k.Line
			}
			if vl := maxNodeLineWithKey(src, li, k, v); vl > maxL {
				maxL = vl
			}
		}
	case yaml.SequenceNode:
		if node.Style == yaml.FlowStyle {
			start := li.offsetAt(node.Line, node.Column)
			if end, ok := scanFlowEnd(src, start, '[', ']'); ok {
				if el := offsetToLine(li, end-1); el > maxL {
					maxL = el
				}
			}
		}
		for _, item := range node.Content {
			var il int
			if item.Kind == yaml.ScalarNode {
				itemLineIndent := countLeadingSpaces(src[li.lineStart(item.Line):li.lineEnd(item.Line)])
				il = scalarEndLine(src, li, itemLineIndent, item)
			} else {
				il = maxNodeLine(src, li, item)
			}
			if il > maxL {
				maxL = il
			}
		}
	case yaml.ScalarNode:
		lineIndent := countLeadingSpaces(src[li.lineStart(node.Line):li.lineEnd(node.Line)])
		if endL := scalarEndLine(src, li, lineIndent, node); endL > maxL {
			maxL = endL
		}
	}
	return maxL
}

func maxNodeLineWithKey(src []byte, li lineIndex, keyNode, valNode *yaml.Node) int {
	if valNode == nil {
		return keyNode.Line
	}
	if valNode.Kind == yaml.ScalarNode {
		keyIndent := keyNode.Column - 1
		if keyIndent < 0 {
			keyIndent = 0
		}
		return scalarEndLine(src, li, keyIndent, valNode)
	}
	return maxNodeLine(src, li, valNode)
}

func scalarEndLine(src []byte, li lineIndex, keyIndent int, node *yaml.Node) int {
	switch node.Style {
	case yaml.LiteralStyle, yaml.FoldedStyle:
		return scanBlockScalarEndLine(src, li, node.Line, keyIndent)
	case yaml.DoubleQuotedStyle:
		start := li.offsetAt(node.Line, node.Column)
		if end, ok := scanDoubleQuotedEnd(src, start); ok {
			return offsetToLine(li, end-1)
		}
	case yaml.SingleQuotedStyle:
		start := li.offsetAt(node.Line, node.Column)
		if end, ok := scanSingleQuotedEnd(src, start); ok {
			return offsetToLine(li, end-1)
		}
	default:
		start := li.offsetAt(node.Line, node.Column)
		_, endLine := scanPlainScalarEnd(src, li, start, node.Line, keyIndent)
		return endLine
	}
	return node.Line
}

func offsetToLine(li lineIndex, offset int) int {
	if offset <= 0 {
		return 1
	}
	lo, hi := 0, len(li.starts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if li.starts[mid] <= offset {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + 1
}

func spliceBytes(src []byte, start, end int, replacement []byte) []byte {
	out := make([]byte, 0, len(src)-(end-start)+len(replacement))
	out = append(out, src[:start]...)
	out = append(out, replacement...)
	out = append(out, src[end:]...)
	return out
}

// emitScalarValue formats a scalar according to the in-place patching rules and
// verifies that YAML parses it back to the exact intended value.
func emitScalarValue(kind configFieldKind, val any, keyIndent int, eol string) (string, error) {
	switch kind {
	case fieldKindBool:
		b, ok := val.(bool)
		if !ok {
			return "", fmt.Errorf("expected bool value, got %T", val)
		}
		return strconv.FormatBool(b), nil
	case fieldKindInt:
		n, ok := val.(int64)
		if !ok {
			return "", fmt.Errorf("expected int64 value, got %T", val)
		}
		return strconv.FormatInt(n, 10), nil
	case fieldKindFloat:
		f, ok := val.(float64)
		if !ok {
			return "", fmt.Errorf("expected float64 value, got %T", val)
		}
		return strconv.FormatFloat(f, 'f', -1, 64), nil
	case fieldKindString, fieldKindText:
		s, ok := val.(string)
		if !ok {
			return "", fmt.Errorf("expected string value, got %T", val)
		}
		return emitYAMLString(s, keyIndent, eol), nil
	default:
		return "", fmt.Errorf("unsupported scalar kind %q", kind)
	}
}

func emitYAMLString(s string, keyIndent int, eol string) string {
	var candidate string
	if strings.Contains(s, "\n") {
		if canUseLiteralBlock(s) {
			candidate = formatLiteralBlock(s, keyIndent+2, eol)
		} else {
			candidate = strconv.Quote(s)
		}
	} else if canUsePlainScalar(s) {
		candidate = s
	} else {
		candidate = strconv.Quote(s)
	}

	if verifyEmittedString(candidate, s) {
		return candidate
	}
	return strconv.Quote(s)
}

// canUseLiteralBlock returns true for multi-line strings with no leading newline,
// no trailing newline, no leading whitespace on the first line, no trailing spaces
// on any line, and no control characters other than '\n'.
func canUseLiteralBlock(s string) bool {
	if s == "" || strings.HasPrefix(s, "\n") || strings.HasSuffix(s, "\n") {
		return false
	}
	if s[0] == ' ' || s[0] == '\t' {
		return false
	}
	for _, r := range s {
		if r == '\n' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	for _, line := range strings.Split(s, "\n") {
		if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			return false
		}
	}
	return true
}

func formatLiteralBlock(s string, contentIndent int, eol string) string {
	pad := strings.Repeat(" ", contentIndent)
	lines := strings.Split(s, "\n")
	var b strings.Builder
	b.WriteString("|-")
	for _, line := range lines {
		b.WriteString(eol)
		if line != "" {
			b.WriteString(pad)
			b.WriteString(line)
		}
	}
	return b.String()
}

func canUsePlainScalar(s string) bool {
	if s == "" {
		return false
	}
	if strings.TrimSpace(s) != s {
		return false
	}
	if _, reserved := yamlReservedPlainWords[strings.ToLower(s)]; reserved {
		return false
	}
	if strings.Contains(s, "${") || strings.Contains(s, ": ") || strings.Contains(s, " #") || strings.HasSuffix(s, ":") {
		return false
	}
	switch s[0] {
	case '-', '?', ':', ',', '[', ']', '{', '}', '#', '&', '*', '!', '|', '>', '\'', '"', '%', '@', '`':
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	// Reject anything that looks like a number so YAML always resolves it as !!str.
	if _, err := strconv.ParseInt(s, 0, 64); err == nil {
		return false
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return false
	}
	return true
}

func verifyEmittedString(emitted, expected string) bool {
	var doc yaml.Node
	probe := "k: " + emitted + "\n"
	if err := yaml.Unmarshal([]byte(probe), &doc); err != nil {
		return false
	}
	root := documentMapping(&doc)
	if root == nil || len(root.Content) != 2 {
		return false
	}
	val := root.Content[1]
	return val.Kind == yaml.ScalarNode && val.Tag == "!!str" && val.Value == expected
}

func emitFlowStringSlice(items []string) string {
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = strconv.Quote(item)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func emitBlockStringSlice(items []string, itemIndent int, eol string) string {
	pad := strings.Repeat(" ", itemIndent)
	var b strings.Builder
	for _, item := range items {
		b.WriteString(eol)
		b.WriteString(pad)
		b.WriteString("- ")
		b.WriteString(emitYAMLString(item, itemIndent+2, eol))
	}
	return b.String()
}

// verifyPatchedDocument compares all leaf paths in origDoc and patchedDoc:
//  1. Every untouched path must remain present and semantically identical.
//  2. No unexpected paths may be inserted or removed.
//  3. Every intended patch path must re-parse to exactly patch.Val.
func verifyPatchedDocument(origDoc, patchedDoc *yaml.Node, patches []yamlPatch) error {
	intended := make(map[string]yamlPatch, len(patches))
	for _, p := range patches {
		intended[p.Path] = p
	}

	origLeaves := make(map[string]*yaml.Node)
	patchedLeaves := make(map[string]*yaml.Node)
	collectLeaves(documentMapping(origDoc), "", origLeaves)
	collectLeaves(documentMapping(patchedDoc), "", patchedLeaves)

	for path, origNode := range origLeaves {
		if _, ok := intended[path]; ok {
			continue
		}
		// An empty/null section (or empty flow mapping {}) may have been expanded
		// into a block mapping if one of its children is intended.
		if ((origNode.Kind == yaml.ScalarNode && origNode.Tag == "!!null") ||
			(origNode.Kind == yaml.MappingNode && len(origNode.Content) == 0)) &&
			hasIntendedDescendant(path, intended) {
			continue
		}
		newNode, exists := patchedLeaves[path]
		if !exists {
			return fmt.Errorf("internal error: unexpected removal of %q", path)
		}
		if !yamlNodesSemanticallyEqual(origNode, newNode) {
			return fmt.Errorf("internal error: unexpected modification of %q", path)
		}
	}

	for path := range patchedLeaves {
		if _, ok := intended[path]; ok {
			continue
		}
		if _, existed := origLeaves[path]; !existed {
			return fmt.Errorf("internal error: unexpected insertion of %q", path)
		}
	}

	for _, p := range patches {
		newNode, ok := getYAMLNode(patchedDoc, p.Path)
		if !ok {
			return fmt.Errorf("internal error: patched path %q missing after re-parse", p.Path)
		}
		if err := verifyNodeMatchesPatch(newNode, p); err != nil {
			return fmt.Errorf("internal error: patched path %q did not round-trip: %w", p.Path, err)
		}
	}
	return nil
}

func verifyNodeMatchesPatch(node *yaml.Node, p yamlPatch) error {
	switch p.Kind {
	case fieldKindString, fieldKindText:
		want, _ := p.Val.(string)
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || node.Value != want {
			return fmt.Errorf("got (%d,%s,%q), want string %q", node.Kind, node.Tag, node.Value, want)
		}
	case fieldKindBool:
		want, _ := p.Val.(bool)
		var got bool
		if node.Kind != yaml.ScalarNode || node.Tag != "!!bool" || node.Decode(&got) != nil || got != want {
			return fmt.Errorf("got (%s,%q), want bool %t", node.Tag, node.Value, want)
		}
	case fieldKindInt:
		want, _ := p.Val.(int64)
		var got int64
		if node.Kind != yaml.ScalarNode || node.Tag != "!!int" || node.Decode(&got) != nil || got != want {
			return fmt.Errorf("got (%s,%q), want int %d", node.Tag, node.Value, want)
		}
	case fieldKindFloat:
		want, _ := p.Val.(float64)
		var got float64
		if node.Kind != yaml.ScalarNode || (node.Tag != "!!float" && node.Tag != "!!int") || node.Decode(&got) != nil || got != want {
			return fmt.Errorf("got (%s,%q), want float %g", node.Tag, node.Value, want)
		}
	case fieldKindStringSlice:
		want, _ := p.Val.([]string)
		var got []string
		if node.Kind != yaml.SequenceNode || node.Decode(&got) != nil || !slices.Equal(got, want) {
			return fmt.Errorf("got %v, want %v", got, want)
		}
	}
	return nil
}

func hasIntendedDescendant(prefix string, intended map[string]yamlPatch) bool {
	dotPrefix := prefix + "."
	for p := range intended {
		if strings.HasPrefix(p, dotPrefix) {
			return true
		}
	}
	return false
}

func collectLeaves(node *yaml.Node, prefix string, out map[string]*yaml.Node) {
	if node == nil {
		return
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			k := node.Content[i].Value
			childPath := k
			if prefix != "" {
				childPath = prefix + "." + k
			}
			collectLeaves(node.Content[i+1], childPath, out)
		}
		return
	}
	if prefix != "" {
		out[prefix] = node
	}
}

func yamlNodesSemanticallyEqual(a, b *yaml.Node) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind || a.Tag != b.Tag {
		return false
	}
	switch a.Kind {
	case yaml.ScalarNode:
		return a.Value == b.Value
	case yaml.SequenceNode:
		if len(a.Content) != len(b.Content) {
			return false
		}
		for i := range a.Content {
			if !yamlNodesSemanticallyEqual(a.Content[i], b.Content[i]) {
				return false
			}
		}
		return true
	default:
		return slices.Equal([]string{a.Value}, []string{b.Value})
	}
}

// writeConfigAtomically resolves symlinks, atomically writes the backup to
// <target>.bak, atomically replaces <target> via temp+chmod+fsync+rename, and
// fsyncs the target directory.
func writeConfigAtomically(path string, origData, newData []byte, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o600
	}

	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve config path %s: %w", path, err)
	}

	bakPath := resolvedPath + ".bak"
	if err := writeFileAtomically(bakPath, origData, mode); err != nil {
		return fmt.Errorf("write backup %s: %w", bakPath, err)
	}

	if err := writeFileAtomically(resolvedPath, newData, mode); err != nil {
		return err
	}

	return syncDir(filepath.Dir(resolvedPath))
}

func writeFileAtomically(targetPath string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(targetPath)
	base := filepath.Base(targetPath)
	tmpFile, err := os.CreateTemp(dir, "."+base+".tmp-*")
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

	if _, err := tmpFile.Write(data); err != nil {
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
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return fmt.Errorf("rename temp file over %s: %w", targetPath, err)
	}
	cleanup = false
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open dir %s for sync: %w", dir, err)
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	if syncErr != nil {
		return fmt.Errorf("sync dir %s: %w", dir, syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close dir %s: %w", dir, closeErr)
	}
	return nil
}
