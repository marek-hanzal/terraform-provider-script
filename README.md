# Terraform Script Provider

A small Terraform Plugin Framework provider for prepared JavaScript entrypoints
executed directly with Node. It provides two separate lifecycle contracts:

- `data "script_output"`: run a local builder during plan and publish public JSON.
- `resource "script_run"`: record a successful apply-time invocation, such as a
  database migration. Read/Delete never execute or undo external data.

The source is hosted at
[github.com/marek-hanzal/terraform-provider-script](https://github.com/marek-hanzal/terraform-provider-script).
Version `0.2.0` is distributed through [GitHub Releases](https://github.com/marek-hanzal/terraform-provider-script/releases/tag/v0.2.0)
and installs as `registry.terraform.io/marek-hanzal/script` through a filesystem
mirror. There is no Terraform Registry release. Consumers can download a pinned
release package without this checkout or Go; runtime requires Terraform and Node.

The provider has no database client, migration history, build cache, input-tree
watcher, deployment policy, rollback, or automatic retry. Consumers own their
build and deployment contracts.

## Install from GitHub Releases

Release assets contain one standard provider ZIP per platform, an archive checksum
manifest (`terraform-provider-script_0.2.0_SHA256SUMS`), a raw binary checksum
manifest (`terraform-provider-script_0.2.0_BINARY_SHA256SUMS`), and a
[machine-readable manifest](https://github.com/marek-hanzal/terraform-provider-script/releases/download/v0.2.0/terraform-provider-script_0.2.0_manifest.json)
with fixed download URLs and both hashes for every platform. Pin the expected hash
in the consumer rather than trusting a freshly downloaded checksum alone.

For example, on macOS arm64, download directly into Terraform's
[packed filesystem mirror layout](https://developer.hashicorp.com/terraform/cli/config/config-file#explicit-installation-method-configuration):

```bash
set -eu
provider_mirror="$PWD/.terraform-provider-mirror"
package=terraform-provider-script_0.2.0_darwin_arm64.zip
expected_sha256=7dc04ccf52ab73bab3e5d9f27216e71e3bc8fc8d0639349440cc0ed88751cae9
provider_directory="$provider_mirror/registry.terraform.io/marek-hanzal/script"
mkdir -p "$provider_directory"
curl --fail --location --output "$provider_directory/$package.tmp" \
  "https://github.com/marek-hanzal/terraform-provider-script/releases/download/v0.2.0/$package"
printf '%s  %s\n' "$expected_sha256" "$provider_directory/$package.tmp" | shasum -a 256 -c -
mv "$provider_directory/$package.tmp" "$provider_directory/$package"
```

Configure a task-specific `TF_CLI_CONFIG_FILE` with an absolute path to
`.terraform-provider-mirror`:

```hcl
provider_installation {
  filesystem_mirror {
    path    = "/absolute/consumer/.terraform-provider-mirror"
    include = ["registry.terraform.io/marek-hanzal/script"]
  }
  direct {
    exclude = ["registry.terraform.io/marek-hanzal/script"]
  }
}
```

Then `terraform init` installs the ZIP directly. No provider source checkout,
local compilation, GitHub credentials, or global Terraform configuration is
required. Select the matching archive and pinned hash for Linux or Windows from
[the release digest table](docs/0.2.0.md#release-archives). Consumer bootstrap
orchestration belongs in its deploy command.

## Plan-time local builds

```hcl
terraform {
  required_providers {
    script = { source = "marek-hanzal/script", version = "= 0.2.0" }
  }
}

provider "script" {
  node_path = var.node_path # absolute path to the selected Node executable
}

data "script_output" "build" {
  script            = abspath("${path.module}/scripts/build.mjs")
  sha256            = filesha256("${path.module}/scripts/build.mjs")
  working_directory = abspath(path.module)
  timeout_seconds   = 300
  environment = {
    PATH = var.build_toolchain_path
    # Explicit build-only values; do not pass deployment credentials.
  }
  arguments = ["--mode", "production"]
  stdin = jsonencode({
    source = abspath("${path.module}/src/input.txt")
    output = abspath("${path.module}/build/bundle.txt")
  })
}

locals {
  build = jsondecode(data.script_output.build.stdout)
}

# Pass local.build.assets into downstream resources' for_each and use their real
# artifact hashes as change inputs. Do not use the builder script hash, source
# timestamps, or a blanket depends_on as a deployment trigger.
```

`script`, `sha256`, `working_directory`, and `environment` are required.
The paths must be absolute; `sha256` must be 64 lowercase hexadecimal characters.
`environment = {}` means an empty explicit environment. `timeout_seconds` is
optional, defaults to 300, and accepts 1–3600. Optional `arguments` is a list of
non-null strings without NUL, passed literally after the entrypoint path, without
a shell. Optional `stdin` defaults to empty and is closed after its contents have
been sent. Arguments are public configuration; pass sensitive inputs via
`environment` or `stdin`, which are marked sensitive.

Known inputs let Terraform read this data source during a normal plan. It can run
again on every new plan; it does not cache builds. Unknown inputs or dependencies
on changing resources can defer the read until apply. This follows Terraform's
[data source lifecycle](https://developer.hashicorp.com/terraform/language/data-sources#data-source-behavior).
Keep build inputs known during plan, particularly when using result keys in
`for_each`. Avoid dependencies on resources that change on every deployment.

Applying a saved plan whose data source was read during plan consumes that stored
result without rerunning the builder. Preserve the declared local build outputs
alongside the saved plan until apply finishes, including when plan/apply run in
separate jobs. If outputs are deleted, create a fresh plan to regenerate them;
applying an old plan does not repair missing artifacts. A consumer can expose a
single command such as `argc deploy dev` that builds through plan and then applies
that plan. Command orchestration belongs to the consumer.

### Public JSON and diagnostic output

Success requires exit code 0 and exactly one valid UTF-8 JSON value on stdout,
limited to 16 MiB. `stdout` is an **explicitly published, non-sensitive data
contract**. It is stored in state and saved plans and may appear in Terraform
output. It must never contain secrets, even when environment or stdin is sensitive.
Use `jsondecode` to consume it; public JSON keys can be used in `for_each` without
`nonsensitive()`.

The provider does not define a manifest schema. For example, a builder can emit:

```json
{"assets":{"bundle":{"path":"/absolute/build/bundle.txt","sha256":"<actual artifact SHA-256>"}}}
```

Hash the **actual generated files** after a successful build. Return deterministic
paths and data, and omit timestamps, random IDs, and irrelevant source metadata.
If the consumer uses only artifact paths/hashes as deployment inputs, identical
outputs do not trigger deployment, remote writes, or purge, even after source
changes. The provider does not enforce downstream providers' deployment behavior.

This minimal builder illustrates the stdout contract (a real consumer supplies
its own compiler):

```javascript
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { dirname } from 'node:path';
import { createHash } from 'node:crypto';

const { source, output } = JSON.parse(readFileSync(0, 'utf8'));
mkdirSync(dirname(output), { recursive: true });
writeFileSync(output, readFileSync(source));
const sha256 = createHash('sha256').update(readFileSync(output)).digest('hex');
process.stdout.write(JSON.stringify({ assets: { bundle: { path: output, sha256 } } }));
```

Put diagnostic logs on stderr. They are discarded, including on failure; arbitrary
child stdout/stderr and environment values are never included in provider failure
diagnostics. Invalid JSON is rejected without quoting it. A compiler's progress
output must be redirected to stderr or discarded by the entrypoint, leaving stdout
for JSON alone. For diagnosis, run the program locally in a controlled environment.
Terraform itself can display configuration expressions and public JSON; protect
state, plans, and Terraform diagnostic logs.

### Execution boundary

The provider checks the entrypoint file hash immediately before starting Node.
Only that file is pinned; imported modules, source inputs, tools, and generated
outputs are the consumer's responsibility. Keep the local checkout stable during
plan and build. This is integrity checking for a trusted local build, not a
security sandbox or an atomic snapshot of the entire source tree.

No Terraform parent environment, cloud keys, HOME, PATH, or NODE_OPTIONS are copied
to the child. The provider's optional `node_path` resolves Node on the provider
PATH if omitted; subprocess tools still require an explicit child PATH or absolute
paths. Windows may add the OS-required SystemRoot. OS/runtime-generated metadata
can exist independently of parent environment inheritance.

Timeout and cancellation terminate Node and supervised subprocesses; remaining
subprocesses are also terminated after the entrypoint exits. Unix uses a dedicated
process group; Windows starts Node suspended, assigns a kill-on-close Job Object,
then resumes it. Builders must await their work and must not detach children into
new sessions/process groups or use services outside the supervised process tree.
A bounded pipe wait prevents descendants retaining stdio from hanging the provider.
This does not protect against forcible provider/OS termination on Unix.

The data source may create only **consumer-declared, disposable local outputs**.
Declare those paths in the consumer's build contract, for example stdin, and keep
them separate from source and durable data. Do not perform cloud writes, deploy,
purge, migrations, or other durable actions during plan. The provider does not
restrict filesystem/network access; use a trusted entrypoint and give it only its
explicit build toolchain environment. It cannot enforce this policy by inspecting
JavaScript. State hashes for deployment must come from the result; `sha256` pins
the entrypoint and is not the deployment trigger.

## Apply-time migrations and other invocations

```hcl
resource "script_run" "migration" {
  script          = abspath("${path.module}/build/migrate.mjs")
  sha256          = filesha256("${path.module}/build/migrate.mjs")
  timeout_seconds = 300
  environment = {
    DATABASE_URL = var.database_url
  }
}
```

`script_run` keeps its `0.1.0` lifecycle: Create/Update check the hash and invoke
Node; Read/plan never invoke it or inspect external application state. Delete only
forgets the invocation. Output is discarded, and success means exit code 0.
Its computed `id` is the hash of the last successfully executed entrypoint.
Environment changes invoke it again even when that hash is unchanged. Optional
`working_directory` defaults to the script's directory; optional `environment`
defaults to empty. Timeout defaults to 300 seconds, with the same execution boundary
as the data source. This resource has no new arguments/stdin/output fields.

Keep the prepared entrypoint unchanged between plan and apply. A hash mismatch
fails before invocation; rebuild and create a fresh plan. Programs own idempotency
and external data lifecycle. Failure or interruption may leave external effects;
a later apply may invoke the program again. There is no rollback or automatic
retry. Sequence independent invocations with explicit Terraform `depends_on`;
sequence cohesive work inside the entrypoint. Sensitive configuration is still
stored in Terraform state and saved plans.

## Local development and filesystem mirror

```bash
mise trust
mise install
mise run test
mise run vet
mise run crossbuild
mise run release
```

`mise.toml` pins Go 1.27.2, Terraform 1.16.5, and Node 24.19.0. Tests explicitly
resolve the pinned Node with `mise which node`. The Go build uses `-mod=readonly`,
`-trimpath`, no VCS stamping, no CGO, a fixed version, and an empty build ID.
`build/mirror/registry.terraform.io/marek-hanzal/script/0.2.0` contains
macOS/Linux/Windows amd64/arm64 binaries and `SHA256SUMS` with raw binary digests.
Crossbuild refuses to replace different bytes already stored under a pinned
version; bump `VERSION` for changed binaries. Previous version directories remain
untouched. Identical builds can be repeated without replacing existing binaries.

Point a task-specific Terraform CLI configuration at this mirror:

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
global Terraform configuration. Raw binary SHA-256 values in `SHA256SUMS` are not
Terraform's `h1:` package-directory checksums; let Terraform initialize/update its
lockfile against the selected mirror.

Tests use tiny local Node programs and real isolated Terraform state/mirrors.
They cover same-output plans, restored deleted outputs, changed artifact hashes,
saved-plan apply without rebuilding, public result keys, arguments/stdin, hash
mismatch, invalid output, redacted failure, environment isolation, timeout and
cancellation with descendants, and the existing migration lifecycle. Terraform
runs without cloud credentials or global CLI configuration. Cross-compilation
checks six build targets; execution testing on one host does not verify the other
operating systems or any cloud deployment.

`mise run release` packages the mirror with Python 3's standard library into
`build/releases/0.2.0`. ZIPs use fixed timestamps and executable file permissions;
packaging verifies the raw binary digests and refuses to replace different bytes
under an existing release version. These files are ready to upload as GitHub
release assets. No signing key or Terraform Registry registration is configured.

To run the real Terraform suite against an already cross-built native binary:

```bash
SCRIPT_TEST_MIRROR="$(pwd)/build/mirror" mise exec -- go test -race ./internal/provider -run '^TestTerraform' -count=1 -v
```

See [0.2.0 validation and binary digests](docs/0.2.0.md) for this build's local
verification evidence and remaining platform/integration limits.
