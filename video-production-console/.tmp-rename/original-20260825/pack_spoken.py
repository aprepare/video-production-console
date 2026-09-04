# -*- coding: utf-8 -*-
from __future__ import annotations
import json, re
from pathlib import Path

BAD_START = set("的了是和在而但就把被让从对把")


def cjk_count(text: str) -> int:
    return sum(1 for ch in text if "\u4e00" <= ch <= "\u9fff")


def semantic_lines(text: str) -> list[str]:
    """Split by clause, then pack into <=9 CJK lines without breaking numbers."""
    text = re.sub(r"\s+", "", text)
    clauses = [p for p in re.split(r"(?<=[，。！？；])", text) if p]
    lines: list[str] = []
    for clause in clauses:
        if cjk_count(clause) <= 9:
            lines.append(clause)
            continue
        buf = ""
        i = 0
        while i < len(clause):
            ch = clause[i]
            # keep number+unit together
            m = re.match(r"\d+(?:\.\d+)?(?:万|亿|元|块|年|月|日|天|个|期|%|％)?", clause[i:])
            token = m.group(0) if m else ch
            cand = buf + token
            if cjk_count(cand) <= 9:
                buf = cand
                i += len(token)
                continue
            if buf:
                lines.append(buf)
                buf = ""
            else:
                buf = token
                i += len(token)
        if buf:
            if lines and cjk_count(lines[-1] + buf) <= 9:
                lines[-1] += buf
            else:
                lines.append(buf)
    # merge singles / bad starters
    out: list[str] = []
    for ln in lines:
        if not ln:
            continue
        if out and ln[0] in BAD_START and cjk_count(out[-1] + ln) <= 9:
            out[-1] += ln
            continue
        if out and ln[0] in BAD_START and cjk_count(out[-1] + ln[0]) <= 9:
            out[-1] += ln[0]
            ln = ln[1:]
            if not ln:
                continue
        if out and cjk_count(ln) <= 1 and cjk_count(out[-1] + ln) <= 9:
            out[-1] += ln
            continue
        out.append(ln)
    return out


def validate(lines, source):
    src = re.sub(r"\s+", "", source)
    return {
        "chars": cjk_count(source),
        "lines": len(lines),
        "max": max((cjk_count(x) for x in lines), default=0),
        "lossless": "".join(lines) == src,
        "overs": [(i+1, ln, cjk_count(ln)) for i, ln in enumerate(lines) if cjk_count(ln) > 9][:8],
        "starters": [(i+1, ln) for i, ln in enumerate(lines) if ln[:1] in BAD_START][:8],
        "singles": [(i+1, ln) for i, ln in enumerate(lines) if cjk_count(ln) <= 1][:8],
    }


if __name__ == "__main__":
    d = Path(__file__).resolve().parent
    report = {}
    for p in sorted(d.glob("0*.txt")):
        if "口播" in p.name:
            continue
        script = p.read_text(encoding="utf-8")
        lines = semantic_lines(script)
        info = validate(lines, script)
        report[p.stem] = info
        p.with_name(p.stem + "-口播.txt").write_text("\n".join(lines) + "\n", encoding="utf-8")
        print(p.stem, info)
    (d / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
