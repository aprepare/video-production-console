import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import {
  fetchCatalogProviders,
  fetchCatalogSources,
  fetchCatalogStatus,
  importCatalogLocal,
  importCatalogRemote,
  MediaCatalogError,
  retryCatalogSource,
  searchCatalogAssets,
  startCatalogIndex,
} from "../api/mediaCatalog";
import type { MediaCatalogRemoteAsset, MediaCatalogSource, MediaCatalogStatus } from "../api/mediaCatalog";
import { catalogStatusRefetchInterval } from "./status-polling";
import "./media-library.css";

type API = (path: string, init?: RequestInit) => Promise<Response>;

type MediaLibraryPanelProps = {
  api: API;
  onClose: () => void;
};

const stateLabels: Record<MediaCatalogStatus["state"], string> = {
  idle: "空库",
  scanning: "正在扫描",
  extracting: "正在切镜提帧",
  analyzing: "正在分析",
  ready: "就绪",
  degraded: "部分可用",
  failed: "建库失败",
};

const kindLabels: Record<string, string> = { movie: "电影", broll: "B-roll", image: "图片" };
const sourceStatusLabels: Record<string, string> = {
  pending_probe: "待处理",
  ready: "就绪",
  failed: "失败",
};

// Actionable messages for the error codes the catalog pipeline reports.
const errorCodeMessages: Record<string, string> = {
  ffmpeg_not_configured: "请安装 FFmpeg 并在设置中填写路径",
  ffprobe_not_configured: "请安装 FFprobe 并在设置中填写路径",
  vision_not_configured: "请在设置中配置视觉分析服务地址与密钥",
  embedding_not_configured: "请在设置中配置向量模型地址与密钥",
  catalog_path_invalid: "素材库目录无效，请在设置中修正",
  catalog_not_configured: "素材库未配置，请在设置中填写素材库目录",
  provider_not_configured: "外部图库未配置，请在设置中填写 Pexels 或 Pixabay 密钥",
  catalog_search_invalid: "搜索参数无效，请填写关键词并选择图库",
  catalog_import_invalid: "导入请求无效，请检查文件或来源信息",
  rights_unknown: "部分素材缺少版权信息，导入时请补齐授权",
  index_failed: "上次建库未完成，请重新开始建库",
  analysis_all_failed: "识别全部失败。请确认视觉/向量地址带 /v1，再点开始建库重试失败镜头",
  probe_failed: "视频探测失败，请检查文件与 FFmpeg 配置",
  image_shot_failed: "图片登记失败，可尝试单项重试",
  source_failed: "素材处理失败，可尝试单项重试",
};

function errorCodeText(code: string): string {
  return errorCodeMessages[code] ?? `处理警告（${code}）`;
}

const statusQueryKey = ["media-catalog", "status"] as const;
const sourcesQueryKey = (kind: string, status: string) =>
  ["media-catalog", "sources", kind, status] as const;

function isNotConfigured(error: unknown): boolean {
  return error instanceof MediaCatalogError && error.code === "catalog_not_configured";
}

export function MediaLibraryPanel({ api, onClose }: MediaLibraryPanelProps) {
  const client = useQueryClient();
  const [kindFilter, setKindFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [message, setMessage] = useState("");
  const [searchProvider, setSearchProvider] = useState("pexels");
  const [searchQuery, setSearchQuery] = useState("");
  const [localKind, setLocalKind] = useState("image");
  const [localFile, setLocalFile] = useState<File | null>(null);

  const statusQuery = useQuery({
    queryKey: statusQueryKey,
    queryFn: () => fetchCatalogStatus(api),
    refetchInterval: (query) => catalogStatusRefetchInterval(query.state.data),
  });
  const notConfigured = isNotConfigured(statusQuery.error);
  const sourcesQuery = useQuery({
    queryKey: sourcesQueryKey(kindFilter, statusFilter),
    queryFn: () => fetchCatalogSources(api, { kind: kindFilter, status: statusFilter }),
    enabled: !notConfigured,
  });
  const providersQuery = useQuery({
    queryKey: ["media-catalog", "providers"],
    queryFn: () => fetchCatalogProviders(api),
    enabled: !notConfigured,
  });

  const refreshCatalog = () => client.invalidateQueries({ queryKey: ["media-catalog"] });
  const indexMutation = useMutation({
    mutationFn: () => startCatalogIndex(api),
    onSuccess: () => {
      setMessage("建库任务已开始。");
      void refreshCatalog();
    },
    onError: (error) => {
      if (error instanceof MediaCatalogError && error.code === "catalog_job_active") {
        setMessage("已有建库任务正在进行，请等待完成。");
      } else if (error instanceof MediaCatalogError) {
        setMessage(errorCodeText(error.code));
      } else {
        setMessage("建库启动失败，请稍后重试。");
      }
    },
  });
  const retryMutation = useMutation({
    mutationFn: (sourceID: string) => retryCatalogSource(api, sourceID),
    onSuccess: () => {
      setMessage("已重新排队处理该素材。");
      void refreshCatalog();
    },
    onError: () => setMessage("重试失败，请稍后再试。"),
  });
  const searchMutation = useMutation({
    mutationFn: () => searchCatalogAssets(api, { provider: searchProvider, q: searchQuery }),
    onError: (error) => {
      setMessage(error instanceof MediaCatalogError ? errorCodeText(error.code) : "搜索失败，请稍后重试。");
    },
  });
  const importRemoteMutation = useMutation({
    mutationFn: (asset: MediaCatalogRemoteAsset) => importCatalogRemote(api, asset),
    onSuccess: (result) => {
      setMessage(result.created ? "已导入外部素材。" : "该素材已在库中，未重复写入。");
      void refreshCatalog();
    },
    onError: (error) => {
      setMessage(error instanceof MediaCatalogError ? errorCodeText(error.code) : "导入失败，请稍后重试。");
    },
  });
  const importLocalMutation = useMutation({
    mutationFn: () => {
      if (!localFile) return Promise.reject(new Error("missing file"));
      return importCatalogLocal(api, localFile, localKind);
    },
    onSuccess: (result) => {
      setMessage(result.created ? "已导入本地文件。" : "该文件已在库中，未重复写入。");
      setLocalFile(null);
      void refreshCatalog();
    },
    onError: (error) => {
      setMessage(error instanceof MediaCatalogError ? errorCodeText(error.code) : "本地导入失败，请稍后重试。");
    },
  });

  const status = statusQuery.data;
  const activeJob = status?.active_job ?? null;

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <section
        className="settings-modal media-library-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="media-library-title"
        tabIndex={-1}
        onClick={(event) => event.stopPropagation()}
      >
        <div className="modal-head">
          <div>
            <span className="muted">混剪素材智能库</span>
            <h2 id="media-library-title">素材库</h2>
          </div>
          <button type="button" className="close" aria-label="关闭素材库" onClick={onClose}>
            <X size={20} aria-hidden="true" />
          </button>
        </div>
        {message ? (
          <div className="notice" role="status">
            {message}
            <button type="button" onClick={() => setMessage("")}>关闭</button>
          </div>
        ) : null}
        {notConfigured ? (
          <div className="media-library-empty" role="status">
            <strong>素材库尚未配置</strong>
            <p>请在设置中填写「素材库目录」和「媒体素材目录」，保存并重启控制台后即可建库。</p>
          </div>
        ) : statusQuery.isPending ? (
          <p className="muted">正在读取素材库状态…</p>
        ) : statusQuery.isError ? (
          <p className="media-library-error" role="alert">素材库状态读取失败，请稍后重试。</p>
        ) : status ? (
          <>
            <div className="media-library-summary">
              <span className={`media-state media-state--${status.state}`}>
                {stateLabels[status.state] ?? status.state}
              </span>
              <button
                type="button"
                className="primary"
                disabled={indexMutation.isPending || Boolean(activeJob)}
                onClick={() => indexMutation.mutate()}
              >
                开始建库
              </button>
              <p className="muted">
                本机建库会扫描 originals、切镜抽帧、视觉打标和向量。建好后混剪会自动从库里检索镜头，不必再手工选片。
              </p>
            </div>
            <div className="media-stats">
              <div className="media-stat"><strong>{status.counts.sources}</strong><span>素材源</span></div>
              <div className="media-stat"><strong>{status.counts.shots}</strong><span>镜头</span></div>
              <div className="media-stat"><strong>{status.counts.ready_shots}</strong><span>就绪镜头</span></div>
              <div className="media-stat"><strong>{status.counts.failed_shots}</strong><span>失败镜头</span></div>
            </div>
            {activeJob ? (
              <div className="media-progress">
                <span>建库进度</span>
                {activeJob.total_units > 0 ? (
                  <>
                    <progress
                      aria-label="建库进度"
                      value={activeJob.completed_units}
                      max={activeJob.total_units}
                    />
                    <span>{activeJob.completed_units}/{activeJob.total_units}</span>
                  </>
                ) : (
                  <progress aria-label="建库进度" />
                )}
              </div>
            ) : null}
            <div className="media-import">
              <h3>外部图库</h3>
              {providersQuery.data?.providers.every((provider) => !provider.configured) ? (
                <p className="muted">未配置 Pexels / Pixabay 密钥时仍可本地建库；外部搜索不可用。</p>
              ) : null}
              <form
                className="media-import-row"
                onSubmit={(event) => {
                  event.preventDefault();
                  if (searchQuery.trim()) searchMutation.mutate();
                }}
              >
                <label>
                  图库
                  <select value={searchProvider} onChange={(event) => setSearchProvider(event.target.value)}>
                    {(providersQuery.data?.providers ?? [
                      { name: "pexels", configured: false },
                      { name: "pixabay", configured: false },
                    ]).map((provider) => (
                      <option key={provider.name} value={provider.name} disabled={!provider.configured}>
                        {provider.name}
                        {provider.configured ? "" : "（未配置）"}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  关键词
                  <input
                    value={searchQuery}
                    onChange={(event) => setSearchQuery(event.target.value)}
                    placeholder="例如：湖面、城市夜景"
                  />
                </label>
                <button type="submit" disabled={searchMutation.isPending || !searchQuery.trim()}>
                  搜索图库
                </button>
              </form>
              <ul className="media-search-list">
                {(searchMutation.data?.assets ?? []).map((asset) => (
                  <li key={`${asset.provider}-${asset.id}`} className="media-source">
                    <div className="media-source-main">
                      <strong>{asset.creator || asset.id}</strong>
                      <small>
                        {asset.license_code || "许可未知"}
                        {asset.width && asset.height ? ` · ${asset.width}×${asset.height}` : ""}
                      </small>
                    </div>
                    <button
                      type="button"
                      disabled={importRemoteMutation.isPending}
                      onClick={() => importRemoteMutation.mutate(asset)}
                    >
                      导入
                    </button>
                  </li>
                ))}
              </ul>
              <h3>本地导入</h3>
              <form
                className="media-import-row"
                onSubmit={(event) => {
                  event.preventDefault();
                  if (localFile) importLocalMutation.mutate();
                }}
              >
                <label>
                  类型
                  <select value={localKind} onChange={(event) => setLocalKind(event.target.value)}>
                    <option value="image">图片</option>
                    <option value="broll">B-roll</option>
                    <option value="movie">电影</option>
                  </select>
                </label>
                <label>
                  文件
                  <input
                    type="file"
                    onChange={(event) => setLocalFile(event.target.files?.[0] ?? null)}
                  />
                </label>
                <button type="submit" disabled={importLocalMutation.isPending || !localFile}>
                  导入文件
                </button>
              </form>
            </div>
            {status.warnings.length ? (
              <ul className="media-warnings">
                {status.warnings.map((warning) => (
                  <li key={warning.code}>
                    {errorCodeText(warning.code)}（{warning.count} 项）
                  </li>
                ))}
              </ul>
            ) : null}
            <div className="media-filters">
              <label>
                按类型筛选
                <select value={kindFilter} onChange={(event) => setKindFilter(event.target.value)}>
                  <option value="">全部类型</option>
                  <option value="movie">电影</option>
                  <option value="broll">B-roll</option>
                  <option value="image">图片</option>
                </select>
              </label>
              <label>
                按状态筛选
                <select value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}>
                  <option value="">全部状态</option>
                  <option value="pending_probe">待处理</option>
                  <option value="ready">就绪</option>
                  <option value="failed">失败</option>
                </select>
              </label>
            </div>
            {sourcesQuery.isError ? (
              <p className="media-library-error" role="alert">素材列表读取失败，请稍后重试。</p>
            ) : (
              <ul className="media-source-list">
                {(sourcesQuery.data?.sources ?? []).map((source: MediaCatalogSource) => (
                  <li key={source.id} className="media-source">
                    <div className="media-source-main">
                      <strong>{source.relative_path}</strong>
                      <small>
                        {kindLabels[source.kind] ?? source.kind} · {sourceStatusLabels[source.status] ?? source.status}
                      </small>
                      {source.status === "failed" && source.error_code ? (
                        <small className="media-source-error">{errorCodeText(source.error_code)}</small>
                      ) : null}
                    </div>
                    {source.status === "failed" ? (
                      <button
                        type="button"
                        disabled={retryMutation.isPending}
                        onClick={() => retryMutation.mutate(source.id)}
                      >
                        重试
                      </button>
                    ) : null}
                  </li>
                ))}
                {sourcesQuery.data && sourcesQuery.data.sources.length === 0 ? (
                  <li className="media-source media-source--empty">没有匹配当前筛选的素材。</li>
                ) : null}
              </ul>
            )}
          </>
        ) : null}
      </section>
    </div>
  );
}
