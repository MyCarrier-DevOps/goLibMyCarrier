package logger

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// maxFieldValueLen caps how many runes of a caller-supplied field are RENDERED.
// Escaping alone leaves size inflation as a residual: a caller can still push an
// arbitrarily large value into a single rendered field. Values whose rendering
// exceeds this are cut and marked.
//
// The cap is tested at the top of each iteration, so the escape that crosses it is
// still written whole: a rendering runs up to 5 runes over — \uNNNN is 6 runes and
// one may begin at rendered rune 1023 — and a value that ends inside that overshoot
// is complete, so it is neither cut nor marked. Measured: 1023 "a"s plus a newline
// renders 1025 runes with no marker.
//
// Note this bounds each field, not the whole log line: renderSanitizedFields places
// no limit on how many fields a caller passes.
const maxFieldValueLen = 1024

// truncationMarkerFormat is appended when a value is cut at maxFieldValueLen. It
// names both how many of the caller's runes are shown and how many arrived, so the
// annotation is readable next to a rendering that escaping may have inflated.
//
// The leading backslash is what makes the marker trustworthy. escapeRune doubles a
// caller-supplied backslash, so a lone backslash can only have come from here: a
// caller who writes this text verbatim renders with two backslashes and is
// distinguishable from a genuine truncation.
const truncationMarkerFormat = `\…(truncated, %d of %d runes shown)`

// sanitizeField makes a caller-supplied string safe to interpolate into a log line.
//
// StdLogger and LogAdapter render fields by folding them into a single formatted
// line, so a value carrying a newline opens a second line that reads as a genuine
// log entry — a caller-supplied value can forge a log record (DEVOPS-284).
//
// Control characters are escaped rather than stripped: the rendered field stays a
// faithful record of what the caller passed, so an attempted forgery is still
// legible as evidence instead of disappearing. The escaping is injective — the
// backslash and the quote are escaped alongside the controls — so a rendered `\n`
// always means a real newline arrived and never a caller-supplied backslash-n.
//
// Bytes that are not valid UTF-8 are escaped as \xNN rather than decoded. Ranging a
// string would silently replace them with U+FFFD, which destroys what arrived; the
// byte form says exactly which byte it was, and is distinguishable from \uNNNN,
// which is only ever emitted for a decoded code point.
//
// The input is returned unchanged when nothing needs escaping and it fits within
// maxFieldValueLen, so the common path allocates nothing.
func sanitizeField(s string) string {
	if !fieldNeedsSanitizing(s) {
		return s
	}

	var b strings.Builder
	// Size from the cap, not the input: sanitizeField's result is bounded by
	// maxFieldValueLen, so growing to len(s) would reserve for the part a large
	// value is about to have discarded. Each retained rune costs at most
	// utf8.UTFMax bytes, plus room for the truncation marker.
	const maxRendered = maxFieldValueLen*utf8.UTFMax + 64
	b.Grow(min(len(s), maxRendered))

	// written counts runes placed in the builder, so the cap bounds the rendered
	// length rather than the input length. Every escape is ASCII, so its rune count
	// is its byte count. shown counts the CALLER's runes those stand for, which is
	// what the marker reports: \uNNNN is the longest escape at 6 runes, so 1024
	// rendered runes can be as few as 171 input runes.
	written, shown := 0, 0
	for i := 0; i < len(s); {
		if written >= maxFieldValueLen {
			fmt.Fprintf(&b, truncationMarkerFormat, shown, utf8.RuneCountInString(s))
			break
		}

		r, size := utf8.DecodeRuneInString(s[i:])

		// A byte that begins no valid sequence. DecodeRuneInString reports it as
		// RuneError with size 1; escape the byte itself rather than the U+FFFD it
		// decoded to, so the record says what actually arrived.
		if r == utf8.RuneError && size == 1 {
			escaped := fmt.Sprintf(`\x%02x`, s[i])
			b.WriteString(escaped)
			written += len(escaped)
			shown++
			i += size
			continue
		}

		if escaped := escapeRune(r); escaped != "" {
			b.WriteString(escaped)
			written += len(escaped)
			shown++
			i += size
			continue
		}

		b.WriteRune(r)
		written++
		shown++
		i += size
	}

	return b.String()
}

// renderSanitizedFields renders fields as a "key=value" list joined by ", ", in
// sorted key order, with every key and value passed through renderField.
//
// Sorting is not cosmetic: map iteration order is randomised, so without it the
// same call produces a different line each time and no test of the rendered output
// can mean anything. Both the key and the value are rendered because both are
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
		b.WriteString(renderField(k))
		b.WriteByte('=')
		b.WriteString(renderField(fmt.Sprintf("%v", fields[k])))
	}

	return b.String()
}

// renderField renders one caller-supplied key or value for the "key=value" list,
// quoting it when it carries the renderer's own structure.
//
// ", " separates fields and "=" separates key from value, so an unquoted value
// holding them forges a sibling field: {"a": "1, b=2"} would otherwise render
// byte-identically to {"a": "1", "b": "2"}. Values carrying neither stay bare, so
// ordinary lines are unchanged.
//
// The test is on the RENDERED form, not on the input. Truncation appends a marker
// that itself contains ", ", so a value carrying no delimiter of its own can still
// acquire one; testing the input left that case bare, and a truncated KEY then
// rendered a syntactically complete sibling pair — the "=" written after it
// supplying the other half. Escaping never introduces "," or "=", and a quote in
// the rendering only ever comes from a quote in the input, so for anything short of
// the cap this is identical to testing the input.
//
// The quotes are written around the already-escaped value rather than by
// strconv.Quote. sanitizeField escapes the quote character itself, so the value
// cannot break out, and the escape is charged to maxFieldValueLen — quoting
// afterwards would re-escape every backslash sanitizeField emitted and double the
// capped length.
func renderField(s string) string {
	rendered := sanitizeField(s)
	if !strings.ContainsAny(rendered, `,="`) {
		return rendered
	}

	return `"` + rendered + `"`
}

// escapeRune returns the escaped rendering of r, or the empty string when r is safe
// to emit as it stands.
//
// The backslash and the quote are escaped so the rendering is injective and so a
// quoted value cannot break out of its quotes. C0 controls, DEL and C1 controls are
// escaped because a terminal may act on them; U+2028 and U+2029 are escaped because
// UAX #14 makes them mandatory line breaks, so a renderer honouring it starts a new
// line on them even though they are not controls.
//
// Byte notation (\xNN) is used only where the code point really is one byte in
// UTF-8. C1, U+2028 and U+2029 use \uNNNN, which is both accurate and what
// strconv.Quote emits, and keeps them distinguishable from a raw byte.
func escapeRune(r rune) string {
	switch r {
	case '\\':
		return `\\`
	case '"':
		return `\"`
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\t':
		return `\t`
	}

	const (
		c0End              = 0x1F
		del                = 0x7F
		c1Start            = 0x80
		c1End              = 0x9F
		lineSeparator      = 0x2028
		paragraphSeparator = 0x2029
	)

	if r <= c0End || r == del {
		return fmt.Sprintf(`\x%02x`, r)
	}

	if r >= c1Start && r <= c1End {
		return fmt.Sprintf(`\u%04x`, r)
	}

	if r == lineSeparator || r == paragraphSeparator {
		return fmt.Sprintf(`\u%04x`, r)
	}

	return ""
}

// fieldNeedsSanitizing reports whether s would be altered by sanitizeField. It
// exists so the common case — a short value with no control characters — is a
// single scan that returns the input untouched. It decodes exactly as
// sanitizeField does, so the two cannot disagree about the same input.
func fieldNeedsSanitizing(s string) bool {
	runes := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		runes++
		if runes > maxFieldValueLen {
			return true
		}
		if r == utf8.RuneError && size == 1 {
			return true
		}
		if escapeRune(r) != "" {
			return true
		}
		i += size
	}

	return false
}
