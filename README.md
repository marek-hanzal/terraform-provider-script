# Terraform Script Provider

A standalone Terraform Plugin Framework provider that runs one prepared JavaScript
entrypoint through Node. It has no database client, migration history, directory
ordering, rollback, or automatic retry. The program owns idempotency and any
external data lifecycle. A changed artifact or explicit environment causes a new
invocation during apply.

The source is currently local-only. Version `0.1.0` installs as
`registry.terraform.io/marek-hanzal/script` through a filesystem mirror; this address
does not imply a registry or GitHub release. Keep a protected copy of this repository
until it has a remote. Consumers pin a clean source commit and binary digests.

```hcl
terraform {
  required_providers {
    script = { source = "marek-hanzal/script", version = "= 0.1.0" }
  }
}

provider "script" {} # optional node_path; otherwise resolve node on provider PATH

resource "script_run" "prepare" {
  script          = abspath("${path.module}/build/prepare.mjs")
  sha256          = filesha256("${path.module}/build/prepare.mjs")
  timeout_seconds = 300
  environment = {
    TARGET_URL = var.target_url
    API_TOKEN  = var.api_token
  }
}
```

`script` must be absolute. `sha256` is lowercase hexadecimal SHA-256. Optional
`working_directory` must be absolute and defaults to the script's directory.
`timeout_seconds` defaults to 300 and permits 1–3600 seconds. The optional
`environment` string map is the complete child environment; absent means empty.
No Terraform parent environment, cloud keys, HOME, PATH or NODE_OPTIONS are copied.
Windows may supply the operating system's required SystemRoot automatically.

Create and Update verify the file hash and invoke Node directly with an argv path,
without a shell. Keep the prepared artifact unchanged through apply. A hash
mismatch fails the invocation; rebuild and plan again. The provider waits for
Node and kills/reaps it on timeout or cancellation. It does not supervise programs
spawned by user JavaScript; entrypoints must finish their own work before exiting.
Output is discarded because arbitrary JS or driver errors may contain credentials.
Errors report failure without child output or environment values.

Read and plan never execute JavaScript or inspect external application state.
Delete only forgets the invocation. The computed `id` is the hash of the last
successfully executed artifact. Environment changes rerun even when that hash is
unchanged. Input environment values remain sensitive configuration in Terraform
state; protect state and saved plans. No output or credential is copied into a
computed field.

Success means Node exited zero, not that a provider verified application behavior.
Failure or interruption can leave external effects; a later apply can invoke the
program again. Write idempotent programs. Sequence independent invocations with
explicit Terraform `depends_on`; sequence cohesive work inside the JS entrypoint.

## Local development

```bash
mise trust
mise install
mise run test
mise run vet
mise run crossbuild
```

The pinned Go build uses `-mod=readonly`, `-trimpath`, no VCS stamping, no CGO,
and a fixed version. `build/mirror` contains macOS/Linux/Windows amd64/arm64
binaries. Point a task-specific Terraform CLI configuration at this mirror:

```hcl
provider_installation {
  filesystem_mirror {
    path    = "/absolute/path/terraform-provider-script/build/mirror"
    include = ["registry.terraform.io/marek-hanzal/script"]
  }
  direct {
    exclude = ["registry.terraform.io/marek-hanzal/script"]
  }
}
```

Set `TF_CLI_CONFIG_FILE` to that file, then initialize the consumer. Do not modify
global Terraform configuration. Do not replace binaries under an existing pinned
version. Tests use tiny local Node programs and isolated Terraform state/mirrors;
they need no database or cloud credentials.
