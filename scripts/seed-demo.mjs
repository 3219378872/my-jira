import assert from "node:assert/strict";

const base = process.env.MYJIRA_API_URL ?? "http://127.0.0.1:8088";
const target = new URL(base);
assert(["127.0.0.1", "localhost"].includes(target.hostname), "Demo fixtures are restricted to a local development instance");
const email = process.env.MYJIRA_DEMO_EMAIL ?? "demo@myjira.local";
const password = process.env.MYJIRA_DEMO_PASSWORD ?? "MyJira-Local-2026!";
const cookies = new Map();
let csrf = "";

async function request(path, method = "GET", body) {
  const response = await fetch(`${base}/api/v1${path}`, {
    method,
    headers: { "Content-Type": "application/json", Origin: "http://127.0.0.1:4173", Cookie: [...cookies].map(([key, value]) => `${key}=${value}`).join("; "), "X-CSRF-Token": csrf },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  for (const cookie of response.headers.getSetCookie()) {
    const pair = cookie.split(";")[0];
    const equals = pair.indexOf("=");
    cookies.set(pair.slice(0, equals), pair.slice(equals + 1));
  }
  const json = response.status === 204 ? {} : await response.json();
  assert(response.ok, `${method} ${path}: ${response.status} ${JSON.stringify(json)}`);
  if (json.data?.csrf_token) csrf = json.data.csrf_token;
  return json.data;
}

await request("/auth/csrf");
const instance = await request("/instance");
if (!instance.is_setup_done) await request("/instance/setup", "POST", { instance_name: "My Jira", email, password, display_name: "演示管理员" });
else await request("/auth/login", "POST", { email, password });

let workspaces = await request("/workspaces");
let workspace = workspaces.find((entry) => entry.slug === "studio");
if (!workspace) workspace = await request("/workspaces", "POST", { name: "星河工作室", slug: "studio", timezone: "Asia/Shanghai" });
const workspacePath = `/workspaces/${workspace.id}`;
const projects = await request(`${workspacePath}/projects`);
let project = projects.find((entry) => entry.identifier === "ORBIT");
if (!project) project = await request(`${workspacePath}/projects`, "POST", { name: "Orbit · 团队知识库", identifier: "ORBIT", description: "让团队的每一次分享都能被找到、被理解、被继续使用。", network: "public", icon: "orbit", color: "#6366f1" });
const projectPath = `${workspacePath}/projects/${project.id}`;
const states = await request(`${projectPath}/states`);
const getState = (group) => states.find((state) => (state.group ?? state.group_name) === group)?.id ?? states[0].id;
const labels = await request(`${projectPath}/labels`);
for (const [name, color] of [["体验优化", "#8b5cf6"], ["新功能", "#3b82f6"], ["用户反馈", "#f59e0b"], ["内容", "#10b981"]]) {
  if (!labels.some((label) => label.name === name)) labels.push(await request(`${projectPath}/labels`, "POST", { name, color, description: "" }));
}
const cycles = await request(`${projectPath}/cycles`);
let cycle = cycles.find((entry) => entry.name === "九月 · 体验提升");
if (!cycle) cycle = await request(`${projectPath}/cycles`, "POST", { name: "九月 · 体验提升", description: "聚焦阅读、搜索与分享体验", start_date: "2026-09-01", end_date: "2026-09-30" });
const modules = await request(`${projectPath}/modules`);
let module = modules.find((entry) => entry.name === "阅读与发现");
if (!module) module = await request(`${projectPath}/modules`, "POST", { name: "阅读与发现", description: "帮助每个人更快找到有用的信息", status: "in-progress" });
const existing = await request(`${projectPath}/issues?limit=500`);
const samples = [
  ["重新梳理知识库首页的信息层级", "started", "high", "体验优化", "2026-09-12"],
  ["为文章增加阅读进度和目录导航", "started", "medium", "新功能", "2026-09-15"],
  ["整理第一轮用户访谈的反馈", "completed", "high", "用户反馈", "2026-09-07"],
  ["支持按主题收藏与整理文章", "unstarted", "high", "新功能", "2026-09-18"],
  ["优化搜索结果中的关键词提示", "started", "urgent", "体验优化", "2026-09-10"],
  ["设计更清晰的空白页面引导", "unstarted", "medium", "体验优化", "2026-09-16"],
  ["准备团队新人入门指南", "completed", "medium", "内容", "2026-09-08"],
  ["调整手机端文章的段落间距", "unstarted", "low", "体验优化", "2026-09-22"],
  ["为分享链接增加有效期设置", "backlog", "medium", "新功能", "2026-09-25"],
  ["汇总本月最受欢迎的团队分享", "backlog", "low", "内容", "2026-09-28"],
  ["改进评论区的回复提醒", "unstarted", "high", "用户反馈", "2026-09-20"],
  ["探索更自然的跨主题发现方式", "backlog", "none", "新功能", "2026-09-30"],
];
for (const [name, group, priority, label, target_date] of samples) {
  if (existing.some((item) => item.name === name)) continue;
  await request(`${projectPath}/issues`, "POST", { name, state_id: getState(group), priority, description_html: `<p>${name}，让团队协作更清晰、顺畅。</p><h2>验收标准</h2><ul><li>完成主要使用场景的设计与实现</li><li>邀请团队成员体验并记录反馈</li><li>补充使用说明</li></ul>`, label_ids: [labels.find((entry) => entry.name === label).id], cycle_id: cycle.id, module_ids: [module.id], start_date: "2026-09-09", target_date: target_date < "2026-09-09" ? "2026-09-09" : target_date });
}
const pages = await request(`${projectPath}/pages`);
if (!pages.some((page) => page.name === "一起把好想法变成产品")) {
  await request(`${projectPath}/pages`, "POST", { name: "一起把好想法变成产品", icon: "✨", content_html: "<h1>一起把好想法变成产品</h1><p>这里是 Orbit 的团队工作空间。我们用清晰的小目标，把讨论变成可以看见的进展。</p><h2>这个月，我们关注什么？</h2><ul><li>让有用的信息更容易被发现</li><li>让阅读和分享成为轻松的习惯</li><li>认真听取每一条用户反馈</li></ul><blockquote><p>好的协作，始于每个人都知道下一步。</p></blockquote>", content_json: { type: "doc", content: [{ type: "heading", attrs: { level: 1 }, content: [{ type: "text", text: "一起把好想法变成产品" }] }, { type: "paragraph", content: [{ type: "text", text: "这里是 Orbit 的团队工作空间。我们用清晰的小目标，把讨论变成可以看见的进展。" }] }] } });
}
console.log(JSON.stringify({ status: "ready", email, workspace_id: workspace.id, project_id: project.id, path: `/w/${workspace.slug}/projects/${project.id}/issues`, fixtures: "Local demo data; independently authored" }, null, 2));
