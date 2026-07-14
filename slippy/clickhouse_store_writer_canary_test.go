package slippy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// routingSlipsWriterClassification is the allowlist of functions in this package that write
// (via ExecWithArgs INSERT ... TableRoutingSlips ...) to the routing_slips table, along with
// how each one arrives at its step-status column values:
//
//   - FRESH_ROW:      builds every column from literal values held in memory (e.g. a fully
//     resolved *Slip); no clone-SELECT of a prior DB row is involved.
//   - R2_DERIVED:     writes literal values from an in-memory Slip whose Steps have already
//     had the R2 argMax-derived 3-tier precedence applied (see resolveEffectiveStepStatuses /
//     updateWithOverrides) before this function is called.
//   - CLONE_DERIVED:  clones the current top row via INSERT...SELECT and must apply the
//     buildCloneStepColumnDerive 3-tier precedence (override > argMax-derived > verbatim
//     clone) to every PURE pipeline step-status column it selects, per bd mycarrier-5dv5.
//
// New routing_slips writers MUST be added here with the correct classification. If a new
// CLONE_DERIVED writer is added, it MUST call buildCloneStepColumnDerive (or an equivalent
// shared helper) for its step-status columns rather than re-introducing a verbatim clone —
// see the bd mycarrier-5dv5 fix and its doc comment above insertAtomicStatusUpdate in
// clickhouse_store.go for the staleness bug this prevents.
var routingSlipsWriterClassification = map[string]string{
	"insertRow":                      "FRESH_ROW",
	"insertAtomicUpdateWithVersions": "R2_DERIVED",
	"insertAtomicStatusUpdate":       "CLONE_DERIVED",
	"insertAtomicHistoryUpdate":      "CLONE_DERIVED",
}

// TestRoutingSlipsWriterCanary scans every non-test .go source file in this package for
// functions whose body contains both an "INSERT INTO" statement and a reference to the
// TableRoutingSlips constant — i.e. functions that write to the routing_slips table — and
// asserts each one is present in routingSlipsWriterClassification with a recognised
// classification (FRESH_ROW / R2_DERIVED / CLONE_DERIVED).
//
// This is a function-name-granularity canary, not a SQL parser: it does not validate that a
// CLONE_DERIVED writer actually calls buildCloneStepColumnDerive correctly (that's covered by
// the dedicated unit tests in clickhouse_store_clone_derive_test.go). Its job is narrower and
// cheaper to keep green: make it impossible to add a brand-new routing_slips writer without a
// human consciously classifying it, so the next stale-clone-shaped bug gets caught at review
// time instead of in production.
//
// KNOWN BLIND SPOT: the substring match below (strings.Contains for "INSERT INTO" and
// "TableRoutingSlips") only catches writers that construct their INSERT query inline, with the
// literal "INSERT INTO" text and the TableRoutingSlips constant both appearing directly in the
// scanned function's own body. A writer that delegates query construction to a helper function
// (e.g. builds the query text via a shared query-builder method, or references the table name
// through a local variable rather than the TableRoutingSlips constant directly) will NOT be
// found by this scan and would silently bypass the classification requirement. This is a real
// gap, not a hypothetical one — treat it as plain fact when relying on this canary.
func TestRoutingSlipsWriterCanary(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("failed to read package directory: %v", err)
	}

	fset := token.NewFileSet()
	var foundWriters []string

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		src, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("failed to read %s: %v", name, err)
		}

		file, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			t.Fatalf("failed to parse %s: %v", name, err)
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}

			start := fset.Position(fn.Body.Pos()).Offset
			end := fset.Position(fn.Body.End()).Offset
			if start < 0 || end > len(src) || start >= end {
				continue
			}
			body := string(src[start:end])

			// Substring match only — catches inline INSERT construction (literal "INSERT
			// INTO" text plus the TableRoutingSlips constant, both directly in this
			// function's body). A writer that delegates query construction to a helper
			// (with the table name supplied via a variable rather than the
			// TableRoutingSlips constant) is a known blind spot; see the doc comment
			// above TestRoutingSlipsWriterCanary.
			if strings.Contains(body, "INSERT INTO") && strings.Contains(body, "TableRoutingSlips") {
				foundWriters = append(foundWriters, fn.Name.Name)
			}
		}
	}

	if len(foundWriters) == 0 {
		t.Fatal("expected to find at least the known routing_slips writers; found none — " +
			"canary scan logic may be broken (check TestRoutingSlipsWriterCanary)")
	}

	for _, fnName := range foundWriters {
		classification, ok := routingSlipsWriterClassification[fnName]
		if !ok {
			t.Errorf(
				"function %q writes to routing_slips (INSERT INTO ... TableRoutingSlips) but is "+
					"not in routingSlipsWriterClassification. Classify it as FRESH_ROW, R2_DERIVED, "+
					"or CLONE_DERIVED in clickhouse_store_writer_canary_test.go. If it is "+
					"CLONE_DERIVED (clones a prior row and overrides only some columns), it MUST "+
					"apply buildCloneStepColumnDerive's 3-tier precedence to its pure pipeline "+
					"step-status columns — see bd mycarrier-5dv5 and the doc comment above "+
					"insertAtomicStatusUpdate in clickhouse_store.go.",
				fnName,
			)
			continue
		}
		switch classification {
		case "FRESH_ROW", "R2_DERIVED", "CLONE_DERIVED":
			// recognised
		default:
			t.Errorf("function %q has unrecognised classification %q; must be one of "+
				"FRESH_ROW, R2_DERIVED, CLONE_DERIVED", fnName, classification)
		}
	}

	// Guard against silent bit-rot the other direction: every allowlist entry should still
	// correspond to a function this scan actually found. A stale entry usually means the
	// function was renamed or removed and the allowlist wasn't updated.
	foundSet := make(map[string]bool, len(foundWriters))
	for _, fnName := range foundWriters {
		foundSet[fnName] = true
	}
	for fnName := range routingSlipsWriterClassification {
		if !foundSet[fnName] {
			t.Errorf(
				"routingSlipsWriterClassification lists %q but no function with that name and "+
					"body containing INSERT INTO + TableRoutingSlips was found — stale allowlist "+
					"entry (function renamed/removed?)",
				fnName,
			)
		}
	}
}
