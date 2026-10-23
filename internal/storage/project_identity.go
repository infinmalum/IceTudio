package storage

import (
	"context"
	"database/sql"

	"icetudio/internal/project"
)

// projectIdentityHistoryTx reads existing authoritative history in the same
// transaction as the new edit. Results cover all creations (even undone ones);
// inverse payloads also cover imported objects removed with a parent, including
// RawEvent IDs that have no standalone add operation. No history is rewritten.
func (s *Store) projectIdentityHistoryTx(ctx context.Context, tx *sql.Tx, projectID string, ops []project.Op) (map[string]bool, error) {
	creates := false
	for _, op := range ops {
		switch op.Kind {
		case "track.add", "clip.add", "note.add", "control.add":
			creates = true
		}
	}
	if !creates {
		return nil, nil
	}
	// Stream only identity strings, not entire historical event/state payloads
	// into Go. Both JSON paths refer to fields owned by the current service.
	rows, err := tx.QueryContext(ctx, `
		SELECT j.value FROM project_revisions AS r, json_each(r.result, '$.createdIds') AS j
		WHERE r.project_id=? AND j.type='text'
		UNION
		SELECT j.atom FROM project_revisions AS r, json_tree(r.inverse) AS j
		WHERE r.project_id=? AND j.key IN ('id','trackId','clipId') AND j.type='text'`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}
