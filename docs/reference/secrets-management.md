---
title: "Secrets Management"
weight: 20
description: "Encryption key management, CA private keys, and secure secret distribution to instances."
---

# Secrets Management

Nstance Server has two main secrets:

1. **CA Private Key(s)**: For certificate issuance.

2. **Registration Nonce Key(s)**: For signing of Registration Nonce JWTs which can be used for API client registration by Nstance Agents and Nstance Operator.

It can also support additional secrets which can be securely distributed to new VM instances via Nstance Agent, with distribution defined per instance template.

For example, custom secrets you might use:

1. **Kubernetes Service Account Private Key(s)**: For service account tokens (kube-apiserver service-account-signing-key-file). Rotation support planned.

2. **Tunnel JSON**: Used for Cloudflare Tunnels to Kubernetes API server instances.

Note that secrets support multiple keys, which makes rotation support possible simply by generating a 2nd private key, flowing this through the various components, and (once the rotation lifecycle completes) removing the old/1st private key. All components must therefore prioritise the newest/first key in a secret first.

## Encryption Key

Nstance Server has a single "Encryption Key" which can be used to encrypt any sensitive data stored in object storage, such as the secrets outlined above like the CA private key.

Note that the Encryption Key value can contain a single key, or two keys for support for key rotation.

Depending on the provider, an object-storage Encryption Key can be stored in one of the following systems:

| Provider | Service |
|----------|---------|
| AWS | Systems Manager Parameter Store (default key source) or Secrets Manager |
| Azure | Key Vault |
| Google Cloud | Secret Manager |
| Other | HashiCorp Vault |
| Other | On-Disk |

Because folks using On-Prem may not wish to incur the overhead of systems like HashiCorp Vault, they may instead choose to run multiple static standby Nstance Server VMs (rather than using an ASG) with keys stored securely on a persistent volume (or some other secure mechanism) attached to that VM, keeping the Nstance goal of being easy to operate.

By using an Encryption Key:

* It reduces required permissions to read-only for secrets store access.

  * For example, rotating the CA private key does not require a write operation to Parameter Store or AWS Secrets Manager.

* It simplifies support for multiple clouds and requirements for on-prem.

AWS uses `aws-parameter-store` as its default direct secrets backend. `aws-secrets-manager` and encrypted `object-storage` remain explicit alternatives. Parameter Store can also be selected as the encryption-key source for object storage, using a name such as `/nstance/<cluster-id>/encryption-key`.

Nstance exposes secret values as `[]byte`, while Parameter Store stores raw `SecureString` text without a base64 envelope. Writes containing invalid UTF-8 are rejected, and standard-tier values are limited to 4 KiB. Nstance calls `GetParameter` with decryption, `PutParameter`, and `DeleteParameter`. This is AWS Systems Manager (SSM) **Parameter Store**, which is separate from SSM Session Manager.

Google Cloud uses `gcp-secret-manager` as its default direct secrets backend. Encrypted `object-storage` remains an explicit alternative, with Google Cloud Secret Manager holding its encryption key.
