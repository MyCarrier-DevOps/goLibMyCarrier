package slippy

import (
	"context"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/MyCarrier-DevOps/goLibMyCarrier/clickhouse/clickhousetest"
)

// This file covers the CLONE_DERIVED SQL-builder logic (buildCloneStepColumnDerive) added for
// bd mycarrier-5dv5 (stale-clone step-column derive fix). It complements the existing
// insertAtomicStatusUpdate / insertAtomicHistoryUpdate coverage in clickhouse_store_unit_test.go,
// which continues to exercise the override (tier 1) and full end-to-end retry/self-heal paths.
//
// testPipelineConfig() (defined in clickhouse_store_unit_test.go) has 4 steps:
//   push_parsed (pure), builds (aggregate: "build"), unit_tests (aggregate: "unit_test"),
//   dev_deploy (pure).

// TestBuildCloneStepColumnDerive_PureStepGetsDeriveExpression verifies that, with no
// overrides, pure pipeline step columns get a coalesce(...) derive expression referencing the
// "derived" CTE with a fallback to the verbatim column, while aggregate step columns are
// copied through unchanged.
func TestBuildCloneStepColumnDerive_PureStepGetsDeriveExpression(t *testing.T) {
	cfg := testPipelineConfig()
	qb := NewSlipQueryBuilder(cfg, "ci")
	stepColumns := qb.BuildStepColumns() // [push_parsed_status, builds_status, unit_tests_status, dev_deploy_status]

	derive := buildCloneStepColumnDerive(cfg, "ci", stepColumns, nil, qb.StepStatusColumn)

	if len(derive.exprs) != len(stepColumns) {
		t.Fatalf("expected %d exprs (one per stepColumn), got %d", len(stepColumns), len(derive.exprs))
	}

	// push_parsed (pure, index 0): must be a coalesce derive expression referencing the CTE,
	// the step name as a literal, and the verbatim column as the tier-3 fallback.
	pushParsedExpr := derive.exprs[0]
	if !strings.Contains(pushParsedExpr, "coalesce(") {
		t.Errorf(
			"expected push_parsed_status expr to contain a coalesce(...) derive expression, got: %s",
			pushParsedExpr,
		)
	}
	if !strings.Contains(pushParsedExpr, "'push_parsed'") {
		t.Errorf("expected push_parsed_status expr to reference step literal 'push_parsed', got: %s", pushParsedExpr)
	}
	if !strings.Contains(pushParsedExpr, "push_parsed_status") {
		t.Errorf("expected push_parsed_status expr to retain the verbatim column as fallback, got: %s", pushParsedExpr)
	}
	if !strings.Contains(pushParsedExpr, "derived") {
		t.Errorf("expected push_parsed_status expr to reference the derived CTE, got: %s", pushParsedExpr)
	}

	// builds (aggregate, index 1): must be copied through verbatim — no coalesce, no CTE ref.
	buildsExpr := derive.exprs[1]
	if buildsExpr != "builds_status" {
		t.Errorf("expected aggregate column builds_status to be verbatim, got: %s", buildsExpr)
	}

	// unit_tests (aggregate, index 2): same as above.
	unitTestsExpr := derive.exprs[2]
	if unitTestsExpr != "unit_tests_status" {
		t.Errorf("expected aggregate column unit_tests_status to be verbatim, got: %s", unitTestsExpr)
	}

	// dev_deploy (pure, index 3): derived like push_parsed.
	devDeployExpr := derive.exprs[3]
	if !strings.Contains(devDeployExpr, "coalesce(") || !strings.Contains(devDeployExpr, "'dev_deploy'") {
		t.Errorf(
			"expected dev_deploy_status expr to be a coalesce derive referencing 'dev_deploy', got: %s",
			devDeployExpr,
		)
	}

	// The CTE (withClause) must be present since at least one pure step needs deriving, and
	// must mirror derivePipelineStepStatuses' filter semantics: correlation_id = ?,
	// component = '', GROUP BY step, and the same argMax sort key. No HAVING clause: unlike
	// derivePipelineStepStatuses' Go-side "rawStatus == ''" guard, an Enum8 st != '' HAVING
	// filter is invalid SQL for this column (slip_component_states.status is Enum8 with no ''
	// member) and fails every query with ClickHouse error 691 at query-analysis time — see
	// buildCloneStepColumnDerive's doc comment. GROUP BY already guarantees st is never '' for
	// an Enum8 column, so no HAVING equivalent is needed.
	if derive.withClause == "" {
		t.Fatal("expected non-empty withClause when pure steps need deriving")
	}
	for _, want := range []string{
		"WITH derived AS", "argMax(status,", componentEventSortKeyNoImageTag,
		"correlation_id = ?", "component = ''", "GROUP BY step",
		TableSlipComponentStates,
	} {
		if !strings.Contains(derive.withClause, want) {
			t.Errorf("expected withClause to contain %q, got: %s", want, derive.withClause)
		}
	}
	if strings.Contains(derive.withClause, "HAVING") {
		t.Errorf("expected no HAVING clause (invalid for Enum8 status column), got: %s", derive.withClause)
	}
	if len(derive.overrideArgs) != 0 {
		t.Errorf("expected 0 overrideArgs with no overrides, got %d: %v", len(derive.overrideArgs), derive.overrideArgs)
	}
}

// TestBuildCloneStepColumnDerive_OverridePrecedenceOverDerive verifies tier-1 precedence:
// a stepStatusOverride on a pure step column replaces the derive expression with a literal "?"
// and the value is recorded in overrideArgs, regardless of the step's aggregate classification.
func TestBuildCloneStepColumnDerive_OverridePrecedenceOverDerive(t *testing.T) {
	cfg := testPipelineConfig()
	qb := NewSlipQueryBuilder(cfg, "ci")
	stepColumns := qb.BuildStepColumns()

	t.Run("override on pure step wins over derive", func(t *testing.T) {
		overrides := []stepStatusOverride{{columnName: "push_parsed_status", status: StepStatusCompleted}}
		derive := buildCloneStepColumnDerive(cfg, "ci", stepColumns, overrides, qb.StepStatusColumn)

		if derive.exprs[0] != "?" {
			t.Errorf("expected overridden column expr to be literal '?', got: %s", derive.exprs[0])
		}
		if len(derive.overrideArgs) != 1 || derive.overrideArgs[0] != string(StepStatusCompleted) {
			t.Errorf("expected overrideArgs=[%q], got %v", string(StepStatusCompleted), derive.overrideArgs)
		}
		// dev_deploy (pure, no override) must still be derived, so the CTE is still present.
		if derive.withClause == "" {
			t.Error("expected withClause still present due to dev_deploy needing derive")
		}
	})

	t.Run("override on aggregate step also wins (tier 1 applies to any column)", func(t *testing.T) {
		overrides := []stepStatusOverride{{columnName: "builds_status", status: StepStatusFailed}}
		derive := buildCloneStepColumnDerive(cfg, "ci", stepColumns, overrides, qb.StepStatusColumn)

		if derive.exprs[1] != "?" {
			t.Errorf("expected overridden aggregate column expr to be literal '?', got: %s", derive.exprs[1])
		}
		if len(derive.overrideArgs) != 1 || derive.overrideArgs[0] != string(StepStatusFailed) {
			t.Errorf("expected overrideArgs=[%q], got %v", string(StepStatusFailed), derive.overrideArgs)
		}
	})

	t.Run("overriding every pure step disables the CTE (fast path)", func(t *testing.T) {
		overrides := []stepStatusOverride{
			{columnName: "push_parsed_status", status: StepStatusCompleted},
			{columnName: "dev_deploy_status", status: StepStatusAborted},
		}
		derive := buildCloneStepColumnDerive(cfg, "ci", stepColumns, overrides, qb.StepStatusColumn)

		if derive.withClause != "" {
			t.Errorf("expected empty withClause when every pure step is overridden, got: %s", derive.withClause)
		}
		// Aggregate columns remain verbatim.
		if derive.exprs[1] != "builds_status" || derive.exprs[2] != "unit_tests_status" {
			t.Errorf("expected aggregate columns verbatim, got exprs=%v", derive.exprs)
		}
	})
}

// TestBuildCloneStepColumnDerive_NilConfigFallsBackVerbatim verifies the defensive fallback:
// with a nil PipelineConfig (should not happen in practice — both callers already guard on
// s.pipelineConfig == nil before reaching this point) every column is copied verbatim and no
// CTE is emitted, rather than panicking or guessing step names.
func TestBuildCloneStepColumnDerive_NilConfigFallsBackVerbatim(t *testing.T) {
	stepColumns := []string{"push_parsed_status", "dev_deploy_status"}
	qb := NewSlipQueryBuilder(nil, "ci")
	derive := buildCloneStepColumnDerive(nil, "ci", stepColumns, nil, qb.StepStatusColumn)

	if derive.withClause != "" {
		t.Errorf("expected empty withClause with nil config, got: %s", derive.withClause)
	}
	for i, col := range stepColumns {
		if derive.exprs[i] != col {
			t.Errorf("expected verbatim column %q at index %d, got: %s", col, i, derive.exprs[i])
		}
	}
}

// TestBuildCloneStepColumnDerive_NonIdentifierStepNameWithQuote_FallsBackVerbatim verifies that
// a step name containing a single quote — which can never legitimately occur, since it doesn't
// match safeStepNameForDerivePattern's ^[A-Za-z0-9_]+$ column-name-stem requirement — never
// reaches the coalesce(...) derive expression at all: it is refused by the non-identifier guard
// and copied verbatim, same as an aggregate column. sqlSingleQuoteEscape's own quoting behavior
// (backslash-then-quote escaping) is covered directly by TestSqlSingleQuoteEscape below.
func TestBuildCloneStepColumnDerive_NonIdentifierStepNameWithQuote_FallsBackVerbatim(t *testing.T) {
	cfg := &PipelineConfig{
		Steps: []StepConfig{{Name: "o'brien_check"}},
	}
	cfg.stepsByName = map[string]*StepConfig{"o'brien_check": &cfg.Steps[0]}

	qb := NewSlipQueryBuilder(cfg, "ci")
	stepColumns := []string{"o'brien_check_status"}
	derive := buildCloneStepColumnDerive(cfg, "ci", stepColumns, nil, qb.StepStatusColumn)

	if derive.exprs[0] != "o'brien_check_status" {
		t.Errorf("expected verbatim fallback for non-identifier step name, got: %s", derive.exprs[0])
	}
	if strings.Contains(derive.exprs[0], "coalesce(") {
		t.Errorf("expected no derive expression for non-identifier step name, got: %s", derive.exprs[0])
	}
	if derive.withClause != "" {
		t.Errorf("expected no CTE when the only pure step is refused by the non-identifier guard, got: %s",
			derive.withClause)
	}
	if len(derive.guardFallbacks) != 1 {
		t.Fatalf(
			"expected exactly 1 guardFallbacks entry, got %d: %+v",
			len(derive.guardFallbacks),
			derive.guardFallbacks,
		)
	}
	if derive.guardFallbacks[0].column != "o'brien_check_status" {
		t.Errorf("expected guardFallbacks column %q, got %q", "o'brien_check_status", derive.guardFallbacks[0].column)
	}
	if derive.guardFallbacks[0].reason != cloneDeriveFallbackReasonNonIdentifierGuard {
		t.Errorf("expected guardFallbacks reason %q, got %q",
			cloneDeriveFallbackReasonNonIdentifierGuard, derive.guardFallbacks[0].reason)
	}
}

// TestBuildCloneStepColumnDerive_NonIdentifierStepName_FallsBackVerbatim verifies the
// non-identifier guard in isolation (no quote/escaping involved): a pure step whose name
// contains a character outside [A-Za-z0-9_] (a hyphen) is refused a derive expression and
// falls back to a verbatim column copy — no derive expr, and it contributes no CTE reference —
// while a sibling pure step with a valid identifier name is unaffected and still derives
// (confirming the guard is column-scoped, not all-or-nothing).
func TestBuildCloneStepColumnDerive_NonIdentifierStepName_FallsBackVerbatim(t *testing.T) {
	cfg := &PipelineConfig{
		Steps: []StepConfig{
			{Name: "good_step"},
			{Name: "bad-step"},
		},
	}
	cfg.stepsByName = map[string]*StepConfig{
		"good_step": &cfg.Steps[0],
		"bad-step":  &cfg.Steps[1],
	}

	qb := NewSlipQueryBuilder(cfg, "ci")
	stepColumns := []string{"good_step_status", "bad-step_status"}
	derive := buildCloneStepColumnDerive(cfg, "ci", stepColumns, nil, qb.StepStatusColumn)

	goodExpr := derive.exprs[0]
	if !strings.Contains(goodExpr, "coalesce(") || !strings.Contains(goodExpr, "'good_step'") {
		t.Errorf("expected good_step to still get a coalesce derive expression, got: %s", goodExpr)
	}

	badExpr := derive.exprs[1]
	if badExpr != "bad-step_status" {
		t.Errorf("expected bad-step (non-identifier) to fall back verbatim, got: %s", badExpr)
	}
	if strings.Contains(badExpr, "coalesce(") {
		t.Errorf("expected no derive expression for non-identifier step name, got: %s", badExpr)
	}

	if derive.withClause == "" {
		t.Error("expected withClause still present due to good_step needing derive")
	}
	if len(derive.guardFallbacks) != 1 {
		t.Fatalf(
			"expected exactly 1 guardFallbacks entry, got %d: %+v",
			len(derive.guardFallbacks),
			derive.guardFallbacks,
		)
	}
	if derive.guardFallbacks[0].column != "bad-step_status" {
		t.Errorf("expected guardFallbacks column %q, got %q", "bad-step_status", derive.guardFallbacks[0].column)
	}
	if derive.guardFallbacks[0].reason != cloneDeriveFallbackReasonNonIdentifierGuard {
		t.Errorf("expected guardFallbacks reason %q, got %q",
			cloneDeriveFallbackReasonNonIdentifierGuard, derive.guardFallbacks[0].reason)
	}
}

// TestBuildCloneStepColumnDerive_AlignmentMismatch_FallsBackVerbatim verifies the alignment
// guard: if stepColumns[i] does not actually equal stepStatusColumn(cfg.Steps[i].Name) — i.e.
// the index-alignment assumption between stepColumns and cfg.Steps doesn't hold for that entry
// — the column falls back to a verbatim copy instead of deriving off a potentially wrong step
// name, while unaffected (still-aligned) columns continue to derive normally.
func TestBuildCloneStepColumnDerive_AlignmentMismatch_FallsBackVerbatim(t *testing.T) {
	cfg := testPipelineConfig()
	qb := NewSlipQueryBuilder(cfg, "ci")
	stepColumns := qb.BuildStepColumns() // [push_parsed_status, builds_status, unit_tests_status, dev_deploy_status]

	// Deliberately misalign index 0 (push_parsed, pure): the column no longer matches
	// qb.StepStatusColumn("push_parsed") ("push_parsed_status"), simulating a caller bug
	// where stepColumns and cfg.Steps fell out of index alignment.
	stepColumns[0] = "some_other_column"

	derive := buildCloneStepColumnDerive(cfg, "ci", stepColumns, nil, qb.StepStatusColumn)

	if derive.exprs[0] != "some_other_column" {
		t.Errorf("expected misaligned column to fall back verbatim, got: %s", derive.exprs[0])
	}
	if strings.Contains(derive.exprs[0], "coalesce(") {
		t.Errorf("expected no derive expression for misaligned column, got: %s", derive.exprs[0])
	}
	// dev_deploy (index 3, still correctly aligned) must still be derived, so the CTE is
	// still present overall.
	if derive.withClause == "" {
		t.Error("expected withClause still present due to dev_deploy still needing derive")
	}
	if len(derive.guardFallbacks) != 1 {
		t.Fatalf(
			"expected exactly 1 guardFallbacks entry, got %d: %+v",
			len(derive.guardFallbacks),
			derive.guardFallbacks,
		)
	}
	if derive.guardFallbacks[0].column != "some_other_column" {
		t.Errorf("expected guardFallbacks column %q, got %q", "some_other_column", derive.guardFallbacks[0].column)
	}
	if derive.guardFallbacks[0].reason != cloneDeriveFallbackReasonAlignmentGuard {
		t.Errorf("expected guardFallbacks reason %q, got %q",
			cloneDeriveFallbackReasonAlignmentGuard, derive.guardFallbacks[0].reason)
	}
}

// TestSqlSingleQuoteEscape covers sqlSingleQuoteEscape directly: backslashes must be escaped
// BEFORE single quotes are doubled, since ClickHouse single-quoted literals honor both
// backslash escapes and doubled-quote escaping — an unescaped backslash immediately preceding
// a doubled quote could otherwise change how that quote is interpreted.
func TestSqlSingleQuoteEscape(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "no special characters", input: "push_parsed", want: "push_parsed"},
		{name: "single quote only", input: "o'brien", want: "o''brien"},
		{name: "embedded backslash", input: `a\b`, want: `a\\b`},
		{name: "trailing backslash", input: `abc\`, want: `abc\\`},
		{name: "backslash immediately before quote", input: `a\'b`, want: `a\\''b`},
		{name: "quote immediately before backslash", input: `a'\b`, want: `a''\\b`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sqlSingleQuoteEscape(tc.input)
			if got != tc.want {
				t.Errorf("sqlSingleQuoteEscape(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestInsertAtomicStatusUpdate_CancelBranchStaysVerbatim verifies that the cancel-row SELECT
// (UNION ALL branch, sign flipped to -1) never contains a derive expression or "WITH derived" —
// cancel rows must mirror exactly the rows being cancelled, unconditionally verbatim.
func TestInsertAtomicStatusUpdate_CancelBranchStaysVerbatim(t *testing.T) {
	var capturedQuery string
	mockSession := &clickhousetest.MockSession{
		QueryRowFunc: func(_ context.Context, _ string, _ ...any) driver.Row {
			return &clickhousetest.MockRow{
				ScanFunc: func(dest ...any) error {
					if len(dest) > 0 {
						if v, ok := dest[0].(*uint64); ok {
							*v = 1
						}
					}
					return nil
				},
			}
		},
		ExecWithArgsFunc: func(_ context.Context, query string, _ ...any) error {
			capturedQuery = query
			return nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	err := store.UpdateSlipStatus(context.Background(), "corr-cancel-verbatim", SlipStatusCompleted)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	unionParts := strings.SplitN(capturedQuery, "UNION ALL", 2)
	if len(unionParts) != 2 {
		t.Fatalf("expected query to contain exactly one UNION ALL, query: %s", capturedQuery)
	}
	cancelBranch, newRowBranch := unionParts[0], unionParts[1]

	if strings.Contains(cancelBranch, "coalesce(") || strings.Contains(cancelBranch, "WITH derived") {
		t.Errorf("cancel branch must stay verbatim (no derive expressions), got: %s", cancelBranch)
	}
	// Sanity: the new-row branch (this pipeline has unoverridden pure steps) DOES contain the
	// derive expression, confirming the split isolated the right half.
	if !strings.Contains(newRowBranch, "coalesce(") {
		t.Errorf("expected new-row branch to contain a derive expression, got: %s", newRowBranch)
	}
}

// TestInsertAtomicHistoryUpdate_ArgOrdering_OverrideAndDerive verifies the full ExecWithArgs
// argument ordering when both a step override and CLONE_DERIVED derivation are in play,
// calling insertAtomicHistoryUpdate directly (bypassing the AppendHistory retry loop) so
// positions are deterministic.
func TestInsertAtomicHistoryUpdate_ArgOrdering_OverrideAndDerive(t *testing.T) {
	var capturedArgs []interface{}
	mockSession := &clickhousetest.MockSession{
		ExecWithArgsFunc: func(_ context.Context, _ string, args ...any) error {
			capturedArgs = args
			return nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")

	const corrID = "corr-arg-order"
	const newVersion = uint64(42)
	const historyJSON = `{"entries":[]}`
	override := stepStatusOverride{columnName: "push_parsed_status", status: StepStatusCompleted}

	err := store.insertAtomicHistoryUpdate(context.Background(), corrID, newVersion, historyJSON, override)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Expected order (dev_deploy is pure/unoverridden, so the CTE is present):
	//   [0] correlationID (cancel WHERE)
	//   [1] newVersion (cancel WHERE)
	//   [2] correlationID (CTE WHERE)
	//   [3] historyJSON (CAST literal)
	//   [4] newVersion (new-row literal)
	//   [5] "completed" (push_parsed_status override)
	//   [6] correlationID (final WHERE)
	want := []interface{}{corrID, newVersion, corrID, historyJSON, newVersion, string(StepStatusCompleted), corrID}
	if len(capturedArgs) != len(want) {
		t.Fatalf("expected %d args, got %d: %v", len(want), len(capturedArgs), capturedArgs)
	}
	for i, w := range want {
		if capturedArgs[i] != w {
			t.Errorf("args[%d] = %v, want %v", i, capturedArgs[i], w)
		}
	}
}

// TestInsertAtomicStatusUpdate_AncestryRetry_PreservesDeriveAndOverrideOrder verifies that when
// the ancestry-column self-healing retry fires (ExecWithArgs returns an ancestry-column error on
// the first attempt), the recursive retry call still produces the same CLONE_DERIVED + override
// precedence and a consistent arg ordering — the retry must not silently drop the override or
// the derive CTE argument.
func TestInsertAtomicStatusUpdate_AncestryRetry_PreservesDeriveAndOverrideOrder(t *testing.T) {
	var stmts []string
	var argSets [][]interface{}
	mockSession := &clickhousetest.MockSession{
		ExecWithArgsFunc: func(_ context.Context, stmt string, args ...any) error {
			stmts = append(stmts, stmt)
			argSets = append(argSets, args)
			if len(stmts) == 1 {
				return &ancestryColumnErr{}
			}
			return nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")
	store.hasAncestryColumn.Store(true)

	override := stepStatusOverride{columnName: "push_parsed_status", status: StepStatusFailed}
	err := store.insertAtomicStatusUpdate(
		context.Background(), "corr-ancestry-retry", uint64(7), SlipStatusFailed, override,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(stmts) != 2 {
		t.Fatalf("expected exactly 2 ExecWithArgs calls (initial + ancestry retry), got %d", len(stmts))
	}
	if !strings.Contains(stmts[0], ColumnAncestry) {
		t.Error("expected first attempt to include the ancestry column")
	}
	if strings.Contains(stmts[1], ColumnAncestry) {
		t.Error("expected retry to omit the ancestry column")
	}

	// Both attempts must still carry the CLONE_DERIVED CTE (dev_deploy is pure/unoverridden)
	// and the override value, in the same relative argument order.
	for i, stmt := range stmts {
		if !strings.Contains(stmt, "WITH derived AS") {
			t.Errorf("attempt %d: expected CLONE_DERIVED CTE to survive the ancestry retry, stmt: %s", i, stmt)
		}
	}
	// Full value-pin (mirrors TestInsertAtomicHistoryUpdate_ArgOrdering_OverrideAndDerive):
	//   [0] correlationID (cancel WHERE)
	//   [1] newVersion (cancel WHERE)
	//   [2] correlationID (CTE WHERE)
	//   [3] newStatus (status literal)
	//   [4] newVersion (new-row literal)
	//   [5] override value (push_parsed_status override)
	//   [6] correlationID (final WHERE)
	want := []interface{}{
		"corr-ancestry-retry", uint64(7), "corr-ancestry-retry",
		string(SlipStatusFailed), uint64(7), string(StepStatusFailed), "corr-ancestry-retry",
	}
	for i, args := range argSets {
		if len(args) != len(want) {
			t.Fatalf("attempt %d: expected %d args, got %d: %v", i, len(want), len(args), args)
		}
		for j, w := range want {
			if args[j] != w {
				t.Errorf("attempt %d: args[%d] = %v, want %v", i, j, args[j], w)
			}
		}
	}
}

// ancestryColumnErr is a minimal error whose message satisfies isAncestryColumnError so the
// ancestry self-healing retry path fires deterministically in unit tests.
type ancestryColumnErr struct{}

func (e *ancestryColumnErr) Error() string {
	return "Missing columns: '" + ColumnAncestry + "' while processing query"
}

// --- CLONE_DERIVED guard-fallback observability (F4) -----------------------------------------
//
// buildCloneStepColumnDerive's guardFallbacks are asserted directly above (in the
// TestBuildCloneStepColumnDerive_* guard tests). The tests below verify the store-level wiring:
// insertAtomicStatusUpdate / insertAtomicHistoryUpdate must surface a non-empty guardFallbacks
// list as a single logger.Warn call carrying correlation_id + the affected column(s) + reason(s),
// since a guard firing here means a code invariant broke and the CLONE_DERIVED stale-clone
// protection is silently off for those columns (bd mycarrier-5dv5).

// nonIdentifierStepPipelineConfig returns a minimal pipeline config with one valid pure step and
// one pure step whose name fails safeStepNameForDerivePattern, so buildCloneStepColumnDerive's
// non-identifier guard fires deterministically for the second column.
func nonIdentifierStepPipelineConfig() *PipelineConfig {
	cfg := &PipelineConfig{
		Steps: []StepConfig{
			{Name: "good_step"},
			{Name: "bad-step"},
		},
	}
	cfg.stepsByName = map[string]*StepConfig{
		"good_step": &cfg.Steps[0],
		"bad-step":  &cfg.Steps[1],
	}
	return cfg
}

// TestInsertAtomicHistoryUpdate_GuardFallback_LogsWarn verifies that when
// buildCloneStepColumnDerive reports a guard fallback (non-identifier step name here),
// insertAtomicHistoryUpdate emits exactly one Warn log carrying the correlation ID and the
// affected column/reason.
func TestInsertAtomicHistoryUpdate_GuardFallback_LogsWarn(t *testing.T) {
	mockSession := &clickhousetest.MockSession{
		ExecWithArgsFunc: func(_ context.Context, _ string, _ ...any) error {
			return nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, nonIdentifierStepPipelineConfig(), "ci")
	capLog := &capturingLogger{}
	store.logger = capLog

	const corrID = "corr-guard-fallback-history"
	err := store.insertAtomicHistoryUpdate(context.Background(), corrID, uint64(1), `{"entries":[]}`)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	warnCalls := capLog.callsWithLevel("warn")
	if len(warnCalls) != 1 {
		t.Fatalf("expected exactly 1 Warn call for the guard fallback, got %d: %+v", len(warnCalls), capLog.calls)
	}
	fields := warnCalls[0].fields
	if cid, _ := fields["correlation_id"].(string); cid != corrID {
		t.Errorf("expected correlation_id %q in Warn fields, got %v", corrID, fields["correlation_id"])
	}
	cols, ok := fields["columns"].([]string)
	if !ok || len(cols) != 1 || cols[0] != "bad-step_status" {
		t.Errorf("expected columns [\"bad-step_status\"] in Warn fields, got %v", fields["columns"])
	}
	reasons, ok := fields["reasons"].([]string)
	if !ok || len(reasons) != 1 || reasons[0] != cloneDeriveFallbackReasonNonIdentifierGuard {
		t.Errorf(
			"expected reasons [%q] in Warn fields, got %v",
			cloneDeriveFallbackReasonNonIdentifierGuard,
			fields["reasons"],
		)
	}
}

// TestInsertAtomicStatusUpdate_GuardFallback_LogsWarn verifies the same guard-fallback Warn
// wiring for insertAtomicStatusUpdate (the sibling CLONE_DERIVED writer).
func TestInsertAtomicStatusUpdate_GuardFallback_LogsWarn(t *testing.T) {
	mockSession := &clickhousetest.MockSession{
		ExecWithArgsFunc: func(_ context.Context, _ string, _ ...any) error {
			return nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, nonIdentifierStepPipelineConfig(), "ci")
	capLog := &capturingLogger{}
	store.logger = capLog

	const corrID = "corr-guard-fallback-status"
	err := store.insertAtomicStatusUpdate(context.Background(), corrID, uint64(1), SlipStatusCompleted)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	warnCalls := capLog.callsWithLevel("warn")
	if len(warnCalls) != 1 {
		t.Fatalf("expected exactly 1 Warn call for the guard fallback, got %d: %+v", len(warnCalls), capLog.calls)
	}
	fields := warnCalls[0].fields
	if cid, _ := fields["correlation_id"].(string); cid != corrID {
		t.Errorf("expected correlation_id %q in Warn fields, got %v", corrID, fields["correlation_id"])
	}
	cols, ok := fields["columns"].([]string)
	if !ok || len(cols) != 1 || cols[0] != "bad-step_status" {
		t.Errorf("expected columns [\"bad-step_status\"] in Warn fields, got %v", fields["columns"])
	}
}

// TestInsertAtomicHistoryUpdate_NoGuardFallback_NoWarn is the negative counterpart: a normal,
// well-formed pipeline config (no alignment/non-identifier guard firing — only the expected
// aggregate-verbatim and pure-step-derive paths) must NOT emit a guard-fallback Warn.
func TestInsertAtomicHistoryUpdate_NoGuardFallback_NoWarn(t *testing.T) {
	mockSession := &clickhousetest.MockSession{
		ExecWithArgsFunc: func(_ context.Context, _ string, _ ...any) error {
			return nil
		},
	}
	store := NewClickHouseStoreFromSession(mockSession, testPipelineConfig(), "ci")
	capLog := &capturingLogger{}
	store.logger = capLog

	err := store.insertAtomicHistoryUpdate(context.Background(), "corr-no-fallback", uint64(1), `{"entries":[]}`)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if warnCalls := capLog.callsWithLevel("warn"); len(warnCalls) != 0 {
		t.Errorf("expected no guard-fallback Warn for a well-formed pipeline config, got %d: %+v",
			len(warnCalls), warnCalls)
	}
}
