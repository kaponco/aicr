// Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES.  All rights reserved.
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
	"strings"
	"text/template"

	"github.com/NVIDIA/aicr/pkg/bundler/deployer"
	"github.com/NVIDIA/aicr/pkg/errors"
	"github.com/NVIDIA/aicr/pkg/recipe"
)

//go:embed templates/install-direct.sh.tmpl
var installDirectTemplate string

//go:embed templates/uninstall-direct.sh.tmpl
var uninstallDirectTemplate string

// writeDirectFolder emits a Direct deployment folder containing:
//   - Static YAML manifest file (copied from SourceFile)
//   - install.sh script that runs `kubectl apply -f <manifest> -n <namespace>`
//
// Direct components use static YAML manifests embedded in AICR (no Helm, no templating).
// The install.sh script applies the YAML using kubectl instead of helm install.
func writeDirectFolder(outputDir, dir string, idx int, c Component) (Folder, error) {
	folderDir, err := deployer.SafeJoin(outputDir, dir)
	if err != nil {
		return Folder{}, errors.Wrap(errors.ErrCodeInvalidRequest, "unsafe folder path", err)
	}
	if mkdirErr := os.MkdirAll(folderDir, 0o755); mkdirErr != nil {
		return Folder{}, errors.Wrap(errors.ErrCodeInternal, "create direct folder", mkdirErr)
	}

	files := make([]string, 0)

	// Copy the static YAML file from the source location
	// Strip "recipes/" prefix if present (embedded FS doesn't include it)
	sourcePath := strings.TrimPrefix(c.SourceFile, "recipes/")
	manifestContent, err := recipe.GetDataProvider().ReadFile(sourcePath)
	if err != nil {
		return Folder{}, errors.Wrap(errors.ErrCodeInternal,
			fmt.Sprintf("failed to read source file %s for component %s", c.SourceFile, c.Name), err)
	}

	// Extract the filename from the source path
	manifestFilename := filepath.Base(c.SourceFile)
	manifestPath, joinErr := deployer.SafeJoin(folderDir, manifestFilename)
	if joinErr != nil {
		return Folder{}, errors.Wrap(errors.ErrCodeInvalidRequest, "unsafe manifest path", joinErr)
	}
	if err := writeFile(manifestPath, manifestContent, 0o600); err != nil {
		return Folder{}, err
	}
	files = append(files, filepath.Join(dir, manifestFilename))

	// Generate install.sh
	tmpl, parseErr := template.New("install-direct.sh").Parse(installDirectTemplate)
	if parseErr != nil {
		return Folder{}, errors.Wrap(errors.ErrCodeInternal, "parse install-direct.sh template", parseErr)
	}

	data := struct {
		ComponentName    string
		Namespace        string
		ManifestFilename string
		Olm              bool
	}{
		ComponentName:    c.Name,
		Namespace:        c.Namespace,
		ManifestFilename: manifestFilename,
		Olm:              c.Olm,
	}

	if err := renderTemplateToFile(tmpl, data, folderDir, "install.sh", 0o755); err != nil {
		return Folder{}, err
	}
	files = append(files, filepath.Join(dir, "install.sh"))

	// Generate uninstall.sh
	uninstallTmpl, parseErr := template.New("uninstall-direct.sh").Parse(uninstallDirectTemplate)
	if parseErr != nil {
		return Folder{}, errors.Wrap(errors.ErrCodeInternal, "parse uninstall-direct.sh template", parseErr)
	}

	if err := renderTemplateToFile(uninstallTmpl, data, folderDir, "uninstall.sh", 0o755); err != nil {
		return Folder{}, err
	}
	files = append(files, filepath.Join(dir, "uninstall.sh"))

	return Folder{
		Index:  idx,
		Dir:    dir,
		Kind:   KindDirect,
		Name:   c.Name,
		Parent: c.Name,
		Files:  files,
	}, nil
}
