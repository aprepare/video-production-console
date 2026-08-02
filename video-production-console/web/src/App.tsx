import { useEffect, useMemo, useState } from 'react'
import './App.css'

type Account = { id: string; name: string; background_path?: string }
type Project = { id: string; account_id: string; title: string; stage: string; updated_at?: string; missing_assets?: string[] }

const stages = ['topic', 'script', 'assets', 'mixing', 'review', 'ready', 'published']

function App() {
  const [accounts, setAccounts] = useState<Account[]>([])
  const [projects, setProjects] = useState<Project[]>([])
  const [account, setAccount] = useState('')
  const [loading, setLoading] = useState(true)
  const [newAccount, setNewAccount] = useState('')
  const [newProject, setNewProject] = useState('')
  const [message, setMessage] = useState('')
  const [limit, setLimit] = useState(2)

  const load = async () => {
    setLoading(true)
    try {
      const [a, p, s] = await Promise.all([fetch('/api/accounts'), fetch('/api/projects'), fetch('/api/settings')])
      setAccounts(a.ok ? await a.json() : [])
      setProjects(p.ok ? await p.json() : [])
      if (s.ok) { const v = await s.json(); setLimit(Number(v.max_codex_concurrency) || 2) }
    } catch { setMessage('控制台服务尚未连接') }
    finally { setLoading(false) }
  }
  const updateLimit = async (v:number) => { setLimit(v); await fetch('/api/settings', {method:'PUT', headers:{'Content-Type':'application/json'}, body:JSON.stringify({max_codex_concurrency:v})}) }
  useEffect(() => { void load() }, [])
  const visible = useMemo(() => account ? projects.filter(p => p.account_id === account) : projects, [projects, account])

  const createAccount = async (e: React.FormEvent) => {
    e.preventDefault(); setMessage('账号需要通过背景图表单创建')
  }
  const createProject = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!newProject.trim() || !account) return
    const res = await fetch('/api/projects', { method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({account_id: account, title: newProject.trim()}) })
    if (res.ok) { setNewProject(''); await load() } else setMessage('项目创建失败')
  }
  return <div className="shell">
    <header><div><span className="eyebrow">LOCAL VIDEO OPERATIONS</span><h1>视频生产控制台</h1></div><div className="status"><span className="dot"/>本机服务 · 爆款库共享 <label className="limit">并发 <select value={limit} onChange={e=>void updateLimit(Number(e.target.value))}>{[1,2,3,4].map(n=><option key={n} value={n}>{n}</option>)}</select></label></div></header>
    <div className="layout">
      <aside><div className="aside-title">账号 <span>{accounts.length}</span></div><button className={!account?'selected':''} onClick={()=>setAccount('')}>全部账号</button>{accounts.map(a=><button key={a.id} className={account===a.id?'selected':''} onClick={()=>setAccount(a.id)}>{a.name}</button>)}<form onSubmit={createAccount} className="add-account"><input value={newAccount} onChange={e=>setNewAccount(e.target.value)} placeholder="添加账号名称"/><button type="submit">+</button></form><div className="aside-foot">固定背景图在账号设置中上传<br/>每个项目单独管理文案、配音、SRT 和成片</div></aside>
      <main><div className="toolbar"><div><div className="muted">{account ? accounts.find(a=>a.id===account)?.name : '全部账号'}</div><h2>视频项目</h2></div><form onSubmit={createProject} className="new-project"><input value={newProject} onChange={e=>setNewProject(e.target.value)} placeholder={account?'新建项目标题':'先选择账号'}/><button disabled={!account}>新建项目</button></form></div>{message&&<div className="notice">{message}</div>}{loading?<div className="empty">正在读取项目...</div>:<div className="board">{stages.map(stage=><section className="column" key={stage}><div className="column-head"><span>{stageLabel(stage)}</span><b>{visible.filter(p=>p.stage===stage).length}</b></div>{visible.filter(p=>p.stage===stage).map(p=><article className="project" key={p.id}><strong>{p.title}</strong><small>{p.id.slice(0,8)} · {p.missing_assets?.length?`缺 ${p.missing_assets.length} 项`:'素材正常'}</small><div className="project-foot"><span>{accountName(p.account_id, accounts)}</span><span className="pulse">●</span></div></article>)}</section>)}</div>}</main>
    </div>
  </div>
}
function stageLabel(s:string){return ({topic:'选题',script:'文案',assets:'素材',mixing:'混剪',review:'审核',ready:'待发布',published:'已发布'} as Record<string,string>)[s]||s}
function accountName(id:string, as:Account[]){return as.find(a=>a.id===id)?.name||'未分配'}
export default App
