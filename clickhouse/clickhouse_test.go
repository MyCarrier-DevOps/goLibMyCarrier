package clickhouse

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClickhouseLoadConfig(t *testing.T) {
	t.Setenv("CLICKHOUSE_HOSTNAME", "localhost")
	t.Setenv("CLICKHOUSE_USERNAME", "default")
	t.Setenv("CLICKHOUSE_PASSWORD", "password")
	t.Setenv("CLICKHOUSE_DATABASE", "default")
	t.Setenv("CLICKHOUSE_SKIP_VERIFY", "true")
	t.Setenv("CLICKHOUSE_PORT", "9000")

	config, err := ClickhouseLoadConfig()
	assert.NoError(t, err)
	assert.Equal(t, "localhost", config.ChHostname)
	assert.Equal(t, "default", config.ChUsername)
	assert.Equal(t, "password", config.ChPassword)
	assert.Equal(t, "default", config.ChDatabase)
	assert.Equal(t, "true", config.ChSkipVerify)
	assert.Equal(t, "9000", config.ChPort)
}

func TestClickhouseValidateConfig(t *testing.T) {
	validConfig := &ClickhouseConfig{
		ChHostname:   "localhost",
		ChUsername:   "default",
		ChPassword:   "password",
		ChDatabase:   "default",
		ChSkipVerify: "true",
		ChPort:       "9000",
	}

	err := ClickhouseValidateConfig(validConfig)
	assert.NoError(t, err)

	invalidConfig := &ClickhouseConfig{}
	err = ClickhouseValidateConfig(invalidConfig)
	assert.Error(t, err)
}
