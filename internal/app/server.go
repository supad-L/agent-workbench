// Package app exposes the local M1 service; human credentials never enter Agent tools.
package app

import (
	"agent-workbench/contracts"
	"agent-workbench/internal/apiconfig"
	"agent-workbench/internal/project"
	"agent-workbench/internal/runtime/direct"
	"agent-workbench/internal/store"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type principalKey struct{}

// Server owns durable state and short-lived role-bound credentials.
type Server struct {
	Store      *store.Store
	DataDir    string
	HumanToken string
	mu         sync.Mutex
	agents     map[string]store.Principal
	mux        *http.ServeMux
}

// Open initializes one local application instance.
func Open(ctx context.Context, dataDir string) (*Server, error) {
	s, err := store.Open(ctx, dataDir)
	if err != nil {
		return nil, err
	}
	a := &Server{Store: s, DataDir: dataDir, HumanToken: store.ID() + store.ID(), agents: map[string]store.Principal{}, mux: http.NewServeMux()}
	a.routes()
	return a, nil
}

// Handler rejects unauthenticated and cross-origin API access.
func (a *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			replyError(w, store.ErrForbidden)
			return
		}
		if r.URL.Path == "/healthz" {
			if r.Method != http.MethodGet {
				w.WriteHeader(405)
				return
			}
			if err := a.Store.CheckHealth(r.Context()); err != nil {
				replyError(w, err)
				return
			}
			reply(w, map[string]any{"status": "ok", "schema_version": 1, "stage": "M1"})
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		var p store.Principal
		if subtle.ConstantTimeCompare([]byte(token), []byte(a.HumanToken)) == 1 {
			p = store.Principal{ID: "desktop-human", Role: "human"}
		} else {
			a.mu.Lock()
			p = a.agents[token]
			a.mu.Unlock()
		}
		if p.ID == "" {
			w.WriteHeader(401)
			return
		}
		a.mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	})
}
func principal(r *http.Request) store.Principal {
	p, _ := r.Context().Value(principalKey{}).(store.Principal)
	return p
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errors.New("请求需要 application/json")
	}
	reader := http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(reader)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return errors.New("请求 JSON 无效或包含未知字段")
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		return errors.New("请求只能包含一个 JSON 对象")
	}
	return nil
}
func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		return
	}
}
func replyError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, store.ErrForbidden) {
		status = 403
	}
	if errors.Is(err, store.ErrConflict) {
		status = 409
	}
	if errors.Is(err, store.ErrNotFound) {
		status = 404
	}
	message := err.Error()
	var failure *direct.Failure
	if errors.As(err, &failure) {
		message = fmt.Sprintf("模型请求失败：%s，HTTP %d", failure.Kind, failure.Status)
	}
	if strings.Contains(message, "sqlite") || strings.Contains(message, "constraint") || strings.Contains(message, "database") {
		message = "本地数据操作失败，请刷新后重试"
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	reply(w, map[string]any{"error": message})
}
func human(r *http.Request) bool { return principal(r).Role == "human" }

func (a *Server) routes() {
	a.mux.HandleFunc("GET /api/projects", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.Projects(r.Context(), principal(r))
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, v)
	})
	a.mux.HandleFunc("POST /api/projects", a.createProject)
	a.mux.HandleFunc("GET /api/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		p := principal(r)
		id := r.PathValue("id")
		v, err := a.Store.Project(r.Context(), p, id)
		if err != nil {
			replyError(w, err)
			return
		}
		messages, err := a.Store.Messages(r.Context(), p, id)
		if err != nil {
			replyError(w, err)
			return
		}
		plans, err := a.Store.Plans(r.Context(), p, id)
		if err != nil {
			replyError(w, err)
			return
		}
		docs, err := a.Store.Documents(r.Context(), p, id)
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, map[string]any{"project": v, "messages": messages, "plans": plans, "documents": docs})
	})
	a.mux.HandleFunc("POST /api/projects/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Text string `json:"text"`
			Key  string `json:"idempotency_key"`
		}
		if err := decode(w, r, &b); err != nil {
			replyError(w, err)
			return
		}
		v, err := a.Store.AppendMessage(r.Context(), principal(r), r.PathValue("id"), b.Key, b.Text)
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, v)
	})
	a.mux.HandleFunc("POST /api/projects/{id}/plans", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Content json.RawMessage `json:"content"`
			Reason  string          `json:"reason"`
			Base    int             `json:"base_version"`
			Key     string          `json:"idempotency_key"`
		}
		if err := decode(w, r, &b); err != nil {
			replyError(w, err)
			return
		}
		v, err := a.Store.CreatePlan(r.Context(), principal(r), r.PathValue("id"), b.Key, b.Reason, b.Base, b.Content)
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, v)
	})
	a.mux.HandleFunc("PUT /api/projects/{id}/plans/{version}", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Content  json.RawMessage `json:"content"`
			Hash     string          `json:"expected_hash"`
			Revision int             `json:"expected_revision"`
			Key      string          `json:"idempotency_key"`
		}
		version, err := strconv.Atoi(r.PathValue("version"))
		if err != nil {
			replyError(w, errors.New("版本无效"))
			return
		}
		if err = decode(w, r, &b); err != nil {
			replyError(w, err)
			return
		}
		v, err := a.Store.UpdatePlan(r.Context(), principal(r), r.PathValue("id"), b.Key, version, b.Revision, b.Hash, b.Content)
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, v)
	})
	a.mux.HandleFunc("POST /api/projects/{id}/plans/{version}/confirm", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Hash     string `json:"expected_hash"`
			Revision int    `json:"expected_revision"`
			Key      string `json:"idempotency_key"`
		}
		version, err := strconv.Atoi(r.PathValue("version"))
		if err != nil {
			replyError(w, errors.New("版本无效"))
			return
		}
		if err = decode(w, r, &b); err != nil {
			replyError(w, err)
			return
		}
		v, err := a.Store.ConfirmPlan(r.Context(), principal(r), r.PathValue("id"), b.Key, version, b.Revision, b.Hash)
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, v)
	})
	a.mux.HandleFunc("POST /api/projects/{id}/documents", a.addDocument)
	a.mux.HandleFunc("GET /api/projects/{id}/events", a.events)
	a.mux.HandleFunc("POST /api/projects/{id}/agent-token", func(w http.ResponseWriter, r *http.Request) {
		if !human(r) {
			replyError(w, store.ErrForbidden)
			return
		}
		if _, err := a.Store.Project(r.Context(), principal(r), r.PathValue("id")); err != nil {
			replyError(w, err)
			return
		}
		token := store.ID() + store.ID()
		a.mu.Lock()
		a.agents[token] = store.Principal{ID: store.ID(), Role: "agent", ProjectID: r.PathValue("id")}
		a.mu.Unlock()
		reply(w, map[string]string{"token": token})
	})
	a.mux.HandleFunc("POST /api/projects/{id}/permission-demo", func(w http.ResponseWriter, r *http.Request) {
		if !human(r) {
			replyError(w, store.ErrForbidden)
			return
		}
		id := r.PathValue("id")
		if _, err := a.Store.Project(r.Context(), principal(r), id); err != nil {
			replyError(w, err)
			return
		}
		_, err := a.Store.ConfirmPlan(r.Context(), store.Principal{ID: "permission-demo-agent", Role: "agent", ProjectID: id}, id, store.ID(), 1, 1, "untrusted")
		if !errors.Is(err, store.ErrForbidden) {
			replyError(w, errors.New("权限验证失败"))
			return
		}
		reply(w, map[string]any{"agent_can_confirm": false, "status": 403, "evidence": "authenticated_role_guard"})
	})
	a.mux.HandleFunc("GET /api/plan-template", func(w http.ResponseWriter, r *http.Request) {
		if !human(r) {
			replyError(w, store.ErrForbidden)
			return
		}
		raw, err := contracts.Files.ReadFile("examples/plan.json")
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, json.RawMessage(raw))
	})
	a.mux.HandleFunc("POST /api/connections/check", func(w http.ResponseWriter, r *http.Request) {
		if !human(r) {
			replyError(w, store.ErrForbidden)
			return
		}
		var b apiconfig.Config
		if err := decode(w, r, &b); err != nil {
			replyError(w, err)
			return
		}
		v, err := b.Validate()
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, v)
	})
	a.mux.HandleFunc("POST /api/connections/test", func(w http.ResponseWriter, r *http.Request) {
		if !human(r) {
			replyError(w, store.ErrForbidden)
			return
		}
		var b struct {
			Config apiconfig.Config  `json:"config"`
			Values map[string]string `json:"values"`
		}
		if err := decode(w, r, &b); err != nil {
			replyError(w, err)
			return
		}
		v, err := b.Config.Run(r.Context(), func(ref string) string { return b.Values[ref] })
		if err != nil {
			replyError(w, err)
			return
		}
		reply(w, v)
	})
}

func (a *Server) createProject(w http.ResponseWriter, r *http.Request) {
	if !human(r) {
		replyError(w, store.ErrForbidden)
		return
	}
	var b struct {
		Name   string `json:"name"`
		Source string `json:"source"`
		Key    string `json:"idempotency_key"`
	}
	if err := decode(w, r, &b); err != nil {
		replyError(w, err)
		return
	}
	prepared, err := (project.Preparer{Root: filepath.Join(a.DataDir, "projects")}).Prepare(r.Context(), b.Name, b.Source)
	if err != nil {
		replyError(w, err)
		return
	}
	result, err := a.Store.CreateProject(r.Context(), principal(r), b.Key, prepared)
	var saved store.Project
	if err == nil {
		err = json.Unmarshal(result, &saved)
	}
	if err != nil || saved.ID != prepared.ID {
		if cleanupErr := os.RemoveAll(filepath.Dir(prepared.Snapshot)); cleanupErr != nil {
			replyError(w, errors.New("未使用的导入副本清理失败"))
			return
		}
	}
	if err != nil {
		replyError(w, err)
		return
	}
	reply(w, result)
}
func (a *Server) addDocument(w http.ResponseWriter, r *http.Request) {
	if !human(r) {
		replyError(w, store.ErrForbidden)
		return
	}
	id := r.PathValue("id")
	if _, err := a.Store.Project(r.Context(), principal(r), id); err != nil {
		replyError(w, err)
		return
	}
	var b struct {
		Path string `json:"path"`
		Key  string `json:"idempotency_key"`
	}
	if err := decode(w, r, &b); err != nil {
		replyError(w, err)
		return
	}
	doc, err := project.ReadDocument(r.Context(), b.Path, filepath.Join(a.DataDir, "projects", id, "documents"))
	if err != nil {
		replyError(w, err)
		return
	}
	result, err := a.Store.AddDocument(r.Context(), principal(r), id, b.Key, doc)
	if err != nil {
		replyError(w, err)
		return
	}
	reply(w, result)
}
func (a *Server) events(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := principal(r)
	if _, err := a.Store.Project(r.Context(), p, id); err != nil {
		replyError(w, err)
		return
	}
	cursorText := r.URL.Query().Get("cursor")
	if cursorText == "" {
		cursorText = r.Header.Get("Last-Event-ID")
	}
	var cursor int64
	if cursorText != "" {
		var err error
		cursor, err = strconv.ParseInt(cursorText, 10, 64)
		if err != nil || cursor < 0 {
			replyError(w, errors.New("事件游标无效"))
			return
		}
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		replyError(w, errors.New("当前连接不支持事件流"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	flusher.Flush()
	heartbeat := time.NewTicker(12 * time.Second)
	defer heartbeat.Stop()
	for {
		watch := a.Store.Watch()
		events, err := a.Store.Events(r.Context(), p, id, cursor)
		if err != nil {
			return
		}
		for _, v := range events {
			raw, err := json.Marshal(v)
			if err != nil {
				return
			}
			if _, err = fmt.Fprintf(w, "id: %d\nevent: update\ndata: %s\n\n", v.Sequence, raw); err != nil {
				return
			}
			cursor = v.Sequence
		}
		flusher.Flush()
		if len(events) == 256 {
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case <-watch:
		case <-heartbeat.C:
			if _, err = io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
