package app

import "net/http"

func (a *Server) nativeRoutes() {
	a.mux.HandleFunc("GET /api/projects/{id}/native-sessions", func(w http.ResponseWriter, r *http.Request) {
		sessions, err := a.Native.List(r.Context(), principal(r), r.PathValue("id"))
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, sessions)
	})
	a.mux.HandleFunc("GET /api/projects/{id}/native-sessions/{session}", func(w http.ResponseWriter, r *http.Request) {
		view, err := a.Native.Get(r.Context(), principal(r), r.PathValue("id"), r.PathValue("session"))
		if err != nil {
			replyError(w, err)
			return
		}
		attempts, err := a.Store.NativeAttempts(r.Context(), principal(r), r.PathValue("id"), r.PathValue("session"))
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, map[string]any{"view": view, "attempts": attempts})
	})
	// Native launch/resume is intentionally not an HTTP operation yet: the current
	// phase only exposes durable records; account and egress integration is separate.
}
