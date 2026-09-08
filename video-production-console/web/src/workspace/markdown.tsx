import type { ReactNode } from "react";

function inline(text: string): ReactNode[] {
  const parts = text.split(/(\*\*[^*]+\*\*|`[^`]+`)/g);
  return parts.map((part, index) => {
    if (part.startsWith("**") && part.endsWith("**")) return <strong key={index}>{part.slice(2, -2)}</strong>;
    if (part.startsWith("`") && part.endsWith("`")) return <code key={index}>{part.slice(1, -1)}</code>;
    return <span key={index}>{part}</span>;
  });
}

function isTableRow(line: string): boolean {
  return line.trim().startsWith("|") && line.trim().endsWith("|");
}

function isTableDivider(line: string): boolean {
  return /^\|[\s:|-]+\|$/.test(line.trim());
}

function cells(line: string): string[] {
  return line.trim().slice(1, -1).split("|").map((cell) => cell.trim());
}

export function MarkdownView({ text }: { text: string }) {
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  const nodes: ReactNode[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) {
      i += 1;
      continue;
    }
    if (line.startsWith("```")) {
      const fence = [];
      i += 1;
      while (i < lines.length && !lines[i].startsWith("```")) {
        fence.push(lines[i]);
        i += 1;
      }
      if (i < lines.length) i += 1;
      nodes.push(<pre key={`c${i}`}><code>{fence.join("\n")}</code></pre>);
      continue;
    }
    if (isTableRow(line) && i + 1 < lines.length && isTableDivider(lines[i + 1])) {
      const head = cells(line);
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && isTableRow(lines[i]) && !isTableDivider(lines[i])) {
        rows.push(cells(lines[i]));
        i += 1;
      }
      nodes.push(
        <div className="workspace-table" key={`t${i}`}>
          <table>
            <thead><tr>{head.map((cell, idx) => <th key={idx}>{inline(cell)}</th>)}</tr></thead>
            <tbody>{rows.map((row, r) => <tr key={r}>{row.map((cell, c) => <td key={c}>{inline(cell)}</td>)}</tr>)}</tbody>
          </table>
        </div>,
      );
      continue;
    }
    if (/^---+$/.test(line.trim())) {
      nodes.push(<hr key={`h${i}`} />);
      i += 1;
      continue;
    }
    const heading = /^(#{1,3})\s+(.+)$/.exec(line);
    if (heading) {
      const Tag = (`h${heading[1].length}` as "h1" | "h2" | "h3");
      nodes.push(<Tag key={`hd${i}`}>{inline(heading[2])}</Tag>);
      i += 1;
      continue;
    }
    if (line.startsWith("> ")) {
      nodes.push(<blockquote key={`q${i}`}>{inline(line.slice(2))}</blockquote>);
      i += 1;
      continue;
    }
    if (/^[-*]\s+/.test(line)) {
      const items = [];
      while (i < lines.length && /^[-*]\s+/.test(lines[i])) {
        items.push(<li key={i}>{inline(lines[i].replace(/^[-*]\s+/, ""))}</li>);
        i += 1;
      }
      nodes.push(<ul key={`ul${i}`}>{items}</ul>);
      continue;
    }
    const para = [line];
    i += 1;
    while (i < lines.length && lines[i].trim() && !lines[i].startsWith("#") && !lines[i].startsWith("|") && !lines[i].startsWith("- ") && !lines[i].startsWith("* ") && !lines[i].startsWith("> ") && !/^---+$/.test(lines[i].trim()) && !lines[i].startsWith("```")) {
      para.push(lines[i]);
      i += 1;
    }
    nodes.push(<p key={`p${i}`}>{inline(para.join(" "))}</p>);
  }
  return <div className="workspace-md">{nodes}</div>;
}
