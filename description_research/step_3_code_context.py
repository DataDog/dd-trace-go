#!/usr/bin/env python3
"""
Step 3 - Code parser / deterministic context packet (no LLM).

This step produces a stable JSON "context packet" that can be used by an LLM in a later step
to generate high-quality configuration descriptions.

It does NOT generate descriptions.

Inputs:
- Step 2 output (default: ./result/configurations_descriptions_step_2.json)
- Repository checkout (default: repo root is inferred as parent of this file's directory)
- internal/env/supported_configurations.json for metadata (type/default/aliases)

Output:
- ./result/configurations_descriptions_step_3_context.json
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional, Sequence, Tuple


DEFAULT_INPUT = "./result/configurations_descriptions_step_2.json"
DEFAULT_OUTPUT = "./result/configurations_descriptions_step_3_context.json"
DEFAULT_SUPPORTED_CONFIGS = "../internal/env/supported_configurations.json"

REGISTRY_SOURCE = "registry_doc"

TOKEN_BOUNDARY_CLASS = r"[A-Za-z0-9_-]"

# Default scan scope (keeps runtime practical). Can be overridden via CLI flags.
DEFAULT_INCLUDE_DIRS = (
    "ddtrace",
    "internal",
    "profiler",
    "appsec",
    "civisibility",
    "datastreams",
    "instrumentation",
    "llmobs",
    "openfeature",
    "rules",
    "orchestrion",
)


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


def compile_union_regex(terms: Iterable[str]) -> Optional[re.Pattern[str]]:
    terms = [t for t in terms if t]
    if not terms:
        return None
    pattern = "|".join(re.escape(t) for t in sorted(set(terms)))
    return re.compile(rf"(?<!{TOKEN_BOUNDARY_CLASS})(?:{pattern})(?!{TOKEN_BOUNDARY_CLASS})")


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


def sources_from_results(results: Any) -> List[str]:
    if not isinstance(results, list):
        return []
    out: List[str] = []
    for r in results:
        if not isinstance(r, dict):
            continue
        s = r.get("source")
        if isinstance(s, str) and s and s not in out:
            out.append(s)
    return out


def llm_needed_pairs(step2: Dict[str, Any]) -> List[Tuple[str, str]]:
    """
    Reuse the same criteria as step_4a_extract_llm_needed_keys.py, but run on Step 2 output:
    - documented entries with no results OR with results but no registry_doc
    - all missing entries
    """
    documented = step2.get("documentedConfigurations")
    missing = step2.get("missingConfigurations")
    if not isinstance(documented, list) or not isinstance(missing, list):
        return []

    needed: set[Tuple[str, str]] = set()

    for entry in documented:
        pair = as_key_impl(entry)
        if pair is None:
            continue
        results = entry.get("results") if isinstance(entry, dict) else None
        if not isinstance(results, list) or len(results) == 0:
            needed.add(pair)
            continue
        if REGISTRY_SOURCE not in set(sources_from_results(results)):
            needed.add(pair)

    for entry in missing:
        pair = as_key_impl(entry)
        if pair is None:
            continue
        needed.add(pair)

    return sorted(needed, key=lambda p: (p[0], p[1]))


def load_supported_configurations(path: Path) -> Dict[str, List[Dict[str, Any]]]:
    data = load_json(path)
    supported = data.get("supportedConfigurations")
    if not isinstance(supported, dict):
        return {}
    out: Dict[str, List[Dict[str, Any]]] = {}
    for k, v in supported.items():
        if isinstance(k, str) and isinstance(v, list):
            out[k] = [x for x in v if isinstance(x, dict)]
    return out


def supported_entry_for_impl(entries: List[Dict[str, Any]], impl: str) -> Optional[Dict[str, Any]]:
    for e in entries:
        if e.get("implementation") == impl:
            return e
    return None


def build_term_map(
    pairs: Sequence[Tuple[str, str]],
    supported: Dict[str, List[Dict[str, Any]]],
) -> Tuple[List[str], Dict[str, str], Dict[str, List[str]]]:
    """
    Returns:
    - terms: list of all terms to scan for in code
    - term_to_key: term => canonical key
    - key_to_terms: key => [terms...]
    """
    term_to_key: Dict[str, str] = {}
    key_to_terms: Dict[str, List[str]] = {}
    terms: List[str] = []

    keys = sorted({k for (k, _) in pairs})
    for k in keys:
        t: List[str] = [k]
        for entry in supported.get(k, []):
            aliases = entry.get("aliases")
            if isinstance(aliases, list):
                for a in aliases:
                    if isinstance(a, str) and a.strip():
                        t.append(a.strip())
        # De-dupe stable.
        seen: set[str] = set()
        deduped: List[str] = []
        for x in t:
            if not x or x in seen:
                continue
            seen.add(x)
            deduped.append(x)
        key_to_terms[k] = deduped
        for x in deduped:
            term_to_key.setdefault(x, k)
            terms.append(x)

    # De-dupe terms overall (stable via sort in regex compiler; keep this for stats only).
    return terms, term_to_key, key_to_terms


@dataclass(frozen=True)
class Occurrence:
    key: str
    term: str
    file: str  # repo-relative posix
    line: int  # 1-based
    func: str
    snippet: str

    def sort_key(self) -> Tuple[str, int, str, str]:
        return (self.file, self.line, self.func, self.term)


def repo_go_files(repo_root: Path, *, include_dirs: Optional[Sequence[str]] = None, full_scan: bool = False) -> List[Path]:
    """
    Deterministic list of Go files to scan, excluding documentation checkout and result artifacts.

    By default, the scan is limited to a curated set of directories (DEFAULT_INCLUDE_DIRS)
    to keep runtime practical. Use full_scan=True to scan the entire repo.
    """
    exclude_dirs = {
        ".git",
        "description_research/documentation",
        "description_research/result",
        "description_research/__pycache__",
        # Huge integration corpus; scan only when explicitly included.
        "contrib",
    }
    out: List[Path] = []
    if full_scan:
        for p in repo_root.rglob("*.go"):
            rel = p.relative_to(repo_root).as_posix()
            if any(rel == d or rel.startswith(d + "/") for d in exclude_dirs):
                continue
            out.append(p)
    else:
        dirs = list(include_dirs or DEFAULT_INCLUDE_DIRS)
        # Also include repo-root .go files.
        for p in repo_root.glob("*.go"):
            rel = p.relative_to(repo_root).as_posix()
            if any(rel == d or rel.startswith(d + "/") for d in exclude_dirs):
                continue
            out.append(p)
        for d in dirs:
            root = repo_root / d
            if not root.exists():
                continue
            for p in root.rglob("*.go"):
                rel = p.relative_to(repo_root).as_posix()
                if any(rel == ex or rel.startswith(ex + "/") for ex in exclude_dirs):
                    continue
                out.append(p)
    out.sort(key=lambda p: p.relative_to(repo_root).as_posix())
    return out


FUNC_RE = re.compile(r"^\s*func\s+(?:\([^)]*\)\s*)?(?P<name>[A-Za-z0-9_]+)\s*\(")


def find_enclosing_func(lines: List[str], line_idx: int) -> str:
    """
    Best-effort: find nearest preceding 'func ...' line within a bounded window.
    """
    start = max(0, line_idx - 120)
    for i in range(line_idx, start - 1, -1):
        m = FUNC_RE.match(lines[i])
        if m:
            return m.group("name")
    return ""


def snippet_around(lines: List[str], line_idx: int, *, before: int, after: int) -> str:
    s = max(0, line_idx - before)
    e = min(len(lines) - 1, line_idx + after)
    return "\n".join(lines[s : e + 1]).rstrip()


def scan_repo_for_occurrences(
    repo_root: Path,
    term_to_key: Dict[str, str],
    terms: List[str],
    *,
    include_dirs: Optional[Sequence[str]],
    full_scan: bool,
    max_occurrences_per_key: int,
    snippet_before: int,
    snippet_after: int,
    debug: bool,
) -> Dict[str, List[Occurrence]]:
    re_terms = compile_union_regex(terms)
    if re_terms is None:
        return {}

    per_key: Dict[str, List[Occurrence]] = {k: [] for k in sorted(set(term_to_key.values()))}

    files = repo_go_files(repo_root, include_dirs=include_dirs, full_scan=full_scan)
    total = len(files)
    for idx, p in enumerate(files, start=1):
        rel = p.relative_to(repo_root).as_posix()
        if debug and (idx == 1 or idx % 400 == 0 or idx == total):
            eprint(f"[scan code] {idx}/{total} {rel}")
        try:
            content = p.read_text(encoding="utf-8", errors="replace")
        except Exception:
            continue

        # Quick reject.
        if re_terms.search(content) is None:
            continue

        content = content.replace("\r\n", "\n").replace("\r", "\n")
        lines = content.split("\n")

        # We only need unique line-level occurrences; avoid spamming repeated terms in the same line.
        for i, ln in enumerate(lines):
            if not ln:
                continue
            ms = list(re_terms.finditer(ln))
            if not ms:
                continue
            seen_terms_line: set[str] = set()
            for m in ms:
                term = m.group(0)
                if term in seen_terms_line:
                    continue
                seen_terms_line.add(term)
                key = term_to_key.get(term)
                if key is None:
                    continue
                if len(per_key.get(key, [])) >= max_occurrences_per_key:
                    continue
                fn = find_enclosing_func(lines, i)
                snip = snippet_around(lines, i, before=snippet_before, after=snippet_after)
                per_key.setdefault(key, []).append(
                    Occurrence(
                        key=key,
                        term=term,
                        file=rel,
                        line=i + 1,
                        func=fn,
                        snippet=snip,
                    )
                )

    # Deterministic sort of occurrences.
    for k, occs in per_key.items():
        occs.sort(key=lambda o: o.sort_key())
    return per_key


PARSING_PATTERNS: List[Tuple[str, re.Pattern[str]]] = [
    ("stableconfig.Bool", re.compile(r"stableconfig\.Bool\(\s*\"(?P<key>[A-Z0-9_]+)\"\s*,\s*(?P<def>[^)]+)\)")),
    ("stableconfig.String", re.compile(r"stableconfig\.String\(\s*\"(?P<key>[A-Z0-9_]+)\"\s*,\s*(?P<def>[^)]+)\)")),
    ("internal.BoolEnv", re.compile(r"internal\.BoolEnv\(\s*\"(?P<key>[A-Z0-9_]+)\"\s*,\s*(?P<def>[^)]+)\)")),
    ("internal.IntEnv", re.compile(r"internal\.IntEnv\(\s*\"(?P<key>[A-Z0-9_]+)\"\s*,\s*(?P<def>[^)]+)\)")),
    ("internal.FloatEnv", re.compile(r"internal\.FloatEnv\(\s*\"(?P<key>[A-Z0-9_]+)\"\s*,\s*(?P<def>[^)]+)\)")),
    ("env.Get", re.compile(r"env\.Get\(\s*\"(?P<key>[A-Z0-9_]+)\"\s*\)")),
    ("env.Lookup", re.compile(r"env\.Lookup\(\s*\"(?P<key>[A-Z0-9_]+)\"\s*\)")),
]


def parsing_hints_from_occurrences(occs: List[Occurrence]) -> List[Dict[str, Any]]:
    """
    Extract structured parsing hints from the snippet text.
    """
    hints: List[Dict[str, Any]] = []
    seen: set[Tuple[str, str, str, int]] = set()
    for o in occs:
        for kind, pat in PARSING_PATTERNS:
            for m in pat.finditer(o.snippet):
                key = m.group("key")
                if key != o.key:
                    continue
                defv = m.groupdict().get("def")
                tup = (kind, o.file, o.func, o.line)
                if tup in seen:
                    continue
                seen.add(tup)
                hints.append(
                    {
                        "kind": kind,
                        "file": o.file,
                        "line": o.line,
                        "func": o.func,
                        **({"defaultExpr": defv.strip()} if defv is not None else {}),
                    }
                )
    hints.sort(key=lambda h: (h.get("file", ""), int(h.get("line", 0)), h.get("kind", ""), h.get("func", "")))
    return hints


def index_step2_entries(step2: Dict[str, Any]) -> Tuple[Dict[Tuple[str, str], Dict[str, Any]], Dict[Tuple[str, str], Dict[str, Any]]]:
    documented = step2.get("documentedConfigurations")
    missing = step2.get("missingConfigurations")
    doc_map: Dict[Tuple[str, str], Dict[str, Any]] = {}
    miss_map: Dict[Tuple[str, str], Dict[str, Any]] = {}
    if isinstance(documented, list):
        for e in documented:
            pair = as_key_impl(e)
            if pair is None:
                continue
            if isinstance(e, dict):
                doc_map[pair] = e
    if isinstance(missing, list):
        for e in missing:
            pair = as_key_impl(e)
            if pair is None:
                continue
            if isinstance(e, dict):
                miss_map[pair] = e
    return doc_map, miss_map


def main(argv: List[str]) -> int:
    parser = argparse.ArgumentParser(description="Step 3: build deterministic code-context packet (no LLM)")
    parser.add_argument("--lang", required=True, help='Tracer language (example: "golang")')
    parser.add_argument("--input", default=DEFAULT_INPUT, help=f"Path to step 2 JSON (default: {DEFAULT_INPUT})")
    parser.add_argument("--repo-root", default=str(Path(__file__).resolve().parent.parent), help="Path to repo root checkout (default: inferred)")
    parser.add_argument(
        "--supported-configurations",
        default=DEFAULT_SUPPORTED_CONFIGS,
        help=f"Path to supported_configurations.json (default: {DEFAULT_SUPPORTED_CONFIGS})",
    )
    parser.add_argument("--output", default=DEFAULT_OUTPUT, help=f"Output JSON path (default: {DEFAULT_OUTPUT})")
    parser.add_argument(
        "--include-dirs",
        default=",".join(DEFAULT_INCLUDE_DIRS),
        help="Comma-separated repo-relative directories to scan for code usage (default: curated core dirs; excludes contrib)",
    )
    parser.add_argument(
        "--full-scan",
        action="store_true",
        help="Scan all Go files in the repo (can be slow). Still excludes description_research/documentation and contrib by default.",
    )
    parser.add_argument("--max-occurrences-per-key", type=int, default=25, help="Cap code occurrences per key (default: 25)")
    parser.add_argument("--snippet-before", type=int, default=3, help="Snippet lines before match (default: 3)")
    parser.add_argument("--snippet-after", type=int, default=4, help="Snippet lines after match (default: 4)")
    parser.add_argument("--debug", action="store_true", help="Enable deterministic progress logs to stderr")
    args = parser.parse_args(argv)

    lang: str = args.lang
    input_path = Path(args.input)
    repo_root = Path(args.repo_root)
    supported_path = Path(args.supported_configurations)
    output_path = Path(args.output)
    debug = bool(args.debug)
    include_dirs = [d.strip() for d in str(args.include_dirs).split(",") if d.strip()]
    full_scan = bool(args.full_scan)

    if not input_path.exists():
        eprint(f"error: input file not found: {input_path}")
        return 2
    if not repo_root.exists():
        eprint(f"error: repo root not found: {repo_root}")
        return 2
    if not supported_path.exists():
        eprint(f"error: supported configurations not found: {supported_path}")
        return 2

    step2 = load_json(input_path)
    if not isinstance(step2, dict):
        eprint("error: step2 JSON must be an object")
        return 2

    pairs = llm_needed_pairs(step2)
    eprint(f"Loaded step 2: llmNeededPairs={len(pairs)}")

    supported = load_supported_configurations(supported_path)
    terms, term_to_key, key_to_terms = build_term_map(pairs, supported)

    occ_by_key = scan_repo_for_occurrences(
        repo_root,
        term_to_key=term_to_key,
        terms=terms,
        include_dirs=include_dirs,
        full_scan=full_scan,
        max_occurrences_per_key=max(1, int(args.max_occurrences_per_key)),
        snippet_before=max(0, int(args.snippet_before)),
        snippet_after=max(0, int(args.snippet_after)),
        debug=debug,
    )

    doc_map, miss_map = index_step2_entries(step2)

    entries: List[Dict[str, Any]] = []
    for key, impl in pairs:
        supp_entries = supported.get(key, [])
        supp = supported_entry_for_impl(supp_entries, impl)
        supported_meta: Dict[str, Any] = {}
        if supp is not None:
            if isinstance(supp.get("type"), str):
                supported_meta["type"] = supp["type"]
            # supported_configurations.json stores default as string or null
            if "default" in supp:
                supported_meta["default"] = supp.get("default")
            aliases = supp.get("aliases")
            if isinstance(aliases, list):
                supported_meta["aliases"] = [a for a in aliases if isinstance(a, str)]
        else:
            # If per-impl entry missing, still include any aliases from other impls.
            aliases: List[str] = []
            for e in supp_entries:
                a = e.get("aliases")
                if isinstance(a, list):
                    aliases.extend([x for x in a if isinstance(x, str)])
            if aliases:
                # stable de-dupe
                seen: set[str] = set()
                deduped: List[str] = []
                for a in aliases:
                    if a in seen:
                        continue
                    seen.add(a)
                    deduped.append(a)
                supported_meta["aliases"] = deduped

        occs = occ_by_key.get(key, [])
        parsing = parsing_hints_from_occurrences(occs)

        from_step2: Dict[str, Any] = {}
        if (key, impl) in doc_map:
            e = doc_map[(key, impl)]
            from_step2["status"] = "documented"
            if isinstance(e.get("results"), list):
                from_step2["results"] = e.get("results")
            if isinstance(e.get("missingSources"), list):
                from_step2["missingSources"] = e.get("missingSources")
        elif (key, impl) in miss_map:
            e = miss_map[(key, impl)]
            from_step2["status"] = "missing"
            if isinstance(e.get("missingReasons"), list):
                from_step2["missingReasons"] = e.get("missingReasons")

        code_obj = {
            "occurrenceCount": len(occs),
            "occurrences": [
                {
                    "term": o.term,
                    "file": o.file,
                    "line": o.line,
                    **({"func": o.func} if o.func else {}),
                    "snippet": o.snippet,
                }
                for o in occs
            ],
            "parsingHints": parsing,
        }

        entries.append(
            {
                "key": key,
                "implementation": impl,
                **({"supported": supported_meta} if supported_meta else {}),
                **({"fromStep2": from_step2} if from_step2 else {}),
                "code": code_obj,
                "terms": key_to_terms.get(key, [key]),
            }
        )

    # Stable ordering.
    entries.sort(key=lambda e: (e.get("key", ""), e.get("implementation", "")))

    output_obj: Dict[str, Any] = {
        "lang": lang,
        "input": str(input_path),
        "repoRoot": str(repo_root),
        "supportedConfigurations": str(supported_path),
        "counts": {
            "llmNeededPairs": len(entries),
        },
        "entries": entries,
    }

    atomic_write_json(output_path, output_obj)
    eprint(f"Wrote {output_path} (entries={len(entries)})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

