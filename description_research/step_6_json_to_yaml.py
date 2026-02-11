#!/usr/bin/env python3
"""
Convert a JSON file (like description_research/final/merged.json) into YAML,
using YAML block scalars for multiline strings.

Primary goal: make long description text readable in YAML and avoid seeing
escaped "\\n" sequences (multiline strings are emitted with "|" blocks).

This script will prefer ruamel.yaml when available (best control over scalar
styles), and will fall back to PyYAML if ruamel isn't installed.

By default it also converts literal "\\n" sequences into real newlines, so that
descriptions dumped from JSON as backslash+n become readable YAML block scalars.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any, Optional, Type


def _die(message: str, *, exit_code: int = 2) -> "None":
    print(f"error: {message}", file=sys.stderr)
    raise SystemExit(exit_code)


def _read_text(path: Path) -> str:
    try:
        return path.read_text(encoding="utf-8")
    except OSError as e:
        _die(f"unable to read {path}: {e}")


def _load_json(path: Path) -> Any:
    raw = _read_text(path)
    try:
        return json.loads(raw)
    except json.JSONDecodeError as e:
        _die(f"{path}: invalid JSON: {e}")


def _normalize_string(
    s: str, *, unescape_literal_backslash_n: bool
) -> str:
    # Normalize Windows newlines to keep YAML output stable.
    if "\r\n" in s:
        s = s.replace("\r\n", "\n")

    # Optional: convert literal "\n" sequences into real newlines.
    # Enabled by default to avoid seeing "\n" in YAML output.
    if unescape_literal_backslash_n and ("\n" not in s) and ("\\n" in s):
        s = s.replace("\\n", "\n")

    return s


def _to_yaml_data_ruamel(
    obj: Any,
    *,
    fold_long_single_line: bool,
    fold_width: int,
    unescape_literal_backslash_n: bool,
    _ruamel: Any,
) -> Any:
    CommentedMap = _ruamel["CommentedMap"]
    CommentedSeq = _ruamel["CommentedSeq"]
    LiteralScalarString = _ruamel["LiteralScalarString"]
    FoldedScalarString = _ruamel["FoldedScalarString"]

    if isinstance(obj, dict):
        m = CommentedMap()
        for k, v in obj.items():
            # JSON object keys must be strings; keep them as-is.
            m[k] = _to_yaml_data(
                v,
                fold_long_single_line=fold_long_single_line,
                fold_width=fold_width,
                unescape_literal_backslash_n=unescape_literal_backslash_n,
                _ruamel=_ruamel,
            )
        return m

    if isinstance(obj, list):
        seq = CommentedSeq()
        for v in obj:
            seq.append(
                _to_yaml_data_ruamel(
                    v,
                    fold_long_single_line=fold_long_single_line,
                    fold_width=fold_width,
                    unescape_literal_backslash_n=unescape_literal_backslash_n,
                    _ruamel=_ruamel,
                )
            )
        return seq

    if isinstance(obj, str):
        s = _normalize_string(s=obj, unescape_literal_backslash_n=unescape_literal_backslash_n)

        if "\n" in s:
            # Literal block keeps newlines intact and avoids \n escapes.
            return LiteralScalarString(s)

        if fold_long_single_line and (len(s) >= fold_width) and (" " in s):
            # Folded block improves readability for very long single-line strings.
            # (It should round-trip back to the same string content.)
            return FoldedScalarString(s)

        return s

    # numbers, booleans, null
    return obj


def _to_yaml_data_plain(
    obj: Any, *, unescape_literal_backslash_n: bool
) -> Any:
    if isinstance(obj, dict):
        return {
            k: _to_yaml_data_plain(v, unescape_literal_backslash_n=unescape_literal_backslash_n)
            for k, v in obj.items()
        }
    if isinstance(obj, list):
        return [
            _to_yaml_data_plain(v, unescape_literal_backslash_n=unescape_literal_backslash_n)
            for v in obj
        ]
    if isinstance(obj, str):
        return _normalize_string(
            s=obj, unescape_literal_backslash_n=unescape_literal_backslash_n
        )
    return obj


def _try_import_ruamel() -> Optional[Any]:
    try:
        from ruamel.yaml import YAML  # type: ignore[import-not-found]
        from ruamel.yaml.comments import (  # type: ignore[import-not-found]
            CommentedMap,
            CommentedSeq,
        )
        from ruamel.yaml.scalarstring import (  # type: ignore[import-not-found]
            FoldedScalarString,
            LiteralScalarString,
        )
    except Exception:
        return None

    return {
        "YAML": YAML,
        "CommentedMap": CommentedMap,
        "CommentedSeq": CommentedSeq,
        "LiteralScalarString": LiteralScalarString,
        "FoldedScalarString": FoldedScalarString,
    }


def _write_yaml_ruamel(
    data: Any,
    *,
    output_path: Optional[Path],
    width: int,
    indent_mapping: int,
    indent_sequence: int,
    indent_offset: int,
    _ruamel: Any,
) -> None:
    YAML = _ruamel["YAML"]

    yaml = YAML(typ="rt")  # round-trip emitter; we control scalar styles
    yaml.allow_unicode = True
    yaml.default_flow_style = False
    yaml.width = width
    yaml.indent(mapping=indent_mapping, sequence=indent_sequence, offset=indent_offset)

    if output_path is None:
        yaml.dump(data, sys.stdout)
        return

    try:
        with output_path.open("w", encoding="utf-8") as f:
            yaml.dump(data, f)
    except OSError as e:
        _die(f"unable to write {output_path}: {e}")


def _make_pyyaml_dumper(
    *,
    fold_long_single_line: bool,
    fold_width: int,
) -> Type[Any]:
    import yaml  # PyYAML

    class Dumper(yaml.SafeDumper):
        pass

    def _repr_str(dumper: Any, value: str) -> Any:
        if "\n" in value:
            return dumper.represent_scalar(
                "tag:yaml.org,2002:str", value, style="|"
            )
        if fold_long_single_line and (len(value) >= fold_width) and (" " in value):
            return dumper.represent_scalar(
                "tag:yaml.org,2002:str", value, style=">"
            )
        # Preserve PyYAML's default quoting/escaping decisions.
        return yaml.SafeDumper.represent_str(dumper, value)

    Dumper.add_representer(str, _repr_str)
    return Dumper


def _write_yaml_pyyaml(
    data: Any,
    *,
    output_path: Optional[Path],
    width: int,
    indent: int,
    fold_long_single_line: bool,
    fold_width: int,
) -> None:
    import yaml  # PyYAML

    dumper = _make_pyyaml_dumper(
        fold_long_single_line=fold_long_single_line,
        fold_width=fold_width,
    )

    dumped = yaml.dump(
        data,
        Dumper=dumper,
        sort_keys=False,
        allow_unicode=True,
        default_flow_style=False,
        indent=indent,
        width=width,
    )

    if output_path is None:
        sys.stdout.write(dumped)
        return

    try:
        output_path.write_text(dumped, encoding="utf-8")
    except OSError as e:
        _die(f"unable to write {output_path}: {e}")


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(
        description="Convert JSON to YAML, using block scalars for multiline strings."
    )
    parser.add_argument(
        "--input",
        default=str(Path("description_research/final/merged.json")),
        help="Input JSON path (default: description_research/final/merged.json)",
    )
    parser.add_argument(
        "--output",
        default=str(Path("description_research/final/merged.yaml")),
        help="Output YAML path, or '-' for stdout (default: description_research/final/merged.yaml)",
    )
    parser.add_argument(
        "--width",
        type=int,
        default=110,
        help="Preferred YAML line width (default: 110)",
    )
    parser.add_argument(
        "--fold-long-single-line",
        action="store_true",
        help="Use folded scalars ('>') for very long single-line strings.",
    )
    parser.add_argument(
        "--fold-width",
        type=int,
        default=140,
        help="Minimum string length to consider folding when --fold-long-single-line is set (default: 140)",
    )
    parser.add_argument(
        "--keep-literal-backslash-n",
        action="store_true",
        help=r"Do not convert literal '\n' sequences into real newlines.",
    )
    # Backwards/compat: kept as an alias. (Unescaping is the default.)
    parser.add_argument(
        "--unescape-literal-backslash-n",
        action="store_true",
        help=argparse.SUPPRESS,
    )
    parser.add_argument(
        "--indent-mapping",
        type=int,
        default=2,
        help="Mapping indentation (default: 2)",
    )
    parser.add_argument(
        "--indent-sequence",
        type=int,
        default=4,
        help="Sequence indentation (default: 4)",
    )
    parser.add_argument(
        "--indent-offset",
        type=int,
        default=2,
        help="Sequence dash offset indentation (default: 2)",
    )

    args = parser.parse_args(argv)

    input_path = Path(args.input)
    output_arg = args.output
    output_path: Optional[Path]
    if output_arg.strip() == "-":
        output_path = None
    else:
        output_path = Path(output_arg)

    json_data = _load_json(input_path)
    unescape_literal_backslash_n = not bool(args.keep_literal_backslash_n)

    _ruamel = _try_import_ruamel()
    if _ruamel is not None:
        yaml_data = _to_yaml_data_ruamel(
            json_data,
            fold_long_single_line=bool(args.fold_long_single_line),
            fold_width=int(args.fold_width),
            unescape_literal_backslash_n=unescape_literal_backslash_n,
            _ruamel=_ruamel,
        )
        _write_yaml_ruamel(
            yaml_data,
            output_path=output_path,
            width=int(args.width),
            indent_mapping=int(args.indent_mapping),
            indent_sequence=int(args.indent_sequence),
            indent_offset=int(args.indent_offset),
            _ruamel=_ruamel,
        )
    else:
        yaml_data = _to_yaml_data_plain(
            json_data,
            unescape_literal_backslash_n=unescape_literal_backslash_n,
        )
        _write_yaml_pyyaml(
            yaml_data,
            output_path=output_path,
            width=int(args.width),
            indent=int(args.indent_mapping),
            fold_long_single_line=bool(args.fold_long_single_line),
            fold_width=int(args.fold_width),
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

