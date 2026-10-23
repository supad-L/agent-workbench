// Package nativeservice journals isolated native sessions without enabling accounts or network.
package nativeservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"agent-workbench/internal/runtime"
	"agent-workbench/internal/runtime/codex"
	"agent-workbench/internal/sandbox"
	"agent-workbench/internal/store"
	"agent-workbench/internal/workspace"
)

type factory func(store.NativeBinding, sandbox.Execution) runtime.NativeExecutor

// Manager owns the lifetime of explicitly requested offline invocations.
type Manager struct {
	store   *store.Store
	root    string
	context context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	closed  bool
	wg      sync.WaitGroup
	factory factory
	cleanup func(context.Context, sandbox.ExecutionIdentity) error
}

// View separates local continuation evidence from real account execution readiness.
type View struct {
	Session        store.NativeSession `json:"session"`
	CanResume      bool                `json:"can_resume"`
	RecoveryReason string              `json:"recovery_reason"`
	LiveReady      bool                `json:"live_ready"`
}

// New reconciles previous journals but never starts or resumes a model invocation.
func New(ctx context.Context, s *store.Store, root string) (*Manager, error) {
	return newManager(ctx, s, root, func(binding store.NativeBinding, execution sandbox.Execution) runtime.NativeExecutor {
		return codex.Executor{Base: execution, Binary: binding.Binary}
	}, sandbox.Cleanup)
}
func newManager(ctx context.Context, s *store.Store, root string, build factory, cleanup func(context.Context, sandbox.ExecutionIdentity) error) (*Manager, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, errors.New("原生会话目录需要明确的绝对路径")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	actual, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(ctx)
	manager := &Manager{store: s, root: actual, context: lifetime, cancel: cancel, factory: build, cleanup: cleanup}
	if err = manager.recover(ctx); err != nil {
		cancel()
		return nil, err
	}
	return manager, nil
}

// Close cancels active invocations and waits for cleanup/checkpoint settlement.
func (m *Manager) Close() { m.mu.Lock(); m.closed = true; m.cancel(); m.mu.Unlock(); m.wg.Wait() }

func canonical(name string) bool {
	if !filepath.IsAbs(name) || filepath.Clean(name) != name {
		return false
	}
	actual, err := filepath.EvalSymlinks(name)
	if err != nil || actual != name {
		return false
	}
	info, err := os.Stat(name)
	return err == nil && info.IsDir()
}
func overlap(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	separator := string(filepath.Separator)
	return a == b || strings.HasPrefix(a, strings.TrimRight(b, separator)+separator) || strings.HasPrefix(b, strings.TrimRight(a, separator)+separator)
}
func digest(ctx context.Context, directory string) (string, workspace.Manifest, error) {
	if !canonical(directory) {
		return "", nil, errors.New("原生会话目录已缺失或包含符号链接")
	}
	manifest, err := workspace.Scan(ctx, directory)
	if err != nil {
		return "", nil, errors.New("无法核对原生会话文件摘要")
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), manifest, nil
}
func (m *Manager) owned(session store.NativeSession) bool {
	return session.RunDirectory == filepath.Join(m.root, session.ID) && canonical(session.RunDirectory) && canonical(filepath.Join(session.RunDirectory, "codex"))
}
func stateDigest(ctx context.Context, home, id string) (string, error) {
	hash, manifest, err := digest(ctx, home)
	if err != nil {
		return "", err
	}
	for name, entry := range manifest {
		if entry.Kind == "file" && strings.HasPrefix(name, "sessions/") && (strings.HasSuffix(name, "-"+id+".jsonl") || filepath.Base(name) == id+".jsonl") {
			return hash, nil
		}
	}
	return "", errors.New("未找到匹配当前原生会话编号的持久文件")
}

// Create persists a binding over an already prepared private source snapshot.
// It does not import a project, grant execution permission or read any account home.
func (m *Manager) Create(ctx context.Context, p store.Principal, project string, binding store.NativeBinding, key string) (store.NativeSession, error) {
	if err := ctx.Err(); err != nil {
		return store.NativeSession{}, err
	}
	if p.ID == "" || p.Role != "human" {
		return store.NativeSession{}, store.ErrForbidden
	}
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return store.NativeSession{}, errors.New("原生会话服务已关闭")
	}
	if binding.Binary == "" {
		binding.Binary = "/usr/local/bin/codex"
	}
	if !path.IsAbs(binding.Binary) || path.Clean(binding.Binary) != binding.Binary || strings.IndexFunc(binding.Binary, unicode.IsControl) >= 0 {
		return store.NativeSession{}, errors.New("原生工具需要容器内的规范绝对路径")
	}
	if !utf8.ValidString(binding.Model) || binding.Model == "" || len(binding.Model) > 128 || strings.TrimSpace(binding.Model) != binding.Model || strings.HasPrefix(binding.Model, "-") || strings.IndexFunc(binding.Model, unicode.IsControl) >= 0 {
		return store.NativeSession{}, errors.New("原生模型标识无效")
	}
	if binding.Runtime != sandbox.Docker && binding.Runtime != sandbox.Podman {
		return store.NativeSession{}, errors.New("需要明确选择原生隔离运行时")
	}
	if !canonical(binding.Spec.SourceRoot) || overlap(binding.Spec.SourceRoot, m.root) {
		return store.NativeSession{}, errors.New("原生源快照与会话管理目录必须隔离")
	}
	for _, source := range binding.Spec.Writable {
		if overlap(source, m.root) {
			return store.NativeSession{}, errors.New("可写源码与会话管理目录必须隔离")
		}
	}
	if len(binding.Spec.Scope.ReadPaths) != 1 || binding.Spec.Scope.ReadPaths[0] != "." {
		return store.NativeSession{}, errors.New("需要已授权的独立完整读取快照")
	}
	binding.Spec.Name = "awb-probe-validation"
	binding.Spec.Command = []string{"/usr/local/bin/codex"}
	if _, err := sandbox.Arguments(binding.Spec); err != nil {
		return store.NativeSession{}, err
	}
	binding.Spec.Name = ""
	binding.Spec.Command = nil
	baseline, _, err := digest(ctx, binding.Spec.SourceRoot)
	if err != nil {
		return store.NativeSession{}, err
	}
	session, err := m.store.CreateNativeSession(ctx, p, project, store.NativeCreate{Root: m.root, Binding: binding, SourceDigest: baseline, Key: key})
	if err != nil {
		return session, err
	}
	if session.RunDirectory != filepath.Join(m.root, session.ID) {
		return session, errors.New("原生会话目录绑定不匹配")
	}
	if err = os.MkdirAll(session.RunDirectory, 0700); err != nil {
		return session, err
	}
	if !canonical(session.RunDirectory) {
		return session, errors.New("原生会话目录不能为符号链接")
	}
	home := filepath.Join(session.RunDirectory, "codex")
	if err = os.MkdirAll(home, 0700); err != nil {
		return session, err
	}
	if !canonical(home) {
		return session, errors.New("原生状态目录不能为符号链接")
	}
	// The enclosing private 0700 session directory protects host access; the mount
	// root must also be writable by the fixed container UID on macOS shared volumes.
	if err = os.Chmod(home, 0777); err != nil {
		return session, err
	}
	return session, nil
}

func (m *Manager) inspect(ctx context.Context, session store.NativeSession) View {
	view := View{Session: session, RecoveryReason: "尚未启动，需显式开始"}
	if session.Status == "running" {
		view.RecoveryReason = "本次执行正在运行"
		return view
	}
	if session.Recovery == "uncertain" {
		view.RecoveryReason = "结果未知，先核对运行与成果；不能自动重发"
		return view
	}
	if !session.CleanupVerified {
		view.RecoveryReason = "尚未确认原容器已停止"
		return view
	}
	if !m.owned(session) {
		view.RecoveryReason = "原生运行目录已缺失或绑定已变化"
		return view
	}
	if session.Recovery != "resumable" {
		if session.Recovery != "not_started" {
			view.RecoveryReason = "运行已中断，缺少完整原生续接证据"
		}
		return view
	}
	if !m.store.NativeBasisCurrent(ctx, session) {
		view.RecoveryReason = "确认方案已变化，需要新的执行尝试"
		return view
	}
	source, _, err := digest(ctx, session.Binding.Spec.SourceRoot)
	if err != nil || source != session.SourceDigest {
		view.RecoveryReason = "只读源快照已缺失或变化，需要新的执行尝试"
		return view
	}
	state, err := stateDigest(ctx, filepath.Join(session.RunDirectory, "codex"), session.NativeID)
	if err != nil || state != session.StateDigest {
		view.RecoveryReason = "原生会话文件已缺失或变化，不能按旧记录续接"
		return view
	}
	view.CanResume = true
	view.RecoveryReason = "本地续接证据齐备；真实账号与在线执行尚未就绪"
	return view
}

// Get reads one session and independently verifies local continuation evidence.
func (m *Manager) Get(ctx context.Context, p store.Principal, project, id string) (View, error) {
	session, err := m.store.NativeSession(ctx, p, project, id)
	if err != nil {
		return View{}, err
	}
	return m.inspect(ctx, session), nil
}

// List never starts a native process or an account operation.
func (m *Manager) List(ctx context.Context, p store.Principal, project string) ([]View, error) {
	sessions, err := m.store.NativeSessions(ctx, p, project)
	if err != nil {
		return nil, err
	}
	result := make([]View, 0, len(sessions))
	for _, session := range sessions {
		result = append(result, m.inspect(ctx, session))
	}
	return result, nil
}

// Run is an internal platform action for a new explicit start/resume. Product
// launch routes remain disabled until account/egress/task dependencies are ready.
func (m *Manager) Run(ctx context.Context, p store.Principal, project, id, key, prompt string, revision int, resume bool, timeout time.Duration) (store.NativeSession, error) {
	if timeout <= 0 || timeout > 20*time.Minute || strings.TrimSpace(prompt) == "" {
		return store.NativeSession{}, errors.New("需要有界时限和显式续接输入")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return store.NativeSession{}, errors.New("原生会话服务已关闭")
	}
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	view, err := m.Get(ctx, p, project, id)
	if err != nil {
		return store.NativeSession{}, err
	}
	// A replayed action may already have completed. Claim's request-key lookup is
	// authoritative and never executes again; readiness is checked for new resumes.
	if resume && !view.CanResume && view.Session.Recovery != "uncertain" && view.Session.Status != "running" {
		return view.Session, errors.New(view.RecoveryReason)
	}
	if !m.owned(view.Session) {
		return view.Session, errors.New("原生运行目录不完整")
	}
	source, _, err := digest(ctx, view.Session.Binding.Spec.SourceRoot)
	if err != nil || source != view.Session.SourceDigest {
		return view.Session, errors.New("原生源快照已变化")
	}
	session, attempt, claimed, err := m.store.ClaimNativeAttempt(ctx, p, project, id, key, prompt, revision, resume)
	if err != nil || !claimed {
		return session, err
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	stop := context.AfterFunc(m.context, cancel)
	defer cancel()
	defer stop()
	execution := sandbox.Execution{Runtime: session.Binding.Runtime, Spec: session.Binding.Spec, StateDirectory: filepath.Join(session.RunDirectory, "codex"), BeforeLaunch: func(ctx context.Context, identity sandbox.ExecutionIdentity) error {
		return m.store.MarkNativeLaunch(ctx, id, attempt.Number, identity)
	}}
	sequence := 0
	request := runtime.NativeRequest{Model: session.Binding.Model, Prompt: prompt, Timeout: timeout, MaxOutputBytes: 1 << 20, Checkpoint: func(ctx context.Context, checkpoint runtime.NativeCheckpoint) error {
		sequence++
		return m.store.CheckpointNative(ctx, id, attempt.Number, sequence, checkpoint)
	}}
	if resume {
		request.ResumeSessionID = session.NativeID
	}
	result, runErr := m.factory(session.Binding, execution).Run(runCtx, request, func(runtime.NativeEvent) error { return nil })
	inspectCtx, inspectCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	state := ""
	nativeID := result.SessionID
	if nativeID == "" {
		nativeID = session.NativeID
	}
	if result.CleanupVerified && nativeID != "" {
		state, err = stateDigest(inspectCtx, execution.StateDirectory, nativeID)
		if err != nil {
			state = ""
		}
	}
	inspectCancel()
	// State inspection must not consume the durable settlement deadline.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer finishCancel()
	if err = m.store.FinishNative(finishCtx, id, attempt.Number, result, state); err != nil {
		m.mu.Lock()
		m.closed = true
		m.cancel()
		m.mu.Unlock()
		return session, err
	}
	session, err = m.store.NativeSession(finishCtx, p, project, id)
	return session, errors.Join(runErr, err)
}

func (m *Manager) recover(ctx context.Context) error {
	sessions, err := m.store.PendingNativeSessions(ctx)
	if err != nil {
		return err
	}
	for _, session := range sessions {
		human := store.Principal{ID: "native-recovery", Role: "human"}
		attempts, err := m.store.NativeAttempts(ctx, human, session.ProjectID, session.ID)
		if err != nil {
			return err
		}
		if len(attempts) == 0 || attempts[len(attempts)-1].Number != session.Attempt {
			return errors.New("原生会话日志不完整，未自动执行")
		}
		attempt := attempts[len(attempts)-1]
		cleaned := attempt.Container.Name == ""
		if !cleaned {
			cleanupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			cleaned = m.cleanup(cleanupCtx, attempt.Container) == nil
			cancel()
		}
		if err = m.store.RecoverNative(ctx, session.ID, session.Attempt, cleaned); err != nil {
			return err
		}
	}
	return nil
}
