---
title: "Certificates"
weight: 20
description: "TLS certificate issuance, renewal, and management for instances."
---


# Certificates

TLS certificate issuance, renewal, and management for instances.

## File Sending

Each instance template has a list of files the Nstance Server can expect to send. For any given file, if the `.kind = certificate` and `.key.from = agent` then Nstance Server will request Nstance Agent generate a corresponding keypair and publish the public key to Nstance Server.

Once Nstance Server has the public key, it will generate and send a certificate back to Nstance Server for writing to disk.

From there, any script/process on the VM itself can handle correctly distributing the certificate (this is typically set up in userdata when the server is provisioned).

This workflow is extremely fast, and secure because Nstance Agent has established a secure channel during its bootstrap.

## Example Configuration

Example Nstance Server configuration:

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

Supported certificate fields:

- `cn`: Common Name (supports templating)
- `organization`: Organization (O)
- `country`: Country (C)
- `province`: State/Province (ST)
- `locality`: Locality (L)
- `street`: Street Address
- `postalCode`: Postal Code
- `dns`: DNS SANs (supports templating)
- `ip`: IP SANs (supports templating)
- `ttl`: Validity period in hours
