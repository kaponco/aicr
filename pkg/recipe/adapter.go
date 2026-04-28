// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
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

// Package recipe provides recipe building and matching functionality.
package recipe

import (
	"embed"
	"fmt"
	"path"
	"strings"

	"github.com/NVIDIA/aicr/pkg/errors"
	"github.com/NVIDIA/aicr/recipes"
	"gopkg.in/yaml.v3"
)

// GetEmbeddedFS returns the embedded data filesystem.
// This is used by the CLI to create layered data providers.
func GetEmbeddedFS() embed.FS {
	return recipes.FS
}

// GetManifestContent retrieves a manifest file from the data provider.
// Path should be relative to data directory (e.g., "components/gpu-operator/manifests/dcgm-exporter.yaml").
func GetManifestContent(path string) ([]byte, error) {
	provider := GetDataProvider()
	return provider.ReadFile(path)
}

// loadAndMergeYAMLFiles loads a base file (if exists) and an overlay file,
// parses both as YAML, and merges them using MergeValues().
// Returns the merged map. If basePath is empty or base file doesn't exist, uses only overlay.
func loadAndMergeYAMLFiles(provider DataProvider, basePath, overlayPath string) (map[string]any, error) {
	result := make(map[string]any)

	// Load base file (optional)
	if basePath != "" {
		baseData, err := provider.ReadFile(basePath)
		if err == nil {
			// Base file exists, parse it
			if parseErr := yaml.Unmarshal(baseData, &result); parseErr != nil {
				return nil, errors.Wrap(errors.ErrCodeInternal,
					fmt.Sprintf("failed to parse base file %q", basePath), parseErr)
			}
		}
		// If base doesn't exist, continue with empty result
	}

	// Load overlay file
	overlayData, err := provider.ReadFile(overlayPath)
	if err != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal,
			fmt.Sprintf("failed to read overlay file %q", overlayPath), err)
	}

	// Parse overlay
	var overlayContent map[string]any
	if err := yaml.Unmarshal(overlayData, &overlayContent); err != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal,
			fmt.Sprintf("failed to parse overlay file %q", overlayPath), err)
	}

	// Merge overlay into base (overlay takes precedence)
	MergeValues(result, overlayContent)

	return result, nil
}

// GetMergedCustomResource retrieves and merges a custom resource file with its base (if applicable).
// For overlay files (e.g., resources-ocp-training.yaml), it loads the base file (e.g., resources-ocp.yaml),
// merges it with the overlay, and returns the merged YAML.
// For base files or files without a base, it returns the content as-is.
//
// Path should be relative to data directory (e.g., "components/gpu-operator/olm/resources-ocp-training.yaml").
func GetMergedCustomResource(path string) ([]byte, error) {
	provider := GetDataProvider()

	// Try to determine if this is an overlay file and find its base
	basePath := deriveBaseResourcePath(path)

	// If no base path found or it's the same as the current path, just return the file content
	if basePath == "" || basePath == path {
		return provider.ReadFile(path)
	}

	// Load and merge base + overlay
	merged, err := loadAndMergeYAMLFiles(provider, basePath, path)
	if err != nil {
		return nil, err
	}

	// Marshal merged content back to YAML
	mergedData, err := yaml.Marshal(merged)
	if err != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal, fmt.Sprintf("failed to marshal merged resource for %q", path), err)
	}

	return mergedData, nil
}

// deriveBaseResourcePath attempts to find the base resource file path for an overlay file.
// For example, "components/gpu-operator/olm/resources-ocp-training.yaml" would return
// "components/gpu-operator/olm/resources-ocp.yaml".
// Returns empty string if no base path can be derived.
func deriveBaseResourcePath(overlayPath string) string {
	// Extract directory and filename
	dir := path.Dir(overlayPath)
	filename := path.Base(overlayPath)

	// Check if filename matches pattern: resources-<criteria>.yaml or resources-<criteria>.yml
	if !strings.HasPrefix(filename, "resources-") {
		return "" // Not a resource file we recognize
	}

	// Remove extension
	ext := path.Ext(filename)
	nameWithoutExt := strings.TrimSuffix(filename, ext)

	// Split by dashes: resources-ocp-training → [resources, ocp, training]
	parts := strings.Split(nameWithoutExt, "-")
	if len(parts) <= 2 {
		// Only "resources-ocp" or less, this is likely already a base file
		return ""
	}

	// Remove last part to get base: [resources, ocp, training] → [resources, ocp]
	baseParts := parts[:len(parts)-1]
	baseName := strings.Join(baseParts, "-") + ext

	return path.Join(dir, baseName)
}

// RecipeInput is an interface that both Recipe and RecipeResult implement.
// This allows bundlers to work with either format during the transition period.
type RecipeInput interface {
	// GetComponentRef returns the component reference for a given component name.
	// Returns nil if the component is not found.
	GetComponentRef(name string) *ComponentRef

	// GetValuesForComponent returns the values map for a given component.
	// For Recipe, this extracts values from measurements.
	// For RecipeResult, this loads values from the component's valuesFile.
	GetValuesForComponent(name string) (map[string]any, error)

	// GetVersion returns the recipe version (CLI version that generated the recipe).
	// Returns empty string if version is not available.
	GetVersion() string

	// GetCriteria returns the criteria used to generate this recipe.
	// Returns nil if criteria is not available (e.g., for legacy Recipe format).
	GetCriteria() *Criteria
}

// Ensure Recipe implements RecipeInput
var _ RecipeInput = (*Recipe)(nil)

// GetComponentRef returns nil for Recipe (v1 format doesn't have components).
func (r *Recipe) GetComponentRef(name string) *ComponentRef {
	return nil
}

// GetValuesForComponent extracts values from measurements for Recipe.
// This maintains backward compatibility with the legacy measurements-based format.
func (r *Recipe) GetValuesForComponent(name string) (map[string]any, error) {
	// For legacy Recipe, values are embedded in measurements
	// This is a no-op - bundlers extract their own values from measurements
	return make(map[string]any), nil
}

// GetVersion returns the recipe version from metadata.
func (r *Recipe) GetVersion() string {
	if r.Metadata == nil {
		return ""
	}
	return r.Metadata["recipe-version"]
}

// GetCriteria returns nil for Recipe (v1 format doesn't have criteria).
func (r *Recipe) GetCriteria() *Criteria {
	return nil
}

// Ensure RecipeResult implements RecipeInput
var _ RecipeInput = (*RecipeResult)(nil)

// GetVersion returns the recipe version from metadata.
func (r *RecipeResult) GetVersion() string {
	return r.Metadata.Version
}

// GetCriteria returns the criteria used to generate this recipe result.
func (r *RecipeResult) GetCriteria() *Criteria {
	return r.Criteria
}

// GetComponentRef returns the component reference for a given component name.
func (r *RecipeResult) GetComponentRef(name string) *ComponentRef {
	for i := range r.ComponentRefs {
		if r.ComponentRefs[i].Name == name {
			return &r.ComponentRefs[i]
		}
	}
	return nil
}

// GetValuesForComponent loads values from the component's valuesFile and inline overrides.
// Merge order: base values → ValuesFile → Overrides (highest precedence).
// This supports three patterns:
//  1. ValuesFile only: Traditional separate file approach
//  2. Overrides only: Fully self-contained recipe with inline overrides
//  3. ValuesFile + Overrides: Hybrid - reusable base with recipe-specific tweaks
func (r *RecipeResult) GetValuesForComponent(name string) (map[string]any, error) {
	ref := r.GetComponentRef(name)
	if ref == nil {
		return nil, errors.New(errors.ErrCodeNotFound, fmt.Sprintf("component %q not found in recipe", name))
	}

	// Start with empty result
	result := make(map[string]any)

	// If no valuesFile and no overrides, return empty map
	if ref.ValuesFile == "" && len(ref.Overrides) == 0 {
		return result, nil
	}

	// Step 1: Load base and/or overlay values from files (if ValuesFile specified)
	if ref.ValuesFile != "" {
		provider := GetDataProvider()

		// Determine if this is an overlay values file (not the base values.yaml)
		baseValuesFile := fmt.Sprintf("components/%s/values.yaml", name)
		isOverlay := ref.ValuesFile != baseValuesFile

		if isOverlay {
			// Load and merge base + overlay values
			var err error
			result, err = loadAndMergeYAMLFiles(provider, baseValuesFile, ref.ValuesFile)
			if err != nil {
				return nil, err
			}
		} else {
			// Just load the base values file
			data, err := provider.ReadFile(ref.ValuesFile)
			if err != nil {
				return nil, errors.Wrap(errors.ErrCodeInternal, fmt.Sprintf("failed to read values file %q", ref.ValuesFile), err)
			}

			if err := yaml.Unmarshal(data, &result); err != nil {
				return nil, errors.Wrap(errors.ErrCodeInternal, fmt.Sprintf("failed to parse values file %q", ref.ValuesFile), err)
			}
		}
	}

	// Step 2: Apply inline overrides (highest precedence)
	if len(ref.Overrides) > 0 {
		MergeValues(result, ref.Overrides)
	}

	return result, nil
}

// MergeValues recursively merges src into dst.
// For maps, it recursively merges nested keys.
// For other types, src values override dst values.
// A nil value in src deletes the key from dst (explicit null override).
// This is used for merging recipe overlays and applying user overrides to custom resources.
func MergeValues(dst, src map[string]any) {
	for key, srcVal := range src {
		// Explicit null in overlay means "delete this key"
		if srcVal == nil {
			delete(dst, key)
			continue
		}
		if dstVal, exists := dst[key]; exists {
			// If both are maps, merge recursively
			if dstMap, dstOK := dstVal.(map[string]any); dstOK {
				if srcMap, srcOK := srcVal.(map[string]any); srcOK {
					MergeValues(dstMap, srcMap)
					continue
				}
			}
			// For non-map or mismatched types, src overrides dst
			dst[key] = srcVal
		} else {
			// Key doesn't exist in dst, add it
			dst[key] = srcVal
		}
	}
}

// hasComponentRefs checks if the input is a RecipeResult with component references.
func hasComponentRefs(input RecipeInput) bool {
	_, ok := input.(*RecipeResult)
	return ok
}
