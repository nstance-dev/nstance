---
title: "Network Address Translation"
weight: 65
description: "Choosing between provider-managed and Nstance-managed NAT44 and NAT64."
---

# Network Address Translation

Nstance supports IPv4-only, dual-stack, and IPv6-only workload networks on
AWS and Google Cloud. `ipv4_enabled` and `ipv6_enabled` select the workload
address families.

`nat_mode` then selects provider NAT, Nstance NAT instances,
or no translation. Depending on the address families, the selected service
provides NAT44 or NAT64.

The [OpenTofu/Terraform reference](../reference/opentofu-terraform.md#ip-and-nat-modes)
lists every valid combination and the exact module inputs.

## Choosing a NAT mode

### `provider`

Provider NAT uses AWS NAT Gateway or Google Cloud NAT. Choose it when you
prefer the cloud provider to operate and scale the translation service. It
reduces the Nstance operational surface, but incurs the provider's gateway and
traffic charges.

### `nstance`

Nstance NAT uses ordinary VM instances as the translation service. Choose it
to avoid managed-gateway costs or retain control of the NAT host. Nstance
creates, scales, replaces, and removes the NAT VMs as workload subnets need
them.

### `none`

No NAT is valid only for IPv6-only workload networks. Workloads use native
IPv6 routes and cannot reach IPv4-only destinations through translation.

## How Nstance NAT works

Nstance Server creates one active NAT instance for each tenant and populated
workload subnet. It creates the instance before the first dependent workload
and removes it only after the subnet has remained empty for the configured
grace period. The workload subnet route targets that instance's primary
network interface.

The NAT instance scales vertically through a configured instance-type ladder.
Nstance uses CPU utilization, conntrack utilization, and packet drops for
scaling decisions; byte and packet rates remain available for observability.
Replacement is create-before-destroy: the new instance must register and
report healthy before Nstance moves the route and optional fixed public IPv4
address, then removes the old instance. Route cutover can interrupt existing
translated connections, so this is not a stateful hot-standby design.

If a NAT instance fails or is deleted outside Nstance, the server replaces it
for the same workload subnet and restores the route after the replacement is
ready. Translation is unavailable while the replacement boots. Nstance manages
only the translation destination: `0.0.0.0/0` for IPv4 NAT or `64:ff9b::/96` for
NAT64. It corrects that route's target even if it was changed manually, leaving
peering routes and native IPv6 routes unchanged. Recovery after a restart does
not require the deleted VM's network interface to remain discoverable. If a
replacement fails during cutover and the previous NAT instance is still
healthy, Nstance restores its route and fixed address before discarding the
failed replacement. Missing or stale health leaves the existing route in
place. When the surviving NAT reports healthy again, reconciliation restores
its route and fixed address without waiting for another workload to be created,
even if the server restarted during recovery.

## How IPv6-only workloads reach IPv4 services

An IPv6-only workload cannot connect directly to an IPv4 address. NAT64 and
DNS64 bridge that gap without assigning IPv4 addresses to the workload:

1. The workload looks up a hostname.
2. If the hostname has only an IPv4 address, DNS64 returns a synthetic IPv6
   address in `64:ff9b::/96`.
3. The workload connects to that IPv6 address.
4. NAT64 translates the connection to IPv4 and forwards it to the destination.

Hostnames with native IPv6 addresses continue to use IPv6 directly and bypass
translation.

Applications should resolve hostnames rather than depend on IPv4 literals:
DNS64 cannot synthesize a destination from an address embedded directly in
application configuration. Protocols that carry or validate IP addresses in
their payloads may also require application-specific support.

With `nat_mode = "provider"`, AWS NAT Gateway or Google Cloud NAT performs the
translation. With `nat_mode = "nstance"`, Nstance routes the prefix through a
NAT instance running Jool. The minimal demonstration userdata installs Jool
from Debian packages during first boot. Production deployments should instead
use a hardened image or userdata that supplies and configures Jool without
depending on boot-time package installation.

## Stable public IPv4 addresses

Nstance NAT can optionally associate a reserved public IPv4 address with each
active NAT instance. This gives translated IPv4 traffic a stable egress
identity without reserving a second address for replacement. Once the new
instance is healthy, Nstance reassigns the address and changes the workload
route as part of the cutover.

Without a fixed address, the NAT VM uses its ordinary provider-assigned public
address and the egress address may change when the VM is replaced. Fixed
public IPv4 addresses do not affect native IPv6 traffic.

## Changing modes

The network modules preserve workload subnets when switching between provider
and Nstance NAT. Switching to provider NAT creates the provider path before
Nstance retires its NAT instances. Switching to Nstance NAT must wait for a
healthy NAT instance and can temporarily interrupt translated egress while
Nstance establishes the new route.

See the [`nat` server configuration](../reference/server-config.md#nat) for
Nstance NAT lifecycle and scaling settings.
