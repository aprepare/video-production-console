"""Resumable local production of the four approved 七万亿换挡 scripts.
Uses the console HTTP API and its local session file. Never logs credentials.
Writes progress immediately after each successful mutation, never publishes.
"""
from __future__ import annotations
import argparse, hashlib, http.cookiejar, json, re, time
from pathlib import Path
import requests

ROOT = Path(__file__).resolve().parent.parent
WORK = Path.home() / '.agentdock/workspaces/qiwanyi-four-20260909'
SOURCE = ROOT / '二创工作区/项目/2026-09-09-七万亿换挡'
STATE = WORK / 'batch.json'
ACCOUNTS = [
 ('认知漫步', '认知漫步', '04bbe3a0-2496-47b8-acb7-9c1862ef6850'),
 ('财经漫游', '财经漫游', 'c7a0d74a-4617-47a5-881b-7f8d44758115'),
 ('财富漫游', '云中观局', '8c232ac1-a2e2-424e-866e-77fe8be1195d'),
 ('认知研习', '认知研习', 'd57748b3-6218-49a9-8239-a5ea7bd2ccf9'),
]

def save(data):
 WORK.mkdir(parents=True,exist_ok=True)
 tmp=STATE.with_suffix('.tmp');tmp.write_text(json.dumps(data,ensure_ascii=False,indent=2),encoding='utf-8');tmp.replace(STATE)

def session():
 session_dir=ROOT/'video-console-data'/'sessions'
 jar=http.cookiejar.MozillaCookieJar(str(session_dir/'console_cookies.txt'));jar.load(ignore_discard=True,ignore_expires=True)
 s=requests.Session();s.trust_env=False
 for cookie in jar:s.cookies.set(cookie.name,cookie.value,domain='127.0.0.1',path='/')
 s.headers['X-CSRF-Token']=(session_dir/'csrf.txt').read_text('utf-8').strip()
 return s

def api(s,method,path,**kw):
 response=s.request(method,'http://127.0.0.1:2030'+path,timeout=kw.pop('timeout',180),**kw)
 if response.status_code>=400:
  # API errors contain production diagnostics, not request headers.
  raise RuntimeError(f'{method} {path}: HTTP {response.status_code}: {response.text[:1600]}')
 return response.json() if response.content else None

def body_from_markdown(text):
 sections=re.split(r'^\s*(?:---|\*\*\*)\s*$',text,flags=re.M)
 if len(sections)!=3:raise ValueError('Expected a metadata header followed by approved prose')
 body=sections[2].strip()
 if len(body)<1000 or '拟发账号' in body or re.search(r'^[甲乙]：',body,re.M):raise ValueError('Unexpected script body')
 return body

def initialize(s):
 if STATE.exists():data=json.loads(STATE.read_text('utf-8'))
 else:data={'batch':'七万亿换挡-20260909','created_at':time.strftime('%Y-%m-%dT%H:%M:%S'),'items':[]}
 publication=(SOURCE/'发布包_2026-09-09_2005.md').read_text('utf-8-sig')
 for label,account,aid in ACCOUNTS:
  item=next((i for i in data['items'] if i['account_id']==aid),None)
  if item is None:
   files=list(SOURCE.glob(f'成稿_*_{label}_不挂车_改1.md'))
   if len(files)!=1:raise RuntimeError(f'Ambiguous approved file for {label}')
   source=files[0];body=body_from_markdown(source.read_text('utf-8-sig'))
   section=next(x for x in re.split(r'^## ',publication,flags=re.M) if x.startswith(label))
   titles=[re.sub(r'\*\*','',m.group(1)).strip() for m in re.finditer(r'^\d+\.\s+(.+?)（',section,re.M)]
   if len(titles)!=3:raise ValueError(f'Expected three publishing titles for {label}')
   description=re.search(r'^描述：(.+)$',section,re.M).group(1)
   topics=[x.strip() for x in re.search(r'^标签：(.+)$',section,re.M).group(1).split('#') if x.strip()]
   cta=re.search(r'^置顶评论：(.+)$',section,re.M).group(1)
   item={'file_account':label,'account':account,'account_id':aid,'source_file':str(source),'source_sha256':hashlib.sha256(source.read_bytes()).hexdigest(),'body_sha256':hashlib.sha256(body.encode()).hexdigest(),'title':titles[0],'body_chars':len(body),'titles':titles,'description':description,'topics':topics,'cta':cta}
   data['items'].append(item);save(data)
  body=body_from_markdown(Path(item['source_file']).read_text('utf-8-sig'))
  if hashlib.sha256(body.encode()).hexdigest()!=item['body_sha256']:raise RuntimeError('Approved source changed mid-batch')
  if not item.get('project_id'):
   # Recover after a lost create response without duplicating an existing project.
   projects=api(s,'GET','/api/projects')
   rows=projects if isinstance(projects,list) else projects.get('projects',[])
   same=[p for p in rows if p.get('account_id')==aid and p.get('title')==item['title']]
   if len(same)>1:raise RuntimeError('Ambiguous existing project')
   project=same[0] if same else api(s,'POST','/api/projects',json={'account_id':aid,'title':item['title']})
   item['project_id']=project['id'];save(data)
  if not item.get('continuous_asset_id'):
   payload={'continuous_script':body,'titles':item['titles'],'short_titles':item['titles'],'descriptions':[item['description']],'topics':item['topics'],'cta':item['cta']}
   result=api(s,'POST',f"/api/projects/{item['project_id']}/assets/continuous_script",files={'file':('approved-script.json',json.dumps(payload,ensure_ascii=False).encode('utf-8'),'application/json')})
   item['continuous_asset_id']=result['id'];save(data)
   api(s,'PUT',f"/api/projects/{item['project_id']}/notes/remix",json={'notes':f"用户指定成稿直接制作，不重新二创。来源：{item['source_file']}\nSHA256：{item['source_sha256']}\n账号映射：{label} → {account}。不挂车；保留原稿的主页橱窗口播，本任务不自动发布。"})
  print(json.dumps({'account':account,'title':item['title'],'project_id':item['project_id'],'imported':True},ensure_ascii=False),flush=True)
 return data

def start_task(s,item,action):
 return api(s,'POST',f"/api/projects/{item['project_id']}/tasks",json={
  'account_id':item['account_id'],'type':'montage' if action=='montage.execute' else 'remix','action':action,'model':'grok-4.6-fast','reasoning_effort':'medium',
  'prompt':('按已确认连续成稿断成适合字幕的一句一行口播稿。只能断行，不能改写、删减、新增数字或单位，不要加入元数据。保留所有内容与原句顺序。' if action=='remix.spoken_lines' else '使用本项目定稿、配音、字幕和本账号背景生成完整剪映草稿。沿用当前模型意象选片、1倍速、较慢镜头、叠化和重点标识策略；不改写口播，不使用国外画面，不发布。')})

def main():
 ap=argparse.ArgumentParser();ap.add_argument('action',choices=['init','spoken','status','narration','montage']);ap.add_argument('--account');args=ap.parse_args();s=session()
 if args.action=='init':initialize(s);return
 data=json.loads(STATE.read_text('utf-8'));items=[i for i in data['items'] if not args.account or i['account']==args.account]
 if not items:raise RuntimeError('No matching account')
 if args.action in ('spoken','montage'):
  key='spoken_task_id' if args.action=='spoken' else 'montage_task_id';act='remix.spoken_lines' if args.action=='spoken' else 'montage.execute'
  for item in items:
   if not item.get(key):
    result=start_task(s,item,act);item[key]=result['id'];save(data)
   print(json.dumps({'account':item['account'],'task':item[key]},ensure_ascii=False),flush=True)
 elif args.action=='narration':
  for item in items:
   if item.get('narration_completed'):continue
   print(json.dumps({'account':item['account'],'phase':'narration_started'},ensure_ascii=False),flush=True)
   result=api(s,'POST',f"/api/projects/{item['project_id']}/narration",json={},timeout=1200)
   item['narration_result']=result;item['narration_completed']=True;save(data)
   print(json.dumps({'account':item['account'],'phase':'narration_completed','duration_seconds':result['duration_seconds'],'captions':result['captions'],'warnings':result.get('warnings',[])},ensure_ascii=False),flush=True)
 else:
  for item in items:
   row={'account':item['account'],'project_id':item['project_id']}
   for key in ('spoken_task_id','montage_task_id'):
    if item.get(key):
     t=api(s,'GET','/api/tasks/'+item[key]);row[key]={k:t.get(k) for k in ('id','status','started_at','finished_at','result_summary','error_code','error_message')}
   row['narration_completed']=item.get('narration_completed',False)
   print(json.dumps(row,ensure_ascii=False),flush=True)

if __name__=='__main__':main()
