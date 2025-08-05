# YAML Helper Module

A Go module that provides enhanced YAML file operations with custom formatting styles, particularly for handling arrays in YAML documents. Supports both file-based and string-based YAML operations.

## Features

- **Custom Array Formatting**: Automatically formats all arrays as inline/flow style (e.g., `["item1", "item2", "item3"]`)
- **Style Preservation**: Read YAML files while preserving original formatting styles
- **String Content Parsing**: Parse YAML directly from string content with `ReadYamlContent` and `ReadYamlContentWithStyle`
- **2-Space Indentation**: Consistent formatting with 2-space indentation
- **Multiple Read/Write Functions**: Various functions for different use cases (files, strings, raw nodes)
- **Comprehensive Error Handling**: Descriptive error messages with proper error wrapping

## Installation

```bash
go get github.com/MyCarrier-DevOps/goLibMyCarrier/yaml
```

## Functions

### WriteYamlFileWithStyle

Writes YAML data to a file with custom formatting. All arrays are automatically formatted as inline arrays.

```go
func WriteYamlFileWithStyle(filePath string, data map[string]interface{}) error
```

**Example:**
```go
data := map[string]interface{}{
    "environments": []string{"dev", "staging", "prod"},
    "ports": []int{80, 443, 8080},
    "config": map[string]interface{}{
        "tags": []string{"api", "backend"},
    },
}

err := WriteYamlFileWithStyle("config.yaml", data)
if err != nil {
    log.Fatal(err)
}
```

**Output YAML:**
```yaml
config:
  tags: ["api", "backend"]
environments: ["dev", "staging", "prod"]
ports: ["80", "443", "8080"]
```

### ReadYamlFile

Reads and parses a YAML file into a map.

```go
func ReadYamlFile(filePath string) (map[string]interface{}, error)
```

**Example:**
```go
data, err := ReadYamlFile("config.yaml")
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Loaded data: %+v\n", data)
```

### ReadYamlFileWithStyle

Reads a YAML file while preserving the original formatting styles.

```go
func ReadYamlFileWithStyle(filePath string) (map[string]interface{}, error)
```

### ReadYamlFileRaw

Returns the raw YAML node structure, preserving all original formatting.

```go
func ReadYamlFileRaw(filePath string) (*yaml.Node, error)
```

### ReadYamlContent

Reads YAML content from a string and unmarshals it into a map.

```go
func ReadYamlContent(content string) (map[string]interface{}, error)
```

**Example:**
```go
yamlContent := `
name: test-api
version: 1.0.0
tags: ["dev", "staging", "prod"]
config:
  debug: true
  timeout: 30
`

data, err := ReadYamlContent(yamlContent)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Parsed data: %+v\n", data)
```

### ReadYamlContentWithStyle

Reads YAML content from a string while preserving the original formatting styles.

```go
func ReadYamlContentWithStyle(content string) (map[string]interface{}, error)
```

**Example:**
```go
yamlContent := `
applications:
  api:
    name: "test-api"
    filters: ["TestCategory=coreapitest", "Priority=High"]
    environments: ["dev", "staging", "prod"]
description: |
  This is a multiline
  description that spans
  multiple lines
`

data, err := ReadYamlContentWithStyle(yamlContent)
if err != nil {
    log.Fatal(err)
}

// The function preserves styles while parsing to standard Go data structures
fmt.Printf("Parsed data: %+v\n", data)
```

## Array Formatting Behavior

The module automatically formats **all arrays** as inline/flow style, regardless of their key names or nesting level:

- ✅ `tags: ["tag1", "tag2", "tag3"]`
- ✅ `ports: ["80", "443", "8080"]`
- ✅ `environments: ["dev", "staging", "prod"]`
- ✅ Nested arrays in complex structures

This ensures consistent, compact representation of arrays throughout your YAML documents.

## Usage Examples

### Basic Write and Read

```go
package main

import (
    "fmt"
    "log"
    
    yaml "github.com/MyCarrier-DevOps/goLibMyCarrier/yaml"
)

func main() {
    // Data to write
    data := map[string]interface{}{
        "application": map[string]interface{}{
            "name": "my-app",
            "environments": []string{"dev", "staging", "prod"},
            "features": []string{"auth", "logging", "monitoring"},
        },
        "database": map[string]interface{}{
            "ports": []int{5432, 5433},
            "replicas": []string{"replica1", "replica2"},
        },
    }
    
    // Write with custom formatting
    if err := yaml.WriteYamlFileWithStyle("app-config.yaml", data); err != nil {
        log.Fatal(err)
    }
    
    // Read back
    loadedData, err := yaml.ReadYamlFile("app-config.yaml")
    if err != nil {
        log.Fatal(err)
    }
    
    fmt.Printf("Loaded: %+v\n", loadedData)
}
```

### Working with Complex Nested Structures

```go
data := map[string]interface{}{
    "services": []interface{}{
        map[string]interface{}{
            "name": "api-service",
            "ports": []int{8080, 8081},
            "tags": []string{"backend", "api"},
        },
        map[string]interface{}{
            "name": "frontend-service", 
            "ports": []int{3000},
            "tags": []string{"frontend", "react"},
        },
    },
    "global": map[string]interface{}{
        "allowed_origins": []string{"localhost", "example.com"},
    },
}

err := yaml.WriteYamlFileWithStyle("services.yaml", data)
```

**Resulting YAML:**
```yaml
global:
  allowed_origins: ["localhost", "example.com"]
services:
- name: api-service
  ports: ["8080", "8081"]
  tags: ["backend", "api"]
- name: frontend-service
  ports: ["3000"]
  tags: ["frontend", "react"]
```

### Working with YAML Content Strings

```go
package main

import (
    "fmt"
    "log"
    
    yaml "github.com/MyCarrier-DevOps/goLibMyCarrier/yaml"
)

func main() {
    // Parse YAML from string content
    yamlContent := `
applications:
  api:
    name: test-api
    filters: ["TestCategory=coreapitest", "Priority=High"]
    testdefinitions:
      - filters: ["TestCategory=unitTest"]
        timeout: 300
      - filters: ["TestCategory=integration"] 
        timeout: 600
    environments: ["dev", "staging", "prod"]
config:
  debug: true
  features:
    - feature1
    - feature2
description: |
  This is a multiline
  description that spans
  multiple lines
`

    // Use ReadYamlContent for basic parsing
    data, err := yaml.ReadYamlContent(yamlContent)
    if err != nil {
        log.Fatal(err)
    }
    
    // Use ReadYamlContentWithStyle to preserve formatting styles
    styledData, err := yaml.ReadYamlContentWithStyle(yamlContent)
    if err != nil {
        log.Fatal(err)
    }
    
    fmt.Printf("Basic parsing: %+v\n", data)
    fmt.Printf("Style-aware parsing: %+v\n", styledData)
    
    // Both functions return equivalent data structures
    // The WithStyle version preserves original formatting information internally
}

## Error Handling

All functions return descriptive errors with proper error wrapping:

```go
data, err := yaml.ReadYamlFile("nonexistent.yaml")
if err != nil {
    // Error will be wrapped and descriptive
    fmt.Printf("Error reading YAML: %v\n", err)
}
```

## Testing

The module includes comprehensive tests covering:
- **File Operations**: Write and read operations with style preservation
- **Content Parsing**: String-based YAML parsing with `ReadYamlContent` and `ReadYamlContentWithStyle`
- **Array Formatting**: Various data types (strings, integers, nested structures)
- **Style Preservation**: Flow-style arrays, block-style arrays, multiline strings
- **Nested Structures**: Complex real-world scenarios with mixed formatting styles
- **Round-trip Operations**: Write → read → verify consistency
- **Error Handling**: Invalid YAML syntax and edge cases
- **Data Consistency**: Ensuring both regular and style-aware functions return equivalent data

**Test Coverage Includes:**
- Simple key-value pairs
- Nested YAML structures
- Inline and block-style arrays
- Mixed data types (strings, booleans, integers, floats)
- Multiline strings (literal `|` and folded `>` styles)
- Complex nested structures with different formatting styles
- Empty content and malformed YAML handling
- Function comparison tests to ensure data consistency

Run tests:
```bash
go test -v ./...
```

Run specific test groups:
```bash
# Test content parsing functions
go test -v -run="TestReadYamlContent"

# Test all array formatting
go test -v -run="Array"

# Test style preservation
go test -v -run="Style"
```

## Dependencies

- `gopkg.in/yaml.v3` - YAML processing with advanced node manipulation

## License

This module is part of the goLibMyCarrier library and follows the same licensing terms.