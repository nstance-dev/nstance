// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package google

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

// TestNATRouteRefusesForeignRoute verifies neither ensure nor removal mutates
// an owned route that points to an unrelated instance.
func TestNATRouteRefusesForeignRoute(t *testing.T) {
	mutations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"name":"route","description":"nstance:cluster:red:subnet-a","nextHopInstance":"zones/zone/instances/nat-other"}`)
	}))
	t.Cleanup(server.Close)
	service, err := compute.NewService(context.Background(), option.WithHTTPClient(server.Client()), option.WithEndpoint(server.URL+"/"))
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{computeService: service, options: ProviderOptions{ProjectID: "project"}}
	req := provider.NATRouteRequest{
		DestinationCIDR:            "0.0.0.0/0",
		ClusterID:                  "cluster",
		Tenant:                     "red",
		InstanceSubnetID:           "subnet-a",
		ProviderInstanceID:         "nat-new",
		PreviousProviderInstanceID: "nat-old",
	}
	if err := p.EnsureNATRoute(context.Background(), req); err == nil {
		t.Fatal("foreign route was accepted")
	}
	if err := p.RemoveNATRoute(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if mutations != 0 {
		t.Fatalf("foreign route received %d mutations", mutations)
	}
}

func TestEnsureNAT64RouteUsesRequestedDestination(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/routes/") && r.Method == http.MethodGet:
			http.NotFound(w, r)
		case strings.HasSuffix(r.URL.Path, "/subnetworks/subnet-a"):
			_, _ = fmt.Fprint(w, `{"network":"global/networks/vpc"}`)
		case strings.HasSuffix(r.URL.Path, "/routes"):
			data := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(data)
			body = string(data)
			_, _ = fmt.Fprint(w, `{"name":"insert"}`)
		case strings.HasSuffix(r.URL.Path, "/wait"):
			_, _ = fmt.Fprint(w, `{"status":"DONE"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	service, err := compute.NewService(context.Background(), option.WithHTTPClient(server.Client()), option.WithEndpoint(server.URL+"/"))
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{computeService: service, config: provider.ProviderConfig{Region: "region", Zone: "zone"}, options: ProviderOptions{ProjectID: "project"}}
	err = p.EnsureNATRoute(context.Background(), provider.NATRouteRequest{
		ClusterID: "cluster", Tenant: "red", InstanceSubnetID: "subnet-a",
		ProviderInstanceID: "nat", DestinationCIDR: "64:ff9b::/96",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"destRange":"64:ff9b::/96"`) {
		t.Fatalf("route body = %s", body)
	}
	if !strings.Contains(body, `-nat64"`) {
		t.Fatalf("route body = %s", body)
	}
}

// TestPermittedNATNextHop verifies route ownership is limited to one cutover pair.
func TestPermittedNATNextHop(t *testing.T) {
	req := provider.NATRouteRequest{
		ProviderInstanceID:         "nat-new",
		PreviousProviderInstanceID: "nat-old",
	}
	for _, test := range []struct {
		instance string
		want     bool
	}{
		{"nat-new", true},
		{"nat-old", true},
		{"nat-other", false},
	} {
		nextHop := "zones/zone/instances/" + test.instance
		if got := permittedNATNextHop(req, nextHop); got != test.want {
			t.Errorf("permittedNATNextHop(%q) = %t, want %t", test.instance, got, test.want)
		}
	}
}

// TestMovePublicAddress verifies replacement removes the fixed address from
// the old VM and the temporary address from the new VM before assigning it.
func TestMovePublicAddress(t *testing.T) {
	var mutations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/instances/nat-old"):
			_, _ = fmt.Fprint(w, `{"networkInterfaces":[{"name":"nic0","accessConfigs":[{"name":"External NAT","type":"ONE_TO_ONE_NAT","natIP":"192.0.2.1"}]}]}`)
		case strings.HasSuffix(r.URL.Path, "/instances/nat-new"):
			_, _ = fmt.Fprint(w, `{"networkInterfaces":[{"name":"nic0","accessConfigs":[{"name":"External NAT","type":"ONE_TO_ONE_NAT","natIP":"192.0.2.99"}]}]}`)
		case strings.HasSuffix(r.URL.Path, "/instances/nat-old/deleteAccessConfig"):
			mutations = append(mutations, "delete-old")
			_, _ = fmt.Fprint(w, `{"name":"delete-old"}`)
		case strings.HasSuffix(r.URL.Path, "/instances/nat-new/deleteAccessConfig"):
			mutations = append(mutations, "delete-new")
			_, _ = fmt.Fprint(w, `{"name":"delete-new"}`)
		case strings.HasSuffix(r.URL.Path, "/instances/nat-new/addAccessConfig"):
			mutations = append(mutations, "add-new")
			_, _ = fmt.Fprint(w, `{"name":"add-new"}`)
		case strings.HasSuffix(r.URL.Path, "/wait"):
			_, _ = fmt.Fprint(w, `{"status":"DONE"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	service, err := compute.NewService(context.Background(), option.WithHTTPClient(server.Client()), option.WithEndpoint(server.URL+"/"))
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{computeService: service, config: provider.ProviderConfig{Zone: "zone"}, options: ProviderOptions{ProjectID: "project"}}
	err = p.movePublicAddress(context.Background(), provider.NATRouteRequest{
		ProviderInstanceID:         "nat-new",
		PreviousProviderInstanceID: "nat-old",
		PublicAddress:              &provider.PublicAddress{IPv4: "192.0.2.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"delete-old", "delete-new", "add-new"}
	if !reflect.DeepEqual(mutations, want) {
		t.Fatalf("mutations = %v, want %v", mutations, want)
	}
}
