// Command schema-check is a dev-time tool that fetches the authoritative
// resource schema for every Terraform provider cloudcompose generates
// config for (via `terraform providers schema -json`, at the versions
// pinned in the generators' required_providers blocks) and cross-checks
// it against the Go structs in internal/models that get marshalled into
// that JSON.
//
// It specifically catches a Go slice field that should be a bare struct
// (or vice versa) for a nested Terraform block, based on the block's
// nesting_mode/max_items in the fetched schema.
//
// Usage:
//
//	go run ./cmd/schema-check
//
// Requires the `terraform` CLI on PATH. Exits non-zero if any check
// finds a definite mismatch.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/gecburton/cloudcompose/internal/models"
)

// providerSpec is one entry in the required_providers block cloudcompose's
// generators declare, plus the registry source path terraform providers
// schema -json keys its output by.
type providerSpec struct {
	localName string
	source    string
	version   string
}

// resourceSet describes one *Resources struct in internal/models and
// which provider's schema its json-tagged resource-type fields should be
// checked against.
type resourceSet struct {
	name      string
	providers []string
	value     any
}

func main() {
	providers := []providerSpec{
		{"aws", "hashicorp/aws", "~> 5.0"},
		{"azurerm", "hashicorp/azurerm", "~> 4.0"},
		{"google", "hashicorp/google", "~> 5.0"},
		{"docker", "kreuzwerker/docker", "~> 3.0"},
		{"random", "hashicorp/random", "~> 3.6"},
		{"time", "hashicorp/time", "~> 0.13"},
	}

	schemaDir, err := os.MkdirTemp("", "cloudcompose-schema-check-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create temp dir:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(schemaDir)

	schema, err := fetchSchema(schemaDir, providers)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fetch provider schema:", err)
		os.Exit(1)
	}

	sets := []resourceSet{
		{"AWSResources", []string{"registry.terraform.io/hashicorp/aws", "registry.terraform.io/kreuzwerker/docker", "registry.terraform.io/hashicorp/random"}, models.AWSResources{}},
		{"AzureResources", []string{"registry.terraform.io/hashicorp/azurerm", "registry.terraform.io/kreuzwerker/docker", "registry.terraform.io/hashicorp/random", "registry.terraform.io/hashicorp/time"}, models.AzureResources{}},
		{"GcpResources", []string{"registry.terraform.io/hashicorp/google", "registry.terraform.io/kreuzwerker/docker", "registry.terraform.io/hashicorp/random"}, models.GcpResources{}},
	}

	var totalMismatches, totalUnclassified int
	for _, set := range sets {
		mismatches, unclassified := checkResourceSet(set, schema)
		totalMismatches += mismatches
		totalUnclassified += unclassified
	}

	fmt.Printf("\n%d resource type(s) checked with %d mismatch(es), %d block(s) not confidently classified.\n",
		len(sets), totalMismatches, totalUnclassified)

	if totalMismatches > 0 {
		os.Exit(1)
	}
}

// fetchSchema runs `terraform init` + `terraform providers schema -json`
// in a scratch directory declaring every provider cloudcompose generates
// config for, at the same version constraints the generators pin.
// Requires real network access to the Terraform registry.
func fetchSchema(dir string, providers []providerSpec) (map[string]any, error) {
	var b strings.Builder
	b.WriteString("terraform {\n  required_providers {\n")
	for _, p := range providers {
		fmt.Fprintf(&b, "    %s = {\n      source  = %q\n      version = %q\n    }\n", p.localName, p.source, p.version)
	}
	b.WriteString("  }\n}\n")

	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(b.String()), 0644); err != nil {
		return nil, fmt.Errorf("write main.tf: %w", err)
	}

	initCmd := exec.Command("terraform", "init", "-input=false")
	initCmd.Dir = dir
	if out, err := initCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("terraform init: %w\n%s", err, out)
	}

	schemaCmd := exec.Command("terraform", "providers", "schema", "-json")
	schemaCmd.Dir = dir
	out, err := schemaCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("terraform providers schema: %w", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("parse schema JSON: %w", err)
	}
	return parsed, nil
}

// blockSchema is the part of a provider's resource_schemas[type].block
// this tool consults: which nested attributes are blocks, and how each
// nests.
type blockSchema struct {
	blockTypes map[string]nestedBlock
}

type nestedBlock struct {
	nestingMode string // "single", "list", "set", "map", "group"
	maxItems    *int
	block       blockSchema
}

// resourceBlock looks up a resource type's top-level block schema across
// every provider source listed, since a resource type's own name prefix
// (e.g. "aws_"/"azurerm_"/"docker_") determines its provider, not any
// mapping recorded here.
func resourceBlock(schema map[string]any, providerSources []string, resourceType string) (blockSchema, bool) {
	providerSchemas, _ := schema["provider_schemas"].(map[string]any)
	for _, source := range providerSources {
		ps, ok := providerSchemas[source].(map[string]any)
		if !ok {
			continue
		}
		resourceSchemas, _ := ps["resource_schemas"].(map[string]any)
		raw, ok := resourceSchemas[resourceType].(map[string]any)
		if !ok {
			continue
		}
		block, _ := raw["block"].(map[string]any)
		return parseBlock(block), true
	}
	return blockSchema{}, false
}

func parseBlock(block map[string]any) blockSchema {
	result := blockSchema{blockTypes: map[string]nestedBlock{}}
	blockTypes, _ := block["block_types"].(map[string]any)
	for name, raw := range blockTypes {
		spec, _ := raw.(map[string]any)
		nb := nestedBlock{nestingMode: fmt.Sprint(spec["nesting_mode"])}
		if mi, ok := spec["max_items"].(float64); ok {
			v := int(mi)
			nb.maxItems = &v
		}
		if nestedBlockRaw, ok := spec["block"].(map[string]any); ok {
			nb.block = parseBlock(nestedBlockRaw)
		}
		result.blockTypes[name] = nb
	}
	return result
}

// isBoundedToOne reports whether Terraform's JSON syntax would accept a
// bare object rather than an array for this nested block: nesting_mode
// "single" always, or "list"/"set" with max_items exactly 1.
func (nb nestedBlock) isBoundedToOne() bool {
	if nb.nestingMode == "single" {
		return true
	}
	if (nb.nestingMode == "list" || nb.nestingMode == "set") && nb.maxItems != nil && *nb.maxItems == 1 {
		return true
	}
	return false
}

func (nb nestedBlock) isRepeatable() bool {
	return (nb.nestingMode == "list" || nb.nestingMode == "set") && !nb.isBoundedToOne()
}

// checkResourceSet reflects over one *Resources struct's fields, and
// for every field with a `json:"<resource_type>,omitempty"` tag that
// looks like a Terraform resource type (map[string]SomeStruct with an
// underscore in the tag), recursively compares SomeStruct's fields
// against the fetched schema's block_types for that resource type.
func checkResourceSet(set resourceSet, schema map[string]any) (mismatches, unclassified int) {
	fmt.Printf("\n=== %s ===\n", set.name)

	t := reflect.TypeOf(set.value)
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("json")
		resourceType := strings.SplitN(tag, ",", 2)[0]
		if resourceType == "" || resourceType == "-" || !strings.Contains(resourceType, "_") {
			continue
		}

		// cloudcompose's own convention: every resource-type field is
		// map[string]T.
		fieldType := field.Type
		if fieldType.Kind() != reflect.Map {
			continue
		}
		elemType := fieldType.Elem()

		block, found := resourceBlock(schema, set.providers, resourceType)
		if !found {
			fmt.Printf("  ? %-45s no schema found for %q under any of %v (renamed/removed upstream, or a provider this tool doesn't yet declare?)\n", field.Name, resourceType, set.providers)
			unclassified++
			continue
		}

		m, u := compareStruct(field.Name, elemType, block, 1)
		mismatches += m
		unclassified += u
	}
	return mismatches, unclassified
}

// compareStruct walks goType's fields, matching each to a same-named
// (by JSON tag) entry in the schema's block_types, and reports a
// mismatch when the Go field's shape (slice vs. non-slice) disagrees
// with what the schema requires.
func compareStruct(path string, goType reflect.Type, block blockSchema, depth int) (mismatches, unclassified int) {
	if goType.Kind() == reflect.Ptr {
		goType = goType.Elem()
	}
	if goType.Kind() != reflect.Struct {
		return 0, 0
	}

	indent := strings.Repeat("  ", depth)

	for i := 0; i < goType.NumField(); i++ {
		field := goType.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" {
			continue
		}
		jsonName := strings.SplitN(tag, ",", 2)[0]
		if jsonName == "" || jsonName == "-" {
			continue
		}

		nb, ok := block.blockTypes[jsonName]
		if !ok {
			// Not every JSON-tagged field is a nested block -- most are
			// plain attributes this tool has nothing to check.
			continue
		}

		ft := field.Type
		isSlice := ft.Kind() == reflect.Slice
		underlying := ft
		if isSlice {
			underlying = ft.Elem()
		}
		if underlying.Kind() == reflect.Ptr {
			underlying = underlying.Elem()
		}

		switch {
		case nb.isRepeatable() && !isSlice:
			fmt.Printf("%s✗ %s.%s (%q): schema allows multiple entries (nesting_mode=%s, no max_items cap) but the Go field is not a slice (%s) -- can only ever express one\n",
				indent, path, field.Name, jsonName, nb.nestingMode, ft.String())
			mismatches++
		case nb.isBoundedToOne() && isSlice:
			// Not a bug: Terraform's JSON syntax also accepts a
			// one-element array for single-cardinality blocks.
			fmt.Printf("%sℹ %s.%s (%q): schema caps this at one entry (nesting_mode=%s, max_items=1); Go field is a slice ([%d]-shaped in JSON, valid but a bare-object field would also work)\n",
				indent, path, field.Name, jsonName, nb.nestingMode, 1)
		}

		if underlying.Kind() == reflect.Struct {
			m, u := compareStruct(path+"."+field.Name, underlying, nb.block, depth+1)
			mismatches += m
			unclassified += u
		}
	}
	return mismatches, unclassified
}
