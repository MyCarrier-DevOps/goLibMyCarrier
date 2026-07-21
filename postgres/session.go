package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultConnectRetryIntervals are the backoff intervals used when the initial
// connect/ping fails with a transient error. Exposed as a var so tests can
// shorten it.
var DefaultConnectRetryIntervals = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

// PostgresSession owns a pgx connection pool. Construct it with
// NewPostgresSession and release it with Close. The pool is safe for concurrent
// use; callers run queries via Pool().
type PostgresSession struct {
	pool *pgxpool.Pool
}

// buildDSN renders a libpq-style connection URL from cfg. Username and password
// are percent-encoded so special characters survive.
func buildDSN(cfg *PostgresConfig) string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.PgUsername, cfg.PgPassword),
		Host:   net.JoinHostPort(cfg.PgHostname, cfg.PgPort),
		Path:   "/" + cfg.PgDatabase,
	}
	q := url.Values{}
	q.Set("sslmode", cfg.PgSSLMode)
	u.RawQuery = q.Encode()
	return u.String()
}

// NewPostgresSession validates cfg, builds a pgx pool with the resolved pool
// settings, and verifies connectivity with a ping (retrying transient
// failures). It returns an error if the config is invalid or the pool cannot
// be established.
func NewPostgresSession(ctx context.Context, cfg *PostgresConfig) (*PostgresSession, error) {
	if err := PostgresValidateConfig(cfg); err != nil {
		return nil, err
	}

	poolCfg, err := pgxpool.ParseConfig(buildDSN(cfg))
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}
	maxConns, minConns, lifetime := ResolvePoolSettings(cfg)
	poolCfg.MaxConns = maxConns
	poolCfg.MinConns = minConns
	poolCfg.MaxConnLifetime = lifetime

	var pool *pgxpool.Pool
	connect := func() error {
		p, perr := pgxpool.NewWithConfig(ctx, poolCfg)
		if perr != nil {
			return fmt.Errorf("create postgres pool: %w", perr)
		}
		if perr := p.Ping(ctx); perr != nil {
			p.Close()
			return fmt.Errorf("ping postgres: %w", perr)
		}
		pool = p
		return nil
	}

	if err := retryConnect(ctx, connect); err != nil {
		return nil, err
	}
	return &PostgresSession{pool: pool}, nil
}

// Pool returns the underlying pgx pool for running queries.
func (s *PostgresSession) Pool() *pgxpool.Pool {
	return s.pool
}

// Ping verifies the pool can reach the database.
func (s *PostgresSession) Ping(ctx context.Context) error {
	if s.pool == nil {
		return errors.New("postgres pool is not established")
	}
	return s.pool.Ping(ctx)
}

// Close releases the pool. Safe to call on a zero/failed session.
func (s *PostgresSession) Close() error {
	if s.pool != nil {
		s.pool.Close()
	}
	return nil
}

// retryConnect runs op, retrying transient failures with backoff. Permanent
// failures (context cancellation, server-side pg errors like auth/config) fail
// immediately.
func retryConnect(ctx context.Context, op func() error) error {
	err := op()
	if err == nil {
		return nil
	}
	if !isTransientConnectError(err) {
		return err
	}
	for i, interval := range DefaultConnectRetryIntervals {
		select {
		case <-ctx.Done():
			return fmt.Errorf("postgres connect cancelled after %d retries: %w", i, ctx.Err())
		case <-time.After(interval):
			err = op()
			if err == nil {
				return nil
			}
			if !isTransientConnectError(err) {
				return err
			}
		}
	}
	return fmt.Errorf("postgres connect failed after %d retries: %w", len(DefaultConnectRetryIntervals), err)
}

// isTransientConnectError reports whether err is worth retrying. Context
// cancellation is never retried; a server-side *pgconn.PgError means the server
// responded (auth/config/database error) and won't resolve on retry. Everything
// else (dial failures, timeouts, EOF) is treated as transient.
func isTransientConnectError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// A server-side pg error means the server responded (auth/config/database
	// error) and won't resolve on retry; anything else is transient.
	var pgErr *pgconn.PgError
	return !errors.As(err, &pgErr)
}
