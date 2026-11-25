"""Checks dependency licences against licences/policy.yaml and renders THIRD_PARTY.md.

Identical in zqnt-sdk-go, zqnt-sdk-python and zqnt-sdk-java. Inputs are the raw reports of the
licence tools: go-licenses CSV, pip-licenses JSON, or the TSV that licences/third-party.ftl makes
license-maven-plugin write. Run through scripts/licence-gate.sh, not directly.
"""

from __future__ import annotations

import argparse
import csv
import datetime
import fnmatch
import json
import re
import sys
from dataclasses import dataclass, field
from pathlib import Path

import yaml

RANK = {"allow": 0, "review": 1, "deny": 2}
UNKNOWN = {"", "unknown", "unknownlicense", "none"}
REQUIRED_EXCEPTION_FIELDS = ("package", "licence", "reason", "reviewer", "date")


@dataclass
class Dependency:
    name: str
    licence: str
    used_by: set[str] = field(default_factory=set)
    status: str = ""
    detail: str = ""


def read_go_licenses(path: Path) -> list[tuple[str, str]]:
    with path.open(newline="") as f:
        return [(row[0], row[2] if len(row) > 2 else "") for row in csv.reader(f) if row]


def read_pip_licenses(path: Path) -> list[tuple[str, str]]:
    return [(p["Name"].lower(), p.get("License", "")) for p in json.loads(path.read_text())]


def read_maven(path: Path) -> list[tuple[str, str]]:
    result = []
    for line in path.read_text().splitlines():
        parts = [p.strip() for p in line.split("\t")]
        if parts and parts[0]:
            licences = [p for p in parts[1:] if p]
            expression = " OR ".join(f"({lic})" for lic in licences) if len(licences) > 1 else "".join(licences)
            result.append((parts[0], expression))
    return result


READERS = {"go": read_go_licenses, "python": read_pip_licenses, "java": read_maven}


def alias_key(text: str) -> str:
    return re.sub(r"[^a-z0-9]", "", text.lower())


class Policy:
    def __init__(self, data: dict, ecosystem: str):
        self.ecosystem = ecosystem
        self.allow = data.get("allow", [])
        self.review = list(data.get("review", []))
        dynamic = data.get("review_dynamic", {})
        self.review_dynamic = dynamic.get("licences", [])
        self.dynamic_here = ecosystem in dynamic.get("ecosystems", [])
        self.deny = data.get("deny", [])
        self.deny_text = [re.compile(p, re.IGNORECASE) for p in data.get("deny_text", [])]
        self.aliases = {alias_key(k): v for k, v in data.get("aliases", {}).items()}

    def normalise(self, licence: str) -> str:
        def replace(part: str) -> str:
            return self.aliases.get(alias_key(part), part.strip())

        whole = self.aliases.get(alias_key(licence))
        if whole:
            return whole
        alternatives = re.findall(r"\(([^()]*)\)", licence)
        if alternatives and re.fullmatch(r"\s*(\([^()]*\)\s*(OR\s*)?)+", licence):
            return " OR ".join(replace(a) for a in alternatives)
        return replace(licence)

    def classify_term(self, term: str) -> tuple[str, str]:
        base = term.split(" WITH ")[0].strip()

        def hit(patterns: list[str]) -> bool:
            return any(fnmatch.fnmatchcase(term, p) or fnmatch.fnmatchcase(base, p) for p in patterns)

        if any(fnmatch.fnmatchcase(term, p) for p in self.review):
            return "review", term
        if hit(self.deny):
            return "deny", f"{term} is denied"
        if hit(self.allow):
            return "allow", term
        if hit(self.review):
            return "review", term
        if hit(self.review_dynamic):
            if self.dynamic_here:
                return "review", term
            return "deny", f"{term} is only allowed where linked dynamically, not in {self.ecosystem}"
        return "deny", f"{term} is not on the allow list"

    def classify(self, raw: str) -> tuple[str, str, str]:
        if alias_key(raw) in UNKNOWN:
            return "deny", "unknown", "no licence detected (unknown or unlicensed)"
        for pattern in self.deny_text:
            if pattern.search(raw):
                return "deny", raw, f"licence text matches denied pattern '{pattern.pattern}'"
        spdx = self.normalise(raw)
        best: tuple[str, str] | None = None
        for alternative in re.split(r"\s+OR\s+", spdx.replace("(", "").replace(")", "")):
            worst = ("allow", alternative)
            for term in re.split(r"\s+AND\s+", alternative):
                result = self.classify_term(term.strip())
                if RANK[result[0]] > RANK[worst[0]]:
                    worst = result
            if best is None or RANK[worst[0]] < RANK[best[0]]:
                best = worst
        assert best is not None
        return best[0], spdx, best[1]


def load_exceptions(path: Path) -> tuple[dict[str, dict], list[str]]:
    if not path.exists():
        return {}, []
    entries = (yaml.safe_load(path.read_text()) or {}).get("exceptions") or []
    errors, by_package = [], {}
    for i, entry in enumerate(entries):
        missing = [k for k in REQUIRED_EXCEPTION_FIELDS if not str(entry.get(k, "")).strip()]
        if missing:
            errors.append(f"exceptions.yaml entry {i + 1}: missing {', '.join(missing)}")
            continue
        try:
            datetime.date.fromisoformat(str(entry["date"]))
        except ValueError:
            errors.append(f"exceptions.yaml entry {i + 1}: date must be YYYY-MM-DD")
            continue
        by_package[entry["package"]] = entry
    return by_package, errors


def collect(inputs: list[str], ecosystem: str, ignore: list[str]) -> dict[str, Dependency]:
    deps: dict[str, Dependency] = {}
    for spec in inputs:
        path_text, _, label = spec.partition("=")
        for name, licence in READERS[ecosystem](Path(path_text)):
            if any(name == p or name.startswith(p + "/") for p in ignore):
                continue
            dep = deps.setdefault(name, Dependency(name, licence))
            if licence and not dep.licence:
                dep.licence = licence
            if label:
                dep.used_by.add(label)
    return deps


def render(deps: list[Dependency], ecosystem: str, with_used_by: bool) -> str:
    lines = [
        "# Third-party licences",
        "",
        f"Generated by `scripts/licence-gate.sh` from the resolved {ecosystem} dependencies (runtime and test).",
        "Policy: `licences/policy.yaml`, reviewed exceptions: `licences/exceptions.yaml`.",
        "Status `review` marks file-level copyleft (ship unmodified, publish changes to its own files).",
        "",
    ]
    header = ["Dependency", "Licence", "Status"] + (["Used by"] if with_used_by else [])
    lines.append("| " + " | ".join(header) + " |")
    lines.append("|" + "---|" * len(header))
    for dep in deps:
        row = [f"`{dep.name}`", dep.licence.replace("|", "\\|"), dep.status]
        if with_used_by:
            row.append(", ".join(sorted(dep.used_by)))
        lines.append("| " + " | ".join(row) + " |")
    return "\n".join(lines) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ecosystem", required=True, choices=sorted(READERS))
    parser.add_argument("--policy", type=Path, default=Path("licences/policy.yaml"))
    parser.add_argument("--exceptions", type=Path, default=Path("licences/exceptions.yaml"))
    parser.add_argument("--ignore", action="append", default=[], help="first-party package prefix")
    parser.add_argument("--third-party", type=Path, default=Path("THIRD_PARTY.md"))
    parser.add_argument("--check", action="store_true", help="fail when THIRD_PARTY.md is out of date")
    parser.add_argument("inputs", nargs="+", help="report file, optionally FILE=label")
    args = parser.parse_args()

    policy = Policy(yaml.safe_load(args.policy.read_text()), args.ecosystem)
    exceptions, errors = load_exceptions(args.exceptions)
    deps = collect(args.inputs, args.ecosystem, args.ignore)
    if not deps:
        errors.append("no dependencies found in the reports")
    used_exceptions = set()

    for dep in sorted(deps.values(), key=lambda d: d.name):
        status, spdx, detail = policy.classify(dep.licence)
        exception = exceptions.get(dep.name)
        if exception and str(exception["licence"]) in (dep.licence, spdx):
            used_exceptions.add(dep.name)
            status, detail = "exception", f"{exception['reason']} ({exception['reviewer']}, {exception['date']})"
        dep.licence = spdx or "unknown"
        dep.status, dep.detail = status, detail
        print(f"{status:9} {dep.name:60} {dep.licence}")
        if status == "deny":
            errors.append(f"{dep.name}: {detail}")

    for name in sorted(set(exceptions) - used_exceptions):
        print(f"warning: exception for {name} matches no dependency with that licence; remove it")

    ordered = sorted(deps.values(), key=lambda d: d.name)
    with_used_by = len({label for d in ordered for label in d.used_by}) > 1
    content = render(ordered, args.ecosystem, with_used_by)
    if args.check:
        current = args.third_party.read_text() if args.third_party.exists() else ""
        if current != content:
            errors.append(f"{args.third_party} is out of date: run scripts/licence-gate.sh and commit it")
    elif not errors:
        args.third_party.write_text(content)

    if errors:
        print("\nLicence gate failed:")
        for error in errors:
            print(f"  - {error}")
        print("Policy: licences/policy.yaml. A reviewed case goes to licences/exceptions.yaml.")
        return 1
    print(f"\nLicence gate passed: {len(deps)} dependencies.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
