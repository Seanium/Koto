#!/usr/bin/env python3
"""Build Finder's installation image without loose artwork in its root."""
import argparse
from pathlib import Path

from dmgbuild import build_dmg
from ds_store import DSStore
from mac_alias import Alias


def build(application, output, version):
    mount = None
    background_set = False

    def callback(event):
        nonlocal mount, background_set
        if event.get("type") == "command::finished" and event.get("command") == "hdiutil::attach" and event.get("ret") == 0:
            mount = next(Path(item["mount-point"]) for item in event["output"]["system-entities"] if item.get("mount-point"))
        if event.get("type") == "operation::finished" and event.get("operation") == "dsstore::create":
            if mount is None:
                raise RuntimeError("Installation image mount was not reported")
            artwork = mount / application.name / "Contents/Resources/installer.tiff"
            if not artwork.is_file():
                raise RuntimeError("Signed App does not contain the installer background")
            with DSStore.open(str(mount / ".DS_Store"), "r+") as store:
                settings = store["."]["icvp"]
                settings["backgroundType"] = 2
                settings["backgroundImageAlias"] = Alias.for_file(str(artwork)).to_bytes()
                store["."]["icvp"] = settings
            background_set = True

    build_dmg(str(output), f"koto {version}",
              settings_file=str(Path(__file__).with_name("dmg-settings.py")),
              defines={"app": str(application)}, callback=callback)
    if not background_set:
        raise RuntimeError("Installation image background was not configured")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("application", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("version")
    args = parser.parse_args()
    build(args.application.resolve(), args.output.resolve(), args.version)
