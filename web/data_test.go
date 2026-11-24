package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	"polynux/disgoroq/medialibrary"
)

const dataTestGuild = "111111111111111111"

type fakeDataEmbedder struct {
	err   error
	calls int
}

func (f *fakeDataEmbedder) GenerateEmbedding(_ context.Context, text string) ([]float32, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	vector := make([]float32, medialibrary.EmbeddingDim)
	if strings.Contains(strings.ToLower(text), "cat") {
		vector[0] = 1
	} else {
		vector[1] = 1
	}
	return vector, nil
}

type dataTestEnv struct {
	srv       *Server
	repo      *database.Repository
	mediaRepo *medialibrary.Repository
	db        *sql.DB
	embedder  *fakeDataEmbedder
}

func newDataTestEnv(t *testing.T, client *bot.Client, withMediaService bool, embedder medialibrary.Embedder) *dataTestEnv {
	t.Helper()

	dbConn := setupTestDB(t)
	repo := database.NewRepositoryWithDB(dbConn)
	mediaRepo := medialibrary.NewRepository(dbConn)

	deps := Deps{
		Client:    client,
		Repo:      repo,
		Config:    config.DefaultConfig(),
		MediaRepo: mediaRepo,
	}
	if withMediaService {
		deps.Media = medialibrary.NewService(mediaRepo, embedder, config.DefaultConfig().MediaLibrary)
		t.Cleanup(deps.Media.Close)
	}

	webCfg := config.WebConfig{Enabled: true, Listen: "127.0.0.1:8090"}
	srv := New(deps, webCfg)

	env := &dataTestEnv{
		srv:       srv,
		repo:      repo,
		mediaRepo: mediaRepo,
		db:        dbConn,
	}
	if fake, ok := embedder.(*fakeDataEmbedder); ok {
		env.embedder = fake
	}
	return env
}

func (e *dataTestEnv) get(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090"+target, nil)
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func (e *dataTestEnv) post(t *testing.T, target string, form url.Values, sameOrigin bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8090"+target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	if sameOrigin {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	rec := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func (e *dataTestEnv) insertSummary(t *testing.T, guildID, userID, text string, createdAt, updatedAt int64) int64 {
	t.Helper()
	res, err := e.db.Exec(`
		INSERT INTO conversation_summaries (guild_id, user_id, summary_text, message_count, created_at, updated_at)
		VALUES (?, ?, ?, 3, ?, ?)`, guildID, userID, text, createdAt, updatedAt)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

func (e *dataTestEnv) insertBufferMessage(t *testing.T, guildID, channelID, messageID, userID, nick, content string, processed bool) {
	t.Helper()
	processedArg := 0
	if processed {
		processedArg = 1
	}
	_, err := e.db.Exec(`
		INSERT INTO message_buffer (guild_id, channel_id, message_id, user_id, author_nick, content, has_image, image_description, timestamp, processed)
		VALUES (?, ?, ?, ?, ?, ?, 0, '', ?, ?)`,
		guildID, channelID, messageID, userID, nick, content, time.Now().Unix(), processedArg)
	require.NoError(t, err)
}

func (e *dataTestEnv) insertDiscordMessage(t *testing.T, guildID, channelID, messageID, authorID, authorName, content, messageJSON string, deleted bool) {
	t.Helper()
	var deletedAt any
	if deleted {
		deletedAt = time.Now().Unix()
	}
	_, err := e.db.Exec(`
		INSERT INTO discord_messages (message_id, channel_id, guild_id, author_id, author_username, content, referenced_message_id, message_json, created_at, edited_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, '999', ?, ?, NULL, ?)`,
		messageID, channelID, guildID, authorID, authorName, content, messageJSON, time.Now().Unix(), deletedAt)
	require.NoError(t, err)
}

func (e *dataTestEnv) insertEvent(t *testing.T, eventType, guildID, errorText string, timestamp int64) {
	t.Helper()
	var errArg any
	if errorText != "" {
		errArg = errorText
	}
	_, err := e.db.Exec(`
		INSERT INTO bot_events (timestamp, event_type, guild_id, channel_id, message_id, user_id, details, duration_ms, error)
		VALUES (?, ?, ?, '222', '333', '444', '{"messages_count":2}', 12, ?)`,
		timestamp, eventType, guildID, errArg)
	require.NoError(t, err)
}

func (e *dataTestEnv) insertAttachment(t *testing.T, kind, key, provider, model, content string, createdAt int64) int64 {
	t.Helper()
	res, err := e.db.Exec(`
		INSERT INTO attachment_cache (cache_kind, attachment_key, source_url, filename, content_type, size_bytes, provider, model, instruction_version, content, created_at, updated_at)
		VALUES (?, ?, 'https://example.com/file.png', 'file.png', 'image/png', 2048, ?, ?, 'v1', ?, ?, ?)`,
		kind, key, provider, model, content, createdAt, createdAt)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

func (e *dataTestEnv) seedMedia(t *testing.T, guildID, rawURL, kind, description, triggerText string) {
	t.Helper()
	var embedder medialibrary.Embedder = &fakeDataEmbedder{}
	if e.embedder != nil {
		embedder = e.embedder
	}
	ctx := context.Background()
	descriptionEmbedding, err := medialibrary.EmbedDocument(ctx, embedder, description)
	require.NoError(t, err)
	triggerEmbedding, err := medialibrary.EmbedDocument(ctx, embedder, triggerText)
	require.NoError(t, err)

	now := time.Now().Unix()
	require.NoError(t, e.mediaRepo.UpsertImport(ctx, medialibrary.Entry{
		GuildID:              guildID,
		URL:                  rawURL,
		Kind:                 kind,
		Description:          description,
		Uses:                 3,
		FirstSeen:            now - 100,
		LastSeen:             now,
		TriggerText:          triggerText,
		DescriptionEmbedding: descriptionEmbedding,
		TriggerEmbedding:     triggerEmbedding,
	}))
}

func TestDataTabsEmpty(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)

	cases := map[string]string{
		"summaries":   "No summaries match these filters.",
		"buffer":      "No buffered messages match these filters.",
		"messages":    "No cached messages match these filters.",
		"attachments": "No attachment cache rows match these filters.",
		"votes":       "No votes match these filters.",
		"events":      "No events match these filters and time range.",
		"media":       "No media library entries match these filters.",
	}

	for tab, want := range cases {
		rec := env.get(t, "/data?tab="+tab)
		require.Equal(t, http.StatusOK, rec.Code, "tab %s", tab)
		body := rec.Body.String()
		assert.Contains(t, body, want, "tab %s", tab)
		assert.Contains(t, body, "Page 1 · 50 per page", "tab %s", tab)
	}

	// Unknown tab falls back to summaries.
	rec := env.get(t, "/data?tab=does-not-exist")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Conversation summaries")
}

func TestDataPaginationClampingAndBeyondEnd(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)

	now := time.Now().Unix()
	for i := range 51 {
		env.insertSummary(t, dataTestGuild, "555", fmt.Sprintf("unique-summary-%02d", i), now-1000, now+int64(i))
	}

	rec := env.get(t, "/data?tab=summaries")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "Page 1 · 50 per page")
	assert.Contains(t, body, "unique-summary-50")
	assert.NotContains(t, body, "unique-summary-00")
	assert.Contains(t, body, "page=2")
	assert.NotContains(t, body, "page=0")

	rec = env.get(t, "/data?tab=summaries&page=2")
	require.Equal(t, http.StatusOK, rec.Code)
	body = rec.Body.String()
	assert.Contains(t, body, "Page 2 · 50 per page")
	assert.Contains(t, body, "unique-summary-00")
	assert.Contains(t, body, "page=1")
	assert.NotContains(t, body, "page=3")

	rec = env.get(t, "/data?tab=summaries&page=99")
	require.Equal(t, http.StatusOK, rec.Code)
	body = rec.Body.String()
	assert.Contains(t, body, "No summaries match these filters.")
	assert.Contains(t, body, "page=98")
	assert.NotContains(t, body, "page=100")

	rec = env.get(t, "/data?tab=summaries&page=-4&limit=-9")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Page 1 · 50 per page")

	rec = env.get(t, "/data?tab=summaries&page=abc&limit=xyz")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Page 1 · 50 per page")

	rec = env.get(t, "/data?tab=summaries&limit=1000")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "200 per page")
}

func TestDataSummaryNamesFiltersAndUnknownGuild(t *testing.T) {
	caches := cache.New(cache.WithCaches(cache.FlagGuilds | cache.FlagChannels))
	cachedGuildID := snowflake.MustParse("777777777777777777")
	caches.AddGuild(discord.CacheGuild{Guild: discord.Guild{ID: cachedGuildID, Name: "Cached Server"}})
	client := &bot.Client{Caches: caches}

	env := newDataTestEnv(t, client, false, nil)

	now := time.Now().Unix()
	env.insertSummary(t, cachedGuildID.String(), "888", "cached guild summary", now-10, now)
	env.insertBufferMessage(t, cachedGuildID.String(), "channel", "m1", "888", "NickFromBuffer", "hello", false)
	env.insertSummary(t, cachedGuildID.String(), "999", "fallback summary", now-11, now-1)
	env.insertDiscordMessage(t, cachedGuildID.String(), "channel", "m2", "999", "FallbackUser", "hi", `{}`, false)
	env.insertSummary(t, dataTestGuild, "999", "other guild summary", now-10, now)

	rec := env.get(t, "/data?tab=summaries&guild_id="+cachedGuildID.String())
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "Cached Server")
	assert.Contains(t, body, "NickFromBuffer")
	assert.Contains(t, body, "cached guild summary")
	assert.NotContains(t, body, "other guild summary")

	rec = env.get(t, "/data?tab=summaries&guild_id="+cachedGuildID.String()+"&user_id=888")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "cached guild summary")

	rec = env.get(t, "/data?tab=summaries&guild_id="+cachedGuildID.String()+"&user_id=000")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "No summaries match these filters.")

	// No buffer row: fall back to discord_messages.author_username.
	rec = env.get(t, "/data?tab=summaries&guild_id="+cachedGuildID.String()+"&user_id=999")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "FallbackUser")
	assert.Contains(t, rec.Body.String(), "fallback summary")

	unknown := "123123123123123123"
	rec = env.get(t, "/data?tab=summaries&guild_id="+unknown)
	require.Equal(t, http.StatusOK, rec.Code)
	body = rec.Body.String()
	assert.Contains(t, body, "No summaries match these filters.")
	assert.Contains(t, body, unknown)

	// A user with no buffered message falls back to the user ID.
	rec = env.get(t, "/data?tab=summaries&guild_id="+dataTestGuild)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), ">999<")
}

func TestDataSummaryDeleteAndForget(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)
	now := time.Now().Unix()
	summaryID := env.insertSummary(t, dataTestGuild, "100", "delete me", now, now)

	rec := env.post(t, "/data/summaries/999999/delete?tab=summaries", url.Values{}, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "already gone")

	rec = env.post(t, fmt.Sprintf("/data/summaries/%d/delete?tab=summaries", summaryID), url.Values{}, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), fmt.Sprintf("Deleted summary #%d.", summaryID))

	var count int
	require.NoError(t, env.db.QueryRow(`SELECT COUNT(*) FROM conversation_summaries`).Scan(&count))
	assert.Zero(t, count)

	env.insertSummary(t, dataTestGuild, "100", "a", now, now)
	env.insertSummary(t, dataTestGuild, "100", "b", now+1, now+1)
	env.insertSummary(t, dataTestGuild, "200", "c", now+2, now+2)

	form := url.Values{"guild_id": {dataTestGuild}, "user_id": {"100"}}
	rec = env.post(t, "/data/summaries/forget?tab=summaries", form, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Deleted 2 summaries for user 100.")

	require.NoError(t, env.db.QueryRow(`SELECT COUNT(*) FROM conversation_summaries WHERE user_id = '100'`).Scan(&count))
	assert.Zero(t, count)

	rec = env.post(t, "/data/summaries/forget?tab=summaries", form, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "No summaries found for that user in this guild.")

	// Missing fields is a validation error, not a 500.
	rec = env.post(t, "/data/summaries/forget?tab=summaries", url.Values{"guild_id": {dataTestGuild}}, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Guild ID and user ID are required")
}

func TestDataRendersUserContentEscaped(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)
	now := time.Now().Unix()

	env.insertSummary(t, dataTestGuild, "100", `<script>alert("summary")</script>`, now, now)
	env.insertDiscordMessage(t, dataTestGuild, "222", "333", "444", "attacker", `<script>alert("message")</script>`, `{"content":"<script>alert("json")</script>"}`, false)

	for _, target := range []string{
		"/data?tab=summaries",
		"/data?tab=messages",
	} {
		rec := env.get(t, target)
		require.Equal(t, http.StatusOK, rec.Code, target)
		body := rec.Body.String()
		assert.NotContains(t, body, "<script>alert", target)
		assert.Contains(t, body, "&lt;script&gt;", target)
	}
}

func TestDataBufferAndMessageFilters(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)

	env.insertBufferMessage(t, dataTestGuild, "222", "b1", "100", "Buffered Nick", "buffer-todo-content", false)
	env.insertBufferMessage(t, dataTestGuild, "222", "b2", "100", "Buffered Nick", "buffer-done-content", true)
	env.insertDiscordMessage(t, dataTestGuild, "222", "d1", "100", "author", "visible-message", `{"id":"1"}`, false)
	env.insertDiscordMessage(t, dataTestGuild, "222", "d2", "100", "author", "deleted-message-content", `{"id":"2"}`, true)

	rec := env.get(t, "/data?tab=buffer&processed=no")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "buffer-todo-content")
	assert.NotContains(t, body, "buffer-done-content")
	assert.Contains(t, body, "Buffered Nick")

	rec = env.get(t, "/data?tab=buffer&processed=yes")
	require.Equal(t, http.StatusOK, rec.Code)
	body = rec.Body.String()
	assert.Contains(t, body, "buffer-done-content")
	assert.NotContains(t, body, "buffer-todo-content")

	rec = env.get(t, "/data?tab=messages")
	require.Equal(t, http.StatusOK, rec.Code)
	body = rec.Body.String()
	assert.Contains(t, body, "visible-message")
	assert.NotContains(t, body, "deleted-message-content")
	assert.Contains(t, body, "reply to")
	assert.Contains(t, body, "message_json")

	rec = env.get(t, "/data?tab=messages&include_deleted=1")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "deleted-message-content")

	rec = env.get(t, "/data?tab=messages&text=visible")
	require.Equal(t, http.StatusOK, rec.Code)
	body = rec.Body.String()
	assert.Contains(t, body, "visible-message")
	assert.NotContains(t, body, "deleted-message-content")
}

func TestDataAttachmentDeleteAndPurge(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)
	now := time.Now().Unix()
	oldCutoff := now - 40*24*60*60

	oldID := env.insertAttachment(t, "vision", "old-key", "groq", "model-a", "old content", oldCutoff)
	newID := env.insertAttachment(t, "vision", "new-key", "groq", "model-a", "new content", now)

	rec := env.post(t, fmt.Sprintf("/data/attachments/%d/delete?tab=attachments", oldID), url.Values{}, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Deleted attachment cache row.")

	rec = env.post(t, fmt.Sprintf("/data/attachments/%d/delete?tab=attachments", oldID), url.Values{}, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "already gone")

	// Purge with an invalid day count must not delete anything.
	rec = env.post(t, "/data/attachments/purge?tab=attachments", url.Values{"days": {"0"}}, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "between 1 and 36500")
	var count int
	require.NoError(t, env.db.QueryRow(`SELECT COUNT(*) FROM attachment_cache`).Scan(&count))
	assert.Equal(t, 1, count)

	env.insertAttachment(t, "vision", "older-key", "groq", "model-a", "older content", oldCutoff)
	rec = env.post(t, "/data/attachments/purge?tab=attachments", url.Values{"days": {"30"}}, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Purged 1 attachment cache row(s) older than 30 day(s).")

	require.NoError(t, env.db.QueryRow(`SELECT COUNT(*) FROM attachment_cache`).Scan(&count))
	assert.Equal(t, 1, count)
	var remainingID int64
	require.NoError(t, env.db.QueryRow(`SELECT id FROM attachment_cache`).Scan(&remainingID))
	assert.Equal(t, newID, remainingID)
}

func TestDataVotesFiltersAndSort(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)
	ctx := context.Background()

	seed := func(messageID, content string, botReply int64, good, bad int) {
		require.NoError(t, env.repo.InsertMessageVote(ctx, database.MessageVote{
			MessageID:  messageID,
			GuildID:    dataTestGuild,
			ChannelID:  "222",
			AuthorID:   "444",
			AuthorName: "Author",
			IsBotReply: botReply,
			Content:    content,
			GoodCount:  0,
			BadCount:   0,
		}))
		require.NoError(t, env.repo.UpdateMessageVoteCounts(ctx, messageID, good, bad))
	}

	seed("v-low", "vote-low", 1, 1, 0)
	seed("v-mid", "vote-mid", 1, 5, 1)
	seed("v-high", "vote-human", 0, 9, 0)

	rec := env.get(t, "/data?tab=votes&sort=good")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	highIdx := strings.Index(body, "vote-human")
	midIdx := strings.Index(body, "vote-mid")
	lowIdx := strings.Index(body, "vote-low")
	require.True(t, highIdx >= 0 && midIdx > highIdx && lowIdx > midIdx, "expected good-count ordering")

	rec = env.get(t, "/data?tab=votes&sort=bad")
	require.Equal(t, http.StatusOK, rec.Code)
	body = rec.Body.String()
	assert.Less(t, strings.Index(body, "vote-mid"), strings.Index(body, "vote-low"))

	rec = env.get(t, "/data?tab=votes&bot_only=1")
	require.Equal(t, http.StatusOK, rec.Code)
	body = rec.Body.String()
	assert.Contains(t, body, "vote-mid")
	assert.NotContains(t, body, "vote-human")

	rec = env.get(t, "/data?tab=votes&guild_id=999999999999999999")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "No votes match these filters.")
}

func TestDataEventsFiltersAndRange(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)
	now := time.Now().Unix()

	env.insertEvent(t, "message_received", dataTestGuild, "", now-60)
	env.insertEvent(t, "ai_call_failed", dataTestGuild, "boom", now-120)

	rec := env.get(t, "/data?tab=events")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "message_received")
	assert.Contains(t, body, "ai_call_failed")
	assert.Contains(t, body, "boom")
	assert.Contains(t, body, `option value="message_received"`)
	assert.Contains(t, body, `option value="ai_call_failed"`)

	rec = env.get(t, "/data?tab=events&event_type=ai_call_failed")
	require.Equal(t, http.StatusOK, rec.Code)
	body = rec.Body.String()
	assert.Contains(t, body, "ai_call_failed")
	assert.NotContains(t, body, ">message_received</td>")

	rec = env.get(t, "/data?tab=events&has_error=1")
	require.Equal(t, http.StatusOK, rec.Code)
	body = rec.Body.String()
	assert.Contains(t, body, "ai_call_failed")
	assert.NotContains(t, body, ">message_received</td>")

	rec = env.get(t, "/data?tab=events&from=2020-01-01T00:00&to=2020-01-02T00:00")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "No events match these filters and time range.")

	rec = env.get(t, "/data?tab=events&from=not-a-date&to=also-bad")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "message_received")

	rec = env.get(t, "/data?tab=events&guild_id=999999999999999999")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "No events match these filters and time range.")
}

func TestDataMediaBrowseDeleteWithDisabledService(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)
	env.seedMedia(t, dataTestGuild, "https://example.com/cat.png", "image", "cat reaction", "when the cat")

	rec := env.get(t, "/data?tab=media")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "Media library is disabled")
	assert.Contains(t, body, "cat reaction")
	assert.Contains(t, body, `loading="lazy"`)
	assert.Contains(t, body, `referrerpolicy="no-referrer"`)

	rec = env.get(t, "/data?tab=media&media_kind=gif")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "No media library entries match these filters.")

	form := url.Values{
		"guild_id":     {dataTestGuild},
		"url":          {"https://example.com/cat.png"},
		"description":  {"changed"},
		"trigger_text": {"changed trigger"},
	}
	rec = env.post(t, "/data/media/edit?tab=media", form, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Media library is disabled; edit is unavailable.")

	var description string
	require.NoError(t, env.db.QueryRow(
		`SELECT description FROM media_library WHERE guild_id = ? AND url = ?`,
		dataTestGuild, "https://example.com/cat.png").Scan(&description))
	assert.Equal(t, "cat reaction", description)

	rec = env.get(t, "/data?tab=media&search_guild="+dataTestGuild+"&search_query=cat")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Media library is disabled; edit and search are unavailable.")

	// Delete still works through the repository when the service is nil.
	delForm := url.Values{"guild_id": {dataTestGuild}, "url": {"https://example.com/cat.png"}}
	rec = env.post(t, "/data/media/delete?tab=media", delForm, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Deleted media library entry.")

	var count int
	require.NoError(t, env.db.QueryRow(`SELECT COUNT(*) FROM media_library`).Scan(&count))
	assert.Zero(t, count)

	rec = env.post(t, "/data/media/delete?tab=media", delForm, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "That media library entry was already gone.")
}

func TestDataMediaEditRecomputesEmbeddings(t *testing.T) {
	embedder := &fakeDataEmbedder{}
	env := newDataTestEnv(t, nil, true, embedder)
	const rawURL = "https://example.com/cat.png"
	env.seedMedia(t, dataTestGuild, rawURL, "image", "old description", "old trigger")

	form := url.Values{
		"guild_id":     {dataTestGuild},
		"url":          {rawURL},
		"description":  {"cat with a hat"},
		"trigger_text": {"cat hat"},
	}
	rec := env.post(t, "/data/media/edit?tab=media", form, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "embeddings recomputed")
	assert.Greater(t, embedder.calls, 0)

	var (
		description, triggerText string
		hasDescriptionEmbedding  int
		hasTriggerEmbedding      int
	)
	require.NoError(t, env.db.QueryRow(`
		SELECT description, trigger_text,
		       description_embedding IS NOT NULL,
		       trigger_embedding IS NOT NULL
		FROM media_library WHERE guild_id = ? AND url = ?`, dataTestGuild, rawURL).
		Scan(&description, &triggerText, &hasDescriptionEmbedding, &hasTriggerEmbedding))
	assert.Equal(t, "cat with a hat", description)
	assert.Equal(t, "cat hat", triggerText)
	assert.Equal(t, 1, hasDescriptionEmbedding)
	assert.Equal(t, 1, hasTriggerEmbedding)

	// Embedder failure leaves both text and embeddings untouched.
	embedder.err = errors.New("ollama is down")
	failingForm := url.Values{
		"guild_id":     {dataTestGuild},
		"url":          {rawURL},
		"description":  {"should not land"},
		"trigger_text": {"should not land either"},
	}
	rec = env.post(t, "/data/media/edit?tab=media", failingForm, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Edit failed:")
	assert.Contains(t, rec.Body.String(), "ollama is down")

	require.NoError(t, env.db.QueryRow(`
		SELECT description, trigger_text,
		       description_embedding IS NOT NULL,
		       trigger_embedding IS NOT NULL
		FROM media_library WHERE guild_id = ? AND url = ?`, dataTestGuild, rawURL).
		Scan(&description, &triggerText, &hasDescriptionEmbedding, &hasTriggerEmbedding))
	assert.Equal(t, "cat with a hat", description)
	assert.Equal(t, "cat hat", triggerText)
	assert.Equal(t, 1, hasDescriptionEmbedding)
	assert.Equal(t, 1, hasTriggerEmbedding)

	// Unknown entry is reported without a 500.
	embedder.err = nil
	missingForm := url.Values{
		"guild_id":     {dataTestGuild},
		"url":          {"https://example.com/missing.png"},
		"description":  {"x"},
		"trigger_text": {"y"},
	}
	rec = env.post(t, "/data/media/edit?tab=media", missingForm, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "not found")
}

func TestDataMediaSearchValidationAndScores(t *testing.T) {
	embedder := &fakeDataEmbedder{}
	env := newDataTestEnv(t, nil, true, embedder)
	env.seedMedia(t, dataTestGuild, "https://example.com/cat.png", "image", "cat reaction", "cat")
	env.seedMedia(t, dataTestGuild, "https://example.com/dog.png", "image", "dog reaction", "dog")

	rec := env.get(t, "/data?tab=media&search_guild="+dataTestGuild+"&search_query=cat")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, ">1.000</td>")
	assert.Contains(t, body, "cat reaction")
	require.Less(t, strings.Index(body, "cat reaction"), strings.Index(body, "dog reaction"))

	rec = env.get(t, "/data?tab=media&search_guild="+dataTestGuild+"&search_query=cat&search_mode=trigger")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), ">1.000</td>")

	rec = env.get(t, "/data?tab=media&search_guild="+dataTestGuild)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Enter a search query.")

	rec = env.get(t, "/data?tab=media&search_query=cat")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Select a guild to search.")

	// Whitespace-only query behaves like an empty query.
	rec = env.get(t, "/data?tab=media&search_guild="+dataTestGuild+"&search_query=%20%20")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Enter a search query.")
}

func TestDataPostWithoutSameOriginHeadersRejected(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)
	now := time.Now().Unix()
	env.insertSummary(t, dataTestGuild, "100", "keep me", now, now)

	form := url.Values{"guild_id": {dataTestGuild}, "user_id": {"100"}}
	rec := env.post(t, "/data/summaries/forget?tab=summaries", form, false)
	require.Equal(t, http.StatusForbidden, rec.Code)

	var count int
	require.NoError(t, env.db.QueryRow(`SELECT COUNT(*) FROM conversation_summaries`).Scan(&count))
	assert.Equal(t, 1, count)
}

func TestDataMediaEditRefusesWithoutEmbedder(t *testing.T) {
	env := newDataTestEnv(t, nil, true, nil)
	const rawURL = "https://example.com/cat.png"
	env.seedMedia(t, dataTestGuild, rawURL, "image", "old description", "old trigger")

	form := url.Values{
		"guild_id":     {dataTestGuild},
		"url":          {rawURL},
		"description":  {"new description"},
		"trigger_text": {"new trigger"},
	}
	rec := env.post(t, "/data/media/edit?tab=media", form, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "embedding provider is not configured")

	var description string
	require.NoError(t, env.db.QueryRow(
		`SELECT description FROM media_library WHERE guild_id = ? AND url = ?`, dataTestGuild, rawURL).Scan(&description))
	assert.Equal(t, "old description", description)
}

func TestDataDeleteRoutesRejectInvalidIDs(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)
	now := time.Now().Unix()
	env.insertSummary(t, dataTestGuild, "100", "keep me", now, now)
	env.insertAttachment(t, "vision", "keep-key", "groq", "model", "keep", now)

	targets := []string{
		"/data/summaries/abc/delete?tab=summaries",
		"/data/summaries/-3/delete?tab=summaries",
		"/data/attachments/abc/delete?tab=attachments",
		"/data/attachments/-3/delete?tab=attachments",
	}
	for _, target := range targets {
		rec := env.post(t, target, url.Values{}, true)
		assert.Equal(t, http.StatusBadRequest, rec.Code, target)
	}

	var summaries int
	require.NoError(t, env.db.QueryRow(`SELECT COUNT(*) FROM conversation_summaries`).Scan(&summaries))
	assert.Equal(t, 1, summaries)

	var attachments int
	require.NoError(t, env.db.QueryRow(`SELECT COUNT(*) FROM attachment_cache`).Scan(&attachments))
	assert.Equal(t, 1, attachments)
}

func TestDataMediaUnsafeURLNeverBecomesAttribute(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)
	env.seedMedia(t, dataTestGuild, "javascript:alert(1)", "image", "unsafe preview", "unsafe")

	rec := env.get(t, "/data?tab=media")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.NotContains(t, body, `src="javascript:`)
	assert.NotContains(t, body, `href="javascript:`)
	assert.Contains(t, body, "unsafe URL")
	assert.Contains(t, body, "javascript:alert(1)")
}

func TestDataFormActionsKeepFilters(t *testing.T) {
	env := newDataTestEnv(t, nil, true, &fakeDataEmbedder{})
	now := time.Now().Unix()
	summaryID := env.insertSummary(t, dataTestGuild, "100", "keep", now, now)
	env.seedMedia(t, dataTestGuild, "https://example.com/cat.png", "image", "cat", "cat")

	rec := env.get(t, "/data?tab=media&guild_id="+dataTestGuild)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `action="/data/media/edit?tab=media&amp;guild_id=`+dataTestGuild+`"`)
	assert.Contains(t, body, `action="/data/media/delete?tab=media&amp;guild_id=`+dataTestGuild+`"`)

	rec = env.get(t, "/data?tab=attachments&guild_id="+dataTestGuild)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `action="/data/attachments/purge?tab=attachments&amp;guild_id=`+dataTestGuild+`"`)

	rec = env.get(t, "/data?tab=summaries&guild_id="+dataTestGuild)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(),
		fmt.Sprintf(`action="/data/summaries/%d/delete?tab=summaries&amp;guild_id=%s"`, summaryID, dataTestGuild))
	assert.Contains(t, rec.Body.String(),
		`action="/data/summaries/forget?tab=summaries&amp;guild_id=`+dataTestGuild+`"`)
}

func TestDataEventsEchoesEffectiveToAndLabelsUTC(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)

	for _, target := range []string{
		"/data?tab=events",
		"/data?tab=events&to=not-a-date",
	} {
		rec := env.get(t, target)
		require.Equal(t, http.StatusOK, rec.Code, target)
		body := rec.Body.String()
		assert.Contains(t, body, "From (UTC)", target)
		assert.Contains(t, body, "To (UTC)", target)
		assert.Regexp(t, `name="to" type="datetime-local" value="[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}"`, body, target)
	}

	// Explicit UTC bounds keep the events inside the range.
	eventTime := time.Date(2020, 1, 1, 0, 0, 30, 0, time.UTC)
	env.insertEvent(t, "message_received", dataTestGuild, "", eventTime.Unix())
	rec := env.get(t, "/data?tab=events&from=2020-01-01T00:00&to=2020-01-01T00:01")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "message_received")
}

func TestDataPageCeilingHasNoNextLink(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)
	rec := env.get(t, fmt.Sprintf("/data?tab=summaries&page=%d", dataMaxPage))
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.NotContains(t, body, fmt.Sprintf("page=%d", dataMaxPage+1))
	assert.Contains(t, body, "Next page")
}

func TestDataWithoutRepoReturns503(t *testing.T) {
	srv := New(Deps{Config: config.DefaultConfig()}, config.WebConfig{Enabled: true, Listen: "127.0.0.1:8090"})

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8090/data", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	form := url.Values{"guild_id": {dataTestGuild}, "user_id": {"100"}}
	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8090/data/summaries/forget", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
