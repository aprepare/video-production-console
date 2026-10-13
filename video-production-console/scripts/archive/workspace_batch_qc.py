"""Read-only verification of approved text, prepared lines and narration assets."""
from __future__ import annotations
import argparse, difflib, hashlib, json, re, sqlite3, subprocess
from pathlib import Path
from produce_workspace_batch import ROOT,WORK,STATE


def current_asset(db, project_id, typ):
 row=db.execute("SELECT path,state FROM asset_versions WHERE project_id=? AND type=? ORDER BY version DESC,created_at DESC LIMIT 1",(project_id,typ)).fetchone()
 if not row or row[1]!='ready' or not Path(row[0]).is_file():return None
 return Path(row[0])


def main():
 ap=argparse.ArgumentParser();ap.add_argument('--account');ap.add_argument('--narration',action='store_true');a=ap.parse_args()
 data=json.loads(STATE.read_text('utf-8'));db=sqlite3.connect((ROOT/'video-console-data/console.db').as_uri()+'?mode=ro',uri=True)
 failed=False
 for item in data['items']:
  if a.account and item['account']!=a.account:continue
  source=current_asset(db,item['project_id'],'continuous_script');spoken=current_asset(db,item['project_id'],'spoken_script')
  if not source or not spoken:
   print(json.dumps({'account':item['account'],'ready':False},ensure_ascii=False),flush=True);failed=True;continue
  command=[str(ROOT/'artifacts/spoken-qc.exe'),'--script',str(source),'--spoken',str(spoken)]
  done=subprocess.run(command,capture_output=True,text=True,encoding='utf-8',timeout=30)
  if done.returncode not in (0,2):raise RuntimeError(done.stderr[:1000])
  report=json.loads(done.stdout);report.update(account=item['account'],project_id=item['project_id'],source_path=str(source),spoken_path=str(spoken))
  report['original_body_preserved']=hashlib.sha256(source.read_text('utf-8-sig').strip().encode()).hexdigest()==item['body_sha256']
  mismatches=[]
  if not report['preserved']:
   for op,x,y,u,v in difflib.SequenceMatcher(None,report['expected'],report['actual'],autojunk=False).get_opcodes():
    if op!='equal':mismatches.append({'operation':op,'expected':report['expected'][max(0,x-10):min(len(report['expected']),y+10)],'actual':report['actual'][max(0,u-10):min(len(report['actual']),v+10)]})
  report['differences']=mismatches
  if a.narration:
   assets={typ:current_asset(db,item['project_id'],typ) for typ in ('narration','subtitle_srt','word_timing')}
   report['narration_assets_present']=all(assets.values())
   if all(assets.values()):
    srt=assets['subtitle_srt'].read_text('utf-8-sig');lines=[line for line in srt.splitlines() if line.strip() and not line.strip().isdigit() and '-->' not in line]
    check=WORK/(item['account']+'-srt-text.txt');check.write_text('\n'.join(lines),encoding='utf-8')
    srtcheck=subprocess.run([str(ROOT/'artifacts/spoken-qc.exe'),'--script',str(source),'--spoken',str(check)],capture_output=True,text=True,encoding='utf-8',timeout=30)
    srtq=json.loads(srtcheck.stdout);report['srt_preserved']=srtq['preserved'];report['caption_count']=srt.count('-->')
    timing=json.loads(assets['word_timing'].read_text('utf-8-sig'));report['timing_words']=len(timing.get('words',[]));report['narration_bytes']=assets['narration'].stat().st_size
    report['asset_paths']={k:str(v) for k,v in assets.items()}
    if not srtq['preserved']:failed=True;report['srt_differences']=[(op,srtq['expected'][x:y],srtq['actual'][u:v]) for op,x,y,u,v in difflib.SequenceMatcher(None,srtq['expected'],srtq['actual'],autojunk=False).get_opcodes() if op!='equal'][:20]
   else:failed=True
  (WORK/(item['account']+'-text-qc.json')).write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf-8')
  if not report['preserved'] or not report['original_body_preserved']:failed=True
  print(json.dumps({k:v for k,v in report.items() if k not in ('expected','actual','source_path','spoken_path','asset_paths')},ensure_ascii=False),flush=True)
 if failed:raise SystemExit(2)

if __name__=='__main__':main()
