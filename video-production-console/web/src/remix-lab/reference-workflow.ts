import type { RemixLabRerunModel, RemixLabWorkflow, RemixLabWorkflowNode } from "./api";

export function isReferenceNode(node:RemixLabWorkflowNode) { return node.type==="agent" && node.config.role==="reference"; }

export function replaceReferenceModels(workflow:RemixLabWorkflow, choices:RemixLabRerunModel[]):RemixLabWorkflow {
  const old=workflow.nodes.filter(isReferenceNode);
  if(!old.length&&!choices.length) return workflow;
  const removed=new Set(old.map(node=>node.id));
  const nodes=workflow.nodes.filter(node=>!removed.has(node.id));
  const source=nodes.find(node=>node.type==="input"),writer=nodes.find(node=>node.type==="writer");
  if(!source||!writer) throw new Error("工作流缺少原文或写手节点。");
  for(const [from,to] of workflow.edges) {
    if(removed.has(from)&&!removed.has(to)&&to!==writer.id) throw new Error("参考节点仍被其他节点依赖，请先调整连线。");
  }
  const edges=workflow.edges.filter(([from,to])=>!removed.has(from)&&!removed.has(to));
  const used=new Set(nodes.map(node=>node.id));
  choices.forEach((choice,index)=>{
    let id=old[index]?.id??`reference_${index+1}`;
    while(used.has(id)) id+="_r";
    used.add(id);
    nodes.push({id,type:"agent",title:`参考稿${index+1}`,x:old[index]?.x??(source.x+writer.x)/2,y:old[index]?.y??source.y+index*150,
      config:{...old[index]?.config,role:"reference",...choice,model:choice.model.trim()}});
    edges.push([source.id,id],[id,writer.id]);
  });
  if(!edges.some(([from,to])=>from===source.id&&to===writer.id)) edges.push([source.id,writer.id]);
  return {...workflow,nodes,edges};
}
