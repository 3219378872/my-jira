import { test, expect, type Page } from "@playwright/test";
import { randomUUID } from "node:crypto";
import { readFile } from "node:fs/promises";
import { request } from "./helpers";

const testPassword = "MyJira-Local-2026!";
async function signIn(page: Page, email = "demo@myjira.local") {
  await page.goto("/login");
  await page.locator("input[type=email]").fill(email);
  await page.locator("input[type=password]").fill(testPassword);
  await page.locator("button[type=submit]").click();
  await page.waitForURL(/\/w\//);
}
async function data(page: Page, method: string, path: string, body?: unknown) {
  const res = await request(page, method, path, body);
  expect(
    res.status,
    `${method} ${path}: ${JSON.stringify(res.body)}`,
  ).toBeGreaterThanOrEqual(200);
  expect(
    res.status,
    `${method} ${path}: ${JSON.stringify(res.body)}`,
  ).toBeLessThan(300);
  return res.body?.data;
}
async function setup(page: Page) {
  const workspaces = await data(page, "GET", "/api/v1/workspaces");
  const w = workspaces.find((x: { slug: string }) => x.slug === "studio");
  const fixtureKey = randomUUID().replaceAll("-", "").slice(0, 9);
  const project = await data(
    page,
    "POST",
    `/api/v1/workspaces/${w.id}/projects`,
    {
      name: `四视图浏览器验收 ${fixtureKey}`,
      identifier: `V${fixtureKey}`,
      network: "private",
    },
  );
  const p = `/api/v1/workspaces/${w.id}/projects/${project.id}`;
  const route = `/w/studio/projects/${project.id}/requirements`;
  const states = await data(page, "GET", `${p}/states`);
  const state = states.find(
    (x: { group?: string; group_name?: string }) =>
      (x.group ?? x.group_name) === "unstarted",
  );
  const completed = states.find(
    (x: { group?: string; group_name?: string }) =>
      (x.group ?? x.group_name) === "completed",
  );
  const admin = await data(page, "GET", "/api/v1/auth/me");
  const epic = await data(page, "POST", `${p}/issues`, {
    name: "协作需求Epic",
    requirement_type: "epic",
    state_id: state.id,
  });
  const activity = await data(page, "POST", `${p}/requirements/activities`, {
    name: "安排工作",
    epic_id: epic.id,
  });
  const s1 = await data(page, "POST", `${p}/cycles`, {
    name: "S1 · 组织工作",
    start_date: "2026-09-14",
    end_date: "2026-09-25",
  });
  const s2 = await data(page, "POST", `${p}/cycles`, {
    name: "S2 · 执行协作",
    start_date: "2026-09-28",
    end_date: "2026-10-09",
  });
  await data(page, "PUT", `${p}/resources/members/${admin.id}`, {
    version: 0,
    skills: ["后端"],
    weekday_minutes: [0, 480, 480, 480, 480, 480, 0],
    project_minutes_per_day: 480,
    exceptions: { "2026-09-16": 0 },
  });
  return { p, route, project, admin, epic, activity, s1, s2, state, completed };
}

test("story commitment, task schedule and completed load synchronize across two browser sessions", async ({
  page,
  browser,
}) => {
  await signIn(page);
  const f = await setup(page);
  const second = await browser.newContext({
    viewport: { width: 1440, height: 960 },
    locale: "zh-CN",
  });
  const peer = await second.newPage();
  try {
    await signIn(peer);
    await Promise.all([
      page.goto(`${f.route}/story-map`),
      peer.goto(`${f.route}/story-map`),
    ]);
    await expect(page.getByTestId("requirements-workspace")).toBeVisible();
    await page.getByRole("button", { name: "新建需求", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await dialog
      .getByRole("textbox", { name: "标题", exact: true })
      .fill("浏览器创建并联动的故事");
    await dialog
      .getByRole("combobox", { name: "父级", exact: true })
      .selectOption(f.epic.id);
    await dialog
      .getByRole("combobox", { name: "用户活动", exact: true })
      .selectOption(f.activity.id);
    await dialog
      .getByRole("combobox", { name: "承诺 Sprint", exact: true })
      .selectOption(f.s1.id);
    await dialog
      .getByLabel("作为（业务角色）", { exact: true })
      .fill("团队成员");
    await dialog
      .getByLabel("我希望（目标）", { exact: true })
      .fill("明确负责人并协作");
    await dialog
      .getByLabel("以便（业务价值）", { exact: true })
      .fill("协调交付");
    await dialog.getByRole("button", { name: "保存需求", exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect(
      peer.getByRole("button", { name: "浏览器创建并联动的故事", exact: true }),
    ).toBeVisible();
    const snapshot = await data(page, "GET", `${f.p}/requirements/snapshot`);
    const story = snapshot.items.find(
      (x: { name: string }) => x.name === "浏览器创建并联动的故事",
    );
    const task = await data(page, "POST", `${f.p}/issues`, {
      name: "浏览器执行任务",
      requirement_type: "task",
      parent_id: story.id,
      state_id: f.state.id,
      estimated_minutes: 960,
      remaining_minutes: 960,
      assignee_ids: [f.admin.id],
      required_skills: ["后端"],
      start_date: "2026-09-14",
      target_date: "2026-09-17",
    });
    expect(task.cycle_id).toBe(f.s1.id);
    await expect(page.getByTestId(`story-card-${story.id}`)).toContainText(
      /1\s*子任务/,
    );
    await page
      .getByTestId(`story-card-${story.id}`)
      .getByRole("button", { name: story.name, exact: true })
      .click();
    await dialog
      .getByRole("combobox", { name: "承诺 Sprint", exact: true })
      .selectOption(f.s2.id);
    await dialog.getByRole("button", { name: "保存需求", exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect(
      peer
        .getByTestId(`story-cell-${f.activity.id}-${f.s2.id}`)
        .getByTestId(`story-card-${story.id}`),
    ).toBeVisible();
    const unchanged = await data(page, "GET", `${f.p}/issues/${task.id}`);
    expect(unchanged.cycle_id).toBe(f.s1.id);
    expect(unchanged.start_date.slice(0, 10)).toBe("2026-09-14");
    await page.getByRole("link", { name: "层级甘特", exact: true }).click();
    await expect(page.getByTestId(`gantt-row-${task.id}`)).toBeVisible();
    await page
      .getByLabel(`${task.name} 开始日期`, { exact: true })
      .fill("2026-09-15");
    await page.getByLabel(`${task.name} 开始日期`, { exact: true }).blur();
    await expect
      .poll(async () =>
        (await data(page, "GET", `${f.p}/issues/${task.id}`)).start_date.slice(
          0,
          10,
        ),
      )
      .toBe("2026-09-15");
    await peer.goto(`${f.route}/members`);
    await expect(peer.getByTestId(`member-lane-${f.admin.id}`)).toContainText(
      task.name,
    );
    await page
      .getByTestId(`gantt-row-${task.id}`)
      .locator(".req-gantt-name")
      .click();
    await dialog
      .getByRole("combobox", { name: "状态", exact: true })
      .selectOption(f.completed.id);
    await dialog.getByRole("button", { name: "保存需求", exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect
      .poll(async () => {
        const load = await data(
          peer,
          "GET",
          `${f.p}/resources/load?start_date=2026-09-14&end_date=2026-09-18`,
        );
        return load.members
          .find((x: { member_id: string }) => x.member_id === f.admin.id)
          .days.reduce(
            (sum: number, d: { allocated_minutes: number }) =>
              sum + d.allocated_minutes,
            0,
          );
      })
      .toBe(0);
    await expect(
      peer.getByTestId(`member-lane-${f.admin.id}`),
    ).not.toContainText("960");
  } finally {
    await second.close();
  }
});

test("structured UML edits, safe SVG export and current source privacy are enforced in the browser", async ({
  page,
  browser,
}) => {
  await signIn(page);
  const f = await setup(page);
  const story = await data(page, "POST", `${f.p}/issues`, {
    name: "场景业务故事",
    requirement_type: "story",
    parent_id: f.epic.id,
    state_id: f.state.id,
    activity_id: f.activity.id,
  });
  const source = await data(page, "POST", `${f.p}/pages`, {
    name: "场景授权来源",
  });
  const actor = randomUUID(),
    system = randomUUID(),
    step = randomUUID();
  const scenario = await data(page, "POST", `${f.p}/scenarios`, {
    story_id: story.id,
    name: "中文调用与返回",
    system_boundary: "协作系统",
    source_page_ids: [source.id],
    participants: [
      { id: actor, name: "业务用户", kind: "actor" },
      { id: system, name: "业务系统", kind: "system" },
    ],
    steps: [
      {
        id: step,
        kind: "call",
        from_id: actor,
        to_id: system,
        message: "提交需求 <safe & 中文>",
      },
    ],
  });
  await page.goto(`${f.route}/scenarios`);
  await page.getByRole("button", { name: "中文调用与返回" }).click();
  await page.getByRole("button", { name: "场景时序图", exact: true }).click();
  await expect(page.locator(".req-diagram-viewport svg")).toContainText(
    "提交需求 <safe & 中文>",
  );
  await page
    .getByRole("button", { name: "编辑结构化场景", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  await dialog
    .getByLabel("场景名称", { exact: true })
    .fill("已编辑的中文业务场景");
  await dialog
    .getByRole("button", { name: "保存场景并更新图", exact: true })
    .click();
  await expect(dialog).toBeHidden();
  await expect(page.locator(".req-scenario-summary")).toContainText(
    "已编辑的中文业务场景",
  );
  const downloadPromise = page.waitForEvent("download");
  await page.getByRole("button", { name: "导出 SVG", exact: true }).click();
  const download = await downloadPromise;
  const exported = await readFile((await download.path())!, "utf8");
  expect(exported).toContain("&lt;safe &amp; 中文&gt;");
  expect(exported).not.toMatch(
    /<script|javascript:|<foreignObject|(?:href|src)=["']https?:\/\//i,
  );
  const members = await data(
    page,
    "GET",
    `${f.p.split("/projects/")[0]}/members`,
  );
  const member = members.find(
    (x: { email: string }) => x.email === "member@requirements.test",
  );
  await data(page, "POST", `${f.p}/members`, {
    user_id: member.user_id,
    role: 15,
  });
  const second = await browser.newContext({
    viewport: { width: 1440, height: 960 },
    locale: "zh-CN",
  });
  const peer = await second.newPage();
  try {
    await signIn(peer, "member@requirements.test");
    await peer.goto(`${f.route}/scenarios`);
    await expect(
      peer.getByRole("button", { name: "已编辑的中文业务场景" }),
    ).toBeVisible();
    const current = await data(page, "GET", `${f.p}/pages/${source.id}`);
    await data(page, "PATCH", `${f.p}/pages/${source.id}`, {
      version: current.version,
      is_private: true,
    });
    await expect(
      peer.getByRole("button", { name: "已编辑的中文业务场景" }),
    ).toHaveCount(0);
    const denied = await request(
      peer,
      "GET",
      `${f.p}/scenarios/${scenario.id}`,
    );
    expect([403, 404]).toContain(denied.status);
  } finally {
    await second.close();
  }
});

test("project capability switch clears and restores another active browser without deleting work", async ({
  page,
  browser,
}) => {
  await signIn(page);
  const f = await setup(page);
  const second = await browser.newContext({
    viewport: { width: 1440, height: 960 },
    locale: "zh-CN",
  });
  const peer = await second.newPage();
  try {
    await signIn(peer);
    await peer.goto(`${f.route}/story-map`);
    await expect(peer.getByTestId("requirements-workspace")).toBeVisible();
    await page.goto(`/w/studio/projects/${f.project.id}/settings`);
    const enabled = page.getByRole("switch", {
      name: "需求工作台（四视图）",
      exact: true,
    });
    await enabled.uncheck();
    await page.getByRole("button", { name: "保存更改", exact: true }).click();
    await expect(peer.getByTestId("requirements-workspace")).toHaveCount(0);
    expect(
      (await request(peer, "GET", `${f.p}/requirements/snapshot`)).status,
    ).toBe(403);
    expect(
      (await request(peer, "GET", `${f.p}/scenarios/use-case.svg`)).status,
    ).toBe(403);
    expect((await request(peer, "GET", `${f.p}/issues`)).status).toBe(200);
    await enabled.check();
    await page.getByRole("button", { name: "保存更改", exact: true }).click();
    await expect(peer.getByTestId("requirements-workspace")).toBeVisible();
    await expect(
      peer.getByRole("button", { name: "协作需求Epic", exact: true }),
    ).toBeVisible();
  } finally {
    await second.close();
  }
});

test("fixed Chinese requirements fixture renders the four views in both themes", async ({
  page,
}) => {
  await signIn(page);
  const workspaces = await data(page, "GET", "/api/v1/workspaces");
  const w = workspaces.find((x: { slug: string }) => x.slug === "studio");
  const projects = await data(
    page,
    "GET",
    `/api/v1/workspaces/${w.id}/projects`,
  );
  const project = projects.find(
    (x: { identifier: string }) => x.identifier === "REQ",
  );
  expect(
    project,
    "Run the dedicated seed-requirements fixture first",
  ).toBeTruthy();
  const originalPreferences =
    (await data(page, "GET", "/api/v1/auth/me")).preferences ?? {};
  try {
    for (const theme of ["light", "dark"]) {
      await data(page, "PATCH", "/api/v1/auth/me", {
        preferences: {
          ...originalPreferences,
          appearance: {
            ...originalPreferences.appearance,
            theme,
            locale: "zh-CN",
          },
        },
      });
      for (const view of ["story-map", "gantt", "members", "scenarios"]) {
        await page.goto(
          `/w/studio/projects/${project.id}/requirements/${view}`,
        );
        await expect(page.getByTestId("requirements-workspace")).toBeVisible();
        await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
        await expect(
          page.getByRole("status").filter({ hasText: "实时同步" }),
        ).toBeVisible();
        if (view === "story-map")
          await expect(
            page.locator("[data-testid^='story-card-']"),
          ).toHaveCount(19);
        if (view === "scenarios")
          await expect(page.locator(".req-diagram-viewport svg")).toBeVisible();
        await page.screenshot({
          path: `docs/screenshots/requirements-${view}-${theme}.png`,
          fullPage: false,
        });
      }
    }
  } finally {
    await data(page, "PATCH", "/api/v1/auth/me", {
      preferences: originalPreferences,
    });
  }
});
