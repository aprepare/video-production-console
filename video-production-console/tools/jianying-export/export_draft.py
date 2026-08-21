# -*- coding: utf-8 -*-
"""Export a Jianying 11.x draft to MP4 by driving the app's UI.

Usage:
    python export_draft.py <草稿名称> [--export-dir DIR] [--timeout-min N]

How it works (Jianying 7+ hides every control from UIA, so this drives the
app at window + pixel level; templates were captured on this machine at the
current theme/scale and live next to this script):

1. On the home window, click the 本地草稿 search icon, paste the draft name,
   press Enter, and double-click the first (only) result row.
   If the editor is already open (automationId=MainWindow), this step is
   skipped and the export starts on whatever draft is loaded.
2. Press Ctrl+E, template-match the blue 导出 button, click it. The export
   path/format/resolution are whatever Jianying remembered from the last
   manual export.
3. Watch the export directory for the draft's mp4 and wait until its size is
   stable, then close the 导出成功 dialog via the 关闭 button.

Caveats (accepted for this route): the mouse/keyboard are busy during the
run, Jianying UI updates can break the templates, and unexpected popups
(update/VIP prompts) are not handled.
"""
import argparse
import os
import sys
import time

import uiautomation as auto

from jy import find_window, grab, match, click_at, paste_text, save_png

HERE = os.path.dirname(os.path.abspath(__file__))
TPL_SEARCH = os.path.join(HERE, "tpl_search.png")
TPL_SEARCH_EXPANDED = os.path.join(HERE, "tpl_search_expanded.png")
TPL_EXPORT = os.path.join(HERE, "tpl_export_btn.png")
TPL_CLOSE = os.path.join(HERE, "tpl_close_btn.png")
DEFAULT_EXPORT_DIR = "E:/B\u7ad9\u7d20\u6750/\u6210\u7247"  # E:/B站素材/成片


def log(message):
    print(message, flush=True)


def fail(window, message, tag):
    if window is not None:
        frame, _ = grab(window)
        save_png(frame, os.path.join(HERE, f"fail_{tag}.png"))
    log(f"FAILED: {message}")
    sys.exit(1)


def open_draft(name):
    home = find_window("HomeWindow", timeout=5)
    if home is None:
        fail(None, "剪映首页窗口不存在（请先打开剪映并停在首页）", "no_home")
    home.SetActive()
    time.sleep(0.8)
    frame, rect = grab(home)
    # The search control has two states: a collapsed magnifier icon, or an
    # expanded input box (left over from a previous search, magnifier at its
    # left edge). Either way we end with a focused input, then overwrite it.
    ok, score, center = match(frame, TPL_SEARCH)
    if not ok:
        ok, score2, center = match(frame, TPL_SEARCH_EXPANDED)
        if not ok:
            fail(home, f"未找到本地草稿搜索图标（collapsed={score:.2f} expanded={score2:.2f}）", "search")
        center = (center[0] + 80, center[1])  # click inside the input, not the icon
    click_at(rect, center[0], center[1])
    time.sleep(0.8)
    auto.SendKeys("{Ctrl}a", waitTime=0.2)
    paste_text(name)
    auto.SendKeys("{Enter}", waitTime=0.3)
    time.sleep(1.5)
    # First result row: fixed offset in the 11.1 list view. Double-click the
    # THUMBNAIL, not the name text — double-clicking the name of an
    # already-selected row starts an inline rename instead of opening.
    width, height = rect.right - rect.left, rect.bottom - rect.top
    click_at(home.BoundingRectangle, width * 0.225, height * 0.795, double=True)
    log("已双击草稿，等待编辑器加载…")
    editor = find_window("MainWindow", timeout=90)
    if editor is None:
        fail(find_window("HomeWindow", timeout=2), "编辑器窗口未出现（草稿名是否匹配到了结果？）", "no_editor")
    time.sleep(8)  # media/timeline load
    return editor


def start_export(editor):
    editor.SetActive()
    time.sleep(1.0)
    auto.SendKeys("{Ctrl}e", waitTime=0.5)
    deadline = time.time() + 30
    while time.time() < deadline:
        time.sleep(2)
        frame, rect = grab(editor)
        ok, score, center = match(frame, TPL_EXPORT)
        if ok:
            click_at(rect, center[0], center[1])
            log(f"已点击导出按钮（score={score:.2f}），开始渲染…")
            return
    fail(editor, "导出面板里未找到导出按钮", "export_btn")


def wait_export(name, export_dir, timeout_min):
    target = os.path.join(export_dir, f"{name}.mp4")
    started = time.time()
    stable, last_size = 0, -1
    while time.time() - started < timeout_min * 60:
        time.sleep(10)
        if not os.path.exists(target):
            log("  尚未生成 mp4 …")
            continue
        size = os.path.getsize(target)
        log(f"  {os.path.basename(target)}: {size / 1e6:.1f} MB")
        if size == last_size and size > 0:
            stable += 1
        else:
            stable = 0
        last_size = size
        if stable >= 3:
            return target
    return None


def close_success_dialog(editor):
    editor.SetActive()
    time.sleep(0.8)
    frame, rect = grab(editor)
    ok, score, center = match(frame, TPL_CLOSE)
    if ok:
        click_at(rect, center[0], center[1])
        log("已关闭导出成功弹窗。")
    else:
        log(f"未找到关闭按钮（score={score:.2f}），弹窗可能需要手动关闭。")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("draft_name")
    parser.add_argument("--export-dir", default=DEFAULT_EXPORT_DIR)
    parser.add_argument("--timeout-min", type=int, default=30)
    args = parser.parse_args()

    auto.SetGlobalSearchTimeout(3)
    editor = find_window("MainWindow", timeout=2)
    if editor is not None:
        log("编辑器已打开，直接在当前草稿上导出。")
    else:
        editor = open_draft(args.draft_name)

    start_export(editor)
    result = wait_export(args.draft_name, args.export_dir, args.timeout_min)
    if result is None:
        fail(find_window("MainWindow", timeout=2), "等待导出超时", "timeout")
    close_success_dialog(find_window("MainWindow", timeout=5))
    log(f"DONE: {result} ({os.path.getsize(result) / 1e6:.1f} MB)")


if __name__ == "__main__":
    main()
