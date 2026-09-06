"""Remove configured fact-check nodes and release only existing facts-blocked drafts.

Dry-run by default. Preserve bodies, model choices, original evidence and backups.
"""
import argparse
import hashlib
import json
import shutil
import sqlite3
from datetime import datetime, timezone
from pathlib import Path


def encode(value):
    return (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode("utf-8")


def without_facts(workflow):
    result = json.loads(json.dumps(workflow))
    removed = {n["id"] for n in result.get("nodes", []) if n.get("type") == "agent" and
               (n["id"] == "facts" or "source_facts" in n.get("config", {}).get("system_prompt", ""))}
    if not removed:
        return result, False
    result["nodes"] = [n for n in result["nodes"] if n["id"] not in removed]
    result["edges"] = [edge for edge in result["edges"] if not (set(edge) & removed)]
    ids = {n["id"] for n in result["nodes"]}
    assert all(set(edge) <= ids for edge in result["edges"])
    reachable = {n["id"] for n in result["nodes"] if n["type"] == "input"}
    for _ in ids:
        reachable |= {b for a, b in result["edges"] if a in reachable}
    assert ids <= reachable, "Removing facts would disconnect another node"
    return result, True


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()
    root = Path.cwd().resolve()
    data = root / "video-console-data"
    dbpath = data / "console.db"
    db = sqlite3.connect(dbpath.as_uri() + "?mode=ro", uri=True)
    db.row_factory = sqlite3.Row
    for table in ("remix_lab_runs", "codex_tasks"):
        active = db.execute(f"SELECT id FROM {table} WHERE status IN ('queued','running')").fetchall()
        assert not active, f"Active jobs in {table}; retry after they finish"
    changes = {}

    def stage(path, value):
        path = path.resolve()
        path.relative_to(data)
        before = path.read_bytes() if path.exists() else None
        after = encode(value)
        if before != after:
            changes[path] = (before, after)

    paths = [data / "remix_lab/workflow.json", *sorted((data / "remix_lab/workflows").glob("*.json"))]
    for path in paths:
        if path.exists():
            updated, changed = without_facts(json.loads(path.read_text(encoding="utf-8-sig")))
            if changed:
                stage(path, updated)
    snapshots = []
    for table, key in (("remix_lab_experiments", "id"), ("remix_lab_run_configs", "run_id")):
        for row in db.execute(f"SELECT {key},workflow_json FROM {table} WHERE workflow_json<>''"):
            updated, changed = without_facts(json.loads(row["workflow_json"]))
            if changed:
                snapshots.append((table, key, row[key], row["workflow_json"], encode(updated).decode()))

    rows = db.execute("""SELECT r.*,e.produce_account_id,a.name AS account_name
      FROM remix_lab_runs r JOIN remix_lab_experiments e ON e.id=r.experiment_id
      LEFT JOIN accounts a ON a.id=e.produce_account_id
      WHERE r.status='failed' AND r.error_message LIKE '%补齐事实%'""").fetchall()
    recovered = []
    now = datetime.now(timezone.utc).isoformat()
    for row in rows:
        folder = Path(row["output_dir"]).resolve()
        folder.relative_to(data)
        draft_path = folder / "draft_v1.json"
        draft_raw = draft_path.read_text(encoding="utf-8-sig")
        draft = json.loads(draft_raw)
        body = draft.get("continuous_script", "")
        assert body.strip() and body == row["continuous_script"]
        assert (folder / "continuous_script.txt").read_text(encoding="utf-8-sig") == body
        assert not row["review_json"], "A reviewed draft needs separate recovery handling"
        last_path = folder / "last.json"
        last = json.loads(last_path.read_text(encoding="utf-8-sig"))
        last["status"] = "completed"
        last["summary"] = "按用户要求保留已有文案并恢复可用，事实核查节点已移除。本次未重新调用模型，自检与审稿未执行。"
        stage(last_path, last)
        context_path = folder / "editorial_context.json"
        if context_path.exists():
            context = json.loads(context_path.read_text(encoding="utf-8-sig"))
            context["facts_required"] = False
            stage(context_path, context)
        evidence = {"run_id": row["id"], "account": row["account_name"], "restored_at": now,
                    "reason": "用户要求移除事实核查并保留现有稿件", "selfcheck": "skipped", "review": "skipped",
                    "body_sha256": hashlib.sha256(body.encode()).hexdigest()}
        stage(folder / "operator_restore.json", evidence)
        recovered.append((dict(row), draft_raw, json.dumps(draft.get("titles", []), ensure_ascii=False), evidence))

    report = {"applied": args.apply, "config_and_artifact_files": len(changes), "workflow_snapshots": len(snapshots),
              "recovered": [item[3] for item in recovered]}
    if not args.apply:
        print(json.dumps(report, ensure_ascii=True, indent=2))
        return
    backup = data / ("backup-remove-facts-" + datetime.now().strftime("%Y%m%d-%H%M%S"))
    backup.mkdir()
    backup_db = sqlite3.connect(backup / "console.db")
    db.backup(backup_db)
    backup_db.close()
    db.close()
    for path, (before, _) in changes.items():
        if before is not None:
            dest = backup / path.relative_to(data)
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(path, dest)
    conn = sqlite3.connect(dbpath)
    written = []
    try:
        conn.execute("BEGIN IMMEDIATE")
        for table in ("remix_lab_runs", "codex_tasks"):
            assert not conn.execute(f"SELECT id FROM {table} WHERE status IN ('queued','running')").fetchall()
        for table, key, ident, before, after in snapshots:
            result = conn.execute(f"UPDATE {table} SET workflow_json=? WHERE {key}=? AND workflow_json=?", (after, ident, before))
            assert result.rowcount == 1, "Concurrent workflow change"
        for row, draft_raw, titles, _ in recovered:
            result = conn.execute("""UPDATE remix_lab_runs SET status='completed',error_message='',package_json=?,draft_v1_json=?,titles_json=?
              WHERE id=? AND status='failed' AND error_message=? AND continuous_script=?""",
              (draft_raw, draft_raw, titles, row["id"], row["error_message"], row["continuous_script"]))
            assert result.rowcount == 1, "Concurrent draft change"
            conn.execute("""INSERT INTO remix_lab_productions(run_id,experiment_id,account_id,auto,status,step,created_at,updated_at)
              VALUES(?,?,?,0,'waiting_confirm','confirm',?,?) ON CONFLICT(run_id) DO NOTHING""",
              (row["id"], row["experiment_id"], row["produce_account_id"], now, now))
            statuses = [s[0] for s in conn.execute("SELECT status FROM remix_lab_runs WHERE experiment_id=?", (row["experiment_id"],))]
            status = "completed" if all(s == "completed" for s in statuses) else "partial" if "completed" in statuses else "failed"
            conn.execute("UPDATE remix_lab_experiments SET status=?,updated_at=? WHERE id=?", (status, now, row["experiment_id"]))
        for path, (before, after) in changes.items():
            assert (path.read_bytes() if path.exists() else None) == before, "Concurrent file change"
            temp = path.with_suffix(path.suffix + ".restore.tmp")
            temp.write_bytes(after)
            temp.replace(path)
            written.append(path)
        conn.commit()
    except Exception:
        conn.rollback()
        for path in written:
            before, _ = changes[path]
            if before is not None:
                path.write_bytes(before)
            else:
                path.unlink()
        raise
    finally:
        conn.close()
    report["backup"] = str(backup.relative_to(root))
    (root / "docs/audits/2026-09-05-remove-facts-restore.json").write_bytes(encode(report))
    print(json.dumps(report, ensure_ascii=True, indent=2))


if __name__ == "__main__":
    main()
