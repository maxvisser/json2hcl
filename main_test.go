package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
)

// ConversionTest represents a single conversion test case
type ConversionTest struct {
	name       string
	inputFile  string
	outputFile string
	flags      []string
}

func normalizeHCL(input string) string {
	// Parse and sort HCL content for consistent comparison
	return sortHCLContent(strings.TrimSpace(input))
}

// HCLBlock represents a parsed HCL block
type HCLBlock struct {
	Type       string
	Labels     []string
	Attributes []HCLAttribute
	Blocks     []HCLBlock
	StartLine  int
}

// HCLAttribute represents a parsed HCL attribute
type HCLAttribute struct {
	Name  string
	Value string
	Line  int
}

// sortHCLContent parses HCL content and returns it sorted
func sortHCLContent(input string) string {
	blocks, attributes := parseHCLContent(input)
	
	// Sort top-level attributes
	sort.Slice(attributes, func(i, j int) bool {
		return attributes[i].Name < attributes[j].Name
	})
	
	// Sort top-level blocks
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].Type != blocks[j].Type {
			return getBlockTypePriority(blocks[i].Type) < getBlockTypePriority(blocks[j].Type)
		}
		// If same type, sort by labels
		for k := 0; k < len(blocks[i].Labels) && k < len(blocks[j].Labels); k++ {
			if blocks[i].Labels[k] != blocks[j].Labels[k] {
				return blocks[i].Labels[k] < blocks[j].Labels[k]
			}
		}
		return len(blocks[i].Labels) < len(blocks[j].Labels)
	})
	
	// Sort nested content recursively
	for i := range blocks {
		sortBlockContent(&blocks[i])
	}
	
	// Rebuild the content
	var result []string
	
	// Add sorted attributes first
	for _, attr := range attributes {
		result = append(result, attr.Name+" = "+attr.Value)
	}
	
	// Add sorted blocks
	for _, block := range blocks {
		result = append(result, renderBlock(block)...)
	}
	
	return strings.Join(result, "\n")
}

// getBlockTypePriority returns the priority order for block types
func getBlockTypePriority(blockType string) int {
	priorities := map[string]int{
		"terraform": 1,
		"provider":  2,
		"data":      3,
		"resource":  4,
		"locals":    5,
		"variable":  6,
		"output":    7,
		"module":    8,
	}
	if priority, exists := priorities[blockType]; exists {
		return priority
	}
	return 999 // Unknown types go last
}

// parseHCLContent parses HCL content into blocks and attributes
func parseHCLContent(input string) ([]HCLBlock, []HCLAttribute) {
	lines := strings.Split(input, "\n")
	var blocks []HCLBlock
	var attributes []HCLAttribute
	
	i := 0
	braceDepth := 0
	bracketDepth := 0
	
	for i < len(lines) {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			i++
			continue
		}
		
		// Track brace and bracket depth to avoid parsing inside arrays/objects
		braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
		bracketDepth += strings.Count(line, "[") - strings.Count(line, "]")
		
		// Only parse top-level content (not inside arrays or nested objects)
		if braceDepth == 0 && bracketDepth == 0 {
			// Check if this is a block declaration
			if isBlockDeclaration(line) {
				block, nextLine := parseBlock(lines, i)
				blocks = append(blocks, block)
				i = nextLine
				continue
			} else if strings.Contains(line, "=") {
				// This is an attribute - handle multi-line values
				attr, nextLine := parseAttribute(lines, i)
				if attr != nil {
					attributes = append(attributes, *attr)
				}
				i = nextLine
				continue
			}
		}
		
		i++
	}
	
	return blocks, attributes
}

// parseAttribute parses an attribute that may span multiple lines
func parseAttribute(lines []string, startIndex int) (*HCLAttribute, int) {
	line := strings.TrimSpace(lines[startIndex])
	
	// Find the equals sign
	eqIndex := strings.Index(line, "=")
	if eqIndex == -1 {
		return nil, startIndex + 1
	}
	
	name := strings.TrimSpace(line[:eqIndex])
	valuePart := strings.TrimSpace(line[eqIndex+1:])
	
	// Handle multi-line values (arrays, objects)
	braceCount := strings.Count(valuePart, "{") - strings.Count(valuePart, "}")
	bracketCount := strings.Count(valuePart, "[") - strings.Count(valuePart, "]")
	
	i := startIndex + 1
	var valueLines []string
	valueLines = append(valueLines, valuePart)
	
	// Continue reading lines until braces and brackets are balanced
	for (braceCount > 0 || bracketCount > 0) && i < len(lines) {
		nextLine := strings.TrimSpace(lines[i])
		if nextLine != "" {
			braceCount += strings.Count(nextLine, "{") - strings.Count(nextLine, "}")
			bracketCount += strings.Count(nextLine, "[") - strings.Count(nextLine, "]")
			valueLines = append(valueLines, nextLine)
		}
		i++
	}
	
	value := strings.Join(valueLines, " ")
	
	attr := &HCLAttribute{
		Name:  name,
		Value: value,
		Line:  startIndex,
	}
	
	return attr, i
}

// isBlockDeclaration checks if a line is a block declaration
func isBlockDeclaration(line string) bool {
	line = strings.TrimSpace(line)
	if strings.HasSuffix(line, "{") {
		// Remove the opening brace and check if it looks like a block
		withoutBrace := strings.TrimSpace(strings.TrimSuffix(line, "{"))
		parts := strings.Fields(withoutBrace)
		if len(parts) >= 1 {
			// Check if first part is a known block type or quoted string
			firstPart := parts[0]
			return isValidBlockType(firstPart) || strings.HasPrefix(firstPart, "\"")
		}
	}
	return false
}

// isValidBlockType checks if a string is a valid HCL block type
func isValidBlockType(blockType string) bool {
	validTypes := map[string]bool{
		"terraform": true, "provider": true, "resource": true, "data": true,
		"variable": true, "output": true, "locals": true, "module": true,
		"attribute": true, "global_secondary_index": true, "local_secondary_index": true,
	}
	return validTypes[blockType]
}

// parseBlock parses a block from the lines starting at the given index
func parseBlock(lines []string, startIndex int) (HCLBlock, int) {
	line := strings.TrimSpace(lines[startIndex])
	
	// Parse block declaration
	blockLine := strings.TrimSpace(strings.TrimSuffix(line, "{"))
	parts := parseBlockDeclaration(blockLine)
	
	block := HCLBlock{
		Type:      parts[0],
		Labels:    parts[1:],
		StartLine: startIndex,
	}
	
	// Parse block content
	braceCount := 1
	i := startIndex + 1
	contentStart := i
	
	for i < len(lines) && braceCount > 0 {
		line := strings.TrimSpace(lines[i])
		braceCount += strings.Count(line, "{") - strings.Count(line, "}")
		i++
	}
	
	// Parse the content between braces
	if i > contentStart {
		contentLines := lines[contentStart : i-1]
		content := strings.Join(contentLines, "\n")
		childBlocks, childAttributes := parseHCLContent(content)
		block.Blocks = childBlocks
		block.Attributes = childAttributes
	}
	
	return block, i
}

// parseBlockDeclaration parses a block declaration line into type and labels
func parseBlockDeclaration(line string) []string {
	var parts []string
	var current strings.Builder
	inQuotes := false
	
	for _, r := range line {
		switch r {
		case '"':
			inQuotes = !inQuotes
			current.WriteRune(r)
		case ' ', '\t':
			if !inQuotes && current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			} else if inQuotes {
				current.WriteRune(r)
			}
		default:
			current.WriteRune(r)
		}
	}
	
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	
	return parts
}

// sortBlockContent sorts the content within a block
func sortBlockContent(block *HCLBlock) {
	// Sort attributes within the block
	sort.Slice(block.Attributes, func(i, j int) bool {
		return block.Attributes[i].Name < block.Attributes[j].Name
	})
	
	// Sort nested blocks
	sort.Slice(block.Blocks, func(i, j int) bool {
		if block.Blocks[i].Type != block.Blocks[j].Type {
			return block.Blocks[i].Type < block.Blocks[j].Type
		}
		// If same type, sort by labels
		for k := 0; k < len(block.Blocks[i].Labels) && k < len(block.Blocks[j].Labels); k++ {
			if block.Blocks[i].Labels[k] != block.Blocks[j].Labels[k] {
				return block.Blocks[i].Labels[k] < block.Blocks[j].Labels[k]
			}
		}
		return len(block.Blocks[i].Labels) < len(block.Blocks[j].Labels)
	})
	
	// Recursively sort nested blocks
	for i := range block.Blocks {
		sortBlockContent(&block.Blocks[i])
	}
}

// renderBlock converts a block back to HCL text
func renderBlock(block HCLBlock) []string {
	var result []string
	
	// Build block declaration
	declaration := block.Type
	for _, label := range block.Labels {
		declaration += " " + label
	}
	declaration += " {"
	result = append(result, declaration)
	
	// Add attributes first
	for _, attr := range block.Attributes {
		result = append(result, "  "+attr.Name+" = "+attr.Value)
	}
	
	// Add nested blocks
	for _, childBlock := range block.Blocks {
		childLines := renderBlock(childBlock)
		for _, childLine := range childLines {
			result = append(result, "  "+childLine)
		}
	}
	
	result = append(result, "}")
	return result
}

// runConversionTest is a helper function that runs a conversion test
func runConversionTest(t *testing.T, test ConversionTest) {
	// Read the input file
	input, err := ioutil.ReadFile(test.inputFile)
	if err != nil {
		t.Fatalf("Failed to read input file %s: %v", test.inputFile, err)
	}

	// Read the expected output file
	expectedOutput, err := ioutil.ReadFile(test.outputFile)
	if err != nil {
		t.Fatalf("Failed to read expected output file %s: %v", test.outputFile, err)
	}

	// Prepare command arguments
	args := []string{"run", "main.go"}
	args = append(args, test.flags...)

	// Run the command
	cmd := exec.Command("go", args...)
	cmd.Stdin = bytes.NewReader(input)
	
	actualOutput, err := cmd.Output()
	if err != nil {
		t.Fatalf("Command failed: %v", err)
	}

	// Compare outputs (normalize whitespace)
	var expected, actual string
	
	if strings.HasSuffix(test.outputFile, ".tfvars") || strings.HasSuffix(test.outputFile, ".tf") {
		// For HCL output files, normalize HCL formatting and sorting
		expected = normalizeHCL(string(expectedOutput))
		actual = normalizeHCL(string(actualOutput))
	} else if strings.HasSuffix(test.outputFile, ".json") {
		// For JSON files, normalize JSON formatting
		var expectedJSON, actualJSON interface{}
		err = json.Unmarshal(expectedOutput, &expectedJSON)
		if err != nil {
			t.Fatalf("Failed to parse expected JSON: %v", err)
		}
		err = json.Unmarshal(actualOutput, &actualJSON)
		if err != nil {
			t.Fatalf("Failed to parse actual JSON: %v", err)
		}
		expectedBytes, _ := json.MarshalIndent(expectedJSON, "", "  ")
		actualBytes, _ := json.MarshalIndent(actualJSON, "", "  ")
		expected = string(expectedBytes)
		actual = string(actualBytes)
	} else {
		// For other files, just trim whitespace
		expected = strings.TrimSpace(string(expectedOutput))
		actual = strings.TrimSpace(string(actualOutput))
	}

	if expected != actual {
		t.Errorf("Output mismatch for %s:\nExpected:\n%s\n\nActual:\n%s", test.name, expected, actual)
	}
}

func TestConversions(t *testing.T) {
	tests := []ConversionTest{
		{
			name:       "JSON to HCL (infra)",
			inputFile:  "fixtures/infra.tf.json",
			outputFile: "fixtures/infra.tf",
			flags:      []string{}, // default behavior
		},
		{
			name:       "HCL to JSON (infra reverse)",
			inputFile:  "fixtures/infra.tf",
			outputFile: "fixtures/infra.tf.json",
			flags:      []string{"-reverse"},
		},
		{
			name:       "JSON to tfvars (simple)",
			inputFile:  "fixtures/simple.tfvars.json",
			outputFile: "fixtures/simple.tfvars",
			flags:      []string{},
		},
		{
			name:       "JSON to tfvars (complex)",
			inputFile:  "fixtures/complex.tfvars.json",
			outputFile: "fixtures/complex.tfvars",
			flags:      []string{},
		},
		{
			name:       "JSON to tfvars (edge cases)",
			inputFile:  "fixtures/edge-cases.tfvars.json",
			outputFile: "fixtures/edge-cases.tfvars",
			flags:      []string{},
		},
		{
			name:       "JSON to tfvars (route53)",
			inputFile:  "fixtures/route53.tfvars.json",
			outputFile: "fixtures/route53.tfvars",
			flags:      []string{},
		},
		{
			name:       "JSON to tfvars (empty object)",
			inputFile:  "fixtures/empty.tfvars.json",
			outputFile: "fixtures/empty.tfvars",
			flags:      []string{},
		},
		{
			name:       "JSON to tfvars (deeply nested)",
			inputFile:  "fixtures/deeply-nested.tfvars.json",
			outputFile: "fixtures/deeply-nested.tfvars",
			flags:      []string{},
		},
		{
			name:       "JSON to tfvars (large arrays)",
			inputFile:  "fixtures/large-array.tfvars.json",
			outputFile: "fixtures/large-array.tfvars",
			flags:      []string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runConversionTest(t, test)
		})
	}
}

func TestHCLFixturesValid(t *testing.T) {
	fixturesDir := "fixtures"
	
	// Find all HCL files (.tf and .tfvars)
	var hclFiles []string
	
	err := filepath.Walk(fixturesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		
		if !info.IsDir() && (strings.HasSuffix(path, ".tf") || strings.HasSuffix(path, ".tfvars")) {
			hclFiles = append(hclFiles, path)
		}
		
		return nil
	})
	
	if err != nil {
		t.Fatalf("Error walking fixtures directory: %v", err)
	}
	
	if len(hclFiles) == 0 {
		t.Skip("No HCL files found in fixtures directory")
	}
	
	parser := hclparse.NewParser()
	
	for _, filename := range hclFiles {
		t.Run(filename, func(t *testing.T) {
			content, err := os.ReadFile(filename)
			if err != nil {
				t.Fatalf("Failed to read file %s: %v", filename, err)
			}
			
			// Parse as HCL native syntax (both .tf and .tfvars use HCL syntax)
			_, diags := parser.ParseHCL(content, filename)
			
			if diags.HasErrors() {
				var errorMsgs []string
				for _, diag := range diags {
					if diag.Severity == hcl.DiagError {
						errorMsgs = append(errorMsgs, diag.Error())
					}
				}
				t.Errorf("HCL syntax errors in %s:\n%s", filename, strings.Join(errorMsgs, "\n"))
			}
		})
	}
}

// TestJSONFixturesHCLCompliance validates that JSON fixtures comply with HCL JSON specification
func TestJSONFixturesHCLCompliance(t *testing.T) {
	fixturesDir := "fixtures"
	
	// Find all JSON files
	var jsonFiles []string
	
	err := filepath.Walk(fixturesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		
		if !info.IsDir() && strings.HasSuffix(path, ".json") {
			jsonFiles = append(jsonFiles, path)
		}
		
		return nil
	})
	
	if err != nil {
		t.Fatalf("Error walking fixtures directory: %v", err)
	}
	
	if len(jsonFiles) == 0 {
		t.Skip("No JSON files found in fixtures directory")
	}
	
	for _, filename := range jsonFiles {
		t.Run(filename, func(t *testing.T) {
			validateJSONHCLCompliance(t, filename)
		})
	}
}

// validateJSONHCLCompliance performs comprehensive validation of a JSON file against HCL JSON spec
func validateJSONHCLCompliance(t *testing.T, filename string) {
	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("Failed to read file %s: %v", filename, err)
	}

	// 1. Basic JSON validity
	var jsonData interface{}
	err = json.Unmarshal(content, &jsonData)
	if err != nil {
		t.Errorf("Invalid JSON in %s: %v", filename, err)
		return
	}

	// 2. HCL JSON parser compatibility
	parser := hclparse.NewParser()
	file, diags := parser.ParseJSON(content, filename)
	if diags.HasErrors() {
		var errorMsgs []string
		for _, diag := range diags {
			if diag.Severity == hcl.DiagError {
				errorMsgs = append(errorMsgs, diag.Error())
			}
		}
		t.Errorf("HCL JSON parsing errors in %s:\n%s", filename, strings.Join(errorMsgs, "\n"))
		return
	}

	// 3. Validate structural elements according to HCL JSON spec
	validateHCLJSONStructure(t, filename, jsonData)

	// 4. Validate expressions can be evaluated
	validateHCLJSONExpressions(t, filename, file)

	// 5. Validate static analysis operations work
	validateHCLJSONStaticAnalysis(t, filename, file)
}

// validateHCLJSONStructure validates the JSON structure follows HCL JSON specification
func validateHCLJSONStructure(t *testing.T, filename string, data interface{}) {
	switch v := data.(type) {
	case map[string]interface{}:
		// Body represented as single JSON object - this is valid
		validateJSONObject(t, filename, v, "root")
	case []interface{}:
		// Body represented as array of objects
		for i, item := range v {
			if obj, ok := item.(map[string]interface{}); ok {
				validateJSONObject(t, filename, obj, fmt.Sprintf("root[%d]", i))
			} else {
				t.Errorf("Invalid HCL JSON structure in %s: array element %d is not an object", filename, i)
			}
		}
	default:
		t.Errorf("Invalid HCL JSON structure in %s: root must be object or array of objects", filename)
	}
}

// validateJSONObject validates a JSON object according to HCL JSON spec
func validateJSONObject(t *testing.T, filename string, obj map[string]interface{}, path string) {
	for key, value := range obj {
		// Check for special comment property
		if key == "//" {
			// Comment property - should be string
			if _, ok := value.(string); !ok {
				t.Errorf("Invalid comment property in %s at %s: '//' property should be string", filename, path)
			}
			continue
		}

		// Validate expressions recursively
		validateJSONExpression(t, filename, value, fmt.Sprintf("%s.%s", path, key))
	}
}

// validateJSONExpression validates JSON expressions according to HCL JSON spec
func validateJSONExpression(t *testing.T, filename string, expr interface{}, path string) {
	switch v := expr.(type) {
	case map[string]interface{}:
		// Object expression - validate all properties
		for key, value := range v {
			// Property names should be valid (not null when evaluated)
			if key == "" {
				t.Errorf("Empty property name in object expression at %s in %s", path, filename)
			}
			validateJSONExpression(t, filename, value, fmt.Sprintf("%s[%s]", path, key))
		}
	case []interface{}:
		// Array expression - validate all elements
		for i, item := range v {
			validateJSONExpression(t, filename, item, fmt.Sprintf("%s[%d]", path, i))
		}
	case float64:
		// Number expression - check for reasonable precision
		if v != v { // NaN check
			t.Errorf("Invalid number (NaN) in expression at %s in %s", path, filename)
		}
		if math.IsInf(v, 0) { // Infinity check
			t.Errorf("Invalid number (Infinity) in expression at %s in %s", path, filename)
		}
	case bool:
		// Boolean expression - always valid
	case nil:
		// Null expression - always valid
	case string:
		// String expression - validate template syntax if it contains interpolations
		if strings.Contains(v, "${") {
			validateTemplateString(t, filename, v, path)
		}
	default:
		t.Errorf("Invalid expression type at %s in %s: %T", path, filename, expr)
	}
}

// validateTemplateString validates template strings for basic syntax
func validateTemplateString(t *testing.T, filename, template, path string) {
	// Basic validation of template interpolation syntax
	openCount := strings.Count(template, "${")
	closeCount := strings.Count(template, "}")
	
	if openCount != closeCount {
		t.Errorf("Unmatched template interpolation braces in %s at %s: %q", filename, path, template)
	}

	// Check for nested interpolations (not allowed in basic form)
	parts := strings.Split(template, "${")
	for i := 1; i < len(parts); i++ {
		beforeClose := strings.Split(parts[i], "}")[0]
		if strings.Contains(beforeClose, "${") {
			t.Errorf("Nested template interpolations not supported in %s at %s: %q", filename, path, template)
		}
	}
}

// validateHCLJSONExpressions validates that expressions can be evaluated
func validateHCLJSONExpressions(t *testing.T, filename string, file *hcl.File) {
	// Try to get attributes and blocks to ensure they can be processed
	attrs, diags := file.Body.JustAttributes()
	if diags.HasErrors() {
		t.Errorf("Failed to extract attributes from %s: %v", filename, diags.Error())
	}

	// Validate attributes can be accessed
	for name := range attrs {
		if name == "" {
			t.Errorf("Empty attribute name found in %s", filename)
		}
	}

	// Try to get content with empty schema to see what blocks exist
	content, _, diags := file.Body.PartialContent(&hcl.BodySchema{})
	if diags.HasErrors() {
		t.Errorf("Failed to extract content from %s: %v", filename, diags.Error())
	}

	// Validate blocks have proper structure
	for _, block := range content.Blocks {
		if block.Type == "" {
			t.Errorf("Empty block type found in %s", filename)
		}
		
		// Recursively validate block bodies
		validateBlockBody(t, filename, block.Body, fmt.Sprintf("block[%s]", block.Type))
	}
}

// validateBlockBody validates the body of a block
func validateBlockBody(t *testing.T, filename string, body hcl.Body, path string) {
	attrs, diags := body.JustAttributes()
	if diags.HasErrors() {
		t.Errorf("Failed to extract attributes from %s at %s: %v", filename, path, diags.Error())
		return
	}

	for name := range attrs {
		if name == "" {
			t.Errorf("Empty attribute name in block body at %s in %s", path, filename)
		}
	}

	content, _, diags := body.PartialContent(&hcl.BodySchema{})
	if diags.HasErrors() {
		t.Errorf("Failed to extract content from %s at %s: %v", filename, path, diags.Error())
		return
	}

	for _, block := range content.Blocks {
		if block.Type == "" {
			t.Errorf("Empty block type in nested block at %s in %s", path, filename)
		}
		validateBlockBody(t, filename, block.Body, fmt.Sprintf("%s.%s", path, block.Type))
	}
}

// validateHCLJSONStaticAnalysis validates static analysis operations work
func validateHCLJSONStaticAnalysis(t *testing.T, filename string, file *hcl.File) {
	// Test that the file can be used for static analysis operations
	attrs, _ := file.Body.JustAttributes()
	
	for name, attr := range attrs {
		// Try to evaluate the attribute expression
		_, diags := attr.Expr.Value(nil)
		// Note: We don't require successful evaluation as expressions might reference variables
		// But we should not get parsing errors
		for _, diag := range diags {
			if diag.Severity == hcl.DiagError && strings.Contains(diag.Error(), "syntax") {
				t.Errorf("Syntax error in attribute %s in %s: %v", name, filename, diag.Error())
			}
		}
	}
} 