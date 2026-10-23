package app

import (
	"agent-workbench/internal/catalog"
	"errors"
	"net/http"
	"strconv"
)

func (a *Server) catalogRoutes() {
	a.mux.HandleFunc("POST /api/connections/validate", func(w http.ResponseWriter, r *http.Request) {
		if !human(r) {
			replyError(w, errors.New("仅用户可校验连接"))
			return
		}
		var c catalog.Connection
		if err := decode(w, r, &c); err != nil {
			replyError(w, err)
			return
		}
		if err := c.Validate(); err != nil {
			replyError(w, err)
			return
		}
		reply(w, map[string]bool{"valid": true})
	})
	a.mux.HandleFunc("GET /api/connections", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.Connections(r.Context(), principal(r))
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, v)
	})
	a.mux.HandleFunc("PUT /api/connections/{id}", func(w http.ResponseWriter, r *http.Request) {
		var v catalog.Connection
		if err := decode(w, r, &v); err != nil {
			replyError(w, err)
			return
		}
		if v.ID != r.PathValue("id") {
			replyError(w, errors.New("连接 ID 不匹配"))
			return
		}
		if err := a.Store.PutConnection(r.Context(), principal(r), v); err != nil {
			replyError(w, err)
			return
		}
		reply(w, v)
	})
	a.mux.HandleFunc("DELETE /api/connections/{id}", func(w http.ResponseWriter, r *http.Request) {
		revision, err := strconv.Atoi(r.URL.Query().Get("revision"))
		if err != nil {
			replyError(w, errors.New("修订号无效"))
			return
		}
		if err = a.Store.DeleteConnection(r.Context(), principal(r), r.PathValue("id"), revision); err != nil {
			replyError(w, err)
			return
		}
		reply(w, map[string]bool{"deleted": true})
	})
	a.mux.HandleFunc("GET /api/roles", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.Roles(r.Context(), principal(r))
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, v)
	})
	a.mux.HandleFunc("GET /api/role-templates", func(w http.ResponseWriter, r *http.Request) {
		if !human(r) {
			replyError(w, errors.New("仅用户可读取角色模板"))
			return
		}
		reply(w, map[string]any{"templates": catalog.Templates(), "tools": catalog.ToolNames})
	})
	a.mux.HandleFunc("PUT /api/roles/{id}", func(w http.ResponseWriter, r *http.Request) {
		var v catalog.Role
		if err := decode(w, r, &v); err != nil {
			replyError(w, err)
			return
		}
		if v.ID != r.PathValue("id") {
			replyError(w, errors.New("角色 ID 不匹配"))
			return
		}
		saved, err := a.Store.SaveRole(r.Context(), principal(r), v)
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, saved)
	})
	a.mux.HandleFunc("DELETE /api/roles/{id}", func(w http.ResponseWriter, r *http.Request) {
		revision, err := strconv.Atoi(r.URL.Query().Get("revision"))
		if err != nil {
			replyError(w, errors.New("修订号无效"))
			return
		}
		if err = a.Store.DeleteRole(r.Context(), principal(r), r.PathValue("id"), revision); err != nil {
			replyError(w, err)
			return
		}
		reply(w, map[string]bool{"deleted": true})
	})
}
