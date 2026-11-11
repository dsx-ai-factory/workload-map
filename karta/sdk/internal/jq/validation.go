// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package jq

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/itchyny/gojq"
)

var (
	ErrDelFunc          = errors.New("del function is not allowed")
	ErrRecursiveDescent = errors.New("recursive descent operator '..' is not allowed")
)

// ValidateJQExpressions recursively validates all the fields of the object tagged with 'jq:"validate"'
func ValidateJQExpressions(object any) []error {
	var errs []error
	validateJQExpressionsRecursive(reflect.ValueOf(object), "", &errs)

	return errs
}

func validateJQExpressionsRecursive(val reflect.Value, fieldPath string, errs *[]error) {
	// Handle nil pointers
	if !val.IsValid() {
		return
	}

	// Dereference pointers
	if val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return
		}
		val = val.Elem()
	}

	switch val.Kind() {
	case reflect.Struct:
		validateJQExpressionsInStruct(val, fieldPath, errs)
	case reflect.Slice, reflect.Array:
		validateJQExpressionsInSlice(val, fieldPath, errs)
	case reflect.Map:
		validateJQExpressionsInMap(val, fieldPath, errs)
	}
}

func validateJQExpressionsInStruct(val reflect.Value, basePath string, errs *[]error) {
	typ := val.Type()

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i)

		// Skip unexported fields
		if !field.CanInterface() {
			continue
		}

		// Build field path for error reporting
		currentPath := buildFieldPath(basePath, fieldType.Name)

		// Check if field has jq:"validate" tag and handle string, *string, []string, and map[K]string fields
		if jqTag, ok := fieldType.Tag.Lookup("jq"); ok && jqTag == "validate" {
			handleTaggedStructField(field, currentPath, errs)
		} else {
			// Recursively validate nested structures
			validateJQExpressionsRecursive(field, currentPath, errs)
		}
	}
}

func handleTaggedStructField(field reflect.Value, currentPath string, errs *[]error) {
	switch {
	case isStringField(field):
		if err := validateStringField(field, currentPath); err != nil {
			*errs = append(*errs, err)
		}
	case isStringSliceField(field):
		for j := 0; j < field.Len(); j++ {
			elem := field.Index(j)
			elemPath := fmt.Sprintf("%s[%d]", currentPath, j)
			if err := validateStringField(elem, elemPath); err != nil {
				*errs = append(*errs, err)
			}
		}
	case isStringMapField(field):
		for _, key := range field.MapKeys() {
			value := field.MapIndex(key)
			valuePath := fmt.Sprintf("%s[%v]", currentPath, key.Interface())
			if err := validateStringField(value, valuePath); err != nil {
				*errs = append(*errs, err)
			}
		}
	default:
		*errs = append(*errs, fmt.Errorf("%s: jq:'validate' tag can only be used on string, *string, []string, and map[K]string fields", currentPath))
	}
}

func isStringField(field reflect.Value) bool {
	return field.Kind() == reflect.String || (field.Kind() == reflect.Pointer && field.Type().Elem().Kind() == reflect.String)
}

func isStringSliceField(field reflect.Value) bool {
	return field.Kind() == reflect.Slice && field.Type().Elem().Kind() == reflect.String
}

func isStringMapField(field reflect.Value) bool {
	return field.Kind() == reflect.Map && field.Type().Elem().Kind() == reflect.String
}

func validateJQExpressionsInSlice(val reflect.Value, basePath string, errs *[]error) {
	for i := 0; i < val.Len(); i++ {
		indexPath := fmt.Sprintf("%s[%d]", basePath, i)
		validateJQExpressionsRecursive(val.Index(i), indexPath, errs)
	}
}

func validateJQExpressionsInMap(val reflect.Value, basePath string, errs *[]error) {
	for _, key := range val.MapKeys() {
		keyPath := fmt.Sprintf("%s[%v]", basePath, key.Interface())
		validateJQExpressionsRecursive(val.MapIndex(key), keyPath, errs)
	}
}

func validateStringField(field reflect.Value, fieldPath string) error {
	// Handle pointer to string (*string)
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			return nil // Optional field, skip validation
		}
		field = field.Elem()
	}

	// Ensure field is a string
	if field.Kind() != reflect.String {
		return fmt.Errorf("%s: jq:'validate' tag can only be used on string or *string fields", fieldPath)
	}

	jqExpression := field.String()
	if jqExpression == "" {
		return nil // Empty jqExpression, skip validation
	}

	parsed, err := gojq.Parse(jqExpression)
	if err != nil {
		return fmt.Errorf("failed to parse JQ expression '%s' at '%s': %w", jqExpression, fieldPath, err)
	}

	err = ValidateParsedJQ(parsed)
	if err != nil {
		return fmt.Errorf("JQ expression '%s' at '%s' failed validation: %w", jqExpression, fieldPath, err)
	}

	return nil
}

// ValidateParsedJQ checks that a parsed JQ query is safe for read-only use.
// It rejects assignment operators, del, dangerous recursive/unbounded built-ins,
// and the recursive descent operator.
//
// The walk is reflection-driven so it automatically covers any new node types
// added by future gojq versions — no manual field enumeration needed.
func ValidateParsedJQ(q *gojq.Query) error {
	return walkAST(reflect.ValueOf(q))
}

// walkAST recursively visits every value reachable from v, calling checkASTNode
// on each struct it encounters.
func walkAST(v reflect.Value) error {
	if !v.IsValid() {
		return nil
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return walkAST(v.Elem())
	case reflect.Struct:
		if err := checkASTNode(v); err != nil {
			return err
		}
		for i := 0; i < v.NumField(); i++ {
			if err := walkAST(v.Field(i)); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := walkAST(v.Index(i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkASTNode inspects a single gojq AST struct node and returns an error if
// it contains a disallowed construct.
func checkASTNode(v reflect.Value) error {
	if !v.CanAddr() {
		return nil
	}
	switch n := v.Addr().Interface().(type) {
	case *gojq.Query:
		switch n.Op {
		case gojq.OpAssign, gojq.OpModify,
			gojq.OpUpdateAdd, gojq.OpUpdateSub, gojq.OpUpdateMul,
			gojq.OpUpdateDiv, gojq.OpUpdateMod, gojq.OpUpdateAlt:
			return fmt.Errorf("modifying operator '%s' is not allowed", n.Op)
		}
	case *gojq.Term:
		if n.Type == gojq.TermTypeRecurse {
			return ErrRecursiveDescent
		}
	case *gojq.Func:
		switch n.Name {
		case "del":
			return ErrDelFunc
		case "range", "paths", "recurse", "walk", "repeat":
			return fmt.Errorf("function '%s' may produce excessive output and is not allowed", n.Name)
		}
	}
	return nil
}

func buildFieldPath(basePath, fieldName string) string {
	if basePath == "" {
		return fieldName
	}
	return fmt.Sprintf("%s.%s", basePath, fieldName)
}
