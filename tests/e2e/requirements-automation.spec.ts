import { expect, test, type BrowserContext, type Page } from "@playwright/test";
import { login, password, request } from "./helpers";

type RecordID = { id: string; version: number; name?: string };
type Run = {
  id: string;
  status: string;
  attempts: number;
  failure: string;
  input: Record<string, unknown>;
  output: Record<string, unknown>;
  before_state: unknown[];
  after_state: { item_id: string }[];
  undo_conflicts: unknown[];
};

async function data<T = any>(
  page: Page,
  method: string,
  path: string,
  body?: unknown,
  status = 200,
): Promise<T> {
  const response = await request(page, method, path, body);
  expect(
    response.status,
    `${method} ${path}: ${response.status === status ? "" : JSON.stringify(response.body)}`,
  ).toBe(status);
  return response.body?.data as T;
}

function date(offset: number) {
  const value = new Date();
  value.setUTCDate(value.getUTCDate() + offset);
  return value.toISOString().slice(0, 10);
}

async function closeDialog(page: Page) {
  if (await page.getByRole("dialog").count())
    await page
      .getByRole("dialog")
      .getByRole("button", { name: "关闭", exact: true })
      .click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
}

async function startRun(
  page: Page,
  api: string,
  capability: string,
  taskID?: string,
): Promise<Run> {
  await closeDialog(page);
  await page.getByRole("tab", { name: "运行与能力", exact: true }).click();
  await page
    .locator(".automation-capability")
    .filter({
      has: page.getByRole("heading", { name: capability, exact: true }),
    })
    .getByRole("button", { name: "开始运行", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  if (capability === "智能排期") {
    await dialog.getByLabel(/^排期窗口开始/).fill(date(0));
    await dialog.getByLabel(/^排期窗口结束/).fill(date(14));
    if (taskID) await dialog.getByLabel(/^选定工作项 ID/).fill(taskID);
  }
  const created = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      response.url().endsWith(`${api}/automation/runs`),
  );
  await dialog.getByRole("button", { name: "提交运行", exact: true }).click();
  const response = await created;
  expect(response.status(), await response.text()).toBe(202);
  const run = (await response.json()).data as Run;
  await expect(page.getByRole("dialog")).toContainText(run.id);
  return run;
}

async function terminalRun(page: Page, api: string, id: string): Promise<Run> {
  let result: Run | undefined;
  await expect
    .poll(
      async () => {
        result = await data<Run>(page, "GET", `${api}/automation/runs/${id}`);
        return result.status;
      },
      { timeout: 55_000, intervals: [500, 1000, 1500] },
    )
    .not.toMatch(/^(queued|running)$/);
  return result!;
}

async function settleRuns(page: Page, api: string) {
  let stable = 0;
  await expect
    .poll(
      async () => {
        const runs = await data<Run[]>(page, "GET", `${api}/automation/runs`);
        stable = runs.some((run) => ["queued", "running"].includes(run.status))
          ? 0
          : stable + 1;
        return stable;
      },
      { timeout: 45_000, intervals: [500] },
    )
    .toBeGreaterThanOrEqual(3);
}

async function setCapabilities(
  page: Page,
  api: string,
  choices: Record<string, boolean>,
) {
  await closeDialog(page);
  await page.getByRole("tab", { name: "授权策略", exact: true }).click();
  for (const [name, enabled] of Object.entries(choices))
    await page.getByRole("checkbox", { name, exact: true }).setChecked(enabled);
  const saved = page.waitForResponse(
    (response) =>
      response.request().method() === "PUT" &&
      response.url().endsWith(`${api}/automation/policy`),
  );
  await page.getByRole("button", { name: "保存策略", exact: true }).click();
  expect((await saved).status()).toBe(200);
  await settleRuns(page, api);
}

test.describe
  .serial("requirements automation with the real durable worker", () => {
  let admin: Page;
  let api = "";
  let route = "";
  let workspaceAPI = "";
  let projectID = "";
  let actorID = "";
  let task: RecordID;
  let prd: RecordID;
  let memberContext: BrowserContext | undefined;
  let memberWorkspaceMembership = "";
  let memberProjectMembership = "";
  let member: Page | undefined;
  const browserErrors: string[] = [];

  test.beforeAll(async ({ browser }) => {
    test.setTimeout(90_000);
    admin = await browser.newPage({
      locale: "zh-CN",
      viewport: { width: 1440, height: 960 },
    });
    admin.on("pageerror", (error) => browserErrors.push(error.message));
    await login(admin);
    const workspaces = await data<{ id: string; slug: string }[]>(
      admin,
      "GET",
      "/api/v1/workspaces",
    );
    const workspace = workspaces.find((entry) => entry.slug === "studio");
    expect(workspace).toBeDefined();
    workspaceAPI = `/api/v1/workspaces/${workspace!.id}`;
    actorID = (await data<{ id: string }>(admin, "GET", "/api/v1/auth/me")).id;
    const project = await data<RecordID>(
      admin,
      "POST",
      `${workspaceAPI}/projects`,
      {
        name: "自动化验收项目",
        identifier: `A${Date.now().toString(36).slice(-7).toUpperCase()}`,
        network: "private",
      },
      201,
    );
    projectID = project.id;
    api = `${workspaceAPI}/projects/${project.id}`;
    route = `/w/${workspace!.slug}/projects/${project.id}/automation`;
    task = await data<RecordID>(
      admin,
      "POST",
      `${api}/issues`,
      {
        name: "校验自动排期约束",
        priority: "high",
        estimated_minutes: 90,
        remaining_minutes: 90,
        required_skills: ["typescript"],
      },
      201,
    );
    prd = await data<RecordID>(
      admin,
      "POST",
      `${api}/pages`,
      {
        name: "客户门户需求依据",
        content_html:
          "<h1>客户门户</h1><p>作为客户，我希望查看自己的订单状态，以便跟踪交付。</p>",
        content_json: {
          type: "doc",
          content: [
            {
              type: "heading",
              attrs: { level: 1 },
              content: [{ type: "text", text: "客户门户" }],
            },
            {
              type: "paragraph",
              content: [
                {
                  type: "text",
                  text: "作为客户，我希望查看自己的订单状态，以便跟踪交付。",
                },
              ],
            },
          ],
        },
      },
      201,
    );
    await admin.goto(route);
    await expect(
      admin.getByRole("heading", { name: "项目自动化", exact: true }),
    ).toBeVisible();
  });

  test.afterAll(async () => {
    await memberContext?.close();
    if (admin && !admin.isClosed()) {
      if (memberWorkspaceMembership)
        await request(
          admin,
          "DELETE",
          `${workspaceAPI}/members/${memberWorkspaceMembership}`,
        );
      if (api) await request(admin, "DELETE", api);
      await admin.close();
    }
  });

  test("real policy forms persist permission, deadline, and budget edits; PRD inputs and missing GitHub evidence stay explicit", async () => {
    await expect(admin.locator(".automation-capability")).toHaveCount(6);
    await expect(
      admin
        .locator(".automation-capability")
        .filter({ hasText: "交付预测" })
        .getByRole("button", { name: "开始运行", exact: true }),
    ).toBeDisabled();
    await admin.getByRole("tab", { name: "授权策略", exact: true }).click();
    await expect(admin.locator(".automation-policy")).toBeVisible();
    await admin.getByRole("checkbox", { name: "启用", exact: true }).check();
    await admin
      .getByRole("checkbox", { name: "风险预警", exact: true })
      .uncheck();
    await admin
      .getByRole("checkbox", { name: "效率改善", exact: true })
      .uncheck();
    await admin.getByLabel(/^单批变更上限/).fill("12");
    await admin.getByLabel(/^调用预算/).fill("40");
    await admin.getByLabel(/^最短运行间隔/).fill("0");
    await admin.getByLabel(/^连续自动调整轮次上限/).fill("3");
    await admin.getByLabel(/^硬截止日/).fill(date(90));
    const saved = admin.waitForResponse(
      (response) =>
        response.request().method() === "PUT" &&
        response.url().endsWith(`${api}/automation/policy`),
    );
    await admin.getByRole("button", { name: "保存策略", exact: true }).click();
    expect((await saved).status()).toBe(200);
    await expect
      .poll(
        async () =>
          (await data<any>(admin, "GET", `${api}/automation/policy`)).enabled,
      )
      .toBe(true);
    const policy = await data<any>(admin, "GET", `${api}/automation/policy`);
    expect(policy).toMatchObject({
      max_changes: 12,
      budget_calls: 40,
      min_interval_seconds: 0,
      max_rounds: 3,
      hard_deadline: date(90),
    });
    expect(policy.allowed_kinds).not.toContain("risk");
    expect(policy.allowed_kinds).not.toContain("efficiency");
    await settleRuns(admin, api);
    await admin.reload();
    await expect(
      admin.getByRole("checkbox", { name: "启用", exact: true }),
    ).toBeChecked();
    await expect(admin.getByLabel(/^调用预算/)).toHaveValue("40");
    await admin.getByRole("tab", { name: "运行与能力", exact: true }).click();
    await admin
      .locator(".automation-capability")
      .filter({
        has: admin.getByRole("heading", {
          name: "需求拆解与估算",
          exact: true,
        }),
      })
      .getByRole("button", { name: "开始运行", exact: true })
      .click();
    const dialog = admin.getByRole("dialog");
    await dialog.getByLabel(/^PRD 文档/).selectOption(prd.id);
    await expect(dialog.getByLabel(/^来源版本/)).toHaveValue(
      String(prd.version),
    );
    await dialog.getByLabel(/^需求来源/).selectOption("text");
    await dialog.locator('input[type="file"]').setInputFiles({
      name: "customer-portal.md",
      mimeType: "text/markdown",
      buffer: Buffer.from("# 客户门户\n客户可以查看自己的订单状态。"),
    });
    await expect(dialog.getByLabel(/^PRD 文本/)).toHaveValue(
      "# 客户门户\n客户可以查看自己的订单状态。",
    );
    await expect(dialog.getByLabel(/^来源标识/)).toHaveValue(
      "customer-portal.md",
    );
    await closeDialog(admin);
    await admin
      .getByRole("tab", { name: "质量与 GitHub", exact: true })
      .click();
    const github = await data<any>(admin, "GET", `${api}/github`);
    expect(github.configured).toBe(false);
    await expect(
      admin.getByText(
        "实例尚未配置 GitHub App。管理员完成实例配置后，项目管理员可授权指定仓库。",
        { exact: true },
      ),
    ).toBeVisible();
    await expect(
      admin.getByRole("button", { name: "连接 GitHub App", exact: true }),
    ).toBeDisabled();
    await expect(
      admin.getByText("尚无符合条件的质量报告", { exact: true }),
    ).toBeVisible();
    await expect(
      admin.locator(".automation-table .automation-status-positive"),
    ).toHaveCount(0);
    expect(browserErrors).toEqual([]);
  });

  test("worker forecasts, constrained schedules, retry conflicts, guarded undo, and risk and improvement evidence persist", async () => {
    test.setTimeout(300_000);
    const forecast = await startRun(admin, api, "交付预测");
    const forecastResult = await terminalRun(admin, api, forecast.id);
    expect(forecastResult.status, forecastResult.failure).toBe("completed");
    expect(forecastResult.input).toHaveProperty("project_revision");
    await expect(
      admin.getByRole("dialog").getByText("固定输入与来源", { exact: true }),
    ).toBeVisible();
    await closeDialog(admin);
    await admin.getByRole("tab", { name: "交付预测", exact: true }).click();
    await expect(
      admin.getByText("历史数据不足", { exact: true }).first(),
    ).toBeVisible();
    const forecasts = await data<any[]>(
      admin,
      "GET",
      `${api}/automation/forecasts`,
    );
    expect(forecasts[0]).toMatchObject({
      status: "insufficient_data",
      p50: null,
      p80: null,
      sample_count: 0,
    });

    const blocked = await startRun(admin, api, "智能排期", task.id);
    let blockedResult = await terminalRun(admin, api, blocked.id);
    expect(blockedResult.status, JSON.stringify(blockedResult.output)).toBe(
      "blocked",
    );
    await expect(
      admin
        .getByRole("dialog")
        .getByRole("button", { name: "按固定输入重试", exact: true }),
    ).toBeVisible();
    const retried = admin.waitForResponse(
      (response) =>
        response.request().method() === "POST" &&
        response.url().endsWith(`${api}/automation/runs/${blocked.id}/retry`),
    );
    await admin
      .getByRole("dialog")
      .getByRole("button", { name: "按固定输入重试", exact: true })
      .click();
    expect((await retried).status()).toBe(200);
    blockedResult = await terminalRun(admin, api, blocked.id);
    expect(blockedResult.status).toBe("blocked");
    expect(blockedResult.attempts).toBeGreaterThanOrEqual(2);

    const resources = await data<any>(admin, "GET", `${api}/resources`);
    const actor = resources.members.find(
      (entry: any) => entry.member_id === actorID,
    );
    await data(admin, "PUT", `${api}/resources/members/${actorID}`, {
      version: actor.version,
      skills: ["typescript"],
      weekday_minutes: [480, 480, 480, 480, 480, 480, 480],
      project_minutes_per_day: 480,
      exceptions: {},
    });
    await expect(
      admin
        .getByRole("dialog")
        .getByRole("button", { name: "按固定输入重试", exact: true }),
    ).toBeEnabled();
    const staleRetry = admin.waitForResponse(
      (response) =>
        response.request().method() === "POST" &&
        response.url().endsWith(`${api}/automation/runs/${blocked.id}/retry`),
    );
    await admin
      .getByRole("dialog")
      .getByRole("button", { name: "按固定输入重试", exact: true })
      .click();
    expect((await staleRetry).status()).toBe(409);
    await expect(admin.getByRole("dialog").getByRole("alert")).toBeVisible();

    const schedule = await startRun(admin, api, "智能排期", task.id);
    const scheduled = await terminalRun(admin, api, schedule.id);
    expect(scheduled.status, JSON.stringify(scheduled.output)).toBe("applied");
    expect(scheduled.after_state.map((entry) => entry.item_id)).toContain(
      task.id,
    );
    await expect(
      admin.getByRole("dialog").locator(".automation-diff"),
    ).toBeVisible();
    let issue = await data<any>(admin, "GET", `${api}/issues/${task.id}`);
    expect(issue.start_date).toBeTruthy();
    expect(issue.assignee_ids).toContain(actorID);
    await admin
      .getByRole("dialog")
      .getByRole("button", { name: "撤销变更批次", exact: true })
      .click();
    const undo = admin.waitForResponse(
      (response) =>
        response.request().method() === "POST" &&
        response.url().endsWith(`${api}/automation/runs/${schedule.id}/undo`),
    );
    await admin
      .getByRole("dialog")
      .getByRole("button", { name: "检查并撤销", exact: true })
      .click();
    expect((await undo).status()).toBe(200);
    expect(
      (await data<Run>(admin, "GET", `${api}/automation/runs/${schedule.id}`))
        .status,
    ).toBe("undone");
    issue = await data<any>(admin, "GET", `${api}/issues/${task.id}`);
    expect(issue.start_date).toBeNull();
    expect(issue.assignee_ids).toEqual([]);

    const secondSchedule = await startRun(admin, api, "智能排期", task.id);
    const secondApplied = await terminalRun(admin, api, secondSchedule.id);
    expect(secondApplied.status, secondApplied.failure).toBe("applied");
    issue = await data<any>(admin, "GET", `${api}/issues/${task.id}`);
    await data(admin, "PATCH", `${api}/issues/${task.id}`, {
      name: "保留人工修改后的排期任务",
      version: issue.version,
    });
    await expect(
      admin
        .getByRole("dialog")
        .getByRole("button", { name: "撤销变更批次", exact: true }),
    ).toBeVisible();
    await admin
      .getByRole("dialog")
      .getByRole("button", { name: "撤销变更批次", exact: true })
      .click();
    const guardedUndo = admin.waitForResponse(
      (response) =>
        response.request().method() === "POST" &&
        response
          .url()
          .endsWith(`${api}/automation/runs/${secondSchedule.id}/undo`),
    );
    await admin
      .getByRole("dialog")
      .getByRole("button", { name: "检查并撤销", exact: true })
      .click();
    expect((await guardedUndo).status()).toBe(409);
    await expect(
      admin
        .getByRole("dialog")
        .getByText("撤销冲突（保留后续修改）", { exact: true }),
    ).toBeVisible();
    expect(
      (await data<any>(admin, "GET", `${api}/issues/${task.id}`)).name,
    ).toBe("保留人工修改后的排期任务");

    await setCapabilities(admin, api, { 智能排期: false, 风险预警: true });

    issue = await data<any>(admin, "GET", `${api}/issues/${task.id}`);
    await data(admin, "PATCH", `${api}/issues/${task.id}`, {
      start_date: date(-2),
      target_date: date(-1),
      version: issue.version,
    });
    await settleRuns(admin, api);
    const risk = await startRun(admin, api, "风险预警");
    const riskResult = await terminalRun(admin, api, risk.id);
    expect(["completed", "applied", "partial"], riskResult.failure).toContain(
      riskResult.status,
    );
    await closeDialog(admin);
    await admin.getByRole("tab", { name: "风险应对", exact: true }).click();
    const riskCard = admin.locator("article.automation-card").filter({
      has: admin.getByRole("heading", {
        name: "执行计划已逾期",
        exact: true,
      }),
    });
    await expect(riskCard).toHaveCount(1);
    await riskCard
      .getByRole("button", { name: "确认风险", exact: true })
      .click();
    await expect(
      riskCard.locator(".automation-status").filter({ hasText: "已确认" }),
    ).toBeVisible();
    await expect(
      riskCard.getByRole("link", { name: "打开应对任务", exact: true }),
    ).toBeVisible();

    await setCapabilities(admin, api, { 风险预警: false, 效率改善: true });
    const efficiency = await startRun(admin, api, "效率改善");
    const efficiencyResult = await terminalRun(admin, api, efficiency.id);
    expect(
      ["completed", "applied", "partial"],
      efficiencyResult.failure,
    ).toContain(efficiencyResult.status);
    await closeDialog(admin);
    await admin.getByRole("tab", { name: "效率改善", exact: true }).click();
    await expect(
      admin.getByRole("heading", { name: "行动前基线", exact: true }),
    ).toBeVisible();
    await admin
      .getByRole("button", { name: "记录当前观测", exact: true })
      .first()
      .click();
    await expect(
      admin
        .locator(".automation-status")
        .filter({ hasText: "证据不足" })
        .first(),
    ).toBeVisible();
    const improvements = await data<any[]>(
      admin,
      "GET",
      `${api}/automation/improvements`,
    );
    expect(improvements[0].status).toBe("insufficient_evidence");

    await admin.getByRole("tab", { name: "运行与能力", exact: true }).click();
    await expect
      .poll(() => admin.locator(".automation-table tbody tr").count())
      .toBeGreaterThanOrEqual(6);
    await expect(admin.locator(".toast")).toHaveCount(0);
    await admin
      .locator(".content-area")
      .evaluate((element) => element.scrollTo(0, 0));
    await expect(admin.locator(".content-area")).toHaveJSProperty("scrollTop", 0);
    await expect(
      admin.getByRole("heading", { name: "项目自动化", exact: true }),
    ).toBeInViewport();
    await admin.mouse.move(0, 0);
    await admin.evaluate(() => {
      document.documentElement.dataset.theme = "light";
      document.documentElement.style.colorScheme = "light";
    });
    await expect(
      admin.locator(".automation-page .button-secondary").first(),
    ).toHaveCSS("background-color", "rgb(255, 255, 255)");
    await admin.screenshot({
      path: "docs/screenshots/requirements-automation-runs-light.png",
      animations: "disabled",
    });
    await admin.evaluate(() => {
      document.documentElement.dataset.theme = "dark";
      document.documentElement.style.colorScheme = "dark";
    });
    await expect(
      admin.locator(".automation-page .button-secondary").first(),
    ).toHaveCSS("background-color", "rgb(33, 37, 45)");
    await admin.screenshot({
      path: "docs/screenshots/requirements-automation-runs-dark.png",
      animations: "disabled",
    });
    await admin.evaluate(() => {
      document.documentElement.dataset.theme = "light";
      document.documentElement.style.colorScheme = "light";
    });
    expect(browserErrors).toEqual([]);
  });

  test("Member sees permitted capabilities without private policy controls; disable and revocation update live UI", async ({
    browser,
  }) => {
    test.setTimeout(100_000);
    memberContext = await browser.newContext({
      locale: "zh-CN",
      viewport: { width: 1440, height: 960 },
    });
    member = await memberContext.newPage();
    member.on("pageerror", (error) => browserErrors.push(error.message));
    await member.goto("/login");
    const email = `automation-member-${Date.now()}@myjira.local`;
    const account = await data<any>(
      member,
      "POST",
      "/api/v1/auth/register",
      { email, password, display_name: "自动化验收成员" },
      201,
    );
    memberWorkspaceMembership = (
      await data<any>(
        admin,
        "POST",
        `${workspaceAPI}/members`,
        { email, role: 15 },
        201,
      )
    ).id;
    memberProjectMembership = (
      await data<any>(
        admin,
        "POST",
        `${api}/members`,
        { user_id: account.user.id, role: 15 },
        201,
      )
    ).id;
    await member.goto(route);
    await expect(
      member.getByRole("tab", { name: "运行与能力", exact: true }),
    ).toBeVisible();
    await expect(
      member.getByRole("tab", { name: "授权策略", exact: true }),
    ).toHaveCount(0);
    expect(
      (await request(member, "GET", `${api}/automation/policy`)).status,
    ).toBe(403);
    await expect(
      member
        .locator(".automation-capability")
        .filter({
          has: member.getByRole("heading", { name: "交付预测", exact: true }),
        })
        .getByRole("button", { name: "开始运行", exact: true }),
    ).toBeEnabled();
    await member
      .getByRole("tab", { name: "质量与 GitHub", exact: true })
      .click();
    await expect(
      member.getByRole("button", { name: "连接 GitHub App", exact: true }),
    ).toHaveCount(0);
    await member.getByRole("tab", { name: "运行与能力", exact: true }).click();

    await admin.getByRole("tab", { name: "授权策略", exact: true }).click();
    await admin.getByRole("checkbox", { name: "启用", exact: true }).uncheck();
    await admin.getByRole("button", { name: "保存策略", exact: true }).click();
    await expect
      .poll(
        async () =>
          (await data<any>(admin, "GET", `${api}/automation/capabilities`))
            .enabled,
      )
      .toBe(false);
    await expect(
      member
        .locator(".automation-capability")
        .filter({
          has: member.getByRole("heading", { name: "交付预测", exact: true }),
        })
        .getByRole("button", { name: "开始运行", exact: true }),
    ).toBeDisabled();
    await expect(
      admin.getByRole("checkbox", { name: "启用", exact: true }),
    ).not.toBeChecked();
    expect(
      (
        await request(member, "POST", `${api}/automation/runs`, {
          kind: "forecast",
          idempotency_key: `disabled-${Date.now()}`,
        })
      ).status,
    ).toBe(409);
    await data(
      admin,
      "DELETE",
      `${api}/members/${memberProjectMembership}`,
      undefined,
      204,
    );
    await expect(
      member.getByRole("heading", { name: "访问权限已变化", exact: true }),
    ).toBeVisible();
    await expect(member.locator(".automation-table")).toHaveCount(0);
    expect([403, 404]).toContain(
      (await request(member, "GET", `${api}/automation/runs`)).status,
    );
    expect(browserErrors).toEqual([]);
    expect(projectID).toBeTruthy();
  });
});
