# -*- coding: utf-8 -*-
"""把整段配音和它的文案逐字对齐，输出每个实字（去标点/空白）的起止秒。

用法：align_narration.py <audio> <script.txt> <out.json>

做法：faster-whisper 带词级时间戳转写 → 把识别出的字和文案的字用 SequenceMatcher 对齐 →
匹配上的字直接拿识别时间，没匹配上的字在相邻锚点之间按位置插值。
输出：{"ok": true, "chars": N, "times": [[start, end], ...]}，times 与文案实字一一对应。
TTS 供应商（AuraSTD）只给十几秒一段的粗时间戳，这一步是拿到逐字时间的唯一办法。
"""
from __future__ import annotations

import difflib
import json
import os
import sys
from pathlib import Path

PUNCT = set(" \t\r\n\u3000，。！？；：、…—～·\"'“”‘’「」『』（）《》〈〉【】,.!?;:()[]<>-_/\\|")


def substantive(text: str) -> list[str]:
    return [ch for ch in text if ch not in PUNCT]


def main() -> int:
    if len(sys.argv) < 4:
        print(json.dumps({"ok": False, "error": "usage: align_narration.py audio script.txt out.json"}))
        return 2
    audio, script_path, out_path = sys.argv[1], Path(sys.argv[2]), Path(sys.argv[3])
    script_chars = substantive(script_path.read_text(encoding="utf-8"))
    if not script_chars:
        print(json.dumps({"ok": False, "error": "script is empty"}))
        return 1
    try:
        from faster_whisper import WhisperModel
    except Exception as exc:  # noqa: BLE001
        print(json.dumps({"ok": False, "error": f"faster_whisper unavailable: {exc}"}))
        return 1

    model_name = os.environ.get("AI_SHORTS_ALIGN_MODEL", "small")
    model = WhisperModel(model_name, device="cpu", compute_type="int8")
    segments, _ = model.transcribe(audio, language="zh", beam_size=1, word_timestamps=True, vad_filter=False)

    # 识别结果展开成逐字：一个 word 里有几个实字就均分它的时长。
    asr_chars: list[str] = []
    asr_times: list[tuple[float, float]] = []
    for seg in segments:
        for w in seg.words or []:
            chars = substantive(w.word)
            if not chars:
                continue
            step = (w.end - w.start) / len(chars)
            for k, ch in enumerate(chars):
                asr_chars.append(ch)
                asr_times.append((w.start + k * step, w.start + (k + 1) * step))
    if not asr_chars:
        print(json.dumps({"ok": False, "error": "asr produced no words"}))
        return 1

    # 对齐：匹配块里的字一一对应；块与块之间没匹配上的文案字，在两侧锚点之间按位置插值。
    matcher = difflib.SequenceMatcher(None, script_chars, asr_chars, autojunk=False)
    anchors: dict[int, tuple[float, float]] = {}
    for a, b, n in matcher.get_matching_blocks():
        for k in range(n):
            anchors[a + k] = asr_times[b + k]
    matched = len(anchors)
    total = len(script_chars)
    times: list[list[float]] = []
    last_end = 0.0
    audio_end = asr_times[-1][1]
    for i in range(total):
        if i in anchors:
            s, e = anchors[i]
            times.append([s, e])
            last_end = e
            continue
        # 找右边最近的锚点，在 last_end 和它之间均分。
        j = i + 1
        while j < total and j not in anchors:
            j += 1
        right = anchors[j][0] if j < total else audio_end
        gap = max(j - i, 1)
        step = max(right - last_end, 0.0) / gap
        times.append([last_end, last_end + step])
        last_end += step
    out_path.write_text(json.dumps({"ok": True, "chars": total, "matched": matched, "times": times}), encoding="utf-8")
    print(json.dumps({"ok": True, "chars": total, "matched": matched, "asr_chars": len(asr_chars)}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
