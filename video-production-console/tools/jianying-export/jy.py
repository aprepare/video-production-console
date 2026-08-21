# -*- coding: utf-8 -*-
"""Shared helpers for driving JianyingPro 11.x by window handles + pixels.

Jianying 7+ exposes nothing inside its Qt Quick windows to UIA, so automation
works at two levels only: top-level windows (automationId like HomeWindow) and
raw pixels (OpenCV template matching + synthetic mouse/keyboard).
"""
import time

import cv2
import mss
import numpy as np
import psutil
import uiautomation as auto


def jianying_pids():
    return {p.pid for p in psutil.process_iter(["name"]) if (p.info["name"] or "").lower().startswith("jianying")}


def list_windows():
    pids = jianying_pids()
    return [w for w in auto.GetRootControl().GetChildren() if w.ProcessId in pids]


def find_window(automation_id, timeout=10):
    deadline = time.time() + timeout
    while time.time() < deadline:
        for w in list_windows():
            if w.AutomationId == automation_id:
                return w
        time.sleep(0.5)
    return None


def grab(window):
    r = window.BoundingRectangle
    with mss.mss() as sct:
        img = sct.grab({"left": r.left, "top": r.top, "width": r.right - r.left, "height": r.bottom - r.top})
    # mss frames are BGRA; slicing to BGR matches cv2's channel order directly.
    frame = np.array(img, dtype=np.uint8)[:, :, :3].copy()
    return frame, r


def match(frame, template_path, threshold=0.85):
    # cv2.imread cannot open non-ASCII (Chinese) paths on Windows; decode from
    # bytes instead.
    template = cv2.imdecode(np.fromfile(template_path, dtype=np.uint8), cv2.IMREAD_COLOR)
    if template is None:
        raise FileNotFoundError(template_path)
    result = cv2.matchTemplate(frame, template, cv2.TM_CCOEFF_NORMED)
    _, score, _, loc = cv2.minMaxLoc(result)
    h, w = template.shape[:2]
    center = (loc[0] + w // 2, loc[1] + h // 2)
    return score >= threshold, score, center


def click_at(window_rect, rel_x, rel_y, double=False):
    x, y = window_rect.left + int(rel_x), window_rect.top + int(rel_y)
    if double:
        # auto.Click sleeps between calls, exceeding the system double-click
        # window; raw mouse events keep the two clicks ~60ms apart.
        import ctypes
        ctypes.windll.user32.SetCursorPos(x, y)
        time.sleep(0.15)
        for _ in range(2):
            ctypes.windll.user32.mouse_event(0x0002, 0, 0, 0, 0)  # left down
            ctypes.windll.user32.mouse_event(0x0004, 0, 0, 0, 0)  # left up
            time.sleep(0.06)
        return
    auto.Click(x, y)


def save_png(frame, path):
    # cv2.imwrite silently fails on non-ASCII paths on Windows.
    ok, buf = cv2.imencode(".png", frame)
    if ok:
        buf.tofile(path)
    return ok


def paste_text(text):
    auto.SetClipboardText(text)
    time.sleep(0.2)
    auto.SendKeys("{Ctrl}v", waitTime=0.3)
