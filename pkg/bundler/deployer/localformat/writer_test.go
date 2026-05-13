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

package localformat_test

import (
	"context"
	stderrors "errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/NVIDIA/aicr/pkg/bundler/deployer/localformat"
	"github.com/NVIDIA/aicr/pkg/errors"
)

var update = flag.Bool("update", false, "update golden files")

func TestWrite_UpstreamHelmOnly(t *testing.T) {
	outDir := t.TempDir()

	res, err := localformat.Write(context.Background(), localformat.Options{
		OutputDir: outDir,
		Components: []localformat.Component{{
			Name:       "nfd",
			Namespace:  "node-feature-discovery",
			Repository: "https://kubernetes-sigs.github.io/node-feature-discovery/charts",
			ChartName:  "node-feature-discovery",
			Version:    "v0.16.1",
			Values:     map[string]any{"image": map[string]any{"tag": "v0.16.1"}},
		}},
	})
	folders := res.Folders
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if len(folders) != 1 {
		t.Fatalf("want 1 folder, got %d", len(folders))
	}
	if got, want := folders[0].Dir, "001-nfd"; got != want {
		t.Errorf("folders[0].Dir = %q, want %q", got, want)
	}
	if got, want := folders[0].Kind, localformat.KindUpstreamHelm; got != want {
		t.Errorf("folders[0].Kind = %v, want %v", got, want)
	}

	// Files written on disk
	for _, rel := range []string{"install.sh", "values.yaml", "cluster-values.yaml", "upstream.env"} {
		if _, err := os.Stat(filepath.Join(outDir, "001-nfd", rel)); err != nil {
			t.Errorf("missing file %s: %v", rel, err)
		}
	}
	// No Chart.yaml for upstream-helm
	if _, err := os.Stat(filepath.Join(outDir, "001-nfd", "Chart.yaml")); !os.IsNotExist(err) {
		t.Errorf("Chart.yaml must not exist for upstream-helm folder")
	}

	// Golden-file compare for install.sh + upstream.env
	assertGolden(t, outDir, "testdata/upstream_helm_only", "001-nfd/install.sh")
	assertGolden(t, outDir, "testdata/upstream_helm_only", "001-nfd/upstream.env")
}

func TestWrite_LocalHelmManifestOnly(t *testing.T) {
	outDir := t.TempDir()

	res, err := localformat.Write(context.Background(), localformat.Options{
		OutputDir: outDir,
		Components: []localformat.Component{{
			Name:       "skyhook-customizations",
			Namespace:  "skyhook",
			Repository: "", // empty: manifest-only
		}},
		ComponentManifests: map[string]map[string][]byte{
			"skyhook-customizations": {
				// Realistic input: project recipe manifests carry a license header
				// (see recipes/components/gpu-operator/manifests/dcgm-exporter.yaml).
				"components/skyhook-customizations/manifests/customization.yaml": []byte(`# Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES.  All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

apiVersion: v1
kind: ConfigMap
metadata:
  name: x
`),
			},
		},
	})
	folders := res.Folders
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(folders) != 1 || folders[0].Kind != localformat.KindLocalHelm {
		t.Fatalf("want 1 local-helm folder, got %d folders kind=%v", len(folders), folders[0].Kind)
	}

	for _, rel := range []string{"install.sh", "values.yaml", "cluster-values.yaml", "Chart.yaml", "templates/customization.yaml"} {
		if _, err := os.Stat(filepath.Join(outDir, "001-skyhook-customizations", rel)); err != nil {
			t.Errorf("missing file %s: %v", rel, err)
		}
	}
	// upstream.env MUST NOT exist for local-helm
	if _, err := os.Stat(filepath.Join(outDir, "001-skyhook-customizations", "upstream.env")); !os.IsNotExist(err) {
		t.Errorf("upstream.env must not exist for local-helm folder")
	}

	assertGolden(t, outDir, "testdata/local_helm_manifest_only", "001-skyhook-customizations/install.sh")
	assertGolden(t, outDir, "testdata/local_helm_manifest_only", "001-skyhook-customizations/Chart.yaml")
	assertGolden(t, outDir, "testdata/local_helm_manifest_only", "001-skyhook-customizations/templates/customization.yaml")
}

func TestWrite_Mixed(t *testing.T) {
	outDir := t.TempDir()

	res, err := localformat.Write(context.Background(), localformat.Options{
		OutputDir: outDir,
		Components: []localformat.Component{{
			Name:       "gpu-operator",
			Namespace:  "gpu-operator",
			Repository: "https://nvidia.github.io/gpu-operator",
			ChartName:  "nvidia/gpu-operator",
			Version:    "v24.9.1",
		}},
		ComponentManifests: map[string]map[string][]byte{
			"gpu-operator": {
				// Realistic: real project manifests carry a license header.
				"components/gpu-operator/manifests/dcgm-exporter.yaml": []byte(`# Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES.  All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

apiVersion: v1
kind: Service
metadata:
  name: dcgm
`),
			},
		},
	})
	folders := res.Folders
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if len(folders) != 2 {
		t.Fatalf("want 2 folders, got %d", len(folders))
	}
	if folders[0].Dir != "001-gpu-operator" || folders[0].Kind != localformat.KindUpstreamHelm {
		t.Errorf("folders[0] = %+v, want 001-gpu-operator / upstream-helm", folders[0])
	}
	if folders[1].Dir != "002-gpu-operator-post" || folders[1].Kind != localformat.KindLocalHelm {
		t.Errorf("folders[1] = %+v, want 002-gpu-operator-post / local-helm", folders[1])
	}
	if folders[1].Parent != "gpu-operator" {
		t.Errorf("folders[1].Parent = %q, want gpu-operator", folders[1].Parent)
	}
	if folders[1].Name != "gpu-operator-post" {
		t.Errorf("folders[1].Name = %q, want gpu-operator-post", folders[1].Name)
	}

	// Primary has NO Chart.yaml (upstream-helm)
	if _, err := os.Stat(filepath.Join(outDir, "001-gpu-operator", "Chart.yaml")); !os.IsNotExist(err) {
		t.Errorf("primary must not have Chart.yaml")
	}
	// Post HAS Chart.yaml + templates/dcgm-exporter.yaml
	if _, err := os.Stat(filepath.Join(outDir, "002-gpu-operator-post", "Chart.yaml")); err != nil {
		t.Errorf("post must have Chart.yaml: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "002-gpu-operator-post", "templates", "dcgm-exporter.yaml")); err != nil {
		t.Errorf("post must have templates/dcgm-exporter.yaml: %v", err)
	}

	// Post's upstream.env MUST NOT exist (wrapped chart, not upstream ref)
	if _, err := os.Stat(filepath.Join(outDir, "002-gpu-operator-post", "upstream.env")); !os.IsNotExist(err) {
		t.Errorf("post must not have upstream.env")
	}
}

func TestWrite_Ordering(t *testing.T) {
	outDir := t.TempDir()
	mk := func(name, repo string) localformat.Component {
		return localformat.Component{
			Name:       name,
			Namespace:  name,
			Repository: repo,
			ChartName:  name,
			Version:    "v1.0.0",
		}
	}

	// b is mixed: helm repo set + manifests → emits b primary + b-post injected
	res, err := localformat.Write(context.Background(), localformat.Options{
		OutputDir: outDir,
		Components: []localformat.Component{
			mk("a", "https://a.example"),
			mk("b", "https://b.example"),
			mk("c", "https://c.example"),
		},
		ComponentManifests: map[string]map[string][]byte{
			"b": {
				"b/manifests/x.yaml": []byte(`# Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES.  All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

apiVersion: v1
kind: ConfigMap
metadata:
  name: x
`),
			},
		},
	})
	folders := res.Folders
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := make([]string, 0, len(folders))
	for _, f := range folders {
		got = append(got, f.Dir)
	}
	want := []string{"001-a", "002-b", "003-b-post", "004-c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("folder order = %v, want %v", got, want)
	}

	// Verify the primary/post relationship on b
	if folders[1].Kind != localformat.KindUpstreamHelm {
		t.Errorf("folders[1] (b) = %v, want KindUpstreamHelm", folders[1].Kind)
	}
	if folders[2].Kind != localformat.KindLocalHelm || folders[2].Parent != "b" || folders[2].Name != "b-post" {
		t.Errorf("folders[2] (b-post) = %+v, want KindLocalHelm parent=b name=b-post", folders[2])
	}

	// Verify subsequent indices are correct on the Folder struct itself (not just the Dir)
	wantIndices := []int{1, 2, 3, 4}
	for i, f := range folders {
		if f.Index != wantIndices[i] {
			t.Errorf("folders[%d].Index = %d, want %d (dir=%s)", i, f.Index, wantIndices[i], f.Dir)
		}
	}
}

func TestWrite_Kustomize(t *testing.T) {
	outDir := t.TempDir()

	// Absolute path to the kustomize fixture. `filepath.Abs` resolves the
	// test-relative "testdata/kustomize_input" to something buildKustomize
	// can feed to kustomize's on-disk filesystem.
	kustomizePath, err := filepath.Abs(filepath.Join("testdata", "kustomize_input"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}

	res, err := localformat.Write(context.Background(), localformat.Options{
		OutputDir: outDir,
		Components: []localformat.Component{{
			Name:      "my-kustomize",
			Namespace: "mk",
			// Local kustomize: Path only. Tag/Repository are only meaningful
			// for git-sourced kustomizations and are validated as a pair by
			// Write — a Tag without Repository would (correctly) be rejected.
			Path: kustomizePath,
		}},
	})
	folders := res.Folders
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(folders) != 1 || folders[0].Kind != localformat.KindLocalHelm {
		t.Fatalf("want 1 local-helm folder (kustomize wrapped), got %d folders kind=%v", len(folders), folders[0].Kind)
	}

	// manifest.yaml is the single flattened output of kustomize build
	manifestPath := filepath.Join(outDir, "001-my-kustomize", "templates", "manifest.yaml")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Errorf("missing templates/manifest.yaml: %v", err)
	}
	// Chart.yaml should still exist (wrapped chart)
	if _, err := os.Stat(filepath.Join(outDir, "001-my-kustomize", "Chart.yaml")); err != nil {
		t.Errorf("missing Chart.yaml: %v", err)
	}
}

func TestWrite_DirectWithRealFiles(t *testing.T) {
	// This test uses actual embedded files from the recipes directory
	// Testing with real gpu-operator-olm OLM manifest
	outDir := t.TempDir()

	res, err := localformat.Write(context.Background(), localformat.Options{
		OutputDir: outDir,
		Components: []localformat.Component{{
			Name:       "gpu-operator-olm",
			Namespace:  "nvidia-gpu-operator",
			SourceFile: "recipes/components/gpu-operator-olm/direct/olm.yaml",
			Olm:        true,
		}},
	})
	folders := res.Folders
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if len(folders) != 1 {
		t.Fatalf("want 1 folder, got %d", len(folders))
	}
	if got, want := folders[0].Dir, "001-gpu-operator-olm"; got != want {
		t.Errorf("folders[0].Dir = %q, want %q", got, want)
	}
	if got, want := folders[0].Kind, localformat.KindDirect; got != want {
		t.Errorf("folders[0].Kind = %v, want %v", got, want)
	}

	// Verify files written on disk
	for _, rel := range []string{"install.sh", "uninstall.sh", "olm.yaml"} {
		path := filepath.Join(outDir, "001-gpu-operator-olm", rel)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing file %s: %v", rel, err)
		}
	}

	// Verify install.sh contains OLM CSV wait logic
	installPath := filepath.Join(outDir, "001-gpu-operator-olm", "install.sh")
	installContent, err := os.ReadFile(installPath)
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	expectedStrings := []string{
		"kubectl apply",
		"olm.yaml",
		"ClusterServiceVersion",
		"reached Succeeded phase",
		"kubectl get subscription",
		"installedCSV",
		"TIMEOUT=",
		"-n nvidia-gpu-operator",
	}
	for _, expected := range expectedStrings {
		if !strings.Contains(string(installContent), expected) {
			t.Errorf("OLM install.sh missing %q", expected)
		}
	}

	// Verify uninstall.sh contains CSV deletion logic
	uninstallPath := filepath.Join(outDir, "001-gpu-operator-olm", "uninstall.sh")
	uninstallContent, err := os.ReadFile(uninstallPath)
	if err != nil {
		t.Fatalf("read uninstall.sh: %v", err)
	}
	// Verify it queries subscriptions to find CSVs
	if !strings.Contains(string(uninstallContent), "kubectl get subscription") {
		t.Errorf("OLM uninstall.sh should query Subscriptions to find CSVs")
	}
	if !strings.Contains(string(uninstallContent), "installedCSV") {
		t.Errorf("OLM uninstall.sh should extract installedCSV from Subscription")
	}
	if !strings.Contains(string(uninstallContent), "kubectl delete csv") {
		t.Errorf("OLM uninstall.sh should contain CSV deletion logic")
	}
	if !strings.Contains(string(uninstallContent), "kubectl delete") {
		t.Errorf("uninstall.sh should contain kubectl delete")
	}

	// Verify install.sh is executable
	installInfo, err := os.Stat(installPath)
	if err != nil {
		t.Fatalf("stat install.sh: %v", err)
	}
	if installInfo.Mode()&0o111 == 0 {
		t.Errorf("install.sh should be executable, got mode %v", installInfo.Mode())
	}

	// Verify manifest content was copied from embedded FS
	manifestPath := filepath.Join(outDir, "001-gpu-operator-olm", "olm.yaml")
	manifestContent, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read olm.yaml: %v", err)
	}
	// Verify it contains expected OLM resources
	manifestStr := string(manifestContent)
	if !strings.Contains(manifestStr, "kind: OperatorGroup") {
		t.Errorf("olm.yaml should contain OperatorGroup")
	}
	if !strings.Contains(manifestStr, "kind: Subscription") {
		t.Errorf("olm.yaml should contain Subscription")
	}
}

func TestWrite_DirectNonOLM(t *testing.T) {
	// Test Direct deployment without OLM (no CSV wait logic)
	outDir := t.TempDir()

	res, err := localformat.Write(context.Background(), localformat.Options{
		OutputDir: outDir,
		Components: []localformat.Component{{
			Name:       "gpu-operator",
			Namespace:  "nvidia-gpu-operator",
			SourceFile: "recipes/components/gpu-operator/direct/clusterpolicy.yaml",
			Olm:        false, // Not an OLM component
		}},
	})
	folders := res.Folders
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if len(folders) != 1 {
		t.Fatalf("want 1 folder, got %d", len(folders))
	}

	// Verify install.sh does NOT contain CSV wait logic (not OLM)
	installPath := filepath.Join(outDir, "001-gpu-operator", "install.sh")
	installContent, err := os.ReadFile(installPath)
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}

	// Should have kubectl apply
	if !strings.Contains(string(installContent), "kubectl apply") {
		t.Errorf("install.sh should contain kubectl apply")
	}
	if !strings.Contains(string(installContent), "clusterpolicy.yaml") {
		t.Errorf("install.sh should reference clusterpolicy.yaml")
	}

	// Should NOT have CSV wait logic (not OLM)
	if strings.Contains(string(installContent), "ClusterServiceVersion") {
		t.Errorf("non-OLM install.sh should not contain CSV wait logic")
	}
	if strings.Contains(string(installContent), "CSV reached Succeeded") {
		t.Errorf("non-OLM install.sh should not wait for CSV")
	}
}

func TestWrite_DirectMissingSourceFile(t *testing.T) {
	outDir := t.TempDir()

	_, err := localformat.Write(context.Background(), localformat.Options{
		OutputDir: outDir,
		Components: []localformat.Component{{
			Name:       "missing-file",
			Namespace:  "test-ns",
			SourceFile: "recipes/components/nonexistent/direct/manifest.yaml",
			Olm:        false,
		}},
	})

	if err == nil {
		t.Fatal("Write should fail when source file doesn't exist")
	}
	if !stderrors.Is(err, errors.New(errors.ErrCodeInternal, "")) {
		t.Errorf("expected ErrCodeInternal, got %v", err)
	}
	if !strings.Contains(err.Error(), "failed to read source file") {
		t.Errorf("error should mention failed to read source file, got: %v", err)
	}
}

func TestWrite_DirectOLMTimeoutConstants(t *testing.T) {
	// Verify that timeout constants from pkg/defaults are properly
	// rendered into the install.sh script with env var override capability
	outDir := t.TempDir()

	res, err := localformat.Write(context.Background(), localformat.Options{
		OutputDir: outDir,
		Components: []localformat.Component{{
			Name:       "gpu-operator-olm",
			Namespace:  "nvidia-gpu-operator",
			SourceFile: "recipes/components/gpu-operator-olm/direct/olm.yaml",
			Olm:        true,
		}},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(res.Folders) != 1 {
		t.Fatalf("want 1 folder, got %d", len(res.Folders))
	}

	// Read the generated install.sh
	installPath := filepath.Join(outDir, "001-gpu-operator-olm", "install.sh")
	installContent, err := os.ReadFile(installPath)
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}

	script := string(installContent)

	// Verify timeout uses env var with default from pkg/defaults (300 seconds)
	if !strings.Contains(script, "TIMEOUT=${AICR_OLM_CSV_TIMEOUT:-300}") {
		t.Error("install.sh should contain TIMEOUT=${AICR_OLM_CSV_TIMEOUT:-300}")
	}

	// Verify interval uses env var with default from pkg/defaults (5 seconds)
	if !strings.Contains(script, "INTERVAL=${AICR_OLM_CSV_INTERVAL:-5}") {
		t.Error("install.sh should contain INTERVAL=${AICR_OLM_CSV_INTERVAL:-5}")
	}

	// Verify the timeout logic still exists
	if !strings.Contains(script, "while [ $ELAPSED -lt $TIMEOUT ]") {
		t.Error("install.sh should contain timeout loop logic")
	}

	// Verify CSV wait logic
	if !strings.Contains(script, "reached Succeeded phase") {
		t.Error("install.sh should contain CSV success message")
	}

	// Verify we resolve CSV name from Subscription
	if !strings.Contains(script, "kubectl get subscription") {
		t.Error("install.sh should query Subscription to resolve CSV name")
	}

	if !strings.Contains(script, "installedCSV") {
		t.Error("install.sh should extract installedCSV from Subscription status")
	}
}

func TestWrite_Deterministic(t *testing.T) {
	kustomizePath, err := filepath.Abs(filepath.Join("testdata", "kustomize_input"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	opts := func(dir string) localformat.Options {
		return localformat.Options{
			OutputDir: dir,
			Components: []localformat.Component{
				{
					Name:       "a",
					Namespace:  "a",
					Repository: "https://a.example",
					ChartName:  "a",
					Version:    "v1",
					Values:     map[string]any{"image": map[string]any{"tag": "v1"}},
				},
				{
					Name:       "b",
					Namespace:  "b",
					Repository: "https://b.example",
					ChartName:  "b",
					Version:    "v1",
				},
				{
					// Kustomize component to lock determinism on the
					// kustomize build path (manifest.yaml ordering, etc.).
					Name:      "k",
					Namespace: "k",
					Path:      kustomizePath,
				},
			},
			// b is mixed — exercise the -post injection path in the determinism check
			ComponentManifests: map[string]map[string][]byte{
				"b": {
					// Two manifests with distinct basenames to exercise sorted iteration
					"b/manifests/m1.yaml": []byte("---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: m1\n"),
					"b/manifests/m2.yaml": []byte("---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: m2\n"),
				},
			},
		}
	}
	d1, d2 := t.TempDir(), t.TempDir()
	if _, err := localformat.Write(context.Background(), opts(d1)); err != nil {
		t.Fatalf("Write 1: %v", err)
	}
	if _, err := localformat.Write(context.Background(), opts(d2)); err != nil {
		t.Fatalf("Write 2: %v", err)
	}
	assertDirsEqual(t, d1, d2)
}

func TestWrite_KustomizeWithManifestsRejected(t *testing.T) {
	// Point Path at the existing kustomize fixture so Tag/Path are set
	// realistically, but attach raw manifests alongside — bundle must refuse.
	kustomizePath, err := filepath.Abs(filepath.Join("testdata", "kustomize_input"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}

	_, err = localformat.Write(context.Background(), localformat.Options{
		OutputDir: t.TempDir(),
		Components: []localformat.Component{{
			Name:      "busted-component",
			Namespace: "ns",
			Tag:       "v1.0.0",
			Path:      kustomizePath,
		}},
		ComponentManifests: map[string]map[string][]byte{
			"busted-component": {
				"extra/m.yaml": []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n"),
			},
		},
	})
	if err == nil {
		t.Fatalf("want error rejecting kustomize + raw manifests, got nil")
	}
	// Must be a structured error with ErrCodeInvalidRequest
	var structErr *errors.StructuredError
	if !stderrors.As(err, &structErr) {
		t.Fatalf("expected *errors.StructuredError, got %T: %v", err, err)
	}
	if structErr.Code != errors.ErrCodeInvalidRequest {
		t.Errorf("error code = %s, want %s (full error: %v)", structErr.Code, errors.ErrCodeInvalidRequest, err)
	}
	// Message should name the component and reference the conflict
	msg := err.Error()
	if !strings.Contains(msg, "busted-component") || !strings.Contains(msg, "kustomize") || !strings.Contains(msg, "manifests") {
		t.Errorf("error message should mention component name + conflict; got: %s", msg)
	}
}

func TestWrite_PathContainment(t *testing.T) {
	_, err := localformat.Write(context.Background(), localformat.Options{
		OutputDir: t.TempDir(),
		Components: []localformat.Component{{
			Name:       "../escape",
			Repository: "https://example.com",
		}},
	})
	if err == nil {
		t.Fatalf("want error rejecting unsafe component name, got nil")
	}
	var structErr *errors.StructuredError
	if !stderrors.As(err, &structErr) {
		t.Fatalf("expected *errors.StructuredError, got %T: %v", err, err)
	}
	if structErr.Code != errors.ErrCodeInvalidRequest {
		t.Errorf("code = %v, want ErrCodeInvalidRequest", structErr.Code)
	}
	if !strings.Contains(err.Error(), "../escape") {
		t.Errorf("error should name the offending component; got: %v", err)
	}
}

func TestWrite_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling Write

	_, err := localformat.Write(ctx, localformat.Options{
		OutputDir: t.TempDir(),
		Components: []localformat.Component{{
			Name:       "a",
			Repository: "https://a.example",
			ChartName:  "a",
			Version:    "v1",
		}},
	})
	if err == nil {
		t.Fatalf("want error on cancelled context, got nil")
	}
	var structErr *errors.StructuredError
	if !stderrors.As(err, &structErr) {
		t.Fatalf("expected *errors.StructuredError, got %T: %v", err, err)
	}
	if structErr.Code != errors.ErrCodeTimeout {
		t.Errorf("code = %v, want ErrCodeTimeout", structErr.Code)
	}
}

// assertDirsEqual walks d1 and compares each file to the corresponding file
// in d2 (same relative path). Fails on missing files, extra files, or content
// mismatch. Path-relative compare — absolute TempDir prefix is stripped.
func assertDirsEqual(t *testing.T, d1, d2 string) {
	t.Helper()
	files1 := listFiles(t, d1)
	files2 := listFiles(t, d2)
	if !reflect.DeepEqual(files1, files2) {
		t.Fatalf("file trees differ:\n  d1=%v\n  d2=%v", files1, files2)
	}
	for _, rel := range files1 {
		b1, err := os.ReadFile(filepath.Join(d1, rel))
		if err != nil {
			t.Fatalf("read %s from d1: %v", rel, err)
		}
		b2, err := os.ReadFile(filepath.Join(d2, rel))
		if err != nil {
			t.Fatalf("read %s from d2: %v", rel, err)
		}
		if string(b1) != string(b2) {
			t.Errorf("content differs at %s:\n--- d1 ---\n%s\n--- d2 ---\n%s", rel, b1, b2)
		}
	}
}

// listFiles returns sorted relative paths of all regular files under dir.
func listFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.Mode().IsRegular() {
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	sort.Strings(files)
	return files
}

// assertGolden reads outDir/relPath and diffs it against goldenDir/relPath.
// With -update, writes the actual content to the golden path.
func assertGolden(t *testing.T, outDir, goldenDir, relPath string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(outDir, relPath))
	if err != nil {
		t.Fatalf("read actual %s: %v", relPath, err)
	}
	goldenPath := filepath.Join(goldenDir, relPath)
	if *update {
		if err = os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("mkdir golden: %v", err)
		}
		if err = os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to regenerate)", goldenPath, err)
	}
	if string(got) != string(want) {
		t.Errorf("%s differs from golden:\n--- got ---\n%s\n--- want ---\n%s", relPath, got, want)
	}
}
