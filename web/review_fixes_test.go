package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"polynux/disgoroq/config"
	"polynux/disgoroq/database"
)

const knownGuildID = "777777777777777777"

func newKnownGuildServer(t *testing.T, mutate func(*config.Config)) (*Server, *database.Repository) {
	t.Helper()
	repo := setupTestRepo(t)
	cfg := config.DefaultConfig()
	cfg.Web = config.WebConfig{Enabled: true, Listen: "127.0.0.1:8090"}
	if mutate != nil {
		mutate(cfg)
	}
	srv := New(Deps{Repo: repo, Config: cfg}, cfg.Web)
	require.NoError(t, repo.SetLastMessage(context.Background(), knownGuildID, 1))
	return srv, repo
}

func sameOriginPost(srv *Server, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8090"+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func storedSettings(t *testing.T, repo *database.Repository, guildID string) map[string]string {
	t.Helper()
	m, err := repo.GetGuildSettingsMap(context.Background(), guildID)
	require.NoError(t, err)
	return m
}

func TestGuildRouteRejectsInvalidAndUnknownGuilds(t *testing.T) {
	srv, repo := newKnownGuildServer(t, nil)
	form := url.Values{"horoscope_channel": {"123456789012345678"}}

	for _, id := range []string{"not-a-snowflake", "..%2Fconfig", "888888888888888888"} {
		rec := sameOriginPost(srv, "/guilds/"+id+"/channels", form)
		assert.Equal(t, http.StatusNotFound, rec.Code, "id %q", id)

		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/guilds/"+id, nil)
		getRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(getRec, req)
		assert.Equal(t, http.StatusNotFound, getRec.Code, "GET id %q", id)
	}

	rec := sameOriginPost(srv, "/guilds/"+knownGuildID+"/reengage/channel/not-a-channel/reset", url.Values{})
	assert.Equal(t, http.StatusNotFound, rec.Code)

	guilds, err := repo.GetAllGuilds(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{knownGuildID}, guilds)
}

func TestGuildRouteWithoutRepoReturns503(t *testing.T) {
	cfg := config.WebConfig{Enabled: true, Listen: "127.0.0.1:8090"}
	srv := New(Deps{Config: config.DefaultConfig()}, cfg)
	rec := sameOriginPost(srv, "/guilds/"+knownGuildID+"/general", url.Values{})
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestGeneralSaveOfDefaultsWritesNothing(t *testing.T) {
	srv, repo := newKnownGuildServer(t, nil)
	rec := sameOriginPost(srv, "/guilds/"+knownGuildID+"/general", url.Values{
		"state":          {"on"},
		"threshold":      {"0.1"},
		"thresholdSexe":  {"0.05"},
		"temperature":    {"0.5"},
		"messagescount":  {"100"},
		"memory_enabled": {"on"},
		"trigger_words":  {"feun\nfeunboy"},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, map[string]string{"last_message": "1"}, storedSettings(t, repo, knownGuildID))
}

func TestGeneralSaveAcceptsValuesSlashCommandsAllow(t *testing.T) {
	srv, repo := newKnownGuildServer(t, nil)
	ctx := context.Background()
	// Values the slash commands accept (they have no bounds).
	require.NoError(t, repo.SetGuildSetting(ctx, knownGuildID, "messagescount", "500"))
	require.NoError(t, repo.SetGuildSetting(ctx, knownGuildID, "temperature", "1.2"))

	rec := sameOriginPost(srv, "/guilds/"+knownGuildID+"/general", url.Values{
		"state":          {"off"},
		"threshold":      {"0.005"},
		"thresholdSexe":  {"0.05"},
		"temperature":    {"1.2"},
		"messagescount":  {"500"},
		"memory_enabled": {"on"},
		"trigger_words":  {"feun\nfeunboy"},
	})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "General settings saved")
	assert.Equal(t, "off", repo.GetState(ctx, knownGuildID))
	assert.Equal(t, 500, repo.GetMessagesCount(ctx, knownGuildID))
	assert.InDelta(t, 0.005, repo.GetThreshold(ctx, knownGuildID), 1e-9)
}

func TestReengageChannelSaveDoesNotCreateDisabledOverride(t *testing.T) {
	srv, repo := newKnownGuildServer(t, nil)
	channelID := "333333333333333333"
	rec := sameOriginPost(srv, "/guilds/"+knownGuildID+"/reengage/channel", url.Values{
		"channel_id": {channelID},
		"chance":     {"0.25"},
	})
	require.Equal(t, http.StatusOK, rec.Code)

	stored := storedSettings(t, repo, knownGuildID)
	assert.Equal(t, "0.25", stored["reengage_chance:"+channelID])
	_, hasEnabled := stored["reengage_enabled:"+channelID]
	assert.False(t, hasEnabled, "unchecked enabled box must not write reengage_enabled:<ch>=false")
}

func TestDefaultsFollowEffectiveConfig(t *testing.T) {
	srv, _ := newKnownGuildServer(t, func(cfg *config.Config) {
		cfg.Bot.TriggerWords = []string{}
		cfg.Bot.DefaultPrompt = "base prompt"
		cfg.Voice.VoiceSystemPrompt = ""
	})
	assert.Empty(t, srv.defaultTriggerWords(), "explicit empty trigger_words must stay empty")
	assert.Equal(t, "base prompt", srv.defaultVoicePrompt(), "empty voice prompt falls back to bot.default_prompt like the orchestrator")
}

func TestCSRFGuardRejectsNullAndNonHTTPOrigins(t *testing.T) {
	srv, _ := newKnownGuildServer(t, nil)
	for _, origin := range []string{"null", "file://127.0.0.1:8090", "chrome-extension://127.0.0.1:8090"} {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8090/guilds/"+knownGuildID+"/general", strings.NewReader(""))
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		assert.Equal(t, http.StatusForbidden, rec.Code, "origin %q", origin)
	}
}
