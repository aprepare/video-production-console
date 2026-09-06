"""Sync a reviewed Go prompt export into local editable copies, with backups.

Run from the repository root. Historical run snapshots and model settings are
never modified. Use --apply only after reviewing the export and dry run.
"""
import argparse
import copy
import hashlib
import json
import shutil
from datetime import datetime
from pathlib import Path


def load(path):
    return json.loads(path.read_text(encoding="utf-8-sig"))


def remove_mechanical_review(doc):
    """Bridge retired nodes by type, including account-specific node IDs."""
    for node in list(doc.get("nodes", [])):
        if node.get("type") != "selfcheck":
            continue
        node_id = node["id"]
        old_edges = doc.get("edges", [])
        incoming = [a for a, b in old_edges if b == node_id]
        outgoing = [b for a, b in old_edges if a == node_id]
        edges = [e for e in old_edges if node_id not in e]
        for source in incoming:
            for target in outgoing:
                if [source, target] not in edges:
                    edges.append([source, target])
        doc["nodes"] = [n for n in doc["nodes"] if n["id"] != node_id]
        doc["edges"] = edges
    return doc


def update(path, doc, policy):
    original = copy.deepcopy(doc)
    doc = copy.deepcopy(doc)
    if path.name == "active_prompt.json":
        doc.update(system=policy["writer_system"], user=policy["writer_user"], stamp=policy["version"])
    elif path.name == "library.json":
        for prompt in doc.get("prompts", []):
            if prompt.get("id") in ("elder_stable", "bone_flesh"):
                prompt.update(system=policy["writer_system"], user=policy["writer_user"], stamp=policy["version"])
    elif path.name == "agent_prompts.json":
        for key, export in [("hook_system", "hook"), ("facts_search_system", "facts"), ("facts_offline_system", "facts_offline"), ("ammo_system", "ammo"), ("reviewer_system", "reviewer")]:
            doc[key] = policy[export]
    elif "nodes" in doc:
        simplify = "planner_user_template" in policy
        removed = {n["id"] for n in doc["nodes"] if simplify and n.get("type") == "agent" and n["id"] in ("facts", "ammo", "hook_library")}
        if removed:
            # Do not silently break custom chains or template dependencies.
            by_id = {n["id"]: n for n in doc["nodes"]}
            for source, target in doc.get("edges", []):
                if source in removed and target not in removed and by_id[target]["type"] != "writer":
                    raise ValueError(f"Removed node feeds custom dependency: {source}->{target} in {path}")
            for node in doc["nodes"]:
                if node["id"] not in removed and any("{{node:" + node_id + "}}" in node.get("config", {}).get("user_template", "") for node_id in removed):
                    raise ValueError(f"Template depends on removed node: {path}/{node['id']}")
            doc["nodes"] = [n for n in doc["nodes"] if n["id"] not in removed]
            doc["edges"] = [e for e in doc.get("edges", []) if not any(node_id in removed for node_id in e)]
        if policy.get("remove_mechanical_review"):
            doc = remove_mechanical_review(doc)
        for node in doc["nodes"]:
            cfg = node.setdefault("config", {})
            if "reference_system" in policy and node["type"] == "agent" and (node["id"] == "hook" or cfg.get("role") == "reference"):
                if cfg.get("role") != "reference":
                    node["title"] = "参考稿1"
                cfg.update(role="reference", system_prompt=policy["reference_system"], user_template=policy["reference_user_template"],
                           inject_title=node["title"], inject_rule=policy["reference_inject_rule"])
            elif node["id"] == "hook" and node["type"] == "agent" and simplify:
                node["title"] = "二创策划"
                cfg.update(system_prompt=policy["hook"], user_template=policy["planner_user_template"],
                           inject_title=policy["planner_inject_title"], inject_rule=policy["planner_inject_rule"])
            elif node["id"] == "facts":
                cfg["system_prompt"] = policy["facts"] if cfg.get("channel") == "search" else policy["facts_offline"]
                cfg["inject_rule"] = policy["facts_inject_rule"]
            elif node["id"] == "ammo":
                cfg["system_prompt"] = policy["ammo"]
                cfg["inject_rule"] = policy["ammo_inject_rule"]
            elif node["type"] == "reviewer":
                cfg["system_prompt"] = policy["reviewer"]
        # Only approved removals, planner labels/prompts and reviewer rules differ.
        def controls(workflow):
            obj = copy.deepcopy(workflow)
            if policy.get("remove_mechanical_review"):
                obj = remove_mechanical_review(obj)
            obj["nodes"] = [n for n in obj.get("nodes", []) if n["id"] not in removed]
            obj["edges"] = [e for e in obj.get("edges", []) if not any(n in removed for n in e)]
            for node in obj.get("nodes", []):
                cfg = node.setdefault("config", {})
                for key in ("system_prompt", "inject_rule"):
                    cfg.pop(key, None)
                if "reference_system" in policy and node["type"] == "agent" and (node["id"] == "hook" or cfg.get("role") == "reference"):
                    node.pop("title", None)
                    for key in ("role", "user_template", "inject_title"):
                        cfg.pop(key, None)
                elif node["id"] == "hook" and node["type"] == "agent" and simplify:
                    node.pop("title", None)
                    cfg.pop("user_template", None)
                    cfg.pop("inject_title", None)
            return obj
        if controls(doc) != controls(original):
            raise ValueError(f"Non-prompt workflow setting changed: {path}")
    return doc


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("export", type=Path)
    parser.add_argument("--apply", action="store_true")
    parser.add_argument("--report", type=Path, help="Audit report destination (optional)")
    args = parser.parse_args()
    root = Path.cwd().resolve()
    data = (root / "video-console-data/remix_lab").resolve()
    policy = load(args.export)
    paths = [data / name for name in ("active_prompt.json", "library.json", "agent_prompts.json", "workflow.json")]
    paths += sorted((data / "workflows").glob("*.json"))
    changes = []
    for path in paths:
        if not path.exists():
            continue
        path.resolve().relative_to(data)
        before = path.read_bytes()
        previous = load(path)
        updated = update(path, previous, policy)
        if updated == previous:
            continue
        after = (json.dumps(updated, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
        changes.append((path, before, after))
    backup = data / ("backup-policy-" + datetime.now().strftime("%Y%m%d-%H%M%S"))
    evidence = {"version": policy["version"], "applied": args.apply, "files": []}
    if args.apply:
        for path, before, _ in changes:
            if path.read_bytes() != before:
                raise RuntimeError(f"Concurrent edit: {path}")
    for path, before, after in changes:
        if args.apply:
            if path.read_bytes() != before:
                raise RuntimeError(f"Concurrent edit: {path}")
            target = backup / path.relative_to(data)
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(path, target)
            temp = path.with_suffix(".policy.tmp")
            temp.write_bytes(after)
            temp.replace(path)
        evidence["files"].append({"path": str(path.relative_to(root)), "before_sha256": hashlib.sha256(before).hexdigest(), "after_sha256": hashlib.sha256(after).hexdigest()})
    if args.apply:
        evidence["backup"] = str(backup.relative_to(root))
        report = args.report or root / ("docs/audits/" + datetime.now().strftime("%Y-%m-%d-policy-sync-%H%M%S.json"))
        report.parent.mkdir(parents=True, exist_ok=True)
        report.write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(evidence, ensure_ascii=True, indent=2))


if __name__ == "__main__":
    main()
