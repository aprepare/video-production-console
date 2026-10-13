"""Roll the compiled v4.2 prompt authority into every remix_lab config file.

Run from the repository root:  python rollout_v42.py <policy-export.json> [--apply]

Per workflow (global + each account):
  * editorial_rules      <- policy.editorial_rules
  * hook (planner) node  <- added if missing (source->hook, hook->writer), prompts refreshed if present
  * writer node          <- role-only system prompt + user template; model/effort/tier/prompt_id untouched
  * reviewer node        <- role-only system prompt + user template; model grok-4.6-fast / high
Library + active prompt <- bundled writer system (role + shared policy), user template, version stamp
agent_prompts.json      <- hook_system / reviewer_system refreshed (facts/ammo untouched)
Historical run snapshots are never touched. Everything touched is copied to a backup dir first.
"""
import copy
import json
import shutil
import sys
from datetime import datetime
from pathlib import Path

REVIEWER_MODEL, REVIEWER_EFFORT = "grok-4.6-fast", "high"
PLANNER_MODEL, PLANNER_EFFORT = "claude-opus-4-6-thinking", "medium"


def load(path: Path):
    return json.loads(path.read_text(encoding="utf-8-sig"))


def dump(doc) -> bytes:
    return (json.dumps(doc, ensure_ascii=False, indent=2) + "\n").encode("utf-8")


def node_by(doc, **match):
    for node in doc.get("nodes", []):
        if all(node.get(k) == v for k, v in match.items()):
            return node
    return None


def update_workflow(doc, policy):
    doc = copy.deepcopy(doc)
    doc["editorial_rules"] = policy["editorial_rules"]
    nodes = doc["nodes"]
    source = node_by(doc, type="input")
    writer = node_by(doc, type="writer")
    reviewer = node_by(doc, type="reviewer")
    if source is None or writer is None:
        raise ValueError("workflow lacks input/writer node")

    hook = next((n for n in nodes if n.get("id") == "hook" and n.get("type") == "agent" and n.get("config", {}).get("role") != "reference"), None)
    if hook is None:
        if any(n.get("id") == "hook" for n in nodes):
            raise ValueError("node id 'hook' already used by a reference node")
        hook = {
            "id": "hook", "type": "agent", "title": "二创策划",
            "x": (source.get("x", 0) + writer.get("x", 600)) / 2 if writer.get("x", 0) > source.get("x", 0) else source.get("x", 0) + 320,
            "y": source.get("y", 190),
            "config": {"model": PLANNER_MODEL, "reasoning_effort": PLANNER_EFFORT, "service_tier": "default"},
        }
        nodes.insert(nodes.index(source) + 1, hook)
    cfg = hook.setdefault("config", {})
    cfg.setdefault("model", PLANNER_MODEL)
    cfg.setdefault("reasoning_effort", PLANNER_EFFORT)
    cfg.setdefault("service_tier", "default")
    hook["title"] = "二创策划"
    cfg.update(system_prompt=policy["planner_system"], user_template=policy["planner_user_template"],
               inject_title=policy["planner_inject_title"], inject_rule=policy["planner_inject_rule"])

    edges = [list(e) for e in doc.get("edges", [])]
    for edge in ([source["id"], "hook"], ["hook", writer["id"]], [source["id"], writer["id"]]):
        if edge not in edges:
            edges.append(edge)
    doc["edges"] = edges

    wcfg = writer.setdefault("config", {})
    wcfg["system_prompt"] = policy["writer_role"]
    wcfg["user_template"] = policy["writer_user"]

    if reviewer is not None:
        rcfg = reviewer.setdefault("config", {})
        rcfg["system_prompt"] = policy["reviewer_role"]
        rcfg["user_template"] = policy["reviewer_user_template"]
        rcfg["model"] = REVIEWER_MODEL
        rcfg["reasoning_effort"] = REVIEWER_EFFORT
        rcfg.setdefault("service_tier", "default")

    ids = {n["id"] for n in nodes}
    for a, b in doc["edges"]:
        if a not in ids or b not in ids:
            raise ValueError(f"edge {a}->{b} references unknown node")
    return doc


def update(path: Path, doc, policy):
    if path.name == "active_prompt.json":
        doc = copy.deepcopy(doc)
        doc.update(system=policy["writer_system"], user=policy["writer_user"], stamp=policy["version"])
        return doc
    if path.name == "library.json":
        doc = copy.deepcopy(doc)
        for prompt in doc.get("prompts", []):
            if prompt.get("id") in ("elder_stable", "bone_flesh"):
                prompt.update(system=policy["writer_system"], user=policy["writer_user"], stamp=policy["version"])
        return doc
    if path.name == "agent_prompts.json":
        doc = copy.deepcopy(doc)
        doc["hook_system"] = policy["planner_system"]
        doc["reviewer_system"] = policy["reviewer_role"]
        return doc
    if "nodes" in doc:
        return update_workflow(doc, policy)
    return doc


def summarize(path: Path, doc):
    if "nodes" not in doc:
        return path.name
    parts = []
    for n in doc["nodes"]:
        c = n.get("config", {})
        parts.append(f"{n['id']}:{n['type']}:{c.get('model','')}/{c.get('reasoning_effort','')}")
    rules_head = (doc.get("editorial_rules") or "").split("\n", 1)[0]
    return f"{path.name}  nodes=[{', '.join(parts)}]  edges={len(doc['edges'])}  rules={rules_head}"


def main():
    apply = "--apply" in sys.argv
    export = Path([a for a in sys.argv[1:] if not a.startswith("--")][0])
    policy = load(export)
    root = Path.cwd().resolve()
    data = root / "video-console-data" / "remix_lab"
    paths = [data / n for n in ("active_prompt.json", "library.json", "agent_prompts.json", "workflow.json")]
    paths += sorted((data / "workflows").glob("*.json"))
    changes = []
    for path in paths:
        if not path.exists():
            continue
        before = path.read_bytes()
        previous = load(path)
        updated = update(path, previous, policy)
        if updated == previous:
            print("unchanged:", path.relative_to(root))
            continue
        changes.append((path, before, dump(updated)))
        print("update   :", summarize(path, updated))
    if not apply:
        print(f"\ndry run: {len(changes)} file(s) would change. Re-run with --apply.")
        return
    backup = data / ("backup-before-prompt-rollout-" + datetime.now().strftime("%Y%m%d-%H%M%S"))
    for path, before, after in changes:
        if path.read_bytes() != before:
            raise RuntimeError(f"concurrent edit: {path}")
        target = backup / path.relative_to(data)
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, target)
        tmp = path.with_suffix(".v42.tmp")
        tmp.write_bytes(after)
        tmp.replace(path)
    print(f"\napplied {len(changes)} file(s); backup at {backup.relative_to(root)}")


if __name__ == "__main__":
    main()

