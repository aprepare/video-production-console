"""给 AI 短片的每套画风出一张参考图，供前端画风选择器当缩略图。

同一个测试场景、同一条生产提示词链路（控制台 /api/ai-shorts/style-preview-prompts），
每套画风各生成一张 9:16 图：原图存进参考库，缩略图写到 web/public/style-previews/<key>.jpg，
之后 `npm --prefix web run build:embed` + `go build` 就进了控制台。

用法（控制台已启动，仓库根目录）：
    go run ./cmd/mintsession            # 写 .tmp_console_cookies.txt
    python scripts/ai-shorts/render_style_previews.py --relay-base http://host:port/v1 --relay-key KEY
可选：--only key1,key2 只重出几套；--console http://127.0.0.1:2030；--model grok-imagine-image-2.0；
      --out 二创工作区/参考库/画面测试/画风参考图；--thumb-dir web/public/style-previews
"""
from __future__ import annotations

import argparse
import base64
import concurrent.futures as cf
import io
import json
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

from PIL import Image

ROOT = Path(__file__).resolve().parents[2]
THUMB_SIZE = (480, 853)  # 9:16，前端卡片里显示 ~110px 宽，480 足够清楚又不撑大 exe
# 静物画风不能出现人脸，用没有人的场景；其余画风共用默认场景（由控制台给）。
SCENE_OVERRIDES = {
    "macro_money": "旧木桌上摊着几张存折、两张银行卡、一小叠人民币和一副老花镜，旁边一杯冒热气的茶，窗光从侧面照进来",
}


def load_cookie(path: Path) -> str:
    pairs = []
    for line in path.read_text(encoding="utf-8").splitlines():
        if not line or line.startswith("#"):
            continue
        parts = line.split("\t")
        if len(parts) >= 7:
            pairs.append(f"{parts[5]}={parts[6]}")
    return "; ".join(pairs)


def console_get(base: str, path: str, cookie: str) -> dict:
    req = urllib.request.Request(base.rstrip("/") + path, headers={"Cookie": cookie})
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.loads(resp.read().decode("utf-8"))


def generate(relay_base: str, relay_key: str, model: str, prompt: str, tries: int = 3) -> bytes:
    body = json.dumps({"model": model, "prompt": prompt, "n": 1, "size": "1152x2048", "aspect_ratio": "9:16"}).encode("utf-8")
    last: Exception | None = None
    for attempt in range(1, tries + 1):
        req = urllib.request.Request(
            relay_base.rstrip("/") + "/images/generations", data=body, method="POST",
            headers={"Authorization": f"Bearer {relay_key}", "Content-Type": "application/json"},
        )
        try:
            with urllib.request.urlopen(req, timeout=300) as resp:
                data = json.loads(resp.read().decode("utf-8"))
            item = (data.get("data") or [{}])[0]
            if item.get("b64_json"):
                return base64.b64decode(item["b64_json"])
            if item.get("url"):
                with urllib.request.urlopen(item["url"], timeout=120) as img:
                    return img.read()
            raise RuntimeError(f"响应里没有图片: {str(data)[:200]}")
        except (urllib.error.URLError, urllib.error.HTTPError, RuntimeError, TimeoutError) as exc:  # noqa: PERF203
            last = exc
            time.sleep(3 * attempt)
    raise RuntimeError(f"生图失败: {last}")


def to_thumb(raw: bytes) -> Image.Image:
    img = Image.open(io.BytesIO(raw)).convert("RGB")
    # 先按 9:16 居中裁，再缩到缩略图尺寸；模型偶尔回非 9:16 的图也能对齐卡片。
    w, h = img.size
    target = THUMB_SIZE[0] / THUMB_SIZE[1]
    if w / h > target:
        nw = int(h * target)
        img = img.crop(((w - nw) // 2, 0, (w - nw) // 2 + nw, h))
    else:
        nh = int(w / target)
        img = img.crop((0, (h - nh) // 2, w, (h - nh) // 2 + nh))
    return img.resize(THUMB_SIZE, Image.LANCZOS)


def composite(left: Image.Image, right: Image.Image) -> Image.Image:
    """混合策略没有自己的画风：左半纸张拼贴、右半微缩模型，中间一条米白分隔。"""
    w, h = THUMB_SIZE
    out = Image.new("RGB", (w, h), (245, 240, 230))
    half = (w - 6) // 2
    out.paste(left.crop((left.width // 2 - half // 2, 0, left.width // 2 - half // 2 + half, h)), (0, 0))
    out.paste(right.crop((right.width // 2 - half // 2, 0, right.width // 2 - half // 2 + half, h)), (w - half, 0))
    return out


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--console", default="http://127.0.0.1:2030")
    ap.add_argument("--cookies", default=str(ROOT / ".tmp_console_cookies.txt"))
    ap.add_argument("--relay-base", required=True)
    ap.add_argument("--relay-key", required=True)
    ap.add_argument("--model", default="grok-imagine-image-2.0")
    ap.add_argument("--only", default="", help="逗号分隔的 key，只重出这些")
    ap.add_argument("--out", default=str(ROOT / "二创工作区" / "参考库" / "画面测试" / "画风参考图"))
    ap.add_argument("--thumb-dir", default=str(ROOT / "web" / "public" / "style-previews"))
    ap.add_argument("--workers", type=int, default=3)
    args = ap.parse_args()

    cookie = load_cookie(Path(args.cookies))
    styles = console_get(args.console, "/api/ai-shorts/style-preview-prompts", cookie)["items"]
    prompts = {s["key"]: s["prompt"] for s in styles}
    names = {s["key"]: s["name"] for s in styles}
    for key, scene in SCENE_OVERRIDES.items():
        if key in prompts:
            prompts[key] = console_get(args.console, f"/api/ai-shorts/style-preview-prompts?scene={urllib.request.quote(scene)}", cookie)
            prompts[key] = next(s["prompt"] for s in prompts[key]["items"] if s["key"] == key)

    only = {k.strip() for k in args.only.split(",") if k.strip()}
    todo = [k for k in prompts if k != "finance_editorial" and (not only or k in only)]
    out_dir, thumb_dir = Path(args.out), Path(args.thumb_dir)
    out_dir.mkdir(parents=True, exist_ok=True)
    thumb_dir.mkdir(parents=True, exist_ok=True)
    (out_dir / "提示词.json").write_text(json.dumps({k: prompts[k] for k in prompts}, ensure_ascii=False, indent=2), encoding="utf-8")

    def work(key: str) -> tuple[str, str]:
        started = time.time()
        raw = generate(args.relay_base, args.relay_key, args.model, prompts[key])
        # 参考库存 1080p 就够看（模型原图 6MB 一张，15 张就 100MB，进仓库太重）。
        full = Image.open(io.BytesIO(raw)).convert("RGB")
        if full.height > 1920:
            full = full.resize((round(full.width * 1920 / full.height), 1920), Image.LANCZOS)
        full.save(out_dir / f"{key}.jpg", "JPEG", quality=85, optimize=True)
        to_thumb(raw).save(thumb_dir / f"{key}.jpg", "JPEG", quality=82, optimize=True)
        return key, f"{names[key]} {time.time() - started:.0f}s"

    failed: list[str] = []
    with cf.ThreadPoolExecutor(max_workers=args.workers) as pool:
        for fut in cf.as_completed({pool.submit(work, k): k for k in todo}):
            try:
                key, msg = fut.result()
                print(f"ok   {key:16s} {msg}", flush=True)
            except Exception as exc:  # noqa: BLE001
                failed.append(str(exc))
                print(f"FAIL {exc}", flush=True)

    pc, mi = thumb_dir / "paper_collage.jpg", thumb_dir / "miniature.jpg"
    if "finance_editorial" in prompts and pc.exists() and mi.exists():
        composite(Image.open(pc).convert("RGB"), Image.open(mi).convert("RGB")).save(thumb_dir / "finance_editorial.jpg", "JPEG", quality=82, optimize=True)
        print("ok   finance_editorial 左拼贴右微缩合成", flush=True)

    missing = [k for k in prompts if not (thumb_dir / f"{k}.jpg").exists()]
    if missing:
        print("缺少缩略图:", ", ".join(missing), file=sys.stderr)
    return 1 if failed or missing else 0


if __name__ == "__main__":
    sys.exit(main())
