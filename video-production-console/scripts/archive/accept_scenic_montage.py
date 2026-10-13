"""Exercise real montage generation in isolated workspaces, never register drafts."""
from __future__ import annotations
import argparse, hashlib, json, sqlite3, subprocess, uuid
from pathlib import Path


def main():
    ap=argparse.ArgumentParser();ap.add_argument('--exe',type=Path,required=True);ap.add_argument('--output',type=Path,required=True);a=ap.parse_args()
    repo=Path.cwd();db=sqlite3.connect((repo/'video-console-data/console.db').as_uri()+'?mode=ro',uri=True)
    settings=dict(db.execute('select key,value from settings'))
    rows=list(db.execute("select t.id,t.project_id,p.title,t.manifest_path from codex_tasks t join projects p on p.id=t.project_id where t.action='montage.execute' and t.status='completed' order by t.created_at desc limit 30"))
    chosen=[]
    for term in ['回村','存款','五年后']:
        row=next((r for r in rows if term in r[2] and r not in chosen),None)
        if row:chosen.append(row)
    for row in rows:
        if len(chosen)>=3:break
        if row not in chosen:chosen.append(row)
    if not chosen:raise SystemExit('No completed montage inputs available')
    profile=Path(settings['machine_profile_path']);digest_before=hashlib.sha256(profile.read_bytes()).hexdigest()
    results=[]
    for source_task,project,title,path in chosen:
        manifest=json.loads(Path(path).read_text('utf-8-sig'));job=str(uuid.uuid4())
        home=a.output/'projects'/project/'tasks'/job;out=home/'output';out.mkdir(parents=True,exist_ok=True)
        manifest.update(task_id=job,job_id=job,output_dir=str(out))
        ns=manifest['non_secret_settings'];ns.update(mix_preset='scenic_semantic',montage_plan_version='2.0',data_root=str(a.output))
        mp=home/'task_manifest.json';mp.write_text(json.dumps(manifest,ensure_ascii=False,indent=2),encoding='utf-8')
        last=home/'output-last-message.json'
        cmd=[str(a.exe.resolve()),'montage-script-run','--manifest',str(mp),'--skill-root',str(Path.home()/'.codex/skills/jianying-montage-draft'),'--output-last-message',str(last),'--python-binary',str(Path(__import__('sys').executable))]
        process=subprocess.run(cmd,cwd=repo,capture_output=True,timeout=240)
        (home/'runtime.stdout.log').write_bytes(process.stdout);(home/'runtime.stderr.log').write_bytes(process.stderr)
        result=json.loads(last.read_text('utf-8')) if last.exists() else {'status':'failed','summary':'missing result envelope'}
        qc_path=out/'scenic_selection_qc.json';qc=json.loads(qc_path.read_text('utf-8')) if qc_path.exists() else {}
        entry={'title':title,'source_task_id':source_task,'acceptance_task_id':job,'status':result.get('status'),'summary':result.get('summary'),'workspace':str(home),'qc':qc}
        results.append(entry);print(json.dumps({k:v for k,v in entry.items() if k!='qc'},ensure_ascii=False),flush=True)
    unchanged=hashlib.sha256(profile.read_bytes()).hexdigest()==digest_before
    report={'machine_profile_unchanged':unchanged,'registered_into_jianying':False,'samples':results}
    a.output.mkdir(parents=True,exist_ok=True);(a.output/'acceptance_results.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf-8')
    print(json.dumps({'passed':sum(r['status']=='completed' for r in results),'samples':len(results),'machine_profile_unchanged':unchanged}),flush=True)
    if not unchanged or any(r['status']!='completed' for r in results):raise SystemExit(1)
if __name__=='__main__':main()
