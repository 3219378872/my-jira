import { expect, test } from "@playwright/test";
import { login, password, project, projectAPI, projectRoute, request, workspace } from "./helpers";

test("real login, project layouts, work-item editing, comment, attachment and persistent reload", async ({ page }) => {
  const errors: string[] = []; page.on("pageerror", (error) => errors.push(error.message));
  await login(page);
  await page.goto(`${projectRoute}/issues`);
  await expect(page.getByText("重新梳理知识库首页的信息层级", { exact: true })).toBeVisible();
  for (const layout of ["看板", "表格", "日历", "时间线", "列表"]) {
    await page.getByRole("button", { name: layout, exact: true }).click();
    await expect(page.getByRole("button", { name: layout, exact: true })).toHaveAttribute("aria-pressed", "true");
  }
  const title = `Browser task ${Date.now()}`;
  await page.getByRole("button", { name: "添加工作项", exact: true }).first().click();
  await page.getByRole("textbox", { name: "工作项标题" }).fill(title);
  await page.getByRole("dialog").locator(".tiptap").fill("Created through the browser editor.");
  const created = page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith("/issues"));
  await page.getByRole("button", { name: "创建工作项", exact: true }).click();
  const response = await created; expect(response.status()).toBe(201); const issue = (await response.json()).data;
  try {
    await page.getByText(title, { exact: true }).first().click();
    await expect(page.getByRole("textbox", { name: "工作项标题" })).toHaveValue(title);
    await page.locator(".comment-composer .tiptap").fill("Browser discussion survives refresh.");
    await page.getByRole("button", { name: "发送评论", exact: true }).click();
    await expect(page.locator(".comment-item").getByText("Browser discussion survives refresh.")).toBeVisible();
    await page.locator('input[type="file"]').setInputFiles({ name: "browser-note.txt", mimeType: "text/plain", buffer: Buffer.from("Browser-owned attachment") });
    await expect(page.getByRole("link", { name: /^browser-note\.txt/ })).toBeVisible();
    await page.reload();
    await expect(page.locator(".comment-item").getByText("Browser discussion survives refresh.")).toBeVisible();
    await expect(page.getByRole("link", { name: /^browser-note\.txt/ })).toBeVisible();
    await page.screenshot({ path: ".local/evidence/browser-work-item-light.png", fullPage: true });
    expect(errors).toEqual([]);
  } finally { expect((await request(page, "DELETE", `${projectAPI}/issues/${issue.id}`)).status).toBe(204); }
});

test("two independent browser sessions collaborate, persist, synchronize title and export a PDF", async ({ page, browser }) => {
  await login(page);
  const created = await request(page, "POST", `${projectAPI}/pages`, { name: `Collaborative browser ${Date.now()}`, content_html: "<p>Shared starting point.</p>", content_json: { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "Shared starting point." }] }] } });
  expect(created.status).toBe(201); const id = created.body.data.id;
  const secondContext = await browser.newContext({ viewport: { width: 1440, height: 960 } }); const second = await secondContext.newPage();
  try {
    await second.goto("/login");
    await expect(second.locator("input[type=email]")).toBeVisible();
    const collaboratorEmail = "collaborator@myjira.local";
    let account = await request(second, "POST", "/api/v1/auth/login", { email: collaboratorEmail, password });
    if (account.status === 401) account = await request(second, "POST", "/api/v1/auth/register", { email: collaboratorEmail, password, display_name: "协作演示成员" });
    expect([200,201]).toContain(account.status); const userID = account.body.data.user.id;
    expect([201,409]).toContain((await request(page, "POST", `/api/v1/workspaces/${workspace}/members`, { email: collaboratorEmail, role: 15 })).status);
    expect([201,409]).toContain((await request(page, "POST", `${projectAPI}/members`, { user_id: userID, role: 15 })).status);
    await Promise.all([page.goto(`${projectRoute}/pages/${id}`), second.goto(`${projectRoute}/pages/${id}`)]);
    const firstEditor = page.locator(".document-editor .tiptap"); const secondEditor = second.locator(".document-editor .tiptap");
    await expect(firstEditor).toContainText("Shared starting point."); await expect(secondEditor).toContainText("Shared starting point.");
    await expect(firstEditor).toHaveAttribute("contenteditable", "true");
    await firstEditor.click(); await page.keyboard.press("Control+End"); await page.keyboard.type(" First browser contribution.");
    await expect(secondEditor).toContainText("First browser contribution.");
    await secondEditor.click(); await second.keyboard.press("Control+End"); await second.keyboard.type(" Second browser contribution.");
    await expect(firstEditor).toContainText("Second browser contribution.");
    await expect.poll(async () => (await request(page, "GET", `${projectAPI}/pages/${id}/content`)).body.data.content_html).toContain("Second browser contribution.");
    const updatedName = "Title synchronized across sessions";
    await page.getByRole("textbox", { name: "文档标题" }).fill(updatedName); await firstEditor.click();
    await expect(second.getByRole("textbox", { name: "文档标题" })).toHaveValue(updatedName);
    await second.reload(); await expect(second.locator(".document-editor .tiptap")).toContainText("First browser contribution.");
    await expect(second.locator(".document-editor .tiptap")).toContainText("Second browser contribution.");
    const documentName = encodeURIComponent(`${workspace}:${project}:${id}`);
    const pdf = await page.request.get(`/live/documents/${documentName}/pdf`);
    expect(pdf.status()).toBe(200); expect((await pdf.body()).subarray(0, 5).toString()).toBe("%PDF-");
    await page.screenshot({ path: ".local/evidence/browser-document-collaboration.png", fullPage: true });
    const history = (await request(page,"GET",`${projectAPI}/pages/${id}/versions`)).body.data;
    const initial = history.find((version: {version:number})=>version.version===1);
    expect(initial).toBeTruthy();
    const beforeRestore = (await request(page,"GET",`${projectAPI}/pages/${id}`)).body.data;
    expect((await request(page,"POST",`${projectAPI}/pages/${id}/versions/${initial.id}/restore`,{version:beforeRestore.version})).status).toBe(200);
    await expect(firstEditor).toHaveText("Shared starting point.");
    await expect(second.locator(".document-editor .tiptap")).toHaveText("Shared starting point.");
    await expect(firstEditor).toHaveAttribute("contenteditable","true");
    await firstEditor.click();await page.keyboard.press("Control+End");await page.keyboard.type(" Edit after history restore.");
    await expect(second.locator(".document-editor .tiptap")).toContainText("Edit after history restore.");
    await expect.poll(async ()=>(await request(page,"GET",`${projectAPI}/pages/${id}/content`)).body.data.content_html).toContain("Edit after history restore.");
    const current = (await request(page, "GET", `${projectAPI}/pages/${id}`)).body.data;
    expect((await request(page, "PATCH", `${projectAPI}/pages/${id}`, { is_locked: true, version: current.version })).status).toBe(200);
    await expect(firstEditor).toHaveAttribute("contenteditable", "false");
    await expect(second.locator(".document-editor .tiptap")).toHaveAttribute("contenteditable", "false");
  } finally {
    await secondContext.close();
    expect((await request(page,"PATCH",`${projectAPI}/pages/${id}`,{archived:true})).status).toBe(200);
    expect((await request(page,"DELETE",`${projectAPI}/pages/${id}`)).status).toBe(204);
  }
});

test("workspace, settings, instance administration and responsive themes render without crashes", async ({ page }) => {
  await login(page);
  const errors: string[] = []; page.on("pageerror", (error) => errors.push(error.message));
  for (const route of ["/w/studio/projects", "/w/studio/my-work", "/w/studio/inbox", "/w/studio/views", "/w/studio/analytics", `${projectRoute}/cycles`, `${projectRoute}/modules`, `${projectRoute}/pages`, `${projectRoute}/settings`, "/w/studio/settings", "/admin", "/admin/ai", "/admin/email"]) {
    await page.goto(route); await expect(page.locator("main")).toBeVisible(); await expect(page.getByText("这个页面遇到了问题", { exact: true })).toHaveCount(0);
  }
  await page.goto(`${projectRoute}/issues`);
  await page.getByRole("button", { name: "切换主题", exact: true }).click();
  await page.screenshot({ path: ".local/evidence/browser-project-dark.png", fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(()=>page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
  await page.screenshot({ path: ".local/evidence/browser-project-mobile.png", fullPage: true });
  expect(errors).toEqual([]);
});
