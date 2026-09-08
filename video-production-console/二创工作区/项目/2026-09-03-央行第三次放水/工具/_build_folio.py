# -*- coding: utf-8 -*-
"""Build a dedicated folio directory: 5 remix scripts + summary HTML."""
from __future__ import annotations

import json
import shutil
from pathlib import Path

ROOT = Path(__file__).resolve().parent
SRC = ROOT.parent / "二创文案" / "2026-09-03-央行第三次放水"
DEST = ROOT / "2026-09-04-央行第三次放水"

META = [
    {
        "id": "01",
        "file": "二创01.txt",
        "name": "正序拆闸",
        "way": "会议开场，按水龙头→两回历史→第三回数字往下拆",
        "comment": "顺风顺水",
        "short_titles": ["央行第三次开闸", "170万亿要往外走", "近二十年翻身窗口"],
        "titles": [
            "刚刚开完会，第三次放水要来了",
            "170万亿居民存款，闸一开会往哪冲",
            "过去四十年只开过两回闸",
            "房价涨二十年，根子是放水",
            "居民存款两个月流出超过2万亿",
            "现在卡在第一阶段最后一截",
            "五块钱换少走十年弯路",
        ],
        "topics": ["#财经", "#存款", "#降息", "#央行", "#财富觉醒"],
    },
    {
        "id": "02",
        "file": "二创02.txt",
        "name": "钱听谁的",
        "way": "先讲钱听利息、利息听央行，再套两回剧本",
        "comment": "顺风顺水",
        "short_titles": ["钱到底听谁的", "利息一降钱就跑", "两回开闸同一剧本"],
        "titles": [
            "钱不认拼命，只认利息",
            "银行利息一降，钱就坐不住",
            "90年代家电、2008年房子，都是开闸造的富人",
            "170万亿水位，闸一开冲得更猛",
            "钱已经动了，两个月流出2万亿",
            "第一阶段尾巴上，难度马上翻十倍",
            "一家有一个人听明白，钱走另一条路",
        ],
        "topics": ["#财经", "#存款", "#银行", "#降息", "#财富觉醒"],
    },
    {
        "id": "03",
        "file": "二创03.txt",
        "name": "房价先问",
        "way": "开场先打人多房少，再回溯两回开闸",
        "comment": "顺风顺水",
        "short_titles": ["房价不是人多房少", "钱把房价顶上去的", "第三回比前两回更凶"],
        "titles": [
            "过去二十年房子凭什么涨成那样",
            "人多房少只是表面，根子是放水",
            "房价是被放出来的钱顶上去的",
            "第一回家电，第二回房子，第三回呢",
            "170万亿加上2万亿，钱已经在搬家",
            "政策一落地，普通人再进难度翻十倍",
            "你缺的是五块钱，还是把事看透的脑子",
        ],
        "topics": ["#财经", "#房价", "#存款", "#放水", "#财富觉醒"],
    },
    {
        "id": "04",
        "file": "二创04.txt",
        "name": "三个数钉死",
        "way": "先钉 170万亿 / 2万亿 / 两回，再展开故事",
        "comment": "顺风顺水",
        "short_titles": ["先记住三个数", "170万亿和2万亿", "第三回放水来了"],
        "titles": [
            "三个数：170万亿、2万亿、两回开闸",
            "居民存款历史最高，钱要从银行往外走",
            "两个月流出2万亿，数据不骗人",
            "过去四十年大开闸只有两回",
            "每一次钱往外走，都有一批人翻身",
            "现在卡在第一步最后一截",
            "半斤鸡蛋的价钱，换少走十年弯路",
        ],
        "topics": ["#财经", "#存款", "#央行", "#通胀", "#财富觉醒"],
    },
    {
        "id": "05",
        "file": "二创05.txt",
        "name": "家里那笔钱",
        "way": "从你家存款马上要被赶出银行切入",
        "comment": "顺风顺水",
        "short_titles": ["你家存款要被赶走", "邻居们已经开始搬家", "管钱那位来听"],
        "titles": [
            "你家那笔存款，马上要被赶出银行",
            "钱不认文凭，只认利息",
            "捏着存折的人，两回都错过了",
            "存款邻居们已经开始搬家",
            "两个月流出2万亿，大放水在路上",
            "最好让家里管钱那位来听",
            "群里抢的红包，你拿它干过什么正事",
        ],
        "topics": ["#财经", "#家庭存款", "#降息", "#通胀", "#财富觉醒"],
    },
]


def last_cta(text: str) -> str:
    parts = [p.strip() for p in text.replace("！", "。").split("。") if p.strip()]
    if len(parts) >= 2:
        return parts[-2] + "。" + parts[-1] + ("。" if not parts[-1].endswith("。") else "")
    return parts[-1] if parts else ""


def main() -> None:
    DEST.mkdir(parents=True, exist_ok=True)
    if not (DEST / "原文.txt").exists():
        shutil.copy2(SRC / "原文.txt", DEST / "原文.txt")
    drafts = []
    for item in META:
        dest = DEST / item["file"]
        src = SRC / item["file"]
        if not dest.exists():
            shutil.copy2(src, dest)
        text = dest.read_text(encoding="utf-8").strip()
        drafts.append(
            {
                **item,
                "chars": len(text),
                "cta": last_cta(text),
                "script": text,
            }
        )

    payload = json.dumps(drafts, ensure_ascii=False)
    html = HTML.replace("__DRAFTS__", payload)
    (DEST / "汇总页.html").write_text(html, encoding="utf-8")
    print(f"wrote {DEST}")
    for d in drafts:
        print(f"  {d['id']} {d['name']} {d['chars']}字")


HTML = r"""<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>五篇二创汇总 · 央行第三次放水</title>
  <link rel="preconnect" href="https://fonts.googleapis.com" />
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
  <link href="https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@400;500&family=Noto+Sans+SC:wght@400;500;700&family=Noto+Serif+SC:wght@600;700&display=swap" rel="stylesheet" />
  <style>
    :root {
      --ink: #1a2332;
      --paper: #e8dcc8;
      --paper-2: #f3ead8;
      --seal: #b23a2f;
      --oil: #c9893a;
      --ash: #8a8175;
      --rule: rgba(26, 35, 50, 0.14);
    }
    * { box-sizing: border-box; }
    html, body { margin: 0; min-height: 100%; }
    body {
      background:
        radial-gradient(1200px 600px at 10% -10%, rgba(201, 137, 58, 0.12), transparent 50%),
        linear-gradient(180deg, #121820 0%, var(--ink) 40%, #10161e 100%);
      color: var(--paper);
      font-family: "Noto Sans SC", sans-serif;
    }
    .desk { min-height: 100vh; display: grid; grid-template-columns: 92px 1fr; }
    .spine {
      border-right: 1px solid rgba(232, 220, 200, 0.12);
      padding: 28px 0 20px;
      display: flex; flex-direction: column; align-items: center; gap: 18px;
    }
    .spine h1 {
      writing-mode: vertical-rl;
      font-family: "Noto Serif SC", serif;
      font-size: 22px; font-weight: 700; margin: 0 0 12px;
    }
    .spine p { writing-mode: vertical-rl; margin: 0; color: var(--ash); font-size: 12px; letter-spacing: 0.18em; }
    .folio { padding: 28px 36px 40px; display: grid; grid-template-rows: auto auto auto 1fr auto; gap: 16px; min-width: 0; }
    .mast { display: flex; justify-content: space-between; align-items: end; gap: 20px; }
    .kicker { font-family: "IBM Plex Mono", monospace; font-size: 11px; color: var(--oil); letter-spacing: 0.16em; text-transform: uppercase; }
    .mast h2 { margin: 6px 0 0; font-family: "Noto Serif SC", serif; font-size: 32px; font-weight: 700; line-height: 1.15; }
    .stats { display: flex; gap: 18px; color: var(--ash); font-family: "IBM Plex Mono", monospace; font-size: 12px; flex-wrap: wrap; }
    .note {
      color: var(--ash); font-size: 13px; line-height: 1.6;
      border: 1px solid rgba(232, 220, 200, 0.12); padding: 10px 14px;
    }
    .tabs { display: grid; grid-template-columns: repeat(5, 1fr); gap: 10px; }
    .tab {
      appearance: none; border: 1px solid rgba(232, 220, 200, 0.16);
      background: rgba(232, 220, 200, 0.04); color: inherit; text-align: left;
      padding: 14px 14px 12px; cursor: pointer; min-height: 118px;
    }
    .tab:hover { background: rgba(232, 220, 200, 0.08); }
    .tab.is-on { background: var(--paper); color: var(--ink); border-color: var(--paper); }
    .tab .mark { font-family: "IBM Plex Mono", monospace; font-size: 11px; color: var(--oil); }
    .tab.is-on .mark { color: var(--seal); }
    .tab .name { display: block; margin: 8px 0 6px; font-family: "Noto Serif SC", serif; font-size: 18px; }
    .tab .way { font-size: 12px; color: var(--ash); line-height: 1.45; }
    .tab.is-on .way { color: #5d564c; }
    .sheet {
      background: var(--paper-2); color: var(--ink); min-height: 520px;
      display: grid; grid-template-columns: minmax(0, 1.4fr) 320px;
      box-shadow: 0 24px 60px rgba(0, 0, 0, 0.28);
    }
    .script { padding: 28px 32px 32px; border-right: 1px solid var(--rule); overflow: auto; max-height: calc(100vh - 390px); }
    .script p { margin: 0 0 1.15em; font-size: 16.5px; line-height: 1.95; white-space: pre-wrap; }
    .side {
      padding: 22px 22px 26px; display: flex; flex-direction: column; gap: 16px;
      background: linear-gradient(180deg, rgba(178, 58, 47, 0.06), transparent 90px), var(--paper);
    }
    .side h3 { margin: 0 0 8px; font-size: 12px; letter-spacing: 0.14em; color: var(--ash); font-weight: 500; }
    .titles { display: flex; flex-direction: column; gap: 6px; }
    .titles button, .action {
      appearance: none; border: 0; background: transparent; color: inherit;
      text-align: left; font: inherit; cursor: pointer;
    }
    .titles button {
      padding: 7px 0; border-bottom: 1px dashed var(--rule); font-size: 13px; line-height: 1.45;
    }
    .titles button:hover { color: var(--seal); }
    .shorts { display: flex; flex-wrap: wrap; gap: 6px; }
    .chip { border: 1px solid var(--rule); padding: 4px 8px; font-size: 12px; background: rgba(26, 35, 50, 0.03); }
    .actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: auto; }
    .action {
      display: inline-flex; align-items: center; gap: 6px;
      border: 1px solid var(--ink) !important; padding: 8px 10px; font-size: 12px;
    }
    .action:hover { background: var(--ink); color: var(--paper); }
    .seal {
      background: #2a1614; color: #f6e6d6; padding: 18px 22px 20px;
      display: grid; grid-template-columns: 1fr auto; gap: 16px; align-items: center;
    }
    .seal .label { font-family: "IBM Plex Mono", monospace; font-size: 11px; letter-spacing: 0.16em; color: #e0a39a; }
    .seal q { display: block; quotes: none; margin-top: 8px; font-family: "Noto Serif SC", serif; font-size: 22px; line-height: 1.35; }
    .seal button {
      appearance: none; border: 1px solid rgba(246, 230, 214, 0.35); background: transparent;
      color: inherit; padding: 10px 12px; cursor: pointer; font-size: 12px;
    }
    .toast {
      position: fixed; right: 24px; bottom: 24px; background: var(--paper); color: var(--ink);
      padding: 10px 14px; font-size: 13px; opacity: 0; transform: translateY(8px);
      pointer-events: none; transition: opacity 160ms ease, transform 160ms ease;
    }
    .toast.show { opacity: 1; transform: none; }
    @media (max-width: 1100px) {
      .tabs { grid-template-columns: 1fr 1fr; }
    }
    @media (max-width: 980px) {
      .desk { grid-template-columns: 1fr; }
      .spine { flex-direction: row; padding: 16px 18px; border-right: 0; border-bottom: 1px solid rgba(232, 220, 200, 0.12); }
      .spine h1, .spine p { writing-mode: horizontal-tb; }
      .folio { padding: 18px; }
      .sheet, .seal { grid-template-columns: 1fr; }
      .script { max-height: none; border-right: 0; border-bottom: 1px solid var(--rule); }
      .mast h2 { font-size: 24px; }
      .seal q { font-size: 18px; }
    }
    @media (max-width: 640px) {
      .tabs { grid-template-columns: 1fr; }
      .tab { min-height: 0; }
    }
  </style>
</head>
<body>
  <div class="desk">
    <aside class="spine">
      <h1>五篇二创汇总</h1>
      <p>同一原文 · 五种切口 · 同一课尾</p>
    </aside>
    <main class="folio">
      <header class="mast">
        <div>
          <div class="kicker">2026-09-04 · 央行第三次放水</div>
          <h2 id="headline">正序拆闸</h2>
        </div>
        <div class="stats">
          <span id="count">0 字</span>
          <span>评论锁词 顺风顺水</span>
          <span>必保数字 170万亿 / 2万亿 / 两回</span>
        </div>
      </header>
      <div class="note">同一项目下的五篇二创。点左侧卡片看全文，右侧可复制标题和正文。以后别的原文，另开一个项目目录。</div>
      <nav class="tabs" id="tabs" aria-label="选择成稿"></nav>
      <section class="sheet">
        <article class="script" id="script"></article>
        <aside class="side">
          <div>
            <h3>板面短标题</h3>
            <div class="shorts" id="shorts"></div>
          </div>
          <div>
            <h3>候选标题 · 点击复制</h3>
            <div class="titles" id="titles"></div>
          </div>
          <div>
            <h3>话题</h3>
            <div class="shorts" id="topics"></div>
          </div>
          <div class="actions">
            <button type="button" class="action" id="copyScript">复制正文</button>
            <button type="button" class="action" id="copyFile">复制带文件名</button>
          </div>
        </aside>
      </section>
      <footer class="seal">
        <div>
          <div class="label">课尾压轴 · CTA</div>
          <q id="cta"></q>
        </div>
        <button type="button" id="copyCta">复制压轴</button>
      </footer>
    </main>
  </div>
  <div class="toast" id="toast" role="status"></div>
  <script>window.REMIX_DRAFTS = __DRAFTS__;</script>
  <script>
    const drafts = window.REMIX_DRAFTS || [];
    let current = 0;
    const $ = (id) => document.getElementById(id);
    function toast(text) {
      const el = $("toast");
      el.textContent = text;
      el.classList.add("show");
      clearTimeout(toast._t);
      toast._t = setTimeout(() => el.classList.remove("show"), 1600);
    }
    async function copy(text, ok) {
      try {
        await navigator.clipboard.writeText(text);
      } catch (err) {
        const ta = document.createElement("textarea");
        ta.value = text;
        document.body.appendChild(ta);
        ta.select();
        document.execCommand("copy");
        ta.remove();
      }
      toast(ok);
    }
    function paras(script) {
      return script.split(/\n\s*\n/).map((p) => p.trim()).filter(Boolean);
    }
    function escapeHtml(s) {
      return s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
    }
    function renderTabs() {
      $("tabs").innerHTML = drafts.map((draft, index) => `
        <button type="button" class="tab${index === current ? " is-on" : ""}" data-i="${index}">
          <span class="mark">${draft.id} · ${draft.chars}字</span>
          <span class="name">${escapeHtml(draft.name)}</span>
          <span class="way">${escapeHtml(draft.way)}</span>
        </button>
      `).join("");
    }
    function render() {
      const draft = drafts[current];
      $("headline").textContent = draft.name;
      $("count").textContent = draft.chars + " 字";
      $("script").innerHTML = paras(draft.script).map((p) => "<p>" + escapeHtml(p) + "</p>").join("");
      $("shorts").innerHTML = draft.short_titles.map((t) => "<span class='chip'>" + escapeHtml(t) + "</span>").join("");
      $("topics").innerHTML = draft.topics.map((t) => "<span class='chip'>" + escapeHtml(t) + "</span>").join("");
      $("titles").innerHTML = draft.titles.map((t) => "<button type='button' data-copy='" + escapeHtml(t) + "'>" + escapeHtml(t) + "</button>").join("");
      $("cta").textContent = draft.cta;
      renderTabs();
    }
    $("tabs").addEventListener("click", (event) => {
      const btn = event.target.closest("[data-i]");
      if (!btn) return;
      current = Number(btn.dataset.i);
      render();
    });
    $("titles").addEventListener("click", (event) => {
      const btn = event.target.closest("button[data-copy]");
      if (!btn) return;
      copy(btn.dataset.copy, "标题已复制");
    });
    $("copyScript").addEventListener("click", () => copy(drafts[current].script, "正文已复制"));
    $("copyFile").addEventListener("click", () => copy(drafts[current].file + "\n\n" + drafts[current].script, "带文件名已复制"));
    $("copyCta").addEventListener("click", () => copy(drafts[current].cta, "压轴已复制"));
    render();
  </script>
</body>
</html>
"""

if __name__ == "__main__":
    main()
