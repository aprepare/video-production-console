"""Re-probe downloaded media and publish exact, attributed montage indexes.
No API key or network request is needed. Existing legacy media are untouched.
"""
from __future__ import annotations
import argparse, collections, concurrent.futures, csv, hashlib, json, os, time
from pathlib import Path
from build_china_pexels_library import CHINA, FOREIGN
import pyJianYingDraft as draft
import threading

# MediaInfo changes process-global parsing options; keep probes serialized.
_probe_lock = threading.Lock()


def atomic(path, data):
    tmp=path.with_name(path.name+'.verify.tmp')
    tmp.write_text(json.dumps(data,ensure_ascii=False,indent=2),encoding='utf-8')
    os.replace(tmp,path)


def inspect(entry,root,full_hash):
    entry=dict(entry);path=root/entry['relative_path']
    try:
        if not path.is_file() or path.stat().st_size!=entry['size_bytes']:
            raise ValueError('size or file identity changed')
        if full_hash:
            h=hashlib.sha256()
            with path.open('rb') as f:
                for block in iter(lambda:f.read(1024*1024),b''):h.update(block)
            if h.hexdigest()!=entry['sha256']:raise ValueError('SHA256 mismatch')
        with _probe_lock:
            material=draft.VideoMaterial(str(path))
        if material.duration<=0 or min(material.width,material.height)<720:
            raise ValueError('invalid duration or below 720p')
        entry.setdefault('vendor_duration_seconds',entry['duration_seconds'])
        entry.update(duration_seconds=material.duration/1_000_000,width=material.width,height=material.height,orientation='portrait' if material.height>material.width else 'landscape',probe_backend='pyJianYingDraft.VideoMaterial',verified_at=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()))
        atomic(path.with_suffix('.json'),entry)
        return entry,None
    except Exception as e:return None,{'id':entry['id'],'error':type(e).__name__,'message':str(e)[:300]}


def main():
    ap=argparse.ArgumentParser();ap.add_argument('--root',type=Path,required=True);ap.add_argument('--full-hash',action='store_true');a=ap.parse_args()
    folder=a.root/'00_INDEX/pexels_china_20260909'
    # Include previously downloaded selections so resuming with improved query
    # balancing never leaves valid media orphaned outside the supplemental index.
    files=list((a.root/'originals/broll/pexels_china_20260909').glob('*/*.json'))
    entries=[json.loads(p.read_text('utf-8')) for p in files]
    ready=[];errors=[];seen=set()
    with concurrent.futures.ThreadPoolExecutor(max_workers=6) as ex:
        for n,(entry,error) in enumerate(ex.map(lambda e:inspect(e,a.root,a.full_hash),entries),1):
            if error:errors.append(error)
            elif entry['sha256'] in seen:errors.append({'id':entry['id'],'error':'exact_duplicate'})
            else:seen.add(entry['sha256']);ready.append(entry)
            if n%100==0:print(json.dumps({'verified':n,'ready':len(ready),'errors':len(errors)}),flush=True)
    ready.sort(key=lambda e:(e['category'],e['id']))
    eligible=[e for e in ready if CHINA.search(e['summary']) and not FOREIGN.search(e['summary'])]
    atomic(folder/'manifest.json',ready)
    atomic(a.root/'00_INDEX/china_scenic_index.json',eligible)
    atomic(folder/'verification_errors.json',errors)
    fields=['id','category','summary','orientation','width','height','duration_seconds','relative_path','source_url','creator','creator_url','license_url','sha256','geography_evidence','visual_review']
    with (folder/'video_links.csv').open('w',encoding='utf-8-sig',newline='') as f:
        writer=csv.DictWriter(f,fieldnames=fields,extrasaction='ignore');writer.writeheader();writer.writerows(eligible)
    previous={}
    if (folder/'summary.json').exists():previous=json.loads((folder/'summary.json').read_text('utf-8'))
    previous.update(phase='verified',eligible_china_clips=len(eligible),geography_review_needed=len(ready)-len(eligible),eligible_orientation=dict(collections.Counter(e['orientation'] for e in eligible)),downloaded_unique=len(ready),landscape_usable=sum(e['width']>=e['height'] and e['duration_seconds']>=6 for e in eligible),categories=dict(collections.Counter(e['category'] for e in ready)),orientation=dict(collections.Counter(e['orientation'] for e in ready)),bytes=sum(e['size_bytes'] for e in ready),verification_errors=len(errors),precise_duration_probe=True,sha256_rechecked=a.full_hash)
    atomic(folder/'summary.json',previous)
    print(json.dumps(previous,ensure_ascii=False),flush=True)
    if len(ready)<500 or previous['landscape_usable']<500:raise SystemExit('Fewer than 500 verified landscape clips; add more media')
if __name__=='__main__':main()
