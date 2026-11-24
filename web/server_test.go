package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"polynux/disgoroq/config"
	"polynux/disgoroq/database"
	"polynux/disgoroq/utils"
)

func setupTestRepo(t *testing.T) *database.Repository {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	sqlDB, err := utils.OpenLocalDB(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	schemaBytes, err := os.ReadFile(filepath.Join("..", "schema.sql"))
	require.NoError(t, err)

	for _, stmt := range strings.Split(string(schemaBytes), ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" || strings.Contains(stmt, "libsql_vector_idx") {
			continue
		}
		_, err := sqlDB.Exec(stmt)
		require.NoError(t, err)
	}

	return database.NewRepositoryWithDB(sqlDB)
}

func newTestServer(t *testing.T, client *bot.Client, webCfg config.WebConfig) (*Server, *database.Repository) {
	t.Helper()
	repo := setupTestRepo(t)
	cfg := config.DefaultConfig()
	cfg.Web = webCfg
	srv := New(Deps{
		Client: client,
		Repo:   repo,
		Config: cfg,
	}, webCfg)
	return srv, repo
}

func TestHostAllowlist(t *testing.T) {
	srv, _ := newTestServer(t, nil, config.WebConfig{
		Enabled:      true,
		Listen:       "10.0.0.5:8090",
		AllowedHosts: []string{"bot.tailnet.ts.net", "Admin.Local:9999"},
	})

	tests := []struct {
		name       string
		host       string
		wantStatus int
	}{
		{name: "localhost", host: "localhost:8090", wantStatus: http.StatusOK},
		{name: "ipv4 loopback", host: "127.0.0.1:8090", wantStatus: http.StatusOK},
		{name: "ipv6 loopback", host: "[::1]:8090", wantStatus: http.StatusOK},
		{name: "listen host", host: "10.0.0.5:8090", wantStatus: http.StatusOK},
		{name: "allowed host exact", host: "bot.tailnet.ts.net:8090", wantStatus: http.StatusOK},
		{name: "allowed host case-insensitive", host: "admin.local:8090", wantStatus: http.StatusOK},
		{name: "rejected evil host", host: "evil.example.com", wantStatus: http.StatusForbidden},
		{name: "rejected evil host with port", host: "evil.example.com:8090", wantStatus: http.StatusForbidden},
		{name: "rejected lookalike host", host: "localhost.evil.com:8090", wantStatus: http.StatusForbidden},
		{name: "rejected empty host", host: "", wantStatus: http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/guilds", nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			assert.Equal(t, tc.wantStatus, rec.Code)
		})
	}
}

func TestCSRFGuard(t *testing.T) {
	srv, _ := newTestServer(t, nil, config.WebConfig{
		Enabled: true,
		Listen:  "127.0.0.1:8090",
	})

	const targetURL = "http://127.0.0.1:8090/guilds/123456789012345678/prompts"

	t.Run("safe GET allowed even with cross-site header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/guilds", nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	tests := []struct {
		name         string
		method       string
		secFetchSite string
		origin       string
		referer      string
		wantAllowed  bool
	}{
		{name: "POST same-origin accepted", method: http.MethodPost, secFetchSite: "same-origin", wantAllowed: true},
		{name: "POST none accepted", method: http.MethodPost, secFetchSite: "none", wantAllowed: true},
		{name: "POST cross-site rejected", method: http.MethodPost, secFetchSite: "cross-site", wantAllowed: false},
		{name: "POST same-site rejected", method: http.MethodPost, secFetchSite: "same-site", wantAllowed: false},
		{name: "DELETE cross-site rejected", method: http.MethodDelete, secFetchSite: "cross-site", wantAllowed: false},
		{name: "POST matching Origin accepted", method: http.MethodPost, origin: "http://127.0.0.1:8090", wantAllowed: true},
		{name: "POST mismatched Origin rejected", method: http.MethodPost, origin: "http://evil.example.com", wantAllowed: false},
		{name: "POST matching Referer accepted", method: http.MethodPost, referer: "http://127.0.0.1:8090/guilds/123", wantAllowed: true},
		{name: "POST mismatched Referer rejected", method: http.MethodPost, referer: "http://evil.example.com/page", wantAllowed: false},
		{name: "POST without Sec-Fetch-Site Origin or Referer rejected", method: http.MethodPost, wantAllowed: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{"prompt": {"hello"}}
			req := httptest.NewRequest(tc.method, targetURL, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("HX-Request", "true")
			if tc.secFetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tc.secFetchSite)
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.referer != "" {
				req.Header.Set("Referer", tc.referer)
			}

			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if tc.wantAllowed {
				assert.NotEqual(t, http.StatusForbidden, rec.Code)
			} else {
				assert.Equal(t, http.StatusForbidden, rec.Code)
			}
		})
	}
}

func TestStubsAndStaticAssets(t *testing.T) {
	srv, _ := newTestServer(t, nil, config.WebConfig{
		Enabled: true,
		Listen:  "127.0.0.1:8090",
	})

	// Root redirects to /guilds
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "/guilds", rec.Header().Get("Location"))

	// Phase 2 stubs
	for _, path := range []string{"/config", "/data"} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090"+path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "coming soon")
	}

	// Static files
	for _, path := range []string{"/static/style.css", "/static/htmx-2.0.4.min.js"} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090"+path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.NotEmpty(t, rec.Body.String())
	}
}

func TestGuildListUnionCacheAndDB(t *testing.T) {
	caches := cache.New(cache.WithCaches(cache.FlagGuilds | cache.FlagChannels))
	cachedGuildID := snowflake.MustParse("111111111111111111")
	caches.AddGuild(discord.CacheGuild{
		Guild: discord.Guild{
			ID:   cachedGuildID,
			Name: "Cached Server",
		},
	})
	client := &bot.Client{Caches: caches}

	srv, repo := newTestServer(t, client, config.WebConfig{
		Enabled: true,
		Listen:  "127.0.0.1:8090",
	})

	ctx := context.Background()
	require.NoError(t, repo.SetGuildSetting(ctx, "222222222222222222", "state", "off"))
	require.NoError(t, repo.SetGuildSetting(ctx, cachedGuildID.String(), "state", "on"))

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/guilds", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "Cached Server")
	assert.Contains(t, body, "222222222222222222")
	assert.Contains(t, body, "Not in cache (DB only)")
}

func TestGuildSettingsSaveAndResetRoundTrips(t *testing.T) {
	guildID := "111111111111111111"
	textChanID := "333333333333333333"
	voiceChanID := "444444444444444444"

	caches := cache.New(cache.WithCaches(cache.FlagGuilds | cache.FlagChannels))
	gSF := snowflake.MustParse(guildID)
	caches.AddGuild(discord.CacheGuild{
		Guild: discord.Guild{ID: gSF, Name: "Test Guild"},
	})
	var textCh discord.GuildTextChannel
	require.NoError(t, json.Unmarshal([]byte(`{"id":"`+textChanID+`","guild_id":"`+guildID+`","name":"general","type":0}`), &textCh))
	caches.AddChannel(textCh)

	var voiceCh discord.GuildVoiceChannel
	require.NoError(t, json.Unmarshal([]byte(`{"id":"`+voiceChanID+`","guild_id":"`+guildID+`","name":"lounge","type":2}`), &voiceCh))
	caches.AddChannel(voiceCh)
	client := &bot.Client{Caches: caches}

	srv, repo := newTestServer(t, client, config.WebConfig{
		Enabled: true,
		Listen:  "127.0.0.1:8090",
	})

	postForm := func(path string, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8090"+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("HX-Request", "true")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	ctx := context.Background()

	// 1. General settings save + per-field reset
	rec := postForm("/guilds/"+guildID+"/general", url.Values{
		"state":         {"off"},
		"threshold":     {"0.42"},
		"thresholdSexe": {"0.12"},
		"temperature":   {"0.85"},
		"messagescount": {"25"},
		"trigger_words": {"Alpha, beta-1"},
		// memory_enabled omitted => false
	})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "General settings saved.")

	assert.Equal(t, "off", repo.GetState(ctx, guildID))
	assert.InDelta(t, 0.42, repo.GetThreshold(ctx, guildID), 1e-6)
	assert.InDelta(t, 0.12, repo.GetThresholdSexe(ctx, guildID), 1e-6)
	assert.InDelta(t, float32(0.85), repo.GetTemperature(ctx, guildID), 1e-5)
	assert.Equal(t, 25, repo.GetMessagesCount(ctx, guildID))
	assert.False(t, repo.GetMemoryEnabled(ctx, guildID))
	words, ok := repo.GetTriggerWords(ctx, guildID)
	require.True(t, ok)
	assert.Equal(t, []string{"alpha", "beta-1"}, words)

	// Reset threshold and trigger_words
	rec = postForm("/guilds/"+guildID+"/reset", url.Values{"key": {"threshold"}, "section": {"general"}})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, database.DefaultThreshold, repo.GetThreshold(ctx, guildID))

	rec = postForm("/guilds/"+guildID+"/reset", url.Values{"key": {"trigger_words"}, "section": {"general"}})
	require.Equal(t, http.StatusOK, rec.Code)
	_, ok = repo.GetTriggerWords(ctx, guildID)
	assert.False(t, ok)

	// 2. Prompts save, empty clears override
	rec = postForm("/guilds/"+guildID+"/prompts", url.Values{
		"prompt":       {"Custom system prompt"},
		"voice_prompt": {"Custom voice prompt"},
	})
	require.Equal(t, http.StatusOK, rec.Code)
	p, ok := repo.GetPrompt(ctx, guildID)
	require.True(t, ok)
	assert.Equal(t, "Custom system prompt", p)
	vp, ok := repo.GetVoicePrompt(ctx, guildID)
	require.True(t, ok)
	assert.Equal(t, "Custom voice prompt", vp)

	rec = postForm("/guilds/"+guildID+"/prompts", url.Values{
		"prompt":       {""},
		"voice_prompt": {"   "},
	})
	require.Equal(t, http.StatusOK, rec.Code)
	_, ok = repo.GetPrompt(ctx, guildID)
	assert.False(t, ok)
	_, ok = repo.GetVoicePrompt(ctx, guildID)
	assert.False(t, ok)

	// 3. Scheduled channels save + reset
	rec = postForm("/guilds/"+guildID+"/channels", url.Values{
		"horoscope_channel":      {textChanID},
		"farting_friday_channel": {textChanID},
	})
	require.Equal(t, http.StatusOK, rec.Code)
	hCh, err := repo.GetHoroscopeChannel(ctx, guildID)
	require.NoError(t, err)
	assert.Equal(t, textChanID, hCh)
	ffCh, err := repo.GetFartingFridayChannel(ctx, guildID)
	require.NoError(t, err)
	assert.Equal(t, textChanID, ffCh)

	rec = postForm("/guilds/"+guildID+"/reset", url.Values{"key": {"horoscope_channel"}, "section": {"channels"}})
	require.Equal(t, http.StatusOK, rec.Code)
	_, err = repo.GetHoroscopeChannel(ctx, guildID)
	assert.Error(t, err)

	// 4. Voice settings save + reset all
	rec = postForm("/guilds/"+guildID+"/voice", url.Values{
		"voice_auto_join":         {"on"},
		"voice_auto_join_channel": {voiceChanID},
		// voice_enabled omitted => false
	})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, repo.GetVoiceEnabled(ctx, guildID))
	assert.True(t, repo.GetVoiceAutoJoin(ctx, guildID))
	ajCh, ok := repo.GetVoiceAutoJoinChannel(ctx, guildID)
	require.True(t, ok)
	assert.Equal(t, voiceChanID, ajCh)

	rec = postForm("/guilds/"+guildID+"/voice/reset", url.Values{})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, repo.GetVoiceEnabled(ctx, guildID))
	assert.False(t, repo.GetVoiceAutoJoin(ctx, guildID))
	_, ok = repo.GetVoiceAutoJoinChannel(ctx, guildID)
	assert.False(t, ok)

	// 5. Reengage message + per-channel save + reset
	rec = postForm("/guilds/"+guildID+"/reengage/message", url.Values{
		"reengage_message": {"Wake up everyone!"},
	})
	require.Equal(t, http.StatusOK, rec.Code)
	rm, ok := repo.GetReengageMessage(ctx, guildID)
	require.True(t, ok)
	assert.Equal(t, "Wake up everyone!", rm)

	rec = postForm("/guilds/"+guildID+"/reengage/channel", url.Values{
		"channel_id": {textChanID},
		"enabled":    {"on"},
		"chance":     {"0.25"},
		"threshold":  {"45"},
	})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, repo.GetReengageEnabled(ctx, guildID, textChanID))
	ch, ok := repo.GetReengageChance(ctx, guildID, textChanID)
	require.True(t, ok)
	assert.InDelta(t, 0.25, ch, 1e-6)
	th, ok := repo.GetReengageThreshold(ctx, guildID, textChanID)
	require.True(t, ok)
	assert.Equal(t, 45, th)

	rec = postForm("/guilds/"+guildID+"/reengage/channel/"+textChanID+"/reset", url.Values{})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, repo.GetReengageEnabled(ctx, guildID, textChanID))
	_, ok = repo.GetReengageChance(ctx, guildID, textChanID)
	assert.False(t, ok)
	_, ok = repo.GetReengageThreshold(ctx, guildID, textChanID)
	assert.False(t, ok)

	// 6. Full guild detail page renders cached channel pickers and bookkeeping
	require.NoError(t, repo.SetLastMessage(ctx, guildID, 1700000000))
	require.NoError(t, repo.SetChannelLastMessage(ctx, guildID, textChanID, 1700000100))

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/guilds/"+guildID, nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "Test Guild")
	assert.Contains(t, body, "#general")
	assert.Contains(t, body, "lounge")
	assert.Contains(t, body, "Bookkeeping timestamps")
	assert.Contains(t, body, "1700000000")
}

func TestGuildSettingsValidationErrors(t *testing.T) {
	srv, repo := newTestServer(t, nil, config.WebConfig{
		Enabled: true,
		Listen:  "127.0.0.1:8090",
	})
	guildID := "555555555555555555"
	// Make the guild known to the route guard (no cache in this test).
	require.NoError(t, repo.SetLastMessage(context.Background(), guildID, 1))

	post := func(path string, form url.Values, htmx bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8090"+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		if htmx {
			req.Header.Set("HX-Request", "true")
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	validGeneral := func() url.Values {
		return url.Values{
			"state":          {"on"},
			"threshold":      {"0.1"},
			"thresholdSexe":  {"0.05"},
			"temperature":    {"0.5"},
			"messagescount":  {"50"},
			"memory_enabled": {"on"},
			"trigger_words":  {"feun"},
		}
	}

	t.Run("general validation errors do not persist", func(t *testing.T) {
		cases := []struct {
			name    string
			mutate  func(url.Values)
			wantErr string
		}{
			{name: "bad state", mutate: func(v url.Values) { v.Set("state", "maybe") }, wantErr: "State must be"},
			{name: "threshold not a number", mutate: func(v url.Values) { v.Set("threshold", "abc") }, wantErr: "Threshold must be"},
			{name: "thresholdSexe infinite", mutate: func(v url.Values) { v.Set("thresholdSexe", "Infinity") }, wantErr: "Threshold (sexe)"},
			{name: "temperature NaN", mutate: func(v url.Values) { v.Set("temperature", "NaN") }, wantErr: "Temperature must be"},
			{name: "temperature overflows float32", mutate: func(v url.Values) { v.Set("temperature", "1e39") }, wantErr: "Temperature must be"},
			{name: "messagescount not a number", mutate: func(v url.Values) { v.Set("messagescount", "abc") }, wantErr: "Messages count"},
			{name: "messagescount not int", mutate: func(v url.Values) { v.Set("messagescount", "1.5") }, wantErr: "Messages count"},
			{name: "invalid trigger word", mutate: func(v url.Values) { v.Set("trigger_words", "bad word!") }, wantErr: "Invalid trigger words"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				form := validGeneral()
				tc.mutate(form)
				rec := post("/guilds/"+guildID+"/general", form, true)
				assert.Equal(t, http.StatusOK, rec.Code)
				assert.Contains(t, rec.Body.String(), tc.wantErr)

				// Non-htmx returns 422
				recNoHTMX := post("/guilds/"+guildID+"/general", form, false)
				assert.Equal(t, http.StatusUnprocessableEntity, recNoHTMX.Code)
				assert.Contains(t, recNoHTMX.Body.String(), tc.wantErr)
			})
		}

		stored, err := repo.GetGuildSettingsMap(context.Background(), guildID)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"last_message": "1"}, stored)
	})

	t.Run("channels and voice snowflake validation", func(t *testing.T) {
		rec := post("/guilds/"+guildID+"/channels", url.Values{
			"horoscope_channel": {"not-a-snowflake"},
		}, true)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "valid Discord snowflake")

		rec = post("/guilds/"+guildID+"/voice", url.Values{
			"voice_auto_join_channel": {"abc"},
		}, true)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "valid Discord snowflake")
	})

	t.Run("reengage validation errors", func(t *testing.T) {
		rec := post("/guilds/"+guildID+"/reengage/message", url.Values{
			"reengage_message": {strings.Repeat("a", 501)},
		}, true)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "at most 500")

		rec = post("/guilds/"+guildID+"/reengage/channel", url.Values{
			"channel_id": {"333333333333333333"},
			"enabled":    {"on"},
			"chance":     {"abc"},
		}, true)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "Reengage chance must be")

		rec = post("/guilds/"+guildID+"/reengage/channel", url.Values{
			"channel_id": {"333333333333333333"},
			"enabled":    {"on"},
			"threshold":  {"1.5"},
		}, true)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "Reengage threshold must be")
	})
}

func TestServerStartAndShutdownSmoke(t *testing.T) {
	srv, repo := newTestServer(t, nil, config.WebConfig{
		Enabled: true,
		Listen:  "127.0.0.1:0",
	})
	require.NoError(t, repo.SetGuildSetting(context.Background(), "999999999999999999", "state", "on"))

	require.NoError(t, srv.Start())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	addr := srv.Addr()
	require.NotEmpty(t, addr)

	resp, err := http.Get("http://" + addr + "/guilds")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "999999999999999999")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, srv.Shutdown(ctx))
}
