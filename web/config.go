package web

import (
	"fmt"
	"math"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"polynux/disgoroq/config"
	"polynux/disgoroq/logger"
)

type configFieldView struct {
	Path           string
	InputID        string
	InputName      string
	Key            string
	Kind           string
	Value          string
	BoolValue      bool
	DefaultDisplay string
	IsDefault      bool
	HotApply       bool
	ReadOnly       bool
	ReadOnlyReason string
}

type configFieldGroup struct {
	Name   string
	Fields []configFieldView
}

type configSectionView struct {
	Name              string
	Label             string
	FileHash          string
	Success           string
	Error             string
	RestartRequired   []string
	Groups            []configFieldGroup
	HasEditableFields bool
	OtherSectionNames []string
}

type configPageData struct {
	Title      string
	ActiveNav  string
	ConfigPath string
	Error      string
	Sections   []configSectionView
}

func (s *Server) configPath() string {
	if strings.TrimSpace(s.deps.ConfigPath) != "" {
		return s.deps.ConfigPath
	}
	return config.DefaultConfigPath
}

func (s *Server) handleConfigPage(w http.ResponseWriter, r *http.Request) {
	data, err := s.buildConfigPageData()
	if err != nil {
		logger.Error("Failed to load config.yaml for web admin",
			zap.String("path", s.configPath()),
			zap.Error(err))
		s.renderPage(w, "config", http.StatusOK, configPageData{
			Title:      "Config",
			ActiveNav:  "config",
			ConfigPath: s.configPath(),
			Error:      "Failed to read configuration file: " + err.Error(),
		})
		return
	}

	s.renderPage(w, "config", http.StatusOK, data)
}

func (s *Server) handleConfigSectionSave(w http.ResponseWriter, r *http.Request) {
	sectionName := strings.TrimSpace(r.PathValue("section"))
	schema := buildConfigSchema()
	secSpec, ok := findSectionSpec(schema, sectionName)
	if !ok {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}

	s.configSaveMu.Lock()
	defer s.configSaveMu.Unlock()

	cfgPath := s.configPath()
	info, statErr := os.Stat(cfgPath)
	if statErr != nil {
		logger.Error("Failed to stat config file before save", zap.String("path", cfgPath), zap.Error(statErr))
		http.Error(w, "Failed to read config file", http.StatusInternalServerError)
		return
	}

	origData, err := os.ReadFile(cfgPath)
	if err != nil {
		logger.Error("Failed to read config file before save", zap.String("path", cfgPath), zap.Error(err))
		http.Error(w, "Failed to read config file", http.StatusInternalServerError)
		return
	}

	currentHash := computeFileHash(origData)
	doc, err := parseYAMLDocument(origData)
	if err != nil {
		logger.Error("Failed to parse config.yaml before save", zap.String("path", cfgPath), zap.Error(err))
		http.Error(w, "Failed to parse config file", http.StatusInternalServerError)
		return
	}

	otherNames := otherSectionNames(schema, sectionName)

	// Optimistic concurrency check.
	submittedHash := strings.TrimSpace(r.FormValue("config_hash"))
	if submittedHash != currentHash {
		secView := buildSectionView(secSpec, doc, currentHash)
		secView.Error = "config.yaml changed on disk, reload"
		secView.OtherSectionNames = otherNames
		s.renderConfigSectionResponse(w, r, schema, doc, currentHash, secView, http.StatusConflict)
		return
	}

	// Parse submitted values and apply changes to the YAML node tree.
	var (
		parseErrs       []string
		touchedPaths    []string
		restartRequired []string
		submittedRaw    = make(map[string]string, len(secSpec.Fields))
		submittedBool   = make(map[string]bool, len(secSpec.Fields))
	)

	for _, field := range secSpec.Fields {
		node, found := getYAMLNode(doc, field.Path)
		if field.Secret || (found && nodeHasEnvPlaceholder(node)) {
			// Read-only fields are never overwritten by form submissions.
			continue
		}

		inputName := "field:" + field.Path
		if field.Kind == fieldKindBool {
			bVal := r.FormValue(inputName) == "on" || r.FormValue(inputName) == "true"
			submittedBool[field.Path] = bVal
			if !fieldValueEqual(field, node, found, bVal) {
				if err := setYAMLValue(doc, field.Path, field.Kind, bVal); err != nil {
					parseErrs = append(parseErrs, fmt.Sprintf("%s: %v", field.Path, err))
					continue
				}
				touchedPaths = append(touchedPaths, field.Path)
				if !field.HotApply {
					restartRequired = append(restartRequired, field.Path)
				}
			}
			continue
		}

		raw := r.FormValue(inputName)
		submittedRaw[field.Path] = raw

		parsedVal, err := parseFormFieldValue(field, raw)
		if err != nil {
			parseErrs = append(parseErrs, fmt.Sprintf("%s: %v", field.Path, err))
			continue
		}

		if !fieldValueEqual(field, node, found, parsedVal) {
			if err := setYAMLValue(doc, field.Path, field.Kind, parsedVal); err != nil {
				parseErrs = append(parseErrs, fmt.Sprintf("%s: %v", field.Path, err))
				continue
			}
			touchedPaths = append(touchedPaths, field.Path)
			if !field.HotApply {
				restartRequired = append(restartRequired, field.Path)
			}
		}
	}

	if len(parseErrs) > 0 {
		// Re-parse original doc so view reflects disk state with user's attempted inputs preserved.
		origDoc, _ := parseYAMLDocument(origData)
		secView := buildSectionViewWithOverrides(secSpec, origDoc, currentHash, submittedRaw, submittedBool)
		secView.Error = strings.Join(parseErrs, "; ")
		s.renderConfigSectionResponse(w, r, schema, origDoc, currentHash, secView, http.StatusUnprocessableEntity)
		return
	}

	newData, err := marshalYAMLDocument(doc)
	if err != nil {
		origDoc, _ := parseYAMLDocument(origData)
		secView := buildSectionViewWithOverrides(secSpec, origDoc, currentHash, submittedRaw, submittedBool)
		secView.Error = "Failed to serialize YAML: " + err.Error()
		s.renderConfigSectionResponse(w, r, schema, origDoc, currentHash, secView, http.StatusUnprocessableEntity)
		return
	}

	// Validate using the exact same pipeline as config.Load.
	parsedCfg, err := config.Parse(newData)
	if err != nil {
		origDoc, _ := parseYAMLDocument(origData)
		secView := buildSectionViewWithOverrides(secSpec, origDoc, currentHash, submittedRaw, submittedBool)
		secView.Error = err.Error()
		s.renderConfigSectionResponse(w, r, schema, origDoc, currentHash, secView, http.StatusUnprocessableEntity)
		return
	}

	if len(touchedPaths) == 0 {
		secView := buildSectionView(secSpec, doc, currentHash)
		secView.Success = "No changes to save."
		s.renderConfigSectionResponse(w, r, schema, doc, currentHash, secView, http.StatusOK)
		return
	}

	if err := writeConfigAtomically(cfgPath, origData, newData, info.Mode().Perm()); err != nil {
		logger.Error("Failed to write config.yaml", zap.String("path", cfgPath), zap.Error(err))
		origDoc, _ := parseYAMLDocument(origData)
		secView := buildSectionViewWithOverrides(secSpec, origDoc, currentHash, submittedRaw, submittedBool)
		secView.Error = "Failed to save config.yaml: " + err.Error()
		s.renderConfigSectionResponse(w, r, schema, origDoc, currentHash, secView, http.StatusInternalServerError)
		return
	}

	if s.deps.ConfigRuntime != nil {
		s.deps.ConfigRuntime.Store(parsedCfg)
	}

	logger.Info("Global config section saved via web admin",
		zap.String("section", secSpec.Name),
		zap.Strings("modified_fields", touchedPaths),
		zap.Strings("restart_required", restartRequired))

	newHash := computeFileHash(newData)
	reloadedDoc, err := parseYAMLDocument(newData)
	if err != nil {
		reloadedDoc = doc
	}
	secView := buildSectionView(secSpec, reloadedDoc, newHash)
	secView.Success = fmt.Sprintf("Saved %s settings (%d updated).", secSpec.Label, len(touchedPaths))
	secView.RestartRequired = restartRequired
	secView.OtherSectionNames = otherNames

	s.renderConfigSectionResponse(w, r, schema, reloadedDoc, newHash, secView, http.StatusOK)
}

func (s *Server) renderConfigSectionResponse(
	w http.ResponseWriter,
	r *http.Request,
	schema []configSectionSpec,
	doc *yaml.Node,
	fileHash string,
	secView configSectionView,
	nonHTMXStatus int,
) {
	if isHTMXRequest(r) {
		s.renderFragment(w, "config_section", http.StatusOK, secView)
		return
	}

	sections := make([]configSectionView, 0, len(schema))
	for _, spec := range schema {
		if spec.Name == secView.Name {
			sections = append(sections, secView)
		} else {
			sections = append(sections, buildSectionView(spec, doc, fileHash))
		}
	}
	s.renderPage(w, "config", nonHTMXStatus, configPageData{
		Title:      "Config",
		ActiveNav:  "config",
		ConfigPath: s.configPath(),
		Sections:   sections,
	})
}

func (s *Server) buildConfigPageData() (configPageData, error) {
	cfgPath := s.configPath()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return configPageData{}, err
	}
	doc, err := parseYAMLDocument(data)
	if err != nil {
		return configPageData{}, err
	}
	fileHash := computeFileHash(data)
	schema := buildConfigSchema()
	sections := make([]configSectionView, 0, len(schema))
	for _, spec := range schema {
		sections = append(sections, buildSectionView(spec, doc, fileHash))
	}
	return configPageData{
		Title:      "Config",
		ActiveNav:  "config",
		ConfigPath: cfgPath,
		Sections:   sections,
	}, nil
}

func buildSectionView(spec configSectionSpec, doc *yaml.Node, fileHash string) configSectionView {
	return buildSectionViewWithOverrides(spec, doc, fileHash, nil, nil)
}

func buildSectionViewWithOverrides(
	spec configSectionSpec,
	doc *yaml.Node,
	fileHash string,
	rawOverrides map[string]string,
	boolOverrides map[string]bool,
) configSectionView {
	sec := configSectionView{
		Name:     spec.Name,
		Label:    spec.Label,
		FileHash: fileHash,
	}

	groupIndex := make(map[string]int)
	for _, f := range spec.Fields {
		fv := buildConfigFieldView(f, doc)
		if !fv.ReadOnly {
			sec.HasEditableFields = true
			if f.Kind == fieldKindBool {
				if bv, ok := boolOverrides[f.Path]; ok {
					fv.BoolValue = bv
				}
			} else if rv, ok := rawOverrides[f.Path]; ok {
				fv.Value = rv
			}
		}

		idx, exists := groupIndex[f.Subgroup]
		if !exists {
			idx = len(sec.Groups)
			groupIndex[f.Subgroup] = idx
			sec.Groups = append(sec.Groups, configFieldGroup{Name: f.Subgroup})
		}
		sec.Groups[idx].Fields = append(sec.Groups[idx].Fields, fv)
	}

	return sec
}

func buildConfigFieldView(spec configFieldSpec, doc *yaml.Node) configFieldView {
	fv := configFieldView{
		Path:      spec.Path,
		InputID:   "cfg-" + strings.ReplaceAll(spec.Path, ".", "-"),
		InputName: "field:" + spec.Path,
		Key:       spec.Key,
		Kind:      string(spec.Kind),
		HotApply:  spec.HotApply,
	}
	if !spec.Secret {
		fv.DefaultDisplay = formatDefaultForDisplay(spec)
	}

	node, found := getYAMLNode(doc, spec.Path)
	hasPlaceholder := found && nodeHasEnvPlaceholder(node)

	if spec.Secret {
		fv.ReadOnly = true
		if !found {
			fv.IsDefault = true
			fv.ReadOnlyReason = "Secret (not set)"
			return fv
		}
		if node.Kind == yaml.ScalarNode && pureEnvPlaceholderRe.MatchString(strings.TrimSpace(node.Value)) {
			fv.Value = strings.TrimSpace(node.Value)
			fv.ReadOnlyReason = "Env placeholder"
			return fv
		}
		fv.Value = maskedSecretDisplay
		fv.ReadOnlyReason = "Secret (masked)"
		return fv
	}

	if hasPlaceholder {
		fv.ReadOnly = true
		fv.ReadOnlyReason = "Env placeholder"
		if node.Kind == yaml.ScalarNode {
			fv.Value = node.Value
		} else if node.Kind == yaml.SequenceNode {
			parts := make([]string, 0, len(node.Content))
			for _, item := range node.Content {
				parts = append(parts, item.Value)
			}
			fv.Value = strings.Join(parts, "\n")
		}
		return fv
	}

	if !found {
		fv.IsDefault = true
		switch spec.Kind {
		case fieldKindBool:
			if b, ok := spec.Default.(bool); ok {
				fv.BoolValue = b
			}
		default:
			fv.Value = formatDefaultValue(spec)
		}
		return fv
	}

	switch spec.Kind {
	case fieldKindString, fieldKindText:
		if node.Tag == "!!null" {
			fv.Value = ""
		} else {
			fv.Value = node.Value
		}
	case fieldKindBool:
		var b bool
		if err := node.Decode(&b); err == nil {
			fv.BoolValue = b
		}
	case fieldKindInt:
		var n int64
		if err := node.Decode(&n); err == nil {
			fv.Value = strconv.FormatInt(n, 10)
		} else {
			fv.Value = node.Value
		}
	case fieldKindFloat:
		var f float64
		if err := node.Decode(&f); err == nil {
			fv.Value = strconv.FormatFloat(f, 'f', -1, 64)
		} else {
			fv.Value = node.Value
		}
	case fieldKindStringSlice:
		var items []string
		if err := node.Decode(&items); err == nil {
			fv.Value = strings.Join(items, "\n")
		}
	}

	return fv
}

func formatDefaultValue(spec configFieldSpec) string {
	switch spec.Kind {
	case fieldKindString, fieldKindText:
		if s, ok := spec.Default.(string); ok {
			return s
		}
	case fieldKindBool:
		if b, ok := spec.Default.(bool); ok {
			return strconv.FormatBool(b)
		}
	case fieldKindInt:
		if n, ok := spec.Default.(int64); ok {
			return strconv.FormatInt(n, 10)
		}
	case fieldKindFloat:
		if f, ok := spec.Default.(float64); ok {
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
	case fieldKindStringSlice:
		if items, ok := spec.Default.([]string); ok {
			return strings.Join(items, "\n")
		}
	}
	return ""
}

func formatDefaultForDisplay(spec configFieldSpec) string {
	switch spec.Kind {
	case fieldKindStringSlice:
		if items, ok := spec.Default.([]string); ok {
			if len(items) == 0 {
				return "(empty)"
			}
			return strings.Join(items, ", ")
		}
		return "(empty)"
	case fieldKindText:
		return "(built-in default)"
	case fieldKindString:
		s := formatDefaultValue(spec)
		if s == "" {
			return "(empty)"
		}
		return s
	default:
		return formatDefaultValue(spec)
	}
}

func parseFormFieldValue(spec configFieldSpec, raw string) (any, error) {
	switch spec.Kind {
	case fieldKindString, fieldKindText:
		// Normalize CRLF from HTML textareas to LF.
		return strings.ReplaceAll(raw, "\r\n", "\n"), nil
	case fieldKindInt:
		trimmed := strings.TrimSpace(raw)
		n, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("must be an integer")
		}
		return n, nil
	case fieldKindFloat:
		trimmed := strings.TrimSpace(raw)
		f, err := strconv.ParseFloat(trimmed, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("must be a number")
		}
		return f, nil
	case fieldKindStringSlice:
		normalized := strings.ReplaceAll(raw, "\r\n", "\n")
		lines := strings.Split(normalized, "\n")
		out := make([]string, 0, len(lines))
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out, nil
	default:
		return raw, nil
	}
}

func fieldValueEqual(spec configFieldSpec, node *yaml.Node, found bool, newVal any) bool {
	var curVal any
	if !found {
		curVal = spec.Default
	} else {
		switch spec.Kind {
		case fieldKindString, fieldKindText:
			if node.Tag == "!!null" {
				curVal = ""
			} else {
				curVal = node.Value
			}
		case fieldKindBool:
			var b bool
			if err := node.Decode(&b); err != nil {
				return false
			}
			curVal = b
		case fieldKindInt:
			var n int64
			if err := node.Decode(&n); err != nil {
				return false
			}
			curVal = n
		case fieldKindFloat:
			var f float64
			if err := node.Decode(&f); err != nil {
				return false
			}
			curVal = f
		case fieldKindStringSlice:
			var items []string
			if err := node.Decode(&items); err != nil {
				return false
			}
			curVal = items
		}
	}

	switch spec.Kind {
	case fieldKindString, fieldKindText:
		a, _ := curVal.(string)
		b, _ := newVal.(string)
		return a == b
	case fieldKindBool:
		a, _ := curVal.(bool)
		b, _ := newVal.(bool)
		return a == b
	case fieldKindInt:
		a, _ := curVal.(int64)
		b, _ := newVal.(int64)
		return a == b
	case fieldKindFloat:
		a, _ := curVal.(float64)
		b, _ := newVal.(float64)
		return a == b
	case fieldKindStringSlice:
		a, _ := curVal.([]string)
		b, _ := newVal.([]string)
		return slices.Equal(a, b)
	default:
		return false
	}
}

func findSectionSpec(schema []configSectionSpec, name string) (configSectionSpec, bool) {
	for _, sec := range schema {
		if sec.Name == name {
			return sec, true
		}
	}
	return configSectionSpec{}, false
}

func otherSectionNames(schema []configSectionSpec, exclude string) []string {
	names := make([]string, 0, len(schema))
	for _, sec := range schema {
		if sec.Name != exclude {
			names = append(names, sec.Name)
		}
	}
	return names
}
