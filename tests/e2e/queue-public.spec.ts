import {expect,test} from "@playwright/test";
import {collaborator,login,projectAPI,request,workspace} from "./helpers";

const mailpitURL = process.env.E2E_MAILPIT_URL ?? "http://127.0.0.1:28025";

test("HTTP writes traverse Redis worker to notifications, SMTP, filtered exports and token reads",async({page,browser})=>{
  test.setTimeout(90_000);
  await login(page);const memberContext=await browser.newContext();const memberPage=await memberContext.newPage();const member=await collaborator(memberPage);
  expect([201,409]).toContain((await request(page,"POST",`/api/v1/workspaces/${workspace}/members`,{email:member.email,role:15})).status);
  expect([201,409]).toContain((await request(page,"POST",`${projectAPI}/members`,{user_id:member.id,role:15})).status);
  const title=`Queue journey ${Date.now()}`;
  const created=await request(page,"POST",`${projectAPI}/issues`,{name:title,assignee_ids:[member.id]});expect(created.status).toBe(201);const id=created.body.data.id;
  const token=await request(page,"POST",`/api/v1/workspaces/${workspace}/api-tokens`,{name:`Browser queue test ${Date.now()}`});expect(token.status).toBe(201);
  const tokenContext=await browser.newContext();
  try{
    await expect.poll(async()=>(await request(memberPage,"GET",`/api/v1/workspaces/${workspace}/notifications?reason=assigned`)).body.data.some((notification:{entity_id:string})=>notification.entity_id===id),{timeout:25_000}).toBe(true);
    await expect.poll(async()=>{const result=await page.request.get(`${mailpitURL}/api/v1/messages?limit=100`);const mail=await result.json();return mail.messages.some((message:{Subject:string;To:{Address:string}[]})=>message.Subject.includes(title)&&message.To.some(to=>to.Address===member.email))},{timeout:25_000}).toBe(true);
    const exported=await request(page,"POST",`/api/v1/workspaces/${workspace}/exports`,{format:"csv",filters:{search:title,assignee_id:[member.id]}});expect(exported.status).toBe(202);
    await expect.poll(async()=>{const result=await request(page,"GET",`/api/v1/workspaces/${workspace}/exports`);return result.body.data.find((item:{id:string})=>item.id===exported.body.data.id)?.status},{timeout:25_000}).toBe("completed");
    const csv=await page.request.get(exported.body.data.download_url);expect(csv.status()).toBe(200);expect(await csv.text()).toContain(title);
    const authenticated=await tokenContext.request.get(`${projectAPI}/issues/${id}`,{headers:{Authorization:`Bearer ${token.body.data.token}`}});expect(authenticated.status()).toBe(200);expect((await authenticated.json()).data.name).toBe(title);
    expect((await request(page,"DELETE",`/api/v1/workspaces/${workspace}/api-tokens/${token.body.data.id}`)).status).toBe(204);
    expect((await tokenContext.request.get(`${projectAPI}/issues/${id}`,{headers:{Authorization:`Bearer ${token.body.data.token}`}})).status()).toBe(401);
  }finally{await request(page,"DELETE",`/api/v1/workspaces/${workspace}/api-tokens/${token.body.data.id}`);await tokenContext.close();await memberContext.close();await request(page,"DELETE",`${projectAPI}/issues/${id}`);}
});

test("public board anonymous reading, authenticated feedback and disable switches work in browsers",async({page,browser})=>{
  await login(page);
  const suffix=Date.now().toString(36);const result=await request(page,"POST",`/api/v1/workspaces/${workspace}/projects`,{name:`Public browser ${suffix}`,identifier:`P${suffix.toUpperCase()}`,network:"private"});expect(result.status).toBe(201);
  const pid=result.body.data.id;const api=`/api/v1/workspaces/${workspace}/projects/${pid}`;const slug=`browser-${suffix}`;
  const publicContext=await browser.newContext();const publicPage=await publicContext.newPage();
  try{
    const created=await request(page,"POST",`${api}/issues`,{name:"Public browser feedback item"});expect(created.status).toBe(201);const id=created.body.data.id;
    await request(page,"POST",`${api}/issues/${id}/comments`,{body_html:"<p>INTERNAL_BROWSER_SECRET</p>"});
    const configuration={slug,title:"Browser public board",is_enabled:true,comments_enabled:true,reactions_enabled:true,votes_enabled:true,intake_enabled:true};
    expect((await request(page,"PUT",`${api}/site`,configuration)).status).toBe(200);
    await publicPage.goto(`/public/${slug}/issues/${id}`);await expect(publicPage.getByRole("heading",{name:"Public browser feedback item"})).toBeVisible();await expect(publicPage.getByText("INTERNAL_BROWSER_SECRET")).toHaveCount(0);
    expect((await request(publicPage,"POST",`/api/v1/public/${slug}/issues/${id}/vote`,{})).status).toBe(401);
    await collaborator(publicPage);await publicPage.goto(`/public/${slug}/issues/${id}`);
    await publicPage.getByPlaceholder("分享你的反馈…").fill("A real public browser contribution.");await publicPage.getByRole("button",{name:"发表评论",exact:true}).click();await expect(publicPage.locator(".comment-item")).toContainText("A real public browser contribution.");
    await publicPage.getByRole("button",{name:"👍 回应",exact:true}).click();await expect(publicPage.getByRole("button",{name:"👍 回应",exact:true})).toContainText("1");
    expect((await request(publicPage,"POST",`/api/v1/public/${slug}/issues/${id}/vote`,{})).status).toBe(200);
    await publicPage.reload();await expect(publicPage.locator(".comment-item")).toContainText("A real public browser contribution.");
    const intake=await request(publicPage,"POST",`/api/v1/public/${slug}/intake`,{name:"Public submitted request",description_html:"<p>Consider this idea.</p>"});expect(intake.status).toBe(201);
    const visible=await request(publicPage,"GET",`/api/v1/public/${slug}/issues`);expect(visible.body.data.map((item:{id:string})=>item.id)).not.toContain(intake.body.data.id);
    await publicPage.screenshot({path:".local/evidence/browser-public-board.png",fullPage:true});
    expect((await request(page,"PUT",`${api}/site`,{...configuration,comments_enabled:false,reactions_enabled:false,votes_enabled:false,intake_enabled:false})).status).toBe(200);
    for(const [path,body]of [[`issues/${id}/comments`,{body_html:"Blocked"}],[`issues/${id}/reactions`,{emoji:"👍"}],[`issues/${id}/vote`,{}],["intake",{name:"Blocked"}]] as const){expect((await request(publicPage,"POST",`/api/v1/public/${slug}/${path}`,body)).status).toBe(403)}
    expect((await request(page,"PUT",`${api}/site`,{...configuration,is_enabled:false})).status).toBe(200);
    expect((await request(publicPage,"GET",`/api/v1/public/${slug}`)).status).toBe(404);
    await publicPage.reload();await expect(publicPage.getByRole("heading",{name:"Public browser feedback item"})).toHaveCount(0);
  }finally{await publicContext.close();await request(page,"DELETE",api);}
});
