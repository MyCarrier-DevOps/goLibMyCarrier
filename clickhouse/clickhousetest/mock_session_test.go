package clickhousetest

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// The QueryFormat/InsertFormat mocks exist because clickhouse-go v2.48.0 added
// both to driver.Conn. Each supports the package's two-tier convention: an
// explicit Func override takes precedence, otherwise the configured
// reader/error fields are returned. Both tiers are covered here so an inverted
// override guard cannot pass unnoticed.

func TestMockConn_QueryFormat_ReturnsConfiguredFields(t *testing.T) {
	want := io.NopCloser(strings.NewReader("col\n1\n"))
	wantErr := errors.New("query format failed")
	conn := &MockConn{QueryFormatReader: want, QueryFormatErr: wantErr}

	got, err := conn.QueryFormat(context.Background(), "CSV", "SELECT 1")

	if got != want {
		t.Errorf("expected the configured reader to be returned, got %v", got)
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("expected the configured error, got %v", err)
	}
}

func TestMockConn_QueryFormat_FuncOverridesFields(t *testing.T) {
	fromFunc := io.NopCloser(strings.NewReader("from func"))
	var gotFormat, gotQuery string
	var gotArgs []any

	conn := &MockConn{
		// Fields are set too: the override must win over both of them.
		QueryFormatReader: io.NopCloser(strings.NewReader("from field")),
		QueryFormatErr:    errors.New("from field"),
		QueryFormatFunc: func(_ context.Context, format, query string, args ...any) (io.ReadCloser, error) {
			gotFormat, gotQuery, gotArgs = format, query, args
			return fromFunc, nil
		},
	}

	got, err := conn.QueryFormat(context.Background(), "JSONEachRow", "SELECT 2", 7)
	if err != nil {
		t.Fatalf("override returned nil error, got %v", err)
	}
	if got != fromFunc {
		t.Error("expected the override's reader, got the field value")
	}
	if gotFormat != "JSONEachRow" || gotQuery != "SELECT 2" {
		t.Errorf("override received (%q, %q), want (JSONEachRow, SELECT 2)", gotFormat, gotQuery)
	}
	if len(gotArgs) != 1 || gotArgs[0] != 7 {
		t.Errorf("override received args %v, want [7]", gotArgs)
	}
}

func TestMockConn_InsertFormat_ReturnsConfiguredError(t *testing.T) {
	wantErr := errors.New("insert format failed")
	conn := &MockConn{InsertFormatErr: wantErr}

	err := conn.InsertFormat(context.Background(), "CSV", "INSERT INTO t", strings.NewReader("1\n"))

	if !errors.Is(err, wantErr) {
		t.Errorf("expected the configured error, got %v", err)
	}
}

func TestMockConn_InsertFormat_FuncOverridesFields(t *testing.T) {
	var gotFormat, gotQuery, gotData string

	conn := &MockConn{
		// Field is set too: the override must win over it.
		InsertFormatErr: errors.New("from field"),
		InsertFormatFunc: func(_ context.Context, format, query string, data io.Reader) error {
			b, _ := io.ReadAll(data)
			gotFormat, gotQuery, gotData = format, query, string(b)
			return nil
		},
	}

	if err := conn.InsertFormat(
		context.Background(), "CSV", "INSERT INTO t", strings.NewReader("42\n"),
	); err != nil {
		t.Fatalf("override returned nil error, got %v", err)
	}
	if gotFormat != "CSV" || gotQuery != "INSERT INTO t" || gotData != "42\n" {
		t.Errorf("override received (%q, %q, %q), want (CSV, INSERT INTO t, \"42\\n\")",
			gotFormat, gotQuery, gotData)
	}
}
