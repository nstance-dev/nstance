// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package proxmox

import (
	"context"
	"fmt"

	proxmox "github.com/luthermonson/go-proxmox"
)

// templateArgs holds template-related arguments extracted from the request Args
type templateArgs struct {
	TemplateVMID int
	TemplateName string
	StoragePool  string
}

// extractTemplateArgs extracts and validates template-related arguments from request Args
func extractTemplateArgs(args map[string]interface{}) (*templateArgs, error) {
	ta := &templateArgs{}

	if vmid, ok := args["TemplateVMID"].(float64); ok {
		ta.TemplateVMID = int(vmid)
	}
	if name, ok := args["TemplateName"].(string); ok {
		ta.TemplateName = name
	}
	if pool, ok := args["StoragePool"].(string); ok {
		ta.StoragePool = pool
	}

	if ta.StoragePool == "" {
		return nil, fmt.Errorf("StoragePool is required in args")
	}
	if ta.TemplateVMID != 0 && ta.TemplateName != "" {
		return nil, fmt.Errorf("TemplateVMID and TemplateName are mutually exclusive")
	}
	if ta.TemplateVMID == 0 && ta.TemplateName == "" {
		return nil, fmt.Errorf("either TemplateVMID or TemplateName is required in args")
	}

	return ta, nil
}

// resolveTemplateVMID returns the template VMID for the given node.
// If TemplateVMID is specified in args, it returns that directly.
// If TemplateName is specified, it looks up the template by name on the node,
// caching the result for subsequent calls.
func (p *Provider) resolveTemplateVMID(ctx context.Context, node *proxmox.Node, ta *templateArgs) (int, error) {
	if ta.TemplateVMID != 0 {
		return ta.TemplateVMID, nil
	}

	cacheKey := node.Name + ":" + ta.TemplateName
	p.templateCache.mu.RLock()
	if vmid, ok := p.templateCache.byNode[cacheKey]; ok {
		p.templateCache.mu.RUnlock()
		return vmid, nil
	}
	p.templateCache.mu.RUnlock()

	vms, err := node.VirtualMachines(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to list VMs on node %s: %w", node.Name, err)
	}

	var matches []int
	for _, vm := range vms {
		if bool(vm.Template) && vm.Name == ta.TemplateName {
			matches = append(matches, int(vm.VMID))
		}
	}

	if len(matches) == 0 {
		return 0, fmt.Errorf("no template named %q found on node %q", ta.TemplateName, node.Name)
	}
	if len(matches) > 1 {
		return 0, fmt.Errorf("multiple templates named %q found on node %q (found %d)", ta.TemplateName, node.Name, len(matches))
	}

	vmid := matches[0]

	p.templateCache.mu.Lock()
	p.templateCache.byNode[cacheKey] = vmid
	p.templateCache.mu.Unlock()

	p.logger.Info("resolved template by name",
		"name", ta.TemplateName,
		"node", node.Name,
		"vmid", vmid,
	)

	return vmid, nil
}
