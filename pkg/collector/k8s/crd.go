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
	"context"
	"log/slog"
	"strings"

	"github.com/NVIDIA/aicr/pkg/errors"
	"github.com/NVIDIA/aicr/pkg/measurement"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Default allowlist of CRD groups for NVIDIA AI infrastructure.
// Only CRDs matching these groups are collected to reduce snapshot size.
var defaultCRDGroups = []string{
	// NVIDIA GPU Infrastructure
	"nvidia.com",             // NVIDIA GPU Operator
	"mellanox.com",           // NVIDIA Network Operator
	"maintenance.nvidia.com", // NVIDIA Maintenance Operator

	// High-performance Networking (multi-vendor, but GPU-cluster relevant)
	"sriovnetwork.openshift.io", // SR-IOV Network Operator
	"dpu.openshift.io",          // DPU Network Operator

	// Node Feature Discovery (generic hardware detection)
	"nfd.openshift.io", // NFD (OpenShift)
	"nfd.k8s-sigs.io",  // NFD (upstream)
}

// collectCRDs retrieves CustomResourceDefinitions matching the allowlist.
// Returns CRD availability, kind, version, and established status.
// Only CRDs whose group matches the allowlist are collected to reduce noise.
//
// For each CRD, the following data is stored:
//   - {crd-name}:            "true" (existence check)
//   - {crd-name}.kind:       The Kind name (e.g., "ClusterPolicy")
//   - {crd-name}.group:      The API group (e.g., "nvidia.com")
//   - {crd-name}.version:    The storage version (e.g., "v1")
//   - {crd-name}.versions:   Comma-separated list of all served versions
//   - {crd-name}.scope:      "Cluster" or "Namespaced"
//   - {crd-name}.established: "true" or "false" (CRD is ready for use)
func (k *Collector) collectCRDs(ctx context.Context) (map[string]measurement.Reading, error) {
	// Create apiextensions client
	apiextClient, err := apiextclient.NewForConfig(k.RestConfig)
	if err != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal, "failed to create apiextensions client", err)
	}

	// List all CRDs
	crdList, err := apiextClient.ApiextensionsV1().CustomResourceDefinitions().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, errors.Wrap(errors.ErrCodeInternal, "failed to list CRDs", err)
	}

	crdData := make(map[string]measurement.Reading)
	collectedCount := 0

	for _, crd := range crdList.Items {
		// Check for context cancellation
		if err := ctx.Err(); err != nil {
			return nil, errors.Wrap(errors.ErrCodeTimeout, "CRD collection cancelled", err)
		}

		// Filter by allowlist
		if !isAllowedCRDGroup(crd.Spec.Group, defaultCRDGroups) {
			continue
		}

		name := crd.Name // e.g., "clusterpolicies.nvidia.com"

		// Mark CRD as present
		crdData[name] = measurement.Str("true")

		// Store kind (e.g., "ClusterPolicy")
		crdData[name+".kind"] = measurement.Str(crd.Spec.Names.Kind)

		// Store group
		crdData[name+".group"] = measurement.Str(crd.Spec.Group)

		// Store scope (Cluster or Namespaced)
		crdData[name+".scope"] = measurement.Str(string(crd.Spec.Scope))

		// Store the storage version (the canonical version)
		for _, version := range crd.Spec.Versions {
			if version.Storage {
				crdData[name+".version"] = measurement.Str(version.Name)
				break
			}
		}

		// Store all served versions (comma-separated)
		var versions []string
		for _, version := range crd.Spec.Versions {
			if version.Served {
				versions = append(versions, version.Name)
			}
		}
		if len(versions) > 0 {
			crdData[name+".versions"] = measurement.Str(strings.Join(versions, ","))
		}

		// Store established status (indicates if CRD is ready for use)
		established := isCRDEstablished(&crd)
		crdData[name+".established"] = measurement.Str(boolToString(established))

		collectedCount++
	}

	slog.Debug("collected CRDs",
		slog.Int("total", len(crdList.Items)),
		slog.Int("collected", collectedCount),
		slog.Int("filtered", len(crdList.Items)-collectedCount))

	return crdData, nil
}

// isAllowedCRDGroup checks if a CRD group matches the allowlist.
// Matches if the group equals or ends with an allowlist entry.
// This allows matching both exact groups (e.g., "nvidia.com") and
// subdomains (e.g., "operator.nvidia.com").
func isAllowedCRDGroup(group string, allowlist []string) bool {
	for _, allowed := range allowlist {
		if group == allowed || strings.HasSuffix(group, "."+allowed) {
			return true
		}
	}
	return false
}

// isCRDEstablished checks if the CRD has the "Established" condition set to True.
// This indicates the CRD is ready to accept custom resources.
func isCRDEstablished(crd *apiextv1.CustomResourceDefinition) bool {
	for _, condition := range crd.Status.Conditions {
		if condition.Type == apiextv1.Established {
			return condition.Status == apiextv1.ConditionTrue
		}
	}
	return false
}

// boolToString converts a boolean to "true" or "false" string.
func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
