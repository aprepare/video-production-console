# -*- coding: utf-8 -*-
"""Screenshot a JianyingPro top-level window (by automationId) to a PNG."""
import sys
import time

import mss
import mss.tools
import psutil
import uiautomation as auto


def find_window(automation_id=None):
    pids = {p.pid for p in psutil.process_iter(["name"]) if (p.info["name"] or "").lower().startswith("jianying")}
    for w in auto.GetRootControl().GetChildren():
        if w.ProcessId in pids and (automation_id is None or w.AutomationId == automation_id):
            return w
    return None


def main():
    out_path = sys.argv[1]
    automation_id = sys.argv[2] if len(sys.argv) > 2 else None
    auto.SetGlobalSearchTimeout(3)
    w = find_window(automation_id)
    if w is None:
        print("WINDOW NOT FOUND")
        sys.exit(1)
    w.SetActive()
    time.sleep(0.8)
    r = w.BoundingRectangle
    with mss.mss() as sct:
        img = sct.grab({"left": r.left, "top": r.top, "width": r.right - r.left, "height": r.bottom - r.top})
        mss.tools.to_png(img.rgb, img.size, output=out_path)
    print(f"OK {out_path} rect=({r.left},{r.top},{r.right},{r.bottom}) automationId={w.AutomationId}")


if __name__ == "__main__":
    main()
