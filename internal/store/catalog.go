package store

import (
	"agent-workbench/internal/catalog"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const migration2 = `
CREATE TABLE connection_catalog(id TEXT PRIMARY KEY,revision INTEGER NOT NULL,account_id TEXT NOT NULL,data BLOB NOT NULL);
CREATE TABLE roles(id TEXT PRIMARY KEY,revision INTEGER NOT NULL,data BLOB NOT NULL);
CREATE TABLE role_connections(role_id TEXT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,connection_id TEXT NOT NULL REFERENCES connection_catalog(id),PRIMARY KEY(role_id,connection_id));
INSERT INTO schema_migrations VALUES(2);`

// SchemaVersion is the latest compatible local database version.
const SchemaVersion = 4

// transaction commits one serialized catalog mutation or rolls it back entirely.
func (s *Store) transaction(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, rollbackErr)
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// PutConnection imports a human-owned, secret-free vault projection. Equal revisions must be equal.
func (s *Store) PutConnection(ctx context.Context, p Principal, c catalog.Connection) error {
	if err := allowed(p, "", true); err != nil {
		return err
	}
	if err := c.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var revision int
		var previous []byte
		err := tx.QueryRowContext(ctx, "SELECT revision,data FROM connection_catalog WHERE id=?", c.ID).Scan(&revision, &previous)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if c.Revision < revision {
				return ErrConflict
			}
			if c.Revision == revision {
				if string(previous) != string(raw) {
					return ErrConflict
				}
				return nil
			}
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO connection_catalog VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,account_id=excluded.account_id,data=excluded.data", c.ID, c.Revision, c.AccountID, raw)
		return err
	})
}

// Connections returns public metadata and never resolves a secret reference.
func (s *Store) Connections(ctx context.Context, p Principal) (result []catalog.Connection, returnErr error) {
	if err := allowed(p, "", true); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT data FROM connection_catalog ORDER BY rowid")
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	result = []catalog.Connection{}
	for rows.Next() {
		var raw []byte
		var c catalog.Connection
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

// DeleteConnection requires its current revision and refuses referenced connections.
func (s *Store) DeleteConnection(ctx context.Context, p Principal, id string, revision int) error {
	if err := allowed(p, "", true); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var references int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM role_connections WHERE connection_id=?", id).Scan(&references); err != nil {
			return err
		}
		if references > 0 {
			return errors.New("连接仍被角色引用，请先修改角色绑定")
		}
		result, err := tx.ExecContext(ctx, "DELETE FROM connection_catalog WHERE id=? AND revision=?", id, revision)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrConflict
		}
		return nil
	})
}

// Roles returns persistent role cards. Templates are returned separately, never auto-activated.
func (s *Store) Roles(ctx context.Context, p Principal) (result []catalog.Role, returnErr error) {
	if err := allowed(p, "", true); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT data FROM roles ORDER BY rowid")
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, rows.Close()) }()
	result = []catalog.Role{}
	for rows.Next() {
		var raw []byte
		var r catalog.Role
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

func checkChoice(ctx context.Context, tx *sql.Tx, choice catalog.Choice, needsTools bool) error {
	var raw []byte
	if err := tx.QueryRowContext(ctx, "SELECT data FROM connection_catalog WHERE id=?", choice.ConnectionID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("角色引用的连接不存在")
		}
		return err
	}
	var c catalog.Connection
	if err := json.Unmarshal(raw, &c); err != nil {
		return err
	}
	if !c.Enabled {
		return errors.New("角色引用的连接已停用")
	}
	for _, m := range c.Models {
		if m.ID == choice.ModelID {
			if needsTools && m.Tools == "unsupported" {
				return errors.New("仅支持文本的模型不能用于需要工具的角色")
			}
			return nil
		}
	}
	return errors.New("角色引用的模型不在该连接的模型列表中")
}

// SaveRole checks bindings and optimistic revisions inside the same database transaction.
func (s *Store) SaveRole(ctx context.Context, p Principal, r catalog.Role) (catalog.Role, error) {
	if err := allowed(p, "", true); err != nil {
		return catalog.Role{}, err
	}
	if err := r.Validate(); err != nil {
		return catalog.Role{}, err
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var revision int
		err := tx.QueryRowContext(ctx, "SELECT revision FROM roles WHERE id=?", r.ID).Scan(&revision)
		if errors.Is(err, sql.ErrNoRows) {
			if r.Revision != 0 {
				return ErrConflict
			}
		} else if err != nil {
			return err
		} else if revision != r.Revision {
			return ErrConflict
		}
		choices := []catalog.Choice{}
		if r.Primary.ConnectionID != "" {
			choices = append(choices, r.Primary)
		}
		if r.Fallback.Enabled {
			choices = append(choices, r.Fallback.Candidates...)
		}
		for _, choice := range choices {
			if err = checkChoice(ctx, tx, choice, len(r.Tools) > 0); err != nil {
				return err
			}
		}
		r.Revision++
		raw, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO roles VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,data=excluded.data", r.ID, r.Revision, raw); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM role_connections WHERE role_id=?", r.ID); err != nil {
			return err
		}
		for _, choice := range choices {
			if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO role_connections VALUES(?,?)", r.ID, choice.ConnectionID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return catalog.Role{}, err
	}
	return r, nil
}

// DeleteRole rejects stale revisions. Running session references are protected separately.
func (s *Store) DeleteRole(ctx context.Context, p Principal, id string, revision int) error {
	if err := allowed(p, "", true); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, "DELETE FROM roles WHERE id=? AND revision=?", id, revision)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}
