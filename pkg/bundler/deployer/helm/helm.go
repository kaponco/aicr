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

package helm

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NVIDIA/aicr/pkg/bundler/checksum"
	"github.com/NVIDIA/aicr/pkg/bundler/deployer"
	"github.com/NVIDIA/aicr/pkg/component"
	"github.com/NVIDIA/aicr/pkg/errors"
	"github.com/NVIDIA/aicr/pkg/manifest"
	"github.com/NVIDIA/aicr/pkg/recipe"
)

//go:embed templates/README.md.tmpl
var readmeTemplate string

//go:embed templates/component-README.md.tmpl
var componentReadmeTemplate string

//go:embed templates/deploy.sh.tmpl
var deployScriptTemplate string

//go:embed templates/undeploy.sh.tmpl
var undeployScriptTemplate string

//go:embed templates/subscribe.sh.tmpl
var subscribeScriptTemplate string

//go:embed templates/unsubscribe.sh.tmpl
var unsubscribeScriptTemplate string

// criteriaAny is the wildcard value for criteria fields.
const criteriaAny = "any"

// ComponentData contains data for rendering per-component templates.
type ComponentData struct {
	Name            string
	Namespace       string
	Repository      string
	ChartName       string
	Version         string // Original version string (preserves 'v' prefix) for helm install --version
	ChartVersion    string // Normalized version (no 'v' prefix) for chart metadata labels
	HasManifests    bool
	HasChart        bool
	IsOCI           bool
	IsKustomize     bool                 // True when the component uses Kustomize instead of Helm
	Tag             string               // Git ref for Kustomize components (tag, branch, or commit)
	Path            string               // Path within the repository to the kustomization
	IsOLM           bool                 // True when the component uses OLM (Operator Lifecycle Manager)
	CustomResources []CustomResourceData // Custom resources for OLM components
	HasInstallFiles bool                 // True when the component has OLM install files
	InstallFiles    []InstallFileData    // OLM install files (Subscription, OperatorGroup, etc.)
	Service         string               // Kubernetes service type (ocp, eks, gke, etc.)
}

// CustomResourceData contains data for a custom resource file.
type CustomResourceData struct {
	Filename string // Base filename (e.g., "cr-node-feature-discovery.yaml")
}

// InstallFileData contains data for an OLM install file.
type InstallFileData struct {
	Filename string // Base filename (e.g., "install.yaml")
	Path     string // Relative path from component dir (e.g., "olm/install.yaml")
}

// compile-time interface check
var _ deployer.Deployer = (*Generator)(nil)

// Generator creates per-component Helm bundles from recipe results.
// Configure it with the required fields, then call Generate.
type Generator struct {
	// RecipeResult contains the recipe metadata and component references.
	RecipeResult *recipe.RecipeResult

	// ComponentValues maps component names to their values.
	// These are collected from individual bundlers.
	ComponentValues map[string]map[string]any

	// Version is the bundler version (from CLI/bundler version).
	Version string

	// IncludeChecksums indicates whether to generate a checksums.txt file.
	IncludeChecksums bool

	// ComponentManifests maps component name → manifest path → content.
	// Each component's manifests are placed in its own manifests/ subdirectory.
	ComponentManifests map[string]map[string][]byte

	// ComponentCustomResources maps component name → custom resource path → content.
	// Each component's custom resources are placed directly in its subdirectory.
	ComponentCustomResources map[string]map[string][]byte

	// ComponentInstallFiles maps component name → install file path → content.
	// Install files (e.g., OLM Subscriptions, OperatorGroups) are placed in component's olm/ subdirectory.
	ComponentInstallFiles map[string]map[string][]byte

	// DataFiles lists additional file paths (relative to output dir) to include
	// in checksum generation. Used for external data files copied into the bundle.
	DataFiles []string

	// DynamicValues maps component names to their dynamic value paths.
	// These paths are removed from values.yaml and written to cluster-values.yaml.
	DynamicValues map[string][]string
}

// Generate creates a per-component Helm bundle from the configured generator fields.
func (g *Generator) Generate(ctx context.Context, outputDir string) (*deployer.Output, error) {
	start := time.Now()

	output := &deployer.Output{
		Files: make([]string, 0),
	}

	if g.RecipeResult == nil {
		return nil, errors.New(errors.ErrCodeInvalidRequest, "RecipeResult is required")
	}

	// Create output directory
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal,
			"failed to create output directory", err)
	}

	// Build sorted component data list (validates component names)
	components, err := g.buildComponentDataList()
	if err != nil {
		return nil, err
	}

	// Generate per-component directories
	files, size, err := g.generateComponentDirectories(ctx, components, outputDir)
	if err != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal,
			"failed to generate component directories", err)
	}
	output.Files = append(output.Files, files...)
	output.TotalSize += size

	// Generate root README.md
	readmePath, readmeSize, err := g.generateRootREADME(ctx, components, outputDir)
	if err != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal,
			"failed to generate README.md", err)
	}
	output.Files = append(output.Files, readmePath)
	output.TotalSize += readmeSize

	// Generate subscribe.sh (for OLM components)
	installPath, installSize, err := g.generateSubscribeScript(ctx, components, outputDir)
	if err != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal,
			"failed to generate subscribe.sh", err)
	}
	if installPath != "" {
		output.Files = append(output.Files, installPath)
		output.TotalSize += installSize
	}

	// Generate unsubscribe.sh (for OLM components)
	uninstallPath, uninstallSize, err := g.generateUnsubscribeScript(ctx, components, outputDir)
	if err != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal,
			"failed to generate unsubscribe.sh", err)
	}
	if uninstallPath != "" {
		output.Files = append(output.Files, uninstallPath)
		output.TotalSize += uninstallSize
	}

	// Generate deploy.sh
	deployPath, deploySize, err := g.generateDeployScript(ctx, components, outputDir)
	if err != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal,
			"failed to generate deploy.sh", err)
	}
	output.Files = append(output.Files, deployPath)
	output.TotalSize += deploySize

	// Generate undeploy.sh
	undeployPath, undeploySize, err := g.generateUndeployScript(ctx, components, outputDir)
	if err != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal,
			"failed to generate undeploy.sh", err)
	}
	output.Files = append(output.Files, undeployPath)
	output.TotalSize += undeploySize

	// Include external data files in the file list (for checksums)
	if err := output.AddDataFiles(outputDir, g.DataFiles); err != nil {
		return nil, err
	}

	// Generate checksums.txt if requested
	if g.IncludeChecksums {
		if err := checksum.WriteChecksums(ctx, outputDir, output); err != nil {
			return nil, err
		}
	}

	output.Duration = time.Since(start)

	// Populate deployment steps for CLI output
	output.DeploymentSteps = []string{
		fmt.Sprintf("cd %s", outputDir),
		"chmod +x deploy.sh",
		"./deploy.sh",
	}

	slog.Debug("helm bundle generated",
		"files", len(output.Files),
		"total_size", output.TotalSize,
		"duration", output.Duration,
	)

	return output, nil
}

// buildComponentDataList builds a sorted list of ComponentData from the recipe.
// It validates that all component names are safe for use as directory names.
func (g *Generator) buildComponentDataList() ([]ComponentData, error) {
	componentMap := make(map[string]recipe.ComponentRef)
	for _, ref := range g.RecipeResult.ComponentRefs {
		componentMap[ref.Name] = ref
	}

	// Extract service from criteria
	var service string
	if g.RecipeResult != nil && g.RecipeResult.Criteria != nil {
		service = string(g.RecipeResult.Criteria.Service)
	}

	// Sort by deployment order
	sorted := deployer.SortComponentRefsByDeploymentOrder(
		g.RecipeResult.ComponentRefs,
		g.RecipeResult.DeploymentOrder,
	)

	components := make([]ComponentData, 0, len(sorted))
	for _, ref := range sorted {
		if !deployer.IsSafePathComponent(ref.Name) {
			return nil, errors.New(errors.ErrCodeInvalidRequest,
				fmt.Sprintf("invalid component name %q: must not contain path separators or parent directory references", ref.Name))
		}

		hasManifests := false
		if g.ComponentManifests != nil {
			if m, ok := g.ComponentManifests[ref.Name]; ok && len(m) > 0 {
				hasManifests = true
			}
		}

		isKustomize := ref.Type == recipe.ComponentTypeKustomize
		isOLM := ref.Type == recipe.ComponentTypeOLM

		chartName := ref.Chart
		if chartName == "" {
			chartName = ref.Name
		}

		isOCI := strings.HasPrefix(ref.Source, "oci://")
		// Preserve version string as-is for deploy.sh --version flag.
		// Helm handles 'v' prefixes correctly via fuzzy matching.
		version := ref.Version

		// Collect custom resources for OLM components
		var customResources []CustomResourceData
		if isOLM {
			for _, crPath := range ref.CustomResources {
				customResources = append(customResources, CustomResourceData{
					Filename: filepath.Base(crPath),
				})
			}
		}

		// Collect install files
		hasInstallFiles := false
		var installFiles []InstallFileData
		if g.ComponentInstallFiles != nil {
			if instFiles, ok := g.ComponentInstallFiles[ref.Name]; ok && len(instFiles) > 0 {
				hasInstallFiles = true
				for installPath := range instFiles {
					installFiles = append(installFiles, InstallFileData{
						Filename: filepath.Base(installPath),
						Path:     filepath.Join("olm", filepath.Base(installPath)),
					})
				}
				// Sort for deterministic output
				sort.Slice(installFiles, func(i, j int) bool {
					return installFiles[i].Filename < installFiles[j].Filename
				})
			}
		}

		components = append(components, ComponentData{
			Name:            ref.Name,
			Namespace:       ref.Namespace,
			Repository:      ref.Source,
			ChartName:       chartName,
			Version:         version,
			ChartVersion:    deployer.NormalizeVersionWithDefault(ref.Version),
			HasManifests:    hasManifests,
			HasChart:        !isKustomize && !isOLM && ref.Source != "",
			IsOCI:           isOCI,
			IsKustomize:     isKustomize,
			Tag:             ref.Tag,
			Path:            ref.Path,
			IsOLM:           isOLM,
			CustomResources: customResources,
			HasInstallFiles: hasInstallFiles,
			InstallFiles:    installFiles,
			Service:         service,
		})
	}

	return components, nil
}

// generateComponentDirectories creates per-component directories with values.yaml, README.md, and optional manifests.
//
//nolint:funlen // Function orchestrates multiple generation steps (values, manifests, OLM resources)
func (g *Generator) generateComponentDirectories(ctx context.Context, components []ComponentData, outputDir string) ([]string, int64, error) {
	files := make([]string, 0, len(components)*3)
	var totalSize int64

	for i, comp := range components {
		select {
		case <-ctx.Done():
			return nil, 0, errors.Wrap(errors.ErrCodeInternal, "context cancelled", ctx.Err())
		default:
		}

		componentDir, err := deployer.SafeJoin(outputDir, comp.Name)
		if err != nil {
			return nil, 0, err
		}
		if mkdirErr := os.MkdirAll(componentDir, 0755); mkdirErr != nil {
			return nil, 0, errors.Wrap(errors.ErrCodeInternal,
				fmt.Sprintf("failed to create directory for %s", comp.Name), mkdirErr)
		}
		if !comp.IsOLM {
			// Deep-copy component values so writeClusterValuesFile can safely
			// remove dynamic paths without mutating the caller's map.
			values := component.DeepCopyMap(g.ComponentValues[comp.Name])

			// Extract dynamic paths (if any) from values into cluster-values.yaml.
			// Every component gets a cluster-values.yaml — dynamic paths are pre-populated,
			// and users can add any additional overrides. deploy.sh always passes it.
			clusterFiles, clusterSize, clusterErr := writeClusterValuesFile(values, g.DynamicValues[comp.Name], componentDir, comp.Name)
			if clusterErr != nil {
				return nil, 0, clusterErr
			}
			files = append(files, clusterFiles...)
			totalSize += clusterSize

			var valuesPath string
			var valuesSize int64
			valuesPath, valuesSize, err = deployer.WriteValuesFile(values, componentDir, "values.yaml")
			if err != nil {
				return nil, 0, errors.Wrap(errors.ErrCodeInternal,
					fmt.Sprintf("failed to write values.yaml for %s", comp.Name), err)
			}
			files = append(files, valuesPath)
			totalSize += valuesSize
		}

		// Write component README.md
		readmePath, readmeSize, err := deployer.GenerateFromTemplate(componentReadmeTemplate, comp, componentDir, "README.md")
		if err != nil {
			return nil, 0, errors.Wrap(errors.ErrCodeInternal,
				fmt.Sprintf("failed to write README.md for %s", comp.Name), err)
		}
		files = append(files, readmePath)
		totalSize += readmeSize

		// Write manifests if present
		if g.ComponentManifests != nil {
			if manifests, ok := g.ComponentManifests[comp.Name]; ok && len(manifests) > 0 {
				manifestDir, manifestDirErr := deployer.SafeJoin(componentDir, "manifests")
				if manifestDirErr != nil {
					return nil, 0, manifestDirErr
				}
				if err := os.MkdirAll(manifestDir, 0755); err != nil {
					return nil, 0, errors.Wrap(errors.ErrCodeInternal,
						fmt.Sprintf("failed to create manifests directory for %s", comp.Name), err)
				}

				// Sort manifest paths for deterministic output
				manifestPaths := make([]string, 0, len(manifests))
				for p := range manifests {
					manifestPaths = append(manifestPaths, p)
				}
				sort.Strings(manifestPaths)

				manifestsWritten := 0
				for _, manifestPath := range manifestPaths {
					content := manifests[manifestPath]
					filename := filepath.Base(manifestPath)
					outputPath, pathErr := deployer.SafeJoin(manifestDir, filename)
					if pathErr != nil {
						return nil, 0, errors.New(errors.ErrCodeInvalidRequest,
							fmt.Sprintf("invalid manifest filename %q in component %s", filename, comp.Name))
					}

					rendered, renderErr := manifest.Render(content, manifest.RenderInput{
						ComponentName: comp.Name,
						Namespace:     comp.Namespace,
						ChartName:     comp.ChartName,
						ChartVersion:  comp.ChartVersion,
						Values:        g.ComponentValues[comp.Name],
					})
					if renderErr != nil {
						return nil, 0, errors.WrapWithContext(errors.ErrCodeInternal, "failed to render manifest template", renderErr,
							map[string]any{"component": comp.Name, "filename": filename})
					}

					if !hasYAMLObjects(rendered) {
						slog.Debug("skipping empty manifest", "component", comp.Name, "filename", filename)
						continue
					}

					if err := os.WriteFile(outputPath, rendered, 0600); err != nil {
						return nil, 0, errors.WrapWithContext(errors.ErrCodeInternal, "failed to write manifest", err,
							map[string]any{"component": comp.Name, "filename": filename})
					}

					files = append(files, outputPath)
					totalSize += int64(len(rendered))
					manifestsWritten++

					slog.Debug("wrote manifest", "component", comp.Name, "filename", filename)
				}

				// If no manifests had content, remove the empty directory and update flag
				if manifestsWritten == 0 {
					if rmErr := os.RemoveAll(manifestDir); rmErr != nil {
						slog.Warn("failed to remove empty manifest directory", "dir", manifestDir, "error", rmErr)
					}
					components[i].HasManifests = false
				}
			}
		}

		// Write custom resources for OLM components
		if comp.IsOLM && g.ComponentCustomResources != nil {
			crFiles, crSize, err := g.writeCustomResources(comp.Name, componentDir, g.ComponentCustomResources)
			if err != nil {
				return nil, 0, err
			}
			files = append(files, crFiles...)
			totalSize += crSize
		}

		// Write OLM install files if present
		if g.ComponentInstallFiles != nil {
			if installFiles, ok := g.ComponentInstallFiles[comp.Name]; ok && len(installFiles) > 0 {
				installFilesWritten, installSize, err := g.writeInstallFiles(comp.Name, comp.Namespace, componentDir, installFiles)
				if err != nil {
					return nil, 0, err
				}
				files = append(files, installFilesWritten...)
				totalSize += installSize
			}
		}
	}

	return files, totalSize, nil
}

// writeCustomResources writes a single merged custom resource file for an OLM component.
// If multiple custom resources are provided (e.g., base + overlay), it uses the most specific
// one (last after sorting) which already contains merged content, and writes it as "resources.yaml".
func (g *Generator) writeCustomResources(componentName, componentDir string, componentCustomResources map[string]map[string][]byte) ([]string, int64, error) {
	customResources, ok := componentCustomResources[componentName]
	if !ok || len(customResources) == 0 {
		return nil, 0, nil
	}

	// Find the most specific custom resource (longest filename = most criteria)
	// e.g., resources-ocp-training.yaml is more specific than resources-ocp.yaml
	// The most specific one already contains merged content from GetMergedCustomResource
	var mostSpecificPath string
	var maxLen int
	for p := range customResources {
		filename := filepath.Base(p)
		if len(filename) > maxLen {
			maxLen = len(filename)
			mostSpecificPath = p
		}
	}

	content := customResources[mostSpecificPath]

	// Always write as "resources.yaml" for consistency
	outputPath, pathErr := deployer.SafeJoin(componentDir, "resources.yaml")
	if pathErr != nil {
		return nil, 0, errors.New(errors.ErrCodeInvalidRequest,
			fmt.Sprintf("invalid custom resource path for component %s", componentName))
	}

	if err := os.WriteFile(outputPath, content, 0600); err != nil {
		return nil, 0, errors.WrapWithContext(errors.ErrCodeInternal, "failed to write custom resource", err,
			map[string]any{"component": componentName, "filename": "resources.yaml"})
	}

	slog.Debug("wrote merged custom resource", "component", componentName, "source", filepath.Base(mostSpecificPath), "output", "resources.yaml")

	return []string{outputPath}, int64(len(content)), nil
}

// writeInstallFiles writes OLM install files to the component's olm/ subdirectory.
// Substitutes {{NAMESPACE}} placeholder with the actual component namespace.
// Returns paths of written files, total size, and any error.
func (g *Generator) writeInstallFiles(componentName, namespace, componentDir string, installFiles map[string][]byte) ([]string, int64, error) {
	if len(installFiles) == 0 {
		return nil, 0, nil
	}

	// Create olm/ subdirectory
	olmDir, err := deployer.SafeJoin(componentDir, "olm")
	if err != nil {
		return nil, 0, err
	}
	if err := os.MkdirAll(olmDir, 0755); err != nil {
		return nil, 0, errors.Wrap(errors.ErrCodeInternal,
			fmt.Sprintf("failed to create olm directory for %s", componentName), err)
	}

	// Sort install file paths for deterministic output
	installPaths := make([]string, 0, len(installFiles))
	for p := range installFiles {
		installPaths = append(installPaths, p)
	}
	sort.Strings(installPaths)

	var files []string
	var totalSize int64

	for _, installPath := range installPaths {
		content := installFiles[installPath]

		filename := filepath.Base(installPath)
		outputPath, pathErr := deployer.SafeJoin(olmDir, filename)
		if pathErr != nil {
			return nil, 0, errors.New(errors.ErrCodeInvalidRequest,
				fmt.Sprintf("invalid install filename %q in component %s", filename, componentName))
		}

		if err := os.WriteFile(outputPath, content, 0600); err != nil {
			return nil, 0, errors.WrapWithContext(errors.ErrCodeInternal, "failed to write install file", err,
				map[string]any{"component": componentName, "filename": filename})
		}

		files = append(files, outputPath)
		totalSize += int64(len(content))

		slog.Debug("wrote install file", "component", componentName, "filename", filename, "namespace", namespace)
	}

	return files, totalSize, nil
}

// generateRootREADME creates the root README.md with deployment instructions.
func (g *Generator) generateRootREADME(ctx context.Context, components []ComponentData, outputDir string) (string, int64, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}

	// Build criteria lines
	criteriaLines := []string{}
	if g.RecipeResult.Criteria != nil {
		c := g.RecipeResult.Criteria
		if c.Service != "" && c.Service != criteriaAny {
			criteriaLines = append(criteriaLines, fmt.Sprintf("- **Service**: %s", c.Service))
		}
		if c.Accelerator != "" && c.Accelerator != criteriaAny {
			criteriaLines = append(criteriaLines, fmt.Sprintf("- **Accelerator**: %s", c.Accelerator))
		}
		if c.Intent != "" && c.Intent != criteriaAny {
			criteriaLines = append(criteriaLines, fmt.Sprintf("- **Intent**: %s", c.Intent))
		}
		if c.OS != "" && c.OS != criteriaAny {
			criteriaLines = append(criteriaLines, fmt.Sprintf("- **OS**: %s", c.OS))
		}
	}

	var service string
	if g.RecipeResult != nil && g.RecipeResult.Criteria != nil {
		service = string(g.RecipeResult.Criteria.Service)
	}

	data := readmeTemplateData{
		RecipeVersion:      g.RecipeResult.Metadata.Version,
		BundlerVersion:     g.Version,
		Service:            service,
		Components:         components,
		ComponentsReversed: reverseComponents(components),
		Criteria:           criteriaLines,
		Constraints:        g.RecipeResult.Constraints,
	}

	readmePath, readmeSize, err := deployer.GenerateFromTemplate(readmeTemplate, data, outputDir, "README.md")
	if err != nil {
		return "", 0, err
	}

	return readmePath, readmeSize, nil
}

// generateDeployScript creates the deploy.sh automation script.
func (g *Generator) generateDeployScript(ctx context.Context, components []ComponentData, outputDir string) (string, int64, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}

	var service string
	if g.RecipeResult != nil && g.RecipeResult.Criteria != nil {
		service = string(g.RecipeResult.Criteria.Service)
	}

	data := deployTemplateData{
		BundlerVersion: g.Version,
		Service:        service,
		Components:     components,
	}

	deployPath, deploySize, err := deployer.GenerateFromTemplate(deployScriptTemplate, data, outputDir, "deploy.sh")
	if err != nil {
		return "", 0, err
	}

	// Make executable
	if err := os.Chmod(deployPath, 0755); err != nil {
		return "", 0, errors.Wrap(errors.ErrCodeInternal, "failed to set deploy.sh permissions", err)
	}

	return deployPath, deploySize, nil
}

// generateUndeployScript creates the undeploy.sh automation script.
func (g *Generator) generateUndeployScript(ctx context.Context, components []ComponentData, outputDir string) (string, int64, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}

	var service string
	if g.RecipeResult != nil && g.RecipeResult.Criteria != nil {
		service = string(g.RecipeResult.Criteria.Service)
	}

	reversed := reverseComponents(components)
	data := undeployTemplateData{
		BundlerVersion:     g.Version,
		Service:            service,
		ComponentsReversed: reversed,
		Namespaces:         uniqueNamespaces(reversed),
	}

	undeployPath, undeploySize, err := deployer.GenerateFromTemplate(undeployScriptTemplate, data, outputDir, "undeploy.sh")
	if err != nil {
		return "", 0, err
	}

	// Make executable
	if err := os.Chmod(undeployPath, 0755); err != nil {
		return "", 0, errors.Wrap(errors.ErrCodeInternal, "failed to set undeploy.sh permissions", err)
	}

	return undeployPath, undeploySize, nil
}

// generateSubscribeScript creates the subscribe.sh automation script for OLM components.
// Only generates the script if there are components with install files.
func (g *Generator) generateSubscribeScript(ctx context.Context, components []ComponentData, outputDir string) (string, int64, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}

	// Check if any components have install files
	hasOLMComponents := false
	for _, comp := range components {
		if comp.HasInstallFiles {
			hasOLMComponents = true
			break
		}
	}

	// Skip if no components have install files
	if !hasOLMComponents {
		return "", 0, nil
	}

	// Collect OLM components for template
	type OLMComponent struct {
		Name      string
		Namespace string
	}
	olmComponents := []OLMComponent{}
	for _, comp := range components {
		if comp.HasInstallFiles {
			olmComponents = append(olmComponents, OLMComponent{
				Name:      comp.Name,
				Namespace: comp.Namespace,
			})
		}
	}

	var service string
	if g.RecipeResult != nil && g.RecipeResult.Criteria != nil {
		service = string(g.RecipeResult.Criteria.Service)
	}

	data := struct {
		BundlerVersion string
		Service        string
		OLMComponents  []OLMComponent
	}{
		BundlerVersion: g.Version,
		Service:        service,
		OLMComponents:  olmComponents,
	}

	installPath, installSize, err := deployer.GenerateFromTemplate(subscribeScriptTemplate, data, outputDir, "subscribe.sh")
	if err != nil {
		return "", 0, err
	}

	// Make executable
	if err := os.Chmod(installPath, 0755); err != nil {
		return "", 0, errors.Wrap(errors.ErrCodeInternal, "failed to set subscribe.sh permissions", err)
	}

	slog.Debug("generated subscribe.sh script")

	return installPath, installSize, nil
}

// generateUnsubscribeScript creates the unsubscribe.sh automation script for OLM components.
// Only generates the script if there are components with install files.
func (g *Generator) generateUnsubscribeScript(ctx context.Context, components []ComponentData, outputDir string) (string, int64, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}

	// Check if any components have install files
	hasOLMComponents := false
	for _, comp := range components {
		if comp.HasInstallFiles {
			hasOLMComponents = true
			break
		}
	}

	// Skip if no components have install files
	if !hasOLMComponents {
		return "", 0, nil
	}

	// Collect OLM components for template (reuse same structure as install script)
	type OLMComponent struct {
		Name      string
		Namespace string
	}
	olmComponents := []OLMComponent{}
	for _, comp := range components {
		if comp.HasInstallFiles {
			olmComponents = append(olmComponents, OLMComponent{
				Name:      comp.Name,
				Namespace: comp.Namespace,
			})
		}
	}

	// Reverse the order for uninstall (dependencies first)
	olmComponentsReversed := make([]OLMComponent, len(olmComponents))
	for i, comp := range olmComponents {
		olmComponentsReversed[len(olmComponents)-1-i] = comp
	}

	var service string
	if g.RecipeResult != nil && g.RecipeResult.Criteria != nil {
		service = string(g.RecipeResult.Criteria.Service)
	}

	data := struct {
		BundlerVersion string
		Service        string
		OLMComponents  []OLMComponent
	}{
		BundlerVersion: g.Version,
		Service:        service,
		OLMComponents:  olmComponentsReversed,
	}

	uninstallPath, uninstallSize, err := deployer.GenerateFromTemplate(unsubscribeScriptTemplate, data, outputDir, "unsubscribe.sh")
	if err != nil {
		return "", 0, err
	}

	// Make executable
	if err := os.Chmod(uninstallPath, 0755); err != nil {
		return "", 0, errors.Wrap(errors.ErrCodeInternal, "failed to set unsubscribe.sh permissions", err)
	}

	slog.Debug("generated unsubscribe.sh script")

	return uninstallPath, uninstallSize, nil
}

// readmeTemplateData is the template data for root README.md generation.
type readmeTemplateData struct {
	RecipeVersion      string
	BundlerVersion     string
	Service            string // Kubernetes service type (ocp, eks, gke, etc.)
	Components         []ComponentData
	ComponentsReversed []ComponentData
	Criteria           []string
	Constraints        []recipe.Constraint
}

// deployTemplateData is the template data for deploy.sh generation.
type deployTemplateData struct {
	BundlerVersion string
	Service        string // Kubernetes service type (ocp, eks, gke, etc.)
	Components     []ComponentData
}

// undeployTemplateData is the template data for undeploy.sh generation.
type undeployTemplateData struct {
	BundlerVersion     string
	Service            string // Kubernetes service type (ocp, eks, gke, etc.)
	ComponentsReversed []ComponentData
	Namespaces         []string // unique namespaces in reverse-deployment order
}

// reverseComponents returns a reversed copy of the component list (for uninstall order).
func reverseComponents(components []ComponentData) []ComponentData {
	reversed := make([]ComponentData, len(components))
	for i, comp := range components {
		reversed[len(components)-1-i] = comp
	}
	return reversed
}

// uniqueNamespaces returns deduplicated namespaces from Helm/Kustomize components,
// preserving order. Manifest-only components and OLM components are excluded.
// OLM namespaces are managed by unsubscribe.sh instead.
func uniqueNamespaces(components []ComponentData) []string {
	seen := make(map[string]bool)
	var namespaces []string
	for _, c := range components {
		if c.Namespace != "" && !seen[c.Namespace] && (c.HasChart || c.IsKustomize) {
			seen[c.Namespace] = true
			namespaces = append(namespaces, c.Namespace)
		}
	}
	return namespaces
}

// writeClusterValuesFile writes a cluster-values.yaml for per-cluster overrides.
// If dynamicPaths is non-empty, those paths are extracted from values and pre-populated.
// WARNING: This function mutates the values map in place (removes dynamic paths via
// RemoveValueByPath). Callers must pass a deep copy if the original map must be preserved.
// The file is always written — even when empty — so users can add any overrides.
func writeClusterValuesFile(values map[string]any, dynamicPaths []string, componentDir, componentName string) ([]string, int64, error) {
	clusterValues := make(map[string]any)
	for _, path := range dynamicPaths {
		val, found := component.GetValueByPath(values, path)
		if found {
			component.RemoveValueByPath(values, path)
		} else {
			val = ""
			slog.Warn("dynamic path not found in component values; introducing empty placeholder",
				"component", componentName, "path", path)
		}
		component.SetValueByPath(clusterValues, path, val)
	}

	clusterPath, clusterSize, err := deployer.WriteValuesFile(clusterValues, componentDir, "cluster-values.yaml")
	if err != nil {
		return nil, 0, errors.Wrap(errors.ErrCodeInternal,
			fmt.Sprintf("failed to write cluster-values.yaml for %s", componentName), err)
	}

	slog.Debug("wrote cluster-values.yaml", "component", componentName, "dynamic_paths", len(dynamicPaths))
	return []string{clusterPath}, clusterSize, nil
}

// hasYAMLObjects returns true if content contains at least one YAML object
// (a non-comment, non-blank, non-separator line).
func hasYAMLObjects(content []byte) bool {
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" {
			continue
		}
		return true
	}
	return false
}
