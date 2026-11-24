package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDirectImageURL(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"gif with signed query", "https://cdn.discordapp.com/attachments/1/2/cat.gif?ex=1&is=2&hm=3", "https://cdn.discordapp.com/attachments/1/2/cat.gif?ex=1&is=2&hm=3"},
		{"tenor media host", "https://media.tenor.com/abcAAAAM/cat.gif", "https://media.tenor.com/abcAAAAM/cat.gif"},
		{"twitter format query", "https://pbs.twimg.com/media/GX1?format=jpg&name=small", "https://pbs.twimg.com/media/GX1?format=jpg&name=small"},
		{"tenor page is not an image", "https://tenor.com/view/cat-gif-12345", ""},
		{"youtube page is not an image", "https://www.youtube.com/watch?v=W_0pGPZvPw8", ""},
		{"javascript rejected", "javascript:alert(1)", ""},
		{"data URL rejected", "data:image/png;base64,AAAA", ""},
		{"twitter without image format", "https://pbs.twimg.com/media/GX1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, directImageURL(tc.raw))
		})
	}
}

func TestMediaPreviewSrcYouTube(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"https://www.youtube.com/watch?v=W_0pGPZvPw8", "https://i.ytimg.com/vi/W_0pGPZvPw8/mqdefault.jpg"},
		{"https://youtube.com/shorts/3W5x5gDtt9I?feature=share", "https://i.ytimg.com/vi/3W5x5gDtt9I/mqdefault.jpg"},
		{"https://youtu.be/QXtbbR08qwo", "https://i.ytimg.com/vi/QXtbbR08qwo/mqdefault.jpg"},
		{"https://www.youtube.com/channel/UC123", ""},
		{"https://www.youtube.com/watch?v=bad id", ""},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, mediaPreviewSrc(tc.raw, "youtube"), tc.raw)
	}
	assert.Equal(t, "", mediaPreviewSrc("https://www.youtube.com/watch?v=W_0pGPZvPw8", "tweet"))
}

func TestMediaPreviewSrcRoutesThroughProxy(t *testing.T) {
	assert.Equal(t,
		mediaThumbSrc("https://cdn.discordapp.com/attachments/1/2/cat.gif?ex=1"),
		mediaPreviewSrc("https://cdn.discordapp.com/attachments/1/2/cat.gif?ex=1", "gif"))
	assert.Equal(t,
		mediaThumbSrc("https://tenor.com/view/cat-gif-123"),
		mediaPreviewSrc("https://tenor.com/view/cat-gif-123", "gif"))
	assert.Equal(t,
		mediaThumbSrc("https://giphy.com/gifs/abc"),
		mediaPreviewSrc("https://giphy.com/gifs/abc", "gif"))
	assert.Equal(t,
		mediaThumbSrc("https://klipy.com/gifs/abc-3"),
		mediaPreviewSrc("https://klipy.com/gifs/abc-3", "gif"))
	// Unsupported pages keep the "open" link instead of a broken tile.
	assert.Equal(t, "", mediaPreviewSrc("https://x.com/user/status/123", "tweet"))
	assert.Equal(t, "", mediaPreviewSrc("javascript:alert(1)", "gif"))
}

func TestShortDisplayURL(t *testing.T) {
	assert.Equal(t,
		"cdn.discordapp.com/attachments/1/2/cat.gif",
		shortDisplayURL("https://cdn.discordapp.com/attachments/1/2/cat.gif?ex=1&is=2&hm=3"))
	assert.Equal(t, "x.com/user/status/123", shortDisplayURL("https://x.com/user/status/123?s=46&t=abc"))
	assert.Equal(t, "tenor.com", shortDisplayURL("https://tenor.com/?utm=1"))

	long := shortDisplayURL("https://example.com/" + strings.Repeat("a", 120))
	assert.LessOrEqual(t, len([]rune(long)), dataShortURLRunes)
	assert.Contains(t, long, "…")

	// Unsafe schemes are not dropped, so the page still shows what the entry holds.
	assert.Equal(t, "javascript:alert(1)", shortDisplayURL("javascript:alert(1)"))
}

func TestMessageMediaPicksRenderableImage(t *testing.T) {
	raw := `{"attachments":[
		{"url":"https://example.com/doc.pdf","content_type":"application/pdf","filename":"doc.pdf"},
		{"url":"https://cdn.discordapp.com/attachments/1/2/pic.png?ex=1","proxy_url":"https://media.discordapp.net/attachments/1/2/pic.png?ex=1","content_type":"image/webp","filename":"pic.png"}
	]}`
	thumb, href, label := messageMedia(raw)
	want := mediaThumbSrc("https://media.discordapp.net/attachments/1/2/pic.png?ex=1")
	assert.Equal(t, want, thumb)
	assert.Equal(t, want, href)
	assert.Equal(t, "pic.png", label)

	thumb, href, label = messageMedia(`{"attachments":[{"url":"javascript:alert(1)","filename":"x"}]}`)
	assert.Empty(t, thumb)
	assert.Empty(t, href)
	assert.Empty(t, label)

	thumb, _, _ = messageMedia(`not json`)
	assert.Empty(t, thumb)
}

func TestDataMediaRendersThumbnailsAndCompactURLs(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)
	signed := "https://cdn.discordapp.com/attachments/1/2/cat.gif?ex=1&is=2&hm=3"
	env.seedMedia(t, dataTestGuild, signed, "gif", "dancing cat", "when the cat")
	env.seedMedia(t, dataTestGuild, "https://www.youtube.com/watch?v=W_0pGPZvPw8", "youtube", "song", "play it")

	rec := env.get(t, "/data?tab=media")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	assert.Contains(t, body, `src="`+mediaThumbSrc(signed)+`"`)
	assert.Contains(t, body, `src="https://i.ytimg.com/vi/W_0pGPZvPw8/mqdefault.jpg"`)
	assert.Contains(t, body, `href="`+mediaThumbSrc(signed)+`"`)
	assert.Contains(t, body, `title="https://cdn.discordapp.com/attachments/1/2/cat.gif?ex=1&amp;is=2&amp;hm=3"`)
	assert.Contains(t, body, `>cdn.discordapp.com/attachments/1/2/cat.gif</a>`)
	assert.Contains(t, body, `>youtube.com/watch</a>`)
	assert.NotContains(t, body, ">https://cdn.discordapp.com/attachments/1/2/cat.gif?ex=1")
}

func TestDataAttachmentsRenderPreviewAndCompactSource(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)
	source := "https://cdn.discordapp.com/attachments/1/2/image.png?ex=1&is=2&hm=3"
	_, err := env.db.Exec(`
		INSERT INTO attachment_cache (cache_kind, attachment_key, source_url, filename, content_type, size_bytes, provider, model, instruction_version, content, created_at, updated_at)
		VALUES ('image_description', 'key', ?, 'image.png', 'image/png', 2048, 'groq', 'model', 'v1', 'a cat', ?, ?)`,
		source, time.Now().Unix(), time.Now().Unix())
	require.NoError(t, err)

	rec := env.get(t, "/data?tab=attachments")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	assert.Contains(t, body, `src="`+mediaThumbSrc(source)+`"`)
	assert.Contains(t, body, `>cdn.discordapp.com/attachments/1/2/image.png</a>`)
	assert.Contains(t, body, "image.png")
}

func TestDataMessagesRenderAttachmentPreview(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)
	messageJSON := `{"attachments":[{"url":"https://cdn.discordapp.com/attachments/1/2/pic.png?ex=1","proxy_url":"https://media.discordapp.net/attachments/1/2/pic.png?ex=1","content_type":"image/png","filename":"pic.png"}]}`
	env.insertDiscordMessage(t, dataTestGuild, "222", "333", "444", "user", "look", messageJSON, false)

	rec := env.get(t, "/data?tab=messages")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	assert.Contains(t, body, `src="`+mediaThumbSrc("https://media.discordapp.net/attachments/1/2/pic.png?ex=1")+`"`)
	assert.Contains(t, body, `class="thumb thumb-sm"`)
	assert.Contains(t, body, `title="pic.png"`)
}

func TestDataMediaUnsafeURLHasNoPreviewOrLink(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)
	env.seedMedia(t, dataTestGuild, "javascript:alert(1)", "gif", "unsafe preview", "unsafe")

	rec := env.get(t, "/data?tab=media")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.NotContains(t, body, `src="javascript:`)
	assert.NotContains(t, body, `href="javascript:`)
	assert.Contains(t, body, "unsafe URL")
	assert.Contains(t, body, "javascript:alert(1)")
}

func TestDataMediaSearchRendersThumbnail(t *testing.T) {
	env := newDataTestEnv(t, nil, true, &fakeDataEmbedder{})
	env.seedMedia(t, dataTestGuild, "https://example.com/cat.png", "image", "cat reaction", "when the cat")

	target := "/data?tab=media&search_guild=" + dataTestGuild + "&search_query=" + url.QueryEscape("cat")
	rec := env.get(t, target)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `src="`+mediaThumbSrc("https://example.com/cat.png")+`"`)
}
