package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func freshExpiryHex() string {
	return strconv.FormatInt(time.Now().Add(24*time.Hour).Unix(), 16)
}

func TestOpenGraphImagePrefersGif(t *testing.T) {
	page := []byte(`<html><head>
		<meta property="og:image" content="https://media0.giphy.com/media/abc/giphy.webp"/>
		<meta property="og:image" content="https://media0.giphy.com/media/abc/giphy.gif"/>
	</head></html>`)
	assert.Equal(t, "https://media0.giphy.com/media/abc/giphy.gif", openGraphImage(page))

	page = []byte(`<meta name="twitter:image" content="https://media1.tenor.com/m/abc/cat.png">`)
	assert.Equal(t, "https://media1.tenor.com/m/abc/cat.png", openGraphImage(page))

	page = []byte(`<meta property="og:image" content="http://127.0.0.1/secret.png">`)
	assert.Equal(t, "", openGraphImage(page))

	assert.Equal(t, "", openGraphImage([]byte(`<html></html>`)))
}

func TestMergeDiscordQueryKeepsExtras(t *testing.T) {
	refreshed := "ex=6b0709c0&is=6b05b840&hm=abc"
	original := "ex=1&is=1&hm=old&animated=true"
	merged, err := url.ParseQuery(mergeDiscordQuery(refreshed, original))
	require.NoError(t, err)
	assert.Equal(t, "6b0709c0", merged.Get("ex"))
	assert.Equal(t, "abc", merged.Get("hm"))
	assert.Equal(t, "true", merged.Get("animated"))
}

func TestMediaProxyRefreshesExpiredDiscordURLAndCaches(t *testing.T) {
	expiry := freshExpiryHex()
	var calls atomic.Int32
	discord := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "Bot test-token", r.Header.Get("Authorization"))
		var payload struct {
			URLs []string `json:"attachment_urls"`
		}
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		if !assert.Len(t, payload.URLs, 1) {
			return
		}
		assert.Equal(t, "https://cdn.discordapp.com/attachments/1/2/cat.gif?ex=1&is=2&hm=old", payload.URLs[0])
		_ = json.NewEncoder(w).Encode(map[string]any{
			"refreshed_urls": []map[string]string{{
				"original":  payload.URLs[0],
				"refreshed": "https://cdn.discordapp.com/attachments/1/2/cat.gif?ex=" + expiry + "&is=2&hm=new",
			}},
		})
	}))
	defer discord.Close()

	proxy := newMediaProxy("test-token")
	proxy.refreshURL = discord.URL

	target, err := proxy.resolve(context.Background(), "https://cdn.discordapp.com/attachments/1/2/cat.gif?ex=1&is=2&hm=old")
	require.NoError(t, err)
	assert.Contains(t, target, "ex="+expiry)
	assert.Contains(t, target, "hm=new")

	// The second resolve is served from memory, not another Discord call.
	_, err = proxy.resolve(context.Background(), "https://cdn.discordapp.com/attachments/1/2/cat.gif?ex=1&is=2&hm=old")
	require.NoError(t, err)
	assert.EqualValues(t, int32(1), calls.Load())
}

func TestMediaProxyKeepsFreshDiscordURL(t *testing.T) {
	proxy := newMediaProxy("")
	target, err := proxy.resolve(context.Background(),
		"https://cdn.discordapp.com/attachments/1/2/cat.gif?ex="+freshExpiryHex()+"&is=2&hm=ok")
	require.NoError(t, err)
	assert.Contains(t, target, "hm=ok")
}

func TestMediaProxyNeedsTokenForExpiredDiscordURL(t *testing.T) {
	proxy := newMediaProxy("")
	_, err := proxy.resolve(context.Background(), "https://cdn.discordapp.com/attachments/1/2/cat.gif?ex=1&is=2&hm=old")
	assert.ErrorIs(t, err, errMediaProxyNoToken)
}

func TestMediaProxyRejectsUnsupportedAndUnsafeURLs(t *testing.T) {
	proxy := newMediaProxy("token")
	_, err := proxy.resolve(context.Background(), "https://x.com/user/status/123")
	assert.ErrorIs(t, err, errUnsupportedMediaURL)
	_, err = proxy.resolve(context.Background(), "javascript:alert(1)")
	assert.ErrorIs(t, err, errInvalidMediaURL)
	// A direct image on the Discord CDN still passes through, signature or not.
	target, err := proxy.resolve(context.Background(), "https://cdn.discordapp.com/not-an-attachment/x.png")
	require.NoError(t, err)
	assert.Equal(t, "https://cdn.discordapp.com/not-an-attachment/x.png", target)
}

func TestMediaProxyResolvesKlipyAPI(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/alejandro9ix-3", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"file": map[string]any{
					"md": map[string]any{
						"gif": map[string]string{"url": "https://static.klipy.com/a.gif"},
					},
				},
			},
		})
	}))
	defer api.Close()

	proxy := newMediaProxy("token")
	proxy.klipyAPI = api.URL + "/"
	target, err := proxy.resolve(context.Background(), "https://klipy.com/gifs/alejandro9ix-3")
	require.NoError(t, err)
	assert.Equal(t, "https://static.klipy.com/a.gif", target)
}

func TestMediaThumbHandlerRedirectsAndValidates(t *testing.T) {
	env := newDataTestEnv(t, nil, false, nil)
	expiry := freshExpiryHex()
	discord := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"refreshed_urls": []map[string]string{{
				"refreshed": "https://cdn.discordapp.com/attachments/1/2/cat.gif?ex=" + expiry + "&is=2&hm=new",
			}},
		})
	}))
	defer discord.Close()
	env.srv.mediaProxy = newMediaProxy("test-token")
	env.srv.mediaProxy.refreshURL = discord.URL

	expired := url.QueryEscape("https://cdn.discordapp.com/attachments/1/2/cat.gif?ex=1&is=2&hm=old")
	rec := env.get(t, "/data/media/thumb?url="+expired)
	assert.Equal(t, http.StatusFound, rec.Code)
	assert.Contains(t, rec.Header().Get("Location"), "hm=new")

	rec = env.get(t, "/data/media/thumb")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec = env.get(t, "/data/media/thumb?url="+url.QueryEscape("https://x.com/user/status/1"))
	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "preview unavailable")
}
