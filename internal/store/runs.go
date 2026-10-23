package store

import (
	"agent-workbench/internal/catalog"
	"agent-workbench/internal/runtime"
	"agent-workbench/internal/runtime/agent"
	"agent-workbench/internal/usage"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const migration3 = `
CREATE TABLE agent_runs(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id),role_id TEXT NOT NULL,connection_id TEXT NOT NULL,account_id TEXT NOT NULL,status TEXT NOT NULL,data BLOB NOT NULL);
CREATE INDEX active_agent_accounts ON agent_runs(account_id,status);
CREATE TABLE usage_reservations(run_id TEXT NOT NULL REFERENCES agent_runs(id),sequence INTEGER NOT NULL,choice_index INTEGER NOT NULL,currency TEXT NOT NULL,amount INTEGER NOT NULL,state TEXT NOT NULL,price BLOB NOT NULL,PRIMARY KEY(run_id,sequence));
CREATE TABLE usage_events(run_id TEXT NOT NULL REFERENCES agent_runs(id),sequence INTEGER NOT NULL,classification TEXT NOT NULL,input_tokens INTEGER NOT NULL,output_tokens INTEGER NOT NULL,cost INTEGER,price BLOB NOT NULL,PRIMARY KEY(run_id,sequence));
CREATE TABLE budgets(currency TEXT PRIMARY KEY,revision INTEGER NOT NULL,enabled INTEGER NOT NULL,limit_micros INTEGER NOT NULL);
INSERT INTO schema_migrations VALUES(3);`

// RunInput is a human-requested role trial, never permission to execute project code.
type RunInput struct {
	RoleID              string         `json:"role_id"`
	RoleRevision        int            `json:"role_revision"`
	Input               string         `json:"input"`
	Scope               string         `json:"scope"`
	Key                 string         `json:"idempotency_key"`
	ConnectionRevisions map[string]int `json:"connection_revisions"`
}

// RunChoice snapshots configuration/model metadata; credential values are not persisted.
type RunChoice struct {
	Connection catalog.Connection `json:"connection"`
	Model      catalog.Model      `json:"model"`
}

// Run is the durable state of a bounded read-only role trial.
type Run struct {
	ID          string          `json:"id"`
	ProjectID   string          `json:"project_id"`
	ProjectName string          `json:"project_name"`
	Plan        json.RawMessage `json:"plan,omitempty"`
	Role        catalog.Role    `json:"role"`
	Choices     []RunChoice     `json:"choices"`
	ChoiceIndex int             `json:"choice_index"`
	Input       string          `json:"input"`
	Scope       string          `json:"scope"`
	Status      string          `json:"status"`
	State       agent.State     `json:"state"`
	MaxTurns    int             `json:"max_turns"`
	Retries     int             `json:"retries"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
	Preview     string          `json:"preview,omitempty"`
}

func readRun(ctx context.Context, q planQuerier, id string) (Run, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, "SELECT data FROM agent_runs WHERE id=?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	var run Run
	err = json.Unmarshal(raw, &run)
	return run, err
}
func writeRun(ctx context.Context, tx *sql.Tx, run Run) error {
	raw, err := json.Marshal(run)
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return errors.New("运行检查点超过上限")
	}
	c := run.Choices[run.ChoiceIndex].Connection
	_, err = tx.ExecContext(ctx, "UPDATE agent_runs SET connection_id=?,account_id=?,status=?,data=? WHERE id=?", c.ID, c.AccountID, run.Status, raw, run.ID)
	return err
}

func accountAvailable(ctx context.Context, tx *sql.Tx, c catalog.Connection, exceptRun string) error {
	limit := c.MaxConcurrent
	rows, err := tx.QueryContext(ctx, "SELECT data FROM connection_catalog WHERE account_id=?", c.AccountID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var raw []byte
		var alias catalog.Connection
		if err = rows.Scan(&raw); err != nil {
			break
		}
		if err = json.Unmarshal(raw, &alias); err != nil {
			break
		}
		if alias.MaxConcurrent < limit {
			limit = alias.MaxConcurrent
		}
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	var occupied int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_runs WHERE account_id=? AND status='running' AND id!=?", c.AccountID, exceptRun).Scan(&occupied); err != nil {
		return err
	}
	if occupied >= limit {
		return errors.New("同一账号的运行槽已占满，请等待当前运行结束")
	}
	return nil
}

// CreateRun atomically captures role, model, connection, plan and account concurrency limits.
func (s *Store) CreateRun(ctx context.Context, p Principal, project string, input RunInput) (Run, error) {
	if err := allowed(p, project, true); err != nil {
		return Run{}, err
	}
	if len(input.Input) == 0 || len(input.Input) > 16384 || input.Scope != "conversation_only" && input.Scope != "project_summary" {
		return Run{}, errors.New("试运行输入或资料范围无效")
	}
	raw, err := s.write(ctx, p, project, "run.create", input.Key, input, func(tx *sql.Tx) (any, error) {
		var roleData []byte
		if err := tx.QueryRowContext(ctx, "SELECT data FROM roles WHERE id=?", input.RoleID).Scan(&roleData); err != nil {
			return nil, ErrNotFound
		}
		var role catalog.Role
		if err := json.Unmarshal(roleData, &role); err != nil {
			return nil, err
		}
		if !role.Enabled || role.Revision != input.RoleRevision {
			return nil, ErrConflict
		}
		choices := []catalog.Choice{role.Primary}
		if role.Fallback.Enabled {
			choices = append(choices, role.Fallback.Candidates...)
		}
		run := Run{ID: ID(), ProjectID: project, Role: role, Input: input.Input, Scope: input.Scope, Status: "running", MaxTurns: 4, Retries: 1, CreatedAt: now(), UpdatedAt: now(), Choices: []RunChoice{}, State: agent.State{Messages: []runtime.Message{{Role: "user", Text: input.Input}}}}
		for _, choice := range choices {
			var data []byte
			if err := tx.QueryRowContext(ctx, "SELECT data FROM connection_catalog WHERE id=?", choice.ConnectionID).Scan(&data); err != nil {
				return nil, ErrNotFound
			}
			var c catalog.Connection
			if err := json.Unmarshal(data, &c); err != nil {
				return nil, err
			}
			if !c.Enabled || c.Revision != input.ConnectionRevisions[c.ID] {
				return nil, ErrConflict
			}
			if c.Config.AuthKind != "none" && c.Config.AuthKind != "" && !c.HasKey {
				return nil, errors.New("连接尚未配置 API Key")
			}
			if c.Config.Egress.Mode == "enforced_proxy" {
				return nil, errors.New("强制出口隔离尚未就绪")
			}
			found := false
			for _, model := range c.Models {
				if model.ID == choice.ModelID {
					if len(role.Tools) > 0 && model.Tools == "unsupported" {
						return nil, errors.New("该角色需要工具能力，所选模型仅支持文本")
					}
					run.Choices = append(run.Choices, RunChoice{Connection: c, Model: model})
					found = true
					break
				}
			}
			if !found {
				return nil, errors.New("角色绑定的模型已移除，请重新选择")
			}
		}
		var active int
		if err := tx.QueryRowContext(ctx, "SELECT name,active_version FROM projects WHERE id=?", project).Scan(&run.ProjectName, &active); err != nil {
			return nil, ErrNotFound
		}
		if input.Scope == "project_summary" && active > 0 {
			plan, err := getPlan(ctx, tx, project, active)
			if err != nil {
				return nil, err
			}
			run.Plan = plan.Content
		}
		if err := accountAvailable(ctx, tx, run.Choices[0].Connection, ""); err != nil {
			return nil, err
		}
		data, err := json.Marshal(run)
		if err != nil {
			return nil, err
		}
		c := run.Choices[0].Connection
		if _, err = tx.ExecContext(ctx, "INSERT INTO agent_runs VALUES(?,?,?,?,?,?,?)", run.ID, project, role.ID, c.ID, c.AccountID, run.Status, data); err != nil {
			return nil, err
		}
		if err = event(ctx, tx, project, "run.started", map[string]string{"run_id": run.ID, "role_id": role.ID}); err != nil {
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
	return s.Run(ctx, p, project, run.ID)
}

// Run returns one project-scoped record without credential material.
func (s *Store) Run(ctx context.Context, p Principal, project, id string) (Run, error) {
	if err := allowed(p, project, false); err != nil {
		return Run{}, err
	}
	run, err := readRun(ctx, s.db, id)
	if err != nil {
		return Run{}, err
	}
	if run.ProjectID != project {
		return Run{}, ErrForbidden
	}
	return run, nil
}

// Runs lists recent role trials, preserving their original snapshots after role edits.
func (s *Store) Runs(ctx context.Context, p Principal, project string) (result []Run, returnErr error) {
	if err := allowed(p, project, false); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT data FROM agent_runs WHERE project_id=? ORDER BY rowid DESC LIMIT 50", project)
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	result = []Run{}
	for rows.Next() {
		var raw []byte
		var r Run
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// CheckRunConfiguration rejects stale credentials/configurations before every turn and tool.
func (s *Store) CheckRunConfiguration(ctx context.Context, run Run) error {
	var roleRevision int
	if err := s.db.QueryRowContext(ctx, "SELECT revision FROM roles WHERE id=?", run.Role.ID).Scan(&roleRevision); err != nil {
		return ErrConflict
	}
	if roleRevision != run.Role.Revision {
		return ErrConflict
	}
	for _, choice := range run.Choices {
		var revision int
		if err := s.db.QueryRowContext(ctx, "SELECT revision FROM connection_catalog WHERE id=?", choice.Connection.ID).Scan(&revision); err != nil {
			return ErrConflict
		}
		if revision != choice.Connection.Revision {
			return ErrConflict
		}
	}
	return nil
}

// CheckpointRun persists a complete tool boundary. It never grants task acceptance.
func (s *Store) CheckpointRun(ctx context.Context, id string, state agent.State) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		run, err := readRun(ctx, tx, id)
		if err != nil {
			return err
		}
		if run.Status != "running" {
			return ErrConflict
		}
		run.State = state
		run.UpdatedAt = now()
		return writeRun(ctx, tx, run)
	})
}

// PreviewRun stores bounded non-authoritative progress without logging tool arguments.
func (s *Store) PreviewRun(ctx context.Context, id, preview string, progress agent.Progress) error {
	if len(preview) > 65536 {
		return errors.New("运行预览过大")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		run, err := readRun(ctx, tx, id)
		if err != nil {
			return err
		}
		if run.Status != "running" {
			return ErrConflict
		}
		run.Preview = preview
		run.UpdatedAt = now()
		if err = writeRun(ctx, tx, run); err != nil {
			return err
		}
		return event(ctx, tx, run.ProjectID, "run.progress", map[string]any{"run_id": id, "type": progress.Type, "tool": progress.Tool, "turn": progress.Turn})
	})
	if err == nil {
		s.notify()
	}
	return err
}

// SwitchRunChoice claims the fallback account before releasing the old account slot.
func (s *Store) SwitchRunChoice(ctx context.Context, id string, index int, state agent.State) (Run, error) {
	var switched Run
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		run, err := readRun(ctx, tx, id)
		if err != nil {
			return err
		}
		if run.Status != "running" || index != run.ChoiceIndex+1 || index >= len(run.Choices) || !run.Role.Fallback.Enabled {
			return ErrConflict
		}
		if err = accountAvailable(ctx, tx, run.Choices[index].Connection, id); err != nil {
			return err
		}
		run.ChoiceIndex = index
		if run.Scope != "conversation_only" {
			run.Scope = run.Role.Fallback.DataScope
		}
		run.State = state
		run.UpdatedAt = now()
		switched = run
		return writeRun(ctx, tx, run)
	})
	return switched, err
}

// FinishRun releases execution slots while preserving all usage reservations for settlement.
func (s *Store) FinishRun(ctx context.Context, id, status, message string, state agent.State) error {
	if status != "completed" && status != "failed" && status != "cancelled" && status != "interrupted" {
		return errors.New("终态无效")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		run, err := readRun(ctx, tx, id)
		if err != nil {
			return err
		}
		if run.Status != "running" {
			return nil
		}
		run.Status = status
		run.Error = message
		run.State = state
		run.UpdatedAt = now()
		if err = writeRun(ctx, tx, run); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE usage_reservations SET state='uncertain' WHERE run_id=? AND state='active'", id); err != nil {
			return err
		}
		return event(ctx, tx, run.ProjectID, "run.finished", map[string]string{"run_id": id, "status": status})
	})
	if err == nil {
		s.notify()
	}
	return err
}

// RecoverRuns marks interrupted work without automatically repeating a paid model request.
func (s *Store) RecoverRuns(ctx context.Context) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT data FROM agent_runs WHERE status='running'")
		if err != nil {
			return err
		}
		runs := []Run{}
		for rows.Next() {
			var raw []byte
			var run Run
			if err = rows.Scan(&raw); err != nil {
				break
			}
			if err = json.Unmarshal(raw, &run); err != nil {
				break
			}
			runs = append(runs, run)
		}
		err = errors.Join(err, rows.Err(), rows.Close())
		if err != nil {
			return err
		}
		for _, run := range runs {
			run.Status = "interrupted"
			run.Error = "服务重启中断，未自动重复付费请求"
			run.UpdatedAt = now()
			if err = writeRun(ctx, tx, run); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, "UPDATE usage_reservations SET state='uncertain' WHERE state='active'")
		return err
	})
}

// ResumeRun is explicit and preserves the request counter and original configuration snapshots.
func (s *Store) ResumeRun(ctx context.Context, p Principal, project, id string) (Run, error) {
	if err := allowed(p, project, true); err != nil {
		return Run{}, err
	}
	var resumed Run
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		run, err := readRun(ctx, tx, id)
		if err != nil {
			return err
		}
		if run.ProjectID != project {
			return ErrForbidden
		}
		if run.Status == "completed" || run.Status == "running" {
			resumed = run
			return nil
		}
		if run.State.Attempts >= run.MaxTurns*(run.Retries+1) {
			return errors.New("累计请求次数已用尽，请新建试运行")
		}
		if err = accountAvailable(ctx, tx, run.Choices[run.ChoiceIndex].Connection, id); err != nil {
			return err
		}
		run.Status = "running"
		run.Error = ""
		run.UpdatedAt = now()
		resumed = run
		return writeRun(ctx, tx, run)
	})
	return resumed, err
}

// Budget is a custom local cap, distinct from subscription quota or provider balance.
type Budget struct {
	Currency string `json:"currency"`
	Revision int    `json:"revision"`
	Enabled  bool   `json:"enabled"`
	Limit    int64  `json:"limit_micros"`
}

// UsageSummary keeps known expenses, uncertain holds and unpriced requests separate.
type UsageSummary struct {
	Currency         string `json:"currency"`
	Cost             int64  `json:"cost_micros"`
	Held             int64  `json:"held_micros"`
	KnownInput       int64  `json:"known_input_tokens"`
	KnownOutput      int64  `json:"known_output_tokens"`
	EstimatedInput   int64  `json:"estimated_input_tokens"`
	EstimatedOutput  int64  `json:"estimated_output_tokens"`
	UnknownRequests  int    `json:"unknown_requests"`
	UnpricedRequests int    `json:"unpriced_requests"`
}

// SaveBudget uses a revision check and refuses a limit below already known spending/holds.
func (s *Store) SaveBudget(ctx context.Context, p Principal, b Budget) (Budget, error) {
	if err := allowed(p, "", true); err != nil {
		return Budget{}, err
	}
	if b.Currency != "CNY" && b.Currency != "USD" || b.Limit < 0 || b.Limit > 1000000000000 || b.Enabled && b.Limit == 0 {
		return Budget{}, errors.New("预算币种或金额无效")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var revision int
		err := tx.QueryRowContext(ctx, "SELECT revision FROM budgets WHERE currency=?", b.Currency).Scan(&revision)
		if errors.Is(err, sql.ErrNoRows) {
			if b.Revision != 0 {
				return ErrConflict
			}
		} else if err != nil {
			return err
		} else if revision != b.Revision {
			return ErrConflict
		}
		spent, held, err := budgetTotals(ctx, tx, b.Currency)
		if err != nil {
			return err
		}
		if b.Enabled && b.Limit < spent+held {
			return errors.New("预算低于已记录费用与未确认预留，请先核对用量")
		}
		b.Revision++
		_, err = tx.ExecContext(ctx, "INSERT INTO budgets VALUES(?,?,?,?) ON CONFLICT(currency) DO UPDATE SET revision=excluded.revision,enabled=excluded.enabled,limit_micros=excluded.limit_micros", b.Currency, b.Revision, b.Enabled, b.Limit)
		return err
	})
	return b, err
}

func budgetTotals(ctx context.Context, q planQuerier, currency string) (spent, held int64, err error) {
	if err = q.QueryRowContext(ctx, "SELECT COALESCE(SUM(cost),0) FROM usage_events e JOIN usage_reservations r USING(run_id,sequence) WHERE r.currency=?", currency).Scan(&spent); err != nil {
		return 0, 0, err
	}
	err = q.QueryRowContext(ctx, "SELECT COALESCE(SUM(amount),0) FROM usage_reservations WHERE currency=? AND state!='settled'", currency).Scan(&held)
	return spent, held, err
}

// BeginAttempt reserves a conservative per-request maximum before any network call.
func (s *Store) BeginAttempt(ctx context.Context, id string, attempt agent.Attempt) error {
	if attempt.Sequence < 1 || attempt.InputBytes < 1 || attempt.InputBytes > agent.MaxInputBytes || attempt.MaxOutputTokens < 1 || attempt.MaxOutputTokens > 4096 {
		return errors.New("请求计量边界无效")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		run, err := readRun(ctx, tx, id)
		if err != nil {
			return err
		}
		if run.Status != "running" {
			return ErrConflict
		}
		price := usage.Snapshot(run.Choices[run.ChoiceIndex].Model)
		maximum, known, err := price.Cost(agent.MaxInputBytes, int64(attempt.MaxOutputTokens))
		if err != nil {
			return err
		}
		var enabled int
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM budgets WHERE enabled=1").Scan(&enabled); err != nil {
			return err
		}
		if enabled > 0 && !known {
			return errors.New("已启用自定义预算，但模型价格未知，不能估算并预约本次费用")
		}
		if known {
			var cap int64
			err = tx.QueryRowContext(ctx, "SELECT limit_micros FROM budgets WHERE currency=? AND enabled=1", price.Currency).Scan(&cap)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil {
				spent, held, err := budgetTotals(ctx, tx, price.Currency)
				if err != nil {
					return err
				}
				if maximum > cap-spent-held {
					return errors.New("剩余自定义预算不足以预约本次请求")
				}
			}
		}
		if run.ChoiceIndex > 0 {
			if !known || price.Currency != run.Role.Fallback.Currency {
				return errors.New("备用模型价格未知或币种与备用预算不一致")
			}
			var used int64
			if err = tx.QueryRowContext(ctx, "SELECT COALESCE(SUM(CASE WHEN r.state='settled' THEN COALESCE(e.cost,0) ELSE r.amount END),0) FROM usage_reservations r LEFT JOIN usage_events e USING(run_id,sequence) WHERE r.run_id=? AND r.choice_index>0", id).Scan(&used); err != nil {
				return err
			}
			if maximum > run.Role.Fallback.MaxExtraCost-used {
				return errors.New("备用策略的额外费用上限不足")
			}
		}
		raw, err := json.Marshal(price)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO usage_reservations VALUES(?,?,?,?,?,'active',?)", id, attempt.Sequence, run.ChoiceIndex, price.Currency, maximum, raw)
		return err
	})
}

// SettleAttempt is idempotent. Unknown outcomes retain their reservation instead of becoming free.
func (s *Store) SettleAttempt(ctx context.Context, id string, attempt agent.Attempt, u *runtime.Usage) error {
	sequence := attempt.Sequence
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var raw []byte
		if err := tx.QueryRowContext(ctx, "SELECT price FROM usage_reservations WHERE run_id=? AND sequence=?", id, sequence).Scan(&raw); err != nil {
			return err
		}
		var price usage.Price
		if err := json.Unmarshal(raw, &price); err != nil {
			return err
		}
		classification := "unknown"
		var input, output int64
		var cost any
		state := "uncertain"
		if u != nil && !u.Known {
			classification = "estimated"
			input = int64((attempt.InputBytes + 2) / 3)
			output = int64((attempt.OutputBytes + 2) / 3)
		}
		if u != nil && u.Known {
			classification = "reported"
			input, output = u.InputTokens, u.OutputTokens
			amount, known, err := price.Cost(input, output)
			if err != nil {
				return err
			}
			if known {
				cost = amount
				state = "settled"
			}
		}
		var existing string
		var oldInput, oldOutput int64
		err := tx.QueryRowContext(ctx, "SELECT classification,input_tokens,output_tokens FROM usage_events WHERE run_id=? AND sequence=?", id, sequence).Scan(&existing, &oldInput, &oldOutput)
		if err == nil {
			if existing != classification || oldInput != input || oldOutput != output {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO usage_events VALUES(?,?,?,?,?,?,?)", id, sequence, classification, input, output, cost, raw); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE usage_reservations SET state=? WHERE run_id=? AND sequence=?", state, id, sequence)
		return err
	})
}

// Usage returns two currency totals and an explicit unpriced bucket, plus custom budgets.
func (s *Store) Usage(ctx context.Context, p Principal) (summaries []UsageSummary, budgets []Budget, returnErr error) {
	if err := allowed(p, "", true); err != nil {
		return nil, nil, err
	}
	for _, currency := range []string{"CNY", "USD", ""} {
		summary := UsageSummary{Currency: currency}
		spent, held, err := budgetTotals(ctx, s.db, currency)
		if err != nil {
			return nil, nil, err
		}
		summary.Cost, summary.Held = spent, held
		if err = s.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(CASE WHEN e.classification='reported' THEN e.input_tokens ELSE 0 END),0),COALESCE(SUM(CASE WHEN e.classification='reported' THEN e.output_tokens ELSE 0 END),0),COALESCE(SUM(CASE WHEN e.classification='estimated' THEN e.input_tokens ELSE 0 END),0),COALESCE(SUM(CASE WHEN e.classification='estimated' THEN e.output_tokens ELSE 0 END),0),COALESCE(SUM(CASE WHEN e.classification='unknown' OR e.classification IS NULL THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN e.cost IS NULL THEN 1 ELSE 0 END),0) FROM usage_reservations r LEFT JOIN usage_events e USING(run_id,sequence) WHERE r.currency=?", currency).Scan(&summary.KnownInput, &summary.KnownOutput, &summary.EstimatedInput, &summary.EstimatedOutput, &summary.UnknownRequests, &summary.UnpricedRequests); err != nil {
			return nil, nil, err
		}
		summaries = append(summaries, summary)
	}
	rows, err := s.db.QueryContext(ctx, "SELECT currency,revision,enabled,limit_micros FROM budgets ORDER BY currency")
	if err != nil {
		return nil, nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	budgets = []Budget{}
	for rows.Next() {
		var b Budget
		if err = rows.Scan(&b.Currency, &b.Revision, &b.Enabled, &b.Limit); err != nil {
			return nil, nil, err
		}
		budgets = append(budgets, b)
	}
	return summaries, budgets, rows.Err()
}
