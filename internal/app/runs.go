package app

import (
	"agent-workbench/internal/store"
	"net/http"
)

func (a *Server) runRoutes() {
	a.mux.HandleFunc("GET /api/projects/{id}/runs", func(w http.ResponseWriter, r *http.Request) {
		runs, err := a.Store.Runs(r.Context(), principal(r), r.PathValue("id"))
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, runs)
	})
	a.mux.HandleFunc("GET /api/projects/{id}/runs/{run}", func(w http.ResponseWriter, r *http.Request) {
		run, err := a.Store.Run(r.Context(), principal(r), r.PathValue("id"), r.PathValue("run"))
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, run)
	})
	a.mux.HandleFunc("POST /api/projects/{id}/runs", func(w http.ResponseWriter, r *http.Request) {
		if !human(r) {
			replyError(w, store.ErrForbidden)
			return
		}
		var input struct {
			store.RunInput
			Values map[string]map[string]string `json:"values"`
		}
		if err := decode(w, r, &input); err != nil {
			replyError(w, err)
			return
		}
		run, err := a.Runner.Start(r.Context(), principal(r), r.PathValue("id"), input.RunInput, input.Values)
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, run)
	})
	a.mux.HandleFunc("POST /api/projects/{id}/runs/{run}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if err := a.Runner.Cancel(r.Context(), principal(r), r.PathValue("id"), r.PathValue("run")); err != nil {
			replyError(w, err)
			return
		}
		reply(w, map[string]bool{"cancel_requested": true})
	})
	a.mux.HandleFunc("POST /api/projects/{id}/runs/{run}/resume", func(w http.ResponseWriter, r *http.Request) {
		if !human(r) {
			replyError(w, store.ErrForbidden)
			return
		}
		var input struct {
			Values map[string]map[string]string `json:"values"`
		}
		if err := decode(w, r, &input); err != nil {
			replyError(w, err)
			return
		}
		run, err := a.Runner.Resume(r.Context(), principal(r), r.PathValue("id"), r.PathValue("run"), input.Values)
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, run)
	})
	a.mux.HandleFunc("POST /api/connections/{id}/revoke", func(w http.ResponseWriter, r *http.Request) {
		if !human(r) {
			replyError(w, store.ErrForbidden)
			return
		}
		a.Runner.RevokeConnection(r.PathValue("id"))
		reply(w, map[string]bool{"revoked": true})
	})
	a.mux.HandleFunc("GET /api/usage", func(w http.ResponseWriter, r *http.Request) {
		summaries, budgets, err := a.Store.Usage(r.Context(), principal(r))
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, map[string]any{"summaries": summaries, "budgets": budgets, "provider_balance": "unknown", "subscription_quota": "unknown"})
	})
	a.mux.HandleFunc("PUT /api/budgets/{currency}", func(w http.ResponseWriter, r *http.Request) {
		var b store.Budget
		if err := decode(w, r, &b); err != nil {
			replyError(w, err)
			return
		}
		if b.Currency != r.PathValue("currency") {
			replyError(w, store.ErrConflict)
			return
		}
		saved, err := a.Store.SaveBudget(r.Context(), principal(r), b)
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, saved)
	})
}
