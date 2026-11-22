package quota

import (
	"context"
	"fmt"
	"maps"

	"github.com/agomez/pulsar/internal/store/postgres"
)

// PGStore keeps per-project limit overrides in the quotas table; resources
// without a row use the operator defaults.
type PGStore struct {
	db       *postgres.DB
	defaults Limits
}

func NewPGStore(db *postgres.DB, defaults Limits) *PGStore {
	return &PGStore{db: db, defaults: defaults}
}

// Limits returns the effective limits: defaults overlaid with project rows.
func (s *PGStore) Limits(ctx context.Context, projectID string) (Limits, error) {
	out := Limits{}
	maps.Copy(out, s.defaults)
	rows, err := s.db.Pool().Query(ctx,
		`SELECT resource, hard_limit FROM quotas WHERE project_id = $1`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r string
		var limit int
		if err := rows.Scan(&r, &limit); err != nil {
			return nil, err
		}
		if Valid(Resource(r)) {
			out[Resource(r)] = limit
		}
	}
	return out, rows.Err()
}

// Set upserts the given overrides. A nil value removes the override, so the
// resource falls back to the default.
func (s *PGStore) Set(ctx context.Context, projectID string, changes map[Resource]*int) error {
	for r, v := range changes {
		if !Valid(r) {
			return fmt.Errorf("unknown resource %q (valid: %s)", r, names())
		}
		if v != nil && *v < Unlimited {
			return fmt.Errorf("%s must be >= -1 (-1 = unlimited)", r)
		}
	}
	tx, err := s.db.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	for r, v := range changes {
		if v == nil {
			if _, err := tx.Exec(ctx,
				`DELETE FROM quotas WHERE project_id = $1 AND resource = $2`, projectID, string(r)); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO quotas (project_id, resource, hard_limit) VALUES ($1, $2, $3)
			 ON CONFLICT (project_id, resource) DO UPDATE SET hard_limit = EXCLUDED.hard_limit`,
			projectID, string(r), *v); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
