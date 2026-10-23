package app

import (
	"agent-workbench/internal/apiconfig"
	"agent-workbench/internal/catalog"
	"agent-workbench/internal/store"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

func (a *Server) probeConnection(w http.ResponseWriter, r *http.Request) {
	if !human(r) {
		replyError(w, store.ErrForbidden)
		return
	}
	var b struct {
		Config     apiconfig.Config   `json:"config"`
		Connection catalog.Connection `json:"connection"`
		Values     map[string]string  `json:"values"`
		Key        string             `json:"idempotency_key"`
	}
	if err := decode(w, r, &b); err != nil {
		replyError(w, err)
		return
	}
	left, err := json.Marshal(b.Config)
	if err != nil {
		replyError(w, err)
		return
	}
	right, err := json.Marshal(b.Connection.Config)
	if err != nil {
		replyError(w, err)
		return
	}
	if !bytes.Equal(left, right) {
		replyError(w, errors.New("连接测试的配置与费用元数据不一致"))
		return
	}
	summary, err := b.Config.Validate()
	if err != nil {
		replyError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(summary.TimeoutSeconds)*time.Second)
	defer cancel()
	run, err := a.Runner.StartProbe(ctx, principal(r), b.Connection, b.Key, b.Values)
	if err != nil {
		replyError(w, err)
		return
	}
	for run.Status == "running" {
		watch := a.Store.Watch()
		run, err = a.Store.Run(ctx, principal(r), store.ProbeProject, run.ID)
		if err != nil {
			break
		}
		if run.Status != "running" {
			break
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-watch:
		}
		if err != nil {
			break
		}
	}
	if err != nil {
		stop, stopCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
		defer stopCancel()
		cancelErr := a.Runner.Cancel(stop, principal(r), store.ProbeProject, run.ID)
		replyError(w, errors.Join(errors.New("连接测试中断，已请求停止并保留用量记录"), cancelErr))
		return
	}
	if run.Status != "completed" {
		replyError(w, errors.New(run.Error))
		return
	}
	known, err := a.Store.ProbeUsageKnown(ctx, run.ID)
	if err != nil {
		replyError(w, err)
		return
	}
	reply(w, map[string]any{"protocol": b.Config.Endpoint.Protocol, "evidence": "explicitly_configured_endpoint", "requests": run.State.Attempts, "tool_verified": true, "text": "", "usage_known": known, "run_id": run.ID})
}
