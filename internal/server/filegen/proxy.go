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

// GenerateProxyFiles generates files consumed by services local to nstance-server.
func (p *Generator) GenerateProxyFiles(ctx context.Context, cfg *config.Config) (map[string][]byte, error) {
	if cfg == nil || len(cfg.Proxy.Files) == 0 {
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

	names := make([]string, 0, len(cfg.Proxy.Files))
	for name := range cfg.Proxy.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	generated := make(map[string][]byte, len(names))
	for _, name := range names {
		file := cfg.Proxy.Files[name]
		switch file.Kind {
		case "secret":
			source, err := p.renderProxySource(file.Source, data)
			if err != nil {
				return nil, fmt.Errorf("render proxy file %s source: %w", name, err)
			}
			content, err := p.secretsStore.Get(ctx, source)
			if err != nil {
				return nil, fmt.Errorf("get proxy file %s secret: %w", name, err)
			}
			generated[name] = content
		case "storage":
			source, err := p.renderProxySource(file.Source, data)
			if err != nil {
				return nil, fmt.Errorf("render proxy file %s source: %w", name, err)
			}
			content, _, err := p.storageBackend.Get(ctx, source)
			if err != nil {
				return nil, fmt.Errorf("get proxy file %s storage object: %w", name, err)
			}
			generated[name] = content
		case "env", "json", "string":
			content, err := p.templateRenderer.Render(&file, data)
			if err != nil {
				return nil, fmt.Errorf("render proxy file %s: %w", name, err)
			}
			generated[name] = content
		default:
			return nil, fmt.Errorf("unsupported proxy file %s kind %s", name, file.Kind)
		}
	}
	return generated, nil
}

// renderProxySource expands a templated storage or secret source.
func (p *Generator) renderProxySource(source string, data pki.CertificateTemplateData) (string, error) {
	if !strings.Contains(source, "{{") {
		return source, nil
	}
	return p.templateRenderer.processTemplate(source, data)
}
