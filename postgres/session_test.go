package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestBuildDSN(t *testing.T) {
	cfg := &PostgresConfig{
		PgHostname: "db.example.com",
		PgUsername: "slippy_write",
		PgPassword: "p@ss word", // special chars must be percent-encoded
		PgDatabase: "ci",
		PgPort:     "5432",
		PgSSLMode:  "require",
	}
	dsn := buildDSN(cfg)
	assert.Contains(t, dsn, "postgres://")
	assert.Contains(t, dsn, "db.example.com:5432")
	assert.Contains(t, dsn, "/ci")
	assert.Contains(t, dsn, "sslmode=require")
	assert.Contains(t, dsn, "slippy_write")
	assert.Contains(t, dsn, "p%40ss%20word")
	assert.NotContains(t, dsn, "p@ss word")
}

func TestIsTransientConnectError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context canceled", context.Canceled, false},
		{"deadline exceeded", context.DeadlineExceeded, false},
		{"server pg error (auth) is permanent", &pgconn.PgError{Code: "28P01", Message: "auth failed"}, false},
		{"generic network error is transient", errors.New("dial tcp: connection refused"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isTransientConnectError(tt.err))
		})
	}
}

func TestNewPostgresSession_InvalidConfig(t *testing.T) {
	_, err := NewPostgresSession(context.Background(), &PostgresConfig{PgHostname: ""})
	require.Error(t, err)
}

func TestNewPostgresSession_ConnectFails(t *testing.T) {
	// Point at a dead port with tiny retry intervals so the retry loop and the
	// final "failed after N retries" wrap are exercised quickly (no Docker).
	orig := DefaultConnectRetryIntervals
	DefaultConnectRetryIntervals = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { DefaultConnectRetryIntervals = orig })

	cfg := &PostgresConfig{
		PgHostname: "127.0.0.1",
		PgUsername: "u",
		PgPassword: "p",
		PgDatabase: "d",
		PgPort:     "1", // nothing listens here
		PgSSLMode:  "disable",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := NewPostgresSession(ctx, cfg)
	require.Error(t, err)
}

func TestPostgresSession_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: requires Docker")
	}
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("ci_test"),
		tcpostgres.WithUsername("slippy_write"),
		tcpostgres.WithPassword("secret"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	host, err := ctr.Host(ctx)
	require.NoError(t, err)
	port, err := ctr.MappedPort(ctx, "5432/tcp")
	require.NoError(t, err)

	cfg := &PostgresConfig{
		PgHostname: host,
		PgPort:     port.Port(),
		PgUsername: "slippy_write",
		PgPassword: "secret",
		PgDatabase: "ci_test",
		PgSSLMode:  "disable", // the test container has no TLS
	}

	sess, err := NewPostgresSession(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })

	require.NoError(t, sess.Ping(ctx))

	var got int
	require.NoError(t, sess.Pool().QueryRow(ctx, "SELECT 1").Scan(&got))
	assert.Equal(t, 1, got)
}
