package slippy

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// stepColumnSpliceSites are the SQL sites that interpolate a %s into identifier position in
// this package's production code. Each one is listed with why it is allowed, and the test below
// asserts the set has not grown.
//
// This replaces a pair of greps the convention's doc comment used to offer as "the check"
// (PR #87 review, jhicks). The first grep ran clean; the second matched five legitimate sites,
// so a reviewer running it could not tell them from a regression — which is the whole property
// that made the first one worth having. A baseline that fails CI on a sixth hit is the version
// of that check which actually holds the line.
var stepColumnSpliceSites = map[string]string{
	"postgres_store.go:UPDATE routing_slips SET %s WHERE correlation_id = $%d": "" +
		"splices a whole SET LIST joined from slipColumns(), not a single identifier",
	"postgres_store.go:SELECT %s FROM routing_slips WHERE correlation_id = $1": "" +
		"splices a whole SELECT LIST joined from slipSelectColumns()",
	"postgres_store.go:SELECT %s FROM routing_slips WHERE correlation_id = $1 FOR UPDATE": "" +
		"splices the claim-state SELECT LIST joined from claimStateColumns()",
	"postgres_store_updates.go:UPDATE routing_slips SET %s = $1, updated_at = now() WHERE correlation_id = $2": "" +
		"splices col, which came from stepStatusColumn",
	"postgres_store_updates.go:SELECT %s FROM routing_slips WHERE correlation_id = $1": "" +
		"splices aggregateColumn(aggStep)",
}

// TestStepColumnConvention_NoNewHandBuiltIdentifiers pins the invariant stepStatusColumn and
// aggregateColumn exist to hold: no caller builds a step's column name by hand.
//
// The `<name>_status` form is checked absolutely — any hit outside an error message is a
// regression. The bare-aggregate form cannot be checked that way, because a legitimate
// column-LIST splice looks identical to a hand-built identifier at the level of a regex, so it
// is checked against the baseline above instead.
func TestStepColumnConvention_NoNewHandBuiltIdentifiers(t *testing.T) {
	statusForm := regexp.MustCompile(`%s_status`)
	spliceForm := regexp.MustCompile(`Sprintf\(.*(?:SELECT|UPDATE|ALTER|SET) .*%s`)
	stmt := regexp.MustCompile(`"((?:SELECT|UPDATE|ALTER)[^"]*%s[^"]*)"`)

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing package files: %v", err)
	}

	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		body, readErr := os.ReadFile(f)
		if readErr != nil {
			t.Fatalf("reading %s: %v", f, readErr)
		}
		for _, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}

			// Absolute: every real splice of the status form goes through Sprintf, and
			// stepStatusColumn is what should produce it. Error TEXT also contains the
			// literal — validateStepIdentifier's messages quote the generated column shape at
			// the reader — and that is not a splice, so the Sprintf conjunct is what separates
			// them. A multi-line Errorf puts its text on a continuation line with no Sprintf,
			// which is exactly the case this distinguishes.
			if statusForm.MatchString(line) && strings.Contains(line, "Sprintf") {
				t.Errorf("%s builds a `_status` column by hand; use stepStatusColumn:\n\t%s", f, trimmed)
			}

			if !spliceForm.MatchString(line) {
				continue
			}
			m := stmt.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			key := f + ":" + m[1]
			seen[key] = true
			if _, ok := stepColumnSpliceSites[key]; !ok {
				t.Errorf("new identifier splice in %s — route it through stepStatusColumn or "+
					"aggregateColumn, or add it to stepColumnSpliceSites with a reason:\n\t%s", f, trimmed)
			}
		}
	}

	// The baseline must not rot either: a site that no longer exists should leave the list.
	for key := range stepColumnSpliceSites {
		if !seen[key] {
			t.Errorf("stepColumnSpliceSites lists %q, which no longer exists; remove it", key)
		}
	}
}
