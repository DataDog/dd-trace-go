#!/usr/bin/env python3
"""
Step 1 - Registry documentation (label: registry_doc)

This script joins:
- tracer key list from internal/env/supported_configurations.json (key + implementation)
- registry JSON from https://dd-feature-parity.azurewebsites.net/configurations/

and produces:
- <output>/configurations_descriptions_step_1.json

Contract: see description_research/README.md
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import urllib.request
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple


REGISTRY_URL = "https://dd-feature-parity.azurewebsites.net/configurations/"
OUTPUT_FILENAME = "configurations_descriptions_step_1.json"
SOURCE_REGISTRY = "registry_doc"


def eprint(*args: object) -> None:
    print(*args, file=sys.stderr)


def load_json(path: Path) -> Any:
    with path.open("r", encoding="utf-8") as f:
        return json.load(f)


def fetch_registry_payload(url: str, timeout_seconds: int = 30) -> Any:
    req = urllib.request.Request(
        url,
        headers={
            "User-Agent": "dd-trace-go description_research step_1_registry_doc",
            "Accept": "application/json",
        },
        method="GET",
    )
    with urllib.request.urlopen(req, timeout=timeout_seconds) as resp:
        raw = resp.read()
    # Assume UTF-8 JSON.
    return json.loads(raw.decode("utf-8"))


def supported_pairs(supported_configurations_json: Any) -> List[Tuple[str, str]]:
    """
    Extract all (key, implementation) pairs from supported_configurations.json.
    Ensures uniqueness and stable ordering (sorted by key, then implementation).
    """
    if not isinstance(supported_configurations_json, dict):
        raise ValueError("supported_configurations.json must be a JSON object")

    supported = supported_configurations_json.get("supportedConfigurations")
    if not isinstance(supported, dict):
        raise ValueError('supported_configurations.json missing object field "supportedConfigurations"')

    pairs: List[Tuple[str, str]] = []
    seen: set[Tuple[str, str]] = set()
    for key, entries in supported.items():
        if not isinstance(key, str) or not key:
            continue
        if not isinstance(entries, list):
            continue
        for entry in entries:
            if not isinstance(entry, dict):
                continue
            impl = entry.get("implementation")
            if not isinstance(impl, str):
                continue
            impl = impl.strip()
            if not impl:
                continue
            pair = (key, impl)
            if pair in seen:
                continue
            seen.add(pair)
            pairs.append(pair)

    pairs.sort(key=lambda p: (p[0], p[1]))
    return pairs


def build_registry_index(registry_payload: Any) -> Dict[str, List[Dict[str, Any]]]:
    """
    Build registryByKey[name] => list of configuration records in stable payload order.
    If the payload contains duplicate "name" entries, their configurations are concatenated
    in order of appearance.
    """
    if not isinstance(registry_payload, list):
        raise ValueError("registry payload must be a JSON array")

    out: Dict[str, List[Dict[str, Any]]] = {}
    for item in registry_payload:
        if not isinstance(item, dict):
            continue
        name = item.get("name")
        if not isinstance(name, str) or not name:
            continue
        configs = item.get("configurations")
        if not isinstance(configs, list):
            continue
        bucket = out.setdefault(name, [])
        for c in configs:
            if isinstance(c, dict):
                bucket.append(c)
    return out


def has_language(config_record: Dict[str, Any], lang: str) -> bool:
    impls = config_record.get("implementations")
    if not isinstance(impls, list):
        return False
    for impl in impls:
        if not isinstance(impl, dict):
            continue
        if impl.get("language") == lang:
            return True
    return False


def normalize_description(raw: Any) -> Optional[str]:
    if raw is None:
        return None
    if not isinstance(raw, str):
        return None
    s = raw.strip()
    if not s:
        return None
    if s.lower() == "null":
        return None
    return s


def passes_quality_bar(description: str) -> bool:
    """
    Steps 1–3 must reject low-quality text.

    The README defines a qualitative bar (specific + self-contained + not trivially short).
    This function implements a deterministic approximation of that bar.
    """
    s = description.strip()
    if len(s) < 20:
        return False

    lowered = s.lower()

    # Reject common placeholders.
    if lowered in {"tbd", "todo", "n/a", "na", "none"}:
        return False

    # Reject descriptions that aren't self-contained (tell the user to look elsewhere).
    if re.search(r"\b(see|refer to|read)\b.*\b(docs?|documentation|here|below|above)\b", lowered):
        return False

    # Reject shallow toggle descriptions like "Enable X." / "Whether to enable X."
    # These usually fail the "specific" and "self-contained" requirements.
    words = re.findall(r"[A-Za-z0-9]+", s)
    word_count = len(words)
    if re.match(r"^(enable|enables|whether to enable|controls whether to enable|turn on|turns on|turn off|turns off)\b", lowered):
        # If it's a short, single-sentence toggle without further context, reject.
        has_detail_punctuation = any(ch in s for ch in [",", ";", ":"])
        multiple_sentences = s.count(".") >= 2
        if word_count <= 10 and not has_detail_punctuation and not multiple_sentences:
            return False

    return True


def select_registry_config_record(
    configs: List[Dict[str, Any]], implementation: str, lang: str
) -> Optional[Dict[str, Any]]:
    """
    Deterministic selection per README:
    - Prefer version == implementation
      - If multiple: prefer ones that include language == lang
      - If still tied: pick first in payload order
    - Else:
      - record with implementations including lang
      - else first record with a non-empty description
      - else None
    """
    matching_version: List[Dict[str, Any]] = []
    for c in configs:
        v = c.get("version")
        if isinstance(v, str) and v.strip() == implementation:
            matching_version.append(c)

    if matching_version:
        for c in matching_version:
            if has_language(c, lang):
                return c
        return matching_version[0]

    for c in configs:
        if has_language(c, lang):
            return c

    for c in configs:
        if normalize_description(c.get("description")) is not None:
            return c

    return None


def atomic_write_json(path: Path, data: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + ".tmp")
    with tmp.open("w", encoding="utf-8") as f:
        json.dump(data, f, indent=2, ensure_ascii=False)
        f.write("\n")
    os.replace(tmp, path)


def main(argv: List[str]) -> int:
    parser = argparse.ArgumentParser(description="Step 1: extract configuration descriptions from registry")
    parser.add_argument("--lang", required=True, help='Tracer language (example: "golang")')
    parser.add_argument(
        "--supported-configurations",
        default="internal/env/supported_configurations.json",
        help=(
            "Path to supported_configurations.json (default: internal/env/supported_configurations.json). "
            "If the path doesn't exist relative to the current working directory, it is resolved relative to the "
            "repository root (parent of description_research/)."
        ),
    )
    parser.add_argument(
        "--output",
        default="./result",
        help="Output directory where configurations_descriptions_step_1.json will be produced (default: ./result)",
    )
    args = parser.parse_args(argv)

    lang: str = args.lang
    supported_path = Path(args.supported_configurations)
    output_dir = Path(args.output)
    output_path = output_dir / OUTPUT_FILENAME

    # Resolve default paths in a way that supports running from description_research/.
    # If the provided path doesn't exist relative to the current working directory,
    # fall back to resolving it relative to the repository root (parent of this file's directory).
    if not supported_path.is_absolute() and not supported_path.exists():
        repo_root = Path(__file__).resolve().parent.parent
        candidate = (repo_root / supported_path).resolve()
        if candidate.exists():
            supported_path = candidate

    if not supported_path.exists():
        eprint(f"error: supported configurations file not found: {supported_path}")
        eprint('hint: run from "description_research/" or pass --supported-configurations explicitly')
        return 2

    supported_json = load_json(supported_path)
    pairs = supported_pairs(supported_json)

    eprint(f"Loaded {len(pairs)} supported (key, implementation) pairs from {supported_path}")
    eprint(f"Fetching registry payload from {REGISTRY_URL}")
    registry_payload = fetch_registry_payload(REGISTRY_URL)
    registry_by_key = build_registry_index(registry_payload)

    documented: List[Dict[str, Any]] = []
    missing: List[Dict[str, Any]] = []

    for key, impl in pairs:
        configs = registry_by_key.get(key)
        if configs is None:
            missing.append(
                {
                    "key": key,
                    "implementation": impl,
                    "missingReasons": [{"source": SOURCE_REGISTRY, "reason": "not_found"}],
                }
            )
            continue

        selected = select_registry_config_record(configs, impl, lang)
        if selected is None:
            missing.append(
                {
                    "key": key,
                    "implementation": impl,
                    "missingReasons": [{"source": SOURCE_REGISTRY, "reason": "not_found"}],
                }
            )
            continue

        desc = normalize_description(selected.get("description"))
        if desc is None or not passes_quality_bar(desc):
            missing.append(
                {
                    "key": key,
                    "implementation": impl,
                    "missingReasons": [{"source": SOURCE_REGISTRY, "reason": "quality"}],
                }
            )
            continue

        documented.append(
            {
                "key": key,
                "implementation": impl,
                "results": [{"description": desc, "shortDescription": "", "source": SOURCE_REGISTRY}],
            }
        )

    documented.sort(key=lambda c: (c["key"], c["implementation"]))
    missing.sort(key=lambda c: (c["key"], c["implementation"]))

    output_obj = {
        "lang": lang,
        "missingCount": len(missing),
        "documentedCount": len(documented),
        "documentedConfigurations": documented,
        "missingConfigurations": missing,
    }

    atomic_write_json(output_path, output_obj)
    eprint(f"Wrote {output_path} (documented={len(documented)}, missing={len(missing)})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

