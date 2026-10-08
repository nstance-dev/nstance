---
title: "Nstance Agent"
weight: 20
description: "A lightweight agent running on each VM instance, handling registration, health reporting, certificate management, and spot instance detection."
---

# Nstance Agent

The `nstance-agent` is a lightweight daemon that runs on every VM instance managed by Nstance. It handles registering the instance with the Nstance Server, generating keypairs for certificates, receiving files (certificates, secrets, environment files), reporting health metrics, and detecting termination notices (for spot instances).

## CLI Flags

The agent binary accepts minimal CLI flags. Configuration is done through environment variables.

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--debug` | `-v` | `false` | Enable debug logging |
| `--version` | | `false` | Show version information |

## Environment Variables

All environment variables use the `NSTANCE_` prefix.

| Variable | Default | Description |
|----------|---------|-------------|
| `NSTANCE_DEBUG` | `false` | Enable debug logging |
| `NSTANCE_ENVIRONMENT` | `development` | Environment name |
| `NSTANCE_SERVER_REGISTRATION_ADDR` | *(required)* | Nstance Server registration endpoint (`host:port`) |
| `NSTANCE_SERVER_AGENT_ADDR` | *(required)* | Nstance Server agent endpoint (`host:port`) |
| `NSTANCE_IDENTITY_DIR` | `/opt/nstance-agent/identity` | Directory for identity files |
| `NSTANCE_KEYS_DIR` | `/opt/nstance-agent/keys` | Directory for generated keypair files |
| `NSTANCE_RECV_DIR` | `/opt/nstance-agent/recv` | Directory for received files |
| `NSTANCE_IDENTITY_MODE` | `0600` | File permissions for identity files (octal) |
| `NSTANCE_KEYS_MODE` | `0640` | File permissions for keypair files (octal) |
| `NSTANCE_RECV_MODE` | `0640` | File permissions for received files (octal) |
| `NSTANCE_INSTANCE_KIND` | *(empty)* | Instance kind (3 char ID prefix) |
| `NSTANCE_INSTANCE_ID` | *(required)* | Instance ID (puidv7 format) |
| `NSTANCE_INSTANCE_HOSTNAME` | *(optional)* | Instance hostname |
| `NSTANCE_INSTANCE_FQDN` | *(optional)* | Instance FQDN |
| `NSTANCE_INSTANCE_IPV4` | *(optional)* | Instance IPv4 address |
| `NSTANCE_INSTANCE_IPV6` | *(optional)* | Instance IPv6 address |
| `NSTANCE_REPORT_INTERVAL` | `60s` | Health report interval (`0` to disable) |
| `NSTANCE_METRICS_INTERFACE` | *(optional)* | Network interface to collect rate and conntrack metrics for |
| `NSTANCE_EBPF_COUNTERS_PATH` | *(optional)* | Directory containing the pinned active-connection link and map |
| `NSTANCE_SPOT_POLL_INTERVAL` | `2s` | Spot termination polling interval |
| `NSTANCE_HEALTH_ADDR` | *(disabled)* | Optional HTTP liveness listener (`host:port`); empty disables it |

## Health Endpoint

The agent can expose a small HTTP endpoint for local monitoring or load-balancer
checks. It is disabled by default. To enable it for local checks, set:

```bash
export NSTANCE_HEALTH_ADDR=127.0.0.1:8080
```

After starting the agent, check it with:

```bash
curl --fail http://127.0.0.1:8080/healthz
```

`GET /healthz` returns `200 OK` with the plain-text body `ok` and no newline; `HEAD`
is also supported. The listener starts after registration and local
initialization, and closes when the agent shuts down. If the configured address
cannot be bound, the agent exits with an error. Unset `NSTANCE_HEALTH_ADDR` or
leave it empty to disable the listener.

This is an agent liveness check, not an application readiness check. It does not
test ongoing connectivity to nstance-server or the workloads on the instance,
and it works independently of `NSTANCE_REPORT_INTERVAL`.

The endpoint uses plain HTTP without authentication and exposes no metrics,
credentials, or debug routes. Prefer a loopback address. For remote probes, use
an instance address or `0.0.0.0:8080` and restrict access with firewall rules.
IPv6 addresses must be bracketed, for example `[::1]:8080` for local checks or
`[::]:8080` for a wildcard listener; IPv4 access through an IPv6 wildcard depends
on the host's network configuration.

## Agent Lifecycle

The agent progresses through several phases after startup:

### 1. Registration

On first boot, the instance userdata script writes two to the identity directory:

1. `ca.crt`: Nstance cluster CA certificate
2. `nonce.jwt`: Registration nonce token

The agent then:

1. Validates the nonce JWT structure and extracts the runtime config hash.
2. Generates an identity keypair (if not already present).
3. Sends the public key and nonce JWT to the Nstance Server's anonymous registration endpoint (`NSTANCE_SERVER_REGISTRATION_ADDR`) over a TLS connection verified against the CA certificate.
4. Receives and stores a signed client certificate.

Unrecoverable errors (e.g. invalid nonce) cause the agent to hang rather than retry. 

Transient server errors trigger a jitter-delayed exit to avoid thundering herd problems on restart.

### 2. Service Connection

After registration, the agent connects to the Nstance Server's agent gRPC service (`NSTANCE_SERVER_AGENT_ADDR`) using its client certificate for mutual TLS authentication.

### 3. File Streaming

The agent receives files from the server over the gRPC connection and writes them to the receive directory (`NSTANCE_RECV_DIR`). These files can include certificates, secrets, and environment files. Each file is written atomically with the configured file permissions (`NSTANCE_RECV_MODE`).

### 4. Key Generation

When the server needs a new certificate issued for this instance, it sends a key generation request. The agent generates a keypair in the keys directory (`NSTANCE_KEYS_DIR`), and sends the public key back to the server. The server then issues a certificate and delivers it via file streaming.

### 5. Health Reporting

The agent periodically sends health reports to the server at the configured interval (`NSTANCE_REPORT_INTERVAL`). Reports include operational metadata and the latest purpose-specific metrics observation. Health reporting can be disabled by setting the interval to `0`.

### 6. Spot Monitoring

The agent automatically detects whether it is running on a preemptible spot instance instance, and polls the cloud provider's metadata endpoint for termination notices at `NSTANCE_SPOT_POLL_INTERVAL`. When a termination notice is detected, the agent notifies the server so it can initiate graceful draining.

## Directory Structure

The agent uses three directories, each configurable via environment variables:

### Identity Directory (`NSTANCE_IDENTITY_DIR`)

```
/opt/nstance-agent/identity/
├── ca.crt          # Cluster CA certificate (from userdata)
├── nonce.jwt       # Registration nonce (consumed during registration)
├── identity.key    # Agent's private key (generated during registration)
└── identity.crt    # Agent's signed client certificate (from server)
```

### Keys Directory (`NSTANCE_KEYS_DIR`)

```
/opt/nstance-agent/keys/
└── <key-id>.key    # Generated keypairs for certificate issuance
```

### Receive Directory (`NSTANCE_RECV_DIR`)

```
/opt/nstance-agent/recv/
├── *.crt           # Certificates received from the server
├── *.env           # Environment files
└── ...             # Other files (secrets, etc.)
```

## Network Requirements

The agent requires outbound connectivity to two Nstance Server endpoints:

- **Registration endpoint** (`NSTANCE_SERVER_REGISTRATION_ADDR`): Used once during initial registration (unauthenticated TLS).
- **Agent service endpoint** (`NSTANCE_SERVER_AGENT_ADDR`): Persistent gRPC connection using mutual TLS.

Both endpoints use gRPC over TLS. The registration endpoint may be the same host as the agent service endpoint but uses a different port.

## Reference

- [Instance Lifecycle](../reference/instance-lifecycle.md) — Full registration and lifecycle flow.
- [Files and Certificates](../reference/files-and-certificates.md) — Certificate chain and file delivery details.
- [Security](../reference/security.md) — Mutual TLS, nonce validation, and trust model.
