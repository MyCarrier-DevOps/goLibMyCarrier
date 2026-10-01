package slippy

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/postgresmigrator"
)

func TestSlipStatus_String(t *testing.T) {
	tests := []struct {
		status   SlipStatus
		expected string
	}{
		{SlipStatusPending, "pending"},
		{SlipStatusInProgress, "in_progress"},
		{SlipStatusCompleted, "completed"},
		{SlipStatusFailed, "failed"},
		{SlipStatusCompensating, "compensating"},
		{SlipStatusCompensated, "compensated"},
		{SlipStatusAbandoned, "abandoned"},
		{SlipStatusPromoted, "promoted"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.status.String(); got != tt.expected {
				t.Errorf("SlipStatus.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestSlipStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		status   SlipStatus
		expected bool
	}{
		{SlipStatusPending, false},
		{SlipStatusInProgress, false},
		{SlipStatusCompleted, true},
		{SlipStatusFailed, false},
		{SlipStatusCompensating, false},
		{SlipStatusCompensated, true},
		{SlipStatusAbandoned, true},
		{SlipStatusPromoted, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsTerminal(); got != tt.expected {
				t.Errorf("SlipStatus(%q).IsTerminal() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestSlipStatus_IsLive(t *testing.T) {
	// IsLive is the repave decision predicate shared by CreateSlipForPush's main path
	// and handleDuplicateSlipBackstop (DEVOPS-231, review finding B5): true for every
	// status that is neither terminal nor SlipStatusFailed. Covers all eight SlipStatus
	// values so a future ninth status must be deliberately classified here too.
	tests := []struct {
		status   SlipStatus
		expected bool
	}{
		{SlipStatusPending, true},
		{SlipStatusInProgress, true},
		{SlipStatusCompleted, false},
		{SlipStatusFailed, false},
		{SlipStatusCompensating, true},
		{SlipStatusCompensated, false},
		{SlipStatusAbandoned, false},
		{SlipStatusPromoted, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsLive(); got != tt.expected {
				t.Errorf("SlipStatus(%q).IsLive() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestStepStatus_String(t *testing.T) {
	tests := []struct {
		status   StepStatus
		expected string
	}{
		{StepStatusPending, "pending"},
		{StepStatusHeld, "held"},
		{StepStatusRunning, "running"},
		{StepStatusCompleted, "completed"},
		{StepStatusFailed, "failed"},
		{StepStatusError, "error"},
		{StepStatusAborted, "aborted"},
		{StepStatusTimeout, "timeout"},
		{StepStatusSkipped, "skipped"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.status.String(); got != tt.expected {
				t.Errorf("StepStatus.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestStepStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		status   StepStatus
		expected bool
	}{
		{StepStatusPending, false},
		{StepStatusHeld, false},
		{StepStatusRunning, false},
		{StepStatusCompleted, true},
		{StepStatusFailed, true},
		{StepStatusError, true},
		{StepStatusAborted, true},
		{StepStatusTimeout, true},
		{StepStatusSkipped, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsTerminal(); got != tt.expected {
				t.Errorf("StepStatus(%q).IsTerminal() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestStepStatus_IsSuccess(t *testing.T) {
	tests := []struct {
		status   StepStatus
		expected bool
	}{
		{StepStatusPending, false},
		{StepStatusHeld, false},
		{StepStatusRunning, false},
		{StepStatusCompleted, true},
		{StepStatusFailed, false},
		{StepStatusError, false},
		{StepStatusAborted, false},
		{StepStatusTimeout, false},
		{StepStatusSkipped, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsSuccess(); got != tt.expected {
				t.Errorf("StepStatus(%q).IsSuccess() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestStepStatus_IsFailure(t *testing.T) {
	tests := []struct {
		status   StepStatus
		expected bool
	}{
		{StepStatusPending, false},
		{StepStatusHeld, false},
		{StepStatusRunning, false},
		{StepStatusCompleted, false},
		{StepStatusFailed, true},
		{StepStatusError, true},
		{StepStatusAborted, true},
		{StepStatusTimeout, true},
		{StepStatusSkipped, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsFailure(); got != tt.expected {
				t.Errorf("StepStatus(%q).IsFailure() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestStepStatus_IsRunning(t *testing.T) {
	tests := []struct {
		status   StepStatus
		expected bool
	}{
		{StepStatusPending, false},
		{StepStatusHeld, true},
		{StepStatusRunning, true},
		{StepStatusCompleted, false},
		{StepStatusFailed, false},
		{StepStatusError, false},
		{StepStatusAborted, false},
		{StepStatusTimeout, false},
		{StepStatusSkipped, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsRunning(); got != tt.expected {
				t.Errorf("StepStatus(%q).IsRunning() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestStepStatus_IsPending(t *testing.T) {
	tests := []struct {
		status   StepStatus
		expected bool
	}{
		{StepStatusPending, true},
		{StepStatusHeld, false},
		{StepStatusRunning, false},
		{StepStatusCompleted, false},
		{StepStatusFailed, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsPending(); got != tt.expected {
				t.Errorf("StepStatus(%q).IsPending() = %v, want %v", tt.status, got, tt.expected)
			}
		})
	}
}

func TestPrereqStatus_String(t *testing.T) {
	tests := []struct {
		status   PrereqStatus
		expected string
	}{
		{PrereqStatusCompleted, "completed"},
		{PrereqStatusRunning, "running"},
		{PrereqStatusFailed, "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.status.String(); got != tt.expected {
				t.Errorf("PrereqStatus.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// releasedCoreMigrationDigests pins the SHA-256 of the UpSQL of every core migration a slippy
// release has shipped: v1-v6, all in slippy/v1.4.4. postgresmigrator applies only the versions
// above the one a database has recorded, and records no checksum of what it applied
// (postgresmigrator/migrator.go: migrateUp, createSchemaVersionTable), so an edit to a released
// migration never reaches an existing database. The merge that adds a core migration tags a
// release, so add its digest in the PR that adds it.
var releasedCoreMigrationDigests = map[int]string{
	1: "ea682143a72f8e0d50f913a2a41981ff3ced61a4ea9015c68fde68911397ceb8",
	2: "3e75311460b0c5f48a1cd0759898da5a5aad679308d82cdcac32eda44131fccc",
	3: "2d289713ceaf60ff2e24d42b7406ffcc99b6c1ad84031e8cf7b5b74503711282",
	4: "31b244dee1d9db9ae85717733802c39adbd082695d8fdf80323592489409809a",
	5: "2dd7c4b1377dad00a0036efe93e4bc483450a87fcfabcf73fcbe76a353ea18c5",
	6: "e7e57697d520e5b397d1bd235c01e4a0532279cb14fb8fd4c15ff9304c179008",
}

// TestReleasedCoreMigrationsAreImmutable fails when a released core migration's UpSQL changes or
// the migration disappears, and when a core migration has no pinned digest. Appending a status to
// v1's CHECK list is the edit it exists to stop: the DOMAIN tests below would pass it, yet no
// existing database runs v1 again, so every one of them would reject the new status with SQLSTATE
// 23514.
func TestReleasedCoreMigrationsAreImmutable(t *testing.T) {
	migrations := NewPostgresDynamicMigrationManager(testPipelineConfig(), nil).GenerateMigrations()
	for _, problem := range releasedMigrationDrift(migrations, releasedCoreMigrationDigests) {
		t.Error(problem)
	}
}

// TestReleasedMigrationDrift pins releasedMigrationDrift itself. It computes its digests from the
// real migrations instead of reading releasedCoreMigrationDigests, and derives next and dropped from
// them, so a real migration that is unpinned, edited or removed fails only
// TestReleasedCoreMigrationsAreImmutable here (the v1-edit case still needs v1 itself).
func TestReleasedMigrationDrift(t *testing.T) {
	released := NewPostgresDynamicMigrationManager(testPipelineConfig(), nil).GenerateMigrations()
	golden := make(map[int]string, len(released))
	for _, mig := range released {
		golden[mig.Version] = upSQLDigest(mig)
	}
	next := slices.Max(slices.Collect(maps.Keys(golden))) + 1
	editedV1 := slices.Clone(released)
	editedV1[0].UpSQL = strings.Replace(editedV1[0].UpSQL, "'promoted'", "'promoted','queued'", 1)
	withNext := append(slices.Clone(released), postgresmigrator.Migration{Version: next, UpSQL: "SELECT 1"})
	dropped := released[len(released)/2].Version
	withoutOne := slices.DeleteFunc(slices.Clone(released), func(m postgresmigrator.Migration) bool {
		return m.Version == dropped
	})

	cases := []struct {
		name       string
		migrations []postgresmigrator.Migration
		want       []string // one substring per expected problem, in version order
	}{
		{"the released migrations as they are", released, nil},
		{"an unpinned migration is reported", withNext,
			[]string{fmt.Sprintf("migration v%d has no pinned digest", next)}},
		{"a status appended to v1's CHECK", editedV1, []string{
			"migration v1 is released and immutable, so revert the edit; to change the schema, add a new version"}},
		{"a pinned migration removed", withoutOne, []string{fmt.Sprintf(
			"migration v%d is released and immutable, but GenerateMigrations no longer returns it", dropped)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems := releasedMigrationDrift(tc.migrations, golden)
			if len(problems) != len(tc.want) {
				t.Fatalf("got %d problems %q, want %d", len(problems), problems, len(tc.want))
			}
			for i, want := range tc.want {
				if !strings.Contains(problems[i], want) {
					t.Errorf("problem %d = %q, want it to contain %q", i, problems[i], want)
				}
			}
		})
	}
}

// releasedMigrationDrift returns one message for each pinned version (a key of golden) whose
// migration is missing from migrations or whose UpSQL no longer has the golden SHA-256, then one
// for each migration golden does not pin, each group in version order. The merge that adds a core
// migration releases it, so the PR that adds it must pin it.
func releasedMigrationDrift(migrations []postgresmigrator.Migration, golden map[int]string) []string {
	byVersion := make(map[int]postgresmigrator.Migration, len(migrations))
	for _, mig := range migrations {
		byVersion[mig.Version] = mig
	}
	var problems []string
	for _, version := range slices.Sorted(maps.Keys(golden)) {
		mig, ok := byVersion[version]
		if !ok {
			problems = append(problems, fmt.Sprintf(
				"migration v%d is released and immutable, but GenerateMigrations no longer returns it: restore it",
				version))
			continue
		}
		if got := upSQLDigest(mig); got != golden[version] {
			problems = append(problems, fmt.Sprintf(
				"migration v%d is released and immutable, so revert the edit; to change the schema, add a new "+
					"version (for a status change, one that ALTERs the DOMAIN): its UpSQL sha256 is %s, want %s",
				version, got, golden[version]))
		}
	}
	for _, version := range slices.Sorted(maps.Keys(byVersion)) {
		if _, pinned := golden[version]; !pinned {
			problems = append(problems, fmt.Sprintf(
				"migration v%d has no pinned digest; the merge that adds it releases it, so add this line to "+
					"releasedCoreMigrationDigests: %d: %q,", version, version, upSQLDigest(byVersion[version])))
		}
	}
	return problems
}

// upSQLDigest is the hex SHA-256 of a migration's UpSQL: the value releasedCoreMigrationDigests pins.
func upSQLDigest(mig postgresmigrator.Migration) string {
	sum := sha256.Sum256([]byte(mig.UpSQL))
	return hex.EncodeToString(sum[:])
}

// TestAllSlipStatusesInEnum verifies that the SlipStatus constants in status.go and the values
// the slip_status DOMAIN admits once every core migration has run are the same set, so neither
// can gain or lose a status without the other. It discovers both sides rather than listing them,
// so adding a status to only one side fails here.
func TestAllSlipStatusesInEnum(t *testing.T) {
	assertStatusConstantsMatchDomain(t, "SlipStatus", "slip_status")
}

// TestAllStepStatusesInEnum is TestAllSlipStatusesInEnum for StepStatus and the step_status DOMAIN.
func TestAllStepStatusesInEnum(t *testing.T) {
	assertStatusConstantsMatchDomain(t, "StepStatus", "step_status")
}

// assertStatusConstantsMatchDomain compares the typeName constants declared in status.go with
// the values the named DOMAIN admits after the latest core migration (domainCheckValues).
func assertStatusConstantsMatchDomain(t *testing.T, typeName, domain string) {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	discovered := parseStatusConstants(t, filepath.Join(filepath.Dir(filename), "status.go"), typeName)
	if len(discovered) == 0 {
		t.Fatalf("no %s constants found in status.go - parsing may have failed", typeName)
	}
	migrations := NewPostgresDynamicMigrationManager(testPipelineConfig(), nil).GenerateMigrations()
	domainValues, err := domainCheckValues(migrations, domain)
	if err != nil {
		t.Fatal(err)
	}

	constByValue := make(map[string]string, len(discovered))
	for name, value := range discovered {
		constByValue[value] = name
	}
	for value, name := range constByValue {
		if _, ok := domainValues[value]; !ok {
			t.Errorf("%[1]s constant %[2]s = %[3]q is not admitted by the %[4]s DOMAIN after the latest "+
				"migration in postgres_migrations.go. Released migrations are immutable, so add a new migration "+
				"that redefines the DOMAIN's CHECK: ALTER DOMAIN %[4]s DROP CONSTRAINT <name> (Postgres named "+
				"v1's unnamed CHECK %[4]s_check), then ALTER DOMAIN %[4]s ADD CONSTRAINT <name> CHECK "+
				"(VALUE IN (...))",
				typeName, name, value, domain)
		}
	}
	for value := range domainValues {
		if _, ok := constByValue[value]; !ok {
			t.Errorf("the %s DOMAIN admits %q but status.go defines no %s with that value", domain, value, typeName)
		}
	}
}

// TestDomainCheckValues pins how domainCheckValues follows a DOMAIN's CHECK across migrations,
// including the redefinitions a later migration makes: the released migrations only ever take
// its CREATE DOMAIN path.
func TestDomainCheckValues(t *testing.T) {
	const create = `DO $$ BEGIN
		BEGIN CREATE DOMAIN slip_status AS text CHECK (VALUE IN ('pending','failed'));
		EXCEPTION WHEN duplicate_object THEN NULL; END;
		BEGIN CREATE DOMAIN step_status AS text CHECK (VALUE IN ('pending','running'));
		EXCEPTION WHEN duplicate_object THEN NULL; END;
	END $$;`
	const redefine = `ALTER DOMAIN slip_status DROP CONSTRAINT slip_status_check;
		ALTER DOMAIN slip_status ADD CONSTRAINT slip_status_check CHECK (VALUE IN ('pending','failed','queued'));`
	migs := func(upSQL ...string) []postgresmigrator.Migration {
		out := make([]postgresmigrator.Migration, 0, len(upSQL))
		for i, sql := range upSQL {
			out = append(out, postgresmigrator.Migration{Version: i + 1, UpSQL: sql})
		}
		return out
	}

	cases := []struct {
		name       string
		migrations []postgresmigrator.Migration
		want       []string // the admitted values, sorted
		wantErr    string
	}{
		{name: "the CREATE alone", migrations: migs(create), want: []string{"failed", "pending"}},
		{name: "a later DROP then ADD redefines the CHECK", migrations: migs(create, redefine),
			want: []string{"failed", "pending", "queued"}},
		{
			// Postgres keeps slip_status_check, which rejects 'queued', so the new value is not admitted.
			name: "an ADD without a DROP keeps the old CHECK too",
			migrations: migs(create, `ALTER DOMAIN slip_status
				ADD CONSTRAINT slip_status_v2 CHECK (VALUE IN ('pending','failed','queued'));`),
			want: []string{"failed", "pending"},
		},
		{name: "lower case and a schema qualifier are read", migrations: migs(create,
			`alter domain public.slip_status drop constraint slip_status_check;
			alter domain public.slip_status add constraint slip_status_v2 check (value in ('pending','queued'));`),
			want: []string{"pending", "queued"}},
		{name: "migrations are read in version order", migrations: []postgresmigrator.Migration{
			{Version: 7, UpSQL: redefine}, {Version: 1, UpSQL: create},
		}, want: []string{"failed", "pending", "queued"}},
		{name: "another DOMAIN's statements are ignored", migrations: migs(create,
			`ALTER DOMAIN step_status DROP CONSTRAINT step_status_check;`), want: []string{"failed", "pending"}},
		{name: "DROP CONSTRAINT IF EXISTS of a name the DOMAIN lacks does nothing", migrations: migs(create,
			`ALTER DOMAIN slip_status DROP CONSTRAINT IF EXISTS slip_status_v2;`), want: []string{"failed", "pending"}},
		{name: "a line-commented DROP is not a DROP", migrations: migs(create,
			`-- ALTER DOMAIN slip_status DROP CONSTRAINT slip_status_check;
			ALTER DOMAIN slip_status ADD CONSTRAINT slip_status_v2 CHECK (VALUE IN ('pending','failed','queued'));`),
			want: []string{"failed", "pending"}},
		{name: "a block-commented DROP is not a DROP", migrations: migs(create,
			`/* ALTER DOMAIN slip_status DROP CONSTRAINT slip_status_check; */
			ALTER DOMAIN slip_status ADD CONSTRAINT slip_status_v2 CHECK (VALUE IN ('pending','failed','queued'));`),
			want: []string{"failed", "pending"}},
		{name: "a comment that names the DROP does not run it", migrations: migs(create,
			"-- To redefine the CHECK, ALTER DOMAIN slip_status DROP CONSTRAINT slip_status_check, then ADD it.\n"+
				redefine),
			want: []string{"failed", "pending", "queued"}},
		{name: "an unnamed ADD is refused", migrations: migs(create,
			`ALTER DOMAIN slip_status ADD CHECK (VALUE IN ('pending'));`),
			wantErr: "migration v2: ALTER DOMAIN slip_status: this test reads only ADD CONSTRAINT <name> CHECK"},
		{name: "a quoted DOMAIN name is refused", migrations: migs(create,
			`ALTER DOMAIN "slip_status" DROP CONSTRAINT slip_status_check;
			ALTER DOMAIN "slip_status"
				ADD CONSTRAINT slip_status_check CHECK (VALUE IN ('pending','failed','queued'));`),
			wantErr: "migration v2: a quoted DOMAIN name is not supported here; write it unquoted"},
		{name: "a schema-qualified quoted DOMAIN name is refused", migrations: migs(create,
			`ALTER DOMAIN public."slip_status" DROP CONSTRAINT slip_status_check;
			ALTER DOMAIN public."slip_status" ADD CONSTRAINT slip_status_values CHECK (VALUE IN ('pending'));`),
			wantErr: "migration v2: a quoted DOMAIN name is not supported here; write it unquoted"},
		{name: "a nested block comment is refused", migrations: migs(create,
			`/* a /* b */ ALTER DOMAIN slip_status DROP CONSTRAINT slip_status_check; */
			ALTER DOMAIN slip_status ADD CONSTRAINT slip_status_v2 CHECK (VALUE IN ('pending','failed','queued'));`),
			wantErr: "migration v2: a nested or unbalanced block comment is not supported here"},
		{name: "a nested block comment whose last */ follows -- is refused", migrations: migs(create,
			`/* a /* b */ ALTER DOMAIN slip_status DROP CONSTRAINT slip_status_check; -- */
			ALTER DOMAIN slip_status ADD CONSTRAINT slip_status_v2 CHECK (VALUE IN ('pending','failed','queued'));`),
			wantErr: "migration v2: a nested or unbalanced block comment is not supported here"},
		{name: "a comment opener after a string literal's -- is refused", migrations: migs(create,
			`SELECT '--'; /* retired:
			ALTER DOMAIN slip_status DROP CONSTRAINT slip_status_check; */
			ALTER DOMAIN slip_status ADD CONSTRAINT slip_status_v2 CHECK (VALUE IN ('pending','failed','queued'));`),
			wantErr: "migration v2: a nested or unbalanced block comment is not supported here"},
		{name: "a DROP of a constraint the DOMAIN lacks is refused", migrations: migs(create,
			`ALTER DOMAIN slip_status DROP CONSTRAINT slip_status_v2;`),
			wantErr: "drops constraint slip_status_v2, which it does not have"},
		{name: "a second CREATE DOMAIN is refused", migrations: migs(create, create),
			wantErr: "migration v2: CREATE DOMAIN slip_status runs again"},
		{name: "dropping every CHECK is refused", migrations: migs(create,
			`ALTER DOMAIN slip_status DROP CONSTRAINT slip_status_check;`),
			wantErr: "the slip_status DOMAIN has no CHECK (VALUE IN (...)) left"},
		{name: "no CREATE is refused", migrations: migs(`SELECT 1`),
			wantErr: "no migration creates the slip_status DOMAIN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := domainCheckValues(tc.migrations, "slip_status")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if values := slices.Sorted(maps.Keys(got)); !slices.Equal(values, tc.want) {
				t.Errorf("admitted values = %q, want %q", values, tc.want)
			}
		})
	}
}

// domainStatementRE finds each CREATE DOMAIN and ALTER DOMAIN statement: group 1 is the verb,
// group 2 the DOMAIN's name without its schema qualifier.
var domainStatementRE = regexp.MustCompile(`(?i)\b(CREATE|ALTER)\s+DOMAIN\s+(?:\w+\.)?(\w+)`)

// sqlCommentRE matches a -- comment or a /* */ comment, without regard to string literals or to
// nesting (Postgres nests /* */ comments). domainCheckValues replaces each match with a space
// before it reads a migration, so SQL quoted in a comment is not taken for a statement.
var sqlCommentRE = regexp.MustCompile(`(?s)/\*.*?\*/|--[^\n]*`)

// quotedDomainNameRE matches a CREATE DOMAIN or ALTER DOMAIN whose name begins with a quoted
// identifier, directly or after an unquoted schema qualifier (public."slip_status").
// domainStatementRE cannot read that, so domainCheckValues refuses it rather than skip it.
var quotedDomainNameRE = regexp.MustCompile(`(?i)\b(CREATE|ALTER)\s+DOMAIN\s+(?:\w+\.)?"`)

// The statement forms domainCheckValues reads, each anchored at the end of a domainStatementRE
// match. A CHECK is always VALUE IN (a list of quoted values).
var (
	domainCreateCheckRE = regexp.MustCompile(
		`(?is)^\s+AS\s+text\s+(?:CONSTRAINT\s+(\w+)\s+)?CHECK\s*\(\s*VALUE\s+IN\s*\(([^)]*)\)\s*\)`)
	domainAddCheckRE = regexp.MustCompile(
		`(?is)^\s+ADD\s+CONSTRAINT\s+(\w+)\s+CHECK\s*\(\s*VALUE\s+IN\s*\(([^)]*)\)\s*\)`)
	domainDropConstraintRE = regexp.MustCompile(`(?is)^\s+DROP\s+CONSTRAINT\s+(IF\s+EXISTS\s+)?(\w+)`)
)

// domainCheckValues returns the values the named text DOMAIN admits once migrations have run in
// version order. A value must pass every CHECK the DOMAIN still has: the one its CREATE DOMAIN
// defines (Postgres names an unnamed one <domain>_check), plus each later ALTER DOMAIN ... ADD
// CONSTRAINT <name> CHECK (VALUE IN (...)), less each ALTER DOMAIN ... DROP CONSTRAINT <name>.
// Postgres has no ALTER DOMAIN that replaces a CHECK in place, and an ADD without a DROP keeps the
// old CHECK, which still rejects every value it does not list. Any other statement on the DOMAIN
// is an error, so a migration this cannot read fails the test instead of being skipped.
// Comments are ignored, but a nested block comment, or a */ the strip leaves behind, fails the
// test, and text inside a string literal (for example a RAISE message) is still read.
func domainCheckValues(migrations []postgresmigrator.Migration, domain string) (map[string]struct{}, error) {
	ordered := slices.Clone(migrations)
	slices.SortFunc(ordered, func(a, b postgresmigrator.Migration) int { return cmp.Compare(a.Version, b.Version) })

	var checks map[string]map[string]struct{} // CHECK name -> the values it admits; nil until the CREATE
	for _, mig := range ordered {
		nested := false
		upSQL := sqlCommentRE.ReplaceAllStringFunc(mig.UpSQL, func(comment string) string {
			if strings.HasPrefix(comment, "/*") && strings.Contains(comment[2:], "/*") {
				nested = true
			}
			return " "
		})
		if nested || strings.Contains(upSQL, "*/") {
			return nil, fmt.Errorf("migration v%d: a nested or unbalanced block comment is not supported here",
				mig.Version)
		}
		if quotedDomainNameRE.MatchString(upSQL) {
			return nil, fmt.Errorf("migration v%d: a quoted DOMAIN name is not supported here; write it unquoted",
				mig.Version)
		}
		for _, loc := range domainStatementRE.FindAllStringSubmatchIndex(upSQL, -1) {
			if !strings.EqualFold(upSQL[loc[4]:loc[5]], domain) {
				continue
			}
			rest := upSQL[loc[1]:]
			var err error
			if strings.EqualFold(upSQL[loc[2]:loc[3]], "CREATE") {
				checks, err = createDomainChecks(checks, rest, domain)
			} else {
				err = alterDomainChecks(checks, rest, domain)
			}
			if err != nil {
				return nil, fmt.Errorf("migration v%d: %w", mig.Version, err)
			}
		}
	}
	if checks == nil {
		return nil, fmt.Errorf("no migration creates the %s DOMAIN", domain)
	}
	if len(checks) == 0 {
		return nil, fmt.Errorf("after the latest migration the %s DOMAIN has no CHECK (VALUE IN (...)) left", domain)
	}
	var admitted map[string]struct{}
	for _, values := range checks {
		if admitted == nil {
			admitted = maps.Clone(values)
			continue
		}
		maps.DeleteFunc(admitted, func(value string, _ struct{}) bool {
			_, ok := values[value]
			return !ok
		})
	}
	return admitted, nil
}

// createDomainChecks returns the CHECK set a CREATE DOMAIN statement defines; rest is the text
// after the DOMAIN's name.
func createDomainChecks(checks map[string]map[string]struct{}, rest, domain string) (
	map[string]map[string]struct{}, error,
) {
	if checks != nil {
		// v1 swallows duplicate_object: on a database that has the DOMAIN, a CREATE changes nothing.
		return nil, fmt.Errorf("CREATE DOMAIN %s runs again, which changes nothing on a database that has it; "+
			"redefine its CHECK with ALTER DOMAIN", domain)
	}
	m := domainCreateCheckRE.FindStringSubmatch(rest)
	if m == nil {
		return nil, fmt.Errorf("CREATE DOMAIN %s is not AS text CHECK (VALUE IN (...))", domain)
	}
	name := m[1]
	if name == "" {
		name = domain + "_check" // the name Postgres gives a DOMAIN's unnamed CHECK
	}
	values, err := quotedValues(m[2], domain)
	if err != nil {
		return nil, err
	}
	return map[string]map[string]struct{}{strings.ToLower(name): values}, nil
}

// alterDomainChecks applies an ALTER DOMAIN statement to checks; rest is the text after the
// DOMAIN's name.
func alterDomainChecks(checks map[string]map[string]struct{}, rest, domain string) error {
	if checks == nil {
		return fmt.Errorf("ALTER DOMAIN %s runs before any migration creates it", domain)
	}
	if m := domainAddCheckRE.FindStringSubmatch(rest); m != nil {
		name := strings.ToLower(m[1])
		if _, dup := checks[name]; dup {
			return fmt.Errorf("ALTER DOMAIN %s adds constraint %s, which it already has", domain, name)
		}
		values, err := quotedValues(m[2], domain)
		if err != nil {
			return err
		}
		checks[name] = values
		return nil
	}
	if m := domainDropConstraintRE.FindStringSubmatch(rest); m != nil {
		name := strings.ToLower(m[2])
		if _, ok := checks[name]; !ok && m[1] == "" {
			return fmt.Errorf("ALTER DOMAIN %s drops constraint %s, which it does not have", domain, name)
		}
		delete(checks, name)
		return nil
	}
	statement := strings.TrimSpace(strings.SplitN(rest, ";", 2)[0])
	return fmt.Errorf("ALTER DOMAIN %s: this test reads only ADD CONSTRAINT <name> CHECK (VALUE IN (...)) "+
		"and DROP CONSTRAINT <name>, not %q", domain, statement)
}

// quotedValues returns the quoted values of a CHECK (VALUE IN (...)) list. A value listed twice,
// or no value at all, is an error.
func quotedValues(list, domain string) (map[string]struct{}, error) {
	values := make(map[string]struct{})
	for _, m := range regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(list, -1) {
		if _, dup := values[m[1]]; dup {
			return nil, fmt.Errorf("a CHECK on the %s DOMAIN lists %q twice", domain, m[1])
		}
		values[m[1]] = struct{}{}
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("a CHECK on the %s DOMAIN lists no values - parsing may have failed", domain)
	}
	return values, nil
}

// parseStatusConstants parses status.go and extracts all constants of the given type.
func parseStatusConstants(t *testing.T, filename, typeName string) map[string]string {
	t.Helper()

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse %s: %v", filename, err)
	}

	discovered := make(map[string]string)
	ast.Inspect(node, func(n ast.Node) bool {
		genDecl, ok := n.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			return true
		}

		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}

			if ident, ok := valueSpec.Type.(*ast.Ident); ok && ident.Name == typeName {
				for i, name := range valueSpec.Names {
					if len(valueSpec.Values) > i {
						if lit, ok := valueSpec.Values[i].(*ast.BasicLit); ok {
							// Remove quotes from string literal
							statusValue := lit.Value[1 : len(lit.Value)-1]
							discovered[name.Name] = statusValue
						}
					}
				}
			}
		}
		return true
	})

	return discovered
}

func TestSlipStatus_IsTerminal_UnknownStatus(t *testing.T) {
	// Test that unknown status values return false
	unknownStatus := SlipStatus("unknown")
	if unknownStatus.IsTerminal() {
		t.Error("unknown SlipStatus should not be terminal")
	}
}

func TestStepStatus_IsTerminal_UnknownStatus(t *testing.T) {
	// Test that unknown status values return false
	unknownStatus := StepStatus("unknown")
	if unknownStatus.IsTerminal() {
		t.Error("unknown StepStatus should not be terminal")
	}
}

func TestStepStatus_IsFailure_UnknownStatus(t *testing.T) {
	// Test that unknown status values return false
	unknownStatus := StepStatus("unknown")
	if unknownStatus.IsFailure() {
		t.Error("unknown StepStatus should not be considered a failure")
	}
}
