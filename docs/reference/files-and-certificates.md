---
title: "Files & Certificates"
weight: 15
description: "How Nstance Server generates and sends files & certificates to Nstance Agent"
---

# Files & Certificates

Key generation requests, certificate generation, and file sending.

## Certificate Generation Workflow

The Nstance Server drives key generation requirements based on the instance template configuration.

When the Agent connects to the Agent gRPC service, it immediately opens a unidirectional stream to receive key generation requests from the Server. The Server analyzes the instance template to determine which certificates will be needed and sends the corresponding key names that should be generated.

The Agent generates the requested keypairs asynchronously and idempotently, then submits the public keys to the Server. The Server generates certificates during health report processing and sends them back to the Agent via the file stream.

Health reports include file status information (last write timestamp or error), allowing the Server to confirm successful file writes and retry sending certificates if needed. The Server can also send additional key generation requests (triggered during health report processing) if keys are still missing.

## Agent File Receiver

A configurable list of files can be received by the Agent from the Server.

This enables asynchronous certificate issuance and renewal.

**Allowed Extensions**: `.crt`, `.key`, `.pub`, `.json`, `.env`

**Validation**:
- `.crt`: PEM-encoded X.509 certificate
- `.key`: PEM-encoded private key
- `.pub`: PEM-encoded public key
- `.json`: Valid JSON format
- `.env`: Standard key-value .env file

The agent accepts any filename with a valid extension from the server.

## Public Key Submission and Storage

Each instance template can specify a map of filenames (without extension) to a key from the certificate block.

When public keys are sent from an Nstance Agent to a Nstance Server, they are stored in object storage and cached in the SQLite database. The SubmitPublicKeys request is only acknowledged after all public keys have been successfully written to the database.

## Certificate Generation During Health Reports

Certificate generation occurs asynchronously during health report processing:

1. **Health Report Analysis**: When a health report is received, the server examines the file status map for missing files or files with errors
2. **Key Mapping**: For each missing/error file, check if it maps to a public key (same filename without extension) that was previously submitted
3. **Template Lookup**: Verify that a certificate template exists in the server configuration for the mapped certificate name
4. **Batch Generation**: Generate certificates in batch for all eligible files to improve efficiency
5. **Serial Recording**: Record certificate serials and write them to a single object storage file per batch to reduce costs
6. **File Queuing**: Queue generated certificates for delivery to the agent via the ReceiveFiles stream

For example, if Nstance Agent sends a public key with name `kubelet.server` and later reports that `kubelet.server.crt` is missing or has an error, the server will generate a certificate using the configuration mapping and deliver it to the agent.

## Example Configuration

```jsonc
{
  "certificates": {
    // Optional: Custom CA template
    "ca": {
      "kind": "server",
      "cn": "My Custom CA - {{ .Vars.ClusterSlug }}",
      "organization": ["My Company Infrastructure"],
      "country": ["US"],
      "province": ["California"],
      "locality": ["San Francisco"],
      "ttl": 87600 // 10 years
    },
    "kubelet-server-csr": {
      "kind": "server",
      "cn": "kubelet",
      "country": ["US"], // Optional
      "province": ["California"], // Optional
    },
  },
  "templates": {
    "knc": {
      "files": {
        "kubelet.server.crt": {
          "kind": "certificate",
          "template": "kubelet-server-csr",
          "key": {
            "from": "agent",
            "name": "kubelet.server.pub",
          },
        },
      },
    }
  }
}
```

## Security Properties

This process has two important security properties:

1. Which certificates can be generated (and with what properties) is restricted by the Nstance Server config.

2. Because key.from = `agent`, the private key for the generated certificate never leaves the instance the Nstance Agent is running on.
