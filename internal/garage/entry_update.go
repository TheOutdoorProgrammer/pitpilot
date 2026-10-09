package garage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// UpdateEntry keeps the read and sparse update in one transaction so unrelated
// edits cannot erase metadata or overwrite one another's changes.
func (s *Store) UpdateEntry(ctx context.Context, id, kind string, change func(map[string]json.RawMessage) error) (out json.RawMessage, err error) {
	ctx, done := operation(ctx, "db.entry.update")
	defer func() { done(err) }()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var data []byte
	if err = tx.QueryRowContext(ctx, "SELECT data FROM entries WHERE id=? AND kind=?", id, kind).Scan(&data); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if err = change(fields); err != nil {
		return nil, err
	}
	if out, err = json.Marshal(fields); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE entries SET data=? WHERE id=? AND kind=?", string(out), id, kind); err != nil {
		return nil, err
	}
	if kind == "record" {
		var record Record
		if err = json.Unmarshal(out, &record); err != nil {
			return nil, err
		}
		if err = syncRecordOdometer(ctx, tx, record); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit()
}
