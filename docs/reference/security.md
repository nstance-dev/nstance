---
title: "Security"
weight: 32
description: "Security model including mTLS authentication, certificate management, and encryption."
---

# Security

This page documents Nstance's security model, which uses:

- **TLS on all gRPC endpoints** for in-transit protection.
- **Short-lived registration nonce JWTs** for bootstrap identity.
- **mTLS client certificates** for ongoing authenticated API access.
- **Role-based authorization** (`agent` vs `operator`) enforced server-side.
- **Tenant scoping** embedded in client certificates.
- **Encrypted secret storage** with pluggable backends.

At a high level:

- The registration endpoint uses TLS and accepts anonymous clients for initial bootstrap only, and those clients must present a valid nonce JWT.
- Agent and operator APIs require mTLS and reject clients without valid certificates signed by the cluster CA.
- Sensitive key material (`ca.key`, `registration-nonce.key`) is loaded from the configured secrets store.

## Transport Security

Nstance Server uses TLS 1.3 for all gRPC services:

| Endpoint | Default purpose | TLS mode |
|----------|-----------------|----------|
| Registration (`:8992`) | Initial bootstrap registration | TLS server-auth only (`NoClientCert`) |
| Agent (`:8994`) | Agent operations | Mutual TLS (`RequireAndVerifyClientCert`) |
| Operator (`:8993`) | Operator operations | Mutual TLS (`RequireAndVerifyClientCert`) |

This means:

- Registration is encrypted and server-authenticated, but client identity is established using nonce JWT + issued certificate flow.
- After registration, all authenticated operations move to mTLS-protected endpoints.

## Identity Bootstrap (Registration Nonce JWT)

Initial identity is established through registration nonce JWTs signed with an Ed25519 private key (`registration-nonce.key`).

### Required claims

The server validates nonce JWTs and requires the following claims:

- `kind` (`agent` or `operator`)
- `sub` (instance ID for agents, cluster ID for operators)
- `cluster_id`
- `tenant`
- standard time validity (`exp`, `nbf` where present)

Additional informational claims are included but not enforced by the JWT validator:

- `shard` (validated downstream during agent registration)
- `config_hash` (group runtime config hash at provision time)
- `group` (group key)
- `on_demand` (whether instance is on-demand)

### Additional validation

- **Agent registration** checks:
  - `kind == "agent"`
  - `cluster_id` matches server config
  - `shard` matches server config
  - nonce exists in local SQLite state
  - nonce has not already been used (`registered_at` check)

- **Operator registration** checks:
  - `kind == "operator"`
  - `cluster_id` matches server config
  - nonce passes operator nonce validation in SQLite

This prevents replay and cross-cluster misuse of nonce tokens.

## mTLS Authentication and Authorization

After registration, clients authenticate with certificates issued by the cluster CA.

### Certificate requirements

- Client certificate chain must validate against cluster CA.
- Certificate must include:
  - Common Name (`CN`) used as client ID.
  - Exactly one `Organization` value, used as tenant identity.
  - Custom role extension OID `1.3.6.1.4.1.999999.1` with role (`agent` or `operator`). Note: this OID uses an unregistered Private Enterprise Number and is intended for internal use only.

### Role enforcement

Server enforces service-level authorization:

- Agent service requires role `agent`.
- Operator service requires role `operator`.

If role does not match required endpoint role, request is rejected (`PermissionDenied`).

## PKI and Certificate Lifecycle

### CA and key material

- `ca.crt` is loaded from cluster-scoped object storage.
- `ca.key` is loaded from the configured secrets store.
- If CA material does not exist, server bootstrap generates it and stores:
  - cert in object storage (`ca.crt`)
  - private key in secrets store (`ca.key`)

### Registration nonce signing key

- `registration-nonce.key` is loaded from secrets store.
- If missing, only the **cluster leader** may generate and persist it.

### Client certificate issuance

Clients must submit an Ed25519 public key as part of the registration request. The server enforces this key type. On successful registration:

- Server signs a client certificate using the Ed25519 CA key, binding the client's Ed25519 public key.
- Tenant is embedded in certificate `Organization`.
- Role is embedded in custom OID extension.
- Certificate TTL is taken from config when provided, with defaults applied otherwise.
- A registration record is persisted to object storage and local SQLite for audit and state tracking.

### Certificate serial log (certlog)

For batch certificate generation (e.g. agent file generation), Nstance writes certificate serial logs to object storage under the `certlog/` prefix. Each log entry is a JSON file stored at `certlog/{tenant}.{timestamp}.{instanceID}.json` containing:

- Instance ID and tenant
- Issuance timestamp
- List of certificate names, serial numbers, and expiry times

This provides an append-only audit trail of all certificates issued, scoped by tenant.

### Renewal

Operator certificate renewal is supported via `OperatorService/RenewCertificate`:

- Requires valid existing operator mTLS certificate.
- Requires cluster leadership.
- Issues a new operator certificate for the authenticated cluster ID + tenant identity.

## Secret Storage and Encryption

Nstance uses a pluggable secrets store abstraction for sensitive values.

Typical core secrets:

- `ca.key`
- `registration-nonce.key`
- additional operator or workload secrets as configured

When using the `object-storage` secrets provider, Nstance performs client-side encryption of secret blobs using AES-256-GCM before writing them to the storage backend (S3, GCS, etc.):

- Algorithm: AES-256-GCM (Galois/Counter Mode), providing both confidentiality and integrity.
- Key size: Encryption keys must be exactly 32 bytes (256-bit). Keys that do not match this length are rejected at startup.
- Nonce: A cryptographically random 12-byte nonce is generated per-encryption using `crypto/rand`. Each encrypted blob is stored as `nonce (12 bytes) || ciphertext + GCM tag`, so the nonce travels with the data and does not need to be managed separately.
- Key sources: Encryption keys can be loaded from multiple providers:
  - `env` — environment variable
  - `file` — local file path
  - `aws-parameter-store` — AWS Systems Manager Parameter Store `SecureString` (by parameter name, such as `/nstance/<cluster-id>/encryption-key`)
  - `aws-secrets-manager` — AWS Secrets Manager secret (by ARN or name)
  - `gcp-secret-manager` — GCP Secret Manager secret (by name, with the `project_id` field)
- Key rotation: The configuration supports a primary `encryption_key` (used for all new writes) and a list of `old_encryption_keys` (used for decryption only). On read, Nstance attempts decryption with each configured key in order until one succeeds, allowing a rotation window where old ciphertexts remain readable while new writes use the current key.
- Optional: If no encryption keys are configured, secrets are stored and retrieved in plaintext. Encryption is strongly recommended for production deployments.

On AWS, `aws-parameter-store` is the default direct secrets store; `aws-secrets-manager` and `object-storage` are explicit alternatives. Parameter Store presents values as `[]byte` to Nstance but stores raw `SecureString` text without base64 encoding, so writes must be valid UTF-8 and no larger than the standard-tier 4 KiB limit. It uses `GetParameter` with decryption, `PutParameter`, and `DeleteParameter`.

On Google Cloud, `gcp-secret-manager` is the default direct secrets store. The Nstance server receives only the Secret Manager permissions needed to create, read, update, list, and delete secrets. Encrypted `object-storage` is available as an explicit alternative.

## Leadership and Security-Critical Operations

Several security-sensitive operations are leader-gated:

- Agent registration requires shard leadership.
- Operator registration requires cluster leadership.
- Operator certificate renewal requires cluster leadership.
- Registration nonce key generation is cluster-leader-only when key is missing.

This avoids split-brain issuance behavior across active replicas.

## Operational Hardening Guidance

For production deployments:

1. Restrict network access so registration, agent, and operator gRPC ports are reachable only by intended callers.
2. Treat the secrets backend as a high-trust boundary, especially for `ca.key` and `registration-nonce.key`.
3. Use least-privilege IAM for object storage and secret provider access.
4. Rotate encryption keys and signing material through controlled procedures.
5. Keep debug-only behavior (for example, gRPC reflection) disabled in production.
6. Configure appropriate certificate TTLs and monitor for expiring certificates, especially operator certificates which support renewal via `RenewCertificate`.
7. Periodically review `certlog/` entries in object storage for unexpected certificate issuance.
