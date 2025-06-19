[![Build Status](https://travis-ci.org/kvz/json2hcl.svg?branch=master)](https://travis-ci.org/kvz/json2hcl)

# json2hcl (and hcl2json)

Convert JSON to HCL and HCL to JSON via STDIN / STDOUT with smart format detection.

## Features

- **Smart Format Detection**: Automatically detects whether to generate `.tf` (separate blocks) or `.tfvars` (nested structures) format based on file extension
- **Explicit Control**: Use flags to override automatic detection for precise output control
- **Bidirectional**: Convert JSON ↔ HCL in both directions
- **Block Conversion**: Intelligently converts JSON arrays to HCL blocks (variables, resources, providers, etc.)
- **Nested Structures**: Preserves complex nested data for `.tfvars` files
- **Expression Handling**: Properly handles Terraform interpolations and expressions

## Warning

We don't use json2hcl anymore ourselves, so we can't invest time into it. However, we're still welcoming PRs.

## Install

Check the [releases](https://github.com/kvz/json2hcl/releases) for the latest version.
Then it's just a matter of downloading the right one for you platform, and making the binary
executable. 

### Linux

Here's how it could look for 64 bits Linux, if you wanted `json2hcl` available globally inside
`/usr/local/bin`:

```bash
curl -SsL https://github.com/kvz/json2hcl/releases/download/v0.0.6/json2hcl_v0.0.6_linux_amd64 \
  | sudo tee /usr/local/bin/json2hcl > /dev/null && sudo chmod 755 /usr/local/bin/json2hcl && json2hcl -version
```

### OSX

Here's how it could look for 64 bits Darwin, if you wanted `json2hcl` available globally inside
`/usr/local/bin`:

```bash
curl -SsL https://github.com/kvz/json2hcl/releases/download/v0.0.6/json2hcl_v0.0.6_darwin_amd64 \
  | sudo tee /usr/local/bin/json2hcl > /dev/null && sudo chmod 755 /usr/local/bin/json2hcl && json2hcl -version
```

## Usage

### Basic Conversion

Convert JSON to HCL (defaults to Terraform format with separate blocks):

```bash
$ json2hcl < input.json
$ json2hcl < input.json > output.tf
```

### Automatic Format Detection

The tool automatically detects the target format based on the output file extension:

**For `.tf` files (Terraform format with separate blocks):**
```bash
$ json2hcl -output infrastructure.tf < infra.tf.json
```

**For `.tfvars` files (nested variable format):**
```bash
$ json2hcl -output variables.tfvars < vars.json
```

### Explicit Control Flags

Override automatic detection with explicit flags:

**Force block format (even for `.tfvars` files):**
```bash
$ json2hcl --treat-arrays-as-blocks < input.json
```

**Force nested format (even for `.tf` files):**
```bash
$ json2hcl --keep-arrays-nested < input.json
```

### Examples

#### Example 1: Converting Infrastructure JSON to Terraform

Input (`infra.tf.json`):
```json
{
  "variable": {
    "region": [{"type": "string"}],
    "instance_type": [{"type": "string"}]
  },
  "resource": {
    "aws_instance": {
      "web": [{
        "ami": "ami-12345",
        "instance_type": "${var.instance_type}"
      }]
    }
  }
}
```

Output with `json2hcl -output infra.tf`:
```hcl
variable "region" {
  type = string
}

variable "instance_type" {
  type = string
}

resource "aws_instance" "web" {
  ami           = "ami-12345"
  instance_type = var.instance_type
}
```

#### Example 2: Converting Variables JSON to .tfvars

Input (`variables.json`):
```json
{
  "vpc_config": {
    "cidr_block": "10.0.0.0/16",
    "enable_dns": true
  },
  "subnets": [
    {
      "name": "public-1",
      "cidr": "10.0.1.0/24"
    },
    {
      "name": "private-1", 
      "cidr": "10.0.2.0/24"
    }
  ]
}
```

Output with `json2hcl -output variables.tfvars`:
```hcl
vpc_config = {
  cidr_block = "10.0.0.0/16"
  enable_dns = true
}

subnets = [
  {
    name = "public-1"
    cidr = "10.0.1.0/24"
  },
  {
    name = "private-1"
    cidr = "10.0.2.0/24"
  }
]
```

## hcl2json (Reverse Conversion)

Convert HCL back to JSON using the `-reverse` flag:

```bash
$ json2hcl -reverse < infrastructure.tf > infrastructure.json
```

Example:
```bash
$ json2hcl -reverse < fixtures/infra.tf
{
  "variable": {
    "region": [
      {
        "type": "string"
      }
    ]
  },
  "resource": {
    "aws_instance": {
      "web": [
        {
          "ami": "ami-12345",
          "instance_type": "${var.instance_type}"
        }
      ]
    }
  }
}
```

## Command Line Options

```
Usage:
  -version
        Prints current app version
  -reverse
        Input HCL, output JSON
  -output string
        Output file path (used to determine file type for conversion)
  -treat-arrays-as-blocks
        Convert JSON arrays to separate HCL blocks (e.g., variables, resources)
  -keep-arrays-nested
        Keep JSON arrays as nested structures (e.g., for .tfvars format)
```

## Conversion Behavior

### Automatic Detection Priority

1. **Explicit flags**: `--treat-arrays-as-blocks` or `--keep-arrays-nested` override everything
2. **File extension**: `-output filename.tf` vs `-output filename.tfvars`
3. **Default**: Terraform format (separate blocks) for backward compatibility

### Block Types

The following JSON structures are converted to separate HCL blocks when using Terraform format:

- `variable` → `variable "name" { ... }`
- `resource` → `resource "type" "name" { ... }`
- `data` → `data "type" "name" { ... }`
- `provider` → `provider "name" { ... }`
- `output` → `output "name" { ... }`
- `locals` → `locals { ... }`
- `module` → `module "name" { ... }`
- `terraform` → `terraform { ... }`

### Nested Blocks

Within resources, the following are also converted to blocks:
- `attribute` → `attribute { ... }`
- `global_secondary_index` → `global_secondary_index { ... }`
- `local_secondary_index` → `local_secondary_index { ... }`

## Development

```bash
mkdir -p ~/go/src/github.com/kvz
cd ~/go/src/github.com/kvz
git clone git@github.com:kvz/json2hcl.git
cd json2hcl
go get
go test -v  # Run tests
```

## Why?

If you don't know HCL, read [Why HCL](https://github.com/hashicorp/hcl#why).

As for why json2hcl and hcl2json, we're building a tool called Frey that marries multiple underlying
tools. We'd like configuration previously written in YAML or TOML to now be in HCL now as well. 
It's easy enough to convert the mentioned formats to JSON, and strictly speaking HCL is already 
able to read JSON natively, so why the extra step?

We're doing this for readability and maintainability, we wanted to save 
our infra recipes as HCL directly in our repos, instead of only having machine readable intermediate 
JSON that we'd need to hack on. This saves time spotting problems, and makes the experience somewhat 
enjoyable even.

In the off-chance you too have machine-readable JSON and are interested in converting that
to the more human-being friendly HCL format, we thought we'd share this.

It's no rocket science, we're using already available HashiCorp libraries to support the conversion,
HashiCorp could have easily released their own tools around this, and perhaps they will, but 
so far, they haven't.

## Changelog

### Ideabox (Unplanned)

- [x] Give the README.md some love
- [ ] Support for more Terraform block types

### v0.1.0 (Unreleased)

- [x] Smart format detection based on file extension
- [x] `--treat-arrays-as-blocks` flag for explicit block conversion
- [x] `--keep-arrays-nested` flag for explicit nested format
- [x] Improved block conversion for variables, resources, providers, outputs
- [x] Better handling of nested blocks (attributes, indexes)
- [x] Expression and interpolation handling
- [x] Comprehensive test suite with HCL sorting

### v0.0.7 (Unreleased)

- [x] Tests

### v0.0.6 (2016-09-06)

- [x] Deprecate goxc in favor of native builds

### v0.0.5 (2016-09-06)

- [x] Add hcl2json via the `-reverse` flag 

### v0.0.4 (2016-09-05)

- [x] Error handling
- [x] Cross-compiling and shipping releases

## Contributors

- [Marius Kleidl](https://github.com/Acconut)
- [Kevin van Zonneveld](https://github.com/kvz)
- [MaxVisser](https://github.com/maxvisser)
