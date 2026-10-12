"""Install the approved single-writer workflow. Dry run unless --apply is given."""
import copy
import hashlib
import json
import sys
from datetime import datetime
from pathlib import Path

root = Path.cwd().resolve()
data = root / 'video-console-data/remix_lab'
baseline = json.loads((root / 'docs/audits/2026-09-10-remix-v43-export.json').read_text(encoding='utf-8'))
system = (root / 'docs/prompts/remix-single-writer-v5.md').read_text(encoding='utf-8').strip()
rules = baseline['editorial_rules']
course = rules[rules.index('【真实课程资料】'):rules.index('【发布字段要求】')].strip()
publishing = rules[rules.index('【发布字段要求】'):].strip()
public = course + '\n\n' + publishing
user = '按本次要求完成二创：先内部纠错和拆解，锁定有效头尾，中段深度重构，内部自查后只交付系统要求的JSON。\n{{NOTES}}\n# 同行原文\n{{SOURCE}}'
version = '头尾锁定v5.0 单写手·中段深度重构 2026-09-10'
paths = [data / n for n in ('workflow.json', 'active_prompt.json', 'library.json')]
paths += sorted((data / 'workflows').glob('*.json'))
changes = []
for path in paths:
    before = path.read_bytes()
    old = json.loads(before.decode('utf-8-sig'))
    new = copy.deepcopy(old)
    if 'nodes' in new:
        removed = {n['id'] for n in new['nodes'] if n['type'] in ('reviewer', 'selfcheck') or (n['type'] == 'agent' and n['id'] == 'hook' and n.get('config', {}).get('role') != 'reference')}
        kept = [n for n in new['nodes'] if n['id'] not in removed]
        assert sorted(n['type'] for n in kept) == ['input', 'output', 'writer'], 'Unexpected additional nodes; inspect before changing'
        for n in kept:
            if n['type'] == 'writer':
                writer = n
                n['config']['system_prompt'] = system
                n['config']['user_template'] = user
            elif n['type'] == 'input':
                source = n
            else:
                final = n
        new['nodes'] = kept
        new['edges'] = [[source['id'], writer['id']], [writer['id'], final['id']]]
        new['editorial_rules'] = public
        new['name'] = '原文 → 写手 → 定稿'
        # Prove model controls and production settings survive the migration.
        def controls(nodes):
            nodes = copy.deepcopy(nodes)
            for n in nodes:
                n.get('config', {}).pop('system_prompt', None)
                n.get('config', {}).pop('user_template', None)
            return nodes
        assert controls(kept) == controls([n for n in old['nodes'] if n['id'] not in removed])
        assert new.get('production') == old.get('production')
    elif path.name == 'active_prompt.json':
        new.update(name='头尾锁定 · 单写手深度二创', stamp=version, system=system+'\n\n'+public, user=user)
    else:
        for p in new.get('prompts', []):
            if p.get('id') in ('elder_stable', 'bone_flesh'):
                p.update(name='头尾锁定 · 单写手深度二创', stamp=version, system=system+'\n\n'+public, user=user)
    if old != new:
        after = (json.dumps(new, ensure_ascii=False, indent=2)+'\n').encode('utf-8')
        changes.append((path, before, after))

apply = '--apply' in sys.argv
backup = data / ('backup-single-writer-' + datetime.now().strftime('%Y%m%d-%H%M%S'))
report = {'version': version, 'applied': apply, 'files': []}
for path, before, _ in changes:
    assert path.read_bytes() == before, f'Concurrent edit: {path}'
for path, before, after in changes:
    if apply:
        assert path.read_bytes() == before, f'Concurrent edit: {path}'
        dest = backup / path.relative_to(data)
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_bytes(before)
        tmp = path.with_suffix('.single-writer.tmp')
        tmp.write_bytes(after)
        tmp.replace(path)
        assert path.read_bytes() == after
    report['files'].append({'path': str(path.relative_to(data)), 'before_sha256': hashlib.sha256(before).hexdigest(), 'after_sha256': hashlib.sha256(after).hexdigest()})
if apply:
    report['backup'] = str(backup)
    (root/'docs/audits/2026-09-10-single-writer-sync.json').write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n', encoding='utf-8')
print(json.dumps({'version': version, 'applied': apply, 'changed_files': len(changes), 'backup': str(backup) if apply else None}, ensure_ascii=True))
