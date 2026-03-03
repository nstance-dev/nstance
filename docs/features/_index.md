---
title: "Features"
weight: 20
description: "Key features of the Nstance platform."
---

- **[Auto-Scaling](./auto-scaling.md)** — How Nstance automatically reconciles instance groups to maintain desired capacity.
- **[Certificates](./certificates.md)** — TLS certificate issuance, renewal, and management for instances.
- **[Health Monitoring](./health-monitoring.md)** — Agent health reports, unhealthy node detection, and automatic instance replacement.
- **[Spot Instances](./spot-instances.md)** — Automatic detection and handling of spot and preemptible instance termination notices.
- **[Instance Expiry](./instance-expiry.md)** — Automatic instance rotation based on configurable age limits for compliance and security.
- **[Load Balancers](./load-balancers.md)** — Automatic registration and deregistration of instances with cloud provider load balancers.
- **[On-Demand Nodes](./on-demand-nodes.md)** — Provisioning individual instances on-demand via Pod annotations for specific workload requirements.
- **[Multi-Tenancy](./multi-tenancy.md)** — How multiple Kubernetes clusters can run on a single Nstance cluster with tenant isolation.
- **[Subnet Pools](./subnet-pools.md)** — Logical subnet pool system that maps human-readable names to provider-specific subnet IDs for portable group configurations.
