"""Sync only editorial text; preserve workflow topology, models and run snapshots.

Usage: python scripts/sync-remix-prompt-text.py EXPORT [--apply]
"""
import copy
import hashlib
import json
import sys
from datetime import datetime
from pathlib import Path


def update(doc, name, policy):
    result = copy.deepcopy(doc)
    if name == 'active_prompt.json':
        result.update(system=policy['writer_system'], user=policy['writer_user'], stamp=policy['version'])
    elif name == 'library.json':
        for p in result.get('prompts', []):
            if p.get('id') in ('elder_stable', 'bone_flesh'):
                p.update(system=policy['writer_system'], user=policy['writer_user'], stamp=policy['version'])
    elif name == 'agent_prompts.json':
        result.update(hook_system=policy['planner_system'], reviewer_system=policy['reviewer_role'])
    elif 'nodes' in result:
        result['editorial_rules'] = policy['editorial_rules']
        for n in result['nodes']:
            cfg = n.get('config', {})
            if n['type'] == 'agent' and n['id'] == 'hook' and cfg.get('role') != 'reference':
                cfg.update(system_prompt=policy['planner_system'], user_template=policy['planner_user_template'],
                           inject_title=policy['planner_inject_title'], inject_rule=policy['planner_inject_rule'])
            elif n['type'] == 'writer':
                cfg.update(system_prompt=policy['writer_role'], user_template=policy['writer_user'])
            elif n['type'] == 'reviewer':
                cfg.update(system_prompt=policy['reviewer_role'], user_template=policy['reviewer_user_template'])
        def controls(w):
            w = copy.deepcopy(w)
            w.pop('editorial_rules', None)
            for n in w['nodes']:
                for key in ('system_prompt', 'user_template', 'inject_title', 'inject_rule'):
                    n.get('config', {}).pop(key, None)
            return w
        assert controls(doc) == controls(result), 'Non-prompt settings changed'
    return result


def main():
    policy = json.loads(Path(sys.argv[1]).read_text(encoding='utf-8-sig'))
    data = Path('video-console-data/remix_lab').resolve()
    paths = [data / n for n in ('active_prompt.json', 'library.json', 'agent_prompts.json', 'workflow.json')]
    paths += sorted((data / 'workflows').glob('*.json'))
    changes = []
    for path in paths:
        if not path.exists():
            continue
        path.resolve().relative_to(data)
        before = path.read_bytes()
        old = json.loads(before.decode('utf-8-sig'))
        new = update(old, path.name, policy)
        if old != new:
            after = (json.dumps(new, ensure_ascii=False, indent=2) + '\n').encode('utf-8')
            changes.append((path, before, after))
    apply = '--apply' in sys.argv
    backup = data / ('backup-prompt-text-' + datetime.now().strftime('%Y%m%d-%H%M%S'))
    for path, before, _ in changes:
        assert path.read_bytes() == before, f'Concurrent edit: {path}'
    report = {'version': policy['version'], 'applied': apply, 'files': []}
    for path, before, after in changes:
        if apply:
            assert path.read_bytes() == before, f'Concurrent edit: {path}'
            dest = backup / path.relative_to(data)
            dest.parent.mkdir(parents=True, exist_ok=True)
            dest.write_bytes(before)
            tmp = path.with_suffix('.prompt.tmp')
            tmp.write_bytes(after)
            tmp.replace(path)
            assert path.read_bytes() == after
        report['files'].append({'path': str(path.relative_to(data)), 'before_sha256': hashlib.sha256(before).hexdigest(), 'after_sha256': hashlib.sha256(after).hexdigest()})
    if apply:
        report['backup'] = str(backup)
        out = Path('docs/audits/2026-09-10-remix-v43-sync.json')
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(json.dumps(report, ensure_ascii=True, indent=2))


if __name__ == '__main__':
    main()
