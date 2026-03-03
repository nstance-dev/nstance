---
title: "Reference"
weight: 50
description: "Detailed reference documentation for Nstance internals and configuration."
---

- **[OpenTofu / Terraform](./opentofu-terraform.md)** — OpenTofu/Terraform modules for deploying Nstance infrastructure across AWS and Google Cloud.
- **[Files & Certificates](./files-and-certificates.md)** — How Nstance Server generates and sends files & certificates to Nstance Agent.
- **[Secrets Management](./secrets-management.md)** — Encryption key management, CA private keys, and secure secret distribution to instances.
- **[Data Storage](./data-storage.md)** — Object storage layout, SQLite caching, and data persistence model.
- **[Security](./security.md)** — Security model including mTLS authentication, certificate management, and encryption.
- **[Leader Election](./leader-election.md)** — How Nstance Server uses object storage-based leader election for shard and cluster leadership.
- **[Instance Lifecycle](./instance-lifecycle.md)** — Instance provisioning, registration, health monitoring, drain coordination, and deletion.
- **[Server Configuration](./server-config.md)** — Detailed reference for all Nstance Server configuration options.
- **[Templates & Vars](./templates-and-vars.md)** — Instance templates, variable hierarchy, userdata templates, and args merge strategy.
- **[Server API](./server-api.md)** — gRPC API services exposed by the Nstance Server for agent and operator communication.
- **[Cluster API Integration](./cluster-api.md)** — How Nstance implements the Cluster API (CAPI) infrastructure provider contract for infrastructure management in Kubernetes.
- **[Operator Internals](./operator-internals.md)** — Sync mechanics, reconciliation loops, drain coordination, CRDs, and connection management.
