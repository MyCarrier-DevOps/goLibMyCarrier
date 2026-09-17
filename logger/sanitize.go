package logger

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// maxFieldValueLen caps how many runes of a caller-supplied field are rendered.
// Escaping alone leaves size inflation as a residual: a caller can still push an
// arbitrarily large value into a single log line. Values longer than this are cut
// and marked with their original length, so the record still says what arrived.
const maxFieldValueLen = 1024

// truncationMarkerFormat is appended when a value is cut at maxFieldValueLen.
// It names the original rune count so the truncation is visible in the record
// rather than silent.
const truncationMarkerFormat = "…(truncated, %d runes)"

// sanitizeField makes a caller-supplied string safe to interpolate into a log line.
//
// StdLogger and LogAdapter render fields by folding them into a single formatted
// line, so a value carrying a newline opens a second line that reads as a genuine
// log entry — a caller-supplied value can forge a log record (DEVOPS-284).
//
// Control characters are escaped rather than stripped: the rendered field stays a
// faithful record of what the caller passed, so an attempted forgery is still
// legible as evidence instead of disappearing. Escapes are chosen over
// strconv.Quote because quoting would change the shape of every field in every log
// line, where escaping only changes values that were dangerous. The result is a
// readable rendering, not a reversible encoding — a backslash is passed through
// unchanged so ordinary values such as Windows paths are not disfigured.
//
// The input is returned unchanged when nothing needs escaping and it fits within
// maxFieldValueLen, so the common path allocates nothing.
func sanitizeField(s string) string {
	if !fieldNeedsSanitizing(s) {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))

	// written counts runes placed in the builder, so the cap bounds the rendered
	// length rather than the input length. Every escape is ASCII, so its rune
	// count is its byte count.
	written := 0
	for _, r := range s {
		if written >= maxFieldValueLen {
			fmt.Fprintf(&b, truncationMarkerFormat, utf8.RuneCountInString(s))
			break
		}
		if escaped := escapeRune(r); escaped != "" {
			b.WriteString(escaped)
			written += len(escaped)
			continue
		}
		b.WriteRune(r)
		written++
	}

	return b.String()
}

// renderSanitizedFields renders fields as a "key=value" list joined by ", ", in
// sorted key order, with every key and value passed through sanitizeField.
//
// Sorting is not cosmetic: map iteration order is randomised, so without it the
// same call produces a different line each time and no test of the rendered output
// can mean anything. Both the key and the value are sanitised because both are
// caller-supplied.
//
// Callers must pass the result as a single pre-rendered argument. Handing the map
// itself to a formatting logger is what DEVOPS-284 fixed: the fields then land in
// the message, where the underlying logger's own escaping does not reach them.
func renderSanitizedFields(fields map[string]interface{}) string {
	if len(fields) == 0 {
		return ""
	}

	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(sanitizeField(k))
		b.WriteByte('=')
		b.WriteString(sanitizeField(fmt.Sprintf("%v", fields[k])))
	}

	return b.String()
}

// escapeRune returns the escaped rendering of r, or the empty string when r is
// safe to emit as it stands. C0 controls, DEL and C1 controls are escaped: the C0
// range is what forges a line, and DEL and C1 are escaped alongside it because a
// terminal may act on them.
func escapeRune(r rune) string {
	switch r {
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\t':
		return `\t`
	default:
	}

	const (
		del      = 0x7F
		c1Start  = 0x80
		c1End    = 0x9F
		c0Length = 0x20
	)

	if r < c0Length || r == del || (r >= c1Start && r <= c1End) {
		return fmt.Sprintf(`\x%02x`, r)
	}

	return ""
}

// fieldNeedsSanitizing reports whether s would be altered by sanitizeField. It
// exists so the common case — a short value with no control characters — is a
// single scan that returns the input untouched.
func fieldNeedsSanitizing(s string) bool {
	runes := 0
	for _, r := range s {
		runes++
		if runes > maxFieldValueLen || escapeRune(r) != "" {
			return true
		}
	}

	return false
}
