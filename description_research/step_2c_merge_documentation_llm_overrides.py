#!/usr/bin/env python3
"""
Step 2c - Documentation (same tracer language) - Materialize LLM-filled overrides.

This script merges:
- step 1 output: configurations_descriptions_step_1.json
- step 2 overrides (LLM-filled data): configurations_descriptions_step_2_overrides.json

and produces:
- configurations_descriptions_step_2.json

Merge rules (deterministic):
- For each entry in step 1 missingConfigurations:
  - If a corresponding overrides entry exists and its description passes the quality bar:
    - move the entry to documentedConfigurations
    - create a results entry with source "documentation_same_language"
    - convert prior missingReasons into missingSources on the documented entry
  - Else:
    - keep it missing and add missingReasons for documentation_same_language:
      - reason "not_found" if no usable override was provided
      - reason "quality" if an override exists but fails the quality bar

Notes:
- This step is meant to *extract* from docs. It should reject low-quality or
  speculative text.
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


DEFAULT_STEP1_INPUT = "./result/configurations_descriptions_step_1.json"
DEFAULT_OVERRIDES_INPUT = "./result/configurations_descriptions_step_2_overrides.json"
DEFAULT_OUTPUT = "./result/configurations_descriptions_step_2.json"

SOURCE_DOCS_SAME_LANGUAGE = "documentation_same_language"


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
    (Copied from step_1_registry_doc.py to keep consistent behavior.)
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


def main(argv: List[str]) -> int:
    parser = argparse.ArgumentParser(
        description="Step 2c: merge LLM-filled doc overrides into step 2 output"
    )
    parser.add_argument(
        "--lang",
        required=True,
        help='Tracer language (example: "golang")',
    )
    parser.add_argument(
        "--step1",
        default=DEFAULT_STEP1_INPUT,
        help=f"Path to configurations_descriptions_step_1.json (default: {DEFAULT_STEP1_INPUT})",
    )
    parser.add_argument(
        "--overrides",
        default=DEFAULT_OVERRIDES_INPUT,
        help=f"Path to LLM-filled step 2 overrides JSON (default: {DEFAULT_OVERRIDES_INPUT})",
    )
    parser.add_argument(
        "--output",
        default=DEFAULT_OUTPUT,
        help=f"Output path for configurations_descriptions_step_2.json (default: {DEFAULT_OUTPUT})",
    )
    args = parser.parse_args(argv)

    lang_arg: str = args.lang
    step1_path = Path(args.step1)
    overrides_path = Path(args.overrides)
    output_path = Path(args.output)

    if not step1_path.exists():
        eprint(f"error: step 1 input file not found: {step1_path}")
        return 2
    if not overrides_path.exists():
        eprint(f"error: overrides file not found: {overrides_path}")
        return 2

    step1 = load_json(step1_path)
    if not isinstance(step1, dict):
        eprint("error: step 1 JSON must be an object")
        return 2

    lang_in = step1.get("lang") if isinstance(step1.get("lang"), str) else None
    if lang_in is not None and lang_in != lang_arg:
        eprint(f'warning: step 1 lang "{lang_in}" != --lang "{lang_arg}", using step 1 lang')
    lang = lang_in or lang_arg

    documented = step1.get("documentedConfigurations")
    missing = step1.get("missingConfigurations")
    if not isinstance(documented, list) or not isinstance(missing, list):
        eprint(
            "error: step 1 JSON does not look like a step output "
            "(missing documentedConfigurations/missingConfigurations arrays)"
        )
        return 2

    overrides_obj = load_json(overrides_path)
    if not isinstance(overrides_obj, dict):
        eprint(
            "error: overrides JSON must be an object "
            "(produced by step_2a_extract_documentation_llm_needed_keys.py)"
        )
        return 2
    overrides_list = overrides_obj.get("overrides")
    if not isinstance(overrides_list, list):
        eprint('error: overrides JSON missing array field "overrides"')
        return 2

    override_by_pair: Dict[Tuple[str, str], Dict[str, Any]] = {}
    for o in overrides_list:
        pair = as_key_impl(o)
        if pair is None:
            continue
        if pair in override_by_pair:
            eprint(f"error: duplicate override entry for {pair[0]} ({pair[1]})")
            return 2
        if isinstance(o, dict):
            override_by_pair[pair] = o

    out_documented: List[Dict[str, Any]] = []
    out_missing: List[Dict[str, Any]] = []

    # Carry forward step 1 documented entries unchanged.
    for entry in documented:
        if isinstance(entry, dict):
            out_documented.append(entry)

    for entry in missing:
        if not isinstance(entry, dict):
            continue
        pair = as_key_impl(entry)
        if pair is None:
            continue

        override = override_by_pair.get(pair)
        desc = (
            normalize_description(override.get("description"))
            if isinstance(override, dict)
            else None
        )

        if desc is not None and passes_quality_bar(desc):
            result: Dict[str, Any] = {
                "description": desc,
                "shortDescription": "",
                "source": SOURCE_DOCS_SAME_LANGUAGE,
            }
            # Optional metadata for review.
            if isinstance(override, dict):
                sf = override.get("sourceFile")
                if isinstance(sf, str) and sf.strip():
                    result["sourceFile"] = sf.strip()

            documented_entry: Dict[str, Any] = {
                "key": pair[0],
                "implementation": pair[1],
                "results": [result],
            }

            prev_missing_reasons = entry.get("missingReasons")
            if isinstance(prev_missing_reasons, list) and prev_missing_reasons:
                documented_entry["missingSources"] = prev_missing_reasons

            out_documented.append(documented_entry)
            continue

        # Keep missing: preserve prior missingReasons and add docs reason if not
        # already present.
        new_entry = dict(entry)
        missing_reasons = new_entry.get("missingReasons")
        if not isinstance(missing_reasons, list):
            missing_reasons = []
            new_entry["missingReasons"] = missing_reasons

        if not has_reason(missing_reasons, SOURCE_DOCS_SAME_LANGUAGE):
            reason = "quality" if desc is not None else "not_found"
            missing_reasons.append({"source": SOURCE_DOCS_SAME_LANGUAGE, "reason": reason})

        out_missing.append(new_entry)

    out_documented.sort(key=lambda c: (c.get("key", ""), c.get("implementation", "")))
    out_missing.sort(key=lambda c: (c.get("key", ""), c.get("implementation", "")))

    out = {
        "lang": lang,
        "missingCount": len(out_missing),
        "documentedCount": len(out_documented),
        "documentedConfigurations": out_documented,
        "missingConfigurations": out_missing,
    }

    atomic_write_json(output_path, out)
    eprint(f"Wrote {output_path} (documented={len(out_documented)}, missing={len(out_missing)})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

