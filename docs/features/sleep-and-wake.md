---
title: "Sleep and Wake"
weight: 35
description: "How Nstance safely sleeps an idle tenant and wakes it on demand."
---

# Sleep and Wake

Nstance can reduce a tenant's managed groups to zero while preserving a network path that wakes it on demand.

Nstance decides when the tenant is eligible for automatic sleep based on network activity to configured ports. For example on Kubernetes, this might be both your ingress controller and the Kubernetes API server. A sleep "guard" prevents automatic sleep if there is network activity on the configured ports.

Any existing on-demand instance blocks sleep. Forced sleep may bypass the network activity guard, but never the on-demand instance protection.

Nstance owns routing cutover to a "wake sleep" proxy and managing the instance lifecycle from sleep (scale-to-zero) to wake (scale-up from zero).

## Sleeping

For guarded sleep, Nstance:

1. Establishes the wake-proxy path before withdrawing production targets.
2. Stops the production path from receiving new connections.
3. Waits for fresh port-activity reports from the remaining relevant instances.
4. Refuses sleep on active connections, missing counters, stale reports, or collection errors.
5. Records the tenant as asleep and terminates its managed instances only after the guard passes.

If any step fails, Nstance restores production routing before withdrawing the proxy path.

## Waking

The `nstance-server proxy` command is designed to run as an unprivileged process which accepts traffic while the tenant is asleep.

On the first payload, it asks the local Nstance Server to wake the listener's tenant, waits for a ready private upstream, and relays the connection.

Nstance server restores production targets before removing the temporary proxy path.

An operator request, scheduled wake deadline, or on-demand instance request can also wake a tenant. Creating an on-demand instance always wakes the tenant prior to instance creation.

## eBPF Port Activity Monitoring

Nstance uses low-overhead, event-driven eBPF counters as its final sleep safety check/gaurd.

A higher-level deployment selects the production TCP ports, loads the eBPF program, and exposes its pinned link and counter map to Nstance Agent. The kernel updates compact counters on TCP state changes; there is no packet capture or userspace polling.

The agent remains unprivileged. It validates the pinned link, reads the map, and includes active connection counts keyed by port in its normal health report.

### Kubernetes Example

A Kubernetes deployment commonly has two independent traffic paths:

| Purpose | Typical ports |
|---|---|
| Kubernetes API server | `6443` |
| Ingress controller | `80`, `443` |

Using application metrics would require separate integrations for the API server and whichever ingress controller is installed. Selected-port accounting uses one small, generic mechanism for both. The deployment remains responsible for choosing the ports; Nstance has no Kubernetes, ingress-controller, or tunnel-provider logic.

### Why eBPF

Interface counters include unrelated control-plane, logging, health-check, and image traffic. Polling `ss` or `/proc` can miss connections between samples, while conntrack and netfilter do not cover every networking implementation consistently. An event-driven eBPF program observes TCP state transitions without changing the traffic path.

The loader pins both the program link and map. Pinning only the map could leave a stale zero-valued map after the program detached, so the agent validates the link before trusting the counters.

The report contains only active counts by configured port. It does not contain client addresses, connection tuples, packet contents, or application-specific data. See [Health Monitoring](./health-monitoring.md) for the report fields.
