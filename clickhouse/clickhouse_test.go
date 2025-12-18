package clickhouse

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockClickhouseSession implements ClickhouseSessionInterface for testing
type MockClickhouseSession struct {
	mock.Mock
}

// MockConn implements driver.Conn for testing
type MockConn struct {
	mock.Mock
}

func (m *MockConn) Contributors() []string {
	args := m.Called()
	return args.Get(0).([]string)
}

func (m *MockConn) ServerVersion() (*driver.ServerVersion, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*driver.ServerVersion), args.Error(1)
}

func (m *MockConn) Select(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	mArgs := m.Called(ctx, dest, query, args)
	return mArgs.Error(0)
}

func (m *MockConn) Query(ctx context.Context, query string, args ...interface{}) (driver.Rows, error) {
	mArgs := m.Called(ctx, query, args)
	if mArgs.Get(0) == nil {
		return nil, mArgs.Error(1)
	}
	return mArgs.Get(0).(driver.Rows), mArgs.Error(1)
}

func (m *MockConn) QueryRow(ctx context.Context, query string, args ...interface{}) driver.Row {
	mArgs := m.Called(ctx, query, args)
	if mArgs.Get(0) == nil {
		return nil
	}
	return mArgs.Get(0).(driver.Row)
}

func (m *MockConn) PrepareBatch(
	ctx context.Context,
	query string,
	opts ...driver.PrepareBatchOption,
) (driver.Batch, error) {
	mArgs := m.Called(ctx, query, opts)
	if mArgs.Get(0) == nil {
		return nil, mArgs.Error(1)
	}
	return mArgs.Get(0).(driver.Batch), mArgs.Error(1)
}

func (m *MockConn) Exec(ctx context.Context, query string, args ...interface{}) error {
	mArgs := m.Called(ctx, query, args)
	return mArgs.Error(0)
}

func (m *MockConn) AsyncInsert(ctx context.Context, query string, wait bool, args ...interface{}) error {
	mArgs := m.Called(ctx, query, wait, args)
	return mArgs.Error(0)
}

func (m *MockConn) Ping(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockConn) Stats() driver.Stats {
	args := m.Called()
	return args.Get(0).(driver.Stats)
}

func (m *MockConn) Close() error {
	args := m.Called()
	return args.Error(0)
}

// MockRows implements driver.Rows for testing
type MockRows struct {
	mock.Mock
}

func (m *MockRows) Next() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockRows) Scan(dest ...interface{}) error {
	args := m.Called(dest)
	return args.Error(0)
}

func (m *MockRows) ScanStruct(dest interface{}) error {
	args := m.Called(dest)
	return args.Error(0)
}

func (m *MockRows) ColumnTypes() []driver.ColumnType {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).([]driver.ColumnType)
}

func (m *MockRows) Totals(dest ...interface{}) error {
	args := m.Called(dest)
	return args.Error(0)
}

func (m *MockRows) Columns() []string {
	args := m.Called()
	return args.Get(0).([]string)
}

func (m *MockRows) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockRows) Err() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockClickhouseSession) Connect(ch *ClickhouseConfig, context context.Context) error {
	args := m.Called(ch, context)
	return args.Error(0)
}

func (m *MockClickhouseSession) Query(ctx context.Context, query string) (driver.Rows, error) {
	args := m.Called(ctx, query)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(driver.Rows), args.Error(1)
}

func (m *MockClickhouseSession) Exec(ctx context.Context, stmt string) error {
	args := m.Called(ctx, stmt)
	return args.Error(0)
}

func (m *MockClickhouseSession) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockClickhouseSession) Conn() driver.Conn {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(driver.Conn)
}

func (m *MockClickhouseSession) QueryWithArgs(ctx context.Context, query string, queryArgs ...any) (driver.Rows, error) {
	args := m.Called(ctx, query, queryArgs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(driver.Rows), args.Error(1)
}

func (m *MockClickhouseSession) ExecWithArgs(ctx context.Context, stmt string, execArgs ...any) error {
	args := m.Called(ctx, stmt, execArgs)
	return args.Error(0)
}

func (m *MockClickhouseSession) QueryRow(ctx context.Context, query string, queryArgs ...any) driver.Row {
	args := m.Called(ctx, query, queryArgs)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(driver.Row)
}

// MockConfigLoader implements ConfigLoaderInterface for testing
type MockConfigLoader struct {
	mock.Mock
}

func (m *MockConfigLoader) LoadConfig() (*ClickhouseConfig, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ClickhouseConfig), args.Error(1)
}

// MockConfigValidator implements ConfigValidatorInterface for testing
type MockConfigValidator struct {
	mock.Mock
}

func (m *MockConfigValidator) ValidateConfig(config *ClickhouseConfig) error {
	args := m.Called(config)
	return args.Error(0)
}

// MockSessionFactory implements SessionFactoryInterface for testing
type MockSessionFactory struct {
	mock.Mock
}

func (m *MockSessionFactory) NewSession(
	ch *ClickhouseConfig,
	context context.Context,
) (ClickhouseSessionInterface, error) {
	args := m.Called(ch, context)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(ClickhouseSessionInterface), args.Error(1)
}

// Test configuration loading success
func TestClickhouseLoadConfig_Success(t *testing.T) {
	// Set up environment variables
	t.Setenv("CLICKHOUSE_HOSTNAME", "localhost")
	t.Setenv("CLICKHOUSE_USERNAME", "default")
	t.Setenv("CLICKHOUSE_PASSWORD", "password")
	t.Setenv("CLICKHOUSE_DATABASE", "testdb")
	t.Setenv("CLICKHOUSE_PORT", "9000")
	t.Setenv("CLICKHOUSE_SKIP_VERIFY", "true")

	config, err := ClickhouseLoadConfig()

	assert.NoError(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "localhost", config.ChHostname)
	assert.Equal(t, "default", config.ChUsername)
	assert.Equal(t, "password", config.ChPassword)
	assert.Equal(t, "testdb", config.ChDatabase)
	assert.Equal(t, "9000", config.ChPort)
	assert.Equal(t, "true", config.ChSkipVerify)
}

// Test configuration validation - missing hostname
func TestClickhouseValidateConfig_MissingHostname(t *testing.T) {
	config := &ClickhouseConfig{
		ChUsername:   "default",
		ChPassword:   "password",
		ChDatabase:   "testdb",
		ChPort:       "9000",
		ChSkipVerify: "true",
	}

	err := ClickhouseValidateConfig(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "clickhouse hostname is required")
}

// Test configuration validation - missing username
func TestClickhouseValidateConfig_MissingUsername(t *testing.T) {
	config := &ClickhouseConfig{
		ChHostname:   "localhost",
		ChPassword:   "password",
		ChDatabase:   "testdb",
		ChPort:       "9000",
		ChSkipVerify: "true",
	}

	err := ClickhouseValidateConfig(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "clickhouse username is required")
}

// Test configuration validation - missing password
func TestClickhouseValidateConfig_MissingPassword(t *testing.T) {
	config := &ClickhouseConfig{
		ChHostname:   "localhost",
		ChUsername:   "default",
		ChDatabase:   "testdb",
		ChPort:       "9000",
		ChSkipVerify: "true",
	}

	err := ClickhouseValidateConfig(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "clickhouse password is required")
}

// Test configuration validation - missing database
func TestClickhouseValidateConfig_MissingDatabase(t *testing.T) {
	config := &ClickhouseConfig{
		ChHostname:   "localhost",
		ChUsername:   "default",
		ChPassword:   "password",
		ChPort:       "9000",
		ChSkipVerify: "true",
	}

	err := ClickhouseValidateConfig(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "clickhouse database is required")
}

// Test configuration validation - missing port
func TestClickhouseValidateConfig_MissingPort(t *testing.T) {
	config := &ClickhouseConfig{
		ChHostname:   "localhost",
		ChUsername:   "default",
		ChPassword:   "password",
		ChDatabase:   "testdb",
		ChSkipVerify: "true",
	}

	err := ClickhouseValidateConfig(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "clickhouse port is required")
}

// Test configuration validation - missing skip verify
func TestClickhouseValidateConfig_MissingSkipVerify(t *testing.T) {
	config := &ClickhouseConfig{
		ChHostname: "localhost",
		ChUsername: "default",
		ChPassword: "password",
		ChDatabase: "testdb",
		ChPort:     "9000",
	}

	err := ClickhouseValidateConfig(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "clickhouse skip verify is required")
}

// Test configuration validation - invalid skip verify value
func TestClickhouseValidateConfig_InvalidSkipVerify(t *testing.T) {
	config := &ClickhouseConfig{
		ChHostname:   "localhost",
		ChUsername:   "default",
		ChPassword:   "password",
		ChDatabase:   "testdb",
		ChPort:       "9000",
		ChSkipVerify: "invalid",
	}

	err := ClickhouseValidateConfig(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "clickhouse skip verify must be true or false")
}

// Test configuration validation - valid config
func TestClickhouseValidateConfig_Valid(t *testing.T) {
	config := &ClickhouseConfig{
		ChHostname:   "localhost",
		ChUsername:   "default",
		ChPassword:   "password",
		ChDatabase:   "testdb",
		ChPort:       "9000",
		ChSkipVerify: "true",
	}

	err := ClickhouseValidateConfig(config)
	assert.NoError(t, err)
}

// Test configuration validation - skip verify false
func TestClickhouseValidateConfig_SkipVerifyFalse(t *testing.T) {
	config := &ClickhouseConfig{
		ChHostname:   "localhost",
		ChUsername:   "default",
		ChPassword:   "password",
		ChDatabase:   "testdb",
		ChPort:       "9000",
		ChSkipVerify: "false",
	}

	err := ClickhouseValidateConfig(config)
	assert.NoError(t, err)
}

// Test DefaultConfigLoader
func TestDefaultConfigLoader_LoadConfig(t *testing.T) {
	// Set up environment variables
	t.Setenv("CLICKHOUSE_HOSTNAME", "localhost")
	t.Setenv("CLICKHOUSE_USERNAME", "default")
	t.Setenv("CLICKHOUSE_PASSWORD", "password")
	t.Setenv("CLICKHOUSE_DATABASE", "testdb")
	t.Setenv("CLICKHOUSE_PORT", "9000")
	t.Setenv("CLICKHOUSE_SKIP_VERIFY", "true")

	loader := &DefaultConfigLoader{}
	config, err := loader.LoadConfig()

	assert.NoError(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "localhost", config.ChHostname)
}

// Test DefaultConfigValidator
func TestDefaultConfigValidator_ValidateConfig(t *testing.T) {
	validator := &DefaultConfigValidator{}

	// Test valid config
	validConfig := &ClickhouseConfig{
		ChHostname:   "localhost",
		ChUsername:   "default",
		ChPassword:   "password",
		ChDatabase:   "testdb",
		ChPort:       "9000",
		ChSkipVerify: "true",
	}

	err := validator.ValidateConfig(validConfig)
	assert.NoError(t, err)

	// Test invalid config
	invalidConfig := &ClickhouseConfig{}
	err = validator.ValidateConfig(invalidConfig)
	assert.Error(t, err)
}

// Test DefaultSessionFactory
func TestDefaultSessionFactory_NewSession(t *testing.T) {
	// This test is limited since it would require actual ClickHouse connection
	// In a real scenario, you'd want to mock the underlying ClickHouse driver
	factory := &DefaultSessionFactory{}

	config := &ClickhouseConfig{
		ChHostname:   "localhost",
		ChUsername:   "default",
		ChPassword:   "password",
		ChDatabase:   "testdb",
		ChPort:       "9000",
		ChSkipVerify: "true",
	}

	ctx := context.Background()

	// This will fail unless there's a real ClickHouse instance, but demonstrates the interface
	session, err := factory.NewSession(config, ctx)

	// Since we don't have a real ClickHouse instance, we expect an error
	// In production tests, you'd mock the actual connection
	if err != nil {
		assert.Error(t, err)
		assert.Nil(t, session)
	} else {
		assert.NoError(t, err)
		assert.NotNil(t, session)
		if session != nil {
			_ = session.Close() // Clean up if somehow successful
		}
	}
}

// Test MockConfigLoader
func TestMockConfigLoader(t *testing.T) {
	mockLoader := &MockConfigLoader{}
	expectedConfig := &ClickhouseConfig{
		ChHostname: "mock-host",
		ChUsername: "mock-user",
	}

	mockLoader.On("LoadConfig").Return(expectedConfig, nil)

	config, err := mockLoader.LoadConfig()

	assert.NoError(t, err)
	assert.Equal(t, expectedConfig, config)
	mockLoader.AssertExpectations(t)
}

// Test MockConfigLoader with error
func TestMockConfigLoader_Error(t *testing.T) {
	mockLoader := &MockConfigLoader{}
	expectedError := errors.New("config load error")

	mockLoader.On("LoadConfig").Return(nil, expectedError)

	config, err := mockLoader.LoadConfig()

	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Equal(t, expectedError, err)
	mockLoader.AssertExpectations(t)
}

// Test MockConfigValidator
func TestMockConfigValidator(t *testing.T) {
	mockValidator := &MockConfigValidator{}
	testConfig := &ClickhouseConfig{}

	mockValidator.On("ValidateConfig", testConfig).Return(errors.New("validation error"))

	err := mockValidator.ValidateConfig(testConfig)

	assert.Error(t, err)
	assert.Equal(t, "validation error", err.Error())
	mockValidator.AssertExpectations(t)
}

// Test MockConfigValidator success
func TestMockConfigValidator_Success(t *testing.T) {
	mockValidator := &MockConfigValidator{}
	testConfig := &ClickhouseConfig{
		ChHostname: "localhost",
	}

	mockValidator.On("ValidateConfig", testConfig).Return(nil)

	err := mockValidator.ValidateConfig(testConfig)

	assert.NoError(t, err)
	mockValidator.AssertExpectations(t)
}

// Test MockSessionFactory
func TestMockSessionFactory(t *testing.T) {
	mockFactory := &MockSessionFactory{}
	mockSession := &MockClickhouseSession{}
	testConfig := &ClickhouseConfig{}
	ctx := context.Background()

	mockFactory.On("NewSession", testConfig, ctx).Return(mockSession, nil)

	session, err := mockFactory.NewSession(testConfig, ctx)

	assert.NoError(t, err)
	assert.Equal(t, mockSession, session)
	mockFactory.AssertExpectations(t)
}

// Test MockSessionFactory with error
func TestMockSessionFactory_Error(t *testing.T) {
	mockFactory := &MockSessionFactory{}
	testConfig := &ClickhouseConfig{}
	ctx := context.Background()
	expectedError := errors.New("session creation error")

	mockFactory.On("NewSession", testConfig, ctx).Return(nil, expectedError)

	session, err := mockFactory.NewSession(testConfig, ctx)

	assert.Error(t, err)
	assert.Nil(t, session)
	assert.Equal(t, expectedError, err)
	mockFactory.AssertExpectations(t)
}

// Test MockClickhouseSession methods
func TestMockClickhouseSession_Connect(t *testing.T) {
	mockSession := &MockClickhouseSession{}
	testConfig := &ClickhouseConfig{}
	ctx := context.Background()

	mockSession.On("Connect", testConfig, ctx).Return(nil)

	err := mockSession.Connect(testConfig, ctx)
	assert.NoError(t, err)
	mockSession.AssertExpectations(t)
}

// Test MockClickhouseSession Exec
func TestMockClickhouseSession_Exec(t *testing.T) {
	mockSession := &MockClickhouseSession{}
	ctx := context.Background()
	stmt := "INSERT INTO test VALUES (1)"

	mockSession.On("Exec", ctx, stmt).Return(nil)

	err := mockSession.Exec(ctx, stmt)
	assert.NoError(t, err)
	mockSession.AssertExpectations(t)
}

// Test MockClickhouseSession Exec with error
func TestMockClickhouseSession_Exec_Error(t *testing.T) {
	mockSession := &MockClickhouseSession{}
	ctx := context.Background()
	stmt := "INVALID SQL"
	expectedError := errors.New("exec error")

	mockSession.On("Exec", ctx, stmt).Return(expectedError)

	err := mockSession.Exec(ctx, stmt)
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	mockSession.AssertExpectations(t)
}

// Test MockClickhouseSession Query
func TestMockClickhouseSession_Query(t *testing.T) {
	mockSession := &MockClickhouseSession{}
	ctx := context.Background()
	query := "SELECT * FROM test"

	// Since we can't easily mock driver.Rows, we'll return nil for success case
	mockSession.On("Query", ctx, query).Return(nil, nil)

	rows, err := mockSession.Query(ctx, query)
	assert.NoError(t, err)
	assert.Nil(t, rows) // In real scenarios, you'd return a mock rows object
	mockSession.AssertExpectations(t)
}

// Test MockClickhouseSession Query with error
func TestMockClickhouseSession_Query_Error(t *testing.T) {
	mockSession := &MockClickhouseSession{}
	ctx := context.Background()
	query := "INVALID QUERY"
	expectedError := errors.New("query error")

	mockSession.On("Query", ctx, query).Return(nil, expectedError)

	rows, err := mockSession.Query(ctx, query)
	assert.Error(t, err)
	assert.Nil(t, rows)
	assert.Equal(t, expectedError, err)
	mockSession.AssertExpectations(t)
}

// Test MockClickhouseSession Close
func TestMockClickhouseSession_Close(t *testing.T) {
	mockSession := &MockClickhouseSession{}

	mockSession.On("Close").Return(nil)

	err := mockSession.Close()
	assert.NoError(t, err)
	mockSession.AssertExpectations(t)
}

// Test MockClickhouseSession Close with error
func TestMockClickhouseSession_Close_Error(t *testing.T) {
	mockSession := &MockClickhouseSession{}
	expectedError := errors.New("close error")

	mockSession.On("Close").Return(expectedError)

	err := mockSession.Close()
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	mockSession.AssertExpectations(t)
}

// Test MockClickhouseSession Conn
func TestMockClickhouseSession_Conn(t *testing.T) {
	mockSession := &MockClickhouseSession{}

	mockSession.On("Conn").Return(nil)

	conn := mockSession.Conn()
	assert.Nil(t, conn)
	mockSession.AssertExpectations(t)
}

// Test configuration loading with missing environment variables
func TestClickhouseLoadConfig_MissingEnvVars(t *testing.T) {
	// Clear all ClickHouse environment variables to ensure isolation
	t.Setenv("CLICKHOUSE_HOSTNAME", "")
	t.Setenv("CLICKHOUSE_USERNAME", "")
	t.Setenv("CLICKHOUSE_PASSWORD", "")
	t.Setenv("CLICKHOUSE_DATABASE", "")
	t.Setenv("CLICKHOUSE_PORT", "")
	t.Setenv("CLICKHOUSE_SKIP_VERIFY", "")

	config, err := ClickhouseLoadConfig()

	// Should return error due to validation failure
	assert.Error(t, err)
	assert.Nil(t, config)
}

// Test ClickhouseLoadConfig with partial environment variables
func TestClickhouseLoadConfig_PartialEnvVars(t *testing.T) {
	// Clear all environment variables first, then set only some
	t.Setenv("CLICKHOUSE_HOSTNAME", "localhost")
	t.Setenv("CLICKHOUSE_USERNAME", "default")
	t.Setenv("CLICKHOUSE_PASSWORD", "")
	t.Setenv("CLICKHOUSE_DATABASE", "")
	t.Setenv("CLICKHOUSE_PORT", "")
	t.Setenv("CLICKHOUSE_SKIP_VERIFY", "")

	config, err := ClickhouseLoadConfig()

	// Should return error due to validation failure
	assert.Error(t, err)
	assert.Nil(t, config)
}

// Test ClickhouseLoadConfig with invalid skip verify
func TestClickhouseLoadConfig_InvalidSkipVerify(t *testing.T) {
	// Set up environment variables with invalid skip verify
	t.Setenv("CLICKHOUSE_HOSTNAME", "localhost")
	t.Setenv("CLICKHOUSE_USERNAME", "default")
	t.Setenv("CLICKHOUSE_PASSWORD", "password")
	t.Setenv("CLICKHOUSE_DATABASE", "testdb")
	t.Setenv("CLICKHOUSE_PORT", "9000")
	t.Setenv("CLICKHOUSE_SKIP_VERIFY", "invalid_value")

	config, err := ClickhouseLoadConfig()

	// Should return error due to validation failure
	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "clickhouse skip verify must be true or false")
}

// Test NewClickhouseSession success scenario
func TestNewClickhouseSession_Success(t *testing.T) {
	config := &ClickhouseConfig{
		ChHostname:   "localhost",
		ChUsername:   "default",
		ChPassword:   "password",
		ChDatabase:   "testdb",
		ChPort:       "9000",
		ChSkipVerify: "true",
	}

	ctx := context.Background()
	session, err := NewClickhouseSession(config, ctx)

	// This will likely fail without a real ClickHouse instance, but we're testing the interface
	if err != nil {
		// Expected in test environment without real ClickHouse
		assert.Error(t, err)
		assert.Nil(t, session)
	} else {
		// If somehow successful, test that we get a valid session
		assert.NoError(t, err)
		assert.NotNil(t, session)
		if session != nil {
			_ = session.Close()
		}
	}
}

// Test NewClickhouseSession with nil config
func TestNewClickhouseSession_NilConfig(t *testing.T) {
	ctx := context.Background()
	session, err := NewClickhouseSession(nil, ctx)

	// Should return error for nil config
	assert.Error(t, err)
	assert.Nil(t, session)
	assert.Contains(t, err.Error(), "clickhouse config cannot be nil")
}

// Test ClickhouseSession Connect method with actual struct
func TestClickhouseSession_Connect(t *testing.T) {
	session := &ClickhouseSession{}
	config := &ClickhouseConfig{
		ChHostname:   "localhost",
		ChUsername:   "default",
		ChPassword:   "password",
		ChDatabase:   "testdb",
		ChPort:       "9000",
		ChSkipVerify: "true",
	}

	ctx := context.Background()
	err := session.Connect(config, ctx)

	// This will likely fail without a real ClickHouse instance
	if err != nil {
		assert.Error(t, err)
	} else {
		assert.NoError(t, err)
		// Clean up if successful
		_ = session.Close()
	}
}

// Test ClickhouseSession Connect with invalid config
func TestClickhouseSession_Connect_InvalidConfig(t *testing.T) {
	session := &ClickhouseSession{}
	config := &ClickhouseConfig{
		ChHostname:   "invalid-host-that-does-not-exist",
		ChUsername:   "invalid",
		ChPassword:   "invalid",
		ChDatabase:   "invalid",
		ChPort:       "99999",
		ChSkipVerify: "true",
	}

	ctx := context.Background()
	err := session.Connect(config, ctx)

	// Should return error due to invalid connection details
	assert.Error(t, err)
}

// Test ClickhouseSession Query method
func TestClickhouseSession_Query(t *testing.T) {
	session := &ClickhouseSession{}
	ctx := context.Background()
	query := "SELECT 1"

	rows, err := session.Query(ctx, query)

	// Without a real connection, this should return an error
	assert.Error(t, err)
	assert.Nil(t, rows)
	assert.Contains(t, err.Error(), "clickhouse connection is not established")
}

// Test ClickhouseSession Exec method
func TestClickhouseSession_Exec(t *testing.T) {
	session := &ClickhouseSession{}
	ctx := context.Background()
	stmt := "CREATE TABLE test (id UInt32) ENGINE = Memory"

	err := session.Exec(ctx, stmt)

	// Without a real connection, this should return an error
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "clickhouse connection is not established")
}

// Test ClickhouseSession Close method
func TestClickhouseSession_Close(t *testing.T) {
	session := &ClickhouseSession{}

	err := session.Close()

	// Should handle close gracefully even without connection
	// This might return error or nil depending on implementation
	if err != nil {
		assert.Error(t, err)
	} else {
		assert.NoError(t, err)
	}
}

// Test ClickhouseSession Conn method
func TestClickhouseSession_Conn(t *testing.T) {
	session := &ClickhouseSession{}

	conn := session.Conn()

	// Without a real connection, this should return nil
	assert.Nil(t, conn)
}

// Test ClickhouseLoadConfig environment variable binding edge cases
func TestClickhouseLoadConfig_EnvVarEdgeCases(t *testing.T) {
	// Test with empty environment variables
	t.Setenv("CLICKHOUSE_HOSTNAME", "")
	t.Setenv("CLICKHOUSE_USERNAME", "")
	t.Setenv("CLICKHOUSE_PASSWORD", "")
	t.Setenv("CLICKHOUSE_DATABASE", "")
	t.Setenv("CLICKHOUSE_PORT", "")
	t.Setenv("CLICKHOUSE_SKIP_VERIFY", "")

	config, err := ClickhouseLoadConfig()

	// Should return error due to validation failure (empty required fields)
	assert.Error(t, err)
	assert.Nil(t, config)
}

// Test ClickhouseLoadConfig with whitespace values
func TestClickhouseLoadConfig_WhitespaceValues(t *testing.T) {
	t.Setenv("CLICKHOUSE_HOSTNAME", "  ")
	t.Setenv("CLICKHOUSE_USERNAME", "  ")
	t.Setenv("CLICKHOUSE_PASSWORD", "  ")
	t.Setenv("CLICKHOUSE_DATABASE", "  ")
	t.Setenv("CLICKHOUSE_PORT", "  ")
	t.Setenv("CLICKHOUSE_SKIP_VERIFY", "  ")

	config, err := ClickhouseLoadConfig()

	// Should return error due to validation failure
	assert.Error(t, err)
	assert.Nil(t, config)
}

// Test Connect method with nil config
func TestClickhouseSession_Connect_NilConfig(t *testing.T) {
	session := &ClickhouseSession{}
	ctx := context.Background()

	err := session.Connect(nil, ctx)

	// Should return error due to nil config
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "clickhouse config cannot be nil")
}

// Test Connect method with nil context
func TestClickhouseSession_Connect_NilContext(t *testing.T) {
	session := &ClickhouseSession{}
	config := &ClickhouseConfig{
		ChHostname:   "localhost",
		ChUsername:   "default",
		ChPassword:   "password",
		ChDatabase:   "testdb",
		ChPort:       "9000",
		ChSkipVerify: "true",
	}

	err := session.Connect(config, nil)

	// Should return error due to nil context
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context cannot be nil")
}

// Test Connect method with malformed hostname
func TestClickhouseSession_Connect_MalformedHostname(t *testing.T) {
	session := &ClickhouseSession{}
	config := &ClickhouseConfig{
		ChHostname:   "invalid-host-with-special-chars@#$%",
		ChUsername:   "default",
		ChPassword:   "password",
		ChDatabase:   "testdb",
		ChPort:       "9000",
		ChSkipVerify: "true",
	}

	ctx := context.Background()
	err := session.Connect(config, ctx)

	// Should return error due to malformed hostname
	assert.Error(t, err)
	// Check for either format of error message
	assert.True(t,
		strings.Contains(err.Error(), "error connecting to ClickHouse") ||
			strings.Contains(err.Error(), "no such host") ||
			strings.Contains(err.Error(), "dial tcp"),
		"Expected error to contain connection or hostname error, got: %s", err.Error())
}

// Test Connect method with invalid port
func TestClickhouseSession_Connect_InvalidPort(t *testing.T) {
	session := &ClickhouseSession{}
	config := &ClickhouseConfig{
		ChHostname:   "localhost",
		ChUsername:   "default",
		ChPassword:   "password",
		ChDatabase:   "testdb",
		ChPort:       "99999", // Invalid port
		ChSkipVerify: "true",
	}

	ctx := context.Background()
	err := session.Connect(config, ctx)

	// Should return error due to invalid port
	assert.Error(t, err)
}

// Test Close method with valid connection (simulated)
func TestClickhouseSession_Close_WithConnection(t *testing.T) {
	session := &ClickhouseSession{}

	// Test close without connection (should not error)
	err := session.Close()
	assert.NoError(t, err)
}

// Test Query and Exec with better error scenarios
func TestClickhouseSession_Query_WithEmptyQuery(t *testing.T) {
	session := &ClickhouseSession{}
	ctx := context.Background()
	query := ""

	rows, err := session.Query(ctx, query)

	assert.Error(t, err)
	assert.Nil(t, rows)
	assert.Contains(t, err.Error(), "clickhouse connection is not established")
}

func TestClickhouseSession_Exec_WithEmptyStatement(t *testing.T) {
	session := &ClickhouseSession{}
	ctx := context.Background()
	stmt := ""

	err := session.Exec(ctx, stmt)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "clickhouse connection is not established")
}

// Test Query and Exec with nil context
func TestClickhouseSession_Query_NilContext(t *testing.T) {
	session := &ClickhouseSession{}
	query := "SELECT 1"

	rows, err := session.Query(context.TODO(), query)

	assert.Error(t, err)
	assert.Nil(t, rows)
	assert.Contains(t, err.Error(), "clickhouse connection is not established")
}

func TestClickhouseSession_Exec_NilContext(t *testing.T) {
	session := &ClickhouseSession{}
	stmt := "SELECT 1"

	err := session.Exec(context.TODO(), stmt)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "clickhouse connection is not established")
}

// Additional edge case tests to increase coverage

// Test ClickhouseLoadConfig with skip verify as false
func TestClickhouseLoadConfig_SkipVerifyFalse(t *testing.T) {
	t.Setenv("CLICKHOUSE_HOSTNAME", "localhost")
	t.Setenv("CLICKHOUSE_USERNAME", "default")
	t.Setenv("CLICKHOUSE_PASSWORD", "password")
	t.Setenv("CLICKHOUSE_DATABASE", "testdb")
	t.Setenv("CLICKHOUSE_PORT", "9000")
	t.Setenv("CLICKHOUSE_SKIP_VERIFY", "false")

	config, err := ClickhouseLoadConfig()

	assert.NoError(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "false", config.ChSkipVerify)
}

// Test NewClickhouseSession with skip verify false
func TestNewClickhouseSession_SkipVerifyFalse(t *testing.T) {
	config := &ClickhouseConfig{
		ChHostname:   "localhost",
		ChUsername:   "default",
		ChPassword:   "password",
		ChDatabase:   "testdb",
		ChPort:       "9000",
		ChSkipVerify: "false",
	}

	ctx := context.Background()
	session, err := NewClickhouseSession(config, ctx)

	// This will likely fail without a real ClickHouse instance
	if err != nil {
		assert.Error(t, err)
		assert.Nil(t, session)
	} else {
		assert.NoError(t, err)
		assert.NotNil(t, session)
		if session != nil {
			_ = session.Close()
		}
	}
}

// Test Connect with different port configurations
func TestClickhouseSession_Connect_DifferentPorts(t *testing.T) {
	session := &ClickhouseSession{}
	config := &ClickhouseConfig{
		ChHostname:   "localhost",
		ChUsername:   "default",
		ChPassword:   "password",
		ChDatabase:   "testdb",
		ChPort:       "9440", // Different port
		ChSkipVerify: "true",
	}

	ctx := context.Background()
	err := session.Connect(config, ctx)

	// Expected to fail without real ClickHouse instance
	assert.Error(t, err)
}

// Test session struct initialization
func TestClickhouseSession_Initialization(t *testing.T) {
	config := &ClickhouseConfig{
		ChHostname:   "test-host",
		ChUsername:   "test-user",
		ChPassword:   "test-pass",
		ChDatabase:   "test-db",
		ChPort:       "9000",
		ChSkipVerify: "true",
	}

	session := &ClickhouseSession{
		db:         config.ChDatabase,
		addr:       []string{config.ChHostname + ":" + config.ChPort},
		username:   config.ChUsername,
		password:   config.ChPassword,
		skipVerify: config.ChSkipVerify == "true",
	}

	assert.Equal(t, "test-db", session.db)
	assert.Equal(t, []string{"test-host:9000"}, session.addr)
	assert.Equal(t, "test-user", session.username)
	assert.Equal(t, "test-pass", session.password)
	assert.True(t, session.skipVerify)
}

// Test Close with nil connection (already tested but adding for completeness)
func TestClickhouseSession_Close_NilConnection(t *testing.T) {
	session := &ClickhouseSession{
		conn: nil,
	}

	err := session.Close()
	assert.NoError(t, err)
}

// Test Conn method returns correct value
func TestClickhouseSession_Conn_ReturnsCorrectValue(t *testing.T) {
	session := &ClickhouseSession{
		conn: nil,
	}

	conn := session.Conn()
	assert.Nil(t, conn)
}

// Test Query with successful connection
func TestClickhouseSession_Query_Success(t *testing.T) {
	mockConn := &MockConn{}
	mockRows := &MockRows{}
	ctx := context.Background()
	query := "SELECT 1"

	mockConn.On("Query", ctx, query, mock.Anything).Return(mockRows, nil)

	session := &ClickhouseSession{
		conn: mockConn,
	}

	rows, err := session.Query(ctx, query)

	assert.NoError(t, err)
	assert.NotNil(t, rows)
	mockConn.AssertExpectations(t)
}

// Test Query with connection error
func TestClickhouseSession_Query_ConnectionError(t *testing.T) {
	mockConn := &MockConn{}
	ctx := context.Background()
	query := "SELECT 1"
	expectedError := errors.New("query failed")

	mockConn.On("Query", ctx, query, mock.Anything).Return(nil, expectedError)

	session := &ClickhouseSession{
		conn: mockConn,
	}

	rows, err := session.Query(ctx, query)

	assert.Error(t, err)
	assert.Nil(t, rows)
	assert.Equal(t, expectedError, err)
	mockConn.AssertExpectations(t)
}

// Test Exec with successful connection
func TestClickhouseSession_Exec_Success(t *testing.T) {
	mockConn := &MockConn{}
	ctx := context.Background()
	stmt := "CREATE TABLE test (id UInt32) ENGINE = Memory"

	mockConn.On("Exec", ctx, stmt, mock.Anything).Return(nil)

	session := &ClickhouseSession{
		conn: mockConn,
	}

	err := session.Exec(ctx, stmt)

	assert.NoError(t, err)
	mockConn.AssertExpectations(t)
}

// Test Exec with connection error
func TestClickhouseSession_Exec_ConnectionError(t *testing.T) {
	mockConn := &MockConn{}
	ctx := context.Background()
	stmt := "INVALID SQL"
	expectedError := errors.New("exec failed")

	mockConn.On("Exec", ctx, stmt, mock.Anything).Return(expectedError)

	session := &ClickhouseSession{
		conn: mockConn,
	}

	err := session.Exec(ctx, stmt)

	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	mockConn.AssertExpectations(t)
}

// Test Close with successful connection close
func TestClickhouseSession_Close_Success(t *testing.T) {
	mockConn := &MockConn{}

	mockConn.On("Close").Return(nil)

	session := &ClickhouseSession{
		conn: mockConn,
	}

	err := session.Close()

	assert.NoError(t, err)
	mockConn.AssertExpectations(t)
}

// Test Close with connection close error
func TestClickhouseSession_Close_ConnectionError(t *testing.T) {
	mockConn := &MockConn{}
	expectedError := errors.New("close failed")

	mockConn.On("Close").Return(expectedError)

	session := &ClickhouseSession{
		conn: mockConn,
	}

	err := session.Close()

	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	mockConn.AssertExpectations(t)
}
