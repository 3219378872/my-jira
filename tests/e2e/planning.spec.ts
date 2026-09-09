import { expect, test, type Page } from "@playwright/test";
import { login, request, workspace } from "./helpers";

type Issue = { id: string; name: string; version: number; estimate: number; target_date: string | null };

test.beforeEach(({ page }) => {
  page.setDefaultTimeout(15_000);
});

function date(offset: number) {
  const value = new Date();
  value.setUTCDate(value.getUTCDate() + offset);
  return value.toISOString().slice(0, 10);
}

async function fixture(page: Page, count: number) {
  const result = await request(page, "POST", `/api/v1/workspaces/${workspace}/projects`, {
    name: `E03 browser acceptance ${Date.now()}`,
    identifier: `E${Date.now().toString(36).toUpperCase()}`,
    network: "private",
    features: { cycles: true, modules: true, views: true, pages: true, intake: true },
  });
  expect(result.status).toBe(201);
  const project = result.body.data;
  const api = `/api/v1/workspaces/${workspace}/projects/${project.id}`;
  const route = `/w/studio/projects/${project.id}`;
  const stateResponse = await request(page, "GET", `${api}/states`);
  expect(stateResponse.status).toBe(200);
  const states = stateResponse.body.data as { id: string; is_default: boolean; group: string }[];
  const state = states.find((entry) => entry.is_default)!;
  const issues: Issue[] = [];
  for (let batch = 0; batch < count; batch += 8) {
    const results = await Promise.all(Array.from({ length: Math.min(8, count - batch) }, (_, offset) => {
      const number = batch + offset + 1;
      return request(page, "POST", `${api}/issues`, {
        name: `E03 ${number <= 105 ? "keep" : "omit"} ${String(number).padStart(3, "0")}`,
        state_id: state.id,
        priority: number <= 60 ? "high" : number <= 105 ? "low" : "medium",
        estimate: number,
        position: number * 65536,
        start_date: number === 1 ? date(0) : null,
        target_date: number === 1 ? date(2) : null,
      });
    }));
    for (const result of results) {
      expect(result.status).toBe(201);
      issues.push(result.body.data);
    }
  }
  return { api, route, project, states, issues };
}

test("ordinary and grouped pagination, nested filters, inline dates and failed updates preserve persisted work", async ({ page }) => {
  test.setTimeout(240_000);
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await login(page);
  const f = await fixture(page, 115);
  try {
    await page.goto(`${f.route}/issues`);
    await expect(page.locator(".issue-row")).toHaveCount(100);
    await expect(page.locator(".pagination-footer")).toContainText("115");
    await page.getByRole("button", { name: "下一页", exact: true }).click();
    await expect(page.locator(".issue-row")).toHaveCount(15);
    await expect(page.getByText("E03 omit 115", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "返回首页", exact: true }).click();
    await expect(page.locator(".issue-row")).toHaveCount(100);

    await page.getByRole("button", { name: "筛选", exact: true }).click();
    await page.getByRole("combobox", { name: "分组属性", exact: true }).selectOption("priority");
    await page.getByRole("button", { name: "看板", exact: true }).click();
    const high = page.locator(".remote-issue-group").filter({ has: page.getByText("高", { exact: true }) });
    await expect(high.locator(".remote-group-card")).toHaveCount(50);
    await high.getByRole("button", { name: /^加载更多工作项/ }).click();
    await expect(high.locator(".remote-group-card")).toHaveCount(60);
    expect((await high.locator(".remote-group-card strong").allTextContents()).sort()).toEqual(f.issues.slice(0, 60).map((item) => item.name).sort());
    await page.getByRole("combobox", { name: "子分组属性", exact: true }).selectOption("state_id");
    await expect(high.locator(".remote-group-card")).toHaveCount(50);
    await high.getByRole("button", { name: /^加载更多工作项/ }).click();
    await expect(high.locator(".remote-group-card")).toHaveCount(60);
    await page.screenshot({ path: ".local/evidence/browser-e03-pagination-board.png", fullPage: true });

    await page.getByRole("combobox", { name: "子分组属性", exact: true }).selectOption("");
    await page.getByRole("combobox", { name: "分组属性", exact: true }).selectOption("state_id");
    await page.getByRole("button", { name: "列表", exact: true }).click();
    await page.getByRole("button", { name: "添加高级筛选条件", exact: true }).click();
    const group = page.locator(".advanced-filter-builder > .filter-expression-group");
    const title = group.locator(":scope > .filter-expression-row");
    await title.getByRole("combobox", { name: "筛选属性", exact: true }).selectOption("name");
    await title.getByRole("combobox", { name: "筛选运算", exact: true }).selectOption("starts_with");
    await title.getByRole("textbox", { name: "筛选值", exact: true }).fill("E03 keep");
    await group.locator(":scope > .inline-actions").getByRole("button", { name: "添加条件组", exact: true }).click();
    const alternatives = group.locator(":scope > .filter-expression-group");
    await expect(alternatives.getByRole("combobox", { name: "筛选逻辑", exact: true })).toHaveValue("or");
    const first = alternatives.locator(":scope > .filter-expression-row").first();
    await first.getByRole("combobox", { name: "筛选属性", exact: true }).selectOption("estimate");
    await first.getByRole("combobox", { name: "筛选运算", exact: true }).selectOption("lte");
    await first.getByRole("spinbutton", { name: "筛选值", exact: true }).fill("2");
    await alternatives.locator(":scope > .inline-actions").getByRole("button", { name: "添加条件", exact: true }).click();
    const last = alternatives.locator(":scope > .filter-expression-row").last();
    await last.getByRole("combobox", { name: "筛选属性", exact: true }).selectOption("estimate");
    await last.getByRole("combobox", { name: "筛选运算", exact: true }).selectOption("gte");
    await last.getByRole("spinbutton", { name: "筛选值", exact: true }).fill("104");
    await expect(page.locator(".issue-row")).toHaveCount(4);
    expect((await page.locator(".issue-row-title").allTextContents()).sort()).toEqual(["E03 keep 001", "E03 keep 002", "E03 keep 104", "E03 keep 105"]);
    await page.screenshot({ path: ".local/evidence/browser-e03-advanced-filter.png", fullPage: true });

    await page.getByRole("button", { name: "清除", exact: true }).click();
    await page.getByRole("button", { name: "表格", exact: true }).click();
    const row = page.locator(".spreadsheet-row").filter({ hasText: "E03 keep 001" });
    const dateInput = row.getByLabel("目标日期", { exact: true });
    await dateInput.fill(date(4));
    const firstPath = `${f.api}/issues/${f.issues[0].id}`;
    await expect.poll(async () => (await request(page, "GET", firstPath)).body.data.target_date).toBe(date(4));
    const before = (await request(page, "GET", firstPath)).body.data;
    let rejected = false;
    const match = `**${firstPath}`;
    await page.route(match, async (route) => {
      if (route.request().method() !== "PATCH") return route.continue();
      rejected = true;
      return route.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: { code: "forbidden", message: "E03 simulated permission rejection" } }) });
    });
    try {
      await dateInput.fill(date(6));
      await expect.poll(() => rejected).toBe(true);
      await expect(dateInput).toHaveValue(date(4));
      const persisted = (await request(page, "GET", firstPath)).body.data;
      expect(persisted.target_date).toBe(date(4));
      expect(persisted.version).toBe(before.version);
    } finally {
      await page.unroute(match);
    }
    expect((await request(page, "PATCH", firstPath, { target_date: date(5), version: before.version })).status).toBe(200);
    const conflict = page.waitForResponse((response) => response.request().method() === "PATCH" && response.url().endsWith(firstPath));
    await dateInput.fill(date(7));
    expect((await conflict).status()).toBe(409);
    await expect(dateInput).toHaveValue(date(4));
    await page.reload();
    await page.getByRole("button", { name: "表格", exact: true }).click();
    await expect(page.locator(".spreadsheet-row").filter({ hasText: "E03 keep 001" }).getByLabel("目标日期", { exact: true })).toHaveValue(date(5));
    await page.screenshot({ path: ".local/evidence/browser-e03-date-rollback.png", fullPage: true });
    expect(errors).toEqual([]);
  } finally {
    expect((await request(page, "DELETE", f.api)).status).toBe(204);
  }
});

async function createPlanning(page: Page, route: string, api: string, kind: "cycles" | "modules", name: string, start: string, end: string) {
  await page.goto(`${route}/${kind}`);
  await page.getByRole("button", { name: kind === "cycles" ? "新建 迭代周期" : "新建 功能模块", exact: true }).first().click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("名称", { exact: true }).fill(name);
  await dialog.getByLabel("开始日期", { exact: true }).fill(start);
  await dialog.getByLabel("结束日期", { exact: true }).fill(end);
  if (kind === "modules") await dialog.getByLabel("模块状态").selectOption("in-progress");
  const saved = page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith(`${api}/${kind}`), { timeout: 15_000 });
  await dialog.getByRole("button", { name: "创建", exact: true }).click();
  const response = await saved;
  expect(response.status()).toBe(201);
  await expect(dialog).toHaveCount(0);
  return (await response.json()).data as { id: string; name: string };
}

async function assign(page: Page, items: Issue[]) {
  await page.getByRole("button", { name: "添加已有工作项", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "选择工作项", exact: true }).click();
  for (const item of items) await page.getByRole("checkbox", { name: new RegExp(`${item.name}$`) }).check();
  await page.keyboard.press("Escape");
  await dialog.getByRole("button", { name: "添加", exact: true }).click();
  await expect(dialog).toHaveCount(0);
}

test("cycle transfer snapshots, module associations and links, scheduled overview and saved views persist", async ({ page }) => {
  test.setTimeout(240_000);
  await login(page);
  const f = await fixture(page, 3);
  try {
    const completed = f.states.find((state) => state.group === "completed")!;
    expect((await request(page, "PATCH", `${f.api}/issues/${f.issues[2].id}`, { state_id: completed.id, version: 1 })).status).toBe(200);
    const source = await createPlanning(page, f.route, f.api, "cycles", "E03 Source cycle", date(-3), date(0));
    const target = await createPlanning(page, f.route, f.api, "cycles", "E03 Next cycle", date(1), date(10));
    await page.goto(`${f.route}/cycles/${source.id}`);
    await page.locator(".planning-detail").getByRole("button", { name: "更多操作", exact: true }).click();
    await page.getByRole("menuitem", { name: "编辑", exact: true }).click();
    await page.getByRole("dialog").getByLabel("名称", { exact: true }).fill("E03 Updated source cycle");
    await page.getByRole("dialog").getByRole("button", { name: "保存更改", exact: true }).click();
    await expect(page.getByRole("heading", { name: "E03 Updated source cycle", exact: true })).toBeVisible();
    await assign(page, f.issues);
    await expect(page.locator(".issue-row")).toHaveCount(3);
    await page.getByRole("button", { name: "转移工作项", exact: true }).click();
    const transfer = page.getByRole("dialog");
    await transfer.getByLabel("目标周期").selectOption(target.id);
    await transfer.getByRole("button", { name: "转移未完成工作项", exact: true }).click();
    await expect(transfer).toHaveCount(0);
    await expect(page.locator(".issue-row")).toHaveCount(1);
    const remaining = (await request(page, "GET", `${f.api}/cycles/${source.id}/items`)).body.data;
    expect(remaining.map((item: Issue) => item.id)).toEqual([f.issues[2].id]);
    expect((await request(page, "GET", `${f.api}/cycles/${target.id}/items`)).body.data).toHaveLength(2);
    const progress = (await request(page, "GET", `${f.api}/cycles/${source.id}/progress`)).body.data;
    expect(progress.is_snapshot).toBe(true);
    expect(progress.total).toBe(3);
    expect(progress.completed).toBe(1);
    await page.getByRole("button", { name: "进度分析", exact: true }).click();
    await expect(page.locator(".planning-progress-view")).toBeVisible();
    await page.screenshot({ path: ".local/evidence/browser-e03-planning-progress.png", fullPage: true });

    const module = await createPlanning(page, f.route, f.api, "modules", "E03 Delivery module", date(0), date(10));
    await page.goto(`${f.route}/modules/${module.id}`);
    await assign(page, f.issues);
    await expect(page.locator(".issue-row")).toHaveCount(3);
    await page.getByRole("button", { name: "添加模块链接", exact: true }).click();
    const linkDialog = page.getByRole("dialog");
    await linkDialog.getByLabel("标题", { exact: true }).fill("E03 Design resource");
    await linkDialog.getByLabel("链接地址", { exact: true }).fill("https://example.test/design");
    await linkDialog.getByRole("button", { name: "保存", exact: true }).click();
    await expect(page.getByRole("link", { name: "E03 Design resource" })).toHaveAttribute("href", "https://example.test/design");
    await page.reload();
    await expect(page.getByRole("link", { name: "E03 Design resource" })).toBeVisible();
    await page.locator(".module-resource-bar").getByRole("button", { name: "更多操作", exact: true }).click();
    await page.getByRole("menuitem", { name: "编辑链接", exact: true }).click();
    await page.getByRole("dialog").getByLabel("标题", { exact: true }).fill("E03 Updated resource");
    await page.getByRole("dialog").getByRole("button", { name: "保存", exact: true }).click();
    await expect(page.getByRole("link", { name: "E03 Updated resource" })).toBeVisible();

    await page.goto(`${f.route}/issues/${f.issues[0].id}`);
    await page.getByLabel("迭代周期").selectOption("");
    await expect.poll(async () => (await request(page, "GET", `${f.api}/issues/${f.issues[0].id}`)).body.data.cycle_id).toBeNull();
    await page.getByRole("button", { name: "添加模块", exact: true }).click();
    await page.getByRole("checkbox", { name: module.name, exact: true }).uncheck();
    await page.keyboard.press("Escape");
    await expect.poll(async () => (await request(page, "GET", `${f.api}/issues/${f.issues[0].id}`)).body.data.module_ids).toEqual([]);
    expect((await request(page, "GET", `${f.api}/cycles/${target.id}/items`)).body.data).toHaveLength(1);
    expect((await request(page, "GET", `${f.api}/modules/${module.id}/items`)).body.data).toHaveLength(2);
    await page.goto(`${f.route}/modules`);
    await page.getByRole("button", { name: "模块甘特图", exact: true }).click();
    await expect(page.locator(".module-timeline-bar")).toContainText(module.name);
    await page.getByRole("button", { name: `编辑日期 ${module.name}`, exact: true }).click();
    await page.getByRole("dialog").getByLabel("结束日期", { exact: true }).fill(date(12));
    await page.getByRole("dialog").getByRole("button", { name: "保存更改", exact: true }).click();
    await expect.poll(async () => (await request(page, "GET", `${f.api}/modules/${module.id}`)).body.data.target_date).toBe(date(12));
    await page.screenshot({ path: ".local/evidence/browser-e03-module-gantt.png", fullPage: true });

    await page.goto(`${f.route}/views`);
    await page.getByRole("button", { name: "创建视图", exact: true }).first().click();
    const viewDialog = page.getByRole("dialog");
    await viewDialog.getByLabel("视图名称", { exact: true }).fill("E03 Saved high-priority board");
    await viewDialog.getByLabel("布局").selectOption("kanban");
    await viewDialog.getByLabel("优先级筛选").selectOption("high");
    await viewDialog.getByText("分组", { exact: true }).locator("..").locator("select").selectOption("priority");
    await viewDialog.getByLabel("子分组").selectOption("state_id");
    const createdView = page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith(`${f.api}/views`));
    await viewDialog.getByRole("button", { name: "保存视图", exact: true }).click();
    const viewResponse = await createdView;
    expect(viewResponse.status()).toBe(201);
    const view = (await viewResponse.json()).data;
    await page.goto(`${f.route}/views/${view.id}`);
    await expect(page.locator(".remote-group-card")).toHaveCount(3);
    await page.reload();
    await expect(page.locator(".remote-group-card")).toHaveCount(3);
    await page.getByRole("button", { name: "编辑视图", exact: true }).click();
    await page.getByRole("dialog").getByLabel("视图名称", { exact: true }).fill("E03 Updated saved view");
    await page.getByRole("dialog").getByRole("button", { name: "保存视图", exact: true }).click();
    await expect(page.getByRole("heading", { name: "E03 Updated saved view", exact: true })).toBeVisible();
    await page.screenshot({ path: ".local/evidence/browser-e03-saved-view.png", fullPage: true });
    await page.goto(`${f.route}/views`);
    await page.locator(".view-row").filter({ hasText: "E03 Updated saved view" }).getByRole("button", { name: "更多操作", exact: true }).click();
    await page.getByRole("menuitem", { name: "删除", exact: true }).click();
    await page.getByRole("dialog").getByRole("button", { name: "确认删除", exact: true }).click();
    await expect(page.getByText("E03 Updated saved view", { exact: true })).toHaveCount(0);
    expect((await request(page, "GET", `${f.api}/views/${view.id}`)).status).toBe(404);
    for (const [kind, resource] of [["modules", module], ["cycles", target]] as const) {
      await page.goto(`${f.route}/${kind}`);
      await page.locator(".planning-card").filter({ hasText: resource.name }).getByRole("button", { name: "更多操作", exact: true }).click();
      await page.getByRole("menuitem", { name: "删除", exact: true }).click();
      await page.getByRole("dialog").getByRole("button", { name: "确认删除", exact: true }).click();
      await expect(page.getByRole("link", { name: resource.name, exact: true })).toHaveCount(0);
      expect((await request(page, "GET", `${f.api}/${kind}/${resource.id}`)).status).toBe(404);
    }
    expect((await request(page, "GET", `${f.api}/issues`)).body.data).toHaveLength(3);
  } finally {
    expect((await request(page, "DELETE", f.api)).status).toBe(204);
  }
});

test("grouped board moves, calendar date drops and Gantt move/resize use persisted updates and roll back failures", async ({ page }) => {
  test.setTimeout(150_000);
  await login(page);
  const f = await fixture(page, 3);
  const item = f.issues[0];
  const issueAPI = `${f.api}/issues/${item.id}`;
  try {
    await page.goto(`${f.route}/issues`);
    await page.getByRole("button", { name: "筛选", exact: true }).click();
    await page.getByRole("combobox", { name: "分组属性", exact: true }).selectOption("priority");
    await page.getByRole("button", { name: "看板", exact: true }).click();
    const low = page.locator(".remote-issue-leaf").filter({ has: page.getByText("低", { exact: true }) });
    const card = page.locator(`[data-group-work-item="${item.id}"]`);
    await card.dragTo(low);
    await expect.poll(async () => (await request(page, "GET", issueAPI)).body.data.priority).toBe("low");
    await expect(low.locator(`[data-group-work-item="${item.id}"]`)).toBeVisible();
    const groupedBefore = (await request(page, "GET", issueAPI)).body.data;
    const high = page.locator(".remote-issue-leaf").filter({ has: page.getByText("高", { exact: true }) });
    let groupedRejected = false;
    const groupMatch = `**${issueAPI}`;
    await page.route(groupMatch, async (route) => {
      if (route.request().method() !== "PATCH") return route.continue();
      groupedRejected = true;
      return route.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: { code: "forbidden", message: "E03 simulated group move rejection" } }) });
    });
    try {
      await card.dragTo(high);
      await expect.poll(() => groupedRejected).toBe(true);
      await expect(low.locator(`[data-group-work-item="${item.id}"]`)).toBeVisible();
      await expect(high.locator(".remote-group-card")).toHaveCount(2);
      const groupedAfter = (await request(page, "GET", issueAPI)).body.data;
      expect([groupedAfter.priority, groupedAfter.version]).toEqual(["low", groupedBefore.version]);
    } finally {
      await page.unroute(groupMatch);
    }
    await page.screenshot({ path: ".local/evidence/browser-e03-board-move.png", fullPage: true });

    await page.getByRole("button", { name: "日历", exact: true }).click();
    const calendarTarget = page.locator(`[data-calendar-date="${date(4)}"]`);
    await page.locator(".calendar-issue").filter({ hasText: item.name }).dragTo(calendarTarget);
    await expect(calendarTarget.getByRole("link", { name: item.name })).toBeVisible();
    await expect.poll(async () => {
      const current = (await request(page, "GET", issueAPI)).body.data;
      return [current.start_date, current.target_date];
    }).toEqual([date(2), date(4)]);
    await page.screenshot({ path: ".local/evidence/browser-e03-calendar-drop.png", fullPage: true });

    await page.getByRole("button", { name: "时间线", exact: true }).click();
    const zoom = page.getByRole("combobox", { name: "甘特图缩放", exact: true });
    for (const scale of ["day", "week", "month", "quarter", "week"]) {
      await zoom.selectOption(scale);
      await expect(zoom).toHaveValue(scale);
      await expect(page.locator(`[data-timeline-item="${item.id}"] .timeline-bar`)).toBeVisible();
    }
    const track = page.locator(`[data-timeline-item="${item.id}"]`);
    const monthStart = new Date(date(0));
    monthStart.setUTCDate(1);
    const index = (value: string) => Math.round((new Date(value).getTime() - monthStart.getTime()) / 86400000);
    const bar = track.locator(".timeline-bar");
    await bar.dragTo(track, { sourcePosition: { x: 20, y: 12 }, targetPosition: { x: (index(date(2)) + 3) * 16 + 4, y: 15 } });
    await expect.poll(async () => {
      const current = (await request(page, "GET", issueAPI)).body.data;
      return [current.start_date, current.target_date];
    }).toEqual([date(4), date(6)]);
    await track.getByRole("button", { name: `调整开始日期 ${item.name}`, exact: true }).dragTo(track, { targetPosition: { x: index(date(5)) * 16 + 4, y: 15 } });
    await expect.poll(async () => (await request(page, "GET", issueAPI)).body.data.start_date).toBe(date(5));
    await track.getByRole("button", { name: `调整结束日期 ${item.name}`, exact: true }).dragTo(track, { targetPosition: { x: index(date(8)) * 16 + 4, y: 15 } });
    await expect.poll(async () => (await request(page, "GET", issueAPI)).body.data.target_date).toBe(date(8));
    const before = (await request(page, "GET", issueAPI)).body.data;
    let rejected = false;
    const match = `**${issueAPI}`;
    await page.route(match, async (route) => {
      if (route.request().method() !== "PATCH") return route.continue();
      rejected = true;
      return route.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: { code: "forbidden", message: "E03 simulated drag rejection" } }) });
    });
    try {
      await bar.dragTo(track, { sourcePosition: { x: 20, y: 12 }, targetPosition: { x: (index(date(5)) + 3) * 16 + 4, y: 15 } });
      await expect.poll(() => rejected).toBe(true);
      await expect(bar).toHaveAttribute("title", `${item.name}: ${date(5)} → ${date(8)}`);
      const after = (await request(page, "GET", issueAPI)).body.data;
      expect([after.start_date, after.target_date, after.version]).toEqual([before.start_date, before.target_date, before.version]);
    } finally {
      await page.unroute(match);
    }
    await page.screenshot({ path: ".local/evidence/browser-e03-gantt-drag-resize.png", fullPage: true });
    await page.reload();
    await page.getByRole("button", { name: "时间线", exact: true }).click();
    await expect(page.locator(`[data-timeline-item="${item.id}"] .timeline-bar`)).toHaveAttribute("title", `${item.name}: ${date(5)} → ${date(8)}`);
  } finally {
    expect((await request(page, "DELETE", f.api)).status).toBe(204);
  }
});
