---
title: "Architecture"
weight: 10
description: "Overview of the Nstance architecture, components, and how they interact."
---

# Architecture

### Zone Shards

Note the term "shard", sometimes referred to as "zone shard", is the partitioning terminology the Nstance architecture uses.

Rather than running a single Nstance server per Availability Zone (AZ), the Nstance has its own concept of a "shard" to enable dividing a single AZ up into multiple partitions.

The primary motiviation for this it because the Nstance Server component is designed to be vertically-scaled only. Meaning that once you have reached a max capacity for a single Nstance Server, your next step is to introduce an addition shard. Keep in mind that this is unlikely to be required; we expect most use cases will run a single shard / Nstance Server per AZ.

Nstance Server itself implements leader election, allowing you to run one or more "hot standby" instances per shard.

### Component Overview

Nstance consists of 4 major components:

- **[Server](components/nstance-server.md)**: run per-shard, responsible for managing instances and issuing certificates for instances in the same shard.
- **[Agent](components/nstance-agent.md)**: run per-VM instance, responsible for instance registration, key generation and certificate retrieval, and health reporting.
- **[Operator](components/nstance-operator.md)**: a Kubernetes controller, responsible for syncing Cluster API resources with each Nstance Server when using Kubernetes.
- **[Admin](components/nstance-admin.md)**: a CLI or HTTP API for admin tasks and an alternative to the Operator for non-Kubernetes use cases.

### Component Interaction

```
┌───────────────────────────────────────────────┐
│  Kubernetes Cluster                           |
│  ┌─────────────────┐    ┌──────────────────┐  |
│  │ Cluster API     │    │ Nstance          │  |
│  │ Resources       │◄───┤ Operator         │  |
│  └─────────────────┘    └──────────────────┘  |
|                               │               |
└───────────────────────────────|───────────────┘
                                ▼ sync MachinePool's & Machine's for Zone Shard
                          ┌───────────────────┐             ┌─────┐
        ┌────────────────►│ Nstance Server(s) │ ◄───────────| ASG |
        | report health   │ (Per Zone Shard)  │  (optional) └─────┘
        |                 └───────────────────┘
        |                       │ create/delete Machine VM instances
        |                       ▼
    ┌──────────────────┐  ┌───────────────────┐
    │ Nstance Agent    │--│ VM Instances      │
    │ (Per VM)         │  └───────────────────┘
    └──────────────────┘
```

### Infrastructure Setup

Each VM runs an Nstance Agent which reports instance health back to the Nstance Server in the same shard.

#### Nstance Server Infrastructure

Per shard only one Nstance Server is required.

* This can run on a Kubernetes node, or a dedicated node, depending on your security and cost preferences.

It should never need to scale to multiple instances - even with thousands of instances connected to it;

* Nstance Server is designed to be scaled vertically.
* The leader election mechanism allows for hot standby's to exist for fast failover. 
* It should work with hundreds of nodes on a `t4g.nano` on AWS, for example.
* This design removes the need for a load balancer per zone just for the Nstance Server.
* Instead, the active Nstance Server leader is attached to a fixed IP / network inteface / VIP.

On AWS for example, per-AZ you would create:

1. an ENI with IPv4 and/or IPv6 address
2. an ASG with desired_capacity = 1, and the ARN of the ENI injected in user data
3. an ASG health check using the HTTP health endpoint (port 8990) for boot readiness validation

Importantly, the ASG would not have any attachment to the ENI - Nstance Server handles this after leader election. This means that per-shard there's a single, fixed IPv4 and/or IPv6 address for connecting to the server.

The HTTP health endpoint returns `503` during boot and `200` after successful initialization (config loaded and synced to SQLite), ensuring ASGs don't consider instances healthy until fully operational and ready/available to become a leader.

This architecture, combined with object storage for persistence, simplifies the operational requirements and minimises costs.

## Provider Abstraction & API Access

Nstance is designed for easy addition of new cloud providers and on-premise support through standardized provider interfaces.

Supported operations:

1. **Instance Management**: Create, list, read, & delete VMs.

2. **IP/Network Interface Association**: Attach Nstance Server to a fixed IP on boot/replacement.

3. **Secret Reads**: Read an Encryption Key. Note this is optional; files can be used instead. Alternatively, a secrets store can be used for all secrets rather than using the Encryption Key + object storage approach.

With this design, Nstance Server is the only component that should require support for multiple providers, and the only component that requires cloud provider API access, providing a clear security boundary.
