// Package catalog defines secret-free connections and reusable Agent role templates.
package catalog

import (
	"agent-workbench/internal/apiconfig"
	"errors"
	"regexp"
	"slices"
	"strings"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Model describes explicitly configured capabilities and a user-supplied price snapshot.
type Model struct {
	ID          string `json:"id"`
	Tools       string `json:"tools"`
	Currency    string `json:"currency,omitempty"`
	InputPrice  int64  `json:"input_price_micros_per_million,omitempty"`
	OutputPrice int64  `json:"output_price_micros_per_million,omitempty"`
	PriceKnown  bool   `json:"price_known"`
}

// Connection is metadata only. AccountID is a private-vault HMAC, never an API Key.
type Connection struct {
	ID            string           `json:"id"`
	Label         string           `json:"label"`
	Revision      int              `json:"revision"`
	Config        apiconfig.Config `json:"config"`
	Models        []Model          `json:"models"`
	AccountID     string           `json:"account_id"`
	MaxConcurrent int              `json:"max_concurrent"`
	Enabled       bool             `json:"enabled"`
	HasKey        bool             `json:"has_key"`
	HasProxy      bool             `json:"has_proxy"`
}

// Choice binds a role to one model on a configured connection.
type Choice struct {
	ConnectionID string `json:"connection_id"`
	ModelID      string `json:"model_id"`
}

// Fallback requires explicit data-scope and cost consent, and is disabled by default.
type Fallback struct {
	Enabled      bool     `json:"enabled"`
	Candidates   []Choice `json:"candidates"`
	DataScope    string   `json:"data_scope"`
	MaxExtraCost int64    `json:"max_extra_cost_micros"`
	Currency     string   `json:"currency,omitempty"`
}

// Role stores instructions and capabilities, independently from accounts or run instances.
type Role struct {
	ID           string   `json:"id"`
	Revision     int      `json:"revision"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Description  string   `json:"description"`
	SystemPrompt string   `json:"system_prompt"`
	Primary      Choice   `json:"primary"`
	Tools        []string `json:"tools"`
	Fallback     Fallback `json:"fallback"`
	Enabled      bool     `json:"enabled"`
}

// ToolNames is the platform vocabulary. Saving a name does not grant a task's path authority.
var ToolNames = []string{"read_project", "read_plan", "list_roles", "read_file", "write_file", "apply_patch", "run_command", "draft_plan", "propose_change", "create_task", "dispatch_task", "read_artifact", "read_verification", "submit_review", "ask_user", "record_suggestion", "get_status"}

// Validate rejects unsafe/unbounded metadata without requesting a model or resolving secrets.
func (c Connection) Validate() error {
	if !identifier.MatchString(c.ID) || c.Revision < 1 || strings.TrimSpace(c.Label) == "" || len(c.Label) > 128 || !identifier.MatchString(c.AccountID) || c.MaxConcurrent < 1 || c.MaxConcurrent > 32 || len(c.Models) == 0 || len(c.Models) > 64 {
		return errors.New("连接标识、名称、账号引用、并发上限或模型列表无效")
	}
	if _, err := c.Config.Validate(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, m := range c.Models {
		if strings.TrimSpace(m.ID) == "" || len(m.ID) > 256 || seen[m.ID] || !slices.Contains([]string{"unknown", "supported", "unsupported"}, m.Tools) {
			return errors.New("模型 ID 重复或工具能力无效")
		}
		if m.InputPrice < 0 || m.OutputPrice < 0 || m.InputPrice > 1000000000 || m.OutputPrice > 1000000000 || m.PriceKnown && m.Currency != "USD" && m.Currency != "CNY" {
			return errors.New("模型价格需要明确币种与有效的非负金额")
		}
		seen[m.ID] = true
	}
	if !seen[c.Config.Model] {
		return errors.New("默认模型必须出现在模型列表中")
	}
	return nil
}

// Validate checks a template's structure. Connection references are checked in the save transaction.
func (r Role) Validate() error {
	if !identifier.MatchString(r.ID) || r.Revision < 0 || strings.TrimSpace(r.Name) == "" || len(r.Name) > 128 || len(r.Description) > 2048 || strings.TrimSpace(r.SystemPrompt) == "" || len(r.SystemPrompt) > 32768 || !slices.Contains([]string{"coordinator", "frontend", "backend", "tester", "reviewer", "custom"}, r.Kind) || len(r.Tools) > len(ToolNames) {
		return errors.New("角色名称、类型、职责或系统提示词无效")
	}
	seen := map[string]bool{}
	for _, tool := range r.Tools {
		if !slices.Contains(ToolNames, tool) || seen[tool] {
			return errors.New("工具名称无效或重复")
		}
		seen[tool] = true
	}
	if r.Primary.ConnectionID == "" && r.Primary.ModelID == "" && !r.Enabled {
		return validateFallback(r.Fallback)
	}
	if !identifier.MatchString(r.Primary.ConnectionID) || r.Primary.ModelID == "" {
		return errors.New("请选择角色的连接和模型；未绑定的角色只能保存为停用")
	}
	return validateFallback(r.Fallback)
}

func validateFallback(f Fallback) error {
	if len(f.Candidates) > 4 || f.MaxExtraCost < 0 || f.MaxExtraCost > 1000000000000 {
		return errors.New("备用连接数量或费用上限无效")
	}
	if !f.Enabled {
		return nil
	}
	if len(f.Candidates) == 0 || f.MaxExtraCost == 0 || f.Currency != "USD" && f.Currency != "CNY" || f.DataScope != "conversation_only" && f.DataScope != "project_summary" {
		return errors.New("启用备用策略需要候选连接、可发送资料范围、币种和额外费用上限")
	}
	return nil
}

// Templates are editable suggestions; they never activate or bind an account automatically.
func Templates() []Role {
	definitions := []struct {
		kind, name, description, prompt string
		tools                           []string
	}{
		{"coordinator", "主 Agent", "讨论方案、拆分任务、审查和协调", "你是主 Agent。依据用户已确认的方案规划和审查。不得自行新增产品要求或验收条件。新增想法只记录为非阻塞建议；无法确认的问题先协调，再询问用户。", []string{"read_project", "read_plan", "list_roles", "create_task", "dispatch_task", "read_artifact", "read_verification", "submit_review", "ask_user", "record_suggestion"}},
		{"frontend", "前端工程师", "负责界面、交互与前端实现", "你是前端工程师。只执行任务合同和已确认方案内的前端工作。遵守目录边界，遇到接口歧义向主 Agent 提问，提交修改和验证证据。", []string{"read_project", "read_plan", "read_file", "write_file", "apply_patch", "run_command", "ask_user"}},
		{"backend", "后端工程师", "负责接口、数据和后端实现", "你是后端工程师。依据已确认接口与任务合同实现后端，遵守写入范围，不私自扩展产品流程。无法确认的问题交由主 Agent 协调。", []string{"read_project", "read_plan", "read_file", "write_file", "apply_patch", "run_command", "ask_user"}},
		{"tester", "测试工程师", "验证需求、边界和回归", "你是测试工程师。按固定方案与验证基准检查行为，报告可复现证据。不得删除或降低正式测试来使验收通过。", []string{"read_project", "read_plan", "read_file", "run_command", "read_verification", "ask_user"}},
		{"reviewer", "代码审查员", "对照固定方案审查成果", "你是代码审查员。审查固定版本和固定成果，只将违反已确认依据的问题作为阻塞项。新想法属于非阻塞建议，不得以个人偏好扩大验收范围。", []string{"read_project", "read_plan", "read_artifact", "read_verification", "submit_review", "record_suggestion"}},
	}
	result := make([]Role, 0, len(definitions))
	for _, d := range definitions {
		result = append(result, Role{ID: d.kind, Name: d.name, Kind: d.kind, Description: d.description, SystemPrompt: d.prompt, Tools: d.tools, Fallback: Fallback{Candidates: []Choice{}, DataScope: "conversation_only"}})
	}
	return result
}
