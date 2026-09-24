// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package filegen

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/pki"
)

// GenerateServerFiles generates files consumed by services local to nstance-server.
func (p *Generator) GenerateServerFiles(ctx context.Context, cfg *config.Config) (map[string][]byte, error) {
	if cfg == nil || len(cfg.Server.Files) == 0 {
		return nil, nil
	}
	images := map[string]string{}
	if p.imageGetter != nil {
		images = p.imageGetter.GetAll()
	}
	data := pki.CertificateTemplateData{
		Cluster: pki.ClusterData{ID: cfg.Cluster.ID, CACert: string(p.caCertPEM)},
		Server: pki.ServerData{
			Shard:            cfg.Shard.ID,
			RegistrationAddr: cfg.Shard.Advertise.RegistrationAddr,
			AgentAddr:        cfg.Shard.Advertise.AgentAddr,
			OperatorAddr:     cfg.Shard.Advertise.OperatorAddr,
		},
		Provider: pki.ProviderData{
			Kind:   cfg.Shard.Infra.Provider,
			Region: cfg.Shard.Infra.Region,
			Zone:   cfg.Shard.Infra.Zone,
		},
		Vars:  cfg.Defaults.Vars,
		Image: images,
	}

	names := make([]string, 0, len(cfg.Server.Files))
	for name := range cfg.Server.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	generated := make(map[string][]byte, len(names))
	for _, name := range names {
		file := cfg.Server.Files[name]
		switch file.Kind {
		case "secret":
			source, err := p.renderServerFileSource(file.Source, data)
			if err != nil {
				return nil, fmt.Errorf("render server file %s source: %w", name, err)
			}
			content, err := p.secretsStore.Get(ctx, source)
			if err != nil {
				return nil, fmt.Errorf("get server file %s secret: %w", name, err)
			}
			generated[name] = content
		case "storage":
			source, err := p.renderServerFileSource(file.Source, data)
			if err != nil {
				return nil, fmt.Errorf("render server file %s source: %w", name, err)
			}
			content, _, err := p.storageBackend.Get(ctx, source)
			if err != nil {
				return nil, fmt.Errorf("get server file %s storage object: %w", name, err)
			}
			generated[name] = content
		case "env", "json", "string":
			content, err := p.templateRenderer.Render(&file, data)
			if err != nil {
				return nil, fmt.Errorf("render server file %s: %w", name, err)
			}
			generated[name] = content
		default:
			return nil, fmt.Errorf("unsupported server file %s kind %s", name, file.Kind)
		}
	}
	return generated, nil
}

// renderServerFileSource expands a templated storage or secret source.
func (p *Generator) renderServerFileSource(source string, data pki.CertificateTemplateData) (string, error) {
	if !strings.Contains(source, "{{") {
		return source, nil
	}
	return p.templateRenderer.processTemplate(source, data)
}
