# Nstance <https://nstance.dev>
# Copyright The Nstance Authors
# SPDX-License-Identifier: Apache-2.0

mock_provider "aws" {
  mock_data "aws_region" {
    defaults = { region = "us-west-2" }
  }
  mock_resource "aws_launch_template" {
    defaults = { id = "lt-0123456789abcdef0", latest_version = 1 }
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
    secrets_provider = "aws-parameter-store"
  }
  account = {
    server_iam_role_arn         = "arn:aws:iam::123456789012:role/server"
    agent_iam_role_arn          = "arn:aws:iam::123456789012:role/agent"
    server_instance_profile_arn = "arn:aws:iam::123456789012:instance-profile/server"
    agent_instance_profile_arn  = "arn:aws:iam::123456789012:instance-profile/agent"
  }
  network = {
    vpc_id        = "vpc-test"
    vpc_cidr_ipv4 = "10.0.0.0/16"
    nat_mode      = "none"
    subnets       = { nstance = { us-west-2a = [{ id = "subnet-server" }] } }
    load_balancers = {
      native = {
        security_group_id = "sg-nlb"
        target_groups = [{
          arn           = "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/test/123"
          listener_port = 443
          target_port   = 8443
          proxy_port    = 18443
        }]
      }
    }
  }
  shard = "us-west-2a"
  zone  = "us-west-2a"
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
    condition = jsonencode(jsondecode(aws_s3_object.shard_config.content).tunnels) == jsonencode({
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
    condition = jsonencode(jsondecode(aws_s3_object.shard_config.content).load_balancers) == jsonencode({
      native  = { provider = "aws", target_groups = var.network.load_balancers.native.target_groups }
      api     = { provider = "tunnel", listeners = [{ target_port = 6443, proxy_port = 16443 }] }
      ingress = { provider = "tunnel", listeners = [{ target_port = 8080, proxy_port = 18080 }, { target_port = 443, proxy_port = 18444 }] }
    })
    error_message = "Generated backends must preserve native metadata and every tunnel listener."
  }
  assert {
    condition     = toset(jsondecode(aws_s3_object.shard_config.content).groups.default.backend.load_balancers) == toset(["native", "api", "ingress"])
    error_message = "Groups must retain their tunnel membership."
  }
  assert {
    condition     = toset(keys(aws_vpc_security_group_ingress_rule.agent_wake_proxy)) == toset(["8443", "6443", "8080", "443"])
    error_message = "Wake proxies must reach native and tunnel target ports, not proxy ports."
  }
  assert {
    condition = alltrue([
      for rule in values(aws_vpc_security_group_ingress_rule.agent_wake_proxy) :
      rule.referenced_security_group_id == aws_security_group.server.id && rule.cidr_ipv4 == null && rule.cidr_ipv6 == null
    ]) && length(aws_vpc_security_group_ingress_rule.server_load_balancer_proxy) == 1 && length(aws_vpc_security_group_ingress_rule.agent_load_balancer) == 1
    error_message = "Tunnel listeners must allow only private server-to-backend traffic, with no cloud load-balancer ingress."
  }
}
