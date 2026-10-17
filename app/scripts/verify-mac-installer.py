#!/usr/bin/env python3
"""Check the image's Finder layout and bundle without opening or installing it."""
import argparse
import json
import plistlib
import struct
import subprocess
import tempfile
from pathlib import Path

from ds_store import DSStore


def run(*args):
    return subprocess.run(args, check=True, stdout=subprocess.PIPE).stdout


def tiff_dimensions(path):
    data = path.read_bytes()
    endian = {b"II": "<", b"MM": ">"}[data[:2]]
    assert struct.unpack_from(endian + "H", data, 2)[0] == 42
    offset = struct.unpack_from(endian + "I", data, 4)[0]
    dimensions = set()
    visited = set()
    while offset:
        assert offset not in visited
        visited.add(offset)
        count = struct.unpack_from(endian + "H", data, offset)[0]
        size = {}
        for index in range(count):
            entry = offset + 2 + 12 * index
            tag, kind, length = struct.unpack_from(endian + "HHI", data, entry)
            if tag in (256, 257):
                assert length == 1 and kind in (3, 4)
                size[tag] = struct.unpack_from(endian + ("H" if kind == 3 else "I"), data, entry + 8)[0]
        dimensions.add((size[256], size[257]))
        offset = struct.unpack_from(endian + "I", data, offset + 2 + 12 * count)[0]
    return dimensions


def verify(image):
    app = Path(__file__).resolve().parents[1]
    with tempfile.TemporaryDirectory(prefix="dmg-verify-", dir=app / ".cache") as temporary:
        mount = Path(temporary) / "image"
        mount.mkdir()
        attachment = plistlib.loads(run(
            "/usr/bin/hdiutil", "attach", "-readonly", "-nobrowse", "-noautoopen",
            "-mountpoint", str(mount), "-plist", str(image),
        ))
        device = next(item["dev-entry"] for item in attachment["system-entities"] if item.get("mount-point"))
        try:
            assert sorted(p.name for p in mount.iterdir() if not p.name.startswith(".")) == ["Applications", "koto.app"]
            assert (mount / "Applications").is_symlink()
            assert (mount / "Applications").readlink() == Path("/Applications")
            with DSStore.open(str(mount / ".DS_Store"), "r") as store:
                metadata = {(entry.filename, entry.code): entry.value for entry in store}
            assert metadata[(".", b"icvl")] == b"icnv"
            window = metadata[(".", b"bwsp")]
            assert window["WindowBounds"] == "{{160, 180}, {660, 410}}"
            assert not any(window[key] for key in ["ShowToolbar", "ShowSidebar", "ShowStatusBar", "ShowPathbar", "ShowTabView"])
            icons = metadata[(".", b"icvp")]
            assert icons["iconSize"] == 96 and icons["textSize"] == 14
            assert icons["backgroundType"] == 2 and icons["backgroundImageAlias"]
            assert icons["arrangeBy"] == "none"
            assert icons["scrollPositionX"] == icons["scrollPositionY"] == 0
            assert metadata[("koto.app", b"Iloc")] == (186, 226)
            assert metadata[("Applications", b"Iloc")] == (474, 226)
            assert tiff_dimensions(mount / ".background.tiff") == {(660, 410), (1320, 820)}
            bundle = mount / "koto.app"
            run("/usr/bin/codesign", "--verify", "--deep", "--strict", str(bundle))
            assert not (bundle / "Contents/Helpers/koto").exists(), "CLI is distributed separately"
            assert (bundle / "Contents/Resources/legal/LICENSE").is_file()
            return {"image": image.name, "window": [660, 410], "icons": {"koto": [186, 226], "Applications": [474, 226]}, "background": "standard + Retina", "signature": "valid"}
        finally:
            run("/usr/bin/hdiutil", "detach", device)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("image", type=Path)
    arguments = parser.parse_args()
    print(json.dumps(verify(arguments.image.resolve()), indent=2))
