# -*- coding: utf-8 -*-
"""Dump the UIA control tree of the running JianyingPro window.

Route-B feasibility probe: pyJianYingDraft claims Jianying 7+ hides its
controls from UIA. Before falling back to pixel matching we check what 11.1
actually exposes on this machine. Windows are located by the JianyingPro.exe
process id, not by title, so browser tabs about Jianying don't match.
"""
import io
import sys

sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding="utf-8", errors="replace")

import psutil
import uiautomation as auto


def jianying_pids():
    return {p.pid for p in psutil.process_iter(["name"]) if (p.info["name"] or "").lower().startswith("jianying")}


def dump(control, depth, max_depth, out, max_children=40):
    rect = control.BoundingRectangle
    try:
        name = control.Name or ""
    except Exception:
        name = "<unreadable>"
    if len(name) > 60:
        name = name[:60] + "…"
    out.write("  " * depth + f"{control.ControlTypeName} | name={name!r} | class={control.ClassName!r} | "
              f"automationId={control.AutomationId!r} | rect=({rect.left},{rect.top},{rect.right},{rect.bottom})\n")
    if depth >= max_depth:
        return
    children = control.GetChildren()
    for i, child in enumerate(children):
        if i >= max_children:
            out.write("  " * (depth + 1) + f"... {len(children) - max_children} more children omitted\n")
            break
        dump(child, depth + 1, max_depth, out)


def main():
    auto.SetGlobalSearchTimeout(3)
    pids = jianying_pids()
    path = sys.argv[1] if len(sys.argv) > 1 else "uia_dump.txt"
    with open(path, "w", encoding="utf-8") as out:
        out.write(f"jianying pids: {sorted(pids)}\n")
        found = False
        for w in auto.GetRootControl().GetChildren():
            if w.ProcessId in pids:
                found = True
                out.write(f"\n=== window pid={w.ProcessId}: name={(w.Name or '')!r} class={w.ClassName!r} "
                          f"automationId={w.AutomationId!r} ===\n")
                dump(w, 0, 7, out)
        if not found:
            out.write("no top-level window owned by JianyingPro.exe was found\n")


if __name__ == "__main__":
    main()
