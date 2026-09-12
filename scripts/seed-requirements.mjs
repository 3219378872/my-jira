// Original, repeatable acceptance fixtures. Only the dedicated local test API.
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { readFile } from "node:fs/promises";

const base = process.env.MYJIRA_API_URL ?? "http://127.0.0.1:18088";
const target = new URL(base);
assert(["127.0.0.1", "localhost"].includes(target.hostname) && target.port === "18088", "Requirements fixtures require the isolated test API on localhost:18088");
const origin = "http://127.0.0.1:14173";
const password = "MyJira-Local-2026!";
const adminEmail = "demo@myjira.local";
function client() {
  const cookies = new Map();
  let csrf = "";
  return async function request(path, method = "GET", body, optional = false) {
    const res = await fetch(`${base}/api/v1${path}`, {
      method,
      headers: { "Content-Type": "application/json", Origin: origin, Cookie: [...cookies].map(([k,v]) => `${k}=${v}`).join("; "), "X-CSRF-Token": csrf },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    for (const cookie of res.headers.getSetCookie()) {
      const pair = cookie.split(";")[0], n = pair.indexOf("=");
      cookies.set(pair.slice(0,n), pair.slice(n+1));
    }
    const json = res.status === 204 ? {} : await res.json();
    if (!res.ok && optional) return null;
    assert(res.ok, `${method} ${path}: ${res.status} ${JSON.stringify(json)}`);
    if (json.data?.csrf_token) csrf = json.data.csrf_token;
    return json.data;
  };
}
const request = client();
await request("/auth/csrf");
const instance = await request("/instance");
if (!instance.is_setup_done) await request("/instance/setup", "POST", { instance_name: "My Jira Requirements Test", email: adminEmail, password, display_name: "需求验收管理员" });
else {
  assert.equal(instance.name, "My Jira Requirements Test", "Refusing to seed an existing business instance");
  await request("/auth/login", "POST", { email: adminEmail, password });
}
const admin = await request("/auth/me");
const accounts = [{ email:"member@requirements.test",display_name:"林舟 · 前后端",role:15 },{ email:"guest@requirements.test",display_name:"许宁 · 访客",role:5 },{ email:"outsider@requirements.test",display_name:"无关租户",role:0 }];
for (const account of accounts) {
  const other = client();
  await other("/auth/csrf");
  const auth = await other("/auth/login", "POST", { email: account.email, password }, true)
    ?? await other("/auth/register", "POST", { email:account.email,password,display_name:account.display_name });
  account.user_id = auth.user.id;
  if (!account.role) {
    const own = await other("/workspaces");
    if (!own.some(w => w.slug === "unrelated-requirements")) await other("/workspaces", "POST", { name:"无关租户工作区",slug:"unrelated-requirements" });
  }
}
let workspace = (await request("/workspaces")).find(w => w.slug === "studio");
workspace ??= await request("/workspaces", "POST", {name:"星河需求实验室",slug:"studio",timezone:"Asia/Shanghai"});
const w = `/workspaces/${workspace.id}`;
let members = await request(`${w}/members`);
for (const account of accounts.filter(a=>a.role)) if (!members.some(m=>m.user_id===account.user_id)) await request(`${w}/members`,"POST",{email:account.email,role:account.role});
let project = (await request(`${w}/projects`)).find(p=>p.identifier==="REQ");
project ??= await request(`${w}/projects`,"POST",{name:"一站式协作 · 需求基线",identifier:"REQ",network:"private",description:"三大Epic、三个Sprint与四视图的独立验收数据"});
const p = `${w}/projects/${project.id}`;
members = await request(`${p}/members`);
for (const account of accounts.filter(a=>a.role)) if (!members.some(m=>m.user_id===account.user_id)) await request(`${p}/members`,"POST",{user_id:account.user_id,role:account.role});
const states = await request(`${p}/states`);
const state = group => states.find(s=>(s.group ?? s.group_name)===group)?.id ?? states[0].id;
const cycles = await request(`${p}/cycles`);
for (const [name,start_date,end_date] of [["S1 · 进入并组织工作","2026-09-14","2026-09-25"],["S2 · 协作并观察执行","2026-09-28","2026-10-09"],["S3 · 交付切片与复盘","2026-10-12","2026-10-23"]]) {
  if (!cycles.some(c=>c.name===name)) cycles.push(await request(`${p}/cycles`,"POST",{name,start_date,end_date}));
}
const items = await request(`${p}/issues?limit=500`);
const byName = new Map(items.map(i=>[i.name,i]));
async function issue(name, fields) {
  if (byName.has(name)) return byName.get(name);
  const item = await request(`${p}/issues`,"POST",{name,state_id:state("unstarted"),priority:"medium",...fields});
  byName.set(name,item); return item;
}
const epics = {};
for (const [key,name] of [["E1","用户管理与权限"],["E2","任务与协作"],["E3","数据与分析"]]) {
  const position = (Object.keys(epics).length + 1) * 1024;
  epics[key] = await issue(`${key} · ${name}`,{requirement_type:"epic",map_position:position});
  if (epics[key].map_position !== position) epics[key] = await request(`${p}/issues/${epics[key].id}`,"PATCH",{version:epics[key].version,map_position:position});
}
const activities = await request(`${p}/requirements/activities`);
for (const [epic,name] of [["E1","加入团队"],["E2","安排工作"],["E2","推进协作"],["E3","检查进展"],["E3","复盘改进"]]) {
  if (!activities.some(a=>a.name===name)) activities.push(await request(`${p}/requirements/activities`,"POST",{name,epic_id:epics[epic].id,position:(activities.length+1)*1024}));
}
const definitions = [
  ["U04","E1",1,"用户","注册、登录和退出","安全进入协作空间","加入团队"],
  ["U05","E1",1,"获准成员","创建工作区和项目","建立协作范围","加入团队"],
  ["T01","E2",1,"团队成员","创建工作项并指定负责人","明确责任","安排工作"],
  ["T06","E2",1,"团队成员","搜索筛选和切换视图","找到所需任务","推进协作"],
  ["D01","E3",1,"项目经理","查看总量、状态和完成分布","掌握项目现状","检查进展"],
  ["D05","E3",1,"项目经理","查看成员任务分布","了解责任分配","检查进展"],
  ["U01","E1",2,"管理员","邀请成员并分配固定角色","建立可控团队","加入团队"],
  ["U02","E1",2,"管理员","调整或移除成员","维护访问范围","加入团队"],
  ["T02","E2",2,"团队成员","拆分任务维护依赖和状态","推进执行","安排工作"],
  ["T04","E2",2,"团队成员","评论提及订阅和共享附件","协作处理工作","推进协作"],
  ["D02","E3",2,"项目经理","按成员周期和日期查看趋势","发现执行变化","检查进展"],
  ["D04","E3",2,"项目经理","查看周期和模块进度","检查阶段目标","检查进展"],
  ["U03","E1",3,"管理员","确保搜索统计导出遵循权限","控制数据访问","加入团队"],
  ["U06","E1",3,"用户","管理资料与通知偏好","适配个人协作方式","加入团队"],
  ["T03","E2",3,"团队","将工作纳入三个Sprint并维护日期","形成可执行切片","安排工作"],
  ["T05","E2",3,"团队","维护PRD和协作文档","保留需求依据","推进协作"],
  ["D03","E3",3,"项目经理","保存和导出分析","开展复盘","复盘改进"],
  ["D06","E3",3,"团队","追踪需求与验收覆盖","判断基线完整性","复盘改进"],
];
const stories = {};
for (const [id,epic,sprint,role,goal,benefit,activity] of definitions) {
  const cycle = cycles.find(c=>c.name.startsWith(`S${sprint} `));
  stories[id] = await issue(`${id} · ${goal}`,{requirement_type:"story",parent_id:epics[epic].id,story_role:role,story_goal:goal,story_benefit:benefit,acceptance_criteria:["保存后重载保持一致","当前权限与跨租户边界得到验证","错误或并发冲突不会覆盖他人修改"],activity_id:activities.find(a=>a.name===activity).id,cycle_id:cycle.id,start_date:cycle.start_date.slice(0,10),target_date:cycle.end_date.slice(0,10),map_position:(Object.keys(stories).length+1)*1024});
}
const member = accounts[0];
await request(`${p}/resources`,"PATCH",{timezone:"Asia/Shanghai"});
const resource = await request(`${p}/resources`);
for (const [member_id,skills,exceptions] of [[admin.id,["后端","测试"],{"2026-09-16":0}],[member.user_id,["前端","后端"],{"2026-09-18":240}]]) {
  const old = resource.members.find(m=>m.member_id===member_id);
  await request(`${p}/resources/members/${member_id}`,"PUT",{version:old?.version??0,skills,weekday_minutes:[0,480,480,480,480,480,0],project_minutes_per_day:480,exceptions});
}
const leaf = await issue("T01-A · 16小时共同实现",{requirement_type:"task",parent_id:stories.T01.id,estimated_minutes:960,remaining_minutes:960,assignee_ids:[admin.id,member.user_id],allocation_weights:[{member_id:admin.id,weight:75},{member_id:member.user_id,weight:25}],required_skills:["后端"],start_date:"2026-09-14",target_date:"2026-09-17"});
await issue("T01-B · 跨执行Sprint回归",{requirement_type:"task",parent_id:stories.T01.id,estimated_minutes:240,remaining_minutes:240,assignee_ids:[admin.id],required_skills:["测试"],cycle_id:cycles.find(c=>c.name.startsWith("S2 ")).id,start_date:"2026-09-28",target_date:"2026-09-29",dependency_ids:[leaf.id]});
await issue("T02-A · 未排期与技能缺口",{requirement_type:"task",parent_id:stories.T02.id,estimated_minutes:1440,remaining_minutes:1440,required_skills:["数据科学"]});
await issue("Backlog · 待澄清跨团队协作",{requirement_type:"story",parent_id:epics.E2.id,story_role:"团队成员",story_goal:"澄清跨团队交付","story_benefit":"保留未承诺范围",activity_id:activities.find(a=>a.name==="推进协作").id});
let pages = await request(`${p}/pages`);
const prd = await readFile(new URL("../tests/fixtures/requirements-prd.md",import.meta.url),"utf8");
let page = pages.find(x=>x.name==="一站式协作PRD · 标注v1");
page ??= await request(`${p}/pages`,"POST",{name:"一站式协作PRD · 标注v1",content_html:`<p>${prd.replaceAll("&","&amp;").replaceAll("<","&lt;").replaceAll(">","&gt;")}</p>`,content_json:{type:"doc",content:[{type:"paragraph",content:[{type:"text",text:prd}]}]}});
if (!pages.some(x=>x.name==="私有权限边界来源")) await request(`${p}/pages`,"POST",{name:"私有权限边界来源",is_private:true,content_html:"<p>仅所有者读取的场景来源，不得扩大图的读者范围。</p>",content_json:{type:"doc",content:[{type:"paragraph",content:[{type:"text",text:"仅所有者读取的场景来源，不得扩大图的读者范围。"}]}]}});
const scenarios = await request(`${p}/scenarios`);
for (const [id,name] of [["T01","分配负责人并提交"],["U01","邀请成员与权限分支"]]) if (!scenarios.some(x=>x.name===name)) {
  const actor = randomUUID(),system = randomUUID(),call = randomUUID();
  const steps = [{id:call,kind:"call",from_id:actor,to_id:system,message:"提交请求 <中文 & 特殊字符>"},{id:randomUUID(),kind:"return",from_id:system,to_id:actor,return_of:call,message:"返回保存结果"}];
  if (id==="U01") steps.push({id:randomUUID(),kind:"alt",message:"邀请邮箱可用"},{id:randomUUID(),kind:"call",from_id:system,to_id:actor,message:"发送邀请"},{id:randomUUID(),kind:"else",message:"已存在成员"},{id:randomUUID(),kind:"call",from_id:system,to_id:actor,message:"显示可定位的原因"},{id:randomUUID(),kind:"end",message:""});
  const scenario = await request(`${p}/scenarios`,"POST",{story_id:stories[id].id,name,goal:"安全且可追踪地完成业务操作",trigger:"用户提交表单",preconditions:"用户有当前项目访问权",outcome:"变更保存且发布事件",system_boundary:"协作平台",bind_story_name:false,source_page_ids:[page.id],participants:[{id:actor,name:"团队成员",kind:"actor"},{id:system,name:"协作系统",kind:"system"}],steps,relationships:[]});
  await request(`${p}/scenarios/${scenario.id}`,"PATCH",{version:scenario.version,relationships:[{id:randomUUID(),kind:"association",from_id:actor,to_id:scenario.id}]});
}
console.log(JSON.stringify({status:"ready",workspace_id:workspace.id,project_id:project.id,route:`/w/studio/projects/${project.id}/requirements`,page_id:page.id,stories:Object.fromEntries(Object.entries(stories).map(([k,v])=>[k,v.id])),accounts:[{email:adminEmail,user_id:admin.id},...accounts],provenance:"Original fixed Chinese test fixtures; isolated requirements stack only"},null,2));
