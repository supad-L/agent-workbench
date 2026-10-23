package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"

	"agent-workbench/internal/accountcoord"
	"agent-workbench/internal/runtime"
	"agent-workbench/internal/sandbox"
)

const migration5 = `
CREATE TABLE native_sessions(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id),revision INTEGER NOT NULL,data BLOB NOT NULL);
CREATE TABLE native_attempts(session_id TEXT NOT NULL REFERENCES native_sessions(id),number INTEGER NOT NULL,request_key TEXT NOT NULL,request_hash TEXT NOT NULL,data BLOB NOT NULL,PRIMARY KEY(session_id,number),UNIQUE(session_id,request_key));
INSERT INTO schema_migrations VALUES(5);`

var errNativeUnchanged = errors.New("native journal unchanged")

var nativeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// NativeBinding is a fixed platform-owned snapshot/environment, not account access.
type NativeBinding struct {
	Account *accountcoord.Reference `json:"account,omitempty"`
	Runtime sandbox.RuntimeKind     `json:"runtime"`
	Spec    sandbox.Spec            `json:"spec"`
	Binary  string                  `json:"binary"`
	Model   string                  `json:"model"`
}

// UncleanNativeAccount detects persistent cleanup debt, including attempts that
// already settled as uncertain and therefore are no longer restart-pending.
func (s *Store) UncleanNativeAccount(ctx context.Context) (unclean bool, returnErr error) {
	rows, err := s.db.QueryContext(ctx, "SELECT data FROM native_sessions")
	if err != nil {
		return false, err
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	for rows.Next() {
		var raw []byte
		var session NativeSession
		if err = rows.Scan(&raw); err != nil {
			return false, err
		}
		if err = json.Unmarshal(raw, &session); err != nil {
			return false, err
		}
		if session.Binding.Account != nil && session.Attempt > 0 && !session.CleanupVerified {
			unclean = true
		}
	}
	return unclean, rows.Err()
}

// NativeSession stores normalized progress separately from opaque CLI state files.
type NativeSession struct {
	ID              string                   `json:"id"`
	ProjectID       string                   `json:"project_id"`
	Revision        int                      `json:"revision"`
	Binding         NativeBinding            `json:"binding"`
	RunDirectory    string                   `json:"run_directory"`
	SourceDigest    string                   `json:"source_digest"`
	StateDigest     string                   `json:"state_digest,omitempty"`
	PlanVersion     int                      `json:"plan_version"`
	PlanHash        string                   `json:"plan_hash,omitempty"`
	NativeID        string                   `json:"native_session_id,omitempty"`
	Status          string                   `json:"status"`
	Recovery        string                   `json:"recovery"`
	Attempt         int                      `json:"attempt"`
	Checkpoint      runtime.NativeCheckpoint `json:"checkpoint"`
	CleanupVerified bool                     `json:"cleanup_verified"`
	ErrorCode       string                   `json:"error_code,omitempty"`
	CreatedAt       string                   `json:"created_at"`
	UpdatedAt       string                   `json:"updated_at"`
}

// NativeAttempt is one explicitly requested execution. A request key never launches twice.
type NativeAttempt struct {
	Number     int                       `json:"number"`
	Mode       string                    `json:"mode"`
	Status     string                    `json:"status"`
	Container  sandbox.ExecutionIdentity `json:"container"`
	Sequence   int                       `json:"checkpoint_sequence"`
	Checkpoint runtime.NativeCheckpoint  `json:"checkpoint"`
	Result     runtime.NativeResult      `json:"result"`
	CreatedAt  string                    `json:"created_at"`
	UpdatedAt  string                    `json:"updated_at"`
}

// NativeCreate only accepts metadata prepared by the trusted native service.
type NativeCreate struct {
	Root         string
	Binding      NativeBinding
	SourceDigest string
	Key          string
}

func readNative(ctx context.Context, q planQuerier, id string) (NativeSession, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, "SELECT data FROM native_sessions WHERE id=?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return NativeSession{}, ErrNotFound
	}
	var session NativeSession
	if err == nil {
		err = json.Unmarshal(raw, &session)
	}
	return session, err
}
func readNativeAttempt(ctx context.Context, q planQuerier, id string, number int) (NativeAttempt, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, "SELECT data FROM native_attempts WHERE session_id=? AND number=?", id, number).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return NativeAttempt{}, ErrNotFound
	}
	var attempt NativeAttempt
	if err == nil {
		err = json.Unmarshal(raw, &attempt)
	}
	return attempt, err
}
func writeNative(ctx context.Context, tx *sql.Tx, session *NativeSession, attempt *NativeAttempt) error {
	session.Revision++
	session.UpdatedAt = now()
	raw, err := json.Marshal(session)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE native_sessions SET revision=?,data=? WHERE id=?", session.Revision, raw, session.ID); err != nil {
		return err
	}
	if attempt != nil {
		attempt.UpdatedAt = session.UpdatedAt
		raw, err = json.Marshal(attempt)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE native_attempts SET data=? WHERE session_id=? AND number=?", raw, session.ID, attempt.Number); err != nil {
			return err
		}
	}
	return event(ctx, tx, session.ProjectID, "native_session.updated", map[string]any{"session_id": session.ID, "status": session.Status, "attempt": session.Attempt})
}

// CreateNativeSession records a new logical session without starting a process.
func (s *Store) CreateNativeSession(ctx context.Context, p Principal, project string, input NativeCreate) (NativeSession, error) {
	if err := allowed(p, project, true); err != nil {
		return NativeSession{}, err
	}
	if !filepath.IsAbs(input.Root) || input.SourceDigest == "" || input.Binding.Model == "" {
		return NativeSession{}, errors.New("原生会话绑定无效")
	}
	data, err := s.write(ctx, p, project, "native.create", input.Key, input, func(tx *sql.Tx) (any, error) {
		session := NativeSession{ID: ID(), ProjectID: project, Revision: 1, Binding: input.Binding, SourceDigest: input.SourceDigest, Status: "ready", Recovery: "not_started", CleanupVerified: true, CreatedAt: now(), UpdatedAt: now()}
		session.RunDirectory = filepath.Join(input.Root, session.ID)
		if err := tx.QueryRowContext(ctx, "SELECT active_version FROM projects WHERE id=?", project).Scan(&session.PlanVersion); err != nil {
			return nil, err
		}
		if session.PlanVersion > 0 {
			if err := tx.QueryRowContext(ctx, "SELECT hash FROM plan_versions WHERE project_id=? AND version=? AND status='confirmed'", project, session.PlanVersion).Scan(&session.PlanHash); err != nil {
				return nil, err
			}
		}
		raw, err := json.Marshal(session)
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO native_sessions VALUES(?,?,?,?)", session.ID, project, session.Revision, raw); err != nil {
			return nil, err
		}
		if err = event(ctx, tx, project, "native_session.created", map[string]string{"session_id": session.ID}); err != nil {
			return nil, err
		}
		return session, nil
	})
	if err != nil {
		return NativeSession{}, err
	}
	var created NativeSession
	if err = json.Unmarshal(data, &created); err != nil {
		return created, err
	}
	// Return the current record on idempotent creation, never the stale initial revision.
	return s.NativeSession(ctx, p, project, created.ID)
}

// NativeSession is human-scoped; task Agents cannot access native conversations here.
func (s *Store) NativeSession(ctx context.Context, p Principal, project, id string) (NativeSession, error) {
	if err := allowed(p, project, true); err != nil {
		return NativeSession{}, err
	}
	session, err := readNative(ctx, s.db, id)
	if err == nil && session.ProjectID != project {
		return NativeSession{}, ErrNotFound
	}
	return session, err
}

// NativeSessions returns project-local metadata, without reading any credential files.
func (s *Store) NativeSessions(ctx context.Context, p Principal, project string) (result []NativeSession, returnErr error) {
	if err := allowed(p, project, true); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT data FROM native_sessions WHERE project_id=? ORDER BY rowid DESC", project)
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	result = []NativeSession{}
	for rows.Next() {
		var raw []byte
		var session NativeSession
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &session); err != nil {
			return nil, err
		}
		result = append(result, session)
	}
	return result, rows.Err()
}

// NativeAttempts preserves each previous attempt when continuing a session.
func (s *Store) NativeAttempts(ctx context.Context, p Principal, project, id string) (result []NativeAttempt, returnErr error) {
	if _, err := s.NativeSession(ctx, p, project, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT data FROM native_attempts WHERE session_id=? ORDER BY number", id)
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	result = []NativeAttempt{}
	for rows.Next() {
		var raw []byte
		var attempt NativeAttempt
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &attempt); err != nil {
			return nil, err
		}
		result = append(result, attempt)
	}
	return result, rows.Err()
}

// ClaimNativeAttempt atomically fences concurrent/replayed actions. Only the winner executes.
func (s *Store) ClaimNativeAttempt(ctx context.Context, p Principal, project, id, key, prompt string, revision int, resume bool) (session NativeSession, attempt NativeAttempt, claimed bool, returnErr error) {
	if err := allowed(p, project, true); err != nil {
		return session, attempt, false, err
	}
	if key == "" || len(key) > 128 || len(prompt) == 0 || len(prompt) > 1<<20 {
		return session, attempt, false, errors.New("原生执行需要显式输入与有效幂等键")
	}
	mode := "start"
	if resume {
		mode = "resume"
	}
	hash := sha256.Sum256([]byte(mode + "\x00" + prompt))
	digest := hex.EncodeToString(hash[:])
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		session, err = readNative(ctx, tx, id)
		if err != nil {
			return err
		}
		if session.ProjectID != project {
			return ErrNotFound
		}
		var existingHash string
		var raw []byte
		err = tx.QueryRowContext(ctx, "SELECT request_hash,data FROM native_attempts WHERE session_id=? AND request_key=?", id, key).Scan(&existingHash, &raw)
		if err == nil {
			if existingHash != digest {
				return ErrConflict
			}
			return json.Unmarshal(raw, &attempt)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if session.Revision != revision || session.Status == "running" || session.Recovery == "uncertain" {
			return ErrConflict
		}
		if resume {
			if session.Recovery != "resumable" || session.NativeID == "" || !session.CleanupVerified || session.StateDigest == "" {
				return ErrConflict
			}
		} else if session.Recovery != "not_started" {
			return ErrConflict
		}
		var version int
		if err = tx.QueryRowContext(ctx, "SELECT active_version FROM projects WHERE id=?", project).Scan(&version); err != nil {
			return err
		}
		if version != session.PlanVersion {
			return ErrConflict
		}
		session.Attempt++
		session.Status = "running"
		session.Recovery = "uncertain"
		session.CleanupVerified = false
		session.ErrorCode = ""
		session.Checkpoint = runtime.NativeCheckpoint{SessionID: session.NativeID}
		attempt = NativeAttempt{Number: session.Attempt, Mode: mode, Status: "prepared", Checkpoint: session.Checkpoint, CreatedAt: now(), UpdatedAt: now()}
		raw, err = json.Marshal(attempt)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO native_attempts VALUES(?,?,?,?,?)", id, attempt.Number, key, digest, raw); err != nil {
			return err
		}
		if err = writeNative(ctx, tx, &session, nil); err != nil {
			return err
		}
		claimed = true
		return nil
	})
	if err == nil {
		s.notify()
	}
	return session, attempt, claimed, err
}

// MarkNativeLaunch durably saves identity before the engine is allowed to create a process.
func (s *Store) MarkNativeLaunch(ctx context.Context, id string, number int, identity sandbox.ExecutionIdentity) error {
	return s.mutateNative(ctx, id, number, func(session *NativeSession, attempt *NativeAttempt) error {
		if identity.Name == "" || identity.Runtime != session.Binding.Runtime {
			return ErrConflict
		}
		if attempt.Status != "prepared" {
			if attempt.Container == identity {
				return errNativeUnchanged
			}
			return ErrConflict
		}
		attempt.Status = "launching"
		attempt.Container = identity
		return nil
	})
}

// CheckpointNative only advances the current execution generation and sequence.
func (s *Store) CheckpointNative(ctx context.Context, id string, number, sequence int, checkpoint runtime.NativeCheckpoint) error {
	if len(checkpoint.Text) > 1<<20 || checkpoint.SessionID != "" && !nativeID.MatchString(checkpoint.SessionID) || checkpoint.Usage.InputTokens < 0 || checkpoint.Usage.OutputTokens < 0 {
		return errors.New("原生检查点无效或超过上限")
	}
	return s.mutateNative(ctx, id, number, func(session *NativeSession, attempt *NativeAttempt) error {
		if attempt.Status != "launching" {
			return ErrConflict
		}
		if sequence == attempt.Sequence {
			if attempt.Checkpoint == checkpoint {
				return errNativeUnchanged
			}
			return ErrConflict
		}
		if sequence != attempt.Sequence+1 || session.NativeID != "" && checkpoint.SessionID != session.NativeID {
			return ErrConflict
		}
		session.NativeID = checkpoint.SessionID
		session.Checkpoint = checkpoint
		attempt.Checkpoint = checkpoint
		attempt.Sequence = sequence
		return nil
	})
}

func (s *Store) mutateNative(ctx context.Context, id string, number int, change func(*NativeSession, *NativeAttempt) error) error {
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		session, err := readNative(ctx, tx, id)
		if err != nil {
			return err
		}
		if session.Status != "running" || session.Attempt != number {
			return ErrConflict
		}
		attempt, err := readNativeAttempt(ctx, tx, id, number)
		if err != nil {
			return err
		}
		if err = change(&session, &attempt); errors.Is(err, errNativeUnchanged) {
			return nil
		} else if err != nil {
			return err
		}
		return writeNative(ctx, tx, &session, &attempt)
	})
	if err == nil {
		s.notify()
	}
	return err
}

// FinishNative preserves unknown outcomes; a session ID alone never enables recovery.
func (s *Store) FinishNative(ctx context.Context, id string, number int, result runtime.NativeResult, stateDigest string) error {
	switch result.Status {
	case "completed", "interrupted", "failed", "uncertain", "blocked":
	default:
		return errors.New("原生执行终态无效")
	}
	if !result.CleanupVerified && result.Status != "blocked" {
		result.Status = "uncertain"
		result.ErrorCode = "native_cleanup_unconfirmed"
	}
	if len(result.Text) > 1<<20 || result.SessionID != "" && !nativeID.MatchString(result.SessionID) {
		return errors.New("原生执行结果无效")
	}
	return s.mutateNative(ctx, id, number, func(session *NativeSession, attempt *NativeAttempt) error {
		if session.NativeID != "" && result.SessionID != "" && session.NativeID != result.SessionID {
			return ErrConflict
		}
		if result.SessionID != "" {
			session.NativeID = result.SessionID
		}
		previousDigest := session.StateDigest
		session.Status = result.Status
		session.CleanupVerified = result.CleanupVerified
		session.StateDigest = stateDigest
		session.ErrorCode = result.ErrorCode
		session.Recovery = "uncertain"
		if result.CleanupVerified && result.Status != "uncertain" {
			session.Recovery = "interrupted"
			if session.NativeID != "" && stateDigest != "" {
				session.Recovery = "resumable"
			}
		}
		if result.Status == "blocked" && attempt.Container.Name == "" {
			session.Recovery = "not_started"
			session.CleanupVerified = true
			if attempt.Mode == "resume" && session.NativeID != "" && previousDigest != "" {
				session.StateDigest = previousDigest
				session.Recovery = "resumable"
			}
		}
		attempt.Status = result.Status
		attempt.Result = result
		return nil
	})
}

// PendingNativeSessions returns only records requiring restart reconciliation.
func (s *Store) PendingNativeSessions(ctx context.Context) (result []NativeSession, returnErr error) {
	rows, err := s.db.QueryContext(ctx, "SELECT data FROM native_sessions")
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	for rows.Next() {
		var raw []byte
		var session NativeSession
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &session); err != nil {
			return nil, err
		}
		if session.Status == "running" {
			result = append(result, session)
		}
	}
	return result, rows.Err()
}

// RecoverNative records interrupted/unknown work; it never restarts an invocation.
func (s *Store) RecoverNative(ctx context.Context, id string, number int, cleanupVerified bool) error {
	return s.mutateNative(ctx, id, number, func(session *NativeSession, attempt *NativeAttempt) error {
		session.Status = "uncertain"
		session.Recovery = "uncertain"
		session.ErrorCode = "native_restart_outcome_unknown"
		session.CleanupVerified = cleanupVerified
		if attempt.Status == "prepared" && attempt.Container.Name == "" {
			session.Status = "interrupted"
			session.Recovery = "not_started"
			session.CleanupVerified = true
			session.ErrorCode = "native_restart_before_launch"
			if session.NativeID != "" && session.StateDigest != "" {
				session.Recovery = "resumable"
			}
		}
		attempt.Status = session.Status
		return nil
	})
}

// NativeBasisCurrent checks the confirmed plan pointer before offering continuation.
func (s *Store) NativeBasisCurrent(ctx context.Context, session NativeSession) bool {
	var version int
	if s.db.QueryRowContext(ctx, "SELECT active_version FROM projects WHERE id=?", session.ProjectID).Scan(&version) != nil || version != session.PlanVersion {
		return false
	}
	if version == 0 {
		return true
	}
	var hash string
	return s.db.QueryRowContext(ctx, "SELECT hash FROM plan_versions WHERE project_id=? AND version=? AND status='confirmed'", session.ProjectID, version).Scan(&hash) == nil && hash == session.PlanHash
}
