package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"icetudio/internal/config"
)

// Preview the same resource assembly used at task creation, without creating a
// run or performing model/tool calls. Execution still captures and checks anew.
func (s *Store) PreviewResources(ctx context.Context, session string, source config.Model, snapshot SkillSnapshotter, role string) (RunResources, error) {
	var resources RunResources
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var contextID, configID, raw string
		if err := tx.QueryRowContext(ctx, `SELECT context_id,config_id FROM sessions WHERE id=? AND kind='normal'`, session).Scan(&contextID, &configID); err != nil {
			return err
		}
		root, err := readConfigRevision(ctx, tx, configID)
		if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT policy FROM contexts WHERE id=?`, contextID).Scan(&raw); err != nil {
			return err
		}
		var policy ContextPolicy
		if err := json.Unmarshal([]byte(raw), &policy); err != nil {
			return err
		}
		resources, err = captureRunResources(ctx, tx, root, contextID, policy, source, snapshot, role)
		return err
	})
	return resources, err
}
