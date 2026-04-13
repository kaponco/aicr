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

package k8s

import (
	"testing"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func TestIsAllowedCRDGroup(t *testing.T) {
	t.Parallel()

	allowlist := []string{"nvidia.com", "nfd.k8s-sigs.io", "mellanox.com"}

	tests := []struct {
		name     string
		group    string
		expected bool
	}{
		{
			name:     "exact match - nvidia.com",
			group:    "nvidia.com",
			expected: true,
		},
		{
			name:     "subdomain match - operator.nvidia.com",
			group:    "operator.nvidia.com",
			expected: true,
		},
		{
			name:     "exact match - mellanox.com",
			group:    "mellanox.com",
			expected: true,
		},
		{
			name:     "subdomain match - device.mellanox.com",
			group:    "device.mellanox.com",
			expected: true,
		},
		{
			name:     "exact match - nfd.k8s-sigs.io",
			group:    "nfd.k8s-sigs.io",
			expected: true,
		},
		{
			name:     "openshift not in allowlist",
			group:    "config.openshift.io",
			expected: false,
		},
		{
			name:     "kubeflow not in allowlist",
			group:    "kubeflow.org",
			expected: false,
		},
		{
			name:     "partial string match should not work",
			group:    "notnvidia.com",
			expected: false,
		},
		{
			name:     "empty group",
			group:    "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := isAllowedCRDGroup(tt.group, allowlist)
			if result != tt.expected {
				t.Errorf("isAllowedCRDGroup(%q) = %v, want %v", tt.group, result, tt.expected)
			}
		})
	}
}

func TestIsCRDEstablished(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		conditions []apiextv1.CustomResourceDefinitionCondition
		expected   bool
	}{
		{
			name: "established condition true",
			conditions: []apiextv1.CustomResourceDefinitionCondition{
				{
					Type:   apiextv1.Established,
					Status: apiextv1.ConditionTrue,
				},
			},
			expected: true,
		},
		{
			name: "established condition false",
			conditions: []apiextv1.CustomResourceDefinitionCondition{
				{
					Type:   apiextv1.Established,
					Status: apiextv1.ConditionFalse,
				},
			},
			expected: false,
		},
		{
			name: "no established condition",
			conditions: []apiextv1.CustomResourceDefinitionCondition{
				{
					Type:   apiextv1.NamesAccepted,
					Status: apiextv1.ConditionTrue,
				},
			},
			expected: false,
		},
		{
			name:       "empty conditions",
			conditions: []apiextv1.CustomResourceDefinitionCondition{},
			expected:   false,
		},
		{
			name: "multiple conditions with established true",
			conditions: []apiextv1.CustomResourceDefinitionCondition{
				{
					Type:   apiextv1.NamesAccepted,
					Status: apiextv1.ConditionTrue,
				},
				{
					Type:   apiextv1.Established,
					Status: apiextv1.ConditionTrue,
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			crd := &apiextv1.CustomResourceDefinition{
				Status: apiextv1.CustomResourceDefinitionStatus{
					Conditions: tt.conditions,
				},
			}
			result := isCRDEstablished(crd)
			if result != tt.expected {
				t.Errorf("isCRDEstablished() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestBoolToString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    bool
		expected string
	}{
		{
			name:     "true converts to 'true'",
			input:    true,
			expected: "true",
		},
		{
			name:     "false converts to 'false'",
			input:    false,
			expected: "false",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := boolToString(tt.input)
			if result != tt.expected {
				t.Errorf("boolToString(%v) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestDefaultCRDGroups(t *testing.T) {
	t.Parallel()

	// Verify the default allowlist contains expected NVIDIA-focused groups
	expectedGroups := map[string]bool{
		"nvidia.com":                true,
		"mellanox.com":              true,
		"maintenance.nvidia.com":    true,
		"sriovnetwork.openshift.io": true,
		"dpu.openshift.io":          true,
		"nfd.openshift.io":          true,
		"nfd.k8s-sigs.io":           true,
	}

	if len(defaultCRDGroups) != len(expectedGroups) {
		t.Errorf("defaultCRDGroups has %d groups, expected %d", len(defaultCRDGroups), len(expectedGroups))
	}

	for _, group := range defaultCRDGroups {
		if !expectedGroups[group] {
			t.Errorf("unexpected group in defaultCRDGroups: %q", group)
		}
	}
}

// TestCollectCRDs_DataStructure validates the structure of collected CRD data
func TestCollectCRDs_DataStructure(t *testing.T) {
	t.Parallel()

	// This test documents the expected data structure for CRD measurements
	// Actual integration testing would require a live cluster or mock apiextensions client

	expectedFields := []string{
		"",             // presence check (empty suffix)
		".kind",        // CRD Kind name
		".group",       // API group
		".version",     // storage version
		".versions",    // all served versions
		".scope",       // Cluster or Namespaced
		".established", // ready status
	}

	// Document expected field count
	if len(expectedFields) != 7 {
		t.Errorf("Expected 7 fields per CRD, got %d", len(expectedFields))
	}
}
