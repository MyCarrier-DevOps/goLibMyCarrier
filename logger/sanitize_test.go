package logger

import (
	"fmt"
	"strings"
	"testing"

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
			name:  "DEL is escaped",
			input: "a\x7fb",
			want:  `a\x7fb`,
		},
		{
			name:  "a C1 control is escaped",
			input: "a\u0085b",
			want:  `a\x85b`,
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
			want:  strings.Repeat("a", maxFieldValueLen) + fmt.Sprintf("…(truncated, %d runes)", overCap),
		},
		{
			name:  "a multi-byte value past the cap is truncated on a rune boundary",
			input: strings.Repeat("日", overCap),
			want:  strings.Repeat("日", maxFieldValueLen) + fmt.Sprintf("…(truncated, %d runes)", overCap),
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
