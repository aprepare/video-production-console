# -*- coding: utf-8 -*-
"""把素材录入页导出的 hook_library_raw.json 合并进正式素材库。

用法（在本目录）：
    python sync-hook-library.py            # 自动去 Chrome 下载目录找 hook_library_raw.json
    python sync-hook-library.py 路径.json  # 指定导出文件

合并规则：按条目 id 去重，只追加新条目；已拆解过的老条目（mechanism 已回填）
不会被覆盖。新条目的 hook_type / mechanism / reusable_pattern 留空即可——
爆款模式库节点运行时会自己从例句提炼机制，不影响使用。
"""
import json
import sys
from pathlib import Path

# 2026-09-07 起本脚本与录入页一起放在 tools/hook-library/，仓库根目录是上两级。
LIBRARY = Path(__file__).resolve().parents[2] / "video-console-data/remix_lab/hook_library.json"


def find_raw():
    if len(sys.argv) > 1:
        return Path(sys.argv[1])
    # Chrome 对重名下载会存成 hook_library_raw (1).json，这里在各候选目录里
    # 收集所有匹配文件，取修改时间最新的一份。
    dirs = [Path.home() / "Downloads"]
    try:
        prefs_path = Path.home() / "AppData/Local/Google/Chrome/User Data/Default/Preferences"
        prefs = json.loads(prefs_path.read_text(encoding="utf-8"))
        dl = prefs.get("download", {}).get("default_directory")
        if dl:
            dirs.insert(0, Path(dl))
    except Exception:
        pass
    found = []
    for d in dirs:
        if d.is_dir():
            found.extend(d.glob("hook_library_raw*.json"))
    if not found:
        sys.exit("找不到 hook_library_raw*.json，请在录入页点「导出 JSON」，或把文件路径作为参数传入")
    return max(found, key=lambda p: p.stat().st_mtime)


def main():
    raw_path = find_raw()
    raw = json.loads(raw_path.read_text(encoding="utf-8"))
    incoming = raw["entries"] if isinstance(raw, dict) else raw
    lib = json.loads(LIBRARY.read_text(encoding="utf-8"))
    known = {e["id"] for e in lib["entries"]}

    added = 0
    for entry in incoming:
        if not entry.get("text") or entry.get("id") in known:
            continue
        lib["entries"].append({
            "id": entry["id"],
            "slot": entry.get("slot", "mid"),
            "source": entry.get("source", ""),
            "text": entry["text"],
            "likes_wan": entry.get("likes_wan"),
            "views_wan": entry.get("views_wan"),
            "note": entry.get("note", ""),
            "hook_type": entry.get("hook_type", ""),
            "mechanism": entry.get("mechanism", ""),
            "reusable_pattern": entry.get("reusable_pattern", ""),
        })
        known.add(entry["id"])
        added += 1

    from datetime import date
    lib["updated_at"] = date.today().isoformat()
    LIBRARY.write_text(json.dumps(lib, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    write_slim(lib)
    print(f"来源：{raw_path}")
    print(f"新增 {added} 条，素材库现有 {len(lib['entries'])} 条 → {LIBRARY}")
    print("无需重启控制台，下次运行自动读最新素材库。")


SLIM = LIBRARY.with_name("hook_library.slim.json")
EXAMPLE_HEAD = 80


def write_slim(lib):
    """给模式库节点吃的瘦身版：例句只留开头 80 字做风格参考，输入量砍掉八成，
    节点从几分钟降到几十秒。完整例句仍在 hook_library.json 里供人工对照。"""
    slim = {
        "format": "hook_library_slim",
        "usage_rule": lib.get("usage_rule", ""),
        "entries": [
            {
                "id": e["id"],
                "slot": e["slot"],
                "source": e.get("source", ""),
                "hook_type": e.get("hook_type", ""),
                "mechanism": e.get("mechanism", ""),
                "reusable_pattern": e.get("reusable_pattern", ""),
                "example_head": (e.get("text") or "")[:EXAMPLE_HEAD],
            }
            for e in lib["entries"]
        ],
    }
    SLIM.write_text(json.dumps(slim, ensure_ascii=False, separators=(",", ":")) + "\n", encoding="utf-8")


if __name__ == "__main__" and len(sys.argv) > 1 and sys.argv[1] == "--slim-only":
    write_slim(json.loads(LIBRARY.read_text(encoding="utf-8")))
    print(f"已重建 {SLIM}")
    sys.exit(0)


if __name__ == "__main__":
    main()
