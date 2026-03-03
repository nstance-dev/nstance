# AGENTS.md - Nstance Development Guide

## Build/Test Commands
- **Build**: `make build`
- **Clean**: `make clean`
- **Test**: `make test` (run all tests)
- **Test single package**: `go test ./internal/buildvars`
- **Typecheck**: `go build ./...`
- **Format**: `make fmt`
- **Lint**: `make lint`
- **Development**: `make clean-dev && make dev` (uses Overmind with Air for live reload)
- **View dev logs**: `timeout 5 overmind echo -s ./.overmind.sock` (view all process logs with 5s timeout)
- **View specific process logs**: `timeout 5 overmind echo -s ./.overmind.sock server` (process names: s3, server, k8s, operator)
- **Log files**: All dev components except agents write logs to `temp/logs/` - `dev-s3.log`, `dev-k8s.log`, `operator.log`, `server-1.log`, `server-2.log`, etc. which is faster/better to use for finding dev logs

## Code Style Guidelines
- **File headers**: Include copyright and SPDX license header on all files
- **Imports**: Standard library first, then third-party, then local packages (grouped with blank lines)
- **Naming**: Use camelCase for variables/functions, PascalCase for exported items
- **Error handling**: Return errors explicitly, use fmt.Errorf for wrapping
- **Logging**: Use `slog` from stdlib for structured logging, except in the Operator use `go-logr/logr` (standard for kubebuild-generated controllers)
- **Comments**: Document exported functions and types, avoid obvious comments. Each package should have a `doc.go` file with a succinct package-level docblock.
- **Context**: Pass context.Context as first parameter when applicable
- **Command Line Flags**: Avoid adding any additional flags to binaries unless necessary
- **Code**: Focus on Go best practices and writing straightforward, idiomatic Go code
- **TODOs**: Try to fully implement things (avoid adding any new TODOs/unimplemented code), and if you do not complete something make sure you add a TODO comment and tell us about it when you do

## Directory Structure
- `api/`: Kubernetes custom resource API types
- `bin/`: Built binaries
- `cmd/`: Main component (nstance-admin, nstance-agent, nstance-operator, nstance-server) and dev component (dev-k8s, dev-s3) entry points
- `config/`: Generated Kubernetes CRD and RBAC manifests
- `docs/`: Documentation organized by topic
  - `docs/components/`: Component-specific docs (nstance-admin, nstance-agent, nstance-operator, nstance-server)
  - `docs/development/`: Local development setup and Kind-based dev workflows
  - `docs/features/`: Feature documentation (auto-scaling, certificates, health monitoring, multi-tenancy, spot instances, etc.)
  - `docs/providers/`: Infrastructure provider docs (AWS, Google Cloud, Proxmox)
  - `docs/reference/`: Reference material (Cluster API, data storage, leader election, security, server/operator config, etc.)
  - `docs/architecture.md`: System architecture overview
  - `docs/motivation.md`: Project motivation and design rationale
- `examples/`: Example configuration files (e.g. server config files, groups config file)
- `deploy/`: Deployment configurations (Helm charts, OpenTofu/Terraform modules, Proxmox scripts)
  - OpenTofu/Terraform modules use `deploy/tf/common/` as the source of truth for shared files. Provider-specific modules (`aws/`, `gcp/`) symlink `variables.tf` and `templates/` back to `common/`, so edits to the common files automatically apply to all providers.
- `images/`: Container image Dockerfiles for each component
- `internal/`: Private packages, with component-specific nesting (e.g. nstance-agent specific packages under `internal/agent`)
- `pkg/`: Public packages (clients, health, instance metadata, TOPSIS)
- `proto/`: Protocol Buffer service definitions
- `scripts/`: Shell scripts for development and testing

## Go Package Overview
```
cmd/
├── dev-k8s/                 # Lightweight fake/mock Kubernetes API server for local dev/testing
├── dev-s3/                  # Local S3-compatible server for local dev/testing
├── nstance-admin/           # Admin CLI and HTTP API entry point
├── nstance-agent/           # Agent binary entry point
├── nstance-operator/        # Operator binary entry point
├── nstance-server/          # Server binary entry point
│
internal/
├── admin/
│   ├── cmd/                 # CLI commands for nstance-admin
│   ├── server/              # HTTP API server for nstance-admin
│   └── service/             # Core logic for nstance-admin operations (used by both CLI & HTTP)
├── agent/
│   ├── cmd/                 # Agent command entry point and lifecycle management
│   ├── config/              # Configuration loading and validation for nstance-agent
│   ├── keygen/              # Key generation request handling for the agent
│   └── receiver/            # Secure file receiving and validation for the agent
├── buildvars/               # Build-time information (version, commit hash, date)
├── files/                   # Utilities for handling/validating PEM, JWT, etc.
├── identifiers/             # Validation helpers for Nstance identifier formats (cluster ID, shard ID, tenant ID, etc.)
├── identity/                # Agent identity management and certificate generation (used by agent & admin)
├── operator/
│   ├── config/              # Configuration loading from K8s secrets and environment
│   ├── connection/          # Persistent gRPC connections to nstance-server zone shards
│   ├── controller/          # K8s controllers for NstanceMachine, NstanceMachinePool, Pods
│   ├── drain/               # Graceful node draining and workload eviction
│   ├── leader/              # Operator runtime orchestration (registration, sync, drain)
│   ├── node/                # Cloud provider instance ID to Kubernetes Node resolution
│   ├── sync/                # Synchronization between K8s CAPI/Nstance resources and servers
│   └── webhooks/            # Validating admission webhooks for Nstance CRDs
├── proto/                   # Generated Protocol Buffer types for gRPC services
├── renewal/                 # Operator client certificate renewal for long-running processes (used by admin & operator)
├── server/
│   ├── api/                 # Main gRPC server and shared authentication logic
│   │   ├── agent/           # Agent gRPC service implementation
│   │   ├── operator/        # Operator gRPC service implementation
│   │   └── registration/    # Registration gRPC service implementation
│   ├── cluster/             # Cluster-level coordination and leader election
│   ├── cmd/                 # Server command entry point and lifecycle management
│   ├── config/              # Configuration loading and validation for nstance-server
│   ├── election/            # Unified manager for cluster and shard leader elections
│   ├── filegen/             # Certificate and key generation for instances
│   ├── gc/                  # Periodic garbage collection (provider sync, cleanup, health)
│   ├── health/              # HTTP health check endpoints for ASG/LB integration
│   ├── images/              # Periodic image resolution and caching across providers
│   ├── infra/               # Infrastructure provider abstraction and factory
│   │   ├── aws/             # AWS EC2 and NLB provider implementation
│   │   ├── gcp/             # Google Cloud Compute Engine provider implementation
│   │   ├── mock/            # Test mock provider implementation
│   │   ├── provider/        # Infrastructure provider interface definition
│   │   ├── proxmox/         # Proxmox VE provider implementation
│   │   └── tmux/            # Local development provider implementation using tmux
│   ├── instances/           # Instance lifecycle orchestration (IDs, registration, userdata)
│   ├── keys/                # Cryptographic key parsing and handling utilities
│   ├── localdb/             # Database models and operations for local server state
│   ├── pki/                 # Certificate and key generation for instance PKI
│   ├── reconciler/          # Infrastructure state reconciliation via event queue processing
│   ├── secrets/             # Encrypted secret storage with pluggable backends
│   └── storage/             # Object storage operations abstraction
│
pkg/
├── client/
│   ├── agent/               # gRPC client for agent services
│   └── registration/        # gRPC client for agent/operator registration
├── health/                  # System health metrics collection and reporting
├── instanceinfo/            # Cloud instance metadata provider interface and implementations
└── topsis/                  # TOPSIS multi-criteria decision analysis (used for VM placement e.g. in Proxmox)
```
