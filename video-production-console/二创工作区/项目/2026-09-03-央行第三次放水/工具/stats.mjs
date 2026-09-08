// 统计一个项目目录下所有二创稿：篇幅、课尾字数、关键词次数、互动位置、
// 与原文去标点后连续 ≥10 字的重合片段。用法：node 二创文案/stats.mjs <项目目录>
import fs from "node:fs";
import path from "node:path";

const dir = process.argv[2];
if (!dir) {
  console.error("用法：node stats.mjs <项目目录>");
  process.exit(1);
}
const strip = (s) => s.replace(/[，。、！？；：“”「」（）《》\s—\-…·,.!?;:"'()\[\]]/g, "");
const source = fs.readFileSync(path.join(dir, "原文.txt"), "utf8").trim();
const sourceStripped = strip(source);
const openingSentences = source.split(/(?<=[。！？])/).slice(0, 3).join("");

const files = fs.readdirSync(dir).filter((f) => /^二创\d+\.txt$/.test(f)).sort();
const TAIL_MARKERS = ["如果你现在", "要是你现在"];

function overlapRuns(body) {
  const b = strip(body);
  const runs = [];
  let i = 0;
  while (i <= b.length - 10) {
    if (sourceStripped.includes(b.slice(i, i + 10))) {
      let j = i + 10;
      while (j < b.length && sourceStripped.includes(b.slice(i, j + 1))) j += 1;
      runs.push(b.slice(i, j));
      i = j;
    } else {
      i += 1;
    }
  }
  return { runs, bodyLen: b.length };
}

const results = files.map((file) => {
  const text = fs.readFileSync(path.join(dir, file), "utf8").trim();
  const tailIdx = Math.max(...TAIL_MARKERS.map((m) => text.lastIndexOf(m)));
  const tail = tailIdx >= 0 ? text.slice(tailIdx) : "";
  const body = text.startsWith(openingSentences) ? text.slice(openingSentences.length) : text;
  const { runs, bodyLen } = overlapRuns(body);
  const count = (re) => (text.match(re) ?? []).length;
  return {
    file,
    total: text.length,
    ratio: +(text.length / source.length).toFixed(2),
    tail: tail.length,
    wukuai: count(/五块钱/g),
    chuchuang: count(/橱窗/g),
    kemin: count(/财富觉醒方法论/g),
    interactPct: Math.round((text.indexOf("顺风顺水") / text.length) * 100),
    overlapRuns: runs.length,
    overlapPct: +((runs.reduce((n, r) => n + r.length, 0) / bodyLen) * 100).toFixed(1),
    runs,
    paragraphs: text.split(/\n\s*\n/).length,
  };
});

console.log(`原文 ${source.length} 字`);
for (const r of results) {
  console.log(
    `${r.file}: ${r.total}字(${r.ratio}x) 段落${r.paragraphs} 课尾${r.tail} 五块钱${r.wukuai} 橱窗${r.chuchuang} 课名${r.kemin} 互动${r.interactPct}% 重合片段${r.overlapRuns} 重合率${r.overlapPct}%`,
  );
  for (const run of r.runs) console.log(`    ⚠ ${run}`);
}
fs.writeFileSync(path.join(dir, "stats.json"), JSON.stringify({ source: source.length, results }, null, 2) + "\n", "utf8");
