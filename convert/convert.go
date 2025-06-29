// Package convert implements HCL to JSON conversion.
// This implementation is based on github.com/tmccombs/hcl2json/convert 
// which is licensed under Apache License 2.0.
// 
// Original work Copyright (c) 2019 Thayne McCombs
// Modifications Copyright (c) 2025 MaxVisser
// 
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
// 
//     http://www.apache.org/licenses/LICENSE-2.0
// 
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	ctyconvert "github.com/zclconf/go-cty/cty/convert"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// evalContext is used for simplification of expressions
var evalContext = &hcl.EvalContext{
	Variables: map[string]cty.Value{},
	Functions: map[string]function.Function{},
}

type Options struct {
	Simplify bool
}

// Bytes takes the contents of an HCL file, as bytes, and converts
// them into a JSON representation of the HCL file.
func Bytes(bytes []byte, filename string, options Options) ([]byte, error) {
	file, diags := hclsyntax.ParseConfig(bytes, filename, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse config: %v", diags.Errs())
	}

	hclBytes, err := File(file, options)
	if err != nil {
		return nil, fmt.Errorf("convert to HCL: %w", err)
	}

	return hclBytes, nil
}

// File takes an HCL file and converts it to its JSON representation.
func File(file *hcl.File, options Options) ([]byte, error) {
	convertedFile, err := ConvertFile(file, options)
	if err != nil {
		return nil, fmt.Errorf("convert file: %w", err)
	}

	jsonBytes, err := json.Marshal(convertedFile)
	if err != nil {
		return nil, fmt.Errorf("marshal json: %w", err)
	}

	return jsonBytes, nil
}

type jsonObj = map[string]interface{}

type converter struct {
	bytes   []byte
	options Options
}

func ConvertFile(file *hcl.File, options Options) (jsonObj, error) {
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("convert file body to body type")
	}

	c := converter{
		bytes:   file.Bytes,
		options: options,
	}

	out, err := c.ConvertBody(body)
	if err != nil {
		return nil, fmt.Errorf("convert body: %w", err)
	}

	return out, nil
}

func (c *converter) ConvertBody(body *hclsyntax.Body) (jsonObj, error) {
	out := make(jsonObj)

	for _, block := range body.Blocks {
		if err := c.convertBlock(block, out); err != nil {
			return nil, fmt.Errorf("convert block: %w", err)
		}
	}

	var err error
	for key, value := range body.Attributes {
		out[key], err = c.ConvertExpression(value.Expr)
		if err != nil {
			return nil, fmt.Errorf("convert expression: %w", err)
		}
	}

	return out, nil
}

func (c *converter) rangeSource(r hcl.Range) string {
	// for some reason the range doesn't include the ending paren, so
	// check if the next character is an ending paren, and include it if it is.
	end := r.End.Byte
	if end < len(c.bytes) && c.bytes[end] == ')' {
		end++
	}
	return string(c.bytes[r.Start.Byte:end])
}

func (c *converter) convertBlock(block *hclsyntax.Block, out jsonObj) error {
	key := block.Type
	for _, label := range block.Labels {

		// Labels represented in HCL are defined as quoted strings after the name of the block:
		// block "label_one" "label_two"
		//
		// Labels represtend in JSON are nested one after the other:
		// "label_one": {
		//   "label_two": {}
		// }
		//
		// To create the JSON representation, check to see if the label exists in the current output:
		//
		// When the label exists, move onto the next label reference.
		// When a label does not exist, create the label in the output and set that as the next label reference
		// in order to append (potential) labels to it.
		if _, exists := out[key]; exists {
			var ok bool
			out, ok = out[key].(jsonObj)
			if !ok {
				return fmt.Errorf("Unable to convert Block to JSON: %v.%v", block.Type, strings.Join(block.Labels, "."))
			}
		} else {
			out[key] = make(jsonObj)
			out = out[key].(jsonObj)
		}

		key = label
	}

	value, err := c.ConvertBody(block.Body)
	if err != nil {
		return fmt.Errorf("convert body: %w", err)
	}

	// Multiple blocks can exist with the same name, at the same
	// level in the JSON document (e.g. locals).
	//
	// For consistency, always wrap the value in a collection.
	// When multiple values are at the same key
	if current, exists := out[key]; exists {
		switch currentTyped := current.(type) {
		case []interface{}:
			currentTyped = append(currentTyped, value)
			out[key] = currentTyped
		default:
			return fmt.Errorf("invalid HCL detected for %q block, cannot have blocks with and without labels", key)
		}
	} else {
		out[key] = []interface{}{value}
	}

	return nil
}

func (c *converter) ConvertExpression(expr hclsyntax.Expression) (interface{}, error) {
	if c.options.Simplify {
		value, err := expr.Value(evalContext)
		if err == nil {
			return ctyjson.SimpleJSONValue{Value: value}, nil
		}
	}

	// assume it is hcl syntax (because, um, it is)
	switch value := expr.(type) {
	case *hclsyntax.LiteralValueExpr:
		return ctyjson.SimpleJSONValue{Value: value.Val}, nil
	case *hclsyntax.ScopeTraversalExpr:
		// Handle simple identifiers like 'string', 'number', etc.
		// These should be treated as string literals, not expressions
		if len(value.Traversal) == 1 {
			if root, ok := value.Traversal[0].(hcl.TraverseRoot); ok {
				// This is a simple identifier - treat it as a string literal
				return root.Name, nil
			}
		}
		// For complex traversals, wrap in ${...}
		return c.wrapExpr(value), nil
	case *hclsyntax.UnaryOpExpr:
		return c.convertUnary(value)
	case *hclsyntax.TemplateExpr:
		return c.convertTemplate(value)
	case *hclsyntax.TemplateWrapExpr:
		return c.ConvertExpression(value.Wrapped)
	case *hclsyntax.TupleConsExpr:
		list := make([]interface{}, 0)
		for _, ex := range value.Exprs {
			elem, err := c.ConvertExpression(ex)
			if err != nil {
				return nil, err
			}
			list = append(list, elem)
		}
		return list, nil
	case *hclsyntax.ObjectConsExpr:
		m := make(jsonObj)
		for _, item := range value.Items {
			key, err := c.convertKey(item.KeyExpr)
			if err != nil {
				return nil, err
			}
			m[key], err = c.ConvertExpression(item.ValueExpr)
			if err != nil {
				return nil, err
			}
		}
		return m, nil
	default:
		return c.wrapExpr(expr), nil
	}
}

func (c *converter) convertUnary(v *hclsyntax.UnaryOpExpr) (interface{}, error) {
	_, isLiteral := v.Val.(*hclsyntax.LiteralValueExpr)
	if !isLiteral {
		// If the expression after the operator isn't a literal, fall back to
		// wrapping the expression with ${...}
		return c.wrapExpr(v), nil
	}
	val, err := v.Value(nil)
	if err != nil {
		return nil, err
	}
	return ctyjson.SimpleJSONValue{Value: val}, nil
}

// Escape sequences that have special meaning in hcl json
// such as ${ that come from literals, that shouldn't have
// real interpolation
func escapeLiteral(lit string) string {
	lit = strings.Replace(lit, "${", "$${", -1)
	lit = strings.Replace(lit, "%{", "%%{", -1)
	return lit
}

func (c *converter) convertTemplate(t *hclsyntax.TemplateExpr) (string, error) {
	if t.IsStringLiteral() {
		// safe because the value is just the string
		v, err := t.Value(nil)
		if err != nil {
			return "", err
		}
		return escapeLiteral(v.AsString()), nil
	}
	var builder strings.Builder
	for _, part := range t.Parts {
		s, err := c.convertStringPart(part)
		if err != nil {
			return "", err
		}
		builder.WriteString(s)
	}
	return builder.String(), nil
}

func (c *converter) convertStringPart(expr hclsyntax.Expression) (string, error) {
	switch v := expr.(type) {
	case *hclsyntax.LiteralValueExpr:
		// If the key is a bare "null", then we end up with null here,
		// in this case we should just return the string "null"
		if v.Val.IsNull() {
			return "null", nil
		}
		s, err := ctyconvert.Convert(v.Val, cty.String)
		if err != nil {
			return "", err
		}
		return escapeLiteral(s.AsString()), nil
	case *hclsyntax.TemplateExpr:
		return c.convertTemplate(v)
	case *hclsyntax.TemplateWrapExpr:
		return c.convertStringPart(v.Wrapped)
	case *hclsyntax.ConditionalExpr:
		return c.convertTemplateConditional(v)
	case *hclsyntax.TemplateJoinExpr:
		return c.convertTemplateFor(v.Tuple.(*hclsyntax.ForExpr))
	default:
		// treating as an embedded expression
		return c.wrapExpr(expr), nil
	}
}

func (c *converter) convertKey(keyExpr hclsyntax.Expression) (string, error) {
	// a key should never have dynamic input
	if k, isKeyExpr := keyExpr.(*hclsyntax.ObjectConsKeyExpr); isKeyExpr {
		keyExpr = k.Wrapped
		if _, isTraversal := keyExpr.(*hclsyntax.ScopeTraversalExpr); isTraversal {
			return c.rangeSource(keyExpr.Range()), nil
		}
	}
	return c.convertStringPart(keyExpr)
}

func (c *converter) convertTemplateConditional(expr *hclsyntax.ConditionalExpr) (string, error) {
	var builder strings.Builder
	builder.WriteString("%{if ")
	builder.WriteString(c.rangeSource(expr.Condition.Range()))
	builder.WriteString("}")
	trueResult, err := c.convertStringPart(expr.TrueResult)
	if err != nil {
		return "", nil
	}
	builder.WriteString(trueResult)
	falseResult, err := c.convertStringPart(expr.FalseResult)
	if len(falseResult) > 0 {
		builder.WriteString("%{else}")
		builder.WriteString(falseResult)
	}
	builder.WriteString("%{endif}")

	return builder.String(), nil
}

func (c *converter) convertTemplateFor(expr *hclsyntax.ForExpr) (string, error) {
	var builder strings.Builder
	builder.WriteString("%{for ")
	if len(expr.KeyVar) > 0 {
		builder.WriteString(expr.KeyVar)
		builder.WriteString(", ")
	}
	builder.WriteString(expr.ValVar)
	builder.WriteString(" in ")
	builder.WriteString(c.rangeSource(expr.CollExpr.Range()))
	builder.WriteString("}")
	templ, err := c.convertStringPart(expr.ValExpr)
	if err != nil {
		return "", err
	}
	builder.WriteString(templ)
	builder.WriteString("%{endfor}")

	return builder.String(), nil
}

func (c *converter) wrapExpr(expr hclsyntax.Expression) string {
	return "${" + c.rangeSource(expr.Range()) + "}"
} 