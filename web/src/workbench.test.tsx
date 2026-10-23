import{describe,it,expect,vi}from'vitest';import{render,screen,fireEvent,waitFor}from'@testing-library/react';import{App,ConfigProvider}from'antd';import Connections from'./connections';import ProjectView from'./project-view';import type{Bundle}from'./api';
function mount(component:React.ReactNode){return render(<ConfigProvider><App>{component}</App></ConfigProvider>)}
describe('M1 桌面界面操作',()=>{
it('自定义 API 选择协议、检查、保存，密钥不放入格式检查或浏览器存储',async()=>{
 let entries:any[]=[];const calls:{operation:string;payload:any}[]=[];
 window.workbench={invoke:vi.fn(async(operation,payload:any)=>{calls.push({operation,payload});if(operation==='connections.list')return{ok:true,value:entries};if(operation==='connections.check')return{ok:true,value:{resolved_endpoint:'https://gateway.invalid/v1/messages'}};if(operation==='connections.save'){const saved={id:'connection-1',label:payload.label,revision:1,config:payload.config,hasKey:true,hasProxy:false,configuredHeaders:[]};entries=[saved];return{ok:true,value:saved}};throw new Error(operation)}),onEvent:()=>()=>{}};
 mount(<Connections/>);await screen.findByText('新增自定义连接');
 fireEvent.change(screen.getByLabelText(/^连接名称/),{target:{value:'公司测试连接'}});
 fireEvent.change(screen.getByLabelText(/^服务地址/),{target:{value:'https://gateway.invalid'}});
 fireEvent.change(screen.getByLabelText(/^模型 ID/),{target:{value:'custom-company-model'}});
 fireEvent.change(screen.getByPlaceholderText('粘贴 API Key'),{target:{value:'SYNTHETIC_UI_KEY'}});
 fireEvent.click(screen.getByRole('button',{name:'检查配置'}));await screen.findByText('实际请求端点');
 const check=calls.find(c=>c.operation==='connections.check');expect(JSON.stringify(check)).not.toContain('SYNTHETIC_UI_KEY');expect(check?.payload.config.endpoint.protocol).toBe('messages');
 fireEvent.mouseDown(screen.getByLabelText(/^接口协议/));fireEvent.click(await screen.findByText('OpenAI Responses'));await waitFor(()=>expect(screen.getByText('Responses 兼容服务')).toBeTruthy());
 fireEvent.click(screen.getByRole('button',{name:/保存连接/}));await screen.findByText('公司测试连接');
 expect(calls.find(c=>c.operation==='connections.save')?.payload.config.endpoint.protocol).toBe('responses');
 expect((screen.getByPlaceholderText('已保存，修改时填写新 Key')as HTMLInputElement).value).toBe('');expect(localStorage.length).toBe(0);
});
it('折叠的连接高级设置在检测与保存时完整保留',async()=>{
 const saved={id:'loopback',label:'本机测试',revision:2,hasKey:true,hasProxy:false,enabled:true,maxConcurrent:3,models:[{id:'fixture',tools:'supported',price_known:true,currency:'CNY',input_price_micros_per_million:2000000,output_price_micros_per_million:8000000}],config:{endpoint:{protocol:'chat_completions',address:'http://127.0.0.1:3000',address_kind:'base_url',allow_local_http:true},model:'fixture',auth_kind:'bearer',key_env:'AWB_DESKTOP_KEY',egress:{mode:'system',dns_mode:'system'},timeout_seconds:90,max_output_tokens:1024,header_envs:{'X-Route':'AWB_DESKTOP_HEADER_0'}}};
 const calls:any[]=[];window.workbench={invoke:vi.fn(async(operation,payload:any)=>{calls.push({operation,payload});if(operation==='connections.list')return{ok:true,value:[saved]};if(operation==='connections.check')return{ok:true,value:{resolved_endpoint:'http://127.0.0.1:3000/v1/chat/completions'}};if(operation==='connections.save')return{ok:true,value:{...saved,revision:3}};throw new Error(operation)}),onEvent:()=>()=>{}};
 mount(<Connections/>);fireEvent.click(await screen.findByRole('button',{name:/本机测试.*fixture/}));
 fireEvent.click(screen.getByRole('button',{name:'检查配置'}));await screen.findByText('实际请求端点');
 const check=calls.find(call=>call.operation==='connections.check').payload.config;expect(check.endpoint.allow_local_http).toBe(true);expect(check.timeout_seconds).toBe(90);expect(check.max_output_tokens).toBe(1024);expect(check.header_envs).toEqual({'X-Route':'AWB_DESKTOP_HEADER_0'});
 fireEvent.click(screen.getByRole('button',{name:/保存连接/}));await waitFor(()=>expect(calls.some(call=>call.operation==='connections.save')).toBe(true));
 const input=calls.find(call=>call.operation==='connections.save').payload;expect(input.models[0].price_known).toBe(true);expect(input.models[0].input_price_micros_per_million).toBe(2000000);expect(input.maxConcurrent).toBe(3);
});
it('用户确认 V1 后创建 V2 草案，界面保留 V1 并持久保存手工消息',async()=>{
 const content={schema_version:1,project_id:'project-1',version:1,title:'自动任务方案',requirements:[{id:'REQ-1',text:'失败后自动重试两次'}],engineering_constraints:[{id:'ENG-1',text:'保持现有测试有效'}],acceptance:[{id:'AC-1',requirement_ids:['REQ-1'],description:'恰好重试两次',verification_profile:'verify-default'}],non_goals:['人工审批'],autonomy:{ordinary_tasks:'automatic',unresolved_questions:'ask_user',product_changes:'user_confirmed'}};
 const state:Bundle={project:{id:'project-1',name:'演示项目',source:'',snapshot:'/managed/source',manifest:[],active_version:0,created_at:new Date().toISOString()},documents:[],messages:[],plans:[{project_id:'project-1',version:1,revision:1,status:'draft',hash:'sha256:original',content,reason:'',base_version:0,created_at:new Date().toISOString()}]};
 window.workbench={invoke:vi.fn(async(operation,payload:any)=>{if(operation==='project.get')return{ok:true,value:structuredClone(state)};if(operation==='events.start'||operation==='events.stop')return{ok:true,value:{}};if(operation==='plan.confirm'){expect(payload.expected_hash).toBe('sha256:original');state.plans[0].status='confirmed';state.project.active_version=1;return{ok:true,value:state.plans[0]}};if(operation==='plan.create'){expect(payload.base_version).toBe(1);const plan={...state.plans[0],version:2,status:'draft'as const,hash:'sha256:new-draft',content:payload.content,reason:payload.reason,base_version:1};state.plans.push(plan);return{ok:true,value:plan}};if(operation==='message.append'){state.messages.push({id:'msg-1',role:'user',text:payload.text,created_at:new Date().toISOString()});return{ok:true,value:state.messages[0]}};throw new Error(operation)}),onEvent:()=>()=>{}};
 mount(<ProjectView id="project-1" onChange={()=>{}}/>);fireEvent.click(await screen.findByRole('button',{name:'确认 V1'}));await screen.findByText('当前确认 V1');
 fireEvent.click(screen.getByRole('button',{name:'提出变更'}));fireEvent.change(await screen.findByLabelText('方案标题'),{target:{value:'V2 变更讨论'}});fireEvent.change(screen.getByLabelText('变更原因'),{target:{value:'新需求尚待确认'}});fireEvent.click(screen.getByRole('button',{name:'保存草案'}));
 await screen.findByText('V2 变更讨论');expect(state.project.active_version).toBe(1);expect(state.plans[0].hash).toBe('sha256:original');expect(state.plans[1].status).toBe('draft');
 fireEvent.change(screen.getByPlaceholderText('记录需求或补充说明，内容会持久保存'),{target:{value:'需求记录需要保留。'}});fireEvent.click(screen.getByRole('button',{name:'保存消息'}));await screen.findByText('需求记录需要保留。');expect(state.messages).toHaveLength(1);
});
});
