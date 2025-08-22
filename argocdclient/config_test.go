package argocdclient

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// testConfigEnvVars sets up test environment variables
type testConfigEnvVars struct {
	server    string
	authToken string
	appName   string
	revision  string
}

// setEnvVars sets the environment variables, skipping empty values
func (vars testConfigEnvVars) setEnvVars(t *testing.T) {
	if vars.server != "" {
		if err := os.Setenv("ARGOCD_SERVER", vars.server); err != nil {
			t.Fatalf("Failed to set ARGOCD_SERVER: %v", err)
		}
	}
	if vars.authToken != "" {
		if err := os.Setenv("ARGOCD_AUTHTOKEN", vars.authToken); err != nil {
			t.Fatalf("Failed to set ARGOCD_AUTHTOKEN: %v", err)
		}
	}
	if vars.appName != "" {
		if err := os.Setenv("ARGOCD_APP_NAME", vars.appName); err != nil {
			t.Fatalf("Failed to set ARGOCD_APP_NAME: %v", err)
		}
	}
	if vars.revision != "" {
		if err := os.Setenv("ARGOCD_REVISION", vars.revision); err != nil {
			t.Fatalf("Failed to set ARGOCD_REVISION: %v", err)
		}
	}
}

// cleanup cleans up the environment variables
func (vars testConfigEnvVars) cleanup(t *testing.T) {
	if vars.server != "" {
		if err := os.Unsetenv("ARGOCD_SERVER"); err != nil {
			t.Errorf("Failed to unset ARGOCD_SERVER: %v", err)
		}
	}
	if vars.authToken != "" {
		if err := os.Unsetenv("ARGOCD_AUTHTOKEN"); err != nil {
			t.Errorf("Failed to unset ARGOCD_AUTHTOKEN: %v", err)
		}
	}
	if vars.appName != "" {
		if err := os.Unsetenv("ARGOCD_APP_NAME"); err != nil {
			t.Errorf("Failed to unset ARGOCD_APP_NAME: %v", err)
		}
	}
	if vars.revision != "" {
		if err := os.Unsetenv("ARGOCD_REVISION"); err != nil {
			t.Errorf("Failed to unset ARGOCD_REVISION: %v", err)
		}
	}
	viper.Reset()
}

// testLoadConfigMissing tests missing environment variable scenarios
func testLoadConfigMissing(t *testing.T, vars testConfigEnvVars, missingVar string) {
	viper.Reset()
	vars.setEnvVars(t)
	defer vars.cleanup(t)

	config, err := LoadConfig()
	if err == nil {
		t.Fatalf("Expected error for missing %s", missingVar)
	}

	if config != nil {
		t.Error("Expected nil config for error case")
	}

	expectedError := missingVar + " is required"
	if err.Error() != "error validating config: "+expectedError {
		t.Errorf("Expected error '%s', got '%v'", expectedError, err)
	}
}

func TestConfig_Struct(t *testing.T) {
	config := Config{
		ServerUrl: "https://argocd.example.com",
		AuthToken: "test-token",
		AppName:   "test-app",
		Revision:  "main",
	}

	if config.ServerUrl != "https://argocd.example.com" {
		t.Errorf("Expected ServerUrl 'https://argocd.example.com', got '%s'", config.ServerUrl)
	}

	if config.AuthToken != "test-token" {
		t.Errorf("Expected AuthToken 'test-token', got '%s'", config.AuthToken)
	}
}

func TestLoadConfig_Success(t *testing.T) {
	// Clean up viper state
	viper.Reset()

	// Set environment variables
	if err := os.Setenv("ARGOCD_SERVER", "https://argocd.example.com"); err != nil {
		t.Fatalf("Failed to set ARGOCD_SERVER: %v", err)
	}
	if err := os.Setenv("ARGOCD_AUTHTOKEN", "test-auth-token"); err != nil {
		t.Fatalf("Failed to set ARGOCD_AUTHTOKEN: %v", err)
	}

	defer func() {
		// Clean up environment variables
		if err := os.Unsetenv("ARGOCD_SERVER"); err != nil {
			t.Errorf("Failed to unset ARGOCD_SERVER: %v", err)
		}
		if err := os.Unsetenv("ARGOCD_AUTHTOKEN"); err != nil {
			t.Errorf("Failed to unset ARGOCD_AUTHTOKEN: %v", err)
		}
		viper.Reset()
	}()

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if config == nil {
		t.Fatal("Expected config, got nil")
	}

	if config.ServerUrl != "https://argocd.example.com" {
		t.Errorf("Expected ServerUrl 'https://argocd.example.com', got '%s'", config.ServerUrl)
	}

	if config.AuthToken != "test-auth-token" {
		t.Errorf("Expected AuthToken 'test-auth-token', got '%s'", config.AuthToken)
	}
}

func TestLoadConfig_MissingServerUrl(t *testing.T) {
	vars := testConfigEnvVars{
		authToken: "test-auth-token",
		appName:   "my-test-app",
		revision:  "v1.0.0",
	}
	testLoadConfigMissing(t, vars, "ARGOCD_SERVER")
}

func TestLoadConfig_MissingAuthToken(t *testing.T) {
	vars := testConfigEnvVars{
		server:   "https://argocd.example.com",
		appName:  "my-test-app",
		revision: "v1.0.0",
	}
	testLoadConfigMissing(t, vars, "ARGOCD_AUTHTOKEN")
}

func TestLoadConfig_MissingAppName(t *testing.T) {
	// Clean up viper state
	viper.Reset()

	// Set environment variables (AppName is now optional)
	if err := os.Setenv("ARGOCD_SERVER", "https://argocd.example.com"); err != nil {
		t.Fatalf("Failed to set ARGOCD_SERVER: %v", err)
	}
	if err := os.Setenv("ARGOCD_AUTHTOKEN", "test-auth-token"); err != nil {
		t.Fatalf("Failed to set ARGOCD_AUTHTOKEN: %v", err)
	}

	defer func() {
		// Clean up environment variables
		if err := os.Unsetenv("ARGOCD_SERVER"); err != nil {
			t.Errorf("Failed to unset ARGOCD_SERVER: %v", err)
		}
		if err := os.Unsetenv("ARGOCD_AUTHTOKEN"); err != nil {
			t.Errorf("Failed to unset ARGOCD_AUTHTOKEN: %v", err)
		}
		viper.Reset()
	}()

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("Expected no error when AppName is missing (now optional), got %v", err)
	}

	if config == nil {
		t.Fatal("Expected config, got nil")
	}

	if config.AppName != "" {
		t.Errorf("Expected empty AppName, got '%s'", config.AppName)
	}
}

func TestLoadConfig_MissingRevision(t *testing.T) {
	// Clean up viper state
	viper.Reset()

	// Set environment variables (Revision is now optional)
	if err := os.Setenv("ARGOCD_SERVER", "https://argocd.example.com"); err != nil {
		t.Fatalf("Failed to set ARGOCD_SERVER: %v", err)
	}
	if err := os.Setenv("ARGOCD_AUTHTOKEN", "test-auth-token"); err != nil {
		t.Fatalf("Failed to set ARGOCD_AUTHTOKEN: %v", err)
	}

	defer func() {
		// Clean up environment variables
		if err := os.Unsetenv("ARGOCD_SERVER"); err != nil {
			t.Errorf("Failed to unset ARGOCD_SERVER: %v", err)
		}
		if err := os.Unsetenv("ARGOCD_AUTHTOKEN"); err != nil {
			t.Errorf("Failed to unset ARGOCD_AUTHTOKEN: %v", err)
		}
		viper.Reset()
	}()

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("Expected no error when Revision is missing (now optional), got %v", err)
	}

	if config == nil {
		t.Fatal("Expected config, got nil")
	}

	if config.Revision != "" {
		t.Errorf("Expected empty Revision, got '%s'", config.Revision)
	}
}

func TestLoadConfig_AllMissing(t *testing.T) {
	// Clean up viper state
	viper.Reset()

	// Ensure no environment variables are set
	if err := os.Unsetenv("ARGOCD_SERVER"); err != nil {
		t.Errorf("Failed to unset ARGOCD_SERVER: %v", err)
	}
	if err := os.Unsetenv("ARGOCD_AUTHTOKEN"); err != nil {
		t.Errorf("Failed to unset ARGOCD_AUTHTOKEN: %v", err)
	}
	if err := os.Unsetenv("ARGOCD_APP_NAME"); err != nil {
		t.Errorf("Failed to unset ARGOCD_APP_NAME: %v", err)
	}
	if err := os.Unsetenv("ARGOCD_REVISION"); err != nil {
		t.Errorf("Failed to unset ARGOCD_REVISION: %v", err)
	}

	defer viper.Reset()

	config, err := LoadConfig()
	if err == nil {
		t.Fatal("Expected error for missing all required environment variables")
	}

	if config != nil {
		t.Error("Expected nil config for error case")
	}

	// Should fail on the first missing required field (ServerUrl)
	expectedError := "ARGOCD_SERVER is required"
	if err.Error() != "error validating config: "+expectedError {
		t.Errorf("Expected error '%s', got '%v'", expectedError, err)
	}
}

func TestValidateConfig_Success(t *testing.T) {
	config := &Config{
		ServerUrl: "https://argocd.example.com",
		AuthToken: "test-token",
		AppName:   "test-app",
		Revision:  "main",
	}

	err := validateConfig(config)
	if err != nil {
		t.Errorf("Expected no error for valid config, got %v", err)
	}
}

func TestValidateConfig_EmptyServerUrl(t *testing.T) {
	config := &Config{
		ServerUrl: "",
		AuthToken: "test-token",
		AppName:   "test-app",
		Revision:  "main",
	}

	err := validateConfig(config)
	if err == nil {
		t.Fatal("Expected error for empty ServerUrl")
	}

	expectedError := "ARGOCD_SERVER is required"
	if err.Error() != expectedError {
		t.Errorf("Expected error '%s', got '%v'", expectedError, err)
	}
}

func TestValidateConfig_EmptyAuthToken(t *testing.T) {
	config := &Config{
		ServerUrl: "https://argocd.example.com",
		AuthToken: "",
		AppName:   "test-app",
		Revision:  "main",
	}

	err := validateConfig(config)
	if err == nil {
		t.Fatal("Expected error for empty AuthToken")
	}

	expectedError := "ARGOCD_AUTHTOKEN is required"
	if err.Error() != expectedError {
		t.Errorf("Expected error '%s', got '%v'", expectedError, err)
	}
}

func TestValidateConfig_EmptyAppName(t *testing.T) {
	config := &Config{
		ServerUrl: "https://argocd.example.com",
		AuthToken: "test-token",
		AppName:   "",
		Revision:  "main",
	}

	err := validateConfig(config)
	if err != nil {
		t.Errorf("Expected no error for empty AppName (now optional), got %v", err)
	}
}

func TestValidateConfig_EmptyRevision(t *testing.T) {
	config := &Config{
		ServerUrl: "https://argocd.example.com",
		AuthToken: "test-token",
		AppName:   "test-app",
		Revision:  "",
	}

	err := validateConfig(config)
	if err != nil {
		t.Errorf("Expected no error for empty Revision (now optional), got %v", err)
	}
}

// Test with different environment variable combinations
func TestLoadConfig_PartialEnvironmentVariables(t *testing.T) {
	testCases := []struct {
		name          string
		envVars       map[string]string
		expectedError string
		shouldSucceed bool
	}{
		{
			name: "Complete config",
			envVars: map[string]string{
				"ARGOCD_SERVER":    "https://argocd.example.com",
				"ARGOCD_AUTHTOKEN": "test-auth-token",
				"ARGOCD_APP_NAME":  "my-test-app",
				"ARGOCD_REVISION":  "v1.0.0",
			},
			shouldSucceed: true,
		},
		{
			name: "Missing server",
			envVars: map[string]string{
				"ARGOCD_AUTHTOKEN": "test-auth-token",
				"ARGOCD_APP_NAME":  "my-test-app",
				"ARGOCD_REVISION":  "v1.0.0",
			},
			expectedError: "ARGOCD_SERVER is required",
			shouldSucceed: false,
		},
		{
			name: "Missing token",
			envVars: map[string]string{
				"ARGOCD_SERVER":   "https://argocd.example.com",
				"ARGOCD_APP_NAME": "my-test-app",
				"ARGOCD_REVISION": "v1.0.0",
			},
			expectedError: "ARGOCD_AUTHTOKEN is required",
			shouldSucceed: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Clean up viper state
			viper.Reset()

			// Clean up any existing environment variables
			_ = os.Unsetenv("ARGOCD_SERVER")
			_ = os.Unsetenv("ARGOCD_AUTHTOKEN")
			_ = os.Unsetenv("ARGOCD_APP_NAME")
			_ = os.Unsetenv("ARGOCD_REVISION")

			// Set test environment variables
			for key, value := range tc.envVars {
				if err := os.Setenv(key, value); err != nil {
					t.Fatalf("Failed to set %s: %v", key, err)
				}
			}

			defer func() {
				// Clean up
				for key := range tc.envVars {
					_ = os.Unsetenv(key)
				}
				viper.Reset()
			}()

			config, err := LoadConfig()

			if tc.shouldSucceed {
				if err != nil {
					t.Fatalf("Expected no error, got %v", err)
				}
				if config == nil {
					t.Fatal("Expected config, got nil")
				}
			} else {
				if err == nil {
					t.Fatal("Expected error, got nil")
				}
				if config != nil {
					t.Error("Expected nil config for error case")
				}
				if !strings.Contains(err.Error(), tc.expectedError) {
					t.Errorf("Expected error containing '%s', got %v", tc.expectedError, err)
				}
			}
		})
	}
}

func TestLoadConfig_WithOptionalFields(t *testing.T) {
	// Clean up viper state
	viper.Reset()

	// Set only required environment variables
	if err := os.Setenv("ARGOCD_SERVER", "https://argocd.example.com"); err != nil {
		t.Fatalf("Failed to set ARGOCD_SERVER: %v", err)
	}
	if err := os.Setenv("ARGOCD_AUTHTOKEN", "test-auth-token"); err != nil {
		t.Fatalf("Failed to set ARGOCD_AUTHTOKEN: %v", err)
	}
	if err := os.Setenv("ARGOCD_APP_NAME", "my-app"); err != nil {
		t.Fatalf("Failed to set ARGOCD_APP_NAME: %v", err)
	}
	if err := os.Setenv("ARGOCD_REVISION", "main"); err != nil {
		t.Fatalf("Failed to set ARGOCD_REVISION: %v", err)
	}

	defer func() {
		// Clean up environment variables
		_ = os.Unsetenv("ARGOCD_SERVER")
		_ = os.Unsetenv("ARGOCD_AUTHTOKEN")
		_ = os.Unsetenv("ARGOCD_APP_NAME")
		_ = os.Unsetenv("ARGOCD_REVISION")
		viper.Reset()
	}()

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if config == nil {
		t.Fatal("Expected config, got nil")
	}

	if config.ServerUrl != "https://argocd.example.com" {
		t.Errorf("Expected ServerUrl 'https://argocd.example.com', got '%s'", config.ServerUrl)
	}

	if config.AuthToken != "test-auth-token" {
		t.Errorf("Expected AuthToken 'test-auth-token', got '%s'", config.AuthToken)
	}

	if config.AppName != "my-app" {
		t.Errorf("Expected AppName 'my-app', got '%s'", config.AppName)
	}

	if config.Revision != "main" {
		t.Errorf("Expected Revision 'main', got '%s'", config.Revision)
	}
}

// validateTestResult is a helper function to reduce nesting complexity
func validateTestResult(t *testing.T, tc struct {
	name          string
	envVars       map[string]string
	expectedError string
	shouldSucceed bool
}, config *Config, err error) {
	if tc.shouldSucceed {
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if config == nil {
			t.Fatal("Expected config, got nil")
		}
		return
	}

	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if config != nil {
		t.Error("Expected nil config for error case")
	}
	if !strings.Contains(err.Error(), tc.expectedError) {
		t.Errorf("Expected error to contain '%s', got '%v'", tc.expectedError, err)
	}
}

// Benchmark test for LoadConfig
func BenchmarkLoadConfig(b *testing.B) {
	// Set up environment variables
	if err := os.Setenv("ARGOCD_SERVER", "https://argocd.example.com"); err != nil {
		b.Fatalf("Failed to set ARGOCD_SERVER: %v", err)
	}
	if err := os.Setenv("ARGOCD_AUTHTOKEN", "test-auth-token"); err != nil {
		b.Fatalf("Failed to set ARGOCD_AUTHTOKEN: %v", err)
	}
	if err := os.Setenv("ARGOCD_APP_NAME", "my-test-app"); err != nil {
		b.Fatalf("Failed to set ARGOCD_APP_NAME: %v", err)
	}
	if err := os.Setenv("ARGOCD_REVISION", "v1.0.0"); err != nil {
		b.Fatalf("Failed to set ARGOCD_REVISION: %v", err)
	}

	defer func() {
		if err := os.Unsetenv("ARGOCD_SERVER"); err != nil {
			b.Errorf("Failed to unset ARGOCD_SERVER: %v", err)
		}
		if err := os.Unsetenv("ARGOCD_AUTHTOKEN"); err != nil {
			b.Errorf("Failed to unset ARGOCD_AUTHTOKEN: %v", err)
		}
		if err := os.Unsetenv("ARGOCD_APP_NAME"); err != nil {
			b.Errorf("Failed to unset ARGOCD_APP_NAME: %v", err)
		}
		if err := os.Unsetenv("ARGOCD_REVISION"); err != nil {
			b.Errorf("Failed to unset ARGOCD_REVISION: %v", err)
		}
	}()

	b.ResetTimer()
	for range b.N {
		viper.Reset()
		_, err := LoadConfig()
		if err != nil {
			b.Fatalf("Unexpected error: %v", err)
		}
	}
}
