package store

import (
	"agent-workbench/internal/catalog"
	"agent-workbench/internal/runtime"
	"agent-workbench/internal/runtime/agent"
	"agent-workbench/internal/workspace"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

// Task runs reuse the agent_runs table with kind "task"; no migration is needed.
const (
	TaskMaxTurns    = 30
	taskMaxRunBytes = 4 << 20
)

// TaskSpec is the task contract captured when a human starts a development task.
// It never authorizes writing outside WriteDirs or touching the original project.
type TaskSpec struct {
	Title        string             `json:"title"`
	Instructions string             `json:"instructions"`
	WriteDirs    []string           `json:"write_dirs"`
	Runtime      string             `json:"runtime,omitempty"`
	Image        string             `json:"image,omitempty"`
	Changes      []workspace.Change `json:"changes,omitempty"`
}

// Scope converts the contract into a workspace scope over the complete task copy.
func (t TaskSpec) Scope() workspace.Scope {
	return workspace.Scope{ReadPaths: []string{"."}, WriteDirs: t.WriteDirs}
}

// TaskInput is a human request to run one task with one role.
type TaskInput struct {
	RoleID              string         `json:"role_id"`
	RoleRevision        int            `json:"role_revision"`
	Title               string         `json:"title"`
	Instructions        string         `json:"instructions"`
	WriteDirs           []string       `json:"write_dirs"`
	Runtime             string         `json:"runtime"`
	Image               string         `json:"image"`
	Key                 string         `json:"idempotency_key"`
	ConnectionRevisions map[string]int `json:"connection_revisions"`
}

// NormalizeTask validates and canonicalizes a task contract.
func NormalizeTask(input TaskInput) (TaskSpec, error) {
	title := strings.TrimSpace(input.Title)
	instructions := strings.TrimSpace(input.Instructions)
	if title == "" || utf8.RuneCountInString(title) > 160 || instructions == "" || len(instructions) > 16384 {
		return TaskSpec{}, errors.New("请填写任务标题与说明（说明不超过 16 KiB）")
	}
	if len(input.WriteDirs) == 0 || len(input.WriteDirs) > 8 {
		return TaskSpec{}, errors.New("请选择 1–8 个允许修改的目录")
	}
	dirs := []string{}
	for _, dir := range input.WriteDirs {
		dir = strings.Trim(strings.TrimSpace(strings.ReplaceAll(dir, "\\", "/")), "/")
		if dir == "" || dir == "." {
			return TaskSpec{}, errors.New("不能把整个项目设为可写目录，请选择具体子目录")
		}
		dirs = append(dirs, dir+"/")
	}
	spec := TaskSpec{Title: title, Instructions: instructions, WriteDirs: dirs}
	if err := spec.Scope().Validate(); err != nil {
		return TaskSpec{}, errors.New("允许修改的目录无效、重复或相互嵌套")
	}
	switch {
	case input.Image == "" && input.Runtime == "":
	case input.Runtime != "docker" && input.Runtime != "podman":
		return TaskSpec{}, errors.New("验证命令需要明确选择 Docker 或 Podman")
	default:
		image := strings.TrimSpace(input.Image)
		if !strings.HasPrefix(image, "sha256:") {
			image = "sha256:" + image
		}
		if _, err := hex.DecodeString(strings.TrimPrefix(image, "sha256:")); err != nil || len(image) != 71 {
			return TaskSpec{}, errors.New("验证命令镜像需填写本地镜像的完整 ID（sha256:…）")
		}
		spec.Runtime, spec.Image = input.Runtime, image
	}
	return spec, nil
}

// TaskPrompt is the first user message of a task run.
func TaskPrompt(spec TaskSpec) string {
	return "任务：" + spec.Title + "\n\n" + spec.Instructions + "\n\n允许修改的目录：" + strings.Join(spec.WriteDirs, "、")
}

// CreateTaskRun captures role, primary connection, confirmed plan and the task contract.
// Tasks use the primary connection only, so project files are never forwarded to a fallback provider.
func (s *Store) CreateTaskRun(ctx context.Context, p Principal, project string, input TaskInput) (Run, error) {
	if err := allowed(p, project, true); err != nil {
		return Run{}, err
	}
	spec, err := NormalizeTask(input)
	if err != nil {
		return Run{}, err
	}
	raw, err := s.write(ctx, p, project, "task.create", input.Key, input, func(tx *sql.Tx) (any, error) {
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
		if !slices.Contains(role.Tools, "read_file") || !slices.Contains(role.Tools, "write_file") && !slices.Contains(role.Tools, "apply_patch") {
			return nil, errors.New("请为执行角色启用“读取任务文件”和“写入任务文件”或“提交范围内补丁”")
		}
		var data []byte
		if err := tx.QueryRowContext(ctx, "SELECT data FROM connection_catalog WHERE id=?", role.Primary.ConnectionID).Scan(&data); err != nil {
			return nil, errors.New("请先为角色绑定模型连接")
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
		var model *catalog.Model
		for i := range c.Models {
			if c.Models[i].ID == role.Primary.ModelID {
				model = &c.Models[i]
			}
		}
		if model == nil {
			return nil, errors.New("角色绑定的模型已移除，请重新选择")
		}
		if model.Tools == "unsupported" {
			return nil, errors.New("该角色需要工具能力，所选模型仅支持文本")
		}
		prompt := TaskPrompt(spec)
		run := Run{Kind: "task", ID: ID(), ProjectID: project, Role: role, Input: prompt, Scope: "project_files", Status: "running", MaxTurns: TaskMaxTurns, Retries: 1, CreatedAt: now(), UpdatedAt: now(),
			Choices: []RunChoice{{Connection: c, Model: *model}}, Task: &spec, State: agent.State{Messages: []runtime.Message{{Role: "user", Text: prompt}}}}
		var active int
		if err := tx.QueryRowContext(ctx, "SELECT name,active_version FROM projects WHERE id=?", project).Scan(&run.ProjectName, &active); err != nil {
			return nil, ErrNotFound
		}
		if active > 0 {
			plan, err := getPlan(ctx, tx, project, active)
			if err != nil {
				return nil, err
			}
			run.Plan = plan.Content
		}
		if err := accountAvailable(ctx, tx, c, ""); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(run)
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO agent_runs VALUES(?,?,?,?,?,?,?)", run.ID, project, role.ID, c.ID, c.AccountID, run.Status, encoded); err != nil {
			return nil, err
		}
		if err = event(ctx, tx, project, "run.started", map[string]string{"run_id": run.ID, "role_id": role.ID, "kind": "task"}); err != nil {
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

// ProjectSnapshot returns the managed copy a task workspace is created from.
func (s *Store) ProjectSnapshot(ctx context.Context, p Principal, project string) (string, error) {
	if err := allowed(p, project, false); err != nil {
		return "", err
	}
	var snapshot string
	if err := s.db.QueryRowContext(ctx, "SELECT snapshot FROM projects WHERE id=?", project).Scan(&snapshot); err != nil {
		return "", ErrNotFound
	}
	return snapshot, nil
}

// RecordTaskChanges stores the reviewed change list of a task's working copy.
func (s *Store) RecordTaskChanges(ctx context.Context, id string, changes []workspace.Change) error {
	if len(changes) > 5000 {
		changes = changes[:5000]
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		run, err := readRun(ctx, tx, id)
		if err != nil {
			return err
		}
		if run.Kind != "task" || run.Task == nil {
			return ErrConflict
		}
		run.Task.Changes = changes
		run.UpdatedAt = now()
		return writeRun(ctx, tx, run)
	})
}
