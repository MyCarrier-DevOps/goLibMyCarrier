package slippy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStepClaimantMigration_V8 pins what v8 executes: one additive nullable column on the
// component-state rows, no default and no backfill, and a down that removes it.
func TestStepClaimantMigration_V8(t *testing.T) {
	cfg := pgTestPipelineConfig(t)
	mgr := NewPostgresDynamicMigrationManager(cfg, nil)
	migs := mgr.GenerateMigrations()
	require.Len(t, migs, 8, "v8 adds the step claimant on top of v1-v7")
	v8 := migs[7]

	assert.Equal(t, 8, v8.Version)
	assert.Equal(t, "step_claimed_by", v8.Name)
	assert.Equal(t, 8, mgr.LatestVersion())
	assert.Equal(t, 8, GetPostgresDynamicMigrationVersion(cfg))

	up := stripSQLLineComments(v8.UpSQL)
	assert.Contains(t, up, "ALTER TABLE slip_component_states")
	assert.Contains(t, up, "ADD COLUMN IF NOT EXISTS claimed_by text")
	assert.NotContains(t, up, "NOT NULL", "a row written before v8 has no claimant, so the column is nullable")
	assert.NotContains(t, up, "DEFAULT", "no default and no backfill: an old running step stays unclaimed")

	down := stripSQLLineComments(v8.DownSQL)
	assert.Contains(t, down, "DROP COLUMN IF EXISTS claimed_by")
}
