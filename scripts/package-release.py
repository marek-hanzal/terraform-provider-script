#!/usr/bin/env python3
"""Package the pinned filesystem mirror for a checkout-independent GH release."""

import argparse
import hashlib
import io
import json
from pathlib import Path
import re
import zipfile

PLATFORMS = (
    "darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64",
    "windows_amd64", "windows_arm64",
)


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    args = parser.parse_args()
    if not re.fullmatch(r"\d+\.\d+\.\d+", args.version):
        parser.error("version must be a stable major.minor.patch number")

    root = Path(__file__).resolve().parent.parent
    version = args.version
    mirror = root / "build/mirror/registry.terraform.io/marek-hanzal/script" / version
    expected = {}
    for line in (mirror / "SHA256SUMS").read_text().splitlines():
        digest, name = line.split()
        expected[name] = digest

    base = f"terraform-provider-script_{version}"
    release_url = f"https://github.com/marek-hanzal/terraform-provider-script/releases/download/v{version}"
    manifest = {
        "schema_version": 1,
        "provider": "registry.terraform.io/marek-hanzal/script",
        "version": version,
        "release_tag": f"v{version}",
        "platforms": {},
    }
    assets = {}
    for platform in PLATFORMS:
        binary = f"terraform-provider-script_v{version}"
        if platform.startswith("windows_"):
            binary += ".exe"
        relative = f"{platform}/{binary}"
        data = (mirror / relative).read_bytes()
        if sha256(data) != expected.get(relative):
            raise SystemExit(f"Mirror binary checksum mismatch: {relative}")
        archive = f"{base}_{platform}.zip"
        buffer = io.BytesIO()
        info = zipfile.ZipInfo(binary, date_time=(1980, 1, 1, 0, 0, 0))
        info.create_system = 3
        info.external_attr = 0o100755 << 16
        info.compress_type = zipfile.ZIP_DEFLATED
        with zipfile.ZipFile(buffer, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as package:
            package.writestr(info, data, compresslevel=9)
        assets[archive] = buffer.getvalue()
        manifest["platforms"][platform] = {
            "url": f"{release_url}/{archive}",
            "archive_sha256": sha256(assets[archive]),
            "binary_filename": binary,
            "binary_sha256": sha256(data),
        }

    assets[f"{base}_manifest.json"] = (json.dumps(manifest, indent=2, sort_keys=True) + "\n").encode()
    assets[f"{base}_BINARY_SHA256SUMS"] = (mirror / "SHA256SUMS").read_bytes()
    assets[f"{base}_SHA256SUMS"] = "".join(
        f"{sha256(data)}  {name}\n" for name, data in sorted(assets.items())
    ).encode()

    destination = root / "build/releases" / version
    # Validate every candidate before writing anything. Never replace a release
    # artifact with different bytes under an existing version.
    for name, data in assets.items():
        path = destination / name
        if path.exists() and path.read_bytes() != data:
            raise SystemExit(f"Refusing to replace different bytes under pinned version {version}: {name}")
    destination.mkdir(parents=True, exist_ok=True)
    for name, data in assets.items():
        path = destination / name
        if not path.exists():
            path.write_bytes(data)
        print(f"{sha256(data)}  {name}")


if __name__ == "__main__":
    main()
