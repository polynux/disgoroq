package medialibrary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

	t.Run("refreshes Discord CDN URL and posts signed URL while keeping canonical URL in DB", func(t *testing.T) {
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

	t.Run("refreshes media.discordapp.net even when validate_links is false without HEAD check", func(t *testing.T) {
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

	t.Run("refreshed URL returning 404 deletes canonical row and tries next candidate", func(t *testing.T) {
		_, repo := setupMediaTestDB(t)
		logs := captureAllMediaLogs(t)

		deadCDN := "https://cdn.discordapp.com/attachments/1/2/deleted.gif"
		cdnServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/attachments/1/2/deleted.gif":
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

		row, err := repo.GetEntry(ctx, deadCDN, "g1")
		require.NoError(t, err)
		assert.Nil(t, row, "dead canonical CDN row must be deleted")

		removed := logs.FilterMessage("media library dead link removed").All()
		require.Len(t, removed, 1)
		assert.Equal(t, deadCDN, removed[0].ContextMap()["url"])
		assert.Equal(t, "g1", removed[0].ContextMap()["guild_id"])
		assert.EqualValues(t, 404, removed[0].ContextMap()["status"])
	})

	t.Run("refresh returning no URL for attachment treats it as dead and deletes row", func(t *testing.T) {
		_, repo := setupMediaTestDB(t)
		logs := captureAllMediaLogs(t)

		deadCDN := "https://cdn.discordapp.com/attachments/1/2/gone.gif"
		mediaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer mediaServer.Close()
		aliveURL := mediaServer.URL + "/backup.gif"

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
			_ = json.NewEncoder(w).Encode(refreshAttachmentURLsResponse{RefreshedURLs: nil})
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
		require.True(t, ok)
		assert.Equal(t, aliveURL, res.URL)

		row, err := repo.GetEntry(ctx, deadCDN, "g1")
		require.NoError(t, err)
		assert.Nil(t, row)

		removed := logs.FilterMessage("media library dead link removed").All()
		require.Len(t, removed, 1)
		assert.Equal(t, deadCDN, removed[0].ContextMap()["url"])
		assert.Equal(t, "g1", removed[0].ContextMap()["guild_id"])
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

	t.Run("404 deletes only matching guild row and selects next candidate", func(t *testing.T) {
		_, repo := setupMediaTestDB(t)
		logs := captureAllMediaLogs(t)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/dead-404.gif":
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

		rowG1, err := repo.GetEntry(ctx, deadURL, "g1")
		require.NoError(t, err)
		assert.Nil(t, rowG1)

		rowG2, err := repo.GetEntry(ctx, deadURL, "g2")
		require.NoError(t, err)
		require.NotNil(t, rowG2, "other guild's row must not be deleted")

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
		var checkedPaths []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			checkedMu.Lock()
			checkedPaths = append(checkedPaths, r.URL.Path)
			checkedMu.Unlock()
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
		// Random fallback may pick /cand4-alive.gif or /fallback-alive.gif (both are live gifs in g1),
		// and any dead fallback candidate it hits is deleted.
		assert.Contains(t, []string{server.URL + "/cand4-alive.gif", server.URL + "/fallback-alive.gif"}, res.URL)

		// First 3 checks must be the 3 dead candidates from the exact lookup; 4th exact match wasn't checked in exact stage.
		checkedMu.Lock()
		require.GreaterOrEqual(t, len(checkedPaths), 4)
		assert.Equal(t, []string{"/dead1.gif", "/dead2.gif", "/dead3.gif"}, checkedPaths[:3])
		checkedMu.Unlock()

		for _, deadPath := range []string{"/dead1.gif", "/dead2.gif", "/dead3.gif"} {
			row, err := repo.GetEntry(ctx, server.URL+deadPath, "g1")
			require.NoError(t, err)
			assert.Nil(t, row)
		}

		// With RandomGifFallback disabled, 3 dead exact candidates stop before the 4th and return no match.
		_, repoNoFB := setupMediaTestDB(t)
		var countNoFB atomic.Int32
		serverNoFB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			countNoFB.Add(1)
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
		assert.False(t, ok)
		assert.Equal(t, int32(3), countNoFB.Load(), "must check at most 3 candidates per lookup")
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
		row, err := repo.GetEntry(ctx, trigDead, "g1")
		require.NoError(t, err)
		assert.Nil(t, row)

		// Cross-guild dead entry deletes row in its owning guild (g2) and picks g3's alive entry.
		res, ok = svc.Resolve(ctx, ai.MediaResolveRequest{
			GuildID:  "g1",
			RawReply: "[gif: shared]",
		})
		require.True(t, ok)
		assert.Equal(t, crossAlive, res.URL)
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
