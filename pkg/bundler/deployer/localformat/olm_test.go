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

package localformat_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NVIDIA/aicr/pkg/bundler/deployer/localformat"
)

func TestWrite_OLM(t *testing.T) {
	outDir := t.TempDir()

	folders, err := localformat.Write(context.Background(), localformat.Options{
		OutputDir: outDir,
		Components: []localformat.Component{{
			Name:          "gpu-operator",
			Namespace:     "nvidia-gpu-operator",
			IsOLM:         true,
			InstallFile:   "components/gpu-operator/olm/install.yaml",
			ResourcesFile: "components/gpu-operator/olm/resources-ocp.yaml",
		}},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(folders) != 1 {
		t.Fatalf("want 1 OLM folder, got %d", len(folders))
	}
	if folders[0].Kind != localformat.KindOLM {
		t.Fatalf("want OLM folder kind=%v, got %v", localformat.KindOLM, folders[0].Kind)
	}
	if folders[0].Dir != "001-gpu-operator" {
		t.Errorf("folder dir = %q, want 001-gpu-operator", folders[0].Dir)
	}
	if folders[0].Name != "gpu-operator" {
		t.Errorf("folder name = %q, want gpu-operator", folders[0].Name)
	}

	// Verify files exist
	for _, rel := range []string{"olm.sh", "install.yaml", "resources-ocp.yaml"} {
		path := filepath.Join(outDir, "001-gpu-operator", rel)
		if _, statErr := os.Stat(path); statErr != nil {
			t.Errorf("missing file %s: %v", rel, statErr)
		}
	}

	// olm.sh must be executable
	info, err := os.Stat(filepath.Join(outDir, "001-gpu-operator", "olm.sh"))
	if err != nil {
		t.Fatalf("stat olm.sh: %v", err)
	}
	if info.Mode()&0111 == 0 {
		t.Errorf("olm.sh is not executable, mode=%o", info.Mode())
	}

	// Verify olm.sh contains expected content (uses oc not kubectl)
	olmScript, err := os.ReadFile(filepath.Join(outDir, "001-gpu-operator", "olm.sh"))
	if err != nil {
		t.Fatalf("read olm.sh: %v", err)
	}
	olmContent := string(olmScript)

	// Check for oc commands
	for _, check := range []string{
		"oc create namespace",
		"oc apply",
		"oc delete",
		"oc get",
		"subscribe|unsubscribe|apply|delete",
		"nvidia-gpu-operator",
		"gpu-operator",
	} {
		if !strings.Contains(olmContent, check) {
			t.Errorf("olm.sh missing expected content: %q", check)
		}
	}

	// Check that kubectl is NOT present (should use oc)
	if strings.Contains(olmContent, "kubectl") {
		t.Errorf("olm.sh contains kubectl, should use oc instead")
	}

	// Verify upstream.env and Chart.yaml do NOT exist (OLM is not Helm)
	for _, file := range []string{"upstream.env", "Chart.yaml", "values.yaml", "install.sh"} {
		if _, err := os.Stat(filepath.Join(outDir, "001-gpu-operator", file)); !os.IsNotExist(err) {
			t.Errorf("%s must not exist for OLM folder", file)
		}
	}
}

func TestWrite_OLMMultiple(t *testing.T) {
	outDir := t.TempDir()

	folders, err := localformat.Write(context.Background(), localformat.Options{
		OutputDir: outDir,
		Components: []localformat.Component{
			{
				Name:          "gpu-operator",
				Namespace:     "nvidia-gpu-operator",
				IsOLM:         true,
				InstallFile:   "components/gpu-operator/olm/install.yaml",
				ResourcesFile: "components/gpu-operator/olm/resources-ocp.yaml",
			},
			{
				Name:          "nfd",
				Namespace:     "node-feature-discovery",
				IsOLM:         true,
				InstallFile:   "components/nfd/olm/install.yaml",
				ResourcesFile: "components/nfd/olm/resources-ocp.yaml",
			},
		},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(folders) != 2 {
		t.Fatalf("want 2 folders, got %d", len(folders))
	}

	// Check ordering and types
	if folders[0].Dir != "001-gpu-operator" || folders[0].Kind != localformat.KindOLM {
		t.Errorf("folders[0] = %+v, want 001-gpu-operator / OLM", folders[0])
	}
	if folders[1].Dir != "002-nfd" || folders[1].Kind != localformat.KindOLM {
		t.Errorf("folders[1] = %+v, want 002-nfd / OLM", folders[1])
	}

	// Verify both have olm.sh
	for i, name := range []string{"001-gpu-operator", "002-nfd"} {
		olmPath := filepath.Join(outDir, name, "olm.sh")
		if _, err := os.Stat(olmPath); err != nil {
			t.Errorf("folder %d missing olm.sh: %v", i, err)
		}
	}
}

func TestWrite_OLMMixed(t *testing.T) {
	outDir := t.TempDir()

	// Mix OLM and Helm components
	folders, err := localformat.Write(context.Background(), localformat.Options{
		OutputDir: outDir,
		Components: []localformat.Component{
			{
				Name:       "cert-manager",
				Namespace:  "cert-manager",
				Repository: "https://charts.jetstack.io",
				ChartName:  "cert-manager",
				Version:    "v1.13.0",
			},
			{
				Name:          "gpu-operator",
				Namespace:     "nvidia-gpu-operator",
				IsOLM:         true,
				InstallFile:   "components/gpu-operator/olm/install.yaml",
				ResourcesFile: "components/gpu-operator/olm/resources-ocp.yaml",
			},
			{
				Name:       "kube-prometheus-stack",
				Namespace:  "monitoring",
				Repository: "https://prometheus-community.github.io/helm-charts",
				ChartName:  "kube-prometheus-stack",
				Version:    "v55.0.0",
			},
		},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(folders) != 3 {
		t.Fatalf("want 3 folders, got %d", len(folders))
	}

	// Check types
	if folders[0].Kind != localformat.KindUpstreamHelm {
		t.Errorf("folders[0] should be upstream-helm, got %v", folders[0].Kind)
	}
	if folders[1].Kind != localformat.KindOLM {
		t.Errorf("folders[1] should be OLM, got %v", folders[1].Kind)
	}
	if folders[2].Kind != localformat.KindUpstreamHelm {
		t.Errorf("folders[2] should be upstream-helm, got %v", folders[2].Kind)
	}

	// Helm components have install.sh, OLM has olm.sh
	if _, err := os.Stat(filepath.Join(outDir, "001-cert-manager", "install.sh")); err != nil {
		t.Errorf("cert-manager missing install.sh: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "002-gpu-operator", "olm.sh")); err != nil {
		t.Errorf("gpu-operator missing olm.sh: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "003-kube-prometheus-stack", "install.sh")); err != nil {
		t.Errorf("kube-prometheus-stack missing install.sh: %v", err)
	}

	// OLM should NOT have Helm files
	if _, err := os.Stat(filepath.Join(outDir, "002-gpu-operator", "install.sh")); !os.IsNotExist(err) {
		t.Errorf("OLM component should not have install.sh")
	}
	if _, err := os.Stat(filepath.Join(outDir, "002-gpu-operator", "Chart.yaml")); !os.IsNotExist(err) {
		t.Errorf("OLM component should not have Chart.yaml")
	}
}

func TestWrite_OLMWithoutResourcesFile(t *testing.T) {
	outDir := t.TempDir()

	// OLM component without resources file (only subscription)
	folders, err := localformat.Write(context.Background(), localformat.Options{
		OutputDir: outDir,
		Components: []localformat.Component{{
			Name:          "gpu-operator",
			Namespace:     "nvidia-gpu-operator",
			IsOLM:         true,
			InstallFile:   "components/gpu-operator/olm/install.yaml",
			ResourcesFile: "", // No resources file
		}},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(folders) != 1 {
		t.Fatalf("want 1 folder, got %d", len(folders))
	}

	// Should have olm.sh and install.yaml but no resources file
	for _, file := range []string{"olm.sh", "install.yaml"} {
		if _, statErr := os.Stat(filepath.Join(outDir, "001-gpu-operator", file)); statErr != nil {
			t.Errorf("missing %s: %v", file, statErr)
		}
	}

	// Verify olm.sh handles missing resources file
	olmScript, err := os.ReadFile(filepath.Join(outDir, "001-gpu-operator", "olm.sh"))
	if err != nil {
		t.Fatalf("read olm.sh: %v", err)
	}
	olmContent := string(olmScript)

	// Should contain "No custom resources" message for apply/delete modes
	if !strings.Contains(olmContent, "No custom resources") {
		t.Errorf("olm.sh should handle missing resources file with 'No custom resources' message")
	}
}

func TestClassify_OLM(t *testing.T) {
	// Test that classify correctly identifies OLM components
	tests := []struct {
		name     string
		comp     localformat.Component
		wantKind localformat.FolderKind
	}{
		{
			name: "OLM component",
			comp: localformat.Component{
				Name:        "gpu-operator",
				IsOLM:       true,
				InstallFile: "components/gpu-operator/olm/install.yaml",
			},
			wantKind: localformat.KindOLM,
		},
		{
			name: "upstream helm component",
			comp: localformat.Component{
				Name:       "cert-manager",
				Repository: "https://charts.jetstack.io",
			},
			wantKind: localformat.KindUpstreamHelm,
		},
		{
			name: "local helm component",
			comp: localformat.Component{
				Name:       "custom",
				Repository: "",
			},
			wantKind: localformat.KindLocalHelm,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outDir := t.TempDir()
			manifests := make(map[string][]byte)
			if tt.wantKind == localformat.KindLocalHelm && tt.comp.Repository == "" {
				manifests["test.yaml"] = []byte("apiVersion: v1\nkind: ConfigMap\n")
			}

			folders, err := localformat.Write(context.Background(), localformat.Options{
				OutputDir:  outDir,
				Components: []localformat.Component{tt.comp},
				ComponentManifests: map[string]map[string][]byte{
					tt.comp.Name: manifests,
				},
			})
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
			if len(folders) == 0 {
				t.Fatalf("no folders generated")
			}
			if folders[0].Kind != tt.wantKind {
				t.Errorf("got kind %v, want %v", folders[0].Kind, tt.wantKind)
			}
		})
	}
}

func TestWrite_OLMFolderStructure(t *testing.T) {
	outDir := t.TempDir()

	folders, err := localformat.Write(context.Background(), localformat.Options{
		OutputDir: outDir,
		Components: []localformat.Component{{
			Name:          "nfd",
			Namespace:     "node-feature-discovery",
			IsOLM:         true,
			InstallFile:   "components/nfd/olm/install.yaml",
			ResourcesFile: "components/nfd/olm/resources-ocp.yaml",
		}},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Check folder metadata
	if len(folders) != 1 {
		t.Fatalf("want 1 folder, got %d", len(folders))
	}
	f := folders[0]
	if f.Index != 1 {
		t.Errorf("Index = %d, want 1", f.Index)
	}
	if f.Dir != "001-nfd" {
		t.Errorf("Dir = %q, want 001-nfd", f.Dir)
	}
	if f.Kind != localformat.KindOLM {
		t.Errorf("Kind = %v, want KindOLM", f.Kind)
	}
	if f.Name != "nfd" {
		t.Errorf("Name = %q, want nfd", f.Name)
	}
	if f.Parent != "nfd" {
		t.Errorf("Parent = %q, want nfd", f.Parent)
	}

	// Check files list
	expectedFiles := []string{
		"001-nfd/install.yaml",
		"001-nfd/resources-ocp.yaml",
		"001-nfd/olm.sh",
	}
	if len(f.Files) != len(expectedFiles) {
		t.Errorf("got %d files, want %d", len(f.Files), len(expectedFiles))
	}
	for i, expected := range expectedFiles {
		if i >= len(f.Files) {
			break
		}
		if f.Files[i] != expected {
			t.Errorf("Files[%d] = %q, want %q", i, f.Files[i], expected)
		}
	}
}
