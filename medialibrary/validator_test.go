package medialibrary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/disgo/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"polynux/disgoroq/ai"
	"polynux/disgoroq/logger"
)

func captureAllMediaLogs(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, logs := observer.New(zap.DebugLevel)
	prev := logger.Log
	logger.Log = zap.New(core)
	t.Cleanup(func() {
		logger.Log = prev
	})
	return logs
}

func newTestDiscordRestClient(t *testing.T, server *httptest.Server, token string, slogOut *bytes.Buffer) rest.Client {
	t.Helper()
	opts := []rest.ClientConfigOpt{
		rest.WithURL(server.URL),
		rest.WithHTTPClient(server.Client()),
		rest.WithRateLimiter(rest.NewNoopRateLimiter()),
		rest.WithUserAgent("DiscordBot (https://github.com/disgoorg/disgo, test)"),
	}
	if slogOut != nil {
		opts = append(opts, rest.WithLogger(slog.New(slog.NewTextHandler(slogOut, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	}
	return rest.NewClient(token, opts...)
}

func TestDiscordCDNRefreshAndValidation(t *testing.T) {
	ctx := context.Background()
	const botToken = "test-bot-token-secret"

	t.Run("refreshes Discord CDN attachment URL and posts signed URL while keeping canonical URL in DB", func(t *testing.T) {
		_, repo := setupMediaTestDB(t)
		canonicalCDN := "https://cdn.discordapp.com/attachments/111/222/funny-cat.gif"
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: canonicalCDN, Kind: "gif",
			Description: "funny cat", Uses: 5, FirstSeen: 100, LastSeen: 200,
		}))

		var headCalls atomic.Int32
		cdnServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodHead, r.Method)
			assert.Equal(t, "abc", r.URL.Query().Get("ex"))
			assert.Equal(t, "def", r.URL.Query().Get("is"))
			assert.Equal(t, "sig123", r.URL.Query().Get("hm"))
			headCalls.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		defer cdnServer.Close()

		refreshedURL := cdnServer.URL + "/attachments/111/222/funny-cat.gif?ex=abc&is=def&hm=sig123"
		var refreshCalls atomic.Int32
		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			refreshCalls.Add(1)
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/attachments/refresh-urls", r.URL.Path)
			assert.Equal(t, "Bot "+botToken, r.Header.Get("Authorization"))
			assert.Contains(t, r.Header.Get("User-Agent"), "DiscordBot")

			var reqBody refreshAttachmentURLsRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&reqBody))
			assert.Equal(t, []string{canonicalCDN}, reqBody.AttachmentURLs)

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(refreshAttachmentURLsResponse{
				RefreshedURLs: []refreshedAttachmentURL{
					{Original: canonicalCDN, Refreshed: refreshedURL},
				},
			})
		}))
		defer apiServer.Close()

		cfg := defaultEnabledConfig()
		cfg.AllowDiscordCDN = true
		cfg.ValidateLinks = true
		svc := NewService(repo, newFakeEmbedder(), cfg)
		defer svc.Close()
		svc.SetDiscordRestClient(newTestDiscordRestClient(t, apiServer, botToken, nil))
		svc.SetHTTPClient(cdnServer.Client())

		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "mdr [gif: funny cat]",
		})
		require.True(t, ok)
		assert.Equal(t, refreshedURL, res.URL)
		assert.Equal(t, int32(1), refreshCalls.Load())
		assert.Equal(t, int32(1), headCalls.Load())

		// Canonical URL remains stored in DB (never overwritten with signed URL).
		stored, err := repo.GetEntry(ctx, canonicalCDN, "g1")
		require.NoError(t, err)
		require.NotNil(t, stored)
		assert.Equal(t, canonicalCDN, stored.URL)
		signedRow, err := repo.GetEntry(ctx, refreshedURL, "g1")
		require.NoError(t, err)
		assert.Nil(t, signedRow)
	})

	t.Run("accepts sole refreshed_urls[0].refreshed regardless of original normalization", func(t *testing.T) {
		_, repo := setupMediaTestDB(t)
		canonicalCDN := "https://cdn.discordapp.com/attachments/111/222/norm.gif"
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: canonicalCDN, Kind: "gif",
			Description: "norm", Uses: 5, FirstSeen: 100, LastSeen: 200,
		}))

		cdnServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer cdnServer.Close()

		refreshedURL := cdnServer.URL + "/attachments/111/222/norm.gif?ex=9&is=8&hm=7"
		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(refreshAttachmentURLsResponse{
				RefreshedURLs: []refreshedAttachmentURL{
					// Normalized differently (scheme, case, port, extra query): must still accept.
					{Original: "HTTP://CDN.DISCORDAPP.COM:443/attachments/111/222/norm.gif?old=1", Refreshed: refreshedURL},
				},
			})
		}))
		defer apiServer.Close()

		cfg := defaultEnabledConfig()
		cfg.AllowDiscordCDN = true
		cfg.ValidateLinks = true
		svc := NewService(repo, newFakeEmbedder(), cfg)
		defer svc.Close()
		svc.SetDiscordRestClient(newTestDiscordRestClient(t, apiServer, botToken, nil))
		svc.SetHTTPClient(cdnServer.Client())

		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: norm]",
		})
		require.True(t, ok)
		assert.Equal(t, refreshedURL, res.URL)
	})

	t.Run("refreshes media.discordapp.net attachment even when validate_links is false without HEAD check", func(t *testing.T) {
		_, repo := setupMediaTestDB(t)
		canonicalCDN := "https://media.discordapp.net/attachments/111/333/dance.gif"
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: canonicalCDN, Kind: "gif",
			Description: "dance", Uses: 5, FirstSeen: 100, LastSeen: 200,
		}))

		var headCalls atomic.Int32
		cdnServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			headCalls.Add(1)
			w.WriteHeader(http.StatusNotFound)
		}))
		defer cdnServer.Close()

		refreshedURL := cdnServer.URL + "/attachments/111/333/dance.gif?ex=1&is=2&hm=3"
		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(refreshAttachmentURLsResponse{
				RefreshedURLs: []refreshedAttachmentURL{
					{Original: canonicalCDN, Refreshed: refreshedURL},
				},
			})
		}))
		defer apiServer.Close()

		cfg := defaultEnabledConfig()
		cfg.AllowDiscordCDN = true
		cfg.ValidateLinks = false
		svc := NewService(repo, newFakeEmbedder(), cfg)
		defer svc.Close()
		svc.SetDiscordRestClient(newTestDiscordRestClient(t, apiServer, botToken, nil))
		svc.SetHTTPClient(cdnServer.Client())

		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: dance]",
		})
		require.True(t, ok)
		assert.Equal(t, refreshedURL, res.URL)
		assert.Equal(t, int32(0), headCalls.Load(), "HEAD check must not run when validate_links is false")
	})

	t.Run("refresh OK + HEAD 404 + GET 200 keeps row and posts refreshed URL", func(t *testing.T) {
		_, repo := setupMediaTestDB(t)
		logs := captureAllMediaLogs(t)
		canonicalCDN := "https://cdn.discordapp.com/attachments/1/2/head-quirk.gif"
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: canonicalCDN, Kind: "gif",
			Description: "quirk", Uses: 10, FirstSeen: 100, LastSeen: 200,
		}))

		var mu sync.Mutex
		var methods []string
		cdnServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			methods = append(methods, r.Method+" range="+r.Header.Get("Range"))
			mu.Unlock()
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer cdnServer.Close()

		refreshedURL := cdnServer.URL + "/attachments/1/2/head-quirk.gif?ex=1&is=2&hm=3"
		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(refreshAttachmentURLsResponse{
				RefreshedURLs: []refreshedAttachmentURL{
					{Original: canonicalCDN, Refreshed: refreshedURL},
				},
			})
		}))
		defer apiServer.Close()

		cfg := defaultEnabledConfig()
		cfg.AllowDiscordCDN = true
		cfg.ValidateLinks = true
		svc := NewService(repo, newFakeEmbedder(), cfg)
		svc.SetDiscordRestClient(newTestDiscordRestClient(t, apiServer, botToken, nil))
		svc.SetHTTPClient(cdnServer.Client())

		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: quirk]",
		})
		svc.Close()
		require.True(t, ok)
		assert.Equal(t, refreshedURL, res.URL)

		row, err := repo.GetEntry(ctx, canonicalCDN, "g1")
		require.NoError(t, err)
		require.NotNil(t, row, "row must be kept when GET confirms 200 after HEAD 404")
		assert.Empty(t, logs.FilterMessage("media library dead link removed").All())

		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, []string{"HEAD range=", "GET range=bytes=0-0"}, methods)
	})

	t.Run("refresh OK + HEAD 404 + GET 404 deletes canonical row asynchronously and posts next candidate", func(t *testing.T) {
		_, repo := setupMediaTestDB(t)
		logs := captureAllMediaLogs(t)

		deadCDN := "https://cdn.discordapp.com/attachments/1/2/deleted.gif"
		var mu sync.Mutex
		var deadChecks []string
		cdnServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/attachments/1/2/deleted.gif":
				mu.Lock()
				deadChecks = append(deadChecks, r.Method+" range="+r.Header.Get("Range"))
				mu.Unlock()
				w.WriteHeader(http.StatusNotFound)
			case "/alive.gif":
				w.WriteHeader(http.StatusOK)
			default:
				w.WriteHeader(http.StatusInternalServerError)
			}
		}))
		defer cdnServer.Close()
		aliveURL := cdnServer.URL + "/alive.gif"

		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: deadCDN, Kind: "gif",
			Description: "cat", Uses: 10, FirstSeen: 100, LastSeen: 200,
		}))
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: aliveURL, Kind: "gif",
			Description: "cat", Uses: 2, FirstSeen: 100, LastSeen: 200,
		}))

		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(refreshAttachmentURLsResponse{
				RefreshedURLs: []refreshedAttachmentURL{
					{Original: deadCDN, Refreshed: cdnServer.URL + "/attachments/1/2/deleted.gif?ex=1&is=2&hm=3"},
				},
			})
		}))
		defer apiServer.Close()

		cfg := defaultEnabledConfig()
		cfg.AllowDiscordCDN = true
		cfg.ValidateLinks = true
		svc := NewService(repo, newFakeEmbedder(), cfg)
		svc.SetDiscordRestClient(newTestDiscordRestClient(t, apiServer, botToken, nil))
		svc.SetHTTPClient(cdnServer.Client())

		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: cat]",
		})
		require.True(t, ok)
		assert.Equal(t, aliveURL, res.URL)

		// Drain async worker so the queued delete completes cleanly before inspecting DB.
		svc.Close()

		row, err := repo.GetEntry(ctx, deadCDN, "g1")
		require.NoError(t, err)
		assert.Nil(t, row, "confirmed dead canonical CDN row must be deleted asynchronously")

		mu.Lock()
		assert.Equal(t, []string{"HEAD range=", "GET range=bytes=0-0"}, deadChecks)
		mu.Unlock()

		removed := logs.FilterMessage("media library dead link removed").All()
		require.Len(t, removed, 1)
		assert.Equal(t, deadCDN, removed[0].ContextMap()["url"])
		assert.Equal(t, "g1", removed[0].ContextMap()["guild_id"])
		assert.EqualValues(t, 404, removed[0].ContextMap()["status"])
	})

	t.Run("empty refresh list or blank refreshed URL is unknown: keeps row and posts canonical URL", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			resp refreshAttachmentURLsResponse
		}{
			{
				name: "empty list",
				resp: refreshAttachmentURLsResponse{RefreshedURLs: nil},
			},
			{
				name: "blank refreshed URL",
				resp: refreshAttachmentURLsResponse{
					RefreshedURLs: []refreshedAttachmentURL{{Original: "https://cdn.discordapp.com/attachments/1/2/keep.gif", Refreshed: "   "}},
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, repo := setupMediaTestDB(t)
				logs := captureAllMediaLogs(t)

				cdnURL := "https://cdn.discordapp.com/attachments/1/2/keep.gif"
				var headCalls atomic.Int32
				mediaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					headCalls.Add(1)
					w.WriteHeader(http.StatusNotFound)
				}))
				defer mediaServer.Close()

				require.NoError(t, repo.UpsertImport(ctx, Entry{
					GuildID: "g1", URL: cdnURL, Kind: "gif",
					Description: "cat", Uses: 10, FirstSeen: 100, LastSeen: 200,
				}))

				apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(tc.resp)
				}))
				defer apiServer.Close()

				cfg := defaultEnabledConfig()
				cfg.AllowDiscordCDN = true
				cfg.ValidateLinks = true
				svc := NewService(repo, newFakeEmbedder(), cfg)
				svc.SetDiscordRestClient(newTestDiscordRestClient(t, apiServer, botToken, nil))
				svc.SetHTTPClient(mediaServer.Client())

				res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
					GuildID:  "g1",
					RawReply: "[gif: cat]",
				})
				svc.Close()
				require.True(t, ok)
				assert.Equal(t, cdnURL, res.URL)
				assert.Equal(t, int32(0), headCalls.Load(), "must not HEAD unrefreshed attachment URL")

				row, err := repo.GetEntry(ctx, cdnURL, "g1")
				require.NoError(t, err)
				require.NotNil(t, row, "empty refresh response must never delete the row")
				assert.Empty(t, logs.FilterMessage("media library dead link removed").All())
				assert.NotEmpty(t, logs.FilterLevelExact(zap.DebugLevel).All())
			})
		}
	})

	t.Run("refresh failure (5xx/429/timeout) never deletes row and posts URL anyway", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			handler http.HandlerFunc
		}{
			{
				name: "500 internal server error",
				handler: func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				},
			},
			{
				name: "429 rate limited",
				handler: func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusTooManyRequests)
				},
			},
			{
				name: "timeout",
				handler: func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					select {
					case <-time.After(2 * time.Second):
					case <-r.Context().Done():
					}
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, repo := setupMediaTestDB(t)
				logs := captureAllMediaLogs(t)
				cdnURL := "https://cdn.discordapp.com/attachments/1/2/keep.gif"
				require.NoError(t, repo.UpsertImport(ctx, Entry{
					GuildID: "g1", URL: cdnURL, Kind: "gif",
					Description: "keep", Uses: 5, FirstSeen: 100, LastSeen: 200,
				}))

				var headCalls atomic.Int32
				mediaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					headCalls.Add(1)
					w.WriteHeader(http.StatusNotFound)
				}))
				defer mediaServer.Close()

				apiServer := httptest.NewServer(tc.handler)
				defer apiServer.Close()

				cfg := defaultEnabledConfig()
				cfg.AllowDiscordCDN = true
				cfg.ValidateLinks = true
				cfg.LookupTimeoutMs = 300
				svc := NewService(repo, newFakeEmbedder(), cfg)
				svc.checkTimeout = 100 * time.Millisecond
				svc.SetDiscordRestClient(newTestDiscordRestClient(t, apiServer, botToken, nil))
				svc.SetHTTPClient(mediaServer.Client())

				start := time.Now()
				res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
					GuildID:  "g1",
					RawReply: "[gif: keep]",
				})
				elapsed := time.Since(start)
				require.True(t, ok)
				assert.Equal(t, cdnURL, res.URL)
				assert.Less(t, elapsed, 500*time.Millisecond)
				assert.Equal(t, int32(0), headCalls.Load(), "must not HEAD unsigned CDN URL after refresh failure")

				row, err := repo.GetEntry(ctx, cdnURL, "g1")
				require.NoError(t, err)
				require.NotNil(t, row, "row must not be deleted on refresh failure")

				assert.Empty(t, logs.FilterMessage("media library dead link removed").All())
				assert.NotEmpty(t, logs.FilterLevelExact(zap.DebugLevel).All())
			})
		}
	})
}

func TestLinkValidationDeadAndFallback(t *testing.T) {
	ctx := context.Background()

	t.Run("404 confirmed by GET deletes only matching guild row asynchronously and selects next candidate", func(t *testing.T) {
		_, repo := setupMediaTestDB(t)
		logs := captureAllMediaLogs(t)

		var mu sync.Mutex
		var deadMethods []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/dead-404.gif":
				mu.Lock()
				deadMethods = append(deadMethods, r.Method+" range="+r.Header.Get("Range"))
				mu.Unlock()
				w.WriteHeader(http.StatusNotFound)
			case "/alive.gif":
				w.WriteHeader(http.StatusOK)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer server.Close()

		deadURL := server.URL + "/dead-404.gif"
		aliveURL := server.URL + "/alive.gif"

		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: deadURL, Kind: "gif",
			Description: "cat", Uses: 10, FirstSeen: 100, LastSeen: 200,
		}))
		// Same URL in another guild must remain untouched.
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g2", URL: deadURL, Kind: "gif",
			Description: "cat", Uses: 10, FirstSeen: 100, LastSeen: 200,
		}))
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: aliveURL, Kind: "gif",
			Description: "cat", Uses: 3, FirstSeen: 100, LastSeen: 200,
		}))

		cfg := defaultEnabledConfig()
		cfg.ValidateLinks = true
		svc := NewService(repo, newFakeEmbedder(), cfg)
		svc.SetHTTPClient(server.Client())

		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: cat]",
		})
		require.True(t, ok)
		assert.Equal(t, aliveURL, res.URL)
		svc.Close()

		rowG1, err := repo.GetEntry(ctx, deadURL, "g1")
		require.NoError(t, err)
		assert.Nil(t, rowG1)

		rowG2, err := repo.GetEntry(ctx, deadURL, "g2")
		require.NoError(t, err)
		require.NotNil(t, rowG2, "other guild's row must not be deleted")

		mu.Lock()
		assert.Equal(t, []string{"HEAD range=", "GET range=bytes=0-0"}, deadMethods)
		mu.Unlock()

		removed := logs.FilterMessage("media library dead link removed").All()
		require.Len(t, removed, 1)
		assert.Equal(t, deadURL, removed[0].ContextMap()["url"])
		assert.Equal(t, "g1", removed[0].ContextMap()["guild_id"])
		assert.EqualValues(t, 404, removed[0].ContextMap()["status"])
	})

	t.Run("410 Gone and redirect to 404 delete rows and follow redirects to 200", func(t *testing.T) {
		_, repo := setupMediaTestDB(t)
		logs := captureAllMediaLogs(t)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/gone.gif":
				w.WriteHeader(http.StatusGone)
			case "/redir-dead.gif":
				http.Redirect(w, r, "/final-404.gif", http.StatusFound)
			case "/final-404.gif":
				w.WriteHeader(http.StatusNotFound)
			case "/redir-alive.gif":
				http.Redirect(w, r, "/final-200.gif", http.StatusMovedPermanently)
			case "/final-200.gif":
				w.WriteHeader(http.StatusOK)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer server.Close()

		goneURL := server.URL + "/gone.gif"
		redirDeadURL := server.URL + "/redir-dead.gif"
		redirAliveURL := server.URL + "/redir-alive.gif"

		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: goneURL, Kind: "gif", Description: "dog", Uses: 10, FirstSeen: 1, LastSeen: 2,
		}))
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: redirDeadURL, Kind: "gif", Description: "dog", Uses: 8, FirstSeen: 1, LastSeen: 2,
		}))
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: redirAliveURL, Kind: "gif", Description: "dog", Uses: 5, FirstSeen: 1, LastSeen: 2,
		}))

		cfg := defaultEnabledConfig()
		cfg.ValidateLinks = true
		svc := NewService(repo, newFakeEmbedder(), cfg)
		svc.SetHTTPClient(server.Client())

		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: dog]",
		})
		require.True(t, ok)
		assert.Equal(t, redirAliveURL, res.URL)
		svc.Close()

		rowGone, err := repo.GetEntry(ctx, goneURL, "g1")
		require.NoError(t, err)
		assert.Nil(t, rowGone)
		rowRedirDead, err := repo.GetEntry(ctx, redirDeadURL, "g1")
		require.NoError(t, err)
		assert.Nil(t, rowRedirDead)

		removed := logs.FilterMessage("media library dead link removed").All()
		require.Len(t, removed, 2)
		assert.EqualValues(t, 410, removed[0].ContextMap()["status"])
		assert.EqualValues(t, 404, removed[1].ContextMap()["status"])
	})

	t.Run("HEAD 405 and 501 fall back to GET with Range bytes=0-0", func(t *testing.T) {
		_, repo := setupMediaTestDB(t)

		var mu sync.Mutex
		var seen []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen = append(seen, fmt.Sprintf("%s %s range=%s", r.Method, r.URL.Path, r.Header.Get("Range")))
			mu.Unlock()
			switch r.URL.Path {
			case "/not-impl-dead.gif":
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusNotImplemented)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			case "/no-head-alive.gif":
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				w.Header().Set("Content-Range", "bytes 0-0/10")
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write([]byte("G"))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer server.Close()

		dead501URL := server.URL + "/not-impl-dead.gif"
		alive405URL := server.URL + "/no-head-alive.gif"

		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: dead501URL, Kind: "gif", Description: "party", Uses: 10, FirstSeen: 1, LastSeen: 2,
		}))
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: alive405URL, Kind: "gif", Description: "party", Uses: 5, FirstSeen: 1, LastSeen: 2,
		}))

		cfg := defaultEnabledConfig()
		cfg.ValidateLinks = true
		svc := NewService(repo, newFakeEmbedder(), cfg)
		svc.SetHTTPClient(server.Client())

		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: party]",
		})
		require.True(t, ok)
		assert.Equal(t, alive405URL, res.URL)
		svc.Close()

		row501, err := repo.GetEntry(ctx, dead501URL, "g1")
		require.NoError(t, err)
		assert.Nil(t, row501)
		row405, err := repo.GetEntry(ctx, alive405URL, "g1")
		require.NoError(t, err)
		require.NotNil(t, row405)

		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, []string{
			"HEAD /not-impl-dead.gif range=",
			"GET /not-impl-dead.gif range=bytes=0-0",
			"HEAD /no-head-alive.gif range=",
			"GET /no-head-alive.gif range=bytes=0-0",
		}, seen)
	})

	t.Run("stops after 3 dead candidates then runs validated random gif fallback", func(t *testing.T) {
		_, repo := setupMediaTestDB(t)

		var checkedMu sync.Mutex
		var checkedHeadPaths []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead {
				checkedMu.Lock()
				checkedHeadPaths = append(checkedHeadPaths, r.URL.Path)
				checkedMu.Unlock()
			}
			switch r.URL.Path {
			case "/dead1.gif", "/dead2.gif", "/dead3.gif", "/fallback-dead.gif":
				w.WriteHeader(http.StatusNotFound)
			case "/cand4-alive.gif", "/fallback-alive.gif":
				w.WriteHeader(http.StatusOK)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer server.Close()

		// 4 exact matches for "cat": top 3 are dead, 4th is alive (must NOT be tried because cap is 3).
		for i, item := range []struct {
			path string
			uses int64
		}{
			{"/dead1.gif", 40},
			{"/dead2.gif", 30},
			{"/dead3.gif", 20},
			{"/cand4-alive.gif", 10},
		} {
			require.NoError(t, repo.UpsertImport(ctx, Entry{
				GuildID: "g1", URL: server.URL + item.path, Kind: "gif",
				Description: "cat", Uses: item.uses, FirstSeen: int64(i + 1), LastSeen: int64(i + 10),
			}))
		}
		// Random fallback candidates (uses >= 2 preferred): one dead, one alive.
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: server.URL + "/fallback-dead.gif", Kind: "gif",
			Description: "other", Uses: 5, FirstSeen: 1, LastSeen: 2,
		}))
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: server.URL + "/fallback-alive.gif", Kind: "gif",
			Description: "other2", Uses: 5, FirstSeen: 1, LastSeen: 2,
		}))

		cfg := defaultEnabledConfig()
		cfg.ValidateLinks = true
		cfg.RandomGifFallback = true
		svc := NewService(repo, newFakeEmbedder(), cfg)
		svc.SetHTTPClient(server.Client())

		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: cat]",
		})
		require.True(t, ok)
		svc.Close()
		assert.Contains(t, []string{server.URL + "/cand4-alive.gif", server.URL + "/fallback-alive.gif"}, res.URL)

		checkedMu.Lock()
		require.GreaterOrEqual(t, len(checkedHeadPaths), 4)
		assert.Equal(t, []string{"/dead1.gif", "/dead2.gif", "/dead3.gif"}, checkedHeadPaths[:3])
		checkedMu.Unlock()

		for _, deadPath := range []string{"/dead1.gif", "/dead2.gif", "/dead3.gif"} {
			row, err := repo.GetEntry(ctx, server.URL+deadPath, "g1")
			require.NoError(t, err)
			assert.Nil(t, row)
		}

		// With RandomGifFallback disabled, 3 dead exact candidates stop before the 4th and return no match.
		_, repoNoFB := setupMediaTestDB(t)
		var headCountNoFB atomic.Int32
		serverNoFB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead {
				headCountNoFB.Add(1)
			}
			if r.URL.Path == "/cand4-alive.gif" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer serverNoFB.Close()

		for _, item := range []struct {
			path string
			uses int64
		}{
			{"/dead1.gif", 40},
			{"/dead2.gif", 30},
			{"/dead3.gif", 20},
			{"/cand4-alive.gif", 10},
		} {
			require.NoError(t, repoNoFB.UpsertImport(ctx, Entry{
				GuildID: "g1", URL: serverNoFB.URL + item.path, Kind: "gif",
				Description: "cat", Uses: item.uses, FirstSeen: 1, LastSeen: 2,
			}))
		}
		cfgNoFB := defaultEnabledConfig()
		cfgNoFB.ValidateLinks = true
		cfgNoFB.RandomGifFallback = false
		svcNoFB := NewService(repoNoFB, newFakeEmbedder(), cfgNoFB)
		svcNoFB.SetHTTPClient(serverNoFB.Client())

		_, ok = svcNoFB.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: cat]",
		})
		svcNoFB.Close()
		assert.False(t, ok)
		assert.Equal(t, int32(3), headCountNoFB.Load(), "must check at most 3 candidates per lookup")
	})

	t.Run("trigger match, gif-on-request, and cross-guild validate and delete owning guild row", func(t *testing.T) {
		_, repo := setupMediaTestDB(t)
		embedder := newFakeEmbedder()
		embedder.set("envoie un gif drôle", unitVec2D(1, 0))

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/trig-dead.gif", "/cross-dead.gif":
				w.WriteHeader(http.StatusNotFound)
			case "/trig-alive.gif", "/cross-alive.gif":
				w.WriteHeader(http.StatusOK)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer server.Close()

		trigDead := server.URL + "/trig-dead.gif"
		trigAlive := server.URL + "/trig-alive.gif"
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: trigDead, Kind: "gif",
			Uses: 5, FirstSeen: 1, LastSeen: 2,
			TriggerEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.98)),
		}))
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: trigAlive, Kind: "gif",
			Uses: 2, FirstSeen: 1, LastSeen: 2,
			TriggerEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.90)),
		}))

		crossDead := server.URL + "/cross-dead.gif"
		crossAlive := server.URL + "/cross-alive.gif"
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g2", URL: crossDead, Kind: "gif",
			Description: "shared", Uses: 10, FirstSeen: 1, LastSeen: 2,
		}))
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g3", URL: crossAlive, Kind: "gif",
			Description: "shared", Uses: 5, FirstSeen: 1, LastSeen: 2,
		}))

		cfg := defaultEnabledConfig()
		cfg.ValidateLinks = true
		cfg.GifOnRequest = true
		cfg.CrossGuild = true
		svc := NewService(repo, embedder, cfg)
		svc.SetHTTPClient(server.Client())

		// gif-on-request validates trigger hits and advances past 404.
		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:         "g1",
			RawReply:        "tiens frérot",
			TargetMessageID: "m1",
			TargetText:      "envoie un gif drôle",
		})
		require.True(t, ok)
		assert.Equal(t, trigAlive, res.URL)

		// Cross-guild dead entry deletes row in its owning guild (g2) and picks g3's alive entry.
		res, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: shared]",
		})
		require.True(t, ok)
		assert.Equal(t, crossAlive, res.URL)
		svc.Close()

		row, err := repo.GetEntry(ctx, trigDead, "g1")
		require.NoError(t, err)
		assert.Nil(t, row)
		rowCross, err := repo.GetEntry(ctx, crossDead, "g2")
		require.NoError(t, err)
		assert.Nil(t, rowCross, "cross-guild dead row must be deleted from owning guild g2")
	})

	t.Run("vector description match skips dead top hit and picks next hit above threshold", func(t *testing.T) {
		_, repo := setupMediaTestDB(t)
		embedder := newFakeEmbedder()
		embedder.set("happy kitten", unitVec2D(1, 0))

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/vec-dead.gif" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		vecDead := server.URL + "/vec-dead.gif"
		vecAlive := server.URL + "/vec-alive.gif"
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: vecDead, Kind: "gif",
			Description: "joyful cat", Uses: 5, FirstSeen: 1, LastSeen: 2,
			DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.95)),
		}))
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: vecAlive, Kind: "gif",
			Description: "smiling feline", Uses: 3, FirstSeen: 1, LastSeen: 2,
			DescriptionEmbedding: mustBlob(t, vecWithSimilarityToXAxis(0.86)),
		}))

		cfg := defaultEnabledConfig()
		cfg.ValidateLinks = true
		svc := NewService(repo, embedder, cfg)
		svc.SetHTTPClient(server.Client())

		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: happy kitten]",
		})
		require.True(t, ok)
		assert.Equal(t, vecAlive, res.URL)
		assert.InDelta(t, 0.86, res.Similarity, 0.01)
		svc.Close()

		row, err := repo.GetEntry(ctx, vecDead, "g1")
		require.NoError(t, err)
		assert.Nil(t, row)
	})
}

func TestLinkValidationUnknownErrorsKeepAndPost(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "500 server error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
		},
		{
			name: "503 service unavailable",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			},
		},
		{
			name: "429 too many requests",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
			},
		},
		{
			name: "timeout",
			handler: func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-time.After(2 * time.Second):
				case <-r.Context().Done():
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, repo := setupMediaTestDB(t)
			logs := captureAllMediaLogs(t)

			server := httptest.NewServer(tc.handler)
			defer server.Close()
			targetURL := server.URL + "/flaky.gif"

			require.NoError(t, repo.UpsertImport(ctx, Entry{
				GuildID: "g1", URL: targetURL, Kind: "gif",
				Description: "flaky", Uses: 5, FirstSeen: 1, LastSeen: 2,
			}))

			cfg := defaultEnabledConfig()
			cfg.ValidateLinks = true
			cfg.LookupTimeoutMs = 1000
			svc := NewService(repo, newFakeEmbedder(), cfg)
			svc.checkTimeout = 100 * time.Millisecond
			svc.SetHTTPClient(server.Client())

			res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
				GuildID:  "g1",
				RawReply: "[gif: flaky]",
			})
			require.True(t, ok)
			assert.Equal(t, targetURL, res.URL)

			row, err := repo.GetEntry(ctx, targetURL, "g1")
			require.NoError(t, err)
			require.NotNil(t, row, "row must not be deleted on unknown/transient error")
			assert.Empty(t, logs.FilterMessage("media library dead link removed").All())
			assert.NotEmpty(t, logs.FilterLevelExact(zap.DebugLevel).All())
		})
	}

	t.Run("network error never deletes and still posts", func(t *testing.T) {
		_, repo := setupMediaTestDB(t)
		logs := captureAllMediaLogs(t)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		closedURL := server.URL + "/closed.gif"
		client := server.Client()
		server.Close()

		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: closedURL, Kind: "gif",
			Description: "closed", Uses: 5, FirstSeen: 1, LastSeen: 2,
		}))

		cfg := defaultEnabledConfig()
		cfg.ValidateLinks = true
		svc := NewService(repo, newFakeEmbedder(), cfg)
		svc.SetHTTPClient(client)

		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: closed]",
		})
		require.True(t, ok)
		assert.Equal(t, closedURL, res.URL)

		row, err := repo.GetEntry(ctx, closedURL, "g1")
		require.NoError(t, err)
		require.NotNil(t, row)
		assert.NotEmpty(t, logs.FilterLevelExact(zap.DebugLevel).All())
	})
}

func TestLinkValidationDisabledMakesNoHTTPCalls(t *testing.T) {
	ctx := context.Background()
	_, repo := setupMediaTestDB(t)

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	targetURL := server.URL + "/cat.gif"
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID: "g1", URL: targetURL, Kind: "gif",
		Description: "cat", Uses: 5, FirstSeen: 1, LastSeen: 2,
	}))

	cfg := defaultEnabledConfig()
	cfg.ValidateLinks = false
	cfg.AllowDiscordCDN = false
	svc := NewService(repo, newFakeEmbedder(), cfg)
	svc.SetDiscordRestClient(newTestDiscordRestClient(t, server, "unused-token", nil))
	svc.SetHTTPClient(server.Client())

	res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:  "g1",
		RawReply: "[gif: cat]",
	})
	require.True(t, ok)
	assert.Equal(t, targetURL, res.URL)
	assert.Equal(t, int32(0), calls.Load(), "validate_links=false must make zero HTTP calls")

	row, err := repo.GetEntry(ctx, targetURL, "g1")
	require.NoError(t, err)
	require.NotNil(t, row)
}

func TestLinkValidationRespectsLookupTimeoutBudget(t *testing.T) {
	ctx := context.Background()
	_, repo := setupMediaTestDB(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(5 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer server.Close()

	slowURL := server.URL + "/slow.gif"
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID: "g1", URL: slowURL, Kind: "gif",
		Description: "slow", Uses: 5, FirstSeen: 1, LastSeen: 2,
	}))

	cfg := defaultEnabledConfig()
	cfg.ValidateLinks = true
	cfg.LookupTimeoutMs = 150 // much smaller than defaultLinkCheckTimeout (1.5s)
	svc := NewService(repo, newFakeEmbedder(), cfg)
	svc.SetHTTPClient(server.Client())

	start := time.Now()
	res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:  "g1",
		RawReply: "[gif: slow]",
	})
	elapsed := time.Since(start)

	require.True(t, ok)
	assert.Equal(t, slowURL, res.URL)
	assert.Less(t, elapsed, 800*time.Millisecond, "validation must stay within lookup_timeout_ms budget, took %v", elapsed)

	row, err := repo.GetEntry(ctx, slowURL, "g1")
	require.NoError(t, err)
	require.NotNil(t, row)
}

func TestDiscordCDNRefreshTokenNeverLogged(t *testing.T) {
	ctx := context.Background()
	const secretToken = "SUPER_SECRET_BOT_TOKEN_987654321"

	logs := captureAllMediaLogs(t)
	var slogBuf bytes.Buffer

	var mode atomic.Int32
	cdnServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer cdnServer.Close()

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bot "+secretToken, r.Header.Get("Authorization"))
		switch mode.Load() {
		case 0: // success
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(refreshAttachmentURLsResponse{
				RefreshedURLs: []refreshedAttachmentURL{
					{
						Original:  "https://cdn.discordapp.com/attachments/1/2/ok.gif",
						Refreshed: cdnServer.URL + "/ok.gif?ex=1&is=2&hm=3",
					},
				},
			})
		case 1: // empty (not found)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(refreshAttachmentURLsResponse{})
		case 2: // 401 unauthorized
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"401: Unauthorized","code":0}`))
		case 3: // 500 server error
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`internal error`))
		case 4: // 429 rate limited
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"You are being rate limited."}`))
		}
	}))
	defer apiServer.Close()

	_, repo := setupMediaTestDB(t)
	cdnURL := "https://cdn.discordapp.com/attachments/1/2/ok.gif"

	cfg := defaultEnabledConfig()
	cfg.AllowDiscordCDN = true
	cfg.ValidateLinks = true
	svc := NewService(repo, newFakeEmbedder(), cfg)
	defer svc.Close()
	svc.SetDiscordRestClient(newTestDiscordRestClient(t, apiServer, secretToken, &slogBuf))
	svc.SetHTTPClient(cdnServer.Client())

	for m := int32(0); m <= 4; m++ {
		mode.Store(m)
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: cdnURL, Kind: "gif",
			Description: "token-test", Uses: 5, FirstSeen: 1, LastSeen: 2,
		}))
		_, _ = svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: token-test]",
		})
	}

	for _, entry := range logs.All() {
		assert.NotContains(t, entry.Message, secretToken, "token leaked in zap log message")
		for _, f := range entry.Context {
			assert.NotContains(t, fmt.Sprint(f), secretToken, "token leaked in zap log field %s", f.Key)
		}
	}
	assert.False(t, strings.Contains(slogBuf.String(), secretToken), "token leaked in disgo rest slog output")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestIsDiscordAttachmentURLAndNonAttachmentCDN(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		raw  string
		want bool
	}{
		{"https://cdn.discordapp.com/attachments/111/222/cat.gif", true},
		{"https://media.discordapp.net/attachments/111/222/cat.gif?width=400", true},
		{"https://cdn.discordapp.com/ephemeral-attachments/111/222/cat.png", true},
		{"https://media.discordapp.net/ephemeral-attachments/111/222/cat.png", true},
		{"https://cdn.discordapp.com/emojis/123456789.gif", false},
		{"https://cdn.discordapp.com/stickers/123456789.png", false},
		{"https://cdn.discordapp.com/avatars/111/abcdef.png", false},
		{"https://media.discordapp.net/external/abc123/https/example.com/cat.gif", false},
		{"https://cdn.discordapp.com/attachments/", false},
		{"https://cdn.discordapp.com/ephemeral-attachments/", false},
		{"https://tenor.com/view/cat-123", false},
		{"ftp://cdn.discordapp.com/attachments/1/2/a.gif", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, IsDiscordAttachmentURL(tc.raw), "url=%s", tc.raw)
	}

	t.Run("emoji and external CDN URLs skip refresh-urls and go straight to link check", func(t *testing.T) {
		for _, cdnURL := range []string{
			"https://cdn.discordapp.com/emojis/123456789.gif",
			"https://media.discordapp.net/external/abc123/https/example.com/cat.gif",
		} {
			t.Run(cdnURL, func(t *testing.T) {
				_, repo := setupMediaTestDB(t)
				require.NoError(t, repo.UpsertImport(ctx, Entry{
					GuildID: "g1", URL: cdnURL, Kind: "gif",
					Description: "emoji-ext", Uses: 5, FirstSeen: 1, LastSeen: 2,
				}))

				var refreshCalls atomic.Int32
				apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					refreshCalls.Add(1)
					w.WriteHeader(http.StatusInternalServerError)
				}))
				defer apiServer.Close()

				var headCalls atomic.Int32
				stubHTTP := &http.Client{
					Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
						assert.Equal(t, http.MethodHead, req.Method)
						assert.Equal(t, cdnURL, req.URL.String())
						headCalls.Add(1)
						return &http.Response{
							StatusCode: http.StatusOK,
							Body:       io.NopCloser(strings.NewReader("")),
							Header:     make(http.Header),
						}, nil
					}),
				}

				cfg := defaultEnabledConfig()
				cfg.AllowDiscordCDN = true
				cfg.ValidateLinks = true
				svc := NewService(repo, newFakeEmbedder(), cfg)
				defer svc.Close()
				svc.SetDiscordRestClient(newTestDiscordRestClient(t, apiServer, "tok", nil))
				svc.SetHTTPClient(stubHTTP)

				res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
					GuildID:  "g1",
					RawReply: "[gif: emoji-ext]",
				})
				require.True(t, ok)
				assert.Equal(t, cdnURL, res.URL)
				assert.Equal(t, int32(0), refreshCalls.Load(), "non-attachment Discord CDN URL must not call refresh-urls")
				assert.Equal(t, int32(1), headCalls.Load(), "non-attachment Discord CDN URL must go straight to HEAD check")
			})
		}
	})
}

func TestDedicatedRefreshClient429Isolation(t *testing.T) {
	ctx := context.Background()
	const botToken = "isolation-test-token"

	var refreshHits atomic.Int32
	var sendHits atomic.Int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/attachments/refresh-urls":
			refreshHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-RateLimit-Bucket", "refresh-urls-bucket")
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Global", "true")
			w.Header().Set("X-RateLimit-Reset-After", "30")
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"You are being rate limited.","retry_after":30,"global":true}`))
		case "/channels/123/messages":
			sendHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"999"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer apiServer.Close()

	refreshClient := NewDiscordRefreshClient(
		botToken,
		rest.WithURL(apiServer.URL),
		rest.WithHTTPClient(apiServer.Client()),
	)
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		refreshClient.Close(closeCtx)
	}()

	sendClient := rest.NewClient(
		botToken,
		rest.WithURL(apiServer.URL),
		rest.WithHTTPClient(apiServer.Client()),
	)
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		sendClient.Close(closeCtx)
	}()

	_, repo := setupMediaTestDB(t)
	cdnURL := "https://cdn.discordapp.com/attachments/1/2/ratelimited.gif"
	require.NoError(t, repo.UpsertImport(ctx, Entry{
		GuildID: "g1", URL: cdnURL, Kind: "gif",
		Description: "rl", Uses: 5, FirstSeen: 1, LastSeen: 2,
	}))

	cfg := defaultEnabledConfig()
	cfg.AllowDiscordCDN = true
	cfg.ValidateLinks = true
	cfg.LookupTimeoutMs = 2000
	svc := NewService(repo, newFakeEmbedder(), cfg)
	defer svc.Close()
	svc.SetDiscordRestClient(refreshClient)

	startRefresh := time.Now()
	res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:  "g1",
		RawReply: "[gif: rl]",
	})
	refreshElapsed := time.Since(startRefresh)
	require.True(t, ok)
	assert.Equal(t, cdnURL, res.URL, "429 on refresh must keep and post canonical URL")
	assert.Equal(t, int32(1), refreshHits.Load(), "dedicated refresh client must fail fast without retrying 429")
	assert.Less(t, refreshElapsed, 500*time.Millisecond, "429 on refresh must fail fast, took %v", refreshElapsed)

	// A second refresh on the same client must fail fast against the recorded
	// bucket/global limit without sending another HTTP request.
	startSecondRefresh := time.Now()
	res2, ok2 := svc.Resolve(ctx, ai.MediaResolveRequest{
		GuildID:  "g1",
		RawReply: "[gif: rl]",
	})
	secondRefreshElapsed := time.Since(startSecondRefresh)
	require.True(t, ok2)
	assert.Equal(t, cdnURL, res2.URL, "second refresh under active limit must still keep and post canonical URL")
	assert.Equal(t, int32(1), refreshHits.Load(), "second refresh must fail fast from recorded rate limit without hitting server again")
	assert.Less(t, secondRefreshElapsed, 500*time.Millisecond, "second refresh under active limit must fail fast, took %v", secondRefreshElapsed)

	row, err := repo.GetEntry(ctx, cdnURL, "g1")
	require.NoError(t, err)
	require.NotNil(t, row, "row must be kept on refresh 429")

	// Second independent "send" client must NOT be delayed by refresh client's recorded 429.
	sendEndpoint := rest.NewEndpoint(http.MethodPost, "/channels/123/messages")
	var sendResp map[string]any
	startSend := time.Now()
	err = sendClient.Do(sendEndpoint.Compile(nil), map[string]string{"content": "hello"}, &sendResp, rest.WithCtx(ctx))
	sendElapsed := time.Since(startSend)
	require.NoError(t, err)
	assert.Equal(t, int32(1), sendHits.Load())
	assert.Less(t, sendElapsed, 500*time.Millisecond, "send client must not be delayed by refresh client 429, took %v", sendElapsed)
}

func TestLinkValidationSSRFGuard(t *testing.T) {
	ctx := context.Background()

	t.Run("isBlockedLinkIP classifies private, loopback, link-local, site-local, NAT64, multicast, unspecified, and public IPs", func(t *testing.T) {
		blocked := []string{
			"127.0.0.1", "::1",
			"10.0.0.1", "172.16.0.1", "192.168.1.1", "fc00::1", "fd12:3456::1",
			"169.254.169.254", "fe80::1",
			"fec0::1", "feff::1", "fe80::1%eth0", "fec0::1%lo", "fd12::1%eth0",
			"0.0.0.0", "::",
			"224.0.0.1", "ff02::1",
			"100.64.0.1", "198.18.0.1", "240.0.0.1",
			"::ffff:127.0.0.1", "::ffff:10.0.0.1",
			"64:ff9b::127.0.0.1", "2002:7f00:0001::1",
			"64:ff9b:1::1.1.1.1", "64:ff9b:1::8.8.8.8", "64:ff9b:1::127.0.0.1",
		}
		for _, raw := range blocked {
			assert.True(t, isBlockedLinkIP(netip.MustParseAddr(raw)), "expected %s to be blocked", raw)
		}
		allowed := []string{
			"93.184.216.34",
			"1.1.1.1",
			"2606:2800:220:1:248:1893:25c8:1946",
			"64:ff9b::1.1.1.1",
		}
		for _, raw := range allowed {
			assert.False(t, isBlockedLinkIP(netip.MustParseAddr(raw)), "expected %s to be allowed", raw)
		}
	})

	t.Run("blocks direct private IP and redirect to 127.0.0.1 without deleting, skipping to next candidate", func(t *testing.T) {
		_, repo := setupMediaTestDB(t)

		var secretHits atomic.Int32
		secretServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			secretHits.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		defer secretServer.Close()

		publicServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/redir-to-loopback.gif":
				http.Redirect(w, r, secretServer.URL+"/internal", http.StatusFound)
			case "/good.gif":
				w.WriteHeader(http.StatusOK)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer publicServer.Close()

		// Route fake public hostname public.example -> 93.184.216.34 -> publicServer's local listener,
		// while leaving real SSRF checks active so 127.0.0.1 / 10.0.0.1 are genuinely rejected by the dialer.
		publicIP := netip.MustParseAddr("93.184.216.34")
		guardedClient := newValidationHTTPClientWithDial(
			func(ctx context.Context, network, host string) ([]netip.Addr, error) {
				if host == "public.example" {
					return []netip.Addr{publicIP}, nil
				}
				return nil, fmt.Errorf("unexpected lookup %q", host)
			},
			func(ctx context.Context, network, address string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return nil, err
				}
				if host == publicIP.String() {
					return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", publicServer.Listener.Addr().String())
				}
				return nil, fmt.Errorf("unexpected dial to %q", address)
			},
		)

		directPrivateURL := "http://10.0.0.1/private.gif"
		redirLoopbackURL := "http://public.example/redir-to-loopback.gif"
		goodPublicURL := "http://public.example/good.gif"

		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: directPrivateURL, Kind: "gif",
			Description: "ssrf", Uses: 30, FirstSeen: 1, LastSeen: 2,
		}))
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: redirLoopbackURL, Kind: "gif",
			Description: "ssrf", Uses: 20, FirstSeen: 1, LastSeen: 2,
		}))
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: goodPublicURL, Kind: "gif",
			Description: "ssrf", Uses: 10, FirstSeen: 1, LastSeen: 2,
		}))

		cfg := defaultEnabledConfig()
		cfg.ValidateLinks = true
		svc := NewService(repo, newFakeEmbedder(), cfg)
		svc.SetHTTPClient(guardedClient)

		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: ssrf]",
		})
		svc.Close()
		require.True(t, ok)
		assert.Equal(t, goodPublicURL, res.URL, "blocked candidates must be skipped to next allowed candidate")
		assert.Equal(t, int32(0), secretHits.Load(), "loopback redirect target must never be contacted")

		// Blocked rows must NOT be deleted from DB.
		rowPrivate, err := repo.GetEntry(ctx, directPrivateURL, "g1")
		require.NoError(t, err)
		require.NotNil(t, rowPrivate, "blocked private IP row must not be deleted")
		rowRedir, err := repo.GetEntry(ctx, redirLoopbackURL, "g1")
		require.NoError(t, err)
		require.NotNil(t, rowRedir, "blocked redirect row must not be deleted")
	})

	t.Run("caps redirects at 5 and blocks unsupported schemes without deleting", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/five-0":
				http.Redirect(w, r, "/five-1", http.StatusFound)
			case "/five-1":
				http.Redirect(w, r, "/five-2", http.StatusFound)
			case "/five-2":
				http.Redirect(w, r, "/five-3", http.StatusFound)
			case "/five-3":
				http.Redirect(w, r, "/five-4", http.StatusFound)
			case "/five-4":
				http.Redirect(w, r, "/five-5", http.StatusFound)
			case "/five-5":
				w.WriteHeader(http.StatusOK)
			case "/six-0":
				http.Redirect(w, r, "/six-1", http.StatusFound)
			case "/six-1":
				http.Redirect(w, r, "/six-2", http.StatusFound)
			case "/six-2":
				http.Redirect(w, r, "/six-3", http.StatusFound)
			case "/six-3":
				http.Redirect(w, r, "/six-4", http.StatusFound)
			case "/six-4":
				http.Redirect(w, r, "/six-5", http.StatusFound)
			case "/six-5":
				http.Redirect(w, r, "/six-6", http.StatusFound)
			case "/six-6":
				w.WriteHeader(http.StatusOK)
			case "/redir-file":
				http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer server.Close()

		publicIP := netip.MustParseAddr("93.184.216.34")
		guardedClient := newValidationHTTPClientWithDial(
			func(ctx context.Context, network, host string) ([]netip.Addr, error) {
				if host == "public.example" {
					return []netip.Addr{publicIP}, nil
				}
				return nil, fmt.Errorf("unexpected lookup %q", host)
			},
			func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", server.Listener.Addr().String())
			},
		)

		outcome5, head5, _, err5 := checkLink(ctx, guardedClient, "http://public.example/five-0")
		require.NoError(t, err5)
		assert.Equal(t, linkAlive, outcome5)
		assert.Equal(t, http.StatusOK, head5)

		outcome6, _, _, err6 := checkLink(ctx, guardedClient, "http://public.example/six-0")
		assert.ErrorIs(t, err6, errLinkBlocked)
		assert.Equal(t, linkBlocked, outcome6)

		outcomeFile, _, _, errFile := checkLink(ctx, guardedClient, "file:///etc/passwd")
		assert.ErrorIs(t, errFile, errLinkBlocked)
		assert.Equal(t, linkBlocked, outcomeFile)

		outcomeRedirFile, _, _, errRedirFile := checkLink(ctx, guardedClient, "http://public.example/redir-file")
		assert.ErrorIs(t, errRedirFile, errLinkBlocked)
		assert.Equal(t, linkBlocked, outcomeRedirFile)
	})
}

func TestLinkValidationBudgetSlicingWithThreeSlowCandidates(t *testing.T) {
	ctx := context.Background()

	t.Run("3 slow candidates stay strictly within lookup_timeout_ms", func(t *testing.T) {
		_, repo := setupMediaTestDB(t)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/slow-dead-1.gif", "/slow-dead-2.gif":
				select {
				case <-time.After(45 * time.Millisecond):
					w.WriteHeader(http.StatusNotFound)
				case <-r.Context().Done():
				}
			case "/slow-hang-3.gif":
				select {
				case <-time.After(5 * time.Second):
				case <-r.Context().Done():
				}
			default:
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer server.Close()

		for _, item := range []struct {
			path string
			uses int64
		}{
			{"/slow-dead-1.gif", 30},
			{"/slow-dead-2.gif", 20},
			{"/slow-hang-3.gif", 10},
		} {
			require.NoError(t, repo.UpsertImport(ctx, Entry{
				GuildID: "g1", URL: server.URL + item.path, Kind: "gif",
				Description: "slow3", Uses: item.uses, FirstSeen: 1, LastSeen: 2,
			}))
		}

		cfg := defaultEnabledConfig()
		cfg.ValidateLinks = true
		cfg.LookupTimeoutMs = 500 // 3 * 1.5s would be 4.5s without budget slicing
		svc := NewService(repo, newFakeEmbedder(), cfg)
		svc.SetHTTPClient(server.Client())

		start := time.Now()
		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: slow3]",
		})
		elapsed := time.Since(start)
		svc.Close()

		require.True(t, ok)
		assert.Equal(t, server.URL+"/slow-hang-3.gif", res.URL)
		assert.Less(t, elapsed, 750*time.Millisecond, "3 slow candidates must fit within lookup_timeout_ms, took %v", elapsed)
	})

	t.Run("first slow candidate is sliced to remaining/3 rather than consuming full budget", func(t *testing.T) {
		_, repo := setupMediaTestDB(t)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-time.After(5 * time.Second):
			case <-r.Context().Done():
			}
		}))
		defer server.Close()

		url1 := server.URL + "/hang1.gif"
		require.NoError(t, repo.UpsertImport(ctx, Entry{
			GuildID: "g1", URL: url1, Kind: "gif",
			Description: "hang", Uses: 10, FirstSeen: 1, LastSeen: 2,
		}))

		cfg := defaultEnabledConfig()
		cfg.ValidateLinks = true
		cfg.LookupTimeoutMs = 600 // slice for candidate 1 of 3 = 200ms
		svc := NewService(repo, newFakeEmbedder(), cfg)
		defer svc.Close()
		svc.SetHTTPClient(server.Client())

		start := time.Now()
		res, ok := svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: hang]",
		})
		elapsed := time.Since(start)

		require.True(t, ok)
		assert.Equal(t, url1, res.URL)
		assert.Less(t, elapsed, 450*time.Millisecond, "candidate 1 slice (~200ms) must trigger well before full 600ms timeout, took %v", elapsed)
	})
}

func TestCheckURLDryRun(t *testing.T) {
	ctx := context.Background()

	t.Run("attachment alive strips signature from RefreshedHostPath", func(t *testing.T) {
		cdnServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer cdnServer.Close()

		signedURL := cdnServer.URL + "/attachments/1/2/cat.gif?ex=deadbeef&is=123456&hm=supersecret"
		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(refreshAttachmentURLsResponse{
				RefreshedURLs: []refreshedAttachmentURL{
					{Original: "https://cdn.discordapp.com/attachments/1/2/cat.gif", Refreshed: signedURL},
				},
			})
		}))
		defer apiServer.Close()

		report := CheckURL(
			ctx,
			"https://cdn.discordapp.com/attachments/1/2/cat.gif",
			newTestDiscordRestClient(t, apiServer, "secret-token", nil),
			cdnServer.Client(),
		)
		assert.True(t, report.IsAttachment)
		assert.Equal(t, "ok", report.RefreshStatus)
		assert.Contains(t, report.RefreshedHostPath, "/attachments/1/2/cat.gif")
		assert.NotContains(t, report.RefreshedHostPath, "ex=")
		assert.NotContains(t, report.RefreshedHostPath, "supersecret")
		assert.Equal(t, http.StatusOK, report.HeadStatus)
		assert.Equal(t, 0, report.GetStatus)
		assert.Equal(t, "alive", report.Verdict)
	})

	t.Run("attachment HEAD 404 + GET 404 reports dead", func(t *testing.T) {
		cdnServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer cdnServer.Close()

		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(refreshAttachmentURLsResponse{
				RefreshedURLs: []refreshedAttachmentURL{
					{Refreshed: cdnServer.URL + "/attachments/1/2/dead.gif?ex=1&hm=2"},
				},
			})
		}))
		defer apiServer.Close()

		report := CheckURL(
			ctx,
			"https://cdn.discordapp.com/attachments/1/2/dead.gif",
			newTestDiscordRestClient(t, apiServer, "secret-token", nil),
			cdnServer.Client(),
		)
		assert.True(t, report.IsAttachment)
		assert.Equal(t, "ok", report.RefreshStatus)
		assert.Equal(t, http.StatusNotFound, report.HeadStatus)
		assert.Equal(t, http.StatusNotFound, report.GetStatus)
		assert.Equal(t, "dead", report.Verdict)
	})

	t.Run("non-attachment private IP reports blocked with default SSRF-guarded client", func(t *testing.T) {
		report := CheckURL(ctx, "http://127.0.0.1:8080/internal.gif", nil, nil)
		assert.False(t, report.IsAttachment)
		assert.Equal(t, "skipped", report.RefreshStatus)
		assert.Equal(t, "blocked", report.Verdict)
	})

	t.Run("refreshed URL error strips signed query from report.Error", func(t *testing.T) {
		closedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		closedURL := closedServer.URL
		closedClient := closedServer.Client()
		closedServer.Close()

		signedURL := closedURL + "/attachments/1/2/err.gif?ex=deadbeef&is=123456&hm=supersecret"
		apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(refreshAttachmentURLsResponse{
				RefreshedURLs: []refreshedAttachmentURL{
					{Original: "https://cdn.discordapp.com/attachments/1/2/err.gif", Refreshed: signedURL},
				},
			})
		}))
		defer apiServer.Close()

		report := CheckURL(
			ctx,
			"https://cdn.discordapp.com/attachments/1/2/err.gif",
			newTestDiscordRestClient(t, apiServer, "secret-token", nil),
			closedClient,
		)
		assert.True(t, report.IsAttachment)
		assert.Equal(t, "ok", report.RefreshStatus)
		assert.Equal(t, "unknown", report.Verdict)
		assert.NotEmpty(t, report.Error)
		assert.Contains(t, report.Error, "/attachments/1/2/err.gif")
		for _, forbidden := range []string{"ex=", "deadbeef", "is=", "hm=", "supersecret"} {
			assert.NotContains(t, report.Error, forbidden)
		}
	})

	t.Run("checkLink trims leading and trailing whitespace for HEAD and GET requests", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		outcome, headStatus, getStatus, err := checkLink(ctx, server.Client(), "  "+server.URL+"/trimmed.gif\t\n")
		require.NoError(t, err)
		assert.Equal(t, linkAlive, outcome)
		assert.Equal(t, http.StatusMethodNotAllowed, headStatus)
		assert.Equal(t, http.StatusOK, getStatus)
	})
}

