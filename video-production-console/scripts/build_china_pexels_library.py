"""Download an attributed, resumable China B-roll library for montage.
API credentials are read from PEXELS_API_KEY only, never saved. Search metadata
is geographical evidence, not a visual verification of every video's location.
"""
from __future__ import annotations
import argparse, collections, concurrent.futures, hashlib, html, json, os, re, shutil, subprocess, threading, time
from pathlib import Path
from urllib.parse import urlsplit
import requests

QUERIES = [
 'China village','China countryside','China rural house','Chinese village architecture',
 'China rice fields','China farming','China farmland','China old town',
 'China apartment buildings','China residential buildings','China construction',
 'Shanghai skyline','Shenzhen city','Chongqing city','Guangzhou city',
 'Beijing street','Chengdu street','Hangzhou city','Suzhou water town',
 'China city traffic','China bridge','China train','China railway station',
 'China market','China street life','China factory','China workers',
 'Guilin countryside','Yangshuo village','Yunnan village','Fujian tulou',
 'Guizhou village','Wuzhen','Hongcun','China river','China mountains',
 'China traditional architecture','Hong Kong residential','China buildings',
 'China elderly park','China city rain','China rural road','China agriculture',
 'Longji rice terraces','Longsheng village','Yuanyang rice terraces','Hongcun village',
 'Xidi village','Wuyuan village','Zhaoxing village','Xijiang village','Shaxi Yunnan',
 'Zhouzhuang water town','Tongli water town','Fenghuang ancient town','Furong village China',
 'Chinese countryside','rural China','China rice terraces','China housing estate',
 'Hong Kong apartments','Chongqing residential buildings','Guangzhou residential',
 'Chinese farm','China village aerial','China traditional houses','Jiangnan village',
 'China village street','China village river','China rural architecture','China construction workers',
]
CHINA = re.compile(r'\b(china|shanghai|beijing|shenzhen|guangzhou|chongqing|chengdu|hangzhou|suzhou|nanjing|wuhan|tianjin|xiamen|qingdao|guilin|yangshuo|zhangjiajie|yunnan|guizhou|fujian|sichuan|zhejiang|jiangsu|guangdong|guangxi|anhui|hunan|hubei|henan|hebei|shandong|shanxi|shaanxi|xinjiang|tibet|lhasa|hong kong|macau|macao|wuzhen|hongcun|fenghuang|dali|lijiang|pudong|canton tower|bund|xi an|xian|kunming|guiyang|huangshan|zhuhai|dongguan|fuzhou|ningbo|foshan|sanya|haikou|hainan|dunhuang|quanzhou|wuxi|zhangzhou|datong|wuyuan|suzhou|shaoxing|chaozhou|tiananmen|longji|longsheng|yuanyang|xidi|zhaoxing|xijiang|shaxi|zhouzhuang|tongli|jiangnan|furong)\b',re.I)
FOREIGN = re.compile(r'\b(japan|japanese|tokyo|kyoto|osaka|korea|korean|seoul|vietnam|vietnamese|hanoi|thailand|thai|bangkok|indonesia|bali|singapore|malaysia|taiwan|taipei|america|usa|new york|london|paris|germany|german|italy|italian|france|french|switzerland|swiss|australia|sydney|canada|toronto|san francisco|chinatown|lunar new year|dragon dance|lion dance)\b',re.I)
THEMES = [
 ('Rural_Life',r'village|rural|countryside|farmland|farm|rice|paddy|agricultur|terrace|harvest|hongcun|tulou', ['农村','乡村','田园','土地','回村','农业']),
 ('Housing_Residential',r'\b(apartment|residential|housing|house|home|neighborhood|estate|dwelling|tenement)s?\b', ['房子','住宅','楼房','买房','住房','家庭']),
 ('Construction_Architecture',r'construction|building|architecture|skyscraper|tower|urban development', ['建筑','楼宇','建设','城市','房产']),
 ('Transport_Infrastructure',r'traffic|train|railway|station|bridge|highway|road|port|metro|transport|ship', ['交通','道路','车流','流动','基础设施','发展']),
 ('City_Business',r'city|skyline|urban|street|downtown|night|pudong|bund|hong kong|shanghai|shenzhen', ['城市','经济','发展','繁华','财富','商业']),
 ('Community_Life',r'market|people|park|walking|shopping|worker|elderly|tai chi|family', ['生活','普通人','家庭','养老','社区']),
 ('Traditional_Architecture',r'temple|ancient|historic|traditional|pagoda|palace|wall|old town|heritage', ['传统建筑','老街','历史','家乡','积累']),
 ('Nature_Landscape',r'river|mountain|lake|forest|water|landscape|sunset|sunrise|nature|valley|scenic|waterfall', ['风景','山水','河流','变化','时间']),
]
_tls=threading.local()
def session():
 if not hasattr(_tls,'session'):
  _tls.session=requests.Session()
 return _tls.session

def atomic(path:Path,data):
 path.parent.mkdir(parents=True,exist_ok=True)
 tmp=path.with_name(path.name+'.tmp');tmp.write_text(json.dumps(data,ensure_ascii=False,indent=2),encoding='utf-8');os.replace(tmp,path)

def fetch(query,page,cache,key):
 p=cache/(hashlib.sha256(f'{query}:{page}'.encode()).hexdigest()+'.json')
 if p.exists():return json.loads(p.read_text('utf-8'))
 for attempt in range(3):
  try:
   # The documented v1 endpoint is preferred; legacy remains supported.
   base='https://api.pexels.com/v1/videos/search' if attempt==0 else 'https://api.pexels.com/videos/search'
   resp=session().get(base,params={'query':query,'per_page':80,'page':page},headers={'Authorization':key},timeout=(12,35))
   if resp.status_code==429:raise RuntimeError('Pexels quota reached; resume after the server reset')
   if resp.status_code in (401,403):raise RuntimeError('Pexels authorization refused')
   resp.raise_for_status();d=resp.json();atomic(p,d);return d
  except RuntimeError:raise
  except (requests.RequestException,ValueError):
   if attempt==2:return {'videos':[],'search_error':True}
   time.sleep(1+attempt)

def title_of(v):return re.sub(r'-\d+/?$','',urlsplit(v['url']).path.rsplit('/video/',1)[-1]).strip('/').replace('-',' ')
def themes(title):
 groups=[];tags=['中国','中国相关','China']
 for name,rx,words in THEMES:
  if re.search(rx,title,re.I):groups.append(name);tags+=words
 priority={'Rural_Life':0,'Housing_Residential':1,'Traditional_Architecture':2,'Construction_Architecture':3,'Transport_Infrastructure':4,'Community_Life':5,'City_Business':6,'Nature_Landscape':7}
 groups.sort(key=lambda g:priority[g])
 return (groups or ['China_General']),list(dict.fromkeys(tags))
def media_file(v):
 files=[f for f in v.get('video_files',[]) if f.get('file_type')=='video/mp4' and min(f.get('width') or 0,f.get('height') or 0)>=720 and max(f.get('width') or 0,f.get('height') or 0)<=2048 and (f.get('size') or 0)<=85*1024*1024]
 if not files:return None
 return min(files,key=lambda f:(0 if min(f['width'],f['height'])>=1080 and (f.get('size') or 0)<=45*1024*1024 else 1, f.get('size') or f['width']*f['height'],abs((f.get('fps') or 30)-30)))
def validate(path,ffmpeg,duration):
 # Three decoded grayscale frames catch truncated/unreadable media and provide
 # perceptual fingerprints. This is sample QC, not a full-frame content review.
 hashes=[]
 for frac in (.1,.5,.9):
  cp=subprocess.run([ffmpeg,'-v','error','-ss',str(max(0,duration*frac)),'-i',str(path),'-frames:v','1','-vf','scale=9:8,format=gray','-f','rawvideo','pipe:1'],capture_output=True,timeout=25)
  if cp.returncode or len(cp.stdout)!=72:raise ValueError('frame_decode_failed')
  b=cp.stdout;bits=0
  for y in range(8):
   for x in range(8):bits=(bits<<1)|int(b[y*9+x]>b[y*9+x+1])
  hashes.append(f'{bits:016x}')
 return hashes

def download(row,root,ffmpeg):
 v=row['video'];f=row['file'];p=root/'originals/broll/pexels_china_20260909'/row['category']/f'pexels_{v["id"]}.mp4';p.parent.mkdir(parents=True,exist_ok=True)
 side=p.with_suffix('.json')
 if p.exists() and side.exists():
  prior=json.loads(side.read_text('utf-8'))
  if prior.get('size_bytes')==p.stat().st_size and prior.get('frame_hashes'):return prior
 part=p.with_suffix('.mp4.part')
 for attempt in range(3):
  try:
   with session().get(f['link'],stream=True,timeout=(15,60)) as resp:
    resp.raise_for_status();expected=int(resp.headers.get('content-length') or 0);n=0;sha=hashlib.sha256()
    with part.open('wb') as out:
     for block in resp.iter_content(256*1024):
      if block:out.write(block);sha.update(block);n+=len(block)
      if n>100*1024*1024:raise ValueError('download_size_limit')
    if n<10000 or (expected and n!=expected):raise ValueError('download_length_mismatch')
   hs=validate(part,ffmpeg,float(v['duration']));os.replace(part,p)
   entry={'id':f'pexels-{v["id"]}','pexels_id':v['id'],'kind':'broll','category':row['category'],'tags':row['tags'],'summary':row['title'],
    'relative_path':p.relative_to(root).as_posix(),'duration_seconds':float(v['duration']),'width':f['width'],'height':f['height'],'fps':f.get('fps',30),
    'origin':'pexels','source_url':v['url'],'download_url':f['link'],'creator':v.get('user',{}).get('name',''),'creator_url':v.get('user',{}).get('url',''),
    'license':'Pexels License','license_url':'https://www.pexels.com/license/','geography':'china_metadata','geography_evidence':'Pexels page title: '+row['title'],
    'visual_review':'sample_decode_only','search_queries':row['queries'],'orientation':'portrait' if f['height']>f['width'] else 'landscape',
    'sha256':sha.hexdigest(),'size_bytes':n,'frame_hashes':hs,'poster_url':v.get('image',''),'downloaded_at':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime())}
   atomic(side,entry);return entry
  except Exception as e:
   if attempt==2:return {'pexels_id':v['id'],'error':type(e).__name__,'reason':str(e)[:140] if not isinstance(e,requests.RequestException) else 'network_error'}
   time.sleep(2+attempt*2)
 return {}

def main():
 ap=argparse.ArgumentParser();ap.add_argument('--root',type=Path,required=True);ap.add_argument('--target',type=int,default=520);ap.add_argument('--workers',type=int,default=8);a=ap.parse_args()
 key=os.environ.get('PEXELS_API_KEY','').strip()
 if not key:raise SystemExit('PEXELS_API_KEY environment variable is required')
 if not 500<=a.target<=1000:raise SystemExit('target must be 500..1000')
 if shutil.disk_usage(a.root).free<20*1024**3:raise SystemExit('At least 20 GiB free disk is required')
 import imageio_ffmpeg
 ffmpeg=imageio_ffmpeg.get_ffmpeg_exe();out=a.root/'00_INDEX/pexels_china_20260909';cache=out/'search_cache';cache.mkdir(parents=True,exist_ok=True)
 pool={};errors=0
 searches=[(q,1) for q in QUERIES]+[('China',i) for i in range(1,16)]+[('Chinese architecture',i) for i in range(1,5)]+[('China village',i) for i in range(2,5)]
 for n,(q,page) in enumerate(searches):
  d=fetch(q,page,cache,key);errors+=bool(d.get('search_error'))
  for v in d.get('videos',[]):
   title=title_of(v);file=media_file(v)
   if not file or not 6<=v.get('duration',0)<=70 or not CHINA.search(title) or FOREIGN.search(title):continue
   if v['id'] in pool:pool[v['id']]['queries'].append(q);continue
   groups,tags=themes(title);pool[v['id']]={'video':v,'title':title,'file':file,'category':groups[0],'tags':tags,'queries':[q]}
  if n%5==0:print(json.dumps({'phase':'search','requests':n+1,'unique_candidates':len(pool),'errors':errors}),flush=True)
 atomic(out/'candidates.json',list(pool.values()))
 buckets=collections.defaultdict(list)
 for row in pool.values():buckets[row['category']].append(row)
 for rows in buckets.values():rows.sort(key=lambda x:(x['file'].get('size') or 0,x['video']['id']))
 chosen=[];author_counts=collections.Counter();portrait_count=0
 while buckets:
  for cat in list(buckets):
   rows=buckets[cat]
   while rows:
    row=rows.pop(0);author=str(row['video'].get('user',{}).get('id',''))
    if author_counts[author]>=400:continue
    portrait=row['file']['height']>row['file']['width']
    if portrait and portrait_count>=80:continue
    portrait_count+=int(portrait)
    chosen.append(row);author_counts[author]+=1;break
   if not rows:del buckets[cat]
 atomic(out/'selected.json',chosen)
 print(json.dumps({'phase':'download','candidates':len(chosen),'target':a.target}),flush=True)
 ready=[];failed=[];seen=set();offset=0
 while len(ready)<a.target and offset<len(chosen):
  batch=chosen[offset:offset+min(80,a.target-len(ready))];offset+=len(batch)
  with concurrent.futures.ThreadPoolExecutor(max_workers=min(a.workers,12)) as executor:
   for entry in executor.map(lambda row:download(row,a.root,ffmpeg),batch):
    if entry.get('error'):failed.append(entry)
    elif entry.get('sha256') in seen:failed.append({'pexels_id':entry['pexels_id'],'error':'exact_duplicate'})
    else:seen.add(entry['sha256']);ready.append(entry)
    atomic(out/'download_progress.json',{'phase':'download','ready':len(ready),'failed':len(failed),'target':a.target,'bytes':sum(x['size_bytes'] for x in ready)})
    if (len(ready)+len(failed))%20==0:print(json.dumps({'ready':len(ready),'failed':len(failed),'GiB':round(sum(x['size_bytes'] for x in ready)/1024**3,2)}),flush=True)
  atomic(out/'manifest.json',ready);atomic(out/'failures.json',failed)
 # A dedicated supplemental index avoids concurrent edits of the legacy index.
 atomic(a.root/'00_INDEX/china_scenic_index.json',ready)
 counts=dict(collections.Counter(x['category'] for x in ready));ori=dict(collections.Counter(x['orientation'] for x in ready))
 summary={'phase':'completed' if len(ready)>=a.target else 'partial','target':a.target,'downloaded_unique':len(ready),'categories':counts,'orientation':ori,'bytes':sum(x['size_bytes'] for x in ready),'search_requests':len(searches),'failed':len(failed),'geography_basis':'source title, not manual location verification','quality_checks':'byte length + SHA256 + 3 decoded frames'}
 atomic(out/'summary.json',summary)
 cards=[]
 for x in ready:
  path=html.escape((a.root/x['relative_path']).as_uri(),quote=True);source=html.escape(x['source_url'],quote=True)
  cards.append('<article><video controls preload="none" poster="'+html.escape(x['poster_url'],quote=True)+'" src="'+path+'"></video><p>'+html.escape(x['summary'])+'</p><p>'+html.escape(x['category'])+' · '+str(x['width'])+'×'+str(x['height'])+'</p><a href="'+source+'">Video by '+html.escape(x['creator'])+' on Pexels</a></article>')
 (out/'review.html').write_text('<!doctype html><meta charset="utf-8"><title>China montage library</title><style>body{font-family:system-ui;margin:24px}main{display:grid;grid-template-columns:repeat(4,1fr);gap:18px}video{width:100%;height:180px;background:#eee}article{border:1px solid #ccc;padding:10px}</style><h1>中国意象素材库 · '+str(len(ready))+' 条</h1><p><a href="https://www.pexels.com">Videos provided by Pexels</a> · 来源地名筛选，非逐条人工地点认证。人物与地标仅作意象，不代表新闻当事人或真实事件。</p><main>'+''.join(cards)+'</main>',encoding='utf-8')
 print(json.dumps(summary,ensure_ascii=False),flush=True)
 if len(ready)<500:raise SystemExit(2)
if __name__=='__main__':main()
