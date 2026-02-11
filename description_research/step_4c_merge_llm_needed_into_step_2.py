#!/usr/bin/env python3
"""
Step 4c helper - Merge LLM-filled descriptions into step 2 output.

This script merges:
- step 2 pipeline output: configurations_descriptions_step_2.json
- LLM-needed key file (filled by an LLM): configurations_llm_needed_keys.json

and produces:
- an updated configurations_descriptions_step_2.json with "llm_generated" results added.

Merge rules (deterministic):
- For each (key, implementation) present in the LLM-needed file:
  - If its description is usable (passes the quality bar):
    - If the pair is in documentedConfigurations:
      - append/replace a results entry with source "llm_generated"
    - If the pair is in missingConfigurations:
      - move it to documentedConfigurations and add a results entry with source "llm_generated"
      - convert prior missingReasons into missingSources on the documented entry
  - If its description is missing/unusable:
    - If the pair is in missingConfigurations, add missingReasons for source "llm_generated"
      with reason "not_found" or "quality" (without duplicating existing reasons).

Notes:
- Deterministic output ordering (sorted by key, then implementation).
- Logs go to stderr; output file contains only JSON.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple


DEFAULT_STEP2_INPUT = "./result/configurations_descriptions_step_2.json"
DEFAULT_LLM_NEEDED_INPUT = "./result/configurations_llm_needed_keys.json"
DEFAULT_OUTPUT = "./result/configurations_descriptions_step_2.json"

SOURCE_LLM = "llm_generated"


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
    Deterministic approximation of the README quality bar used in steps 1–3.
    (Copied from step_1_registry_doc.py / step_2c_merge_documentation_llm_overrides.py.)
    """
    s = description.strip()
    if len(s) < 20:
        return False

    lowered = s.lower()

    if lowered in {"tbd", "todo", "n/a", "na", "none"}:
        return False

    if re.search(
        r"\b(see|refer to|read)\b.*\b(docs?|documentation|here|below|above)\b",
        lowered,
    ):
        return False

    words = re.findall(r"[A-Za-z0-9]+", s)
    word_count = len(words)
    if re.match(
        r"^(enable|enables|whether to enable|controls whether to enable|turn on|turns on|turn off|turns off)\b",
        lowered,
    ):
        has_detail_punctuation = any(ch in s for ch in [",", ";", ":"])
        multiple_sentences = s.count(".") >= 2
        if word_count <= 10 and not has_detail_punctuation and not multiple_sentences:
            return False

    return True


def has_reason(missing_reasons: Any, source: str) -> bool:
    if not isinstance(missing_reasons, list):
        return False
    for r in missing_reasons:
        if isinstance(r, dict) and r.get("source") == source:
            return True
    return False


def remove_results_with_source(results: Any, source: str) -> List[Dict[str, Any]]:
    if not isinstance(results, list):
        return []
    out: List[Dict[str, Any]] = []
    for r in results:
        if not isinstance(r, dict):
            continue
        if r.get("source") == source:
            continue
        out.append(r)
    return out


def iter_llm_needed_entries(llm_needed_obj: Dict[str, Any]) -> List[Dict[str, Any]]:
    llm_needed = llm_needed_obj.get("llmNeeded")
    if not isinstance(llm_needed, dict):
        raise ValueError('LLM-needed JSON missing object field "llmNeeded"')

    buckets = ["documentedNoRegistryDoc", "documentedNoResults", "missingConfigurations"]
    entries: List[Dict[str, Any]] = []
    for b in buckets:
        arr = llm_needed.get(b)
        if arr is None:
            continue
        if not isinstance(arr, list):
            raise ValueError(f'LLM-needed JSON field "llmNeeded.{b}" must be an array')
        for it in arr:
            if isinstance(it, dict):
                entries.append(it)
    return entries


def main(argv: List[str]) -> int:
    parser = argparse.ArgumentParser(
        description="Merge LLM-filled descriptions from configurations_llm_needed_keys.json into configurations_descriptions_step_2.json"
    )
    parser.add_argument(
        "--step2",
        default=DEFAULT_STEP2_INPUT,
        help=f"Path to configurations_descriptions_step_2.json (default: {DEFAULT_STEP2_INPUT})",
    )
    parser.add_argument(
        "--llm-needed",
        default=DEFAULT_LLM_NEEDED_INPUT,
        help=f"Path to configurations_llm_needed_keys.json (default: {DEFAULT_LLM_NEEDED_INPUT})",
    )
    parser.add_argument(
        "--output",
        default=DEFAULT_OUTPUT,
        help=f"Output path (default: {DEFAULT_OUTPUT})",
    )
    args = parser.parse_args(argv)

    step2_path = Path(args.step2)
    llm_needed_path = Path(args.llm_needed)
    output_path = Path(args.output)

    if not step2_path.exists():
        eprint(f"error: step 2 input file not found: {step2_path}")
        return 2
    if not llm_needed_path.exists():
        eprint(f"error: LLM-needed file not found: {llm_needed_path}")
        return 2

    step2 = load_json(step2_path)
    if not isinstance(step2, dict):
        eprint("error: step 2 JSON must be an object")
        return 2

    documented = step2.get("documentedConfigurations")
    missing = step2.get("missingConfigurations")
    if not isinstance(documented, list) or not isinstance(missing, list):
        eprint(
            "error: step 2 JSON does not look like a step output "
            "(missing documentedConfigurations/missingConfigurations arrays)"
        )
        return 2

    llm_needed_obj = load_json(llm_needed_path)
    if not isinstance(llm_needed_obj, dict):
        eprint("error: LLM-needed JSON must be an object")
        return 2

    # Index step 2 entries by pair.
    documented_by_pair: Dict[Tuple[str, str], Dict[str, Any]] = {}
    for entry in documented:
        pair = as_key_impl(entry)
        if pair is None or not isinstance(entry, dict):
            continue
        if pair in documented_by_pair:
            eprint(f"error: duplicate documented entry for {pair[0]} ({pair[1]}) in step 2")
            return 2
        documented_by_pair[pair] = entry

    missing_by_pair: Dict[Tuple[str, str], Dict[str, Any]] = {}
    for entry in missing:
        pair = as_key_impl(entry)
        if pair is None or not isinstance(entry, dict):
            continue
        if pair in missing_by_pair:
            eprint(f"error: duplicate missing entry for {pair[0]} ({pair[1]}) in step 2")
            return 2
        missing_by_pair[pair] = entry

    llm_override_by_pair: Dict[Tuple[str, str], Dict[str, Any]] = {}
    try:
        llm_entries = iter_llm_needed_entries(llm_needed_obj)
    except ValueError as ve:
        eprint(f"error: {ve}")
        return 2

    for o in llm_entries:
        pair = as_key_impl(o)
        if pair is None:
            continue
        if pair in llm_override_by_pair:
            eprint(f"error: duplicate LLM-needed entry for {pair[0]} ({pair[1]})")
            return 2
        llm_override_by_pair[pair] = o

    updated_documented: List[Dict[str, Any]] = [e for e in documented if isinstance(e, dict)]
    updated_missing: List[Dict[str, Any]] = [e for e in missing if isinstance(e, dict)]

    # Build a set for fast removal from updated_missing when promoting.
    promoted_pairs: set[Tuple[str, str]] = set()

    applied = 0
    promoted = 0
    kept_missing = 0

    for pair, override in llm_override_by_pair.items():
        raw_desc = override.get("description") if isinstance(override, dict) else None
        desc = normalize_description(raw_desc)
        usable = desc is not None and passes_quality_bar(desc)

        if usable:
            # Update existing documented entry (append/replace llm_generated).
            doc_entry = documented_by_pair.get(pair)
            if doc_entry is not None and isinstance(doc_entry, dict):
                results = remove_results_with_source(doc_entry.get("results"), SOURCE_LLM)
                results.append(
                    {
                        "description": desc,
                        "shortDescription": "",
                        "source": SOURCE_LLM,
                    }
                )
                doc_entry["results"] = results
                applied += 1
                continue

            # Promote missing entry to documented.
            miss_entry = missing_by_pair.get(pair)
            if miss_entry is not None and isinstance(miss_entry, dict):
                new_doc: Dict[str, Any] = {
                    "key": pair[0],
                    "implementation": pair[1],
                    "results": [
                        {
                            "description": desc,
                            "shortDescription": "",
                            "source": SOURCE_LLM,
                        }
                    ],
                }
                prev_missing = miss_entry.get("missingReasons")
                if isinstance(prev_missing, list) and prev_missing:
                    new_doc["missingSources"] = prev_missing
                updated_documented.append(new_doc)
                promoted_pairs.add(pair)
                applied += 1
                promoted += 1
                continue

            # Pair not found in step2 (unexpected but not fatal).
            eprint(f"warning: pair not found in step2: {pair[0]} ({pair[1]})")
            continue

        # Unusable override: only annotate missing entries.
        miss_entry = missing_by_pair.get(pair)
        if miss_entry is None or not isinstance(miss_entry, dict):
            continue

        reason = "quality" if desc is not None else "not_found"
        missing_reasons = miss_entry.get("missingReasons")
        if not isinstance(missing_reasons, list):
            missing_reasons = []
            miss_entry["missingReasons"] = missing_reasons
        if not has_reason(missing_reasons, SOURCE_LLM):
            missing_reasons.append({"source": SOURCE_LLM, "reason": reason})
        kept_missing += 1

    if promoted_pairs:
        updated_missing = [
            e
            for e in updated_missing
            if (p := as_key_impl(e)) is None or p not in promoted_pairs
        ]

    updated_documented.sort(key=lambda c: (c.get("key", ""), c.get("implementation", "")))
    updated_missing.sort(key=lambda c: (c.get("key", ""), c.get("implementation", "")))

    lang = step2.get("lang") if isinstance(step2.get("lang"), str) else None
    out: Dict[str, Any] = {
        **({"lang": lang} if lang is not None else {}),
        "missingCount": len(updated_missing),
        "documentedCount": len(updated_documented),
        "documentedConfigurations": updated_documented,
        "missingConfigurations": updated_missing,
    }

    atomic_write_json(output_path, out)
    eprint(
        f"Wrote {output_path} (applied={applied}, promoted={promoted}, kept_missing={kept_missing}, "
        f"documented={len(updated_documented)}, missing={len(updated_missing)})"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

