---
title: "Components"
weight: 30
description: "Core components of the Nstance architecture: Server, Agent, Operator, and Admin."
---

- **[Nstance Server](./nstance-server.md)** — The core control plane component responsible for configuration management, leader election, instance provisioning, health monitoring, and API services.
- **[Nstance Agent](nstance-agent.md)** — A lightweight agent running on each VM instance, handling registration, health reporting, certificate management, and spot instance detection.
- **[Nstance Operator](nstance-operator.md)** — A Kubernetes operator that synchronizes Cluster API resources with Nstance Server, managing instance groups, drain coordination, and on-demand nodes.
- **[Nstance Admin](nstance-admin.md)** — Command-line and HTTP API tool for managing Nstance clusters, including bootstrap operations, configuration management, and group scaling.
