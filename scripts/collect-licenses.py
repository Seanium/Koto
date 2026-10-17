#!/usr/bin/env python3
"""Refresh license copies from pinned, already-installed project dependencies."""
from __future__ import annotations

import hashlib
import json
import re
import shutil
import tempfile
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
TARGET = ROOT / "licenses"
SKIP = {"node_modules", ".git", "testdata", "tests", "test"}


def notice_files(directory: Path):
    for path in sorted(directory.rglob("*")):
        if path.is_symlink() or not path.is_file():
            continue
        relative = path.relative_to(directory)
        if any(part in SKIP for part in relative.parts[:-1]):
            continue
        if re.match(r"^(LICENSE|LICENCE|COPYING|NOTICE)(?:[.\-_]|$)", path.name, re.I):
            yield path


def collect(target: Path):
    target.mkdir(parents=True, exist_ok=True)
    packages = []
    # The linked Go runtime is redistributed with every executable.
    go_root = ROOT / "app/.tools/go"
    runtime_license = go_root / "LICENSE"
    if not runtime_license.is_file():
        raise SystemExit("Missing project-local Go runtime license")
    runtime_output = target / "go/go"
    runtime_output.mkdir(parents=True)
    shutil.copyfile(runtime_license, runtime_output / "LICENSE")
    go_version = (go_root / "VERSION").read_text().splitlines()[0]
    packages.append({"ecosystem": "go", "name": "Go runtime", "version": go_version, "directory": "go/go"})
    go_mod = (ROOT / "app/go.mod").read_text()
    for module, version in re.findall(r"^\s*([^\s()]+) (v[^\s]+)", go_mod, re.M):
        escaped = re.sub(r"[A-Z]", lambda match: "!" + match[0].lower(), module)
        source = ROOT / "app/.cache/gomod" / f"{escaped}@{version}"
        if not source.is_dir():
            raise SystemExit(f"Missing pinned module: {module}@{version}")
        output = target / "go" / f"{module.replace('/', '_')}@{version}"
        files = list(notice_files(source))
        if not files:
            raise SystemExit(f"No license found for {module}@{version}")
        for path in files:
            destination = output / path.relative_to(source)
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(path, destination)
        packages.append({"ecosystem": "go", "name": module, "version": version, "directory": output.relative_to(target).as_posix()})

    lock = json.loads((ROOT / "app/frontend/package-lock.json").read_text())
    for package_path, metadata in sorted(lock["packages"].items()):
        if not package_path:
            continue
        source = ROOT / "app/frontend" / package_path
        if not source.is_dir():
            continue  # Optional packages for a different host are not in this build.
        package = json.loads((source / "package.json").read_text())
        if package["version"] != metadata["version"]:
            raise SystemExit(f"Installed version differs from lockfile: {package['name']}")
        output = target / "npm" / f"{package['name'].replace('/', '_')}@{package['version']}"
        files = list(notice_files(source))
        if not files and package["name"] == "@wailsio/runtime":
            wails = re.search(r"github.com/wailsapp/wails/v3 (v[^\s]+)", go_mod)[1]
            files = [ROOT / "app/.cache/gomod" / f"github.com/wailsapp/wails/v3@{wails}" / "LICENSE"]
        if not files and package["name"].startswith("@rolldown/binding-"):
            parent = ROOT / "app/frontend/node_modules/rolldown"
            parent_metadata = json.loads((parent / "package.json").read_text())
            if parent_metadata["version"] != package["version"]:
                raise SystemExit("Rolldown binding and parent versions differ")
            files = list(notice_files(parent))
        if not files:
            raise SystemExit(f"No license found for installed package: {package['name']}")
        for path in files:
            relative = path.relative_to(source) if path.is_relative_to(source) else Path(path.name)
            destination = output / relative
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(path, destination)
        packages.append({"ecosystem": "npm", "name": package["name"], "version": package["version"], "directory": output.relative_to(target).as_posix()})

    # Public upstream license texts for the included identification marks.
    for name, url in {
        "lobe-icons-LICENSE": "https://raw.githubusercontent.com/lobehub/lobe-icons/master/LICENSE",
        "magpie-LICENSE": "https://raw.githubusercontent.com/yetone/magpie/main/LICENSE",
        "octicons-LICENSE": "https://raw.githubusercontent.com/primer/octicons/main/LICENSE",
    }.items():
        destination = target / "assets" / name
        destination.parent.mkdir(parents=True, exist_ok=True)
        with urllib.request.urlopen(url, timeout=30) as response:
            contents = response.read()
        if not contents.startswith(b"MIT License"):
            raise SystemExit(f"Unexpected license response for {name}")
        destination.write_bytes(contents)

    hashes = {path.relative_to(target).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest() for path in sorted(target.rglob("*")) if path.is_file() and path.name != "INDEX.json"}
    (target / "INDEX.json").write_text(json.dumps({"packages": packages, "files": hashes}, indent=2, ensure_ascii=False) + "\n")
    print(f"Preserved {len(hashes)} license files for {len(packages)} installed packages plus included marks.")


def main():
    if TARGET.is_symlink() or (TARGET.exists() and any(TARGET.iterdir()) and not (TARGET / "INDEX.json").is_file()):
        raise SystemExit("Refusing to replace an unindexed license directory")
    cache = ROOT / "app/.cache"
    cache.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".license-stage-", dir=cache) as temporary:
        stage = Path(temporary)
        collect(stage)
        if TARGET.exists():
            shutil.rmtree(TARGET)
        stage.rename(TARGET)


if __name__ == "__main__":
    main()
