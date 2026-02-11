#!/usr/bin/env python3
"""
Merge per-language "final" configuration description JSON outputs into a
single file.

Inputs: all *.json files directly under --input-dir (non-recursive).
Output: a single JSON file matching the schema described in the
repository README.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any, Dict, Iterable, List, Set, Tuple


class MergeError(ValueError):
    pass


def _die(message: str) -> "None":
    raise MergeError(message)


def _expect_type(path: Path, where: str, value: Any, typ: type) -> Any:
    if not isinstance(value, typ):
        _die(
            f"{path}: {where}: expected {typ.__name__}, got "
            f"{type(value).__name__}"
        )
    return value


def _expect_nonempty_str(path: Path, where: str, value: Any) -> str:
    _expect_type(path, where, value, str)
    s = value.strip()
    if not s:
        _die(f"{path}: {where}: expected non-empty string")
    return s


def _iter_input_files(input_dir: Path, output_path: Path) -> List[Path]:
    # Input discovery is intentionally *non-recursive*:
    # the contract is "all *.json directly under --input-dir".
    #
    # We also keep ordering deterministic by sorting on filename, so that
    # the merged output is stable across runs and machines.
    if not input_dir.exists():
        _die(f"Input dir does not exist: {input_dir}")
    if not input_dir.is_dir():
        _die(f"Input path is not a directory: {input_dir}")

    # If the output lives under the input directory, ignore it during
    # discovery so the script can be re-run without accidentally trying to
    # ingest its own output.
    output_resolved: Path | None = None
    try:
        output_resolved = output_path.resolve()
    except OSError:
        output_resolved = None

    files: List[Path] = []
    for p in sorted(input_dir.iterdir(), key=lambda x: x.name):
        if not p.is_file() or p.suffix.lower() != ".json":
            continue
        if output_resolved is not None:
            try:
                if p.resolve() == output_resolved:
                    continue
            except OSError:
                pass
        files.append(p)

    if not files:
        _die(f"No input files found in {input_dir} (expected *.json)")
    return files


def _load_json(path: Path) -> Dict[str, Any]:
    # Read as UTF-8 and parse the entire file as JSON.
    # If a file isn't valid JSON, fail fast with a location-specific error
    # so it's obvious which per-language output needs fixing.
    try:
        raw = path.read_text(encoding="utf-8")
    except OSError as e:
        _die(f"{path}: unable to read file: {e}")

    try:
        data = json.loads(raw)
    except json.JSONDecodeError as e:
        _die(f"{path}: invalid JSON: {e}")

    return _expect_type(path, "$", data, dict)


def _extract_inputs(
    path: Path, data: Dict[str, Any]
) -> Tuple[str, List[Dict[str, Any]]]:
    # Validate the minimal schema we rely on for grouping and merging.
    # We intentionally fail fast here (instead of silently skipping) because
    # these "final" files are pipeline outputs and should be well-formed.
    lang = _expect_nonempty_str(path, "$.lang", data.get("lang"))
    documented = data.get("documentedConfigurations")
    documented = _expect_type(path, "$.documentedConfigurations", documented, list)

    items: List[Dict[str, Any]] = []
    for i, item in enumerate(documented):
        prefix = f"$.documentedConfigurations[{i}]"
        item = _expect_type(path, prefix, item, dict)
        # Fail fast: required fields for grouping.
        _expect_nonempty_str(path, f"{prefix}.key", item.get("key"))
        _expect_nonempty_str(
            path, f"{prefix}.implementation", item.get("implementation")
        )
        _expect_type(path, f"{prefix}.results", item.get("results"), list)
        items.append(item)

    return lang, items


def _iter_results(
    path: Path, item_index: int, results: List[Any], *, lang: str
) -> Iterable[Tuple[str, str, str]]:
    """
    Yield (lang, source, trimmed_description) for each usable result.

    - Ignore missing/empty descriptions.
    - Fail fast if a non-empty description exists but source is missing/invalid.
    """
    for j, res in enumerate(results):
        prefix = f"$.documentedConfigurations[{item_index}].results[{j}]"
        res = _expect_type(path, prefix, res, dict)

        # "description" is optional in the input, but if it's present we
        # enforce it is a string and we trim whitespace for consistent
        # conflict detection and de-duplication across languages.
        desc_val = res.get("description", None)
        if desc_val is None:
            continue
        if not isinstance(desc_val, str):
            _die(
                f"{path}: {prefix}.description: expected string or null/missing, "
                f"got {type(desc_val).__name__}"
            )

        desc = desc_val.strip()
        if not desc:
            continue

        # "source" is required for provenance. We keep the original source
        # string, but treat empty/whitespace-only as an input error.
        source = _expect_nonempty_str(
            path, f"{prefix}.source", res.get("source")
        )
        yield (lang, source, desc)


def merge_final_json(input_dir: Path, output_path: Path) -> Dict[str, Any]:
    all_langs: Set[str] = set()

    # key -> {langs: set[str], impls: {impl: {langs: set[str],
    #         desc_entries: set[(lang, source, desc)]}}}
    #
    # "desc_entries" is a set of (lang, source, trimmed_description) triples.
    # This gives us a simple, deterministic de-dupe rule:
    # identical (description + source + lang) entries collapse naturally.
    by_key: Dict[str, Dict[str, Any]] = {}

    for file_path in _iter_input_files(input_dir, output_path):
        data = _load_json(file_path)
        lang, documented_items = _extract_inputs(file_path, data)
        all_langs.add(lang)

        for i, item in enumerate(documented_items):
            key = str(item["key"]).strip()
            impl = str(item["implementation"]).strip()
            results = item.get("results")
            prefix = f"$.documentedConfigurations[{i}].results"
            results = _expect_type(
                file_path, prefix, results, list
            )

            key_entry = by_key.setdefault(key, {"langs": set(), "impls": {}})
            key_entry["langs"].add(lang)

            impls: Dict[str, Any] = key_entry["impls"]
            impl_entry = impls.setdefault(
                impl, {"langs": set(), "desc_entries": set()}
            )
            impl_entry["langs"].add(lang)

            # Merge all usable results for this (key, implementation) from
            # the current language into the global accumulator.
            for entry in _iter_results(file_path, i, results, lang=lang):
                impl_entry["desc_entries"].add(entry)

    conflicting_count = 0
    non_conflicting_count = 0

    documented_configurations: List[Dict[str, Any]] = []
    for key in sorted(by_key.keys()):
        key_entry = by_key[key]
        impls: Dict[str, Any] = key_entry["impls"]

        implementations: List[Dict[str, Any]] = []
        for impl in sorted(impls.keys()):
            impl_entry = impls[impl]

            desc_entries = impl_entry["desc_entries"]
            # Deterministic ordering for output: (lang, source, description).
            sorted_desc_entries = sorted(
                desc_entries, key=lambda t: (t[0], t[1], t[2])
            )
            # Conflict is based on distinct *description strings* only,
            # ignoring provenance (lang/source). Whitespace has already been
            # trimmed at ingestion time.
            distinct_desc_strings = {d for (_lang, _source, d) in desc_entries}
            is_conflicting = len(distinct_desc_strings) > 1
            if is_conflicting:
                conflicting_count += 1
            else:
                non_conflicting_count += 1

            implementations.append(
                {
                    "implementation": impl,
                    "langs": sorted(impl_entry["langs"]),
                    "conflictingDescriptions": is_conflicting,
                    "descriptions": [
                        {"description": desc, "source": source, "lang": lang}
                        for (lang, source, desc) in sorted_desc_entries
                    ],
                }
            )

        documented_configurations.append(
            {
                "key": key,
                "langs": sorted(key_entry["langs"]),
                "implementations": implementations,
            }
        )

    # These counts are derived from the grouped structure to guarantee
    # internal consistency (rather than trusting any input "documentedCount").
    implementation_pairs_count = sum(len(by_key[k]["impls"]) for k in by_key)
    merged = {
        "langs": sorted(all_langs),
        "totalCount": len(by_key),
        "implementationPairsCount": implementation_pairs_count,
        "conflictingDescriptionsCount": conflicting_count,
        "nonConflictingDescriptionsCount": non_conflicting_count,
        "documentedConfigurations": documented_configurations,
    }
    return merged


def _parse_args(argv: List[str]) -> argparse.Namespace:
    p = argparse.ArgumentParser(
        description=(
            "Merge description_research/final/*.json into a single merged "
            "JSON file."
        )
    )
    p.add_argument(
        "--input-dir",
        default="description_research/final",
        help=(
            "Directory containing per-language final JSON files "
            "(default: %(default)s)"
        ),
    )
    p.add_argument(
        "--output",
        default="description_research/final/merged.json",
        help="Output JSON path (default: %(default)s)",
    )
    return p.parse_args(argv)


def main(argv: List[str]) -> int:
    args = _parse_args(argv)
    input_dir = Path(args.input_dir)
    output_path = Path(args.output)

    try:
        merged = merge_final_json(input_dir=input_dir, output_path=output_path)
        output_path.parent.mkdir(parents=True, exist_ok=True)
        # Pretty-printed JSON makes diffs reviewable and keeps outputs stable.
        output_path.write_text(
            json.dumps(merged, indent=2, ensure_ascii=False) + "\n",
            encoding="utf-8",
        )
    except MergeError as e:
        print(f"error: {e}", file=sys.stderr)
        return 2

    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
