#!/usr/bin/env python3
"""Read-only release checks for the current repository; never copies or publishes files."""
from __future__ import annotations

import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path
from urllib.parse import unquote

ROOT = Path(__file__).resolve().parents[1]

# Every tracked or unignored candidate must belong to a reviewed source tree.
# The current worktree is the release source; there is no parallel export tree.
EXACT = {
    "app/internal/legal/notices.txt.gz",
    ".gitignore", ".gitattributes", "README.md", "CONTRIBUTING.md", "LICENSE", "THIRD_PARTY_NOTICES.md",
    "app/README.md", "scripts/README.md", "scripts/check-source.py", "scripts/collect-licenses.py",
    "app/ASSETS.md", "app/Taskfile.yml", "app/VERSION",
    "app/go.mod", "app/go.sum", "app/env.sh",
    "app/frontend/index.html", "app/frontend/package.json",
    "app/frontend/package-lock.json", "app/frontend/tsconfig.json",
    "app/frontend/vite.config.ts", ".github/workflows/pages.yml", ".github/workflows/ci.yml",
    "app/scripts/dmg-requirements.txt", "app/scripts/render-installer-emoji.swift", "app/scripts/generate-icons.cjs",
    "site/install.sh", "site/install.ps1",
}
TREES = {
    "docs": {".md"},
    "licenses": None,
    "app/cmd": {".go"},
    "app/internal": {".go"},
    "app/frontend/src": {".ts", ".tsx", ".css", ".json", ".svg", ".png", ".webp"},
    "app/frontend/tests": {".ts"},
    "app/frontend/bindings": {".ts"},
    "app/brand": {".svg", ".png", ".json", ".icns", ".ico", ".md"},
    "app/build": {".yml", ".yaml", ".plist", ".png", ".icns", ".ico", ".car", ".sh", ".json", ".xml", ".nsi", ".nsh", ".manifest", ""},
    "app/scripts": {".sh", ".ps1", ".cmd", ".py", ".mjs", ".cjs"},
    "site": {".html", ".css", ".js", ".mjs", ".svg", ".png", ".webp", ".md"},
}
FIXTURES = {
    "app/internal/connectors/cursor/testdata/native-cli-2026.10.json",
    "app/internal/connectors/cursor/testdata/native-3.25.json",
    "app/internal/connectors/workbuddy/testdata/schema-5.8.sql",
    "app/internal/connectors/workbuddy/testdata/project-key-oracle.json",
    "app/internal/connectors/zcode/testdata/schema-0.16.9.sql",
    "app/internal/core/testdata/ev1.json",
    "app/internal/koto/testdata/cc_session.jsonl",
    "app/internal/koto/testdata/cc_session_plain.jsonl",
    "app/internal/koto/testdata/codex_rollout.jsonl",
    "app/internal/koto/testdata/pi_session.jsonl",
    "app/internal/koto/testdata/dsh-verify.mjs",
}
SITE_CAPTURES = {
    f"site/assets/screenshots/{locale}-{theme}-{view}.png"
    for locale in ("en", "zh", "zh-TW", "ja", "de")
    for theme in ("light", "dark")
    for view in ("copy", "tree", "diff")
}
FORBIDDEN_PARTS = {".git", ".cache", ".sandbox", ".tools", ".task", "node_modules", "screenshots", "bin", "dist", "research"}
FORBIDDEN_NAMES = {".DS_Store", ".env"}
SECRET_PATTERNS = {
    "private key": re.compile(rb"-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----"),
    "GitHub token": re.compile(rb"(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{50,})"),
    "provider token": re.compile(rb"\bsk-(?:proj-|ant-)?[A-Za-z0-9_-]{24,}\b"),
    "AWS access key": re.compile(rb"\b(?:AKIA|ASIA)[A-Z0-9]{16}\b"),
    "credential URL": re.compile(rb"https?://[^/\s:@]+:[^/\s@]+@"),
    "literal credential": re.compile(rb'''(?im)["']?(?:api[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret|password)["']?\s*[:=]\s*["'](?!demo|test|example|placeholder|mock|fake)[A-Za-z0-9_./+=-]{12,}["']'''),
}


def allowed(relative: str) -> bool:
    path = Path(relative)
    if relative in EXACT or relative in FIXTURES:
        return True
    if path.parent == Path("app") and path.suffix == ".go":
        return True
    for tree, suffixes in TREES.items():
        if path.is_relative_to(tree) and (suffixes is None or path.suffix in suffixes):
            return True
    return False


def candidates():
    result = subprocess.run(
        ["git", "--no-optional-locks", "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
        cwd=ROOT, capture_output=True, check=True,
    )
    return sorted(set(value.decode("utf-8") for value in result.stdout.split(b"\0") if value))


def check():
    problems = []
    entries = []
    selected = set()
    candidate_paths = candidates()
    candidate_set = set(candidate_paths)
    username = Path.home().name.encode()
    username_pattern = re.compile(rb"(?i)(?<![A-Za-z0-9])" + re.escape(username) + rb"(?![A-Za-z0-9])")
    personal_path = str(Path.home()).encode()

    def fail(relative, reason):
        problems.append({"file": relative, "reason": reason})

    for relative in candidate_paths:
        path = ROOT / relative
        if not path.exists() and not path.is_symlink():
            continue  # A tracked deletion is not part of the release worktree.
        if path.is_symlink() or any(parent.is_symlink() for parent in path.parents if parent != ROOT.parent):
            fail(relative, "symlink in candidate source")
            continue
        if not path.is_file():
            fail(relative, "directory, submodule, or special file in candidate source")
            continue
        selected.add(relative)
        if not allowed(relative):
            fail(relative, "path is outside the reviewed source allowlist")
        if (any(part in FORBIDDEN_PARTS for part in Path(relative).parts) and relative not in SITE_CAPTURES) or path.name in FORBIDDEN_NAMES:
            fail(relative, "runtime data or forbidden path")
        contents = path.read_bytes()
        if personal_path in contents or username_pattern.search(contents):
            fail(relative, "local home path or account name")
        for reason, pattern in SECRET_PATTERNS.items():
            if pattern.search(contents):
                fail(relative, reason)
        if path.suffix in {".jsonl", ".zstd", ".sqlite", ".sqlite3", ".db", ".log"} and relative not in FIXTURES:
            fail(relative, "unapproved session or runtime data")
        if path.suffix == ".md":
            # Code examples may contain illustrative URLs or path placeholders.
            markdown = re.sub(r"```.*?```", "", contents.decode("utf-8"), flags=re.S)
            for target in re.findall(r"!?\[[^\]]*\]\(([^)]+)\)", markdown):
                target = target.split("#", 1)[0].split("?", 1)[0].strip("<>")
                if not target or re.match(r"[a-z][a-z0-9+.-]*:", target, re.I):
                    continue
                linked = (path.parent / unquote(target)).resolve()
                if not linked.is_relative_to(ROOT) or not linked.exists():
                    fail(relative, "broken or external local documentation link: " + target)
                elif linked.is_file() and linked.relative_to(ROOT).as_posix() not in candidate_set:
                    fail(relative, "documentation links to an ignored local file: " + target)
        entries.append({"path": relative, "bytes": len(contents), "sha256": hashlib.sha256(contents).hexdigest()})

    required = set(EXACT | FIXTURES | SITE_CAPTURES)
    for locale in ("zh", "zh-TW", "ja", "de"):
        required.update(f"docs/{locale}/{name}.md" for name in ("README", "usage", "compatibility", "development", "ci", "contributing"))
    required.update(f"docs/{name}.md" for name in ("usage", "compatibility", "development", "ci"))
    for missing in sorted(required - selected):
        fail(missing, "required source or translation is missing/ignored")

    index = ROOT / "licenses/INDEX.json"
    if index.is_file():
        hashes = json.loads(index.read_text())["files"]
        actual = {Path(relative).relative_to("licenses").as_posix() for relative in selected if Path(relative).is_relative_to("licenses") and relative != "licenses/INDEX.json"}
        for relative in sorted(set(hashes) | actual):
            path = ROOT / "licenses" / relative
            if relative not in hashes or relative not in actual or not path.is_file() or hashlib.sha256(path.read_bytes()).hexdigest() != hashes[relative]:
                fail("licenses/" + relative, "license differs from the recorded index")
    else:
        fail("licenses/INDEX.json", "license index is missing")
    return {
        "passed": not problems,
        "files_checked": len(entries),
        "bytes_checked": sum(entry["bytes"] for entry in entries),
        "problems": problems,
        "files": entries,
        "limits": "Checks current tracked and unignored worktree files, not Git history. Pattern checks complement the allowlist; review remains necessary. No files or remotes were changed.",
    }


def main():
    try:
        report = check()
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(json.dumps({"passed": False, "error": str(error)}, indent=2))
        return 1
    print(json.dumps(report, indent=2, ensure_ascii=False))
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
