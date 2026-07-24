---
title: "Templates & Vars"
weight: 55
description: "Instance templates, variable hierarchy, userdata templates, and args merge strategy."
---

# Args and Vars Merge Strategy

Args and Vars can be set at multiple levels and Nstance Server uses the same merge stragegy for both.

Merges work by replacing the higher priority layer over the lower priorities layer, except where both values are objects and then it merges the object fields and recurses down into each value of that object.

For example with Args:

_defaults.args:_
```jsonc
{
  "a": 1,
  "b": { "hello": "world", "always": "here" },
  "c": { "goodbye": "world" }
}
```

_templates.example.args:_
```jsonc
{
  "a": "2",
  "b": { "hello": "there", "location": "world" },
  "c": 3
}
```

_merged output:_
```jsonc
{
  "a": "2",
  "b": { "hello": "there", "always": "here", "location": "world" },
  "c": 3
}
```

This is a straightforward recursive Go function:

```go
func deepMerge(dst, src map[string]any) {
	for k, v := range src {
		if vMap, ok := v.(map[string]any); ok {
			if dMap, ok := dst[k].(map[string]any); ok {
				deepMerge(dMap, vMap)
				continue
			}
		}
		dst[k] = v
	}
}
```

## Image Resolution

Nstance supports automatic resolution of cloud provider images (e.g., latest Debian AMI) based on predefined criteria, eliminating the need to hardcode region-specific image IDs.

- Provider-specific filters to lookup latest AMIs/images
- Cached in SQLite, refreshed periodically (default 6 hours)
- Optional fallback image IDs if lookup fails

## Provider Support

Currently this feature is only useful for AWS.

Different cloud providers handle image references differently:

- **AWS (EC2)**: Uses region-specific AMI IDs that must be looked up. Nstance automatically queries EC2 DescribeImages API with filters and caching.
- **Google Cloud (Compute Engine)**: Uses image families (e.g., `projects/debian-cloud/global/images/family/debian-13`) that automatically resolve to latest. No lookup needed.
- **Oracle Cloud (OCI)**: Would benefit from lookup mechanism (similar to AWS), and may be added in the future.

## Configuration

Images are configured in the `images` block with provider-specific filters:

```jsonc
{
  "images": {
    "debian_13_arm64": {
      "provider": "aws",
      "filters": [
        {"name": "name", "values": ["debian-13-arm64-*"]},
        {"name": "owner-id", "values": ["136693071363"]}  // Debian
      ],
      "sort": "creation-date",
      "order": "desc",
      "fallback": "ami-026fccd88446aa0bf"  // Optional fallback if lookup fails
    }
  }
}
```

Note that image name/map key should be a valid Go template identifiers (alphanumeric + underscores, starting with letter/underscore).

**Configuration Fields**:

- `provider`: Cloud provider (`aws`) - must match server provider
- `filters`: Array of provider-specific filters (e.g., AWS filter name/values)
- `sort`: Field to sort results by (`creation-date`, `name`)
- `order`: Sort order (`asc`, `desc`)
- `fallback`: Optional fallback image ID if lookup fails

## Usage in Templates

Resolved images are available in userdata and args templates via `.Image` map:

```jsonc
{
  "templates": {
    "knc": {
      "args": {
        "ImageId": "{{ .Image.debian_13_arm64 }}"  // Resolved AMI ID
      }
    }
  }
}
```

Alternative syntax using `index` helper for kebab-case names:
```
{{ index .Image "debian-13-arm64" }}
```

## Resolution Process

1. **On Shard Leader Election**: Service starts and performs initial image resolution
2. **Lookup**: Queries provider API (e.g., EC2 DescribeImages) with configured filters
3. **Sort & Select**: Sorts results and selects first match (e.g., latest by creation date)
4. **Cache**: Stores resolved images in SQLite (`images` table)
5. **Periodic Refresh**: Re-resolves at configured interval (default 6 hours)

## Error Handling & Fallback

If image resolution fails:
1. **Try Cache**: Load previously resolved value from SQLite
2. **Use Fallback**: Use configured `fallback` image ID if specified
3. **Fail Provisioning**: If no cache and no fallback, instance provisioning fails with error log

Cache persists across server restarts and does not expire (persists until next successful lookup).

## Args

Args are passed to the provider VM creation SDK function call. Args are provider-specific and support different fields for AWS (`RunInstances`) and Google Cloud (`Instance`) configurations. For example on AWS, Args effectively becomes AWS `RunInstances` input.

Args are processed as a `text/template` after the merge strategy has applied, recursively interpolating string values in nested objects and arrays. Note that templating can only be used inside JSON string values, for example:

```jsonc
{
    "someKey": "{{ .Var.SOME_VALUE }}"
}
```

## AWS Args

AWS Args map directly to `RunInstances` input fields. You cannot set `InstanceType`, `SubnetId`, `UserData`, `MinCount`, or `MaxCount` (these are managed by Nstance). 

## Google Cloud Args

Google Cloud Args are applied to the `computepb.Instance` resource. The following fields are supported:

| Arg | Type | Description |
|-----|------|-------------|
| `SourceImage` | string | Boot disk image (e.g., `"projects/debian-cloud/global/images/family/debian-13"`) |
| `ServiceAccount` | string | Service account email. Sets `cloud-platform` scope automatically. |
| `ServiceAccounts` | array | Full service account config (alternative to `ServiceAccount` for custom scopes) |
| `Labels` | object | Additional Google Cloud labels merged with Nstance-managed labels |
| `NetworkTags` | array | Google Cloud network tags for firewall rule targeting (e.g., `["nstance-agent-us-central1-a"]`) |
| `Preemptible` | bool | Use preemptible VM scheduling |
| `ProvisioningModel` | string | VM provisioning model (e.g., `"SPOT"`) |
| `OnHostMaintenance` | string | Host maintenance behavior (e.g., `"TERMINATE"`) |
| `AutomaticRestart` | bool | Automatically restart on failure |
| `MinCpuPlatform` | string | Minimum CPU platform (e.g., `"Intel Cascade Lake"`) |
| `GuestAccelerators` | array | GPU accelerator configs |
| `ShieldedInstanceConfig` | object | Shielded VM settings |
| `ConfidentialInstanceConfig` | object | Confidential VM settings |
| `ResourcePolicies` | array | Resource policy URLs |

**Example:**

```jsonc
{
  "defaults": {
    "args": {
      "SourceImage": "projects/debian-cloud/global/images/family/debian-13",
      "ServiceAccount": "nstance-agent@my-project.iam.gserviceaccount.com"
    }
  },
  "groups": {
    "default": {
      "workers": {
        "template": "default",
        "size": 3,
        "args": {
          "NetworkTags": ["nstance-agent-us-central1-a"],
          "Labels": {
            "nstance-group": "workers"
          }
        }
      }
    }
  }
}
```

**Note:** Nstance automatically manages core Google Cloud labels (`nstance-managed`, `nstance-instance-id`, `nstance-cluster-id`, `nstance-shard`, `nstance-group`, `nstance-template`, `nstance-instance-kind`) and uses these labels (not network tags) for instance identification and filtering.

## Vars

Vars are set in a hierarchy, with order of precedence:

* (MachinePool): Group -> Template -> Global
* (Machine): Group -> Template -> Global

For example:

```jsonc
{
  "vars": {
    "EXAMPLE": "one",
  },
  "templates": {
    "default": {
      "vars": {
        "EXAMPLE": "two",
      },
    },
  },
  "groups": {
    "blue": {},
    "green": {
      "template": "default",
    },
    "red": {
      "template": "default",
      "vars": {
        "EXAMPLE": "three",
      },
    },
    "black": {
      "vars": {
        "EXAMPLE": "four",
      },
    },
  },
}
```

Given the above example config, the `.Var.EXAMPLE` value in userdata:

* for group `blue` is `one`.
* for group `green` is `two`.
* for group `red` is `three`.
* for group `black` is `four`.

## Userdata Templates

The `defaults` and `templates` block can specify a userdata configuration object:

```jsonc
{
  "templates": {
    "default": {
      "userdata": {
        "source": "inline",       // "inline" (default) or "url"
        "encoding": "plain",      // "plain" (default), "base64", "gzip", "base64+gzip"
        "content": "#!/bin/bash\necho 'Hello {{ .Instance.ID }}'" // or the URL
      }
    }
  }
}
```

**Fields:**

- `source`: Where the content comes from
  - `"inline"` (default): The `content` field contains the userdata directly
  - `"url"`: The `content` field contains a URL to fetch the userdata from

- `encoding`: How the content is encoded
  - `"plain"` (default): Plain text with escaped newlines
  - `"base64"`: Base64 encoded content
  - `"gzip"`: Gzip compressed content (only valid with `source: "url"`)
  - `"base64+gzip"`: Base64 encoded, gzip compressed content

- `content`: The userdata content or URL (required)

The default userdata template is only used if no userdata template is specified for the template.

The userdata template is processed as a Go `text/template`, where the template can reference:

- Merged `Vars` e.g. `{{ .Vars.MY_VAR }}` (see [Args & Vars Merge Strategy](#args-and-vars-merge-strategy))

- Server information such as:

  - `.Server.Shard` e.g. `us-west-2a-1`
  - `.Server.RegistrationAddr` i.e. the IP and port for agent registration (e.g. `10.0.0.1:8992`)
  - `.Server.AgentAddr` i.e. the IP and port for agent gRPC service (e.g. `10.0.0.1:8994`)

- Provider information such as:

  - `.Provider.Kind` e.g. `aws`
  - `.Provider.Region` e.g. `us-west-2`
  - `.Provider.Zone` e.g. `us-west-2a`

- Instance information (derived from Pool/MachinePool or Instance/Machine configuration, which can be derived from the instance template) such as:

  - `.Instance.Arch` e.g. `arm64`
  - `.Instance.Type` e.g. `t4g.medium`.

## Templated Files

In addition to certificates and secrets, the Nstance Server can generate and send templated files to agents. These files are dynamically generated using Go `text/template` with access to instance, cluster, and configuration data.

## Template Types

Templated files support three distinct template formats:

### kind: env

For environment variable files (`.env` format), use `kind: "env"` with a key-value template object:

```jsonc
"instance.env": {
  "kind": "env",
  "template": {
    "INSTANCE_ID": "{{ .Instance.ID }}",
    "ENVIRONMENT": "{{ .Vars.Environment }}",
    "K8S_NODE_LABELS": "{{ .Vars.KUBELET_NODE_LABELS }}",
    "CLUSTER_FQDN": "{{ .Cluster.FQDN }}"
  }
}
```

This generates standard environment file format:
```
INSTANCE_ID=knc0000000001r010000000000000
ENVIRONMENT=production
K8S_NODE_LABELS=controlplane
CLUSTER_FQDN=example-cluster.cluster.cool
```

### kind: json

For JSON configuration files, use `kind: "json"` with a template object where string values can contain templates:

```jsonc
"kubelet-config.json": {
  "kind": "json",
  "template": {
    "kind": "KubeletConfiguration",
    "apiVersion": "kubelet.config.k8s.io/v1beta1",
    "address": "{{ .Instance.IP4 }}",
    "port": 10250,
    "clusterDomain": "cluster.local",
    "nodeLabels": {
      "instance.example.com/id": "{{ .Instance.ID }}",
    }
  }
}
```

String values are processed as templates while preserving JSON structure, numbers, booleans, and arrays.

This generates a JSON document file format e.g.:

```json
{
  "kind": "KubeletConfiguration",
  "apiVersion": "kubelet.config.k8s.io/v1beta1",
  "address": "172.18.0.1",
  "port": 10250,
  "clusterDomain": "cluster.local",
  "nodeLabels": {
    "instance.example.com/id": "knc0000000001r010000000000000",
  }
}
```

### kind: string

For custom file formats or when you need full control over the output format:

```jsonc
"custom-script.sh": {
  "kind": "string",
  "template": "#!/bin/bash\necho \"Instance {{ .Instance.ID }} starting\"\nexport ENV={{ .Vars.Environment }}"
}
```

e.g.

```bash
#!/bin/bash
echo "Instance knc0000000001r010000000000000 starting"
export ENV=production
```

## Template Data

File and certificate templates have access to:

- **Instance**: `{{ .Instance.ID }}`, `{{ .Instance.Kind }}`, `{{ .Instance.Arch }}`, `{{ .Instance.Type }}`, `{{ .Instance.Hostname }}`, `{{ .Instance.FQDN }}`, `{{ .Instance.IP4 }}`, `{{ .Instance.IP6 }}`
- **Cluster**: `{{ .Cluster.ID }}`, `{{ .Cluster.CACert }}`
- **Server**: `{{ .Server.Shard }}`, `{{ .Server.RegistrationAddr }}`, `{{ .Server.AgentAddr }}`, `{{ .Server.OperatorAddr }}`
- **Provider**: `{{ .Provider.Kind }}`, `{{ .Provider.Region }}`, `{{ .Provider.Zone }}`
- **Variables**: `{{ .Vars.ENVIRONMENT }}` - All merged variables from global → template → group hierarchy
- **Images**: `{{ .Image.debian_13_arm64 }}` - Resolved image IDs from [Image Resolution](#image-resolution) configuration

Template data uses the same variable merging strategy as [userdata templates](#userdata-templates).
