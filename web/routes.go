package web

import (
	"context"
	"io/fs"
	"net/http"
	"strings"

	"github.com/disgoorg/snowflake/v2"
	"go.uber.org/zap"

	"polynux/disgoroq/logger"
)

// registerRoutes registers all HTTP routes on the server mux.
// Phase 2a (Config) and Phase 2b (Data) can replace the stub handlers below
// and add their own route lines here.
func (s *Server) registerRoutes() {
	staticSub, err := fs.Sub(embeddedFS, "static")
	if err != nil {
		panic("web: missing embedded static directory: " + err.Error())
	}
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/guilds", http.StatusFound)
	})

	// Phase 1: Guilds
	s.mux.HandleFunc("GET /guilds", s.handleGuildList)
	s.mux.HandleFunc("GET /guilds/{id}", s.guildRoute(s.handleGuildDetail))
	s.mux.HandleFunc("POST /guilds/{id}/general", s.guildRoute(s.handleGuildGeneralSave))
	s.mux.HandleFunc("POST /guilds/{id}/prompts", s.guildRoute(s.handleGuildPromptsSave))
	s.mux.HandleFunc("POST /guilds/{id}/channels", s.guildRoute(s.handleGuildChannelsSave))
	s.mux.HandleFunc("POST /guilds/{id}/voice", s.guildRoute(s.handleGuildVoiceSave))
	s.mux.HandleFunc("POST /guilds/{id}/voice/reset", s.guildRoute(s.handleGuildVoiceReset))
	s.mux.HandleFunc("POST /guilds/{id}/reengage/message", s.guildRoute(s.handleGuildReengageMessageSave))
	s.mux.HandleFunc("POST /guilds/{id}/reengage/channel", s.guildRoute(s.handleGuildReengageChannelSave))
	s.mux.HandleFunc("POST /guilds/{id}/reengage/channel/{channelID}/reset", s.guildRoute(s.handleGuildReengageChannelReset))
	s.mux.HandleFunc("POST /guilds/{id}/reset", s.guildRoute(s.handleGuildSettingReset))

	// Phase 2a: Global config.yaml editor
	s.mux.HandleFunc("GET /config", s.handleConfigPage)
	s.mux.HandleFunc("POST /config/{section}", s.handleConfigSectionSave)

	// Phase 2b: Data views
	s.mux.HandleFunc("GET /data", s.handleData)
	s.mux.HandleFunc("POST /data/summaries/forget", s.handleDataSummariesForget)
	s.mux.HandleFunc("POST /data/summaries/{id}/delete", s.handleDataSummaryDelete)
	s.mux.HandleFunc("POST /data/attachments/purge", s.handleDataAttachmentPurge)
	s.mux.HandleFunc("POST /data/attachments/{id}/delete", s.handleDataAttachmentDelete)
	s.mux.HandleFunc("POST /data/media/delete", s.handleDataMediaDelete)
	s.mux.HandleFunc("POST /data/media/edit", s.handleDataMediaEdit)
}

type stubPageData struct {
	Title       string
	ActiveNav   string
	Heading     string
	Description string
}

func (s *Server) handleStub(title, activeNav, description string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.renderPage(w, "stub", http.StatusOK, stubPageData{
			Title:       title,
			ActiveNav:   activeNav,
			Heading:     title,
			Description: description,
		})
	}
}

// guildRoute guards every /guilds/{id} handler: the repository must be wired,
// {id} (and {channelID} when present) must be Discord snowflakes, and the guild
// must be known to the bot (in the cache or with stored settings). This keeps
// typo'd URLs from creating orphan guild_settings rows.
func (s *Server) guildRoute(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.deps.Repo == nil {
			http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
			return
		}
		guildID := strings.TrimSpace(r.PathValue("id"))
		if _, err := snowflake.Parse(guildID); err != nil {
			http.NotFound(w, r)
			return
		}
		if channelID := r.PathValue("channelID"); channelID != "" {
			if _, err := snowflake.Parse(channelID); err != nil {
				http.NotFound(w, r)
				return
			}
		}
		known, err := s.isKnownGuild(r.Context(), guildID)
		if err != nil {
			http.Error(w, "Failed to check guild", http.StatusInternalServerError)
			return
		}
		if !known {
			http.NotFound(w, r)
			return
		}
		next(w, r)
	}
}

// isKnownGuild reports whether the bot knows the guild: in the cache, marked
// unavailable during a gateway outage (still a member, like reengage treats it),
// or with stored settings. A DB error is returned, not reported as "unknown".
func (s *Server) isKnownGuild(ctx context.Context, guildID string) (bool, error) {
	if s.lookupGuildSummary(guildID).InCache {
		return true, nil
	}
	if s.deps.Client != nil && s.deps.Client.Caches != nil {
		if sf, err := snowflake.Parse(guildID); err == nil && s.deps.Client.Caches.IsGuildUnavailable(sf) {
			return true, nil
		}
	}
	settings, err := s.deps.Repo.GetGuildSettingsMap(ctx, guildID)
	if err != nil {
		logger.Error("Failed to check guild existence", zap.String("guild_id", guildID), zap.Error(err))
		return false, err
	}
	return len(settings) > 0, nil
}
