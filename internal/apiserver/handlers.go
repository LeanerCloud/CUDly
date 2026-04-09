package apiserver

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/scorer"
	"github.com/google/uuid"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	var req RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	id := uuid.New().String()
	approveCh := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())

	run := &Run{
		ID:        id,
		Status:    RunStatusPendingApproval,
		CreatedAt: time.Now(),
		Request:   req,
		AuditPath: s.cfg.AuditLog,
		approveCh: approveCh,
		cancelCtx: cancel,
	}
	s.store.Set(id, run)

	go s.executeRun(ctx, run)

	writeJSON(w, http.StatusAccepted, map[string]string{"id": id})
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, ok := s.store.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "run not found"})
		return
	}
	s.store.RLock()
	defer s.store.RUnlock()
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleApproveRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, ok := s.store.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "run not found"})
		return
	}

	s.store.Lock()
	defer s.store.Unlock()

	if run.Status != RunStatusPendingApproval {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "run is not pending approval"})
		return
	}
	run.Status = RunStatusRunning
	close(run.approveCh)
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, ok := s.store.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "run not found"})
		return
	}

	s.store.Lock()
	defer s.store.Unlock()

	switch run.Status {
	case RunStatusCompleted, RunStatusFailed:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "run is already terminal"})
		return
	case RunStatusCancelled:
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
		return
	}

	now := time.Now()
	run.cancelCtx()
	run.Status = RunStatusCancelled
	run.CompletedAt = &now
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

func (s *Server) handleGetRecommendations(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	_ = ctx

	// Cloud fetch is not yet wired into the server; return scored empty list.
	recs := []common.Recommendation{}
	cfg := scorer.Config{
		MinSavingsPct:      s.cfg.Scorer.MinSavingsPct,
		MaxBreakEvenMonths: s.cfg.Scorer.MaxBreakEvenMonths,
		MinCount:           s.cfg.Scorer.MinCount,
		EnabledServices:    s.cfg.Scorer.EnabledServices,
	}
	result := scorer.Score(recs, cfg)
	writeJSON(w, http.StatusOK, result)
}

// executeRun runs the scoring/purchase pipeline for a single run.
func (s *Server) executeRun(ctx context.Context, run *Run) {
	runCfg := s.cfg
	runCfg.DryRun = run.Request.DryRun
	runCfg.AutoApprove = run.Request.AutoApprove

	if runCfg.AutoApprove {
		s.store.Lock()
		if run.Status == RunStatusPendingApproval {
			run.Status = RunStatusRunning
		}
		s.store.Unlock()
	} else {
		select {
		case <-run.approveCh:
		case <-ctx.Done():
			return
		}
	}

	recs := []common.Recommendation{}
	scorerCfg := scorer.Config{
		MinSavingsPct:      runCfg.Scorer.MinSavingsPct,
		MaxBreakEvenMonths: runCfg.Scorer.MaxBreakEvenMonths,
		MinCount:           runCfg.Scorer.MinCount,
		EnabledServices:    runCfg.Scorer.EnabledServices,
	}
	result := scorer.Score(recs, scorerCfg)

	s.store.Lock()
	defer s.store.Unlock()
	if run.Status == RunStatusCancelled {
		return
	}
	now := time.Now()
	run.Status = RunStatusCompleted
	run.CompletedAt = &now
	run.Result = &result
	log.Printf("Run %s completed: %d passed, %d filtered", run.ID, len(result.Passed), len(result.Filtered))
}

// writeJSON writes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}
