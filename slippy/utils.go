package slippy

import "strings"

// pluralize returns a simple pluralized form of a word.
// Used for generating JSON column names from aggregate step names.
func pluralize(word string) string {
	if word == "" {
		return ""
	}

	// Handle common irregular plurals
	irregulars := map[string]string{
		"unit_test": "unit_tests",
		"build":     "builds",
		"test":      "tests",
		"deploy":    "deploys",
	}

	if plural, ok := irregulars[word]; ok {
		return plural
	}

	// Simple pluralization rules
	if strings.HasSuffix(word, "s") ||
		strings.HasSuffix(word, "x") ||
		strings.HasSuffix(word, "z") ||
		strings.HasSuffix(word, "ch") ||
		strings.HasSuffix(word, "sh") {
		return word + "es"
	}

	if strings.HasSuffix(word, "y") && len(word) > 1 {
		// Check if vowel before y
		vowels := "aeiou"
		if !strings.ContainsRune(vowels, rune(word[len(word)-2])) {
			return word[:len(word)-1] + "ies"
		}
	}

	return word + "s"
}
