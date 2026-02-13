package yaml

import (
	"fmt"
	"os"

	"go.yaml.in/yaml/v4"
)

// WriteYamlFileWithStyle writes YAML data to a file while preserving specific formatting styles.
// It handles special cases like filters arrays, ensuring they are formatted as ["value"] instead of multiline lists.
func WriteYamlFileWithStyle(filePath string, data map[string]interface{}) error {
	// Convert to YAML node structure
	var node yaml.Node
	err := node.Encode(data)
	if err != nil {
		return fmt.Errorf("error encoding data to yaml node: %w", err)
	}

	// Process the node tree to handle specific style requirements
	processNode(&node)

	// Create file for writing
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("error creating file: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			// Silently ignore close errors in defer as the main operation has already succeeded/failed
			_ = closeErr
		}
	}()

	// Create encoder with 2-space indentation
	encoder := yaml.NewEncoder(file)
	encoder.SetIndent(2)

	// Encode the node directly to file with custom indentation
	err = encoder.Encode(&node)
	if err != nil {
		return fmt.Errorf("error encoding yaml to file: %w", err)
	}

	// Close the encoder
	err = encoder.Close()
	if err != nil {
		return fmt.Errorf("error closing encoder: %w", err)
	}

	return nil
}

// processNode recursively traverses the YAML node tree to apply custom styling
// Note: This function handles node styling while indentation (2-spaces) is managed by the encoder
func processNode(node *yaml.Node) {
	if node == nil {
		return
	}

	// Process all child nodes first
	for i := range node.Content {
		processNode(node.Content[i])
	}

	// Apply flow style only to sequences where ALL items are scalars.
	// Sequences containing maps or nested sequences stay in block style for readability.
	if node.Kind == yaml.SequenceNode && len(node.Content) > 0 {
		allScalar := true
		for _, child := range node.Content {
			if child.Kind != yaml.ScalarNode {
				allScalar = false
				break
			}
		}

		if allScalar {
			// Set the sequence to use flow style (inline array format)
			node.Style = yaml.FlowStyle

			// Ensure all sequence items are set as scalar nodes with double quotes
			for j := range node.Content {
				node.Content[j].Style = yaml.DoubleQuotedStyle
			}
		}
	}
}

// ReadYamlFile reads and parses a YAML file at the specified path.
// It unmarshals the YAML content into a map of string to interface.
// Returns the parsed YAML data or an error if reading or parsing fails.
func ReadYamlFile(filePath string) (map[string]interface{}, error) {
	// read the file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}
	// unmarshal the yaml file
	var result map[string]interface{}
	err = yaml.Unmarshal(data, &result)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling yaml file: %w", err)
	}
	// return the result
	return result, nil
}

// ReadYamlFileWithStyle reads a YAML file and preserves the original formatting styles.
// Returns a map of parsed data and any error encountered during reading or parsing.
func ReadYamlFileWithStyle(filePath string) (map[string]interface{}, error) {
	// Read the file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	// Use a yaml.Node to parse the YAML while preserving style
	var rootNode yaml.Node
	err = yaml.Unmarshal(data, &rootNode)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling yaml file: %w", err)
	}

	// Convert the node tree to a map
	var result map[string]interface{}
	err = rootNode.Decode(&result)
	if err != nil {
		return nil, fmt.Errorf("error decoding yaml node: %w", err)
	}

	return result, nil
}

// ReadYamlFileRaw reads a YAML file and returns the raw node structure,
// which preserves the original formatting styles.
func ReadYamlFileRaw(filePath string) (*yaml.Node, error) {
	// Read the file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	// Use a yaml.Node to parse the YAML while preserving style
	var rootNode yaml.Node
	err = yaml.Unmarshal(data, &rootNode)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling yaml file: %w", err)
	}

	return &rootNode, nil
}

// ReadYamlContent reads YAML content from a string and unmarshals it into a map.
func ReadYamlContent(content string) (map[string]interface{}, error) {
	// Unmarshal the YAML content
	var result map[string]interface{}
	err := yaml.Unmarshal([]byte(content), &result)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling yaml content: %w", err)
	}
	// Return the result
	return result, nil
}

// ReadYamlContentWithStyle reads YAML content from a string and preserves the original formatting styles.
func ReadYamlContentWithStyle(content string) (map[string]interface{}, error) {
	// Use a yaml.Node to parse the YAML while preserving style
	var rootNode yaml.Node
	err := yaml.Unmarshal([]byte(content), &rootNode)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling yaml content: %w", err)
	}

	// Convert the node tree to a map
	var result map[string]interface{}
	err = rootNode.Decode(&result)
	if err != nil {
		return nil, fmt.Errorf("error decoding yaml node: %w", err)
	}

	return result, nil
}
