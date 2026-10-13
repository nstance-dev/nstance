# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

mock_provider "google" {
  mock_data "google_client_config" {
    defaults = { project = "tunnel-test" }
  }
}
mock_provider "random" {}
mock_provider "null" {}
mock_provider "tls" {}
mock_provider "cloudinit" {}

variables {
  cluster = {
    id               = "tunnel-test"
    bucket           = "tunnel-test"
    secrets_provider = "google-secret-manager"
    project_id       = "tunnel-test"
  }
  account = {
    server_iam_role_arn = "server@tunnel-test.iam.gserviceaccount.com"
    agent_iam_role_arn  = "agent@tunnel-test.iam.gserviceaccount.com"
  }
  network = {
    vpc_id        = "projects/tunnel-test/global/networks/test"
    vpc_cidr_ipv4 = "10.0.0.0/16"
    nat_mode      = "none"
    subnets       = { nstance = { us-central1-a = [{ id = "projects/tunnel-test/regions/us-central1/subnetworks/server" }] } }
    load_balancers = {
      native = {
        network_endpoint_groups = { us-central1-a = ["native-neg"] }
        frontends               = [{ ip = "192.0.2.1", port = 443 }]
      }
      other-zone = {
        network_endpoint_groups = { us-central1-b = ["other-neg"] }
        frontends               = [{ ip = "192.0.2.2", port = 8443 }]
      }
    }
  }
  shard = "us-central1-a"
  zone  = "us-central1-a"
}

run "tunnel_config" {
  variables {
    tunnels = {
      api = {
        listeners         = [{ target_port = 6443, proxy_port = 16443 }]
        image             = "example.invalid/tunnel@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
        args              = ["tunnel", "--config", "/run/tunnel/config.json"]
        readiness_url     = "http://127.0.0.1:2000/ready"
        readiness_timeout = "45s"
        files = {
          "/run/tunnel/config.json" = {
            kind = "json"
            template = {
              origin = "https://127.0.0.1:16443"
              tls    = { verify = true, ports = [16443, 18080] }
              label  = "{{ .Vars.KEY }}"
            }
          }
          "/run/tunnel/credentials.json" = { kind = "secret", source = "tunnel-credentials" }
        }
      }
      ingress = {
        listeners = [
          { target_port = 8080, proxy_port = 18080 },
          { target_port = 443, proxy_port = 18444 },
        ]
        image         = "example.invalid/tunnel@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
        readiness_url = "http://127.0.0.1:2001/ready"
        files         = { "/run/tunnel/config.json" = { kind = "storage", source = "ingress.json" } }
      }
    }
    groups = {
      default = { backend = { size = 1, subnet_pool = "nstance", load_balancers = ["native", "api", "ingress"] } }
    }
  }

  assert {
    condition = jsonencode(jsondecode(google_storage_bucket_object.shard_config.content).tunnels) == jsonencode({
      api = {
        image             = var.tunnels.api.image
        args              = var.tunnels.api.args
        readiness_url     = var.tunnels.api.readiness_url
        readiness_timeout = var.tunnels.api.readiness_timeout
        files             = var.tunnels.api.files
      }
      ingress = {
        image         = var.tunnels.ingress.image
        readiness_url = var.tunnels.ingress.readiness_url
        files         = var.tunnels.ingress.files
      }
    })
    error_message = "Runtime fields must pass through unchanged, without listener metadata."
  }
  assert {
    condition = jsonencode(jsondecode(google_storage_bucket_object.shard_config.content).load_balancers) == jsonencode({
      native  = { provider = "google", network_endpoint_groups = ["native-neg"], frontends = [{ ip = "192.0.2.1", port = 443 }] }
      api     = { provider = "tunnel", listeners = [{ target_port = 6443, proxy_port = 16443 }] }
      ingress = { provider = "tunnel", listeners = [{ target_port = 8080, proxy_port = 18080 }, { target_port = 443, proxy_port = 18444 }] }
    })
    error_message = "Generated backends must preserve zone-filtered native metadata and every tunnel listener."
  }
  assert {
    condition     = toset(jsondecode(google_storage_bucket_object.shard_config.content).groups.default.backend.load_balancers) == toset(["native", "api", "ingress"])
    error_message = "Groups must retain their tunnel membership."
  }
}
