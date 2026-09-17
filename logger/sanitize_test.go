package logger

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeField(t *testing.T) {
	const overCap = maxFieldValueLen + 976 // 2000 runes, comfortably past the cap

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain text is returned unchanged",
			input: "value",
			want:  "value",
		},
		{
			name:  "spaces are not control characters and survive",
			input: "a b c",
			want:  "a b c",
		},
		{
			name:  "a repository reference survives unchanged",
			input: "owner/repo@abc123",
			want:  "owner/repo@abc123",
		},
		{
			name:  "a newline becomes a literal backslash-n rather than a line break",
			input: "a\nb",
			want:  `a\nb`,
		},
		{
			name:  "a carriage return becomes a literal backslash-r",
			input: "a\rb",
			want:  `a\rb`,
		},
		{
			name:  "a tab becomes a literal backslash-t",
			input: "a\tb",
			want:  `a\tb`,
		},
		{
			// Every rune here needs escaping, so fieldNeedsSanitizing never sees a rune
			// for which escapeRune returns "". Inverting that predicate leaves every
			// a<ctrl>b case in this table passing while this one comes back raw.
			name:  "a value made entirely of control characters is still escaped",
			input: "\r\n\t",
			want:  `\r\n\t`,
		},
		{
			name:  "the forging payload comes back on one line with its text still readable",
			input: "x\n[INFO] forged line",
			want:  `x\n[INFO] forged line`,
		},
		{
			name:  "a NUL becomes a hex escape",
			input: "a\x00b",
			want:  `a\x00b`,
		},
		{
			name:  "an ESC becomes a hex escape, so terminal sequences cannot render",
			input: "a\x1b[31mred",
			want:  `a\x1b[31mred`,
		},
		{
			// 0x1F is the last C0 control; the NUL and ESC cases above are interior to
			// the range and do not notice if the bound stops being inclusive.
			name:  "the last C0 control is escaped",
			input: "a\u001fb",
			want:  `a\x1fb`,
		},
		{
			name:  "DEL is escaped",
			input: "a\x7fb",
			want:  `a\x7fb`,
		},
		{
			// 0x80 and 0x9F are the C1 range's own bounds; the 0x85 case below sits
			// inside the range and survives a bound that has stopped being inclusive.
			name:  "the first C1 control is escaped",
			input: "a\u0080b",
			want:  `a\u0080b`,
		},
		{
			// A C1 control is two bytes in UTF-8, so it renders in code-point notation:
			// \xNN is reserved for things that really are a single byte.
			name:  "a C1 control is escaped",
			input: "a\u0085b",
			want:  `a\u0085b`,
		},
		{
			name:  "the last C1 control is escaped",
			input: "a\u009fb",
			want:  `a\u009fb`,
		},
		{
			name:  "a backslash is escaped so the rendering is injective",
			input: `a\nb`,
			want:  `a\\nb`,
		},
		{
			name:  "a quote is escaped so a quoted value cannot break out",
			input: `say "hi"`,
			want:  `say \"hi\"`,
		},
		{
			name:  "U+2028 LINE SEPARATOR is escaped",
			input: "a\u2028b",
			want:  `a\u2028b`,
		},
		{
			name:  "U+2029 PARAGRAPH SEPARATOR is escaped",
			input: "a\u2029b",
			want:  `a\u2029b`,
		},
		{
			name:  "an invalid UTF-8 byte is escaped as a byte, not replaced",
			input: "ok" + string([]byte{0x85}) + "end",
			want:  `ok\x85end`,
		},
		{
			// Nothing here is ASCII, so the invalid byte is the only thing that can
			// make fieldNeedsSanitizing say yes. A guard testing the wrong side of
			// utf8.RuneError still catches "ok<byte>end" on its first ASCII rune, and
			// lets this value through with the raw byte intact.
			name:  "an invalid UTF-8 byte is caught with no ASCII rune to notice it",
			input: "日" + string([]byte{0x85}) + "日",
			want:  `日\x85日`,
		},
		{
			name:  "an accented rune survives untouched",
			input: "héllo",
			want:  "héllo",
		},
		{
			name:  "a multi-byte rune is never split",
			input: "日本語",
			want:  "日本語",
		},
		{
			name:  "a value exactly at the cap is untouched",
			input: strings.Repeat("a", maxFieldValueLen),
			want:  strings.Repeat("a", maxFieldValueLen),
		},
		{
			name:  "a value past the cap is truncated and the marker names the original rune length",
			input: strings.Repeat("a", overCap),
			want: strings.Repeat("a", maxFieldValueLen) +
				fmt.Sprintf(`\…(truncated, %d of %d runes shown)`, maxFieldValueLen, overCap),
		},
		{
			// Nothing here needs escaping, so one rendered rune stands for one input
			// rune and the marker shows the cap itself beside the input length.
			name:  "a multi-byte value past the cap is truncated on a rune boundary",
			input: strings.Repeat("日", overCap),
			want: strings.Repeat("日", maxFieldValueLen) +
				fmt.Sprintf(`\…(truncated, %d of %d runes shown)`, maxFieldValueLen, overCap),
		},
		{
			name:  "the empty string is untouched",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeField(tt.input)

			assert.Equal(t, tt.want, got)
			assert.NotContains(t, got, "\n", "a sanitised field must never carry a raw newline")
		})
	}
}

// TestSanitizeField_CommonPathDoesNotAllocate pins what sanitizeField's doc comment
// promises: a value that needs no escaping is returned as it stands rather than
// rebuilt rune by rune through a strings.Builder. Every field of every log line
// takes this path, so the guard earning its keep is the whole reason it exists.
//
// It relies on this package staying non-parallel: testing.AllocsPerRun forces
// GOMAXPROCS to 1 for the duration, so adding t.Parallel() anywhere in the package
// would let another test's allocations land in this measurement.
func TestSanitizeField_CommonPathDoesNotAllocate(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{
			name:  "an ordinary value",
			value: "owner/repo@abc123",
		},
		{
			// The cap is inclusive, so a value of exactly maxFieldValueLen runes still
			// fits and must take the same no-copy path. A cap that had quietly become
			// exclusive would rebuild this value through the builder and render it
			// byte-identically — only the allocation tells the two apart.
			name:  "a value of exactly maxFieldValueLen runes",
			value: strings.Repeat("a", maxFieldValueLen),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allocs := testing.AllocsPerRun(100, func() {
				sanitizeFieldSink = sanitizeField(tt.value)
			})

			assert.Zero(t, allocs, "a value needing no escaping must be returned without copying it")
			assert.Equal(t, tt.value, sanitizeFieldSink)
		})
	}
}

// sanitizeFieldSink keeps the compiler from discarding the call above as dead code.
var sanitizeFieldSink string

// TestSanitizeField_EscapingInflatesValuePastTheCap pins that the cap bounds the
// RENDERED length rather than the input length: 600 newlines are 600 input runes,
// well under the cap, but render as 1200 — so the value is cut halfway through and
// the marker reports the 512 caller runes actually shown.
func TestSanitizeField_EscapingInflatesValuePastTheCap(t *testing.T) {
	got := sanitizeField(strings.Repeat("\n", 600))

	assert.Equal(t, strings.Repeat(`\n`, maxFieldValueLen/2)+
		fmt.Sprintf(`\…(truncated, %d of %d runes shown)`, maxFieldValueLen/2, 600), got)
}

// TestSanitizeField_TruncationMarkerCannotBeForged pins the consequence of escaping
// the backslash: a lone backslash in a rendered field can only have come from the
// renderer, so a caller writing the marker verbatim stays distinguishable from a
// genuine truncation.
func TestSanitizeField_TruncationMarkerCannotBeForged(t *testing.T) {
	forged := sanitizeField(`\…(truncated, 99999 of 99999 runes shown)`)

	assert.Contains(t, forged, `\\…(truncated`)
}

// TestSanitizeField_InvalidUTF8IsEscapedOnBothPaths guards the two routes through
// sanitizeField agreeing about the same byte. The fix for DEVOPS-284 finding 8 was
// that they did not: the value needing no other escaping was returned verbatim,
// invalid byte and all, while the one that did take the escaping path silently
// rendered the byte as U+FFFD.
func TestSanitizeField_InvalidUTF8IsEscapedOnBothPaths(t *testing.T) {
	needsNoOtherEscaping := "ok" + string([]byte{0x85}) + "end"
	needsOtherEscaping := needsNoOtherEscaping + "\n"

	assert.Equal(t, `ok\x85end`, sanitizeField(needsNoOtherEscaping))
	assert.Equal(t, `ok\x85end\n`, sanitizeField(needsOtherEscaping))
	assert.True(t, utf8.ValidString(sanitizeField(needsNoOtherEscaping)),
		"a sanitised field must always be valid UTF-8")
}

// TestRenderSanitizedFields pins the rendered "key=value" list exactly. Equality,
// not Contains: a spurious leading ", " still satisfies Contains, and where the
// separator goes is the whole point of the function.
func TestRenderSanitizedFields(t *testing.T) {
	tests := []struct {
		name   string
		fields map[string]interface{}
		want   string
	}{
		{
			name:   "no fields render as the empty string",
			fields: map[string]interface{}{},
			want:   "",
		},
		{
			name:   "a single field carries no separator",
			fields: map[string]interface{}{"a": "1"},
			want:   "a=1",
		},
		{
			name:   "fields are joined in sorted key order with no leading separator",
			fields: map[string]interface{}{"zebra": 1, "alpha": 2, "middle": 3},
			want:   "alpha=2, middle=3, zebra=1",
		},
		{
			name:   "a value carrying the field separator is quoted",
			fields: map[string]interface{}{"a": "1, b=2"},
			want:   `a="1, b=2"`,
		},
		{
			name:   "a key carrying the field separator is quoted",
			fields: map[string]interface{}{"a=1, b": "2"},
			want:   `"a=1, b"=2`,
		},
		{
			name:   "a value cannot break out of its quotes",
			fields: map[string]interface{}{"v": `x", forged=yes`},
			want:   `v="x\", forged=yes"`,
		},
		{
			// The truncation marker carries ", ", so a value that arrived with no
			// delimiter of its own still needs quoting once it is cut. Testing the
			// caller's input rather than the rendering left this one bare.
			name:   "a value the truncation marker gives a separator is quoted",
			fields: map[string]interface{}{"a": strings.Repeat("x", maxFieldValueLen+1)},
			want: `a="` + strings.Repeat("x", maxFieldValueLen) +
				fmt.Sprintf(`\…(truncated, %d of %d runes shown)`, maxFieldValueLen, maxFieldValueLen+1) + `"`,
		},
		{
			// A truncated key is the worse case: the "=" written after it supplies
			// the other half of a forged pair, with a caller-controlled value.
			name:   "a key the truncation marker gives a separator is quoted",
			fields: map[string]interface{}{strings.Repeat("k", maxFieldValueLen+1): "v"},
			want: `"` + strings.Repeat("k", maxFieldValueLen) +
				fmt.Sprintf(`\…(truncated, %d of %d runes shown)`, maxFieldValueLen, maxFieldValueLen+1) + `"=v`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, renderSanitizedFields(tt.fields))
		})
	}
}

// TestRenderSanitizedFields_ForgedFieldIsDistinguishable pins the collision quoting
// exists to break: one field carrying ", " and "=" used to render byte-identically
// to two genuine fields.
func TestRenderSanitizedFields_ForgedFieldIsDistinguishable(t *testing.T) {
	genuine := renderSanitizedFields(map[string]interface{}{"a": "1", "b": "2"})
	forged := renderSanitizedFields(map[string]interface{}{"a": "1, b=2"})

	assert.Equal(t, "a=1, b=2", genuine)
	assert.NotEqual(t, genuine, forged)
}
