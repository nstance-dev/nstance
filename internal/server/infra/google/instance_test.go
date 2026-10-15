// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package google

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/nstance-dev/nstance/v2/internal/server/infra/provider"
)

// TestCreateInstanceDiskSize verifies boot disk sizing through the Compute API.
func TestCreateInstanceDiskSize(t *testing.T) {
	for _, tt := range []struct {
		name    string
		value   any
		want    int64
		wantErr bool
	}{
		{name: "image default"},
		{name: "integer", value: 64, want: 64},
		{name: "decoded JSON", value: float64(96), want: 96},
		{name: "zero", value: 0, wantErr: true},
		{name: "negative", value: -1, wantErr: true},
		{name: "fractional", value: 64.5, wantErr: true},
		{name: "string", value: "64", wantErr: true},
		{name: "overflow", value: json.Number("9223372036854775808"), wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var inserted computepb.Instance
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/subnetworks/subnet"):
					_, _ = fmt.Fprint(w, `{"stackType":"IPV4_ONLY"}`)
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/instances"):
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					if err := protojson.Unmarshal(body, &inserted); err != nil {
						t.Error(err)
					}
					_, _ = fmt.Fprint(w, `{"name":"insert","status":"DONE"}`)
				case strings.HasSuffix(r.URL.Path, "/operations/insert"):
					_, _ = fmt.Fprint(w, `{"name":"insert","status":"DONE"}`)
				case strings.HasSuffix(r.URL.Path, "/instances/node"):
					_, _ = fmt.Fprint(w, `{"name":"node","status":"RUNNING"}`)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			ctx := context.Background()
			opts := []option.ClientOption{option.WithEndpoint(server.URL), option.WithHTTPClient(server.Client())}
			instances, err := compute.NewInstancesRESTClient(ctx, opts...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = instances.Close() })
			subnets, err := compute.NewSubnetworksRESTClient(ctx, opts...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = subnets.Close() })
			p := &Provider{
				instancesClient: instances, subnetClient: subnets,
				config:  provider.ProviderConfig{Region: "region", Zone: "zone"},
				options: ProviderOptions{ProjectID: "project"},
				logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			args := map[string]any{"SourceImage": "projects/example/global/images/pinned"}
			if tt.value != nil {
				args["DiskSizeGb"] = tt.value
			}
			_, err = p.CreateInstance(ctx, provider.CreateInstanceRequest{
				InstanceID: "node", InstanceType: "e2-medium", SubnetID: "subnet", Args: args,
			})
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "DiskSizeGb") || inserted.Name != nil {
					t.Fatalf("invalid size reached Compute API or was not rejected: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(inserted.Disks) != 1 {
				t.Fatalf("boot disks = %v", inserted.Disks)
			}
			disk := inserted.Disks[0]
			if tt.value == nil && disk.GetInitializeParams().DiskSizeGb != nil {
				t.Fatal("boot disk size must be omitted when not configured")
			}
			if disk.GetInitializeParams().GetDiskSizeGb() != tt.want || disk.GetInitializeParams().GetSourceImage() != args["SourceImage"] || !disk.GetBoot() || !disk.GetAutoDelete() {
				t.Fatalf("boot disk = %v, want size %d with pinned image and boot/auto-delete preserved", disk, tt.want)
			}
		})
	}
}
