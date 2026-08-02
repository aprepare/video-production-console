import { useCallback, useEffect, useMemo, useState } from 'react'
import './App.css'

type Account = { id: string; name: string; background_path?: string }
type Project = { id: string; account_id: string; title: string; stage: string; updated_at?: string; missing_assets?: string[] }
type Asset = { id: string; type: string; filename: string; mime_type: string; size: number; version: number; created_at: string }
type TaskMessage = { id: string; role: string; content: string; created_at: string }
type Task = { id: string; project_id?: string; type: string; skill_name: string; status: string; result_summary?: string; error_message?: string; created_at: string; messages?: TaskMessage[] }
type ProjectDetail = { project: Project; assets: Record<string, Asset>; asset_history?: Record<string, Asset[]>; missing_assets?: string[] }

const stages = ['topic', 'script', 'assets', 'mixing', 'review', 'ready', 'published']
const assetLabels: Record<string, string> = { continuous_script: '连续版文案', spoken_script: '口播稿', audio: '配音', subtitle: 'SRT 字幕', mix_draft: '混剪草稿', final_video: '成片' }
const statusLabels: Record<string, string> = { queued: '排队中', running: '运行中', waiting_input: '等待回答', completed: '已完成', failed: '失败', cancelled: '已取消' }

function App() {
  const [accounts, setAccounts] = useState<Account[]>([])
  const [projects, setProjects] = useState<Project[]>([])
  const [account, setAccount] = useState('')
  const [loading, setLoading] = useState(true)
  const [newAccount, setNewAccount] = useState('')
  const [accountBackground, setAccountBackground] = useState<File | null>(null)
  const [newProject, setNewProject] = useState('')
  const [message, setMessage] = useState('')
  const [limit, setLimit] = useState(2)
  const [selected, setSelected] = useState<Project | null>(null)
  const [detail, setDetail] = useState<ProjectDetail | null>(null)
  const [tasks, setTasks] = useState<Task[]>([])
  const [detailLoading, setDetailLoading] = useState(false)
  const [assetType, setAssetType] = useState('continuous_script')
  const [assetFile, setAssetFile] = useState<File | null>(null)
  const activeTasks = useMemo(() => tasks.filter(t => ['queued', 'running', 'waiting_input'].includes(t.status)), [tasks])

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [a, p, s] = await Promise.all([fetch('/api/accounts'), fetch('/api/projects'), fetch('/api/settings')])
      setAccounts(a.ok ? await a.json() : [])
      setProjects(p.ok ? await p.json() : [])
      if (s.ok) { const v = await s.json(); setLimit(Number(v.max_codex_concurrency) || 2) }
    } catch { setMessage('控制台服务尚未连接') } finally { setLoading(false) }
  }, [])

  const loadDetail = useCallback(async (project: Project) => {
    setDetailLoading(true)
    try {
      const [p, t] = await Promise.all([fetch(`/api/projects/${project.id}`), fetch(`/api/tasks?project_id=${project.id}`)])
      if (!p.ok || !t.ok) throw new Error('读取项目详情失败')
      const nextDetail = await p.json() as ProjectDetail
      const listed = await t.json() as Task[]
      const nextTasks = await Promise.all(listed.map(async task => {
        if (task.status !== 'waiting_input') return task
        const response = await fetch(`/api/tasks/${task.id}`)
        return response.ok ? await response.json() as Task : task
      }))
      setDetail(nextDetail); setTasks(nextTasks)
    } catch (error) { setMessage(error instanceof Error ? error.message : '读取项目详情失败') }
    finally { setDetailLoading(false) }
  }, [])

  useEffect(() => { void load() }, [load])
  useEffect(() => {
    if (!selected) return
    const timer = window.setInterval(() => void loadDetail(selected), 5000)
    return () => window.clearInterval(timer)
  }, [selected, loadDetail])
  useEffect(() => {
    if (!selected) return
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const sockets = activeTasks.map(task => {
      const socket = new WebSocket(`${protocol}//${location.host}/api/tasks/${task.id}/events?after=0`)
      socket.onmessage = () => void loadDetail(selected)
      return socket
    })
    return () => sockets.forEach(socket => socket.close())
  }, [selected, activeTasks, loadDetail])
  const visible = useMemo(() => account ? projects.filter(p => p.account_id === account) : projects, [projects, account])
  const updateLimit = async (v: number) => { setLimit(v); await fetch('/api/settings', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ max_codex_concurrency: v }) }) }
  const openProject = (project: Project) => { setSelected(project); setDetail(null); setTasks([]); void loadDetail(project) }
  const closeProject = () => { setSelected(null); setDetail(null); setTasks([]) }

  const createAccount = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!newAccount.trim() || !accountBackground) { setMessage('请输入账号名称并选择固定背景图'); return }
    const body = new FormData(); body.set('name', newAccount.trim()); body.set('background', accountBackground)
    const res = await fetch('/api/accounts', { method: 'POST', body })
    if (!res.ok) { setMessage('账号创建失败，请检查图片格式和名称'); return }
    setNewAccount(''); setAccountBackground(null); await load()
  }
  const createProject = async (e: React.FormEvent) => {
    e.preventDefault(); if (!newProject.trim() || !account) return
    const res = await fetch('/api/projects', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ account_id: account, title: newProject.trim() }) })
    if (res.ok) { setNewProject(''); await load() } else setMessage('项目创建失败')
  }
  const answerTask = async (task: Task) => {
    const answer = window.prompt('请输入给 Codex 的回答')
    if (!answer?.trim()) return
    const res = await fetch(`/api/tasks/${task.id}/answer`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ answer: answer.trim() }) })
    if (!res.ok) setMessage('任务回答失败'); else if (selected) await loadDetail(selected)
  }
  const cancelTask = async (task: Task) => {
    if (!window.confirm('确定取消这个 Codex 任务吗？')) return
    const res = await fetch(`/api/tasks/${task.id}/cancel`, { method: 'POST' })
    if (!res.ok) setMessage('任务取消失败'); else if (selected) await loadDetail(selected)
  }
  const startTask = async (type: string, prompt: string) => {
    if (!selected) return
    const res = await fetch(`/api/projects/${selected.id}/tasks`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ account_id: selected.account_id, type, prompt }) })
    if (!res.ok) setMessage('Codex 任务创建失败'); else await loadDetail(selected)
  }
  const uploadAsset = async (e: React.FormEvent) => {
    e.preventDefault(); if (!selected || !assetFile) return
    const body = new FormData(); body.set('file', assetFile)
    const res = await fetch(`/api/projects/${selected.id}/assets/${assetType}`, { method: 'POST', body })
    if (!res.ok) setMessage('素材上传失败，请检查文件格式'); else { setAssetFile(null); await loadDetail(selected) }
  }

  return <div className="shell">
    <header><div><span className="eyebrow">LOCAL VIDEO OPERATIONS</span><h1>视频生产控制台</h1></div><div className="status"><span className="dot" />本机服务 · 爆款库共享 <label className="limit">并发 <select value={limit} onChange={e => void updateLimit(Number(e.target.value))}>{[1, 2, 3, 4].map(n => <option key={n} value={n}>{n}</option>)}</select></label></div></header>
    <div className="layout">
      <aside><div className="aside-title">账号 <span>{accounts.length}</span></div><button className={!account ? 'selected' : ''} onClick={() => setAccount('')}>全部账号</button>{accounts.map(a => <button key={a.id} className={account === a.id ? 'selected' : ''} onClick={() => setAccount(a.id)}>{a.name}</button>)}<form onSubmit={createAccount} className="add-account"><input value={newAccount} onChange={e => setNewAccount(e.target.value)} placeholder="添加账号名称" /><label className="background-pick">{accountBackground ? '已选图片' : '选择背景图'}<input type="file" accept="image/png,image/jpeg,image/webp" onChange={e => setAccountBackground(e.target.files?.[0] || null)} /></label><button type="submit">添加账号</button></form><div className="aside-foot">每个账号使用一张固定背景图；每个项目独立管理文案、配音、SRT 和成片。</div></aside>
      <main><div className="toolbar"><div><div className="muted">{account ? accounts.find(a => a.id === account)?.name : '全部账号'}</div><h2>视频项目</h2></div><form onSubmit={createProject} className="new-project"><input value={newProject} onChange={e => setNewProject(e.target.value)} placeholder={account ? '新建项目标题' : '先选择账号'} /><button disabled={!account}>新建项目</button></form></div>{message && <div className="notice">{message}<button onClick={() => setMessage('')}>关闭</button></div>}{loading ? <div className="empty">正在读取项目...</div> : <div className="board">{stages.map(stage => <section className="column" key={stage}><div className="column-head"><span>{stageLabel(stage)}</span><b>{visible.filter(p => p.stage === stage).length}</b></div>{visible.filter(p => p.stage === stage).map(p => <button className="project" key={p.id} onClick={() => openProject(p)}><strong>{p.title}</strong><small>{p.id.slice(0, 8)} · {p.missing_assets?.length ? `缺少 ${p.missing_assets.length} 项` : '素材正常'}</small><div className="project-foot"><span>{accountName(p.account_id, accounts)}</span><span className="pulse">●</span></div></button>)}</section>)}</div>}</main>
    </div>
    {selected && <div className="drawer-backdrop" onClick={closeProject}><aside className="drawer" onClick={e => e.stopPropagation()}><div className="drawer-head"><div><span className="muted">{accountName(selected.account_id, accounts)}</span><h2>{selected.title}</h2></div><button className="close" onClick={closeProject} aria-label="关闭">×</button></div>{detailLoading && !detail ? <div className="empty">正在读取详情...</div> : detail && <><section className="drawer-section"><h3>项目状态</h3><div className="detail-meta"><span className="stage-badge">{stageLabel(detail.project.stage)}</span><span>更新于 {formatDate(detail.project.updated_at)}</span></div>{detail.missing_assets?.length ? <p className="warning">待补充：{detail.missing_assets.map(a => assetLabels[a] || a).join('、')}</p> : <p className="ok">当前阶段所需素材齐全</p>}</section><section className="drawer-section"><h3>启动工作流</h3><div className="workflow-actions"><button onClick={() => void startTask('topic_select', '给我选题，并在 Obsidian 创建候选选题卡。')}>给我选题</button><button onClick={() => void startTask('topic_deepen', '深化当前选题卡，从爆款库补充依据和可借鉴片段。')}>深化一下</button><button onClick={() => void startTask('remix', '根据当前项目素材完成财经爆款二创。')}>二创文案</button><button onClick={() => void startTask('spoken_format', '把连续版文案转换为口播稿，不删词不漏段。')}>口播稿</button><button onClick={() => void startTask('montage', '使用当前文案、配音、SRT 和固定背景图生成混剪草稿。')}>生成混剪</button></div></section><section className="drawer-section"><h3>素材</h3><form className="asset-upload" onSubmit={uploadAsset}><select value={assetType} onChange={e => setAssetType(e.target.value)}>{Object.entries(assetLabels).map(([value,label]) => <option key={value} value={value}>{label}</option>)}</select><input type="file" onChange={e => setAssetFile(e.target.files?.[0] || null)} /><button disabled={!assetFile}>上传</button></form><div className="asset-list">{Object.entries(detail.assets || {}).map(([type, asset]) => <a className="asset-row" key={type} href={`/api/assets/${asset.id}/content`} target="_blank" rel="noreferrer"><span>{assetLabels[type] || type}</span><div><strong>{asset.filename}</strong><small>{formatSize(asset.size)} · v{asset.version}</small></div></a>)}{!Object.keys(detail.assets || {}).length && <p className="muted">暂无项目素材</p>}</div></section><section className="drawer-section"><h3>Codex 任务 <span className="count">{tasks.length}</span></h3><div className="task-list">{tasks.map(task => <div className="task-row" key={task.id}><div className="task-top"><strong>{task.skill_name || task.type}</strong><span className={`task-status status-${task.status}`}>{statusLabels[task.status] || task.status}</span></div>{task.result_summary && <p>{task.result_summary}</p>}{task.error_message && <p className="warning">{task.error_message}</p>}{task.status === 'waiting_input' && <><p className="question">{latestQuestion(task)}</p><div className="task-actions"><button onClick={() => void answerTask(task)}>回答</button><button className="secondary" onClick={() => void cancelTask(task)}>取消任务</button></div></>}</div>)}{!tasks.length && <p className="muted">暂无 Codex 任务</p>}</div></section></>}</aside></div>}
  </div>
}
function stageLabel(s: string) { return ({ topic: '选题', script: '文案', assets: '素材', mixing: '混剪', review: '审核', ready: '待发布', published: '已发布' } as Record<string, string>)[s] || s }
function accountName(id: string, as: Account[]) { return as.find(a => a.id === id)?.name || '未分配' }
function formatSize(size: number) { if (size < 1024) return `${size} B`; if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`; return `${(size / (1024 * 1024)).toFixed(1)} MB` }
function formatDate(value?: string) { return value ? new Date(value).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : '暂无' }
function latestQuestion(task: Task) { return task.messages?.filter(m => m.role === 'assistant' || m.role === 'system').slice(-1)[0]?.content || task.result_summary || 'Codex 正在等待你的输入。' }
export default App
