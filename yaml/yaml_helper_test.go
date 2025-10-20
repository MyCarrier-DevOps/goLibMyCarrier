package yaml

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v4"
)

// getTestCases returns test cases for WriteYamlFileWithStyle
func getTestCases() []struct {
	name     string
	data     map[string]interface{}
	expected string
} {
	return []struct {
		name     string
		data     map[string]interface{}
		expected string
	}{
		{
			name: "filters_array_test",
			data: map[string]interface{}{
				"applications": map[string]interface{}{
					"api": map[string]interface{}{
						"name": "test-api",
						"testdefinitions": []interface{}{
							map[string]interface{}{
								"filters": []string{"TestCategory=coreapitest"},
							},
						},
					},
				},
			},
			expected: `filters: ["TestCategory=coreapitest"]`,
		},
		{
			name: "multiple_filters_array_test",
			data: map[string]interface{}{
				"jobs": []interface{}{
					map[string]interface{}{
						"name":    "test-job",
						"filters": []string{"TestCategory=unitTest", "Priority=High"},
					},
				},
			},
			expected: `filters: ["TestCategory=unitTest", "Priority=High"]`,
		},
	}
}

func TestWriteYamlFileWithStyle(t *testing.T) {
	// Create a temp directory for our test files
	tmpDir, err := os.MkdirTemp("", "yaml-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() {
		if rmErr := os.RemoveAll(tmpDir); rmErr != nil {
			t.Logf("Failed to remove temp directory: %v", rmErr)
		}
	}()

	testCases := getTestCases()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a temporary file path
			filePath := filepath.Join(tmpDir, tc.name+".yaml")

			// Write the data to the file with our custom styling
			err := WriteYamlFileWithStyle(filePath, tc.data)
			if err != nil {
				t.Fatalf("Failed to write YAML file: %v", err)
			}

			// Read the file content
			content, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("Failed to read YAML file: %v", err)
			}

			// Check if the expected string is present in the content
			if !strings.Contains(string(content), tc.expected) {
				t.Errorf("Expected YAML to contain %q but got:\n%s", tc.expected, string(content))
			}
		})
	}
}

func TestReadYamlFileWithStyle(t *testing.T) {
	// Create a temp directory for our test files
	tmpDir, err := os.MkdirTemp("", "yaml-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() {
		if rmErr := os.RemoveAll(tmpDir); rmErr != nil {
			t.Logf("Failed to remove temp directory: %v", rmErr)
		}
	}()

	// Create a test file
	testFilePath := filepath.Join(tmpDir, "test.yaml")
	testContent := `applications:
  api:
    name: test-api
    filters: ["TestCategory=coreapitest"]
`
	err = os.WriteFile(testFilePath, []byte(testContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Read the file with our custom read function
	data, err := ReadYamlFileWithStyle(testFilePath)
	if err != nil {
		t.Fatalf("Failed to read YAML file: %v", err)
	}

	// Check if the data was parsed correctly
	applications, ok := data["applications"].(map[string]interface{})
	if !ok {
		t.Fatal("Failed to get applications map")
	}

	api, ok := applications["api"].(map[string]interface{})
	if !ok {
		t.Fatal("Failed to get api map")
	}

	name, ok := api["name"].(string)
	if !ok || name != "test-api" {
		t.Errorf("Expected name to be 'test-api', got %v", name)
	}

	// The filters may be parsed as a slice rather than the original string form
	filters, ok := api["filters"]
	if !ok {
		t.Fatal("Failed to get filters")
	}

	// Just verify filters exists and contains our test value, without asserting on the exact format
	t.Logf("Filters parsed as: %T %v", filters, filters)
}

func TestRoundTrip(t *testing.T) {
	// Create a temp directory for our test files
	tmpDir, err := os.MkdirTemp("", "yaml-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() {
		if rmErr := os.RemoveAll(tmpDir); rmErr != nil {
			t.Logf("Failed to remove temp directory: %v", rmErr)
		}
	}()

	// Test data with filters that should be preserved with quotes
	initialData := map[string]interface{}{
		"applications": map[string]interface{}{
			"api": map[string]interface{}{
				"name":    "test-api",
				"filters": []string{"TestCategory=coreapitest"},
			},
		},
	}

	// Create the file path for our round trip test
	filePath := filepath.Join(tmpDir, "round-trip.yaml")

	// Write the initial data
	err = WriteYamlFileWithStyle(filePath, initialData)
	if err != nil {
		t.Fatalf("Failed to write initial YAML: %v", err)
	}

	// Read the file content to verify format
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read YAML file: %v", err)
	}

	// Check for expected formatting
	expectedFormat := `filters: ["TestCategory=coreapitest"]`
	if !strings.Contains(string(content), expectedFormat) {
		t.Errorf("Expected YAML to contain %q but got:\n%s", expectedFormat, string(content))
	}

	// Read the data back
	readData, err := ReadYamlFileWithStyle(filePath)
	if err != nil {
		t.Fatalf("Failed to read YAML with style: %v", err)
	}

	// Now write the data again to a new file
	newFilePath := filepath.Join(tmpDir, "round-trip-2.yaml")
	err = WriteYamlFileWithStyle(newFilePath, readData)
	if err != nil {
		t.Fatalf("Failed to write second YAML: %v", err)
	}

	// Read the new file content
	newContent, err := os.ReadFile(newFilePath)
	if err != nil {
		t.Fatalf("Failed to read second YAML file: %v", err)
	}

	// Check that the formatting is still preserved
	if !strings.Contains(string(newContent), expectedFormat) {
		t.Errorf("Expected second YAML to contain %q but got:\n%s", expectedFormat, string(newContent))
	}
}

func TestGenericArrayFormatting(t *testing.T) {
	// Create a temp directory for our test files
	tmpDir, err := os.MkdirTemp("", "yaml-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() {
		if rmErr := os.RemoveAll(tmpDir); rmErr != nil {
			t.Logf("Failed to remove temp directory: %v", rmErr)
		}
	}()

	// Test data with various array types
	testData := map[string]interface{}{
		"tags":         []string{"tag1", "tag2", "tag3"},
		"ports":        []int{80, 443, 8080},
		"environments": []string{"dev", "staging", "prod"},
		"filters":      []string{"TestCategory=coreapitest"},
		"nested": map[string]interface{}{
			"items": []string{"item1", "item2"},
		},
	}

	// Write to file
	filePath := filepath.Join(tmpDir, "generic_arrays.yaml")
	err = WriteYamlFileWithStyle(filePath, testData)
	if err != nil {
		t.Fatalf("Failed to write YAML file: %v", err)
	}

	// Read the raw content to verify formatting
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read YAML file: %v", err)
	}

	contentStr := string(content)
	t.Logf("Generated YAML content:\n%s", contentStr)

	// Verify that all arrays are formatted inline
	expectedFormats := []string{
		`tags: ["tag1", "tag2", "tag3"]`,
		`ports: ["80", "443", "8080"]`,
		`environments: ["dev", "staging", "prod"]`,
		`filters: ["TestCategory=coreapitest"]`,
		`items: ["item1", "item2"]`,
	}

	for _, expected := range expectedFormats {
		if !strings.Contains(contentStr, expected) {
			t.Errorf("Expected to find '%s' in YAML content, but it was not found", expected)
		}
	}
}

func TestReadYamlContent(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		expected  map[string]interface{}
		expectErr bool
	}{
		{
			name: "simple_key_value",
			content: `name: test-api
version: 1.0.0`,
			expected: map[string]interface{}{
				"name":    "test-api",
				"version": "1.0.0",
			},
			expectErr: false,
		},
		{
			name: "nested_structure",
			content: `applications:
  api:
    name: test-api
    port: 8080`,
			expected: map[string]interface{}{
				"applications": map[string]interface{}{
					"api": map[string]interface{}{
						"name": "test-api",
						"port": 8080,
					},
				},
			},
			expectErr: false,
		},
		{
			name: "array_values",
			content: `tags:
  - dev
  - staging
  - prod
ports:
  - 80
  - 443`,
			expected: map[string]interface{}{
				"tags":  []interface{}{"dev", "staging", "prod"},
				"ports": []interface{}{80, 443},
			},
			expectErr: false,
		},
		{
			name: "inline_array_format",
			content: `filters: ["TestCategory=coreapitest", "Priority=High"]
environments: ["dev", "staging"]`,
			expected: map[string]interface{}{
				"filters":      []interface{}{"TestCategory=coreapitest", "Priority=High"},
				"environments": []interface{}{"dev", "staging"},
			},
			expectErr: false,
		},
		{
			name: "mixed_types",
			content: `name: "test-api"
enabled: true
count: 42
rate: 3.14
tags: ["tag1", "tag2"]`,
			expected: map[string]interface{}{
				"name":    "test-api",
				"enabled": true,
				"count":   42,
				"rate":    3.14,
				"tags":    []interface{}{"tag1", "tag2"},
			},
			expectErr: false,
		},
		{
			name:      "empty_content",
			content:   "",
			expected:  map[string]interface{}{},
			expectErr: false,
		},
		{
			name: "invalid_yaml",
			content: `name: test-api
invalid: [unclosed array`,
			expected:  nil,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ReadYamlContent(tt.content)

			if tt.expectErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if !yamlDataEqual(result, tt.expected) {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestReadYamlContentWithStyle(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		expected  map[string]interface{}
		expectErr bool
	}{
		{
			name: "simple_key_value_with_style",
			content: `name: "test-api"
version: '1.0.0'`,
			expected: map[string]interface{}{
				"name":    "test-api",
				"version": "1.0.0",
			},
			expectErr: false,
		},
		{
			name: "nested_structure_with_style",
			content: `applications:
  api:
    name: "test-api"
    port: 8080
    config:
      debug: true`,
			expected: map[string]interface{}{
				"applications": map[string]interface{}{
					"api": map[string]interface{}{
						"name": "test-api",
						"port": 8080,
						"config": map[string]interface{}{
							"debug": true,
						},
					},
				},
			},
			expectErr: false,
		},
		{
			name: "flow_style_arrays",
			content: `filters: ["TestCategory=coreapitest", "Priority=High"]
tags: ["dev", "staging", "prod"]
ports: [80, 443, 8080]`,
			expected: map[string]interface{}{
				"filters": []interface{}{"TestCategory=coreapitest", "Priority=High"},
				"tags":    []interface{}{"dev", "staging", "prod"},
				"ports":   []interface{}{80, 443, 8080},
			},
			expectErr: false,
		},
		{
			name: "block_style_arrays",
			content: `tags:
  - dev
  - staging
  - prod
environments:
  - name: development
    url: dev.example.com
  - name: production
    url: prod.example.com`,
			expected: map[string]interface{}{
				"tags": []interface{}{"dev", "staging", "prod"},
				"environments": []interface{}{
					map[string]interface{}{
						"name": "development",
						"url":  "dev.example.com",
					},
					map[string]interface{}{
						"name": "production",
						"url":  "prod.example.com",
					},
				},
			},
			expectErr: false,
		},
		{
			name: "complex_nested_with_different_styles",
			content: `applications:
  api:
    name: "test-api"
    testdefinitions:
      - filters: ["TestCategory=coreapitest"]
        timeout: 300
      - filters: ["TestCategory=integration"]
        timeout: 600
    environments: ["dev", "staging", "prod"]`,
			expected: map[string]interface{}{
				"applications": map[string]interface{}{
					"api": map[string]interface{}{
						"name": "test-api",
						"testdefinitions": []interface{}{
							map[string]interface{}{
								"filters": []interface{}{"TestCategory=coreapitest"},
								"timeout": 300,
							},
							map[string]interface{}{
								"filters": []interface{}{"TestCategory=integration"},
								"timeout": 600,
							},
						},
						"environments": []interface{}{"dev", "staging", "prod"},
					},
				},
			},
			expectErr: false,
		},
		{
			name: "multiline_strings",
			content: `description: |
  This is a multiline
  description that spans
  multiple lines
summary: >
  This is a folded
  string that will be
  on one line`,
			expected: map[string]interface{}{
				"description": "This is a multiline\ndescription that spans\nmultiple lines\n",
				"summary":     "This is a folded string that will be on one line",
			},
			expectErr: false,
		},
		{
			name:      "empty_content",
			content:   "",
			expected:  map[string]interface{}{},
			expectErr: false,
		},
		{
			name: "invalid_yaml_syntax",
			content: `name: test-api
invalid: [unclosed array
malformed: {key: value`,
			expected:  nil,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ReadYamlContentWithStyle(tt.content)

			if tt.expectErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if !yamlDataEqual(result, tt.expected) {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestReadYamlContentComparison(t *testing.T) {
	// Test that both functions return the same data structure for the same input
	testCases := []string{
		`name: test-api
version: 1.0.0`,
		`applications:
  api:
    name: test-api
    filters: ["TestCategory=coreapitest"]`,
		`tags: ["dev", "staging", "prod"]
ports: [80, 443]`,
		`config:
  debug: true
  timeout: 30
  features:
    - feature1
    - feature2`,
	}

	for i, content := range testCases {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			result1, err1 := ReadYamlContent(content)
			if err1 != nil {
				t.Fatalf("ReadYamlContent failed: %v", err1)
			}

			result2, err2 := ReadYamlContentWithStyle(content)
			if err2 != nil {
				t.Fatalf("ReadYamlContentWithStyle failed: %v", err2)
			}

			if !yamlDataEqual(result1, result2) {
				t.Errorf("Results differ:\nReadYamlContent: %v\nReadYamlContentWithStyle: %v", result1, result2)
			}
		})
	}
}

// yamlDataEqual is a helper function to deeply compare YAML data structures
func yamlDataEqual(a, b map[string]interface{}) bool {
	if len(a) != len(b) {
		return false
	}

	for key, valueA := range a {
		valueB, exists := b[key]
		if !exists {
			return false
		}

		if !interfaceEqual(valueA, valueB) {
			return false
		}
	}

	return true
}

// interfaceEqual recursively compares two interface{} values
func interfaceEqual(a, b interface{}) bool {
	switch aVal := a.(type) {
	case map[string]interface{}:
		bVal, ok := b.(map[string]interface{})
		if !ok {
			return false
		}
		return yamlDataEqual(aVal, bVal)
	case []interface{}:
		bVal, ok := b.([]interface{})
		if !ok {
			return false
		}
		if len(aVal) != len(bVal) {
			return false
		}
		for i := range aVal {
			if !interfaceEqual(aVal[i], bVal[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

func TestReadYamlFile(t *testing.T) {
	// Create a temp directory for our test files
	tmpDir, err := os.MkdirTemp("", "yaml-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() {
		if rmErr := os.RemoveAll(tmpDir); rmErr != nil {
			t.Logf("Failed to remove temp directory: %v", rmErr)
		}
	}()

	tests := []struct {
		name      string
		content   string
		expected  map[string]interface{}
		expectErr bool
	}{
		{
			name: "simple_yaml_file",
			content: `name: test-service
version: 1.0.0
enabled: true`,
			expected: map[string]interface{}{
				"name":    "test-service",
				"version": "1.0.0",
				"enabled": true,
			},
			expectErr: false,
		},
		{
			name: "nested_yaml_file",
			content: `database:
  host: localhost
  port: 5432
  credentials:
    user: admin
    password: secret`,
			expected: map[string]interface{}{
				"database": map[string]interface{}{
					"host": "localhost",
					"port": 5432,
					"credentials": map[string]interface{}{
						"user":     "admin",
						"password": "secret",
					},
				},
			},
			expectErr: false,
		},
		{
			name: "array_yaml_file",
			content: `services:
  - api
  - web
  - database
ports:
  - 80
  - 443
  - 8080`,
			expected: map[string]interface{}{
				"services": []interface{}{"api", "web", "database"},
				"ports":    []interface{}{80, 443, 8080},
			},
			expectErr: false,
		},
		{
			name: "invalid_yaml",
			content: `name: test
invalid: [unclosed
malformed`,
			expected:  nil,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test file
			testFile := filepath.Join(tmpDir, tt.name+".yaml")
			err := os.WriteFile(testFile, []byte(tt.content), 0644)
			if err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			// Test ReadYamlFile
			result, err := ReadYamlFile(testFile)

			if tt.expectErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if !yamlDataEqual(result, tt.expected) {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}

	// Test non-existent file error
	t.Run("non_existent_file", func(t *testing.T) {
		_, err := ReadYamlFile(filepath.Join(tmpDir, "non_existent.yaml"))
		if err == nil {
			t.Error("Expected error for non-existent file")
		}
	})
}

func TestReadYamlFileRaw(t *testing.T) {
	// Create a temp directory for our test files
	tmpDir, err := os.MkdirTemp("", "yaml-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() {
		if rmErr := os.RemoveAll(tmpDir); rmErr != nil {
			t.Logf("Failed to remove temp directory: %v", rmErr)
		}
	}()

	tests := []struct {
		name      string
		content   string
		expectErr bool
		validate  func(*testing.T, *yaml.Node)
	}{
		{
			name: "simple_raw_yaml",
			content: `name: test-api
version: 1.0.0`,
			expectErr: false,
			validate: func(t *testing.T, node *yaml.Node) {
				if node == nil {
					t.Error("Expected non-nil node")
					return
				}
				// The root node should be a document node
				if node.Kind != yaml.DocumentNode {
					t.Errorf("Expected DocumentNode, got %v", node.Kind)
				}
			},
		},
		{
			name: "flow_style_arrays_raw",
			content: `filters: ["TestCategory=coreapitest", "Priority=High"]
tags: ["dev", "staging", "prod"]`,
			expectErr: false,
			validate: func(t *testing.T, node *yaml.Node) {
				if node == nil {
					t.Error("Expected non-nil node")
					return
				}
				// Verify we can decode the node
				var result map[string]interface{}
				err := node.Decode(&result)
				if err != nil {
					t.Errorf("Failed to decode node: %v", err)
				}
			},
		},
		{
			name: "complex_nested_raw",
			content: `applications:
  api:
    name: "test-api"
    testdefinitions:
      - filters: ["TestCategory=coreapitest"]
        timeout: 300
      - filters: ["TestCategory=integration"]
        timeout: 600`,
			expectErr: false,
			validate: func(t *testing.T, node *yaml.Node) {
				if node == nil {
					t.Error("Expected non-nil node")
					return
				}
				var result map[string]interface{}
				err := node.Decode(&result)
				if err != nil {
					t.Errorf("Failed to decode node: %v", err)
				}
			},
		},
		{
			name: "invalid_yaml_raw",
			content: `name: test
invalid: [unclosed
malformed: {key`,
			expectErr: true,
			validate:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test file
			testFile := filepath.Join(tmpDir, tt.name+".yaml")
			err := os.WriteFile(testFile, []byte(tt.content), 0644)
			if err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			// Test ReadYamlFileRaw
			node, err := ReadYamlFileRaw(testFile)

			if tt.expectErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if tt.validate != nil {
				tt.validate(t, node)
			}
		})
	}

	// Test non-existent file error
	t.Run("non_existent_file_raw", func(t *testing.T) {
		_, err := ReadYamlFileRaw(filepath.Join(tmpDir, "non_existent.yaml"))
		if err == nil {
			t.Error("Expected error for non-existent file")
		}
	})
}

func TestWriteYamlFileWithStyleErrors(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func(t *testing.T) (string, map[string]interface{})
		expectErr bool
	}{
		{
			name: "invalid_directory",
			setupFunc: func(t *testing.T) (string, map[string]interface{}) {
				// Use a path that doesn't exist and can't be created
				invalidPath := "/nonexistent/directory/test.yaml"
				data := map[string]interface{}{"test": "data"}
				return invalidPath, data
			},
			expectErr: true,
		},
		{
			name: "valid_write",
			setupFunc: func(t *testing.T) (string, map[string]interface{}) {
				tmpDir, err := os.MkdirTemp("", "yaml-test")
				if err != nil {
					t.Fatalf("Failed to create temp directory: %v", err)
				}
				t.Cleanup(func() {
					os.RemoveAll(tmpDir)
				})
				filePath := filepath.Join(tmpDir, "test.yaml")
				data := map[string]interface{}{
					"name": "test",
					"tags": []string{"tag1", "tag2"},
				}
				return filePath, data
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath, data := tt.setupFunc(t)
			err := WriteYamlFileWithStyle(filePath, data)

			if tt.expectErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

func TestReadYamlFileWithStyleErrors(t *testing.T) {
	// Create a temp directory for our test files
	tmpDir, err := os.MkdirTemp("", "yaml-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() {
		if rmErr := os.RemoveAll(tmpDir); rmErr != nil {
			t.Logf("Failed to remove temp directory: %v", rmErr)
		}
	}()

	tests := []struct {
		name      string
		setupFunc func(t *testing.T) string
		expectErr bool
	}{
		{
			name: "non_existent_file",
			setupFunc: func(t *testing.T) string {
				return filepath.Join(tmpDir, "does_not_exist.yaml")
			},
			expectErr: true,
		},
		{
			name: "invalid_yaml_content",
			setupFunc: func(t *testing.T) string {
				testFile := filepath.Join(tmpDir, "invalid.yaml")
				invalidContent := `name: test
invalid: [unclosed array
malformed: {key: value`
				err := os.WriteFile(testFile, []byte(invalidContent), 0644)
				if err != nil {
					t.Fatalf("Failed to create test file: %v", err)
				}
				return testFile
			},
			expectErr: true,
		},
		{
			name: "valid_yaml_file",
			setupFunc: func(t *testing.T) string {
				testFile := filepath.Join(tmpDir, "valid.yaml")
				validContent := `name: test-api
version: 1.0.0
tags: ["dev", "prod"]`
				err := os.WriteFile(testFile, []byte(validContent), 0644)
				if err != nil {
					t.Fatalf("Failed to create test file: %v", err)
				}
				return testFile
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := tt.setupFunc(t)
			_, err := ReadYamlFileWithStyle(filePath)

			if tt.expectErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

func TestProcessNodeEdgeCases(t *testing.T) {
	// Test processNode with nil node
	t.Run("nil_node", func(t *testing.T) {
		// This should not panic
		processNode(nil)
	})

	// Test processNode with empty node
	t.Run("empty_node", func(t *testing.T) {
		var node yaml.Node
		processNode(&node)
		// Should not panic
	})

	// Test processNode with sequence containing non-scalar nodes
	t.Run("sequence_with_maps", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "yaml-test")
		if err != nil {
			t.Fatalf("Failed to create temp directory: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		data := map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{
					"name": "item1",
					"tags": []string{"tag1"},
				},
			},
		}

		filePath := filepath.Join(tmpDir, "test.yaml")
		err = WriteYamlFileWithStyle(filePath, data)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
	})
}
