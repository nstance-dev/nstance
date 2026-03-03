// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
)

func (s *Server) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path

	switch path {
	case "/api":
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"kind":     "APIVersions",
			"versions": []string{"v1"},
		})

	case "/api/v1":
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"kind":         "APIResourceList",
			"apiVersion":   "v1",
			"groupVersion": "v1",
			"resources": []map[string]interface{}{
				{"name": "secrets", "singularName": "secret", "namespaced": true, "kind": "Secret", "verbs": []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
				{"name": "configmaps", "singularName": "configmap", "namespaced": true, "kind": "ConfigMap", "verbs": []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
				{"name": "namespaces", "singularName": "namespace", "namespaced": false, "kind": "Namespace", "verbs": []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
				{"name": "nodes", "singularName": "node", "namespaced": false, "kind": "Node", "verbs": []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
				{"name": "pods", "singularName": "pod", "namespaced": true, "kind": "Pod", "verbs": []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
				{"name": "serviceaccounts", "singularName": "serviceaccount", "namespaced": true, "kind": "ServiceAccount", "verbs": []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
				{"name": "serviceaccounts/token", "singularName": "", "namespaced": true, "kind": "TokenRequest", "verbs": []string{"create"}},
			},
		})

	case "/apis":
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"kind":       "APIGroupList",
			"apiVersion": "v1",
			"groups": []map[string]interface{}{
				{
					"name": "infrastructure.cluster.x-k8s.io",
					"versions": []map[string]string{
						{"groupVersion": "infrastructure.cluster.x-k8s.io/v1beta1", "version": "v1beta1"},
					},
					"preferredVersion": map[string]string{
						"groupVersion": "infrastructure.cluster.x-k8s.io/v1beta1",
						"version":      "v1beta1",
					},
				},
				{
					"name": "cluster.x-k8s.io",
					"versions": []map[string]string{
						{"groupVersion": "cluster.x-k8s.io/v1beta2", "version": "v1beta2"},
					},
					"preferredVersion": map[string]string{
						"groupVersion": "cluster.x-k8s.io/v1beta2",
						"version":      "v1beta2",
					},
				},
				{
					"name": "coordination.k8s.io",
					"versions": []map[string]string{
						{"groupVersion": "coordination.k8s.io/v1", "version": "v1"},
					},
					"preferredVersion": map[string]string{
						"groupVersion": "coordination.k8s.io/v1",
						"version":      "v1",
					},
				},
			},
		})

	case "/apis/infrastructure.cluster.x-k8s.io/v1beta1":
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"kind":         "APIResourceList",
			"apiVersion":   "v1",
			"groupVersion": "infrastructure.cluster.x-k8s.io/v1beta1",
			"resources": []map[string]interface{}{
				{"name": "nstancemachines", "singularName": "nstancemachine", "namespaced": true, "kind": "NstanceMachine", "verbs": []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
				{"name": "nstancemachines/status", "singularName": "", "namespaced": true, "kind": "NstanceMachine", "verbs": []string{"get", "update", "patch"}},
				{"name": "nstancemachinepools", "singularName": "nstancemachinepool", "namespaced": true, "kind": "NstanceMachinePool", "verbs": []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
				{"name": "nstancemachinepools/status", "singularName": "", "namespaced": true, "kind": "NstanceMachinePool", "verbs": []string{"get", "update", "patch"}},
				{"name": "nstanceclusters", "singularName": "nstancecluster", "namespaced": true, "kind": "NstanceCluster", "verbs": []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
				{"name": "nstanceclusters/status", "singularName": "", "namespaced": true, "kind": "NstanceCluster", "verbs": []string{"get", "update", "patch"}},
				{"name": "nstancemachinetemplates", "singularName": "nstancemachinetemplate", "namespaced": true, "kind": "NstanceMachineTemplate", "verbs": []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
				{"name": "nstanceshardgroups", "singularName": "nstanceshardgroup", "namespaced": true, "kind": "NstanceShardGroup", "verbs": []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
				{"name": "nstanceshardgroups/status", "singularName": "", "namespaced": true, "kind": "NstanceShardGroup", "verbs": []string{"get", "update", "patch"}},
			},
		})

	case "/apis/cluster.x-k8s.io/v1beta2":
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"kind":         "APIResourceList",
			"apiVersion":   "v1",
			"groupVersion": "cluster.x-k8s.io/v1beta2",
			"resources": []map[string]interface{}{
				{"name": "clusters", "singularName": "cluster", "namespaced": true, "kind": "Cluster", "verbs": []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
				{"name": "clusters/status", "singularName": "", "namespaced": true, "kind": "Cluster", "verbs": []string{"get", "update", "patch"}},
				{"name": "machines", "singularName": "machine", "namespaced": true, "kind": "Machine", "verbs": []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
				{"name": "machines/status", "singularName": "", "namespaced": true, "kind": "Machine", "verbs": []string{"get", "update", "patch"}},
				{"name": "machinepools", "singularName": "machinepool", "namespaced": true, "kind": "MachinePool", "verbs": []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
				{"name": "machinepools/status", "singularName": "", "namespaced": true, "kind": "MachinePool", "verbs": []string{"get", "update", "patch"}},
			},
		})

	case "/apis/coordination.k8s.io/v1":
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"kind":         "APIResourceList",
			"apiVersion":   "v1",
			"groupVersion": "coordination.k8s.io/v1",
			"resources": []map[string]interface{}{
				{"name": "leases", "singularName": "lease", "namespaced": true, "kind": "Lease", "verbs": []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
			},
		})

	default:
		http.Error(w, "Not found", http.StatusNotFound)
	}
}
