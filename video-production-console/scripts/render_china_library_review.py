"""Build a local attributed review page and decoded-frame contact sheets."""
from __future__ import annotations
import argparse, collections, html, io, json, subprocess
from pathlib import Path
from PIL import Image, ImageDraw, ImageFont
import imageio_ffmpeg


def main():
    ap=argparse.ArgumentParser();ap.add_argument('--root',type=Path,required=True);ap.add_argument('--audit-output',type=Path,required=True);a=ap.parse_args()
    folder=a.root/'00_INDEX/pexels_china_20260909';rows=json.loads((a.root/'00_INDEX/china_scenic_index.json').read_text('utf-8'))
    groups=sorted({r['category'] for r in rows});cards=[]
    for r in rows:
        search=' '.join([r['summary'],r['category'],' '.join(r.get('tags',[])),str(r['pexels_id'])])
        cards.append('<article data-search="'+html.escape(search,quote=True)+'" data-orientation="'+r['orientation']+'"><video controls preload="none" poster="'+html.escape(r['poster_url'],quote=True)+'" src="'+html.escape((a.root/r['relative_path']).as_uri(),quote=True)+'"></video><h3>'+html.escape(r['summary'])+'</h3><p>'+html.escape(r['category'])+' · '+str(r['width'])+'×'+str(r['height'])+' · '+str(round(r['duration_seconds'],2))+'s</p><p>'+html.escape(' / '.join(r.get('tags',[])))+'</p><a href="'+html.escape(r['source_url'],quote=True)+'" target="_blank" rel="noopener">Video by '+html.escape(r['creator'])+' on Pexels</a></article>')
    page='''<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>中国意象素材库</title><style>body{font-family:system-ui;background:#f5f6f8;color:#18202b;margin:24px}header{position:sticky;top:0;padding:14px;background:#fff;z-index:2;box-shadow:0 2px 12px #0002}input,select{font:inherit;padding:9px;margin-right:10px}main{display:grid;grid-template-columns:repeat(auto-fill,minmax(300px,1fr));gap:16px;margin-top:20px}article{background:#fff;padding:12px;border-radius:10px;content-visibility:auto;contain-intrinsic-size:auto 360px}video{width:100%;height:180px;background:#151515}h3{font-size:15px}p{font-size:14px;line-height:1.5}[hidden]{display:none}</style><header><h1>中国意象素材库 · COUNT 条</h1><p><a href="https://www.pexels.com/">Videos provided by Pexels</a> · <a href="video_links.csv">来源链接表</a> · <a href="summary.json">核验统计</a></p><input id="query" placeholder="搜索：农村、房子、城市、ID…"><select id="orientation"><option value="">全部方向</option><option value="landscape">横屏</option><option value="portrait">竖屏</option></select><span id="count"></span><p>按来源标题中的中国地名等元数据筛选；非逐条人工地点认证。人物与场景仅作意象，不代表新闻当事人或事件现场。</p></header><main>CARDS</main><script>const q=document.querySelector('#query'),o=document.querySelector('#orientation'),cards=[...document.querySelectorAll('article')];function filter(){let n=0;const text=q.value.trim().toLowerCase();for(const c of cards){c.hidden=!(c.dataset.search.toLowerCase().includes(text)&&(!o.value||c.dataset.orientation===o.value));if(!c.hidden)n++}document.querySelector('#count').textContent='显示 '+n+' 条'}q.oninput=filter;o.onchange=filter;filter();document.addEventListener('play',e=>{for(const v of document.querySelectorAll('video'))if(v!==e.target)v.pause()},true);</script></html>'''.replace('COUNT',str(len(rows))).replace('CARDS',''.join(cards))
    (folder/'review.html').write_text(page,encoding='utf-8')
    policy={'version':1,'china_only':True,'orientation':'landscape','playback_speed':1,'opening_min_s':4,'opening_max_s':6,'body_min_s':5,'body_max_s':8,'dissolve_ratio':.25,'max_source_uses':1,'reuse_gap':5,'history_window':12}
    policy_path=a.root/'00_INDEX/scenic_policy.json'
    if not policy_path.exists():policy_path.write_text(json.dumps(policy,ensure_ascii=False,indent=2),encoding='utf-8')
    selected=[]
    for category in ['Housing_Residential','Rural_Life','City_Business','Traditional_Architecture','Transport_Infrastructure','Nature_Landscape']:
        candidates=[r for r in rows if r['category']==category and r['orientation']=='landscape']
        selected.extend(candidates[:4])
    portraits=[r for r in rows if r['orientation']=='portrait'][:8]
    a.audit_output.mkdir(parents=True,exist_ok=True)
    font=ImageFont.truetype('C:/Windows/Fonts/arial.ttf',14)
    ffmpeg=imageio_ffmpeg.get_ffmpeg_exe()
    for name,items in [('landscape-sample',selected),('portrait-sample',portraits)]:
        sheet=Image.new('RGB',(1280,230*((len(items)+3)//4)),(238,240,243));draw=ImageDraw.Draw(sheet)
        for i,r in enumerate(items):
            cmd=[ffmpeg,'-v','error','-ss',str(r['duration_seconds']*.45),'-i',str(a.root/r['relative_path']),'-frames:v','1','-vf','scale=320:180:force_original_aspect_ratio=decrease,pad=320:180:(ow-iw)/2:(oh-ih)/2','-f','image2pipe','-vcodec','mjpeg','pipe:1']
            result=subprocess.run(cmd,capture_output=True,timeout=25,check=True)
            image=Image.open(io.BytesIO(result.stdout));x=(i%4)*320;y=(i//4)*230;sheet.paste(image,(x,y));draw.text((x+5,y+183),str(r['pexels_id'])+' '+r['category'][:25],font=font,fill=(0,0,0));draw.text((x+5,y+204),r['summary'][:40],font=font,fill=(0,0,0))
        sheet.save(a.audit_output/(name+'.jpg'),quality=90)
    print(json.dumps({'videos':len(rows),'review':str(folder/'review.html'),'policy':str(policy_path),'sample_sheets':str(a.audit_output)},ensure_ascii=False),flush=True)
if __name__=='__main__':main()
