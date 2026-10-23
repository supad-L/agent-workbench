// Package store owns the M1 SQLite state, transactional events and immutable plans.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"agent-workbench/contracts"
	"agent-workbench/internal/contractguard"
	_ "github.com/mattn/go-sqlite3"
)

// ErrConflict rejects stale revisions and conflicting idempotency keys.
var ErrConflict = errors.New("数据已变化，请刷新后重试")

// ErrForbidden rejects operations outside the authenticated principal's role.
var ErrForbidden = errors.New("当前身份没有此操作权限")

// ErrNotFound identifies a missing project or record.
var ErrNotFound = errors.New("未找到记录")

// Principal comes only from the service authentication boundary.
type Principal struct{ ID, Role, ProjectID string }

// Project describes a managed project and its current adopted plan.
type Project struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Source        string          `json:"source"`
	Snapshot      string          `json:"snapshot"`
	Manifest      json.RawMessage `json:"manifest"`
	ActiveVersion int             `json:"active_version"`
	CreatedAt     string          `json:"created_at"`
}

// Message is persisted conversation content with authenticated attribution.
type Message struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}

// Plan is a versioned draft or a confirmed, immutable contract.
type Plan struct {
	ProjectID   string          `json:"project_id"`
	Version     int             `json:"version"`
	Revision    int             `json:"revision"`
	Status      string          `json:"status"`
	Hash        string          `json:"hash"`
	Content     json.RawMessage `json:"content"`
	Reason      string          `json:"reason"`
	BaseVersion int             `json:"base_version"`
	CreatedAt   string          `json:"created_at"`
}

// Document records source bytes/text and an explicit parsing status.
type Document struct {
	Source    string `json:"source"`
	CopyPath  string `json:"copy_path"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Version   int    `json:"version"`
	Hash      string `json:"hash"`
	Text      string `json:"text"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// Event is committed atomically with the state it describes.
type Event struct {
	Sequence  int64           `json:"sequence"`
	ProjectID string          `json:"project_id"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"`
}

// Store is owned by one local service instance. Subscribers can replay durable events.
type Store struct {
	db        *sql.DB
	lock      *os.File
	validator *contracts.Validator
	mu        sync.Mutex
	changed   chan struct{}
	closeOnce sync.Once
	closeErr  error
}

const migration = `
CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY);
CREATE TABLE IF NOT EXISTS projects(id TEXT PRIMARY KEY,name TEXT NOT NULL,source TEXT NOT NULL,snapshot TEXT NOT NULL,manifest BLOB NOT NULL,active_version INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS conversations(id TEXT PRIMARY KEY,project_id TEXT NOT NULL UNIQUE REFERENCES projects(id));
CREATE TABLE IF NOT EXISTS messages(id TEXT PRIMARY KEY,conversation_id TEXT NOT NULL REFERENCES conversations(id),role TEXT NOT NULL CHECK(role IN ('user','assistant')),text TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS plan_versions(project_id TEXT NOT NULL REFERENCES projects(id),version INTEGER NOT NULL,revision INTEGER NOT NULL,status TEXT NOT NULL CHECK(status IN ('draft','confirmed')),hash TEXT NOT NULL,content BLOB NOT NULL,reason TEXT NOT NULL,base_version INTEGER NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(project_id,version));
CREATE TABLE IF NOT EXISTS plan_confirmations(project_id TEXT NOT NULL,version INTEGER NOT NULL,hash TEXT NOT NULL,human_id TEXT NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(project_id,version),FOREIGN KEY(project_id,version) REFERENCES plan_versions(project_id,version));
CREATE TABLE IF NOT EXISTS change_requests(id TEXT PRIMARY KEY,project_id TEXT NOT NULL,base_version INTEGER NOT NULL,proposal_version INTEGER NOT NULL,reason TEXT NOT NULL,FOREIGN KEY(project_id,proposal_version) REFERENCES plan_versions(project_id,version));
CREATE TABLE IF NOT EXISTS documents(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id),name TEXT NOT NULL,version INTEGER NOT NULL,hash TEXT NOT NULL,text TEXT NOT NULL,status TEXT NOT NULL,created_at TEXT NOT NULL,source TEXT NOT NULL,copy_path TEXT NOT NULL,UNIQUE(project_id,name,version));
CREATE TABLE IF NOT EXISTS events(sequence INTEGER PRIMARY KEY AUTOINCREMENT,project_id TEXT NOT NULL REFERENCES projects(id),kind TEXT NOT NULL,payload BLOB NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS idempotency(scope TEXT NOT NULL,key TEXT NOT NULL,request_hash TEXT NOT NULL,result BLOB NOT NULL,PRIMARY KEY(scope,key));
CREATE TRIGGER IF NOT EXISTS immutable_confirmed_plan BEFORE UPDATE ON plan_versions WHEN OLD.status='confirmed' BEGIN SELECT RAISE(ABORT,'confirmed plan is immutable'); END;
CREATE TRIGGER IF NOT EXISTS no_delete_confirmed_plan BEFORE DELETE ON plan_versions WHEN OLD.status='confirmed' BEGIN SELECT RAISE(ABORT,'confirmed plan is immutable'); END;
INSERT OR IGNORE INTO schema_migrations VALUES(1);`

// Open enables foreign keys, WAL, a durable schema and a nonblocking instance lock.
func Open(ctx context.Context, directory string) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(directory) {
		return nil, errors.New("数据目录必须为绝对路径")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	directory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, err
	}
	lock, err := acquireLock(filepath.Join(directory, "instance.lock"))
	if err != nil {
		return nil, err
	}
	dbURL := (&url.URL{Scheme: "file", Path: filepath.Join(directory, "workbench.sqlite")}).String()
	db, err := sql.Open("sqlite3", dbURL+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate")
	if err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	db.SetMaxOpenConns(1)
	if err = migrate(ctx, db); err != nil {
		return nil, errors.Join(err, db.Close(), lock.Close())
	}
	validator, err := contracts.New()
	if err != nil {
		return nil, errors.Join(err, db.Close(), lock.Close())
	}
	return &Store{db: db, lock: lock, validator: validator, changed: make(chan struct{})}, nil
}

// migrate checks compatibility before changing any schema and commits atomically.
func migrate(ctx context.Context, db *sql.DB) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, rollbackErr)
		}
	}()
	var exists, version int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'").Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&version); err != nil {
			return err
		}
	}
	if version < 0 || version > SchemaVersion {
		return errors.New("数据库版本不受支持")
	}
	if version == 0 {
		if _, err = tx.ExecContext(ctx, migration); err != nil {
			return err
		}
	}
	if version < 2 {
		if _, err = tx.ExecContext(ctx, migration2); err != nil {
			return err
		}
	}
	if version < 3 {
		if _, err = tx.ExecContext(ctx, migration3); err != nil {
			return err
		}
	}
	if version < 4 {
		if _, err = tx.ExecContext(ctx, "ALTER TABLE projects ADD COLUMN internal INTEGER NOT NULL DEFAULT 0; INSERT INTO schema_migrations VALUES(4)"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Close releases SQLite and the single-instance lock.
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = errors.Join(s.db.Close(), s.lock.Close())
		s.mu.Lock()
		close(s.changed)
		s.changed = nil
		s.mu.Unlock()
	})
	return s.closeErr
}

// Watch returns a notification channel; durable replay remains authoritative.
func (s *Store) Watch() <-chan struct{} { s.mu.Lock(); defer s.mu.Unlock(); return s.changed }
func (s *Store) notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.changed != nil {
		close(s.changed)
		s.changed = make(chan struct{})
	}
}

// ID creates an opaque local identity.
func ID() string  { return rand.Text() }
func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func allowed(p Principal, project string, humanOnly bool) error {
	if p.ID == "" || p.Role != "human" && p.Role != "agent" || humanOnly && p.Role != "human" || p.Role == "agent" && p.ProjectID != project {
		return ErrForbidden
	}
	return nil
}
func (s *Store) write(ctx context.Context, p Principal, project, operation, key string, request any, fn func(*sql.Tx) (any, error)) (resultJSON json.RawMessage, returnErr error) {
	if key == "" || len(key) > 128 {
		return nil, errors.New("缺少有效的幂等键")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	scope := p.ID + ":" + project + ":" + operation
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	var oldHash string
	var result []byte
	err = tx.QueryRowContext(ctx, "SELECT request_hash,result FROM idempotency WHERE scope=? AND key=?", scope, key).Scan(&oldHash, &result)
	if err == nil {
		if oldHash != digest {
			return nil, ErrConflict
		}
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	value, err := fn(tx)
	if err != nil {
		return nil, err
	}
	result, err = json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO idempotency VALUES(?,?,?,?)", scope, key, digest, result); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.notify()
	return result, nil
}
func event(ctx context.Context, tx *sql.Tx, project, kind string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO events(project_id,kind,payload,created_at) VALUES(?,?,?,?)", project, kind, raw, now())
	return err
}

// CreateProject commits a prepared snapshot and default conversation in one transaction.
func (s *Store) CreateProject(ctx context.Context, p Principal, key string, project Project) (json.RawMessage, error) {
	if err := allowed(p, project.ID, true); err != nil {
		return nil, err
	}
	if project.Name == "" || len(project.Name) > 160 || !json.Valid(project.Manifest) {
		return nil, errors.New("项目名称或导入清单无效")
	}
	return s.write(ctx, p, "", "project.create", key, struct{ Name, Source string }{project.Name, project.Source}, func(tx *sql.Tx) (any, error) {
		project.CreatedAt = now()
		_, err := tx.ExecContext(ctx, "INSERT INTO projects(id,name,source,snapshot,manifest,active_version,created_at) VALUES(?,?,?,?,?,0,?)", project.ID, project.Name, project.Source, project.Snapshot, []byte(project.Manifest), project.CreatedAt)
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO conversations VALUES(?,?)", project.ID, project.ID); err != nil {
			return nil, err
		}
		if err = event(ctx, tx, project.ID, "project.created", map[string]string{"id": project.ID}); err != nil {
			return nil, err
		}
		return project, nil
	})
}

// Projects lists local projects for a human identity.
func (s *Store) Projects(ctx context.Context, p Principal) (result []Project, returnErr error) {
	if err := allowed(p, "", true); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,name,source,snapshot,manifest,active_version,created_at FROM projects WHERE internal=0 ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	result = []Project{}
	for rows.Next() {
		var v Project
		if err := rows.Scan(&v.ID, &v.Name, &v.Source, &v.Snapshot, &v.Manifest, &v.ActiveVersion, &v.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

// Project loads one project after checking project-scoped access.
func (s *Store) Project(ctx context.Context, p Principal, id string) (Project, error) {
	if err := allowed(p, id, false); err != nil {
		return Project{}, err
	}
	var v Project
	err := s.db.QueryRowContext(ctx, "SELECT id,name,source,snapshot,manifest,active_version,created_at FROM projects WHERE id=?", id).Scan(&v.ID, &v.Name, &v.Source, &v.Snapshot, &v.Manifest, &v.ActiveVersion, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return v, err
}

// AppendMessage attributes the role from authentication, not request JSON.
func (s *Store) AppendMessage(ctx context.Context, p Principal, project, key, text string) (json.RawMessage, error) {
	if err := allowed(p, project, false); err != nil {
		return nil, err
	}
	if text == "" || len(text) > 32768 {
		return nil, errors.New("消息内容为空或过长")
	}
	return s.write(ctx, p, project, "message.append", key, text, func(tx *sql.Tx) (any, error) {
		role := "user"
		if p.Role == "agent" {
			role = "assistant"
		}
		v := Message{ID: ID(), Role: role, Text: text, CreatedAt: now()}
		if _, err := tx.ExecContext(ctx, "INSERT INTO messages VALUES(?,?,?,?,?)", v.ID, project, role, text, v.CreatedAt); err != nil {
			return nil, err
		}
		if err := event(ctx, tx, project, "message.created", v); err != nil {
			return nil, err
		}
		return v, nil
	})
}

// Messages returns the durable default conversation in insertion order.
func (s *Store) Messages(ctx context.Context, p Principal, project string) (result []Message, returnErr error) {
	if err := allowed(p, project, false); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,role,text,created_at FROM messages WHERE conversation_id=? ORDER BY rowid", project)
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	result = []Message{}
	for rows.Next() {
		var v Message
		if err := rows.Scan(&v.ID, &v.Role, &v.Text, &v.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

// CreatePlan appends a schema-validated draft; an existing confirmed version remains active.
func (s *Store) CreatePlan(ctx context.Context, p Principal, project, key, reason string, base int, content json.RawMessage) (json.RawMessage, error) {
	if err := allowed(p, project, false); err != nil {
		return nil, err
	}
	return s.write(ctx, p, project, "plan.create", key, struct {
		Reason  string
		Base    int
		Content json.RawMessage
	}{reason, base, content}, func(tx *sql.Tx) (any, error) {
		var active int
		if err := tx.QueryRowContext(ctx, "SELECT active_version FROM projects WHERE id=?", project).Scan(&active); err != nil {
			return nil, ErrNotFound
		}
		if base != active || active > 0 && reason == "" {
			return nil, errors.New("请基于当前确认版本提出变更并填写原因")
		}
		var version int
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0)+1 FROM plan_versions WHERE project_id=?", project).Scan(&version); err != nil {
			return nil, err
		}
		var object map[string]any
		if json.Unmarshal(content, &object) != nil || object == nil {
			return nil, errors.New("方案必须是 JSON 对象")
		}
		object["project_id"], object["version"], object["schema_version"] = project, version, 1
		raw, err := json.Marshal(object)
		if err != nil {
			return nil, err
		}
		if err = s.validator.Validate("plan", raw); err != nil {
			return nil, err
		}
		if base != 0 {
			var status string
			if err = tx.QueryRowContext(ctx, "SELECT status FROM plan_versions WHERE project_id=? AND version=?", project, base).Scan(&status); err != nil {
				return nil, ErrNotFound
			}
			if status != "confirmed" || reason == "" {
				return nil, errors.New("变更草案需要已确认基线与变更原因")
			}
		}
		hash, err := contractguard.CanonicalHash(raw)
		if err != nil {
			return nil, err
		}
		v := Plan{ProjectID: project, Version: version, Revision: 1, Status: "draft", Hash: hash, Content: raw, Reason: reason, BaseVersion: base, CreatedAt: now()}
		if _, err = tx.ExecContext(ctx, "INSERT INTO plan_versions VALUES(?,?,?,?,?,?,?,?,?)", project, version, 1, "draft", hash, raw, reason, base, v.CreatedAt); err != nil {
			return nil, err
		}
		if base != 0 {
			if _, err = tx.ExecContext(ctx, "INSERT INTO change_requests VALUES(?,?,?,?,?)", ID(), project, base, version, reason); err != nil {
				return nil, err
			}
		}
		if err = event(ctx, tx, project, "plan.drafted", map[string]any{"version": version, "hash": hash}); err != nil {
			return nil, err
		}
		return v, nil
	})
}

// UpdatePlan revises only a draft and requires its exact previously read hash/revision.
func (s *Store) UpdatePlan(ctx context.Context, p Principal, project, key string, version, revision int, hash string, content json.RawMessage) (json.RawMessage, error) {
	if err := allowed(p, project, false); err != nil {
		return nil, err
	}
	return s.write(ctx, p, project, "plan.update", key, struct {
		Version, Revision int
		Hash              string
		Content           json.RawMessage
	}{version, revision, hash, content}, func(tx *sql.Tx) (any, error) {
		v, err := getPlan(ctx, tx, project, version)
		if err != nil {
			return nil, err
		}
		if v.Status != "draft" || v.Revision != revision || v.Hash != hash {
			return nil, ErrConflict
		}
		var doc map[string]any
		if json.Unmarshal(content, &doc) != nil || doc == nil {
			return nil, errors.New("方案必须是 JSON 对象")
		}
		doc["project_id"], doc["version"], doc["schema_version"] = project, version, 1
		raw, err := json.Marshal(doc)
		if err != nil {
			return nil, err
		}
		if err = s.validator.Validate("plan", raw); err != nil {
			return nil, err
		}
		newHash, err := contractguard.CanonicalHash(raw)
		if err != nil {
			return nil, err
		}
		v.Content, v.Hash, v.Revision = raw, newHash, v.Revision+1
		if _, err = tx.ExecContext(ctx, "UPDATE plan_versions SET content=?,hash=?,revision=? WHERE project_id=? AND version=?", raw, newHash, v.Revision, project, version); err != nil {
			return nil, err
		}
		if err = event(ctx, tx, project, "plan.updated", map[string]any{"version": version, "revision": v.Revision, "hash": newHash}); err != nil {
			return nil, err
		}
		return v, nil
	})
}

// ConfirmPlan is deliberately human-only, with exact hash and revision matching.
func (s *Store) ConfirmPlan(ctx context.Context, p Principal, project, key string, version, revision int, hash string) (json.RawMessage, error) {
	if err := allowed(p, project, true); err != nil {
		return nil, err
	}
	return s.write(ctx, p, project, "plan.confirm", key, struct {
		Version, Revision int
		Hash              string
	}{version, revision, hash}, func(tx *sql.Tx) (any, error) {
		v, err := getPlan(ctx, tx, project, version)
		if err != nil {
			return nil, err
		}
		if v.Status != "draft" || v.Revision != revision || v.Hash != hash {
			return nil, ErrConflict
		}
		var active int
		if err = tx.QueryRowContext(ctx, "SELECT active_version FROM projects WHERE id=?", project).Scan(&active); err != nil {
			return nil, err
		}
		if active != v.BaseVersion {
			return nil, ErrConflict
		}
		if _, err = tx.ExecContext(ctx, "UPDATE plan_versions SET status='confirmed' WHERE project_id=? AND version=?", project, version); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO plan_confirmations VALUES(?,?,?,?,?)", project, version, hash, p.ID, now()); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE projects SET active_version=? WHERE id=?", version, project); err != nil {
			return nil, err
		}
		v.Status = "confirmed"
		if err = event(ctx, tx, project, "plan.confirmed", map[string]any{"version": version, "hash": hash}); err != nil {
			return nil, err
		}
		return v, nil
	})
}

type planQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getPlan(ctx context.Context, q planQuerier, project string, version int) (Plan, error) {
	var v Plan
	err := q.QueryRowContext(ctx, "SELECT project_id,version,revision,status,hash,content,reason,base_version,created_at FROM plan_versions WHERE project_id=? AND version=?", project, version).Scan(&v.ProjectID, &v.Version, &v.Revision, &v.Status, &v.Hash, &v.Content, &v.Reason, &v.BaseVersion, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return v, err
}

// Plans lists all immutable historical versions and drafts.
func (s *Store) Plans(ctx context.Context, p Principal, project string) (result []Plan, returnErr error) {
	if err := allowed(p, project, false); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT project_id,version,revision,status,hash,content,reason,base_version,created_at FROM plan_versions WHERE project_id=? ORDER BY version", project)
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	result = []Plan{}
	for rows.Next() {
		var v Plan
		if err := rows.Scan(&v.ProjectID, &v.Version, &v.Revision, &v.Status, &v.Hash, &v.Content, &v.Reason, &v.BaseVersion, &v.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

// Events replays persisted project events strictly after a committed cursor.
func (s *Store) Events(ctx context.Context, p Principal, project string, cursor int64) (result []Event, returnErr error) {
	if err := allowed(p, project, false); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT sequence,project_id,kind,payload,created_at FROM events WHERE project_id=? AND sequence>? ORDER BY sequence LIMIT 256", project, cursor)
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	result = []Event{}
	for rows.Next() {
		var v Event
		if err := rows.Scan(&v.Sequence, &v.ProjectID, &v.Kind, &v.Payload, &v.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

// AddDocument versions imported document content without changing confirmed plans.
func (s *Store) AddDocument(ctx context.Context, p Principal, project, key string, v Document) (json.RawMessage, error) {
	if err := allowed(p, project, true); err != nil {
		return nil, err
	}
	if v.Name == "" || len(v.Text) > 2<<20 || v.Status != "ready" && v.Status != "needs_parser" {
		return nil, errors.New("资料内容无效")
	}
	return s.write(ctx, p, project, "document.add", key, v, func(tx *sql.Tx) (any, error) {
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0)+1 FROM documents WHERE project_id=? AND name=?", project, v.Name).Scan(&v.Version); err != nil {
			return nil, err
		}
		v.ID, v.CreatedAt = ID(), now()
		if _, err := tx.ExecContext(ctx, "INSERT INTO documents VALUES(?,?,?,?,?,?,?,?,?,?)", v.ID, project, v.Name, v.Version, v.Hash, v.Text, v.Status, v.CreatedAt, v.Source, v.CopyPath); err != nil {
			return nil, err
		}
		if err := event(ctx, tx, project, "document.added", map[string]any{"id": v.ID, "name": v.Name, "version": v.Version, "hash": v.Hash, "status": v.Status}); err != nil {
			return nil, err
		}
		return v, nil
	})
}

// Documents returns document versions ordered by creation.
func (s *Store) Documents(ctx context.Context, p Principal, project string) (result []Document, returnErr error) {
	if err := allowed(p, project, false); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,name,version,hash,text,status,created_at,source,copy_path FROM documents WHERE project_id=? ORDER BY rowid", project)
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	result = []Document{}
	for rows.Next() {
		var v Document
		if err := rows.Scan(&v.ID, &v.Name, &v.Version, &v.Hash, &v.Text, &v.Status, &v.CreatedAt, &v.Source, &v.CopyPath); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

// CheckHealth verifies the database engine and foreign-key invariant.
func (s *Store) CheckHealth(ctx context.Context) error {
	var enabled int
	if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil {
		return err
	}
	if enabled != 1 {
		return fmt.Errorf("foreign keys disabled")
	}
	return s.db.PingContext(ctx)
}
