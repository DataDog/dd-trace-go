#!/usr/bin/env python3
"""
Step 2a - Documentation (same tracer language) - LLM-needed key extraction.

Rationale:
The Datadog documentation repo is not reliably parseable with deterministic scripts
(shortcodes, partials, mixed formats). Instead, we use an LLM to *extract*
(not invent) descriptions by searching the docs repo.

This script is deterministic and produces an "overrides skeleton" JSON that an LLM
can fill in. A separate deterministic materializer (step 2c) merges the filled
overrides back into the step 1 output to produce the step 2 pipeline output.

Output written by this script is intended to be edited by an LLM (reviewable data).
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple


DEFAULT_INPUT = "./result/configurations_descriptions_step_1.json"
DEFAULT_OUTPUT = "./result/configurations_descriptions_step_2_overrides.json"
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


def main(argv: List[str]) -> int:
    parser = argparse.ArgumentParser(
        description=(
            "Step 2a: produce an LLM-fillable overrides JSON for missing step 1 keys"
        )
    )
    parser.add_argument(
        "--lang",
        required=True,
        help='Tracer language (example: "golang")',
    )
    parser.add_argument(
        "--input",
        default=DEFAULT_INPUT,
        help=(
            "Path to configurations_descriptions_step_1.json "
            f"(default: {DEFAULT_INPUT})"
        ),
    )
    parser.add_argument(
        "--output",
        default=DEFAULT_OUTPUT,
        help=f"Output overrides JSON path (default: {DEFAULT_OUTPUT})",
    )
    args = parser.parse_args(argv)

    lang_arg: str = args.lang
    input_path = Path(args.input)
    output_path = Path(args.output)

    if not input_path.exists():
        eprint(f"error: input file not found: {input_path}")
        return 2

    step1 = load_json(input_path)
    if not isinstance(step1, dict):
        eprint("error: input JSON must be an object")
        return 2

    lang_in = step1.get("lang") if isinstance(step1.get("lang"), str) else None
    if lang_in is not None and lang_in != lang_arg:
        eprint(
            f'warning: input lang "{lang_in}" != --lang "{lang_arg}", using input lang'
        )
    lang = lang_in or lang_arg

    missing = step1.get("missingConfigurations")
    if not isinstance(missing, list):
        eprint(
            "error: input JSON does not look like step 1 output "
            "(missingConfigurations not an array)"
        )
        return 2

    pairs: List[Tuple[str, str]] = []
    for entry in missing:
        pair = as_key_impl(entry)
        if pair is None:
            continue
        pairs.append(pair)

    # Unique + deterministic ordering.
    pairs = sorted(set(pairs), key=lambda p: (p[0], p[1]))

    overrides: List[Dict[str, Any]] = []
    for key, impl in pairs:
        overrides.append(
            {
                "key": key,
                "implementation": impl,
                # To be filled by an LLM:
                "description": "",
                # Optional but strongly recommended for review:
                # e.g. "content/en/tracing/setup/go.md:123"
                "sourceFile": "",
            }
        )

    out: Dict[str, Any] = {
        "lang": lang,
        "source": SOURCE_DOCS_SAME_LANGUAGE,
        "input": str(input_path),
        "docsRepoHint": "./documentation",
        "counts": {
            "missingConfigurations": len(pairs),
        },
        "overrides": overrides,
    }

    atomic_write_json(output_path, out)
    eprint(f"Wrote {output_path} (missingConfigurations={len(pairs)})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

