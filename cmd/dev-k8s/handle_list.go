// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Server) handleList(w http.ResponseWriter, r *http.Request, resource, namespace string) {
	var items []json.RawMessage

	if namespace == "" {
		// Could be listing all namespaces for a namespaced resource,
		// or listing a cluster-scoped resource (like nodes)
		resourceDir := filepath.Join(s.dir, resource)
		entries, err := os.ReadDir(resourceDir)
		if err != nil {
			if os.IsNotExist(err) {
				s.writeList(w, resource, nil)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, entry := range entries {
			if entry.IsDir() {
				// This is a namespace subdirectory - list its contents
				nsPath := filepath.Join(resourceDir, entry.Name())
				nsEntries, err := os.ReadDir(nsPath)
				if err != nil {
					continue
				}
				for _, nsEntry := range nsEntries {
					if !nsEntry.IsDir() && strings.HasSuffix(nsEntry.Name(), ".json") {
						data, err := os.ReadFile(filepath.Join(nsPath, nsEntry.Name()))
						if err != nil {
							continue
						}
						items = append(items, data)
					}
				}
			} else if strings.HasSuffix(entry.Name(), ".json") {
				// This is a cluster-scoped resource file directly in the resource dir
				data, err := os.ReadFile(filepath.Join(resourceDir, entry.Name()))
				if err != nil {
					continue
				}
				items = append(items, data)
			}
		}
	} else {
		dir := filepath.Join(s.dir, resource, namespace)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				s.writeList(w, resource, nil)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
				data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
				if err != nil {
					continue
				}
				items = append(items, data)
			}
		}
	}

	s.writeList(w, resource, items)
}

func (s *Server) writeList(w http.ResponseWriter, resource string, items []json.RawMessage) {
	if items == nil {
		items = []json.RawMessage{}
	}

	response := map[string]interface{}{
		"apiVersion": resourceToAPIVersion(resource),
		"kind":       singularToKind(resource) + "List",
		"metadata": map[string]interface{}{
			"resourceVersion": fmt.Sprintf("%d", time.Now().UnixNano()),
		},
		"items": items,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func singularToKind(resource string) string {
	switch resource {
	case "secrets":
		return "Secret"
	case "configmaps":
		return "ConfigMap"
	case "nstancemachines":
		return "NstanceMachine"
	case "nstancemachinepools":
		return "NstanceMachinePool"
	case "nstanceclusters":
		return "NstanceCluster"
	case "nstancemachinetemplates":
		return "NstanceMachineTemplate"
	case "nstanceshardgroups":
		return "NstanceShardGroup"
	case "clusters":
		return "Cluster"
	case "machines":
		return "Machine"
	case "machinepools":
		return "MachinePool"
	case "nodes":
		return "Node"
	case "pods":
		return "Pod"
	case "namespaces":
		return "Namespace"
	case "leases":
		return "Lease"
	default:
		s := strings.TrimSuffix(resource, "s")
		if len(s) > 0 {
			return strings.ToUpper(s[:1]) + s[1:]
		}
		return s
	}
}

func resourceToAPIVersion(resource string) string {
	switch resource {
	case "nstanceclusters", "nstancemachines", "nstancemachinepools", "nstancemachinetemplates", "nstanceshardgroups":
		return "infrastructure.cluster.x-k8s.io/v1beta1"
	case "clusters", "machines", "machinepools":
		return "cluster.x-k8s.io/v1beta2"
	default:
		return "v1"
	}
}
