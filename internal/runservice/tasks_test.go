package runservice

import (
	"agent-workbench/internal/apiconfig"
	"agent-workbench/internal/catalog"
	"agent-workbench/internal/connection"
	"agent-workbench/internal/egress"
	"agent-workbench/internal/runtime"
	"agent-workbench/internal/sandbox"
	"agent-workbench/internal/store"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

type taskEnv struct {
	s        *store.Store
	m        *Manager
	p        store.Principal
	r        catalog.Role
	c        catalog.Connection
	snapshot string
	mu       sync.Mutex
	commands []sandbox.Execution
}

func taskFixture(t *testing.T, model runtime.Model, tools []string) *taskEnv {
	t.Helper()
	ctx := context.Background()
	env := &taskEnv{p: store.Principal{ID: "human", Role: "human"}}
	s, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	env.s = s
	env.snapshot = t.TempDir()
	for name, content := range map[string]string{"src/app.js": "export const answer = 41;\n", "README.md": "readme\n", ".git/HEAD": "ref: main\n"} {
		path := filepath.Join(env.snapshot, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.CreateProject(ctx, env.p, "project", store.Project{ID: "project", Name: "Demo", Snapshot: env.snapshot, Manifest: json.RawMessage(`[]`)}); err != nil {
		t.Fatal(err)
	}
	c := catalog.Connection{ID: "connection", Label: "Primary", Revision: 1, AccountID: "account", MaxConcurrent: 1, Enabled: true, Config: apiconfig.Config{Endpoint: connection.Endpoint{Protocol: connection.Chat, Address: "https://gateway.invalid", AddressKind: "base_url"}, Model: "primary", AuthKind: "none", Egress: egress.Policy{Mode: "system", DNSMode: "system"}, MaxOutputTokens: 64}, Models: []catalog.Model{{ID: "primary", Tools: "supported", PriceKnown: true, Currency: "CNY", InputPrice: 1000000, OutputPrice: 1000000}}}
	if err = s.PutConnection(ctx, env.p, c); err != nil {
		t.Fatal(err)
	}
	r := catalog.Templates()[1]
	r.Primary = catalog.Choice{ConnectionID: c.ID, ModelID: "primary"}
	r.Tools = tools
	r.Enabled = true
	if r, err = s.SaveRole(ctx, env.p, r); err != nil {
		t.Fatal(err)
	}
	env.r, env.c = r, c
	runner := func(_ context.Context, e sandbox.Execution, onLine func([]byte) error) (sandbox.ExecutionResult, error) {
		env.mu.Lock()
		env.commands = append(env.commands, e)
		env.mu.Unlock()
		_ = onLine([]byte("FAIL src/app.test.js"))
		_ = onLine([]byte("expected 42"))
		return sandbox.ExecutionResult{Status: "failed", CleanupVerified: true}, errors.New("isolated process failed")
	}
	m, err := NewWithTasks(ctx, s, factoryFor(model), filepath.Join(t.TempDir(), "tasks"), runner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	env.m = m
	return env
}

func (e *taskEnv) input(key string) store.TaskInput {
	return store.TaskInput{RoleID: e.r.ID, RoleRevision: e.r.Revision, Title: "修正答案", Instructions: "把 answer 改成 42", WriteDirs: []string{"src"}, Runtime: "podman", Image: "sha256:" + strings.Repeat("a", 64), Key: key, ConnectionRevisions: map[string]int{e.c.ID: e.c.Revision}}
}

func call(id, name, args string) runtime.ToolCall {
	return runtime.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(args)}
}

func TestTaskRunEditsOnlyPrivateCopyWithinScope(t *testing.T) {
	var seenTools []string
	results := map[string]string{}
	turn := 0
	model := modelFunc(func(_ context.Context, r runtime.Request, _ func(runtime.Event) error) (runtime.Result, error) {
		for _, m := range r.Messages {
			if m.Role == "tool" {
				results[m.CallID] = m.Text
			}
		}
		turn++
		switch turn {
		case 1:
			for _, tool := range r.Tools {
				seenTools = append(seenTools, tool.Name)
			}
			if !strings.Contains(r.System, "src/") || !strings.Contains(r.Messages[0].Text, "把 answer 改成 42") {
				t.Error("task contract missing from prompt")
			}
			return runtime.Result{Calls: []runtime.ToolCall{call("l1", "list_files", `{"recursive":true}`), call("r1", "read_file", `{"path":"./src/app.js"}`)}}, nil
		case 2:
			return runtime.Result{Calls: []runtime.ToolCall{
				call("w1", "replace_in_file", `{"path":"src/app.js","old_text":"41","new_text":"42"}`),
				call("w2", "write_file", `{"path":"README.md","content":"pwned"}`),
				call("w3", "write_file", `{"path":"/workspace/src/app.test.js","content":"test\n"}`),
				call("w4", "write_file", `{"path":"src/../../escape.txt","content":"x"}`),
			}}, nil
		case 3:
			return runtime.Result{Calls: []runtime.ToolCall{call("c1", "run_command", `{"command":"npm test"}`)}}, nil
		default:
			return runtime.Result{Text: "已把 answer 改为 42，测试仍需依赖安装后复核。"}, nil
		}
	})
	env := taskFixture(t, model, []string{"read_project", "read_plan", "read_file", "write_file", "run_command"})
	run, err := env.m.StartTask(context.Background(), env.p, "project", env.input("task-1"), nil)
	if err != nil {
		t.Fatal(err)
	}
	final := awaitRun(t, env.s, env.p, run.ID)
	if final.Status != "completed" || final.Kind != "task" || final.Task == nil {
		t.Fatalf("final %+v", final)
	}
	if !slices.Equal(seenTools, []string{"list_files", "read_file", "write_file", "replace_in_file", "run_command"}) {
		t.Fatalf("tools offered %v", seenTools)
	}
	if !strings.Contains(results["l1"], "src/app.js") || strings.Contains(results["l1"], ".git") {
		t.Fatalf("listing %q", results["l1"])
	}
	if !strings.Contains(results["r1"], "1\texport const answer = 41;") {
		t.Fatalf("read %q", results["r1"])
	}
	if !strings.HasPrefix(results["w2"], "错误") || !strings.HasPrefix(results["w4"], "错误") || strings.HasPrefix(results["w3"], "错误") {
		t.Fatalf("scope results w2=%q w3=%q w4=%q", results["w2"], results["w3"], results["w4"])
	}
	if !strings.Contains(results["c1"], "失败") || !strings.Contains(results["c1"], "expected 42") {
		t.Fatalf("command %q", results["c1"])
	}
	changes := []string{}
	for _, c := range final.Task.Changes {
		changes = append(changes, c.Path+":"+c.Kind)
	}
	if strings.Join(changes, ",") != "src/app.js:modified,src/app.test.js:added" {
		t.Fatalf("changes %v", changes)
	}
	original, _ := os.ReadFile(filepath.Join(env.snapshot, "src/app.js"))
	if string(original) != "export const answer = 41;\n" {
		t.Fatal("managed snapshot was modified")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(env.snapshot), "escape.txt")); !os.IsNotExist(err) {
		t.Fatal("write escaped the task copy")
	}
	env.mu.Lock()
	command := env.commands[0]
	env.mu.Unlock()
	if command.Runtime != sandbox.Podman || len(command.Spec.Writable) != 0 || !strings.HasSuffix(command.Spec.SourceRoot, filepath.Join(run.ID, "work")) || !strings.Contains(strings.Join(command.Spec.Command, " "), "npm test") || command.Timeout > commandTimeout {
		t.Fatalf("command execution %+v", command)
	}
	diffs, err := env.m.TaskChanges(context.Background(), env.p, "project", run.ID)
	if err != nil || len(diffs) != 2 || !strings.Contains(diffs[0].Text, "-export const answer = 41;") || !strings.Contains(diffs[0].Text, "+export const answer = 42;") {
		t.Fatalf("diffs %+v %v", diffs, err)
	}
	// Idempotent retry returns the same task without another model request.
	again, err := env.m.StartTask(context.Background(), env.p, "project", env.input("task-1"), nil)
	if err != nil || again.ID != run.ID || turn != 4 {
		t.Fatalf("retry %v %v turns=%d", again.ID, err, turn)
	}
}

func TestTaskContractValidation(t *testing.T) {
	model := modelFunc(func(context.Context, runtime.Request, func(runtime.Event) error) (runtime.Result, error) {
		return runtime.Result{Text: "ok"}, nil
	})
	env := taskFixture(t, model, []string{"read_file"})
	if _, err := env.m.StartTask(context.Background(), env.p, "project", env.input("no-write"), nil); err == nil || !strings.Contains(err.Error(), "写入任务文件") {
		t.Fatalf("role without write tools: %v", err)
	}
	for name, mutate := range map[string]func(*store.TaskInput){
		"whole project": func(i *store.TaskInput) { i.WriteDirs = []string{"."} },
		"traversal":     func(i *store.TaskInput) { i.WriteDirs = []string{"../outside"} },
		"nested":        func(i *store.TaskInput) { i.WriteDirs = []string{"src", "src/lib"} },
		"git":           func(i *store.TaskInput) { i.WriteDirs = []string{".git"} },
		"image":         func(i *store.TaskInput) { i.Image = "alpine:latest" },
		"runtime":       func(i *store.TaskInput) { i.Runtime = "host" },
		"empty":         func(i *store.TaskInput) { i.Instructions = " " },
	} {
		input := env.input("x")
		mutate(&input)
		if _, err := store.NormalizeTask(input); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
	spec, err := store.NormalizeTask(store.TaskInput{Title: "t", Instructions: "i", WriteDirs: []string{"/web/src/"}})
	if err != nil || spec.WriteDirs[0] != "web/src/" || spec.Image != "" {
		t.Fatalf("normalize %+v %v", spec, err)
	}
}

func TestTaskWithoutImageOffersNoCommandTool(t *testing.T) {
	var offered []string
	model := modelFunc(func(_ context.Context, r runtime.Request, _ func(runtime.Event) error) (runtime.Result, error) {
		for _, tool := range r.Tools {
			offered = append(offered, tool.Name)
		}
		return runtime.Result{Text: "无需修改"}, nil
	})
	env := taskFixture(t, model, []string{"read_file", "apply_patch", "run_command"})
	input := env.input("no-image")
	input.Image, input.Runtime = "", ""
	run, err := env.m.StartTask(context.Background(), env.p, "project", input, nil)
	if err != nil {
		t.Fatal(err)
	}
	final := awaitRun(t, env.s, env.p, run.ID)
	if final.Status != "completed" || !slices.Equal(offered, []string{"list_files", "read_file", "replace_in_file"}) || len(final.Task.Changes) != 0 {
		t.Fatalf("offered %v final %+v", offered, final)
	}
}

// TestRealTaskCommandIsOfflineAndReadOnly runs one verification command in the
// selected real engine. It needs a local image with sh and node (the Codex image).
func TestRealTaskCommandIsOfflineAndReadOnly(t *testing.T) {
	kind := os.Getenv("AWB_TEST_SANDBOX_RUNTIME")
	image, scratch := os.Getenv("AWB_TEST_SANDBOX_IMAGE"), os.Getenv("AWB_TEST_SANDBOX_SCRATCH")
	if kind == "" || image == "" || scratch == "" {
		t.Skip("set explicit AWB_TEST_SANDBOX_RUNTIME, IMAGE and SCRATCH for a real task command")
	}
	work, err := os.MkdirTemp(scratch, "awb-task-command-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(work) })
	if work, err = filepath.EvalSymlinks(work); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(work, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "src", "app.js"), []byte("console.log('task-copy-ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Manager{command: sandbox.Execute}
	run := store.Run{Task: &store.TaskSpec{Runtime: kind, Image: image}}
	script := `node src/app.js
(echo x > src/written.txt) 2>/dev/null && echo write-succeeded || echo readonly-ok
node -e "fetch('http://example.com',{signal:AbortSignal.timeout(3000)}).then(()=>console.log('network-reachable')).catch(()=>console.log('offline-ok'))"
echo to-stderr 1>&2
exit 3`
	output := m.runCommand(context.Background(), run, work, script)
	for _, want := range []string{"task-copy-ok", "readonly-ok", "offline-ok", "to-stderr", "失败"} {
		if !strings.Contains(output, want) {
			t.Fatalf("missing %q in output:\n%s", want, output)
		}
	}
	if strings.Contains(output, "write-succeeded") || strings.Contains(output, "network-reachable") || strings.Contains(output, "清理未确认") {
		t.Fatalf("isolation violated:\n%s", output)
	}
	if _, err := os.Stat(filepath.Join(work, "src", "written.txt")); !os.IsNotExist(err) {
		t.Fatal("command wrote into the task copy")
	}
	t.Log(output)
}
