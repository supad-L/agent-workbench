package store

import (
	"agent-workbench/internal/catalog"
	"agent-workbench/internal/runtime"
	"agent-workbench/internal/runtime/agent"
	"context"
	"database/sql"
	"encoding/json"
)

// ProbeProject is an internal accounting namespace, excluded from user project lists.
const ProbeProject = "AWB-CONNECTION-PROBES"

// CreateProbeRun accounts connection tests in the same budget/slot ledger as role trials.
func (s *Store) CreateProbeRun(ctx context.Context, p Principal, c catalog.Connection, key string) (Run, error) {
	if err := allowed(p, "", true); err != nil {
		return Run{}, err
	}
	if err := c.Validate(); err != nil {
		return Run{}, err
	}
	var selected catalog.Model
	for _, m := range c.Models {
		if m.ID == c.Config.Model {
			selected = m
			break
		}
	}
	if _, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO projects(id,name,source,snapshot,manifest,active_version,created_at,internal) VALUES(?,?,?,?,?,0,?,1)", ProbeProject, "连接诊断", "", "", []byte(`[]`), now()); err != nil {
		return Run{}, err
	}
	request := struct {
		Account     string
		Config      any
		Model       catalog.Model
		Concurrency int
	}{c.AccountID, c.Config, selected, c.MaxConcurrent}
	raw, err := s.write(ctx, p, ProbeProject, "probe.create", key, request, func(tx *sql.Tx) (any, error) {
		if err := accountAvailable(ctx, tx, c, ""); err != nil {
			return nil, err
		}
		role := catalog.Role{ID: "connection-probe", Name: "连接测试", Kind: "custom", SystemPrompt: "这是连接测试。先调用 probe 工具并提供 value=42，收到工具结果后简短确认。不要请求其他工具。", Primary: catalog.Choice{ConnectionID: c.ID, ModelID: c.Config.Model}, Enabled: true}
		run := Run{Kind: "connection_probe", ID: ID(), ProjectID: ProbeProject, ProjectName: "连接诊断", Role: role, Choices: []RunChoice{{Connection: c, Model: selected}}, Scope: "conversation_only", Status: "running", Input: "调用 probe 并确认工具结果。", State: agent.State{Messages: []runtime.Message{{Role: "user", Text: "调用 probe 并确认工具结果。"}}}, MaxTurns: 2, Retries: 0, CreatedAt: now(), UpdatedAt: now()}
		data, err := json.Marshal(run)
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO agent_runs VALUES(?,?,?,?,?,?,?)", run.ID, ProbeProject, role.ID, c.ID, c.AccountID, run.Status, data); err != nil {
			return nil, err
		}
		return run, nil
	})
	if err != nil {
		return Run{}, err
	}
	var run Run
	if err = json.Unmarshal(raw, &run); err != nil {
		return Run{}, err
	}
	return s.Run(ctx, p, ProbeProject, run.ID)
}

// ProbeUsageKnown reports whether every successful request supplied complete usage.
func (s *Store) ProbeUsageKnown(ctx context.Context, id string) (bool, error) {
	var total, known int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM(CASE WHEN classification='reported' THEN 1 ELSE 0 END),0) FROM usage_events WHERE run_id=?", id).Scan(&total, &known)
	return total > 0 && total == known, err
}
