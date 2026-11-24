package web

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"go.uber.org/zap"

	"polynux/disgoroq/config"
	"polynux/disgoroq/database"
	"polynux/disgoroq/logger"
	"polynux/disgoroq/medialibrary"
	"polynux/disgoroq/memory"
)

// Deps holds the runtime dependencies injected into the web admin server.
// Add new optional services here as additional admin pages are implemented.
// Repo is required for the guild pages; they answer 503 without it.
type Deps struct {
	Client    *bot.Client
	Repo      *database.Repository
	Config    *config.Config
	Memory    memory.Service           // optional, may be nil
	Media     *medialibrary.Service    // optional, nil when the media library is disabled
	MediaRepo *medialibrary.Repository // optional, nil when the database is unavailable
}

// Server serves the embedded web admin UI.
type Server struct {
	deps      Deps
	cfg       config.WebConfig
	mux       *http.ServeMux
	handler   http.Handler
	templates *templateStore

	mu         sync.Mutex
	httpServer *http.Server
	listener   net.Listener
}

// New constructs a configured web admin Server without starting it.
func New(deps Deps, cfg config.WebConfig) *Server {
	s := &Server{
		deps:      deps,
		cfg:       cfg,
		mux:       http.NewServeMux(),
		templates: mustLoadTemplates(),
	}
	s.registerRoutes()
	s.handler = s.withHostAllowlist(s.withCSRFGuard(s.mux))
	return s
}

// Handler returns the root HTTP handler including security middleware.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// Addr returns the bound listener address once started, or the configured listen address.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return strings.TrimSpace(s.cfg.Listen)
}

// Start binds the configured listen address and serves requests in a background goroutine.
// If binding fails, the error is logged and returned so the caller can keep the bot running.
func (s *Server) Start() error {
	listen := strings.TrimSpace(s.cfg.Listen)
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		logger.Error("Failed to bind web admin UI listener",
			zap.String("listen", listen),
			zap.Error(err))
		return err
	}

	httpSrv := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		// Route net/http's own errors (handler panics, accept errors) through zap.
		ErrorLog: zap.NewStdLog(logger.Log.Named("web")),
	}

	s.mu.Lock()
	s.listener = ln
	s.httpServer = httpSrv
	s.mu.Unlock()

	logger.Info("Web admin UI started", zap.String("listen", ln.Addr().String()))

	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Web admin UI server stopped with error", zap.Error(err))
		}
	}()

	return nil
}

// Shutdown gracefully stops the HTTP server if it was started.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	httpSrv := s.httpServer
	s.mu.Unlock()

	if httpSrv == nil {
		return nil
	}

	logger.Info("Shutting down web admin UI")
	return httpSrv.Shutdown(ctx)
}
