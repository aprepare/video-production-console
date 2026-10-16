import { useEffect, useState } from "react";
import type { ImageModelOption, ImageModelsResponse } from "./imageStudioTypes";

type API = (path: string, init?: RequestInit) => Promise<Response>;

let cached: Promise<ImageModelsResponse | null> | null = null;

function validModels(body: unknown): body is ImageModelsResponse {
  return Boolean(body) && Array.isArray((body as ImageModelsResponse).models);
}

/** 图片模型列表（网关实际可用的），成功后整个页面会话只请求一次。 */
export function loadImageModels(api: API, refresh = false): Promise<ImageModelsResponse | null> {
  if (!cached || refresh) {
    const request = Promise.resolve()
      .then(() => api("/api/image-studio/models"))
      .then(async (response) => {
        if (!response?.ok) return null;
        const body: unknown = await response.json();
        return validModels(body) ? body : null;
      })
      .catch(() => null);
    cached = request;
    // A failed load must not stick: the next mount asks again.
    void request.then((body) => { if (!body && cached === request) cached = null; });
  }
  return cached;
}

export function resetImageModelsCache() {
  cached = null;
}

/** 图文项目里的图片模型下拉：列网关上实际能用的模型，也能手输。 */
export function ImageModelField({ api, value, onChange }: { api: API; value: string; onChange: (value: string) => void }) {
  const [models, setModels] = useState<ImageModelOption[]>([]);
  const [custom, setCustom] = useState(false);
  useEffect(() => {
    let alive = true;
    void loadImageModels(api).then((body) => {
      if (!alive || !body) return;
      setModels(body.models);
      if (value && !body.models.some((model) => model.id === value)) setCustom(true);
    });
    return () => { alive = false; };
  }, [api, value]);
  const current = models.find((model) => model.id === value);
  if (custom || !models.length) {
    return <label>
      图片模型
      <input aria-label="图片模型" value={value} maxLength={128} placeholder="grok-imagine-image-2.0" onChange={(event) => onChange(event.target.value)} />
      {models.length ? <button type="button" className="studio-link image-model-field__switch" onClick={() => { setCustom(false); onChange(models.find((model) => model.available)?.id || value); }}>从列表选择</button> : null}
    </label>;
  }
  return <label>
    图片模型
    <select aria-label="图片模型" value={value} onChange={(event) => { if (event.target.value === "__custom") setCustom(true); else onChange(event.target.value); }}>
      {models.map((model) => <option key={model.id} value={model.id} disabled={!model.available}>{model.label || model.id}{model.available ? "" : " · 不可用"}</option>)}
      <option value="__custom">自定义模型名…</option>
    </select>
    {current?.note ? <small>{current.note}</small> : null}
  </label>;
}
