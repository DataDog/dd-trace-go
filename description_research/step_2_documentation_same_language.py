#!/usr/bin/env python3
"""
Step 2 - Documentation (same tracer language) (label: documentation_same_language)

This step is an *extraction step*:
- Do NOT invent or paraphrase.
- Prefer structured definitions (tables/definition lists) over nearby paragraphs.
- Output must be deterministic and reviewable (stable ordering + sourceFile with line number).

Reads:
- <input>/configurations_descriptions_step_1.json
- a local checkout of the DataDog/documentation repository

Writes:
- <output>/configurations_descriptions_step_2.json

See description_research/README_v2.md for the pipeline contract.
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


OUTPUT_FILENAME = "configurations_descriptions_step_2.json"
SOURCE_DOCS = "documentation_same_language"

# Extensions to scan in the documentation repo.
DOC_EXTENSIONS = (".md", ".mdx", ".yaml", ".yml", ".json")

# Prefer tracer/product areas first.
PASS1_SEED_DIRS = (
    "content/en/tracing",
    "content/en/serverless",
    "content/en/profiler",
    "content/en/security",
    "content/en/opentelemetry",
    # CI Visibility is often documented outside tracing; keep pass1 small but include this area if present.
    "content/en/continuous_integration",
)

# Pass 2 still should not be “scan everything”: keep within areas likely to contain env var docs.
PASS2_PATH_KEYWORDS = (
    "tracing",
    "apm",
    "agent",
    "serverless",
    "profiling",
    "profiler",
    "opentelemetry",
    "security",
    "appsec",
    "civisibility",
    "continuous_integration",
    "test",
    "otel",
)

TRACER_LANG_HINTS: Dict[str, List[str]] = {
    "golang": ["golang", "go"],
    "java": ["java"],
    "python": ["python"],
    "ruby": ["ruby"],
    "nodejs": ["nodejs", "node"],
    "dotnet": ["dotnet", "csharp"],
    "php": ["php"],
}

# When searching for config keys/aliases, avoid matching prefixes of other keys
# (e.g. DD_API_KEY inside DD_API_KEY_SECRET_ARN).
TOKEN_BOUNDARY_CLASS = r"[A-Za-z0-9_-]"


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


def normalize_text(s: str) -> str:
    s = s.strip()
    s = s.replace("\r\n", "\n").replace("\r", "\n")
    # Trim trailing spaces per line.
    lines = [ln.rstrip() for ln in s.split("\n")]
    s = "\n".join(lines).strip()

    # Strip common markdown links while keeping the visible text.
    s = re.sub(r"\[([^\]]+)\]\[[^\]]+\]", r"\1", s)
    s = re.sub(r"\[([^\]]+)\]\([^)]+\)", r"\1", s)
    # Strip inline code ticks but keep the text.
    s = s.replace("`", "")
    # Strip simple HTML tags used in docs content.
    s = re.sub(r"</?code\s*>", "", s)
    s = re.sub(r"</?a\b[^>]*>", "", s)
    s = re.sub(r"<br\s*/?>", "\n", s, flags=re.IGNORECASE)
    s = re.sub(r"</?[a-z][^>]*>", "", s)
    # Strip Hugo shortcodes.
    s = re.sub(r"\{\{<[^>]*>\}\}", "", s)
    s = re.sub(r"\{\{%[^%]*%\}\}", "", s)
    # Normalize repeated whitespace in single-line strings.
    s = re.sub(r"[ \t]+", " ", s)
    return s.strip()


def normalize_for_dedupe(s: str) -> str:
    s = normalize_text(s).lower()
    s = re.sub(r"\s+", " ", s)
    return s.strip()


def lang_hints(lang: str) -> List[str]:
    return TRACER_LANG_HINTS.get(lang, [lang])


def token_in_path(path_lower: str, token: str) -> bool:
    return re.search(rf"(?:^|[^a-z0-9]){re.escape(token)}(?:[^a-z0-9]|$)", path_lower) is not None


def path_has_any_token(path_lower: str, tokens: Iterable[str]) -> bool:
    return any(token_in_path(path_lower, t) for t in tokens)


def is_pass2_candidate_path(relpath: str) -> bool:
    pl = relpath.lower()
    return any(k in pl for k in PASS2_PATH_KEYWORDS)


def compile_union_regex(terms: Iterable[str]) -> Optional[re.Pattern[str]]:
    terms = [t for t in terms if t]
    if not terms:
        return None
    pattern = "|".join(re.escape(t) for t in sorted(set(terms)))
    return re.compile(rf"(?<!{TOKEN_BOUNDARY_CLASS})(?:{pattern})(?!{TOKEN_BOUNDARY_CLASS})")


def key_aliases_from_supported_configurations(repo_root: Path) -> Dict[str, List[str]]:
    """
    Extract aliases from internal/env/supported_configurations.json.
    """
    supported_path = repo_root / "internal/env/supported_configurations.json"
    if not supported_path.exists():
        return {}
    try:
        data = load_json(supported_path)
    except Exception:
        return {}

    supported = data.get("supportedConfigurations")
    if not isinstance(supported, dict):
        return {}

    out: Dict[str, List[str]] = {}
    for key, entries in supported.items():
        if not isinstance(key, str) or not isinstance(entries, list):
            continue
        aliases: List[str] = []
        for entry in entries:
            if not isinstance(entry, dict):
                continue
            raw = entry.get("aliases")
            if isinstance(raw, list):
                for a in raw:
                    if isinstance(a, str) and a.strip():
                        aliases.append(a.strip())
        if aliases:
            # Deduplicate stable.
            seen: set[str] = set()
            deduped: List[str] = []
            for a in aliases:
                if a in seen:
                    continue
                seen.add(a)
                deduped.append(a)
            out[key] = deduped
    return out


def build_search_terms(keys: Iterable[str], alias_map: Dict[str, List[str]]) -> Tuple[Dict[str, List[str]], Dict[str, str]]:
    """
    Returns:
    - key_to_terms: canonical key => [terms...], including key itself + alias variants
    - alias_term_to_key: alias term => canonical key
    """
    key_to_terms: Dict[str, List[str]] = {}
    alias_term_to_key: Dict[str, str] = {}
    for key in sorted(set(keys)):
        terms: List[str] = [key]
        if "_" in key:
            terms.append(key.replace("_", "-"))
        for a in alias_map.get(key, []):
            terms.append(a)
        # Deduplicate stable.
        seen: set[str] = set()
        deduped: List[str] = []
        for t in terms:
            if not t:
                continue
            if t in seen:
                continue
            seen.add(t)
            deduped.append(t)
        key_to_terms[key] = deduped
        for t in deduped[1:]:
            alias_term_to_key[t] = key
    return key_to_terms, alias_term_to_key


def list_doc_files(docs_repo: Path, roots: Sequence[str]) -> List[Path]:
    """
    Return absolute doc file paths under selected roots.
    Deterministic order: by docs-repo-relative path.
    """
    files: List[Path] = []
    lang_suffix_re = re.compile(r"\.(?P<lang>[a-z]{2})\.(md|mdx)$", re.IGNORECASE)

    for root_rel in roots:
        root = docs_repo / root_rel
        if not root.exists():
            continue
        for p in root.rglob("*"):
            if ".git" in p.parts:
                continue
            if not p.is_file():
                continue
            if p.suffix.lower() not in DOC_EXTENSIONS:
                continue

            rel = p.relative_to(docs_repo).as_posix()
            # Filter non-English shortcodes (e.g. *.fr.md, *.ja.md, ...).
            if rel.startswith("layouts/shortcodes/"):
                m = lang_suffix_re.search(p.name)
                if m is not None and m.group("lang").lower() != "en":
                    continue
            files.append(p)

    files.sort(key=lambda p: p.relative_to(docs_repo).as_posix())
    return files


@dataclass(frozen=True)
class FileMatch:
    relpath: str
    abspath: Path
    pass_id: int  # 1 or 2
    match_pos: int

    has_lang_hint: bool
    path_keyword_score: int  # lower is better

    def sort_key(self) -> Tuple[int, int, int, str, int]:
        return (
            self.pass_id,
            0 if self.has_lang_hint else 1,
            self.path_keyword_score,
            self.relpath,
            self.match_pos,
        )


@dataclass(frozen=True)
class Extracted:
    description: str
    relpath: str
    line_number: int  # 1-based
    extractor: str
    filematch: FileMatch

    def source_file_ref(self) -> str:
        return f"{self.relpath}:{self.line_number}"


def compute_path_keyword_score(relpath: str) -> int:
    pl = relpath.lower()
    # Lower is better. Start at 0, add penalties for weak paths.
    score = 0
    if "release" in pl or "changelog" in pl:
        score += 50
    # Reward for common doc areas.
    if any(x in pl for x in ("tracing", "apm", "serverless", "profil", "opentelemetry", "security", "appsec")):
        score -= 10
    return score


def scan_for_file_matches(
    files: List[Path],
    docs_repo: Path,
    lang: str,
    keys: List[str],
    key_to_terms: Dict[str, List[str]],
    alias_term_to_key: Dict[str, str],
    pass_id: int,
    *,
    max_files_per_key: int,
    debug: bool,
) -> Dict[str, List[FileMatch]]:
    """
    Scan doc files and keep bounded file match lists per canonical key.
    """
    # Canonical keys regex.
    re_keys = compile_union_regex(keys)
    # Aliases/variants regex.
    alias_terms: List[str] = []
    for k in keys:
        for t in key_to_terms.get(k, [])[1:]:
            alias_terms.append(t)
    re_alias = compile_union_regex(alias_terms)

    out: Dict[str, List[FileMatch]] = {}
    hints = lang_hints(lang)

    total = len(files)
    for idx, p in enumerate(files, start=1):
        rel = p.relative_to(docs_repo).as_posix()
        if debug and (idx == 1 or idx % 500 == 0 or idx == total):
            eprint(f"[scan pass{pass_id}] {idx}/{total} {rel}")
        try:
            content = p.read_text(encoding="utf-8", errors="replace")
        except Exception:
            continue

        pl = rel.lower()
        has_lang = path_has_any_token(pl, hints)
        path_score = compute_path_keyword_score(rel)

        # Keep first occurrence per term per file to avoid flooding.
        seen_in_file: set[str] = set()

        if re_keys is not None:
            for m in re_keys.finditer(content):
                term = m.group(0)
                if term in seen_in_file:
                    continue
                seen_in_file.add(term)
                key = term
                lst = out.setdefault(key, [])
                lst.append(
                    FileMatch(
                        relpath=rel,
                        abspath=p,
                        pass_id=pass_id,
                        match_pos=m.start(),
                        has_lang_hint=has_lang,
                        path_keyword_score=path_score,
                    )
                )

        if re_alias is not None:
            for m in re_alias.finditer(content):
                term = m.group(0)
                if term in seen_in_file:
                    continue
                seen_in_file.add(term)
                key = alias_term_to_key.get(term)
                if key is None:
                    continue
                lst = out.setdefault(key, [])
                lst.append(
                    FileMatch(
                        relpath=rel,
                        abspath=p,
                        pass_id=pass_id,
                        match_pos=m.start(),
                        has_lang_hint=has_lang,
                        path_keyword_score=path_score,
                    )
                )

    # Sort and bound deterministically.
    for k, lst in out.items():
        lst.sort(key=lambda fm: fm.sort_key())
        if len(lst) > max_files_per_key:
            del lst[max_files_per_key:]
    return out


def build_code_context_mask(lines: List[str]) -> List[bool]:
    fence_re = re.compile(r"^\s*(```|~~~)")
    highlight_start_re = re.compile(r"^\s*\{\{[<%]\s*highlight\b")
    highlight_end_re = re.compile(r"^\s*\{\{[<%]\s*/highlight\b")
    code_block_start_re = re.compile(r"^\s*\{\{[<%]\s*code-block\b")
    code_block_end_re = re.compile(r"^\s*\{\{[<%]\s*/code-block\b")

    in_fence = False
    in_highlight = False
    in_code_block = False
    mask: List[bool] = [False] * len(lines)
    for i, ln in enumerate(lines):
        if fence_re.match(ln):
            mask[i] = True
            in_fence = not in_fence
            continue
        if highlight_start_re.match(ln):
            mask[i] = True
            in_highlight = True
            continue
        if highlight_end_re.match(ln):
            mask[i] = True
            in_highlight = False
            continue
        if code_block_start_re.match(ln):
            mask[i] = True
            in_code_block = True
            continue
        if code_block_end_re.match(ln):
            mask[i] = True
            in_code_block = False
            continue
        mask[i] = in_fence or in_highlight or in_code_block
    return mask


def looks_like_heading(line: str) -> bool:
    return re.match(r"^\s*#{1,6}\s+", line) is not None


def passes_quality_bar(description: str) -> bool:
    s = normalize_text(description)
    if not s:
        return False
    lowered = s.lower()
    if lowered in {"tbd", "todo", "n/a", "na", "none"}:
        return False
    # Reject templating fragments dominating content.
    if s.count("{{") >= 2 or s.count("}}") >= 2:
        return False
    # Avoid pure exports / code-ish fragments.
    if re.search(r"^\s*(export\s+)?(DD|OTEL)_[A-Z0-9_]+\s*=", s):
        return False
    # Length: README says ~20 chars, but allow short highly-specific key definitions.
    if len(s) < 20:
        # Allow a few short-but-precise patterns.
        if re.search(r"\bapi key\b", lowered) or re.search(r"\bapp key\b", lowered):
            return True
        return False
    return True


def count_other_env_vars(text: str, key: str) -> int:
    vars_found = {m for m in re.findall(r"\b(?:DD|OTEL)_[A-Z0-9_]+\b", text) if m != key}
    return len(vars_found)


def extract_md_definition_list(lines: List[str], code_mask: List[bool], terms: List[str]) -> List[Tuple[str, int, str]]:
    """
    Extract markdown definition list items, e.g.:
      `DD_FOO`
      : description...
    Returns list of (desc, line_no, extractor_name)
    """
    out: List[Tuple[str, int, str]] = []
    term_set = set(terms)
    term_line_re = re.compile(r"^\s*`(?P<term>[^`]+)`\s*$")
    for i in range(len(lines) - 1):
        if code_mask[i] or code_mask[i + 1]:
            continue
        m = term_line_re.match(lines[i])
        if not m:
            continue
        term = m.group("term").strip()
        if term not in term_set:
            continue
        if not re.match(r"^\s*:\s*", lines[i + 1]):
            continue
        # Capture up to a bounded number of subsequent lines while they look like part of the definition.
        parts: List[str] = []
        # First line: remove leading ':'.
        first = re.sub(r"^\s*:\s*", "", lines[i + 1]).rstrip()
        if first:
            parts.append(first)
        j = i + 2
        max_lines = 10
        while j < len(lines) and len(parts) < max_lines:
            if code_mask[j]:
                break
            ln = lines[j].rstrip()
            if ln.strip() == "":
                # allow a single blank line inside, but stop on double blanks or followed by a new term/heading
                next_ln = lines[j + 1] if j + 1 < len(lines) else ""
                if looks_like_heading(next_ln) or term_line_re.match(next_ln):
                    break
                parts.append("")
                j += 1
                continue
            if looks_like_heading(ln) or term_line_re.match(ln):
                break
            # Stop if we hit a table row start; those are handled elsewhere.
            if "|" in ln and ln.strip().startswith("|"):
                break
            parts.append(ln)
            j += 1
        desc = normalize_text("\n".join(parts))
        if desc:
            out.append((desc, i + 2, "md_definition_list"))
    return out


def extract_md_inline_bullet(lines: List[str], code_mask: List[bool], terms: List[str]) -> List[Tuple[str, int, str]]:
    """
    Extract inline bullet/colon patterns:
      - `DD_FOO`: description
    """
    out: List[Tuple[str, int, str]] = []
    # Prefer longer terms first to reduce accidental partial matches.
    terms_sorted = sorted(set(terms), key=lambda t: (-len(t), t))
    # Build a regex matching one of the terms inside backticks.
    term_pat = "|".join(re.escape(t) for t in terms_sorted)
    re_inline = re.compile(rf"^\s*[-*]\s+`(?P<term>{term_pat})`\s*:\s*(?P<desc>.+?)\s*$")
    for i, ln in enumerate(lines):
        if code_mask[i]:
            continue
        m = re_inline.match(ln)
        if not m:
            continue
        desc = normalize_text(m.group("desc"))
        if desc:
            out.append((desc, i + 1, "md_inline_bullet"))
    return out


def extract_md_table_description(lines: List[str], code_mask: List[bool], terms: List[str]) -> List[Tuple[str, int, str]]:
    """
    Extract Description cell from markdown tables with a Description column.
    """
    out: List[Tuple[str, int, str]] = []
    terms_re = compile_union_regex(terms)
    if terms_re is None:
        return out

    def split_row(line: str) -> List[str]:
        return [p.strip() for p in line.strip().strip("|").split("|")]

    def looks_like_separator(line: str) -> bool:
        return re.match(r"^\s*\|?\s*:?-{3,}", line) is not None

    for i, ln in enumerate(lines):
        if code_mask[i]:
            continue
        if "|" not in ln:
            continue
        if not terms_re.search(ln):
            continue
        if looks_like_separator(ln):
            continue

        cells = split_row(ln)
        if len(cells) < 2:
            continue
        desc_idx = None
        # Find header row in a small window above.
        for j in range(i - 1, max(-1, i - 8), -1):
            if j < 0:
                break
            hdr = lines[j]
            if hdr.strip() == "":
                break
            if "|" not in hdr:
                break
            if looks_like_separator(hdr):
                continue
            hdr_cells = [c.strip().lower() for c in split_row(hdr)]
            for k, c in enumerate(hdr_cells):
                if c == "description":
                    desc_idx = k
                    break
            break
        if desc_idx is None or desc_idx >= len(cells):
            continue
        desc = normalize_text(cells[desc_idx])
        if desc:
            out.append((desc, i + 1, "md_table"))
            if len(out) >= 3:
                break
    return out


def extract_yaml_description_near_key(lines: List[str], terms: List[str]) -> List[Tuple[str, int, str]]:
    """
    YAML-ish extraction without adding dependencies:
    Look for:
      name: DD_FOO
      description: ...
    Supports block scalars: description: | / >
    """
    out: List[Tuple[str, int, str]] = []
    terms_set = set(terms)
    key_line_re = re.compile(r"^\s*(name|key)\s*:\s*['\"]?(?P<term>[A-Za-z0-9_-]+)['\"]?\s*$")
    desc_line_re = re.compile(r"^\s*(description|desc)\s*:\s*(?P<val>.*)\s*$")

    for i, ln in enumerate(lines):
        m = key_line_re.match(ln)
        if not m:
            continue
        term = m.group("term")
        if term not in terms_set:
            continue
        # Search forward a small window for description.
        for j in range(i + 1, min(len(lines), i + 30)):
            dm = desc_line_re.match(lines[j])
            if not dm:
                continue
            val = dm.group("val").rstrip()
            if val in {"|", ">", "|-", ">-"}:
                # Capture indented block.
                block: List[str] = []
                k = j + 1
                while k < len(lines):
                    l2 = lines[k]
                    if l2.strip() == "":
                        block.append("")
                        k += 1
                        continue
                    if re.match(r"^\s{2,}\S", l2) is None:
                        break
                    block.append(l2.strip())
                    k += 1
                desc = normalize_text("\n".join(block))
                if desc:
                    out.append((desc, j + 1, "yaml_description_block"))
                break
            desc = normalize_text(val.strip().strip("'\""))
            if desc:
                out.append((desc, j + 1, "yaml_description"))
            break
    return out


def extract_json_description_near_key(lines: List[str], terms: List[str]) -> List[Tuple[str, int, str]]:
    """
    JSON-ish extraction without parsing:
    Look for lines containing \"name\": \"DD_FOO\" and nearby \"description\":.
    """
    out: List[Tuple[str, int, str]] = []
    terms_set = set(terms)
    name_re = re.compile(r"\"(name|key)\"\s*:\s*\"(?P<term>[A-Za-z0-9_-]+)\"")
    desc_re = re.compile(r"\"description\"\s*:\s*\"(?P<desc>.*?)\"")
    for i, ln in enumerate(lines):
        m = name_re.search(ln)
        if not m:
            continue
        term = m.group("term")
        if term not in terms_set:
            continue
        for j in range(i, min(len(lines), i + 60)):
            dm = desc_re.search(lines[j])
            if not dm:
                continue
            desc = normalize_text(dm.group("desc"))
            if desc:
                out.append((desc, j + 1, "json_description"))
            break
    return out


def extract_prose_paragraph_near_match(lines: List[str], code_mask: List[bool], match_line_idx: int, key: str, terms: List[str]) -> Optional[Tuple[str, int, str]]:
    """
    Fallback: paragraph around a match. If the match is in code, try adjacent prose
    within a small window, even if it does not repeat the term.
    """
    # Build paragraphs separated by blank lines.
    paragraphs: List[Tuple[int, int]] = []
    i = 0
    while i < len(lines):
        while i < len(lines) and lines[i].strip() == "":
            i += 1
        if i >= len(lines):
            break
        start = i
        while i < len(lines) and lines[i].strip() != "":
            i += 1
        end = i - 1
        paragraphs.append((start, end))

    if not paragraphs:
        return None

    # Find paragraph containing the match line.
    pidx = 0
    for j, (s, e) in enumerate(paragraphs):
        if s <= match_line_idx <= e:
            pidx = j
            break

    terms_re = compile_union_regex(terms)
    def para_text(s: int, e: int) -> str:
        return "\n".join(lines[s : e + 1]).strip()

    # helper to check "prose-ness"
    def is_prose(txt: str) -> bool:
        t = txt.lower()
        return any(w in t for w in ("environment variable", "set ", "controls", "configure", "enables", "disable", "used to", "specifies"))

    # Radius search; prefer current, then adjacent. Deterministic: left before right.
    max_radius = max(pidx, len(paragraphs) - 1 - pidx)
    for r in range(0, min(max_radius, 6) + 1):
        cand_idxs: List[int] = [pidx] if r == 0 else [i for i in (pidx - r, pidx + r) if 0 <= i < len(paragraphs)]
        for idx in cand_idxs:
            s, e = paragraphs[idx]
            if any(code_mask[s : e + 1]):
                continue
            txt = para_text(s, e)
            if not txt or looks_like_heading(txt.split("\n")[0]):
                continue
            # If the match was in prose, require the term to be in the paragraph.
            if r == 0 and terms_re is not None and not terms_re.search(txt):
                continue
            # If match was in code, allow adjacent prose but require it looks explanatory.
            if r > 0 and not is_prose(txt):
                continue
            desc = normalize_text(txt)
            if count_other_env_vars(desc, key) > 6:
                # too generic / too many vars mentioned
                continue
            if desc:
                return (desc, s + 1, "prose_paragraph")
    return None


def extract_from_file_for_key(key: str, terms: List[str], fm: FileMatch, *, debug: bool) -> List[Extracted]:
    """
    Extract candidates from one file, preferring structured extractors.
    """
    try:
        content = fm.abspath.read_text(encoding="utf-8", errors="replace")
    except Exception:
        return []

    content = content.replace("\r\n", "\n").replace("\r", "\n")
    lines = content.split("\n")
    code_mask = build_code_context_mask(lines)

    extracted: List[Extracted] = []
    # Structured extractors first.
    extracted_specs: List[Tuple[str, int, str]] = []

    suffix = fm.abspath.suffix.lower()
    if suffix in {".md", ".mdx"}:
        extracted_specs.extend(extract_md_definition_list(lines, code_mask, terms))
        extracted_specs.extend(extract_md_table_description(lines, code_mask, terms))
        extracted_specs.extend(extract_md_inline_bullet(lines, code_mask, terms))
    elif suffix in {".yml", ".yaml"}:
        extracted_specs.extend(extract_yaml_description_near_key(lines, terms))
    elif suffix == ".json":
        extracted_specs.extend(extract_json_description_near_key(lines, terms))

    for desc, line_no, extractor in extracted_specs:
        desc_n = normalize_text(desc)
        if not passes_quality_bar(desc_n):
            continue
        extracted.append(Extracted(description=desc_n, relpath=fm.relpath, line_number=line_no, extractor=extractor, filematch=fm))

    # Fallback prose paragraph extraction (only if we still have nothing from structured sources).
    if not extracted:
        match_line_idx = content[: fm.match_pos].count("\n")
        prose = extract_prose_paragraph_near_match(lines, code_mask, match_line_idx, key, terms)
        if prose is not None:
            desc, line_no, extractor = prose
            if passes_quality_bar(desc):
                extracted.append(Extracted(description=desc, relpath=fm.relpath, line_number=line_no, extractor=extractor, filematch=fm))

    if debug and extracted:
        for ex in extracted[:3]:
            eprint(f"[debug] {key} -> {ex.extractor} {ex.source_file_ref()}")
    return extracted


def rank_extracted(key: str, extracted: List[Extracted]) -> List[Extracted]:
    """
    Deterministic ranking across extractors/files, favoring precision.
    """
    extractor_rank = {
        "md_definition_list": 0,
        "md_table": 1,
        "md_inline_bullet": 2,
        "yaml_description_block": 3,
        "yaml_description": 4,
        "json_description": 5,
        "prose_paragraph": 9,
    }

    def score(ex: Extracted) -> Tuple[Any, ...]:
        other_vars = count_other_env_vars(ex.description, key)
        # Prefer fewer other vars and moderately short texts.
        return (
            extractor_rank.get(ex.extractor, 50),
            ex.filematch.pass_id,
            0 if ex.filematch.has_lang_hint else 1,
            other_vars,
            len(ex.description),
            ex.filematch.path_keyword_score,
            ex.relpath,
            ex.line_number,
        )

    extracted.sort(key=score)
    # De-dupe by normalized description text, keep best sourceFile for each unique desc.
    seen_desc: set[str] = set()
    out: List[Extracted] = []
    for ex in extracted:
        k = normalize_for_dedupe(ex.description)
        if k in seen_desc:
            continue
        seen_desc.add(k)
        out.append(ex)
    return out


def main(argv: List[str]) -> int:
    parser = argparse.ArgumentParser(description="Step 2: extract configuration descriptions from docs (same tracer language)")
    parser.add_argument("--lang", required=True, help='Tracer language (example: "golang")')
    parser.add_argument(
        "--input",
        default="./result/configurations_descriptions_step_1.json",
        help="Path to configurations_descriptions_step_1.json (default: ./result/configurations_descriptions_step_1.json)",
    )
    parser.add_argument(
        "--docs-repo",
        default="./documentation",
        help="Path to local DataDog/documentation checkout (default: ./documentation)",
    )
    parser.add_argument(
        "--output",
        default="./result",
        help="Output directory where configurations_descriptions_step_2.json will be produced (default: ./result)",
    )
    parser.add_argument(
        "--max-results-per-key",
        type=int,
        default=3,
        help="Max number of extracted docs candidates to include per key (default: 3)",
    )
    parser.add_argument(
        "--max-files-per-key",
        type=int,
        default=30,
        help="Max number of matched files to consider per key per pass (default: 30)",
    )
    parser.add_argument(
        "--debug",
        action="store_true",
        help="Enable deterministic debug logs to stderr (file selection, extractor chosen, and rejections summary)",
    )
    args = parser.parse_args(argv)

    lang: str = args.lang
    input_path = Path(args.input)
    docs_repo = Path(args.docs_repo)
    output_dir = Path(args.output)
    output_path = output_dir / OUTPUT_FILENAME
    debug: bool = bool(args.debug)

    if not input_path.exists():
        eprint(f"error: input file not found: {input_path}")
        return 2
    if not docs_repo.exists():
        eprint(f"error: docs repo not found: {docs_repo}")
        return 2

    step1 = load_json(input_path)
    missing_entries = step1.get("missingConfigurations")
    documented_entries = step1.get("documentedConfigurations")
    if not isinstance(missing_entries, list) or not isinstance(documented_entries, list):
        eprint("error: input JSON does not look like step 1 output (missingConfigurations/documentedConfigurations)")
        return 2

    missing_by_key: Dict[str, List[Dict[str, Any]]] = {}
    for entry in missing_entries:
        if not isinstance(entry, dict):
            continue
        key = entry.get("key")
        if not isinstance(key, str) or not key:
            continue
        missing_by_key.setdefault(key, []).append(entry)

    keys_to_find = sorted(missing_by_key.keys())
    eprint(f"Loaded step 1 output: documented={len(documented_entries)}, missing={len(missing_entries)}")
    eprint(f"Step 2 will attempt to document {len(keys_to_find)} unique missing keys")

    repo_root = Path(__file__).resolve().parent.parent
    alias_map = key_aliases_from_supported_configurations(repo_root)
    key_to_terms, alias_term_to_key = build_search_terms(keys_to_find, alias_map)

    # Pass 1 corpus: seeded dirs + shortcodes/partials/data (but only within those dirs).
    pass1_roots: List[str] = [d for d in PASS1_SEED_DIRS if (docs_repo / d).exists()]
    # Always include reusable sources in pass1 (if present) — but they will still be ranked by path features.
    for extra in ("layouts/shortcodes", "layouts/partials", "data"):
        if (docs_repo / extra).exists():
            pass1_roots.append(extra)
    pass1_files = list_doc_files(docs_repo, pass1_roots)

    # Pass 2 corpus: broader but filtered by path keywords.
    pass2_roots = ["content/en", "layouts/shortcodes", "layouts/partials", "data"]
    all_pass2_files = list_doc_files(docs_repo, pass2_roots)
    pass2_files = [p for p in all_pass2_files if is_pass2_candidate_path(p.relative_to(docs_repo).as_posix())]

    eprint(f"Docs corpus pass1={len(pass1_files)} files, pass2={len(pass2_files)} files (filtered)")

    max_files_per_key = max(1, int(args.max_files_per_key))

    # Scan pass 1 for all keys.
    pass1_matches = scan_for_file_matches(
        pass1_files,
        docs_repo,
        lang,
        keys_to_find,
        key_to_terms,
        alias_term_to_key,
        pass_id=1,
        max_files_per_key=max_files_per_key,
        debug=debug,
    )

    # Determine which keys still need pass 2 scanning (no pass1 matches at all).
    remaining = [k for k in keys_to_find if not pass1_matches.get(k)]

    pass2_matches: Dict[str, List[FileMatch]] = {}
    if remaining:
        pass2_matches = scan_for_file_matches(
            pass2_files,
            docs_repo,
            lang,
            remaining,
            key_to_terms,
            alias_term_to_key,
            pass_id=2,
            max_files_per_key=max_files_per_key,
            debug=debug,
        )

    documented_out: List[Dict[str, Any]] = [e for e in documented_entries if isinstance(e, dict)]
    missing_out: List[Dict[str, Any]] = []

    max_results = max(1, int(args.max_results_per_key))

    for key in keys_to_find:
        terms = key_to_terms.get(key, [key])
        file_matches = (pass1_matches.get(key) or []) + (pass2_matches.get(key) or [])
        file_matches.sort(key=lambda fm: fm.sort_key())

        extracted_all: List[Extracted] = []
        for fm in file_matches:
            extracted_all.extend(extract_from_file_for_key(key, terms, fm, debug=debug))
            # Soft bound: stop early if we already have many extracted snippets.
            if len(extracted_all) >= max_results * 6:
                break

        ranked = rank_extracted(key, extracted_all)
        ranked = ranked[:max_results]

        if ranked:
            results = [
                {
                    "description": ex.description,
                    "shortDescription": "",
                    "source": SOURCE_DOCS,
                    "sourceFile": ex.source_file_ref(),
                }
                for ex in ranked
            ]

            if debug:
                chosen = ", ".join(ex.source_file_ref() for ex in ranked)
                eprint(f"[debug] {key}: selected {len(ranked)} candidates from {chosen}")

            for entry in missing_by_key[key]:
                impl = entry.get("implementation")
                if not isinstance(impl, str) or not impl:
                    continue
                missing_sources = entry.get("missingReasons")
                missing_sources_out: List[Dict[str, Any]] = []
                if isinstance(missing_sources, list):
                    for ms in missing_sources:
                        if isinstance(ms, dict) and isinstance(ms.get("source"), str) and isinstance(ms.get("reason"), str):
                            missing_sources_out.append({"source": ms["source"], "reason": ms["reason"]})

                documented_out.append(
                    {
                        "key": key,
                        "implementation": impl,
                        "results": results,
                        **({"missingSources": missing_sources_out} if missing_sources_out else {}),
                    }
                )
            continue

        # Nothing usable extracted.
        # Distinguish: not_found if no match at all; quality if matches but all candidates rejected.
        reason = "not_found" if not file_matches else "quality"
        if debug:
            eprint(f"[debug] {key}: missing ({reason}), file_matches={len(file_matches)}, extracted={len(extracted_all)}")

        for entry in missing_by_key[key]:
            impl = entry.get("implementation")
            if not isinstance(impl, str) or not impl:
                continue
            existing = entry.get("missingReasons")
            reasons: List[Dict[str, Any]] = []
            if isinstance(existing, list):
                for r in existing:
                    if isinstance(r, dict) and isinstance(r.get("source"), str) and isinstance(r.get("reason"), str):
                        reasons.append({"source": r["source"], "reason": r["reason"]})
            reasons.append({"source": SOURCE_DOCS, "reason": reason})
            missing_out.append({"key": key, "implementation": impl, "missingReasons": reasons})

    documented_out.sort(key=lambda c: (c.get("key", ""), c.get("implementation", "")))
    missing_out.sort(key=lambda c: (c.get("key", ""), c.get("implementation", "")))

    output_obj = {
        "lang": lang,
        "missingCount": len(missing_out),
        "documentedCount": len(documented_out),
        "documentedConfigurations": documented_out,
        "missingConfigurations": missing_out,
    }

    atomic_write_json(output_path, output_obj)
    eprint(f"Wrote {output_path} (documented={len(documented_out)}, missing={len(missing_out)})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

