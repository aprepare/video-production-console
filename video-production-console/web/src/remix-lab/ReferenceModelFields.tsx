import { RoleModelFields } from "./RoleModelFields";
import type { RemixLabRerunModel } from "./api";

export function ReferenceModelFields({ values, onChange, fallback, disabled=false, max=6 }: {
  values: RemixLabRerunModel[]; onChange: (values:RemixLabRerunModel[])=>void;
  fallback: RemixLabRerunModel; disabled?: boolean; max?: number;
}) {
  return <section className="remix-reference-models" aria-label="参考模型配置">
    <div className="remix-reference-models__head"><h3>参考模型 · {values.length} 个</h3>
      <button type="button" className="header-button" disabled={disabled||values.length>=max}
        onClick={()=>onChange([...values,{...fallback,service_tier:"default"}])}>添加参考模型</button></div>
    <p className="remix-lab-muted">每个模型独立生成完整文案、标题和视频描述，保存后交给写手借鉴。数量由你选择；全部移除时由写手直接二创。沿用当前二创 API 连接。</p>
    <div className="remix-lab-slots">
      {values.map((value,index)=><fieldset className="remix-lab-slot" disabled={disabled} key={index}>
        <legend>参考模型{index+1}</legend>
        <div className="remix-lab-slot__fields"><RoleModelFields id={`reference-model-${index}`} label={`参考模型${index+1}`} value={value}
          onChange={next=>onChange(values.map((item,i)=>i===index?next:item))} />
          <button type="button" className="header-button" aria-label={`移除参考模型${index+1}`}
            onClick={()=>onChange(values.filter((_,i)=>i!==index))}>移除此模型</button>
        </div>
      </fieldset>)}
    </div>
  </section>;
}
