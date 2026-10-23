package app

import (
	"context"
	"net/http"
	"time"

	"agent-workbench/internal/store"
)

// These control-plane endpoints exchange no credentials and have no renderer
// IPC equivalents. Only the desktop account owner supplies operation receipts.
func (a *Server) accountRoutes() {
	a.mux.HandleFunc("GET /api/native-account", func(w http.ResponseWriter, r *http.Request) {
		if !human(r) {
			replyError(w, store.ErrForbidden)
			return
		}
		reply(w, a.Accounts.View())
	})
	a.mux.HandleFunc("POST /api/native-account/auth/begin", func(w http.ResponseWriter, r *http.Request) {
		if !human(r) {
			replyError(w, store.ErrForbidden)
			return
		}
		var input struct {
			Kind string `json:"kind"`
		}
		if err := decode(w, r, &input); err != nil {
			replyError(w, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		operation, err := a.Accounts.Begin(ctx, input.Kind)
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, map[string]any{"operation": operation})
	})
	a.mux.HandleFunc("POST /api/native-account/auth/finish", func(w http.ResponseWriter, r *http.Request) {
		if !human(r) {
			replyError(w, store.ErrForbidden)
			return
		}
		var input struct {
			Operation     string `json:"operation"`
			Authenticated bool   `json:"authenticated"`
			OwnerActive   bool   `json:"owner_active"`
			Healthy       bool   `json:"healthy"`
		}
		if err := decode(w, r, &input); err != nil {
			replyError(w, err)
			return
		}
		if err := a.Accounts.Finish(input.Operation, input.Authenticated, input.OwnerActive, input.Healthy); err != nil {
			replyError(w, err)
			return
		}
		reply(w, a.Accounts.View())
	})
}
