package store

import (
	"context"
	"fmt"
)

// RememberedCommands is how many command execution ids the store keeps: a command delivered again
// within the last this many runs is never run twice.
const RememberedCommands = 256

// CommandRecord is a command the node claimed and has not finished.
type CommandRecord struct {
	ID      string
	Payload []byte
}

// ClaimCommand records a command execution id durably before the command runs. It reports false
// when the id was claimed before, in which case the command must not run again. payload is kept
// until the command finishes, so a restart can report what was interrupted.
func (s *Store) ClaimCommand(id string, payload []byte) (bool, error) {
	if payload == nil {
		payload = []byte{}
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("store: claim command: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `INSERT INTO commands (id, finished, payload) VALUES (?, 0, ?) ON CONFLICT (id) DO NOTHING`, id, payload)
	if err != nil {
		return false, fmt.Errorf("store: claim command: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM commands WHERE finished = 1 AND n <= (SELECT MAX(n) FROM commands) - ?`, RememberedCommands); err != nil {
		return false, fmt.Errorf("store: claim command: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("store: claim command: %w", err)
	}
	return true, nil
}

// ForgetCommand undoes a claim whose command never started, so a later delivery may run it.
func (s *Store) ForgetCommand(id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.db.ExecContext(context.Background(), `DELETE FROM commands WHERE id = ? AND finished = 0`, id); err != nil {
		return fmt.Errorf("store: forget command: %w", err)
	}
	return nil
}

// FinishCommand records that a claimed command reached its final state.
func (s *Store) FinishCommand(id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.db.ExecContext(context.Background(), `UPDATE commands SET finished = 1, payload = x'' WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: finish command: %w", err)
	}
	return nil
}

// UnfinishedCommands are the commands claimed and not finished, oldest first: after a restart,
// the ones the node was running when it stopped.
func (s *Store) UnfinishedCommands() ([]CommandRecord, error) {
	rows, err := s.db.QueryContext(context.Background(), `SELECT id, payload FROM commands WHERE finished = 0 ORDER BY n`)
	if err != nil {
		return nil, fmt.Errorf("store: unfinished commands: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []CommandRecord
	for rows.Next() {
		var r CommandRecord
		if err := rows.Scan(&r.ID, &r.Payload); err != nil {
			return nil, fmt.Errorf("store: unfinished commands: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
