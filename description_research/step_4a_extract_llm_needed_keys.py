#!/usr/bin/env python3
"""
Step 4a helper - Extract keys still needing LLM-generated descriptions.

This script reads a step JSON (e.g. configurations_descriptions_step_2.json) and
outputs a deterministic JSON report listing (key, implementation) pairs that
still need LLM-generated descriptions.

LLM-needed criteria (as of README step 4):
- documentedConfigurations entries with no `results` (or empty results)
- documentedConfigurations entries that have results but none with source == "registry_doc"
- all missingConfigurations entries

Notes:
- Deterministic output: stable sorting by key, then implementation.
- Logs go to stderr; JSON output file contains only JSON.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional, Set, Tuple


DEFAULT_INPUT = "./result/configurations_descriptions_step_2.json"
DEFAULT_OUTPUT = "./result/configurations_llm_needed_keys.json"

REGISTRY_SOURCE = "registry_doc"


def eprint(*args: object) -> None:
    print(*args, file=sys.stderr)


def load_json(path: Path) -> Any:
    with path.open("r", encoding="utf-8") as f:
        return json.load(f)


def atomic_write_json(path: Path, data: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + ".tmp")
    with tmp.open("w", encoding="utf-8") as f:
        json.dump(data, f, indent=2, ensure_ascii=False)
        f.write("\n")
    os.replace(tmp, path)


def as_key_impl(entry: Any) -> Optional[Tuple[str, str]]:
    if not isinstance(entry, dict):
        return None
    key = entry.get("key")
    impl = entry.get("implementation")
    if not isinstance(key, str) or not key:
        return None
    if not isinstance(impl, str) or not impl:
        return None
    return (key, impl)


def sorted_pairs(pairs: Iterable[Tuple[str, str]]) -> List[Dict[str, str]]:
    # Include a placeholder `description` so an LLM can fill it directly.
    out = [
        {"key": k, "implementation": i, "description": ""}
        for (k, i) in sorted(set(pairs), key=lambda p: (p[0], p[1]))
    ]
    return out


def sources_from_results(results: Any) -> Set[str]:
    sources: Set[str] = set()
    if not isinstance(results, list):
        return sources
    for r in results:
        if not isinstance(r, dict):
            continue
        s = r.get("source")
        if isinstance(s, str) and s:
            sources.add(s)
    return sources


def main(argv: List[str]) -> int:
    parser = argparse.ArgumentParser(description="Extract (key, implementation) pairs still needing LLM-generated descriptions")
    parser.add_argument(
        "--input",
        default=DEFAULT_INPUT,
        help=f"Path to step JSON (default: {DEFAULT_INPUT})",
    )
    parser.add_argument(
        "--output",
        default=DEFAULT_OUTPUT,
        help=f"Output JSON path (default: {DEFAULT_OUTPUT})",
    )
    args = parser.parse_args(argv)

    input_path = Path(args.input)
    output_path = Path(args.output)

    if not input_path.exists():
        eprint(f"error: input file not found: {input_path}")
        return 2

    data = load_json(input_path)
    if not isinstance(data, dict):
        eprint("error: input JSON must be an object")
        return 2

    lang = data.get("lang") if isinstance(data.get("lang"), str) else None

    documented = data.get("documentedConfigurations")
    missing = data.get("missingConfigurations")
    if not isinstance(documented, list) or not isinstance(missing, list):
        eprint("error: input JSON does not look like a step output (missing documentedConfigurations/missingConfigurations arrays)")
        return 2

    documented_no_results: Set[Tuple[str, str]] = set()
    documented_no_registry_doc: Set[Tuple[str, str]] = set()
    missing_pairs: Set[Tuple[str, str]] = set()

    for entry in documented:
        pair = as_key_impl(entry)
        if pair is None:
            continue

        results = entry.get("results") if isinstance(entry, dict) else None
        if not isinstance(results, list) or len(results) == 0:
            documented_no_results.add(pair)
            continue

        sources = sources_from_results(results)
        if REGISTRY_SOURCE not in sources:
            documented_no_registry_doc.add(pair)

    for entry in missing:
        pair = as_key_impl(entry)
        if pair is None:
            continue
        missing_pairs.add(pair)

    # Total unique pairs across all buckets (should match the sum in normal inputs).
    total_pairs = len(documented_no_results | documented_no_registry_doc | missing_pairs)

    out: Dict[str, Any] = {
        **({"lang": lang} if lang is not None else {}),
        "input": str(input_path),
        "criteria": {
            "documented_no_registry_doc_source": REGISTRY_SOURCE,
            "notes": [
                "documentedConfigurations entries with no results",
                "documentedConfigurations entries with results but no registry_doc source",
                "missingConfigurations entries",
            ],
        },
        "counts": {
            "documentedNoResults": len(documented_no_results),
            "documentedNoRegistryDoc": len(documented_no_registry_doc),
            "missingConfigurations": len(missing_pairs),
            "totalPairs": total_pairs,
        },
        "llmNeeded": {
            "documentedNoRegistryDoc": sorted_pairs(documented_no_registry_doc),
            "documentedNoResults": sorted_pairs(documented_no_results),
            "missingConfigurations": sorted_pairs(missing_pairs),
        },
    }

    atomic_write_json(output_path, out)
    eprint(f"Wrote {output_path} (pairs={total_pairs})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

