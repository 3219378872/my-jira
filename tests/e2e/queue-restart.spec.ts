import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { expect,test } from "@playwright/test";
import { password,request } from "./helpers";

test("queued export survives isolated worker and Redis restart and duplicate delivery",async({page})=>{
  test.skip(process.env.E2E_FIRST_RUN!=="1","Requires the dedicated, initialized bootstrap Compose installation");
  test.setTimeout(120_000);
  expect(process.env.E2E_BASE_URL).toBe("http://127.0.0.1:4181");
  const projectName="my-jira-bootstrap-test";
  const queueProgram=resolve(".local/queue-control");
  execFileSync("go",["build","-o",queueProgram,"../../tests/queue-control/main.go"],{cwd:resolve("apps/api"),timeout:45_000});
  const queue=(action:string,taskID?:string)=>execFileSync(queueProgram,[action,...taskID?[taskID]:[]],{env:{...process.env,QUEUE_CHECK_REDIS_ADDR:"127.0.0.1:26380",QUEUE_CHECK_COMPOSE_PROJECT:projectName},timeout:10_000}).toString();
  const queueStatus=()=>{try{return JSON.parse(queue("status"))}catch{return{}}};
  const compose=(...args:string[])=>execFileSync("docker",["compose","-p",projectName,"-f","compose.yaml","-f","compose.app.yaml",...args],{timeout:30_000,stdio:["ignore","pipe","pipe"]}).toString();
  const sql=(query:string)=>compose("exec","-T","postgres","psql","-U","myjira","-d","myjira","-At","-c",query).trim();
  await page.goto("/login");expect((await request(page,"POST","/api/v1/auth/login",{email:"bootstrap-admin@myjira.local",password})).status).toBe(200);
  const workspaces=await request(page,"GET","/api/v1/workspaces");const workspace=workspaces.body.data.find((item:{slug:string})=>item.slug==="bootstrap-acceptance");expect(workspace).toBeTruthy();
  const prefix=`/api/v1/workspaces/${workspace.id}`;
  const created=await request(page,"POST",`${prefix}/projects`,{name:"Durable queue acceptance",identifier:`Q${Date.now().toString(36).toUpperCase()}`,network:"private"});expect(created.status).toBe(201);
  const project=created.body.data;
  try{
    const issue=await request(page,"POST",`${prefix}/projects/${project.id}/issues`,{name:"Persisted across the queue restart"});expect(issue.status).toBe(201);
    queue("pause");
    await expect.poll(()=>JSON.parse(queue("status")).active).toBe(0);
    const exported=await request(page,"POST",`${prefix}/exports`,{project_id:project.id,format:"json",filters:{search:"Persisted across the queue restart"}});expect(exported.status).toBe(202);const exportID=exported.body.data.id;
    expect(exportID).toMatch(/^[0-9a-f-]{36}$/);
    await expect.poll(()=>JSON.parse(queue("status")).pending,{timeout:15_000}).toBeGreaterThan(0);
    await expect.poll(()=>sql(`SELECT id FROM outbox_events WHERE topic='export.generate' AND payload->>'export_id'='${exportID}' AND dispatched_at IS NOT NULL`)).toMatch(/^[0-9a-f-]{36}$/);
    const taskID=sql(`SELECT id FROM outbox_events WHERE topic='export.generate' AND payload->>'export_id'='${exportID}' AND dispatched_at IS NOT NULL`);
    expect(JSON.parse(queue("task",taskID)).state).toBe("pending");
    expect(sql(`SELECT status FROM exports WHERE id='${exportID}'`)).toBe("queued");
    compose("stop","worker");compose("restart","redis");
    await expect.poll(()=>queueStatus().paused,{timeout:15_000}).toBe(true);
    expect(JSON.parse(queue("status")).pending).toBeGreaterThan(0);
    expect(JSON.parse(queue("task",taskID)).state).toBe("pending");
    expect(sql(`SELECT status FROM exports WHERE id='${exportID}'`)).toBe("queued");
    compose("start","worker");queue("resume");
    const getExport=async()=>(await request(page,"GET",`${prefix}/exports`)).body.data.find((item:{id:string})=>item.id===exportID);
    await expect.poll(async()=>(await getExport()).status,{timeout:30_000}).toBe("completed");
    const downloadURL=exported.body.data.download_url;expect(downloadURL).toEqual(`${prefix}/exports/${exportID}/download`);
    const first=await page.request.get(downloadURL);expect(first.status()).toBe(200);const original=await first.body();expect(original.toString()).toContain("Persisted across the queue restart");
    const completedAt=sql(`SELECT completed_at::text FROM exports WHERE id='${exportID}'`);
    queue("replay",taskID);
    await expect.poll(()=>JSON.parse(queue("task",taskID+"-acceptance-replay")).state).toBe("completed");
    expect(sql(`SELECT count(*) FROM exports WHERE id='${exportID}'`)).toBe("1");
    expect(sql(`SELECT completed_at::text FROM exports WHERE id='${exportID}'`)).toBe(completedAt);
    const again=await page.request.get(downloadURL);expect(again.status()).toBe(200);expect(await again.body()).toEqual(original);
  }finally{
    compose("start","worker","redis");
    if(JSON.parse(queue("status")).paused)queue("resume");
    await request(page,"DELETE",`${prefix}/projects/${project.id}`);
  }
});
