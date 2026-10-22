export type EventRecord={sequence:number;project_id:string;kind:string;payload:unknown;created_at:string};
declare global {interface Window {workbench:{invoke:(operation:string,payload?:unknown)=>Promise<{ok:boolean;value?:unknown;error?:string}>;onEvent:(listener:(value:EventRecord)=>void)=>()=>void}}}
export async function api<T=any>(operation:string,payload:unknown={}):Promise<T>{if(!window.workbench)throw new Error('请从 Electron 桌面应用打开此界面');const result=await window.workbench.invoke(operation,payload);if(!result.ok)throw new Error(result.error||'操作失败');return result.value as T;}
export const idempotency=()=>crypto.randomUUID();
export type Project={id:string;name:string;source:string;snapshot:string;manifest:{path:string;kind:string;hash?:string;excluded?:string}[];active_version:number;created_at:string};
export type Plan={project_id:string;version:number;revision:number;status:'draft'|'confirmed';hash:string;content:any;reason:string;base_version:number;created_at:string};
export type Doc={id:string;name:string;version:number;hash:string;text:string;status:string;source:string;copy_path:string};
export type Bundle={project:Project;messages:{id:string;role:string;text:string;created_at:string}[];plans:Plan[];documents:Doc[]};
