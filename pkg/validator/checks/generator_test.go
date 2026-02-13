// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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

package checks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConstraintToFuncName(t *testing.T) {
	tests := []struct {
		name       string
		constraint string
		want       string
	}{
		{
			name:       "deployment gpu operator version",
			constraint: "Deployment.gpu-operator.version",
			want:       "DeploymentGpuOperatorVersion",
		},
		{
			name:       "simple constraint",
			constraint: "K8s.server.version",
			want:       "K8sServerVersion",
		},
		{
			name:       "single part",
			constraint: "version",
			want:       "Version",
		},
		{
			name:       "with dashes",
			constraint: "my-app.some-value",
			want:       "MyAppSomeValue",
		},
		{
			name:       "empty string",
			constraint: "",
			want:       "",
		},
		{
			name:       "uppercase parts",
			constraint: "OS.release.ID",
			want:       "OSReleaseID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := constraintToFuncName(tt.constraint)
			if got != tt.want {
				t.Errorf("constraintToFuncName(%q) = %q, want %q", tt.constraint, got, tt.want)
			}
		})
	}
}

func TestToSnakeCase(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "camel case",
			input: "GPUOperatorVersion",
			want:  "g_p_u_operator_version",
		},
		{
			name:  "simple camel",
			input: "FooBar",
			want:  "foo_bar",
		},
		{
			name:  "already lowercase",
			input: "foobar",
			want:  "foobar",
		},
		{
			name:  "single uppercase",
			input: "A",
			want:  "a",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "mixed case",
			input: "DeploymentGpuOperatorVersion",
			want:  "deployment_gpu_operator_version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toSnakeCase(tt.input)
			if got != tt.want {
				t.Errorf("toSnakeCase(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGenerateConstraintValidator(t *testing.T) {
	// Create a temporary directory for test output
	tmpDir, err := os.MkdirTemp("", "generator-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create subdirectory named "deployment" so package name is correct
	outputDir := filepath.Join(tmpDir, "deployment")
	if mkdirErr := os.MkdirAll(outputDir, 0755); mkdirErr != nil {
		t.Fatalf("failed to create output dir: %v", mkdirErr)
	}

	cfg := GeneratorConfig{
		ConstraintName: "Deployment.test-app.version",
		Phase:          "deployment",
		Description:    "Test validator for test-app version",
		OutputDir:      outputDir,
	}

	err = GenerateConstraintValidator(cfg)
	if err != nil {
		t.Fatalf("GenerateConstraintValidator() error = %v", err)
	}

	// Verify files were created
	// "Deployment.test-app.version" -> "DeploymentTestAppVersion" -> "deployment_test_app_version"
	expectedFiles := []string{
		"deployment_test_app_version.go",
		"deployment_test_app_version_test.go",
		"deployment_test_app_version_integration_test.go",
	}

	for _, filename := range expectedFiles {
		path := filepath.Join(outputDir, filename)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file %s was not created", filename)
			continue
		}

		// Read and verify content has expected markers
		content, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("failed to read %s: %v", filename, err)
			continue
		}

		contentStr := string(content)

		// Verify common elements
		if !strings.Contains(contentStr, "package deployment") {
			t.Errorf("%s missing package declaration", filename)
		}

		if !strings.Contains(contentStr, "DeploymentTestAppVersion") {
			t.Errorf("%s missing function name DeploymentTestAppVersion", filename)
		}
	}

	// Verify helper file has TODO comments
	helperContent, _ := os.ReadFile(filepath.Join(outputDir, "deployment_test_app_version.go"))
	if !strings.Contains(string(helperContent), "TODO") {
		t.Error("helper file missing TODO comments")
	}

	// Verify integration test has registration
	integrationContent, _ := os.ReadFile(filepath.Join(outputDir, "deployment_test_app_version_integration_test.go"))
	if !strings.Contains(string(integrationContent), "RegisterConstraintTest") {
		t.Error("integration test missing RegisterConstraintTest")
	}
	if !strings.Contains(string(integrationContent), "Deployment.test-app.version") {
		t.Error("integration test missing constraint pattern")
	}
}

func TestGenerateConstraintValidator_DifferentPhases(t *testing.T) {
	// Test that the generator works with different valid phases
	phases := []string{"deployment", "performance", "conformance", "readiness"}

	for _, phase := range phases {
		t.Run(phase, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "generator-test-*")
			if err != nil {
				t.Fatalf("failed to create temp dir: %v", err)
			}
			defer os.RemoveAll(tmpDir)

			cfg := GeneratorConfig{
				ConstraintName: "Test.constraint",
				Phase:          phase,
				Description:    "Test",
				OutputDir:      tmpDir,
			}

			err = GenerateConstraintValidator(cfg)
			if err != nil {
				t.Errorf("GenerateConstraintValidator() error = %v for phase %s", err, phase)
			}
		})
	}
}

func TestGenerateConstraintValidator_EmptyConstraint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "generator-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := GeneratorConfig{
		ConstraintName: "",
		Phase:          "deployment",
		Description:    "Test",
		OutputDir:      tmpDir,
	}

	err = GenerateConstraintValidator(cfg)
	if err == nil {
		t.Error("expected error for empty constraint, got nil")
	}
}

func TestGenerateConstraintValidator_EmptyPhase(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "generator-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := GeneratorConfig{
		ConstraintName: "Deployment.test.version",
		Phase:          "",
		Description:    "Test",
		OutputDir:      tmpDir,
	}

	err = GenerateConstraintValidator(cfg)
	if err == nil {
		t.Error("expected error for empty phase, got nil")
	}
}

func TestGenerateConstraintValidator_EmptyOutputDir(t *testing.T) {
	cfg := GeneratorConfig{
		ConstraintName: "Deployment.test.version",
		Phase:          "deployment",
		Description:    "Test",
		OutputDir:      "",
	}

	err := GenerateConstraintValidator(cfg)
	if err == nil {
		t.Error("expected error for empty output dir, got nil")
	}
}

func TestGenerateConstraintValidator_DefaultDescription(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "generator-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	outputDir := filepath.Join(tmpDir, "deployment")
	if mkdirErr := os.MkdirAll(outputDir, 0755); mkdirErr != nil {
		t.Fatalf("failed to create output dir: %v", mkdirErr)
	}

	cfg := GeneratorConfig{
		ConstraintName: "Deployment.test-app.version",
		Phase:          "deployment",
		Description:    "", // empty description triggers default
		OutputDir:      outputDir,
	}

	err = GenerateConstraintValidator(cfg)
	if err != nil {
		t.Fatalf("GenerateConstraintValidator() error = %v", err)
	}

	// Verify integration test file contains default description
	integrationContent, readErr := os.ReadFile(filepath.Join(outputDir, "deployment_test_app_version_integration_test.go"))
	if readErr != nil {
		t.Fatalf("failed to read integration test file: %v", readErr)
	}

	if !strings.Contains(string(integrationContent), "Validates Deployment.test-app.version constraint") {
		t.Error("integration test missing default description")
	}
}

func TestGenerateConstraintValidator_InvalidOutputDir(t *testing.T) {
	cfg := GeneratorConfig{
		ConstraintName: "Deployment.test.version",
		Phase:          "deployment",
		Description:    "Test",
		OutputDir:      "/nonexistent/path/that/does/not/exist",
	}

	err := GenerateConstraintValidator(cfg)
	if err == nil {
		t.Error("expected error for invalid output directory, got nil")
	}
}

func TestGenerateHelperFile_CreateError(t *testing.T) {
	// Use a path that cannot be created (directory does not exist)
	err := generateHelperFile("/nonexistent/dir/file.go", "TestFunc", GeneratorConfig{
		OutputDir:      "/nonexistent/dir",
		ConstraintName: "Test.constraint",
		Description:    "Test",
	})
	if err == nil {
		t.Error("expected error when output path does not exist, got nil")
	}
}

func TestGenerateUnitTestFile_CreateError(t *testing.T) {
	err := generateUnitTestFile("/nonexistent/dir/file_test.go", "TestFunc", GeneratorConfig{
		OutputDir:      "/nonexistent/dir",
		ConstraintName: "Test.constraint",
	})
	if err == nil {
		t.Error("expected error when output path does not exist, got nil")
	}
}

func TestGenerateIntegrationTestFile_CreateError(t *testing.T) {
	err := generateIntegrationTestFile("/nonexistent/dir/file_integration_test.go", "TestFunc", "TestTestFunc", GeneratorConfig{
		OutputDir:      "/nonexistent/dir",
		ConstraintName: "Test.constraint",
		Phase:          "deployment",
		Description:    "Test",
	})
	if err == nil {
		t.Error("expected error when output path does not exist, got nil")
	}
}
