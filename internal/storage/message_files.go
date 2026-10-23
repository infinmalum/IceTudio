package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"icetudio/internal/materials"
)

func (s *Store) MessageFiles(ctx context.Context, id string) ([]materials.File, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM message_files WHERE message_id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []materials.File
	err = json.Unmarshal([]byte(raw), &files)
	return files, err
}
func (s *Store) RunInputFiles(ctx context.Context, run string) ([]materials.File, error) {
	root, err := s.RootRunID(ctx, run)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT f.payload FROM messages m JOIN message_files f ON f.message_id=m.id WHERE m.run_id=? AND m.role='user' ORDER BY m.seq`, root)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []materials.File
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var batch []materials.File
		if err := json.Unmarshal([]byte(raw), &batch); err != nil {
			return nil, err
		}
		files = append(files, batch...)
	}
	return files, rows.Err()
}
