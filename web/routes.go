package web

import (
	"io/fs"
	"net/http"
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
	s.mux.HandleFunc("GET /guilds/{id}", s.handleGuildDetail)
	s.mux.HandleFunc("POST /guilds/{id}/general", s.handleGuildGeneralSave)
	s.mux.HandleFunc("POST /guilds/{id}/prompts", s.handleGuildPromptsSave)
	s.mux.HandleFunc("POST /guilds/{id}/channels", s.handleGuildChannelsSave)
	s.mux.HandleFunc("POST /guilds/{id}/voice", s.handleGuildVoiceSave)
	s.mux.HandleFunc("POST /guilds/{id}/voice/reset", s.handleGuildVoiceReset)
	s.mux.HandleFunc("POST /guilds/{id}/reengage/message", s.handleGuildReengageMessageSave)
	s.mux.HandleFunc("POST /guilds/{id}/reengage/channel", s.handleGuildReengageChannelSave)
	s.mux.HandleFunc("POST /guilds/{id}/reengage/channel/{channelID}/reset", s.handleGuildReengageChannelReset)
	s.mux.HandleFunc("POST /guilds/{id}/reset", s.handleGuildSettingReset)

	// Phase 2a: Global config.yaml editor (stub)
	s.mux.HandleFunc("GET /config", s.handleStub("Config", "config", "Global config.yaml editor — coming soon."))

	// Phase 2b: Data views (stub)
	s.mux.HandleFunc("GET /data", s.handleStub("Data", "data", "Summaries, message cache, media library, and events — coming soon."))
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
