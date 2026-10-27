// Package preflight diagnoses local images and one explicitly requested route.
// Its containers never mount projects or accounts, and cannot start model tasks.
package preflight

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"agent-workbench/internal/netcheck"
	"agent-workbench/internal/sandbox"
)

// Request is transient diagnostic input. Never log it or persist proxy values.
type Request struct {
	Provider string              `json:"provider"`
	Runtime  sandbox.RuntimeKind `json:"runtime"`
	Image    string              `json:"image"`
	Binary   string              `json:"binary"`
	Scratch  string              `json:"scratch"`
	Mode     string              `json:"mode"`
	Target   string              `json:"target"`
	Proxy    string              `json:"proxy,omitempty"`
}

// Check records the scope of one observation, not provider or model readiness.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// Report is safe to display and cannot authorize native execution.
type Report struct {
	Provider         string              `json:"provider"`
	Runtime          sandbox.RuntimeKind `json:"runtime"`
	ImageID          string              `json:"image_id,omitempty"`
	Version          string              `json:"version,omitempty"`
	IP               string              `json:"ip,omitempty"`
	CheckedAt        string              `json:"checked_at"`
	Checks           []Check             `json:"checks"`
	EnvironmentReady bool                `json:"environment_ready"`
	ExecutionReady   bool                `json:"execution_ready"`
}

// ErrBusy prevents overlapping diagnostics and scratch/container ownership.
var ErrBusy = errors.New("执行环境检测正在进行，请稍后重试")

type command func(context.Context, string, []string, string) ([]byte, error)

// Service owns foreground diagnostics, their cancellation and cleanup journal.
type Service struct {
	root, helpers         string
	ctx                   context.Context
	cancel                context.CancelFunc
	mu                    sync.Mutex
	closed, busy, pending bool
	wg                    sync.WaitGroup
	lookup                func(string) (string, error)
	run                   command
	cleanup               func(context.Context, sandbox.ExecutionIdentity) error
}

// New uses helpers beside the application backend and reconciles only its own journals.
func New(ctx context.Context, root string) (*Service, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return newService(ctx, root, filepath.Dir(executable), exec.LookPath, runCommand, sandbox.Cleanup)
}

func newService(ctx context.Context, root, helpers string, lookup func(string) (string, error), run command, cleanup func(context.Context, sandbox.ExecutionIdentity) error) (*Service, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	actual, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	root = actual
	if !directory(root) {
		return nil, errors.New("检测日志目录必须为独立规范目录")
	}
	lifetime, cancel := context.WithCancel(ctx)
	s := &Service{root: root, helpers: helpers, ctx: lifetime, cancel: cancel, lookup: lookup, run: run, cleanup: cleanup}
	if err := s.recover(ctx); err != nil {
		s.pending = true
	}
	return s, nil
}

// Close cancels diagnostics and waits for bounded independent cleanup.
func (s *Service) Close() { s.mu.Lock(); s.closed = true; s.cancel(); s.mu.Unlock(); s.wg.Wait() }

func directory(value string) bool {
	actual, err := filepath.EvalSymlinks(value)
	if err != nil || !filepath.IsAbs(value) || filepath.Clean(value) != value || actual != value || strings.ContainsAny(value, ",\r\n") {
		return false
	}
	info, err := os.Stat(value)
	return err == nil && info.IsDir()
}

func validate(r Request) error {
	if r.Provider != "codex" && r.Provider != "claude" {
		return errors.New("请选择内建的原生工具")
	}
	if r.Runtime != sandbox.Docker && r.Runtime != sandbox.Podman {
		return errors.New("请明确选择 Docker 或 Podman")
	}
	if len(r.Image) == 0 || len(r.Image) > 256 || strings.HasPrefix(r.Image, "-") || strings.ContainsAny(r.Image, " \t\r\n") {
		return errors.New("请填写明确的本地镜像名称或 ID")
	}
	if !path.IsAbs(r.Binary) || path.Clean(r.Binary) != r.Binary || strings.ContainsAny(r.Binary, " \t\r\n\x00") || len(r.Binary) > 256 {
		return errors.New("工具路径必须为容器内的规范绝对路径")
	}
	if !directory(r.Scratch) {
		return errors.New("临时检测目录必须存在、无符号链接，并可供运行时访问")
	}
	if len(r.Proxy) > 4096 {
		return errors.New("代理配置超过上限")
	}
	probe := netcheck.Input{Target: r.Target, Mode: r.Mode, Proxy: r.Proxy}
	if probe.Target == "" {
		probe.Target = "https://example.invalid"
	}
	return netcheck.Validate(probe)
}

// Inspect performs only explicit checks, with no cached readiness or model calls.
func (s *Service) Inspect(ctx context.Context, r Request) (Report, error) {
	if err := validate(r); err != nil {
		return Report{}, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Report{}, errors.New("检测服务已关闭")
	}
	if s.busy {
		s.mu.Unlock()
		return Report{}, ErrBusy
	}
	s.busy = true
	s.wg.Add(1)
	pending := s.pending
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.busy = false; s.mu.Unlock(); s.wg.Done() }()
	report := Report{Provider: r.Provider, Runtime: r.Runtime, CheckedAt: time.Now().UTC().Format(time.RFC3339), Checks: []Check{}}
	add := func(name, status, detail string) { report.Checks = append(report.Checks, Check{name, status, detail}) }
	if pending {
		add("cleanup", "blocked", "上次检测清理未确认；请核对所选运行时并重启后重新清理")
		return report, nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	binary, err := s.lookup(string(r.Runtime))
	if err != nil {
		add("runtime", "blocked", "所选运行时命令不可用；未切换引擎或宿主执行")
		return report, nil
	}
	output, err := s.command(checkCtx, binary, []string{"version", "--format", "{{.Server.Version}}"}, "", 3*time.Second)
	if err != nil || strings.TrimSpace(string(output)) == "" {
		add("runtime", "blocked", "所选引擎未响应或无权限；不会自动启动")
		return report, nil
	}
	add("runtime", "passed", "所选引擎已响应")
	output, err = s.command(checkCtx, binary, []string{"image", "inspect", "--format", "{{.Id}}|{{.Os}}|{{.Architecture}}", r.Image}, "", 4*time.Second)
	parts := strings.Split(strings.TrimSpace(string(output)), "|")
	if err != nil || len(parts) != 3 {
		add("image", "blocked", "本地镜像不可用；未自动拉取")
		return report, nil
	}
	image := parts[0]
	if len(image) == 64 {
		image = "sha256:" + image
	}
	if !digest.MatchString(image) || parts[1] != "linux" || (parts[2] != "amd64" && parts[2] != "arm64") {
		add("image", "blocked", "需要 Linux amd64/arm64 的不可变本地镜像")
		return report, nil
	}
	report.ImageID = image
	add("image", "passed", "已锁定本地镜像 ID，检测不会自动拉取")
	output, err, clean := s.container(checkCtx, binary, r, image, r.Binary, []string{"--version"}, "", "")
	if !clean {
		add("cleanup", "blocked", "检测容器清理未确认；已保留日志，禁止继续检测")
		return report, nil
	}
	add("cli_cleanup", "passed", "版本检测容器已确认移除")
	version := strings.TrimSpace(string(output))
	cliOK := err == nil && versionPattern[r.Provider].MatchString(version)
	if cliOK {
		report.Version = version
		add("cli", "passed", "容器内版本命令通过；未验证官方来源、登录或推理")
	} else {
		add("cli", "blocked", "容器内工具不存在、无法运行或版本响应不匹配")
	}
	if r.Provider == "claude" {
		add("network", "blocked", "Claude 登录、续期与推理的强制出口链路尚未验证，未发送网络请求")
		return report, nil
	}
	if r.Target == "" {
		add("network", "skipped", "未填写检测目标，未发起网络请求")
		return report, nil
	}
	helper := filepath.Join(s.helpers, "awb-network-probe-linux-"+parts[2])
	if info, e := os.Stat(helper); e != nil || !info.Mode().IsRegular() {
		add("network", "blocked", "缺少对应架构的内置检测程序，请重新构建应用")
		return report, nil
	}
	input, err := json.Marshal(netcheck.Input{Target: r.Target, Mode: r.Mode, Proxy: r.Proxy})
	if err != nil {
		return report, err
	}
	output, err, clean = s.container(checkCtx, binary, r, image, "/awb-network-probe", nil, string(input), helper)
	if !clean {
		add("cleanup", "blocked", "网络检测容器清理未确认；已保留日志")
		return report, nil
	}
	add("network_cleanup", "passed", "网络检测容器已确认移除")
	var result netcheck.Result
	if err != nil || json.Unmarshal(output, &result) != nil || !result.Reachable {
		add("network", "blocked", "指定路径未通过检测；未回退直连、其他代理或宿主请求")
		return report, nil
	}
	add("network", "passed", "容器内独立 HTTP 检测已响应；不代表原生工具或登录浏览器的完整出口")
	if ip := net.ParseIP(result.IP); ip != nil {
		report.IP = ip.String()
		add("egress_ip", "passed", "检测目标报告了出口 IP，仅代表本次目标和路径")
	} else {
		add("egress_ip", "unverified", "目标未返回有效 IP，出口身份未知")
	}
	add("isolation", "unverified", "普通诊断网络未提供强制出口隔离；DNS、IPv6、UDP 和浏览器旁路仍待验证")
	report.EnvironmentReady = cliOK
	return report, nil
}

var digest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var noncePattern = regexp.MustCompile(`^[a-z2-7]{26}$`)
var versionPattern = map[string]*regexp.Regexp{
	"codex":  regexp.MustCompile(`^codex-cli [0-9][A-Za-z0-9.+_-]{0,100}$`),
	"claude": regexp.MustCompile(`^[0-9][A-Za-z0-9.+_-]{0,100} \(Claude Code\)$`),
}

func (s *Service) command(ctx context.Context, binary string, args []string, input string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return s.run(ctx, binary, args, input)
}

type limitedOutput struct{ buffer bytes.Buffer }

func (w *limitedOutput) Bytes() []byte { return w.buffer.Bytes() }

func (w *limitedOutput) Write(p []byte) (int, error) {
	if w.buffer.Len()+len(p) > 65536 {
		return 0, errors.New("diagnostic output exceeds limit")
	}
	return w.buffer.Write(p)
}

func runCommand(ctx context.Context, binary string, args []string, input string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	var out limitedOutput
	cmd.Stdout = &out
	err := cmd.Run()
	return out.Bytes(), err
}
