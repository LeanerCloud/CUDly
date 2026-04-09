package apiserver

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/config"
)

// Server is the CUDly HTTP API server.
type Server struct {
	cfg        config.Config
	store      *RunStore
	httpServer *http.Server
	auditMu    sync.Mutex
}

// NewServer creates a new API server with the given config.
func NewServer(cfg config.Config) *Server {
	s := &Server{
		cfg:   cfg,
		store: NewRunStore(),
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	s.httpServer = &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           authMiddleware(cfg, mux),
		ReadHeaderTimeout: 30 * time.Second,
	}
	return s
}

// registerRoutes wires all API routes onto mux.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /runs", s.handleCreateRun)
	mux.HandleFunc("GET /runs/{id}", s.handleGetRun)
	mux.HandleFunc("POST /runs/{id}/approve", s.handleApproveRun)
	mux.HandleFunc("DELETE /runs/{id}", s.handleCancelRun)
	mux.HandleFunc("GET /recommendations", s.handleGetRecommendations)
	mux.HandleFunc("GET /health", s.handleHealth)
}

// authMiddleware enforces Bearer token auth for all paths except /health.
// If the API key env var is unset or empty, all requests are allowed through.
func authMiddleware(cfg config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		expectedKey := os.Getenv(cfg.Server.APIKeyEnv)
		if expectedKey == "" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+expectedKey {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Start starts the HTTP server and blocks until SIGTERM or SIGINT.
func (s *Server) Start() error {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, os.Interrupt)

	errCh := make(chan error, 1)
	go func() {
		log.Printf("API server listening on %s", s.cfg.Server.Listen)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-quit:
		return s.gracefulShutdown()
	}
}

// gracefulShutdown cancels all non-terminal runs and drains the HTTP server.
func (s *Server) gracefulShutdown() error {
	log.Println("Shutting down API server...")
	now := time.Now()

	s.store.Lock()
	for _, r := range s.store.runs {
		if r.Status != RunStatusCompleted && r.Status != RunStatusFailed && r.Status != RunStatusCancelled {
			if r.cancelCtx != nil {
				r.cancelCtx()
			}
			r.Status = RunStatusCancelled
			t := now
			r.CompletedAt = &t
		}
	}
	s.store.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}
