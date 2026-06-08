// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package fakeserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/pki"
)

// pendingFile is a generated file staged for delivery to an agent.
type pendingFile struct {
	Filename     string    `json:"filename"`
	Content      []byte    `json:"content"`
	LastModified time.Time `json:"last_modified"`
}

// agentService implements the fake agent gRPC API backed by Server state.
type agentService struct {
	proto.UnimplementedAgentServiceServer
	s *Server
}

// ReceiveKeyRequests streams key names the authenticated agent should generate.
func (a *agentService) ReceiveKeyRequests(_ *emptypb.Empty, stream proto.AgentService_ReceiveKeyRequestsServer) error {
	info, err := api.GetClientInfo(stream.Context())
	if err != nil {
		return err
	}
	inst, err := a.s.getInstance(stream.Context(), info.ClientID)
	if err != nil {
		return status.Errorf(codes.NotFound, "instance not found")
	}
	tenant, err := a.s.tenant(stream.Context(), inst.TenantID)
	if err != nil {
		return status.Errorf(codes.NotFound, "tenant not found")
	}
	var names []string
	seen := map[string]bool{}
	for fn, fc := range tenant.Files {
		if fc.Kind == "certificate" && fc.Key != nil && fc.Key.Source == "agent" {
			n := baseKeyName(fc, fn)
			if !seen[n] {
				names = append(names, n)
				seen[n] = true
			}
		}
	}
	sort.Strings(names)
	if len(names) > 0 {
		if err := stream.Send(&proto.KeyGenerationRequest{KeyNames: names}); err != nil {
			return err
		}
	}
	return nil
}

// SubmitPublicKeys stores agent-generated public keys and stages any files they unlock.
func (a *agentService) SubmitPublicKeys(ctx context.Context, req *proto.PublicKeysRequest) (*emptypb.Empty, error) {
	info, err := api.GetClientInfo(ctx)
	if err != nil {
		return nil, err
	}
	if info.ClientID != req.InstanceId {
		return nil, status.Error(codes.PermissionDenied, "instance ID mismatch")
	}
	inst, err := a.s.getInstance(ctx, req.InstanceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, "instance not found")
	}
	tenant, err := a.s.tenant(ctx, inst.TenantID)
	if err != nil {
		return nil, status.Error(codes.NotFound, "tenant not found")
	}
	for _, k := range req.Keys {
		publicKeyPEM := k.PublicKeyPem
		if block, _ := pem.Decode(publicKeyPEM); block == nil {
			if der, err := base64.StdEncoding.DecodeString(string(publicKeyPEM)); err == nil {
				publicKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
			}
		}
		if err := a.s.cfg.Store.Put(ctx, a.s.publicKey(req.InstanceId, strings.TrimSuffix(k.Filename, ".pub")), publicKeyPEM); err != nil {
			return nil, status.Errorf(codes.Internal, "store public key: %v", err)
		}
	}
	files, err := a.generateFiles(ctx, inst, tenant)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "generate files: %v", err)
	}
	b, err := json.Marshal(files)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal pending files: %v", err)
	}
	if err := a.s.cfg.Store.Put(ctx, a.s.pendingKey(req.InstanceId), b); err != nil {
		return nil, status.Errorf(codes.Internal, "store pending files: %v", err)
	}
	return &emptypb.Empty{}, nil
}

// ReceiveFiles streams staged files to the authenticated agent and clears them after delivery.
func (a *agentService) ReceiveFiles(_ *emptypb.Empty, stream proto.AgentService_ReceiveFilesServer) error {
	info, err := api.GetClientInfo(stream.Context())
	if err != nil {
		return err
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		b, err := a.s.cfg.Store.Get(stream.Context(), a.s.pendingKey(info.ClientID))
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		var files []pendingFile
		if len(b) > 0 {
			if err := json.Unmarshal(b, &files); err != nil {
				return status.Errorf(codes.Internal, "decode pending files: %v", err)
			}
		}
		// Look up the tenant once per batch so each FileTransfer can carry the
		// current runtime config hash, and return an empty hash when the tenant
		// cannot be loaded, both matching real nstance server behavior
		configHash := ""
		if len(files) > 0 {
			if inst, err := a.s.getInstance(stream.Context(), info.ClientID); err == nil {
				if tenant, err := a.s.tenant(stream.Context(), inst.TenantID); err == nil {
					configHash = tenantRuntimeConfigHash(tenant)
				}
			}
		}
		for _, f := range files {
			if err := stream.Send(&proto.FileTransfer{Filename: f.Filename, Content: f.Content, LastModified: timestamppb.New(f.LastModified), ConfigHash: configHash}); err != nil {
				return err
			}
		}
		if len(files) > 0 {
			if err := a.s.cfg.Store.Delete(context.WithoutCancel(stream.Context()), a.s.pendingKey(info.ClientID)); err != nil && !errors.Is(err, ErrNotFound) {
				return status.Errorf(codes.Internal, "delete pending files: %v", err)
			}
		}
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
		}
	}
}

// SubmitHealthReport drains agent health reports until the stream closes.
func (a *agentService) SubmitHealthReport(stream proto.AgentService_SubmitHealthReportServer) error {
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			return stream.SendAndClose(&emptypb.Empty{})
		}
		if err != nil {
			return err
		}
	}
}

// generateFiles renders static files and certificates for an instance tenant configuration.
func (a *agentService) generateFiles(ctx context.Context, inst *persistedInstance, tenant *TenantConfig) ([]pendingFile, error) {
	now := time.Now()
	var out []pendingFile
	registrationAddr, agentAddr := a.s.Addr()
	data := pki.CreateCertificateTemplateData(
		pki.InstanceData{
			ID:       inst.InstanceID,
			Kind:     tenant.Kind,
			Arch:     tenant.Arch,
			Type:     tenant.InstanceType,
			Hostname: inst.Hostname,
			FQDN:     inst.Hostname,
			IP4:      inst.IPv4,
			IP6:      inst.IPv6,
		},
		pki.ClusterData{ID: a.s.cfg.ClusterID, CACert: string(a.s.caCertPEM)},
		pki.ServerData{
			Shard:            a.s.cfg.ShardID,
			RegistrationAddr: registrationAddr,
			AgentAddr:        agentAddr,
		},
		pki.ProviderData{},
		tenant.Vars,
		nil,
	)
	names := make([]string, 0, len(tenant.Files))
	for name := range tenant.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fc := tenant.Files[name]
		switch fc.Kind {
		case "string":
			if s, ok := fc.Template.(string); ok {
				out = append(out, pendingFile{name, []byte(s), now})
			}
		case "env", "json":
			b, err := json.Marshal(fc.Template)
			if err != nil {
				return nil, err
			}
			out = append(out, pendingFile{name, b, now})
		case "certificate":
			tmpl, ok := fc.Template.(string)
			if !ok {
				return nil, fmt.Errorf("certificate file %q template must be a string", name)
			}
			cc := tenant.Certificates[tmpl]
			cn := tmpl
			if cc.CN != nil {
				cn = *cc.CN
			}
			pkiCfg := pki.CertificateConfig{Kind: cc.Kind, CN: &cn, Organization: cc.Organization, DNS: cc.DNS, IP: cc.IP, URI: cc.URI, Country: cc.Country, Province: cc.Province, Locality: cc.Locality, Street: cc.Street, PostalCode: cc.PostalCode, TTL: cc.TTL}
			processed, err := pki.ProcessCertificateTemplate(pkiCfg, data)
			if err != nil {
				return nil, err
			}
			keyPEM, err := a.s.cfg.Store.Get(ctx, a.s.publicKey(inst.InstanceID, baseKeyName(fc, name)))
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			cert, _, err := pki.GenerateClientCertificateWithConfig(a.s.caCertPEM, a.s.caKeyPEM, keyPEM, inst.InstanceID, processed)
			if err != nil {
				return nil, err
			}
			out = append(out, pendingFile{name, cert, now})
		}
	}
	return out, nil
}
