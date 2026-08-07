package compiler

import (
	"reflect"
	"strings"
)

// structToPyOrdered converts a Go struct (or pointer to struct) into a
// PyOrdered value using reflection over its json tags, in field
// declaration order, honoring "omitempty" the same way encoding/json does.
//
// Needed because PyDumpsIndent has to preserve insertion order at every
// level of the Azure Terraform document (generator_azure.py's
// json.dumps(terraform, indent=2) has no sort_keys=True at all, unlike
// AWS's generator.py) -- and the AWS port's approach of round-tripping
// structs through encoding/json into map[string]any to get an
// intermediate form loses field order entirely, which was fine for AWS
// (whose Python side does sort_keys=True) but is not fine here. Go struct
// field declaration order already matches the corresponding Pydantic
// model's field declaration order (both were written by hand from the
// same source), so reflecting over that order rather than any other is
// what makes this correct.
func structToPyOrdered(v any) any {
	// PyOrdered/PyFloat already state their own shape and must not be
	// treated as generic slices/floats by the reflection dispatch below --
	// PyOrdered's underlying type is a slice of PyPair, which
	// sliceToPyOrdered would otherwise try to convert element-by-element
	// as if each PyPair were an arbitrary struct, producing
	// {"Key": ..., "Value": ...} objects instead of the intended
	// {key: value} shape.
	switch val := v.(type) {
	case nil:
		return nil
	case PyOrdered:
		return val
	case PyFloat:
		return val
	}

	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil
	}
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}

	switch rv.Kind() {
	case reflect.Struct:
		return structFieldsToPyOrdered(rv)
	case reflect.Map:
		return mapToPyOrdered(rv)
	case reflect.Slice, reflect.Array:
		return sliceToPyOrdered(rv)
	default:
		// rv may have been dereferenced from a pointer above (e.g. a
		// *string field); returning the original v here instead of
		// rv.Interface() would hand back the still-pointer value, which
		// writePyValueIndent's default case would feed straight back into
		// structToPyOrdered -- dereference, return the pointer again,
		// forever. Confirmed as the actual cause of a real stack overflow
		// (not a theoretical one) when this returned v instead of
		// rv.Interface() for pointer-typed scalar fields like
		// PostgreSQLFlexibleServer.DelegatedSubnetID (2026-08-06).
		return rv.Interface()
	}
}

func structFieldsToPyOrdered(rv reflect.Value) PyOrdered {
	t := rv.Type()
	obj := make(PyOrdered, 0, t.NumField())

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue // unexported
		}
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts := parseJSONTag(tag)
		if name == "" {
			name = field.Name
		}

		fieldValue := rv.Field(i)
		if opts.omitempty && isEmptyValue(fieldValue) {
			continue
		}

		obj = append(obj, p(name, structToPyOrdered(fieldValue.Interface())))
	}

	return obj
}

type jsonTagOpts struct {
	omitempty bool
}

func parseJSONTag(tag string) (string, jsonTagOpts) {
	parts := strings.Split(tag, ",")
	name := parts[0]
	var opts jsonTagOpts
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			opts.omitempty = true
		}
	}
	return name, opts
}

func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface:
		return v.IsNil()
	case reflect.Slice, reflect.Map, reflect.Array:
		return v.Len() == 0
	case reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	default:
		return false
	}
}

func mapToPyOrdered(rv reflect.Value) any {
	if rv.IsNil() {
		return nil
	}
	keys := rv.MapKeys()
	sortReflectStringKeys(keys)

	obj := make(PyOrdered, 0, len(keys))
	for _, k := range keys {
		obj = append(obj, p(k.String(), structToPyOrdered(rv.MapIndex(k).Interface())))
	}
	return obj
}

func sortReflectStringKeys(keys []reflect.Value) {
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1].String() > keys[j].String(); j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
}

func sliceToPyOrdered(rv reflect.Value) any {
	if rv.IsNil() {
		return nil
	}
	out := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		out[i] = structToPyOrdered(rv.Index(i).Interface())
	}
	return out
}
