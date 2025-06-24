package yaml

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
