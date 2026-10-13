"""Continue four real console montage tasks serially, with bounded polling.
Stops on a failed task instead of retrying paid model calls or overwriting work.
"""
from __future__ import annotations
import json,time
from pathlib import Path
from produce_workspace_batch import ROOT,WORK,STATE,session,api,save,start_task


def log(value):
 line=json.dumps(value,ensure_ascii=False)
 print(line,flush=True)
 with (WORK/'production-events.jsonl').open('a',encoding='utf-8') as f:f.write(line+'\n')


def main():
 s=session();data=json.loads(STATE.read_text('utf-8'))
 for item in data['items']:
  qc=json.loads((WORK/(item['account']+'-text-qc.json')).read_text('utf-8'))
  if not all(qc.get(key) for key in ('preserved','original_body_preserved','narration_assets_present','srt_preserved')):
   raise RuntimeError('Text/narration QC not passed for '+item['account'])
  if not item.get('montage_task_id'):
   task=start_task(s,item,'montage.execute');item['montage_task_id']=task['id'];save(data)
  tid=item['montage_task_id'];out=ROOT/'video-console-data/projects'/item['project_id']/'tasks'/tid/'output'
  log({'account':item['account'],'task_id':tid,'phase':'monitoring'})
  deadline=time.monotonic()+1800;previous=None;last_notice=0
  while time.monotonic()<deadline:
   task=api(s,'GET','/api/tasks/'+tid);status=task['status']
   phase={'account':item['account'],'task_id':tid,'status':status,'model_choices_written':(out/'scenic_model_selection.json').exists(),'plan_written':(out/'production_plan.json').exists(),'draft_validated':(out/'draft.validation.json').exists()}
   if phase!=previous or time.monotonic()-last_notice>90:log(phase);previous=phase;last_notice=time.monotonic()
   if status=='completed':
    report=json.loads((out/'draft.validation.json').read_text('utf-8-sig'))
    errors=[f for f in report.get('findings',[]) if f.get('level')=='error']
    if errors:raise RuntimeError('Completed task has draft validation errors')
    item['montage_completed']=True;item['output_dir']=str(out);save(data)
    log({'account':item['account'],'phase':'completed','video_segments':report.get('video_segments'),'duration_s':report.get('duration_s'),'editorial':report.get('editorial')})
    break
   if status in ('failed','canceled','cancelled','awaiting_input'):
    raise RuntimeError(json.dumps({'account':item['account'],'task_id':tid,'status':status,'summary':task.get('result_summary'),'error':task.get('error_message'),'code':task.get('error_code')},ensure_ascii=False))
   time.sleep(12)
  else:raise TimeoutError('Montage did not finish within bounded monitor window: '+tid)
 log({'batch':'七万亿换挡','completed':len(data['items'])})

if __name__=='__main__':main()
