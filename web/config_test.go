package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	doc, err := parseYAMLDocument([]byte(sampleConfigYAML))
	require.NoError(t, err)

	// Update an existing scalar.
	require.NoError(t, setYAMLValue(doc, "votes.probability", fieldKindFloat, 0.5))
	// Update an existing sequence.
	require.NoError(t, setYAMLValue(doc, "bot.trigger_words", fieldKindStringSlice, []string{"alpha", "beta"}))
	// Insert a missing nested key (neither reengage nor its child exists in sampleConfigYAML).
	require.NoError(t, setYAMLValue(doc, "reengage.default_inactivity_minutes", fieldKindInt, int64(45)))
	// Insert a missing deeply nested key under an existing section.
	require.NoError(t, setYAMLValue(doc, "ai.tools.web.max_bytes", fieldKindInt, int64(1048576)))

	out, err := marshalYAMLDocument(doc)
	require.NoError(t, err)
	rendered := string(out)

	// Comments and env placeholders survive.
	assert.Contains(t, rendered, "# Top-level comment that must survive edits")
	assert.Contains(t, rendered, "# Discord bot token from env")
	assert.Contains(t, rendered, "# inline comment on api_key")
	assert.Contains(t, rendered, "# Vote configuration comment")
	assert.Contains(t, rendered, "${TEST_CFG_DISCORD_TOKEN}")
	assert.Contains(t, rendered, "${TEST_CFG_GROQ_KEY}")

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
