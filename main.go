package main

import (
	stdlibjson "encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclwrite"
	
	"github.com/kvz/json2hcl/convert"
	"github.com/zclconf/go-cty/cty"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// VERSION is what is returned by the `-v` flag
var Version = "development"

// Global variable to track target file type
var targetFileType string

func main() {
	version := flag.Bool("version", false, "Prints current app version")
	reverse := flag.Bool("reverse", false, "Input HCL, output JSON")
	outputFile := flag.String("output", "", "Output file path (used to determine file type for conversion)")
	treatArraysAsBlocks := flag.Bool("treat-arrays-as-blocks", false, "Convert JSON arrays to separate HCL blocks (e.g., variables, resources)")
	keepArraysNested := flag.Bool("keep-arrays-nested", false, "Keep JSON arrays as nested structures (e.g., for .tfvars format)")
	flag.Parse()
	if *version {
		fmt.Println(Version)
		return
	}

	// Set target file type based on flags and output file
	if *treatArraysAsBlocks && *keepArraysNested {
		fmt.Fprintln(os.Stderr, "Error: Cannot use both --treat-arrays-as-blocks and --keep-arrays-nested flags together")
		os.Exit(1)
	}
	
	if *treatArraysAsBlocks {
		targetFileType = "terraform"
	} else if *keepArraysNested {
		targetFileType = "tfvars"
	} else if *outputFile != "" {
		targetFileType = getFileType(*outputFile)
	} else {
		// Default to terraform format for backward compatibility
		targetFileType = "terraform"
	}

	var err error
	if *reverse {
		err = toJSON()
	} else {
		err = toHCL()
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// getFileType determines the file type based on extension
func getFileType(filename string) string {
	ext := filepath.Ext(filename)
	switch ext {
	case ".tf":
		return "terraform"
	case ".tfvars":
		return "tfvars"
	default:
		// Default to terraform for unknown extensions
		return "terraform"
	}
}

func toJSON() error {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("unable to read from stdin: %s", err)
	}

	// Use the convert package to convert HCL to JSON
	jsonBytes, err := convert.Bytes(input, "<stdin>", convert.Options{})
	if err != nil {
		return fmt.Errorf("unable to convert HCL to JSON: %s", err)
	}

	// Pretty print the JSON
	var jsonData interface{}
	if err := stdlibjson.Unmarshal(jsonBytes, &jsonData); err != nil {
		return fmt.Errorf("unable to parse JSON for formatting: %s", err)
	}

	prettyJSON, err := stdlibjson.MarshalIndent(jsonData, "", "  ")
	if err != nil {
		return fmt.Errorf("unable to format JSON: %s", err)
	}

	fmt.Println(string(prettyJSON))
	return nil
}

func toHCL() error {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("unable to read from stdin: %s", err)
	}

	// Use hclparse for JSON parsing - this handles JSON->HCL conversion natively
	parser := hclparse.NewParser()
	file, diags := parser.ParseJSON(input, "<stdin>")
	if diags.HasErrors() {
		return fmt.Errorf("unable to parse JSON: %s", diags.Error())
	}

	// Convert to native HCL syntax using hclwrite
	nativeFile := hclwrite.NewEmptyFile()
	err = convertToNativeHCL(file.Body, nativeFile.Body())
	if err != nil {
		return fmt.Errorf("unable to convert to native HCL: %s", err)
	}

	// Output the formatted HCL
	fmt.Print(string(nativeFile.Bytes()))
	return nil
}

func convertToNativeHCL(jsonBody hcl.Body, nativeBody *hclwrite.Body) error {
	// Get all attributes first to check if this is a block body or attribute body
	attrs, diags := jsonBody.JustAttributes()
	if !diags.HasErrors() {
		// This body only contains attributes, process them
		for name, attr := range attrs {
			val, valDiags := attr.Expr.Value(nil)
			if valDiags.HasErrors() {
				// If we can't evaluate, skip it (handles expressions)
				continue
			}
			
			// Check if this attribute represents an HCL JSON block structure
			if val.Type().IsListType() || val.Type().IsTupleType() {
				// Check if this looks like a block array (array of objects with nested structure)
				// vs a regular attribute array (simple array of objects/values)
				if isHCLBlockArray(name, val) {
					if err := convertJSONBlockArray(name, val, nativeBody); err != nil {
						// If block conversion fails, treat as regular attribute
						setAttributeWithExpressionHandling(nativeBody, name, val)
					}
				} else {
					// This is a regular attribute array, not block definitions
					setAttributeWithExpressionHandling(nativeBody, name, val)
				}
			} else if val.Type().IsObjectType() {
				// Check if this object should be converted to blocks (like variable definitions in .tf files)
				if shouldConvertObjectToBlocks(name, val) {
					if err := convertObjectToBlocks(name, val, nativeBody); err != nil {
						// If block conversion fails, treat as regular attribute
						setAttributeWithExpressionHandling(nativeBody, name, val)
					}
				} else {
					setAttributeWithExpressionHandling(nativeBody, name, val)
				}
			} else {
				setAttributeWithExpressionHandling(nativeBody, name, val)
			}
		}
		return nil
	}

	// This body contains blocks, get partial content
	content, _, diags := jsonBody.PartialContent(&hcl.BodySchema{})
	if diags.HasErrors() {
		return fmt.Errorf("unable to get content: %s", diags.Error())
	}

	// Process any attributes that were found
	for name, attr := range content.Attributes {
		val, valDiags := attr.Expr.Value(nil)
		if valDiags.HasErrors() {
			continue
		}
		setAttributeWithExpressionHandling(nativeBody, name, val)
	}

	// Process blocks recursively
	for _, block := range content.Blocks {
		nativeBlock := nativeBody.AppendNewBlock(block.Type, block.Labels)
		err := convertToNativeHCL(block.Body, nativeBlock.Body())
		if err != nil {
			return err
		}
	}

	return nil
}

func setAttributeWithExpressionHandling(body *hclwrite.Body, name string, val cty.Value) {
	// Check if this is a special case where we need unquoted literals
	if val.Type() == cty.String {
		strVal := val.AsString()
		
		// Handle type attributes that should be unquoted
		if name == "type" && isUnquotedType(strVal) {
			// Set as an unquoted identifier
			body.SetAttributeRaw(name, hclwrite.Tokens{
				{Type: hclsyntax.TokenIdent, Bytes: []byte(strVal)},
			})
			return
		}
		
		if expr := unwrapInterpolationExpression(strVal); expr != "" {
			// Set as a raw expression instead of a string value
			body.SetAttributeRaw(name, hclwrite.Tokens{
				{Type: hclsyntax.TokenIdent, Bytes: []byte(expr)},
			})
			return
		}
		
		// Check if this string contains interpolations that need special handling
		if strings.Contains(strVal, "${") {
			// This is a template string with interpolations - use raw tokens to avoid escaping
			body.SetAttributeRaw(name, hclwrite.Tokens{
				{Type: hclsyntax.TokenQuotedLit, Bytes: []byte(`"` + strVal + `"`)},
			})
			return
		}
	}
	
	// Regular attribute value
	body.SetAttributeValue(name, val)
}

// isUnquotedType checks if a type value should be unquoted
func isUnquotedType(str string) bool {
	unquotedTypes := map[string]bool{
		"string": true,
		"number": true,
		"bool":   true,
		"list":   true,
		"set":    true,
		"map":    true,
		"object": true,
		"tuple":  true,
		"any":    true,
	}
	return unquotedTypes[str]
}

func unwrapInterpolationExpression(str string) string {
	// Check if this is a simple interpolation expression like "${var.name}"
	if len(str) > 3 && str[0:2] == "${" && str[len(str)-1:] == "}" {
		inner := str[2 : len(str)-1]
		// Only unwrap if it looks like a simple reference (no complex expressions)
		if isSimpleReference(inner) {
			return inner
		}
	}
	return ""
}

func isSimpleReference(expr string) bool {
	// Simple heuristic: contains only alphanumeric, dots, underscores, and hyphens
	// This matches patterns like: var.name, aws_instance.foo.id, etc.
	for _, r := range expr {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || 
			 r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return len(expr) > 0
}

func unescapeInterpolations(str string) string {
	// Unescape double-escaped interpolations: $${...} -> ${...}
	str = strings.ReplaceAll(str, "$${", "${")
	// Unescape double-escaped template expressions: %%{...} -> %{...}
	str = strings.ReplaceAll(str, "%%{", "%{")
	return str
}

// isHCLBlockArray determines if an array should be treated as HCL blocks vs a regular attribute
func isHCLBlockArray(name string, val cty.Value) bool {
	// Only check arrays/lists
	if !val.Type().IsListType() && !val.Type().IsTupleType() {
		return false
	}
	
	// Empty arrays are not block arrays
	if val.LengthInt() == 0 {
		return false
	}
	
	// Check for known HCL block types that should be treated as blocks
	hclBlockTypes := map[string]bool{
		"resource":                true,
		"data":                    true,
		"provider":                true,
		"output":                  true,
		"locals":                  true,
		"module":                  true,
		"terraform":               true,
		"attribute":               true,
		"global_secondary_index":  true,
		"local_secondary_index":   true,
		"backup_policy":           true,
		"point_in_time_recovery":  true,
		"server_side_encryption":  true,
		"stream_specification":    true,
		"ttl":                     true,
	}
	
	// Special handling for variables based on target file type
	if name == "variable" {
		// For .tf files, variables should be separate blocks
		// For .tfvars files, variables should be nested attributes
		return targetFileType == "terraform"
	}
	
	// If this is a known HCL block type, treat it as a block array
	if hclBlockTypes[name] {
		return true
	}
	
	// Get the first element to inspect its structure
	firstElemIt := val.ElementIterator()
	firstElemIt.Next()
	_, firstElem := firstElemIt.Element()
	
	if !firstElem.Type().IsObjectType() {
		// If elements aren't objects, this is definitely not a block array
		return false
	}
	
	firstElemMap := firstElem.AsValueMap()
	
	// HCL JSON block arrays have a very specific nested structure:
	// "resource": [{"aws_instance": [{"name": [{...actual content...}]}]}]
	// Regular attribute arrays have a flat structure:
	// "subnets": [{"name": "value", "other": "value"}, ...]
	
	// Check if this has the nested block structure
	if len(firstElemMap) == 1 {
		for _, value := range firstElemMap {
			// If the single key maps to another array/list, it might be a block structure
			if value.Type().IsListType() || value.Type().IsTupleType() {
				if value.LengthInt() == 1 {
					valueElemIt := value.ElementIterator()
					valueElemIt.Next()
					_, valueFirstElem := valueElemIt.Element()
					
					if valueFirstElem.Type().IsObjectType() {
						valueFirstElemMap := valueFirstElem.AsValueMap()
						// If there's another level of nesting with labels, it's likely a block structure
						if len(valueFirstElemMap) == 1 {
							// This looks like a labeled block structure
							return true
						}
					}
				}
			}
		}
	}
	
	// For regular attribute arrays like subnets, security_groups, etc.,
	// these should be treated as regular attributes, not blocks
	return false
}

func convertJSONBlockArray(blockType string, val cty.Value, nativeBody *hclwrite.Body) error {
	// Check if this is an array/list of objects (HCL JSON block format)
	if !val.Type().IsListType() && !val.Type().IsTupleType() {
		return fmt.Errorf("not a block array")
	}

	// Iterate through each block instance in the array
	for it := val.ElementIterator(); it.Next(); {
		_, blockInstance := it.Element()
		
		if !blockInstance.Type().IsObjectType() {
			return fmt.Errorf("block instance is not an object")
		}

		// Extract block labels and content
		labels, blockContent, err := extractBlockLabelsAndContent(blockInstance)
		if err != nil {
			return err
		}

		// Create the native HCL block
		nativeBlock := nativeBody.AppendNewBlock(blockType, labels)
		
		// Add attributes to the block
		for attrName, attrVal := range blockContent {
			// Handle nested block arrays recursively
			if attrVal.Type().IsListType() || attrVal.Type().IsTupleType() {
				if err := convertJSONBlockArray(attrName, attrVal, nativeBlock.Body()); err != nil {
					// If it's not a nested block array, treat as regular attribute
					setAttributeWithExpressionHandling(nativeBlock.Body(), attrName, attrVal)
				}
			} else {
				setAttributeWithExpressionHandling(nativeBlock.Body(), attrName, attrVal)
			}
		}
	}

	return nil
}

func extractBlockLabelsAndContent(blockInstance cty.Value) ([]string, map[string]cty.Value, error) {
	labels := []string{}
	content := make(map[string]cty.Value)
	
	// Iterate through the object attributes
	blockMap := blockInstance.AsValueMap()
	
	// In HCL JSON format, labeled blocks are nested as:
	// "resource": [{"aws_instance": [{"my-instance": [{...actual content...}]}]}]
	// We need to extract the labels from the nested structure
	
	// If there's only one key at the top level, it might be a label
	if len(blockMap) == 1 {
		for key, value := range blockMap {
			// Check if this is a list with a single element that's also a single-key object
			if value.Type().IsListType() || value.Type().IsTupleType() {
				if value.LengthInt() == 1 {
					elemIt := value.ElementIterator()
					elemIt.Next()
					_, firstElem := elemIt.Element()
					
					if firstElem.Type().IsObjectType() {
						firstElemMap := firstElem.AsValueMap()
						if len(firstElemMap) == 1 {
							// This looks like another nested label
							for innerKey, innerValue := range firstElemMap {
								labels = append(labels, key, innerKey)
								if innerValue.Type().IsListType() || innerValue.Type().IsTupleType() {
									if innerValue.LengthInt() == 1 {
										innerElemIt := innerValue.ElementIterator()
										innerElemIt.Next()
										_, innerFirstElem := innerElemIt.Element()
										if innerFirstElem.Type().IsObjectType() {
											content = innerFirstElem.AsValueMap()
											return labels, content, nil
										}
									}
								}
								// If it's not a nested structure, use it as content
								content[innerKey] = innerValue
								return labels[:1], content, nil // Only first label
							}
						} else {
							// This is the content with the first key as label
							labels = append(labels, key)
							content = firstElemMap
							return labels, content, nil
						}
					}
				} else {
					// Multiple elements in the array - this could be multiple instances
					// of the same block type, treat as regular content
					content[key] = value
					return labels, content, nil
				}
			} else {
				// If it's not a list structure, treat as regular content
				content[key] = value
			}
		}
	} else {
		// Multiple keys at top level - treat all as regular content
		content = blockMap
	}
	
	return labels, content, nil
}

// shouldConvertObjectToBlocks determines if an object should be converted to separate blocks
func shouldConvertObjectToBlocks(name string, val cty.Value) bool {
	// Only check objects
	if !val.Type().IsObjectType() {
		return false
	}
	
	// For .tf files, certain object types should be converted to separate blocks
	if targetFileType == "terraform" {
		blockTypes := map[string]bool{
			"variable":  true,
			"output":    true,
			"locals":    true,
			"provider":  true,
			"resource":  true,
			"data":      true,
			"module":    true,
			"terraform": true,
		}
		return blockTypes[name]
	}
	
	return false
}

// convertObjectToBlocks converts an object to separate blocks
func convertObjectToBlocks(blockType string, val cty.Value, nativeBody *hclwrite.Body) error {
	if !val.Type().IsObjectType() {
		return fmt.Errorf("not an object")
	}
	
	// Iterate through each key-value pair in the object
	for key, value := range val.AsValueMap() {
		if err := convertObjectToBlocksRecursive(blockType, []string{key}, value, nativeBody); err != nil {
			return err
		}
	}
	
	return nil
}

// convertObjectToBlocksRecursive handles nested block structures
func convertObjectToBlocksRecursive(blockType string, labels []string, val cty.Value, nativeBody *hclwrite.Body) error {
	// Handle case where value is an array with a single object (HCL JSON format)
	if val.Type().IsListType() || val.Type().IsTupleType() {
		if val.LengthInt() == 1 {
			// Get the first (and only) element
			elemIt := val.ElementIterator()
			elemIt.Next()
			_, firstElem := elemIt.Element()
			
			if firstElem.Type().IsObjectType() {
				// Create the native HCL block with all collected labels
				nativeBlock := nativeBody.AppendNewBlock(blockType, labels)
				
				// Add attributes to the block, handling nested blocks recursively
				for attrName, attrVal := range firstElem.AsValueMap() {
					if attrVal.Type().IsListType() || attrVal.Type().IsTupleType() {
						// Check if this is a nested block array
						if isHCLBlockArray(attrName, attrVal) {
							if err := convertJSONBlockArray(attrName, attrVal, nativeBlock.Body()); err != nil {
								setAttributeWithExpressionHandling(nativeBlock.Body(), attrName, attrVal)
							}
						} else {
							setAttributeWithExpressionHandling(nativeBlock.Body(), attrName, attrVal)
						}
					} else {
						setAttributeWithExpressionHandling(nativeBlock.Body(), attrName, attrVal)
					}
				}
				return nil
			} else {
				return fmt.Errorf("expected object in array for block %s.%s", blockType, strings.Join(labels, "."))
			}
		} else {
			return fmt.Errorf("expected single element array for block %s.%s", blockType, strings.Join(labels, "."))
		}
	} else if val.Type().IsObjectType() {
		// Check if this is another level of nested structure (like resource.aws_instance.name)
		// If all values in the object are arrays/lists, it's likely another level of nesting
		valueMap := val.AsValueMap()
		allArrays := true
		for _, v := range valueMap {
			if !v.Type().IsListType() && !v.Type().IsTupleType() {
				allArrays = false
				break
			}
		}
		
		if allArrays && len(valueMap) > 0 {
			// This is another level of nesting, recurse deeper
			for nestedKey, nestedValue := range valueMap {
				newLabels := append(labels, nestedKey)
				if err := convertObjectToBlocksRecursive(blockType, newLabels, nestedValue, nativeBody); err != nil {
					return err
				}
			}
			return nil
		} else {
			// Direct object content - create block with current labels
			nativeBlock := nativeBody.AppendNewBlock(blockType, labels)
			for attrName, attrVal := range valueMap {
				setAttributeWithExpressionHandling(nativeBlock.Body(), attrName, attrVal)
			}
			return nil
		}
	} else {
		return fmt.Errorf("unexpected value type for block %s.%s", blockType, strings.Join(labels, "."))
	}
}

