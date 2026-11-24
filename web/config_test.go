package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/html"

	"polynux/disgoroq/config"
)

const sampleConfigYAML = `# Top-level comment that must survive edits
discord:
  # Discord bot token from env
  token: "${TEST_CFG_DISCORD_TOKEN}"

database:
  url: "libsql://secret-db-host.example.com"
  token: "literal-db-secret-token-value"
  local: true

ai:
  primary_provider: "groq"
  groq:
    api_key: "${TEST_CFG_GROQ_KEY}" # inline comment on api_key
    model: "openai/gpt-oss-20b"

memory:
  enabled: false
  summary_api_key: "literal-summary-secret-key"

votes:
  # Vote configuration comment
  enabled: true
  probability: 0.25
  good_emoji: "😂"
  bad_emoji: "🗑️"

bot:
  default_prompt: "initial prompt"
  trigger_words:
    - "feun"
    - "feunboy"
`

func newConfigTestServer(t *testing.T, yamlContent string) (*Server, *config.Runtime, string) {
	t.Helper()

	t.Setenv("TEST_CFG_DISCORD_TOKEN", "resolved-discord-secret-xyz")
	t.Setenv("TEST_CFG_GROQ_KEY", "resolved-groq-secret-abc")

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(yamlContent), 0o640))

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	runtime := config.NewRuntime(cfg)

	repo := setupTestRepo(t)
	webCfg := config.WebConfig{
		Enabled: true,
		Listen:  "127.0.0.1:8090",
	}
	srv := New(Deps{
		Repo:          repo,
		Config:        cfg,
		ConfigRuntime: runtime,
		ConfigPath:    cfgPath,
	}, webCfg)

	return srv, runtime, cfgPath
}

func postConfigSection(t *testing.T, srv *Server, section string, form url.Values, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8090/config/"+section, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestYAMLNodeEditsPreserveCommentsPlaceholdersAndInsertMissingKeys(t *testing.T) {
	out, err := patchYAMLBytes([]byte(sampleConfigYAML), []yamlPatch{
		// Update an existing scalar.
		{Path: "votes.probability", Kind: fieldKindFloat, Val: 0.5},
		// Update an existing sequence.
		{Path: "bot.trigger_words", Kind: fieldKindStringSlice, Val: []string{"alpha", "beta"}},
		// Insert a missing nested key (neither reengage nor its child exists in sampleConfigYAML).
		{Path: "reengage.default_inactivity_minutes", Kind: fieldKindInt, Val: int64(45)},
		// Insert a missing deeply nested key under an existing section.
		{Path: "ai.tools.web.max_bytes", Kind: fieldKindInt, Val: int64(1048576)},
	})
	require.NoError(t, err)
	rendered := string(out)

	// Comments and env placeholders survive.
	assert.Contains(t, rendered, "# Top-level comment that must survive edits")
	assert.Contains(t, rendered, "# Discord bot token from env")
	assert.Contains(t, rendered, "# inline comment on api_key")
	assert.Contains(t, rendered, "# Vote configuration comment")
	assert.Contains(t, rendered, "${TEST_CFG_DISCORD_TOKEN}")
	assert.Contains(t, rendered, "${TEST_CFG_GROQ_KEY}")

	// Untouched emoji and blank separator lines stay byte-identical.
	assert.Contains(t, rendered, "  good_emoji: \"😂\"")
	assert.Contains(t, rendered, "  bad_emoji: \"🗑️\"")
	assert.Contains(t, rendered, "\n\ndatabase:\n")

	// 2-space indentation is preserved and missing keys are inserted at the right place.
	assert.Contains(t, rendered, "  probability: 0.5")
	assert.Contains(t, rendered, "reengage:\n  default_inactivity_minutes: 45")
	assert.Contains(t, rendered, "  tools:\n    web:\n      max_bytes: 1048576")

	// Round-trip through config.Parse verifies everything is valid YAML and parses cleanly.
	t.Setenv("TEST_CFG_DISCORD_TOKEN", "resolved-discord-secret-xyz")
	t.Setenv("TEST_CFG_GROQ_KEY", "resolved-groq-secret-abc")
	parsed, err := config.Parse(out)
	require.NoError(t, err)
	assert.InDelta(t, 0.5, parsed.Votes.Probability, 1e-9)
	assert.Equal(t, []string{"alpha", "beta"}, parsed.Bot.TriggerWords)
	assert.Equal(t, 45, parsed.Reengage.DefaultInactivityMinutes)
	assert.Equal(t, int64(1048576), parsed.AI.Tools.Web.MaxBytes)
}

func TestPatchYAMLBytesEdgeCases(t *testing.T) {
	t.Run("empty section converted to mapping", func(t *testing.T) {
		src := []byte("discord:\n  token: \"tok\"\nreengage:\n\nlogging:\n  level: \"info\"\n")
		out, err := patchYAMLBytes(src, []yamlPatch{
			{Path: "reengage.default_chance", Kind: fieldKindFloat, Val: 0.05},
		})
		require.NoError(t, err)
		assert.Contains(t, string(out), "reengage:\n  default_chance: 0.05\n\nlogging:\n  level: \"info\"\n")
	})

	t.Run("YAML 1.1 reserved words, emoji, block scalars, and leading newlines", func(t *testing.T) {
		src := []byte("bot:\n  default_prompt: |\n    line one\n    line two\nreengage:\n  reengage_message: \"old\"\nvotes:\n  good_emoji: \"😂\"\n")
		out, err := patchYAMLBytes(src, []yamlPatch{
			{Path: "bot.default_prompt", Kind: fieldKindText, Val: "first line\nsecond line"},
			{Path: "reengage.reengage_message", Kind: fieldKindText, Val: "\n\nleading newline"},
			{Path: "votes.good_emoji", Kind: fieldKindString, Val: "on"},
		})
		require.NoError(t, err)
		rendered := string(out)
		assert.Contains(t, rendered, "default_prompt: |-\n    first line\n    second line")
		assert.Contains(t, rendered, `reengage_message: "\n\nleading newline"`)
		assert.Contains(t, rendered, `good_emoji: "on"`)

		doc, err := parseYAMLDocument(out)
		require.NoError(t, err)
		n, ok := getYAMLNode(doc, "reengage.reengage_message")
		require.True(t, ok)
		assert.Equal(t, "\n\nleading newline", n.Value)
		n2, ok := getYAMLNode(doc, "bot.default_prompt")
		require.True(t, ok)
		assert.Equal(t, "first line\nsecond line", n2.Value)
		n3, ok := getYAMLNode(doc, "votes.good_emoji")
		require.True(t, ok)
		assert.Equal(t, "!!str", n3.Tag)
		assert.Equal(t, "on", n3.Value)
	})
}

// extractSectionFormValues parses rendered HTML with the HTML5 parser (which
// performs the browser's textarea leading-newline strip) and returns the exact
// form submission values for the given /config/{section} form.
func extractSectionFormValues(t *testing.T, htmlBody, section string) url.Values {
	t.Helper()
	root, err := html.Parse(strings.NewReader(htmlBody))
	require.NoError(t, err)

	wantAction := "/config/" + section
	var formNode *html.Node
	var findForm func(*html.Node)
	findForm = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" {
			for _, a := range n.Attr {
				if a.Key == "action" && a.Val == wantAction {
					formNode = n
					return
				}
			}
		}
		for c := n.FirstChild; c != nil && formNode == nil; c = c.NextSibling {
			findForm(c)
		}
	}
	findForm(root)
	require.NotNil(t, formNode, "form for section %q not found", section)

	values := url.Values{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "input":
				var name, typ, val string
				var checked bool
				for _, a := range n.Attr {
					switch a.Key {
					case "name":
						name = a.Val
					case "type":
						typ = a.Val
					case "value":
						val = a.Val
					case "checked":
						checked = true
					}
				}
				if name != "" {
					if typ == "checkbox" {
						if checked {
							if val == "" {
								val = "on"
							}
							values.Add(name, val)
						}
					} else {
						values.Add(name, val)
					}
				}
			case "textarea":
				var name string
				for _, a := range n.Attr {
					if a.Key == "name" {
						name = a.Val
					}
				}
				if name != "" {
					var text strings.Builder
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						if c.Type == html.TextNode {
							text.WriteString(c.Data)
						}
					}
					values.Add(name, text.String())
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(formNode)
	return values
}

func TestConfigNoEditSaveInvariant(t *testing.T) {
	exampleBytes, err := os.ReadFile(filepath.Join("..", "config.example.yaml"))
	require.NoError(t, err)

	trickyFixture := `# Tricky YAML fixture
discord:
  token: "${DISCORD_TOKEN}"

database:
  local: true

ai:
  groq:
    api_key: "${GROQ_API_KEY}"
    model: "on"
    vision_model: 'yes'

memory:
  enabled: false

reengage:
  reengage_message: "\n\nLe chat est calme!"

bot:
  default_prompt: |
    first line of block
    second line of block
  trigger_words:
    - "feun"
    - "yes"
`

	cases := []struct {
		name string
		yaml string
	}{
		{name: "config.example.yaml", yaml: string(exampleBytes)},
		{name: "tricky fixture", yaml: trickyFixture},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VAR_NAME", "dummy") // config.example.yaml mentions ${VAR_NAME} in a header comment
			t.Setenv("DISCORD_TOKEN", "test-discord-token")
			t.Setenv("DB_URL", "libsql://example.invalid")
			t.Setenv("DB_TOKEN", "test-db-token")
			t.Setenv("GROQ_API_KEY", "test-groq-key")

			tmpDir := t.TempDir()
			cfgPath := filepath.Join(tmpDir, "config.yaml")
			require.NoError(t, os.WriteFile(cfgPath, []byte(tc.yaml), 0o640))

			cfg, err := config.Load(cfgPath)
			require.NoError(t, err)
			runtime := config.NewRuntime(cfg)

			srv := New(Deps{
				Repo:          setupTestRepo(t),
				Config:        cfg,
				ConfigRuntime: runtime,
				ConfigPath:    cfgPath,
			}, config.WebConfig{Enabled: true, Listen: "127.0.0.1:8090"})

			getReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/config", nil)
			getRec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(getRec, getReq)
			require.Equal(t, http.StatusOK, getRec.Code)
			pageHTML := getRec.Body.String()

			for _, sec := range buildConfigSchema() {
				form := extractSectionFormValues(t, pageHTML, sec.Name)
				postRec := postConfigSection(t, srv, sec.Name, form, true)
				require.Equal(t, http.StatusOK, postRec.Code, "section %s", sec.Name)
				assert.Contains(t, postRec.Body.String(), "No changes to save.", "section %s unexpectedly detected changes", sec.Name)

				afterBytes, err := os.ReadFile(cfgPath)
				require.NoError(t, err)
				assert.Equal(t, []byte(tc.yaml), afterBytes, "section %s modified file on no-edit save", sec.Name)

				_, bakErr := os.Stat(cfgPath + ".bak")
				assert.True(t, os.IsNotExist(bakErr), "section %s wrote .bak on no-edit save", sec.Name)
			}
		})
	}
}

func TestConfigSingleFieldEditChangesExactlyOneLine(t *testing.T) {
	exampleBytes, err := os.ReadFile(filepath.Join("..", "config.example.yaml"))
	require.NoError(t, err)

	t.Setenv("VAR_NAME", "dummy") // config.example.yaml mentions ${VAR_NAME} in a header comment
	t.Setenv("DISCORD_TOKEN", "test-discord-token")
	t.Setenv("DB_URL", "libsql://example.invalid")
	t.Setenv("DB_TOKEN", "test-db-token")
	t.Setenv("GROQ_API_KEY", "test-groq-key")

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, exampleBytes, 0o640))

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	srv := New(Deps{
		Repo:          setupTestRepo(t),
		Config:        cfg,
		ConfigRuntime: config.NewRuntime(cfg),
		ConfigPath:    cfgPath,
	}, config.WebConfig{Enabled: true, Listen: "127.0.0.1:8090"})

	getReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/config", nil)
	getRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)

	form := extractSectionFormValues(t, getRec.Body.String(), "votes")
	form.Set("field:votes.probability", "0.5")

	postRec := postConfigSection(t, srv, "votes", form, true)
	require.Equal(t, http.StatusOK, postRec.Code)
	assert.Contains(t, postRec.Body.String(), "Saved Votes settings (1 updated).")

	afterBytes, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	beforeLines := strings.Split(string(exampleBytes), "\n")
	afterLines := strings.Split(string(afterBytes), "\n")
	require.Equal(t, len(beforeLines), len(afterLines))

	diffCount := 0
	for i := range beforeLines {
		if beforeLines[i] != afterLines[i] {
			diffCount++
			assert.Equal(t, "  probability: 0.25", beforeLines[i])
			assert.Equal(t, "  probability: 0.5", afterLines[i])
		}
	}
	assert.Equal(t, 1, diffCount, "editing one field must change exactly one line")
}

func TestConfigPageMasksSecretsAndNeverExposesResolvedValues(t *testing.T) {
	srv, _, _ := newConfigTestServer(t, sampleConfigYAML)

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/config", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	// Resolved env secret values must NEVER appear.
	assert.NotContains(t, body, "resolved-discord-secret-xyz")
	assert.NotContains(t, body, "resolved-groq-secret-abc")

	// Literal secret values in YAML must NEVER appear.
	assert.NotContains(t, body, "libsql://secret-db-host.example.com")
	assert.NotContains(t, body, "literal-db-secret-token-value")
	assert.NotContains(t, body, "literal-summary-secret-key")

	// Pure ${VAR} placeholders are displayed as placeholder text (not resolved).
	assert.Contains(t, body, "${TEST_CFG_DISCORD_TOKEN}")
	assert.Contains(t, body, "${TEST_CFG_GROQ_KEY}")

	// Literal secrets are masked.
	assert.Contains(t, body, maskedSecretDisplay)

	// Live and Restart required badges are both present.
	assert.Contains(t, body, "badge-live")
	assert.Contains(t, body, "badge-restart")
}

func TestConfigSaveValidationFailureLeavesFileUntouched(t *testing.T) {
	srv, runtime, cfgPath := newConfigTestServer(t, sampleConfigYAML)
	origBytes, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	form := url.Values{
		"config_hash":             {computeFileHash(origBytes)},
		"field:votes.enabled":     {"on"},
		"field:votes.probability": {"2.5"}, // invalid: > 1.0
		"field:votes.good_emoji":  {"😂"},
		"field:votes.bad_emoji":   {"🗑️"},
	}

	rec := postConfigSection(t, srv, "votes", form, false)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "votes.probability")

	// File on disk must be byte-for-byte unchanged, no .bak created, runtime unchanged.
	afterBytes, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, origBytes, afterBytes)
	_, bakErr := os.Stat(cfgPath + ".bak")
	assert.True(t, os.IsNotExist(bakErr), "backup file should not be written on validation failure")
	assert.InDelta(t, 0.25, runtime.Current().Votes.Probability, 1e-9)
}

func TestConfigSaveDetectsHashConflict(t *testing.T) {
	srv, _, cfgPath := newConfigTestServer(t, sampleConfigYAML)
	origBytes, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	staleHash := computeFileHash(origBytes)

	// Simulate an external edit on disk.
	modifiedOnDisk := append(origBytes, []byte("\n# external edit\n")...)
	require.NoError(t, os.WriteFile(cfgPath, modifiedOnDisk, 0o640))

	form := url.Values{
		"config_hash":             {staleHash},
		"field:votes.enabled":     {"on"},
		"field:votes.probability": {"0.4"},
		"field:votes.good_emoji":  {"😂"},
		"field:votes.bad_emoji":   {"🗑️"},
	}

	rec := postConfigSection(t, srv, "votes", form, false)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "config.yaml changed on disk, reload")

	// External edit must remain intact on disk.
	afterBytes, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, modifiedOnDisk, afterBytes)

	// HTMX conflict must not OOB-refresh other sections' hashes, so another section
	// with its original stale hash is still rejected.
	htmxRec := postConfigSection(t, srv, "votes", form, true)
	require.Equal(t, http.StatusOK, htmxRec.Code)
	assert.Contains(t, htmxRec.Body.String(), "config.yaml changed on disk, reload")
	assert.NotContains(t, htmxRec.Body.String(), "hx-swap-oob")

	otherForm := url.Values{
		"config_hash":              {staleHash},
		"field:bot.default_prompt": {"stale overwrite attempt"},
		"field:bot.trigger_words":  {"feun\nfeunboy"},
	}
	otherRec := postConfigSection(t, srv, "bot", otherForm, false)
	assert.Equal(t, http.StatusConflict, otherRec.Code)
	assert.Contains(t, otherRec.Body.String(), "config.yaml changed on disk, reload")

	afterBytes2, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, modifiedOnDisk, afterBytes2)
}

func TestConfigNonHTMXSaveDoesNotEmitDuplicateHashIDs(t *testing.T) {
	srv, _, cfgPath := newConfigTestServer(t, sampleConfigYAML)
	origBytes, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	form := url.Values{
		"config_hash":             {computeFileHash(origBytes)},
		"field:votes.enabled":     {"on"},
		"field:votes.probability": {"0.4"},
		"field:votes.good_emoji":  {"😂"},
		"field:votes.bad_emoji":   {"🗑️"},
	}
	rec := postConfigSection(t, srv, "votes", form, false)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.NotContains(t, body, "hx-swap-oob")
	assert.Equal(t, 1, strings.Count(body, `id="config-hash-bot"`))
}

func TestConfigSaveWritesBackupPreservesModeAndHotAppliesRuntime(t *testing.T) {
	srv, runtime, cfgPath := newConfigTestServer(t, sampleConfigYAML)
	origBytes, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	form := url.Values{
		"config_hash":              {computeFileHash(origBytes)},
		"field:bot.default_prompt": {"updated live prompt for {{.BotNick}}"},
		"field:bot.trigger_words":  {"Gamma\nDelta"},
	}

	rec := postConfigSection(t, srv, "bot", form, true)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "Saved Bot settings")
	assert.NotContains(t, body, "Restart required</strong>")

	// .bak file exists with the exact original content.
	bakBytes, err := os.ReadFile(cfgPath + ".bak")
	require.NoError(t, err)
	assert.Equal(t, origBytes, bakBytes)

	// Original file mode (0640) is preserved.
	info, err := os.Stat(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())

	// Comments and env placeholders still exist in the saved file.
	newBytes, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Contains(t, string(newBytes), "# Top-level comment that must survive edits")
	assert.Contains(t, string(newBytes), "${TEST_CFG_DISCORD_TOKEN}")

	// Hot-applied values are immediately visible through Runtime.Current().
	assert.Equal(t, "updated live prompt for {{.BotNick}}", runtime.Current().Bot.DefaultPrompt)
	assert.Equal(t, []string{"gamma", "delta"}, runtime.Current().Bot.TriggerWords)

	// Phase 1 guild page reads defaults from the runtime and reflects the live edit.
	// The guild must be known to the route guard (no cache here), so seed a bookkeeping row.
	require.NoError(t, srv.deps.Repo.SetLastMessage(context.Background(), "123456789012345678", 1))
	guildReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/guilds/123456789012345678", nil)
	guildRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(guildRec, guildReq)
	require.Equal(t, http.StatusOK, guildRec.Code)
	assert.Contains(t, guildRec.Body.String(), "updated live prompt for {{.BotNick}}")
	assert.Contains(t, guildRec.Body.String(), "gamma, delta")
}

func TestConfigSaveRestartRequiredBannerListsTouchedFields(t *testing.T) {
	srv, _, cfgPath := newConfigTestServer(t, sampleConfigYAML)
	origBytes, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	form := url.Values{
		"config_hash":                               {computeFileHash(origBytes)},
		"field:reengage.check_interval_seconds":     {"600"}, // restart-required
		"field:reengage.default_inactivity_minutes": {"45"},  // live
		"field:reengage.default_chance":             {"0.05"},
		"field:reengage.reengage_message":           {config.DefaultConfig().Reengage.ReengageMessage},
	}

	rec := postConfigSection(t, srv, "reengage", form, true)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "Restart required")
	assert.Contains(t, body, "reengage.check_interval_seconds")
	assert.NotContains(t, body, "reengage.check_interval_seconds, reengage.default_inactivity_minutes")
}

func TestWriteConfigAtomicallyFollowsSymlinkAndUpdatesBackupMode(t *testing.T) {
	targetDir := t.TempDir()
	linkDir := t.TempDir()

	targetPath := filepath.Join(targetDir, "real-config.yaml")
	linkPath := filepath.Join(linkDir, "config.yaml")
	bakPath := targetPath + ".bak"

	origData := []byte("votes:\n  probability: 0.25\n")
	newData := []byte("votes:\n  probability: 0.5\n")

	require.NoError(t, os.WriteFile(targetPath, origData, 0o600))
	// Pre-create .bak with a wider mode (0644) to verify it gets atomically replaced with 0600.
	require.NoError(t, os.WriteFile(bakPath, []byte("stale backup"), 0o644))
	require.NoError(t, os.Symlink(targetPath, linkPath))

	require.NoError(t, writeConfigAtomically(linkPath, origData, newData, 0o600))

	// Symlink must still be a symlink pointing to targetPath.
	lstatInfo, err := os.Lstat(linkPath)
	require.NoError(t, err)
	assert.NotZero(t, lstatInfo.Mode()&os.ModeSymlink, "config.yaml must remain a symlink")

	// Target file is updated with newData and keeps mode 0600.
	gotTarget, err := os.ReadFile(targetPath)
	require.NoError(t, err)
	assert.Equal(t, newData, gotTarget)

	targetInfo, err := os.Stat(targetPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), targetInfo.Mode().Perm())

	// Backup file in targetDir has origData and now has mode 0600 (not 0644).
	gotBak, err := os.ReadFile(bakPath)
	require.NoError(t, err)
	assert.Equal(t, origData, gotBak)

	bakInfo, err := os.Stat(bakPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), bakInfo.Mode().Perm())

	// No stray .bak created in linkDir.
	_, strayErr := os.Stat(linkPath + ".bak")
	assert.True(t, os.IsNotExist(strayErr))
}

func TestConfigSaveIgnoresAbsentFieldsAndDistinguishesUncheckedCheckbox(t *testing.T) {
	srv, runtime, cfgPath := newConfigTestServer(t, sampleConfigYAML)
	origBytes, err := os.ReadFile(cfgPath)
	require.NoError(t, err)

	// 1. Omitting field:bot.trigger_words must leave existing trigger words intact.
	botForm := url.Values{
		"config_hash":              {computeFileHash(origBytes)},
		"field:bot.default_prompt": {"updated prompt only"},
	}
	rec := postConfigSection(t, srv, "bot", botForm, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "updated prompt only", runtime.Current().Bot.DefaultPrompt)
	assert.Equal(t, []string{"feun", "feunboy"}, runtime.Current().Bot.TriggerWords)

	// 2. Omitting field:votes.enabled must leave votes.enabled = true.
	curBytes, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	votesOmitted := url.Values{
		"config_hash":             {computeFileHash(curBytes)},
		"field:votes.probability": {"0.5"},
	}
	rec = postConfigSection(t, srv, "votes", votesOmitted, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, runtime.Current().Votes.Enabled)
	assert.InDelta(t, 0.5, runtime.Current().Votes.Probability, 1e-9)

	// 3. Submitting hidden sentinel field:votes.enabled=false (unchecked checkbox) sets it to false.
	curBytes, err = os.ReadFile(cfgPath)
	require.NoError(t, err)
	votesUnchecked := url.Values{
		"config_hash":         {computeFileHash(curBytes)},
		"field:votes.enabled": {"false"},
	}
	rec = postConfigSection(t, srv, "votes", votesUnchecked, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, runtime.Current().Votes.Enabled)
}
