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

package localformat

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/NVIDIA/aicr/pkg/bundler/deployer"
	"github.com/NVIDIA/aicr/pkg/errors"
	"github.com/NVIDIA/aicr/pkg/recipe"
)

//go:embed templates/olm.sh.tmpl
var olmScriptTemplate string

var olmScriptTmpl = template.Must(
	template.New("olm.sh").Parse(olmScriptTemplate),
)

// olmScriptData carries template data for rendering OLM olm.sh.
type olmScriptData struct {
	Name              string
	Namespace         string
	ResourcesFile     string // Full path (e.g., "components/gpu-operator/olm/resources-ocp.yaml")
	ResourcesFileName string // Just the filename (e.g., "resources-ocp.yaml")
}

// writeOLMFolder writes an OLM component folder containing:
// - install.yaml (Subscription, OperatorGroup, etc.) - applied by subscribe.sh
// - resources-*.yaml (custom resources) - applied by deploy.sh via olm.sh
// - olm.sh - script to manage OLM operator lifecycle
//
// OLM components do not use Helm, so no Chart.yaml or values.yaml are written.
func writeOLMFolder(outputDir, dir string, index int, c Component) (Folder, error) {
	folderPath, pathErr := deployer.SafeJoin(outputDir, dir)
	if pathErr != nil {
		return Folder{}, pathErr
	}

	if err := os.MkdirAll(folderPath, 0755); err != nil {
		return Folder{}, errors.Wrap(errors.ErrCodeInternal,
			fmt.Sprintf("create folder for %s", c.Name), err)
	}

	files := make([]string, 0)

	// Get the embedded filesystem
	fs := recipe.GetEmbeddedFS()

	// Copy install.yaml if present
	if c.InstallFile != "" {
		content, err := fs.ReadFile(c.InstallFile)
		if err != nil {
			return Folder{}, errors.Wrap(errors.ErrCodeInternal,
				fmt.Sprintf("read OLM install file %s", c.InstallFile), err)
		}

		installPath, err := deployer.SafeJoin(folderPath, "install.yaml")
		if err != nil {
			return Folder{}, err
		}

		if err := os.WriteFile(installPath, content, 0600); err != nil {
			return Folder{}, errors.Wrap(errors.ErrCodeInternal,
				"write install.yaml", err)
		}

		relPath := filepath.Join(dir, "install.yaml")
		files = append(files, relPath)
	}

	// Copy resources file if present
	resourcesFileName := ""
	if c.ResourcesFile != "" {
		content, err := fs.ReadFile(c.ResourcesFile)
		if err != nil {
			return Folder{}, errors.Wrap(errors.ErrCodeInternal,
				fmt.Sprintf("read OLM resources file %s", c.ResourcesFile), err)
		}

		// Use the same filename as in the source path (e.g., resources-ocp.yaml)
		filename := filepath.Base(c.ResourcesFile)
		resourcesFileName = filename
		resourcesPath, err := deployer.SafeJoin(folderPath, filename)
		if err != nil {
			return Folder{}, err
		}

		if err := os.WriteFile(resourcesPath, content, 0600); err != nil {
			return Folder{}, errors.Wrap(errors.ErrCodeInternal,
				fmt.Sprintf("write %s", filename), err)
		}

		relPath := filepath.Join(dir, filename)
		files = append(files, relPath)
	}

	// Generate olm.sh to manage OLM operator lifecycle (called by base scripts)
	data := olmScriptData{
		Name:              c.Name,
		Namespace:         c.Namespace,
		ResourcesFile:     c.ResourcesFile,
		ResourcesFileName: resourcesFileName,
	}
	if err := renderTemplateToFile(olmScriptTmpl, data, folderPath, "olm.sh", 0755); err != nil {
		return Folder{}, errors.Wrap(errors.ErrCodeInternal,
			fmt.Sprintf("render olm.sh for %s", c.Name), err)
	}
	files = append(files, filepath.Join(dir, "olm.sh"))

	return Folder{
		Index:  index,
		Dir:    dir,
		Kind:   KindOLM,
		Name:   c.Name,
		Parent: c.Name,
		Files:  files,
	}, nil
}
