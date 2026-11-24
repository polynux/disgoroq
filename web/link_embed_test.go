package web

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDataPaginationFirstLastAndJump(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)
	for _, id := range []string{"301", "302", "303"} {
		env.insertDiscordMessage(t, dataTestGuild, "222", id, "444", "user", "message "+id, "{}", false)
	}

	rec := env.get(t, "/data?tab=messages&limit=1")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "Page 1 of 3 · 1 per page")
	assert.Contains(t, body, "page=3") // Last
	assert.Contains(t, body, `name="tab" value="messages"`)
	assert.Contains(t, body, `name="limit" value="1"`)
	assert.Contains(t, body, `max="3"`)

	rec = env.get(t, "/data?tab=messages&limit=1&page=2")
	require.Equal(t, http.StatusOK, rec.Code)
	body = rec.Body.String()
	assert.Contains(t, body, "Page 2 of 3 · 1 per page")
	assert.Contains(t, body, "page=1") // First and previous
	assert.Contains(t, body, "page=3") // Last and next

	rec = env.get(t, "/data?tab=messages&limit=1&page=99")
	require.Equal(t, http.StatusOK, rec.Code)
	body = rec.Body.String()
	assert.Contains(t, body, "Page 3 of 3 · 1 per page")
	assert.NotContains(t, body, "page=99")
}

func TestDataPaginationLastDisabledOnFinalPage(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)
	env.insertDiscordMessage(t, dataTestGuild, "222", "301", "444", "user", "only", "{}", false)

	rec := env.get(t, "/data?tab=messages")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "Page 1 of 1 · 50 per page")
	assert.Contains(t, body, `aria-disabled="true">Last`)
	assert.Contains(t, body, `aria-disabled="true">Next`)
}

func TestParseLinkPreview(t *testing.T) {
	base, err := url.Parse("https://example.com/posts/1")
	require.NoError(t, err)
	page := []byte(`<html><head>
		<title>fallback title</title>
		<meta property="og:title" content="Card title">
		<meta property="og:description" content="Card description">
		<meta property="og:site_name" content="Example">
		<meta property="og:image" content="/media/card.png">
	</head></html>`)
	preview := parseLinkPreview(page, base)
	assert.Equal(t, "Card title", preview.Title)
	assert.Equal(t, "Card description", preview.Description)
	assert.Equal(t, "Example", preview.Site)
	assert.Equal(t, "https://example.com/media/card.png", preview.Image)

	// XHTML-style self-closing meta tags (as served by x.com) are read too.
	page = []byte(`<html><head>
		<meta property="og:title" content="Self closing"/>
		<meta property="og:description" content="Still parsed"/>
		<meta property="og:image" content="https://example.com/card.png"/>
	</head></html>`)
	preview = parseLinkPreview(page, base)
	assert.Equal(t, "Self closing", preview.Title)
	assert.Equal(t, "Still parsed", preview.Description)
	assert.Equal(t, "https://example.com/card.png", preview.Image)

	// A private image URL is not turned into a card image.
	page = []byte(`<meta property="og:image" content="http://127.0.0.1/secret.png">`)
	assert.Equal(t, "", parseLinkPreview(page, base).Image)

	page = []byte(`<title>Only a title</title>`)
	assert.Equal(t, "Only a title", parseLinkPreview(page, base).Title)
}

func TestIsPublicIP(t *testing.T) {
	assert.False(t, isPublicIP(net.ParseIP("127.0.0.1")))
	assert.False(t, isPublicIP(net.ParseIP("10.1.2.3")))
	assert.False(t, isPublicIP(net.ParseIP("192.168.1.10")))
	assert.False(t, isPublicIP(net.ParseIP("100.64.0.1")))
	assert.False(t, isPublicIP(net.ParseIP("169.254.1.1")))
	assert.True(t, isPublicIP(net.ParseIP("8.8.8.8")))
	assert.True(t, isPublicIP(net.ParseIP("1.1.1.1")))
}

func TestMediaEmbedRendersOpenGraphCard(t *testing.T) {
	var calls atomic.Int32
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head>
			<meta property="og:title" content="GTA VI Countdown">
			<meta property="og:description" content="Countdowns and news">
			<meta property="og:site_name" content="X (formerly Twitter)">
			<meta property="og:image" content="https://pbs.twimg.com/media/HE0GmyraAAArXEX?format=webp&amp;name=large">
		</head></html>`))
	}))
	defer page.Close()

	env := newDataTestEnv(t, nil, false, nil)
	env.srv.linkPreviewer = newLinkPreviewer()
	env.srv.linkPreviewer.client = page.Client()

	target := "/data/media/embed?url=" + url.QueryEscape(page.URL+"/GTAVI_Countdown")
	rec := env.get(t, target)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "GTA VI Countdown")
	assert.Contains(t, body, "Countdowns and news")
	assert.Contains(t, body, "X (formerly Twitter)")
	assert.Contains(t, body, `src="https://pbs.twimg.com/media/HE0GmyraAAArXEX?format=webp&amp;name=large"`)

	// The second request is served from the preview cache.
	rec = env.get(t, target)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.EqualValues(t, int32(1), calls.Load())
}

func TestMediaEmbedFallsBackWithoutMetadata(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusNotFound)
	}))
	defer page.Close()

	env := newDataTestEnv(t, nil, false, nil)
	env.srv.linkPreviewer = newLinkPreviewer()
	env.srv.linkPreviewer.client = page.Client()

	rec := env.get(t, "/data/media/embed?url="+url.QueryEscape(page.URL+"/missing"))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "127.0.0.1")
}
