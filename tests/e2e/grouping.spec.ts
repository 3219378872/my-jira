import { expect, test, type Locator, type Page } from "@playwright/test";
import { collaborator, login, request } from "./helpers";
import type { Project, State, WorkItem } from "../../apps/web/src/types";

async function data(
  page: Page,
  method: string,
  path: string,
  body?: unknown,
  status = 200,
) {
  const result = await request(page, method, path, body);
  expect(result.status, `${method} ${path}`).toBe(status);
  return result.body?.data;
}

async function workspaceFixture(page: Page) {
  const stamp = Date.now();
  const workspace = await data(
    page,
    "POST",
    "/api/v1/workspaces",
    {
      name: `Grouped work ${stamp}`,
      slug: `grouped-work-${stamp}`,
      timezone: "UTC",
    },
    201,
  );
  const api = `/api/v1/workspaces/${workspace.id}`;
  const route = `/w/${workspace.slug}`;
  return { workspace, api, route };
}

async function projectFixture(
  page: Page,
  workspaceAPI: string,
  workspaceRoute: string,
  name: string,
) {
  const project = (await data(
    page,
    "POST",
    `${workspaceAPI}/projects`,
    {
      name,
      identifier: name.toUpperCase(),
      network: "private",
      guest_can_view_all: true,
    },
    201,
  )) as Project;
  const api = `${workspaceAPI}/projects/${project.id}`;
  const states = (await data(page, "GET", `${api}/states`)) as State[];
  return {
    project,
    api,
    route: `${workspaceRoute}/projects/${project.id}`,
    states,
  };
}

function leaf(page: Page, project: string, subgroup?: string) {
  return page.locator(
    `.remote-issue-leaf[data-group-by="project_id"][data-group-key="${project}"]${subgroup ? `[data-sub-group-key="${subgroup}"]` : ""}`,
  );
}

function card(page: Page, id: string) {
  return page.locator(`[data-group-work-item="${id}"]`);
}

async function rejectedDrag(page: Page, source: Locator, target: Locator) {
  await page.evaluate(() => {
    const session = window as Window & { rejectedGroupOvers?: boolean[] };
    session.rejectedGroupOvers = [];
    const observe = (event: DragEvent) => {
      if ((event.target as HTMLElement)?.closest("[data-rejected-drop]"))
        session.rejectedGroupOvers!.push(event.defaultPrevented);
    };
    document.addEventListener("dragover", observe);
    document.addEventListener(
      "dragend",
      () => document.removeEventListener("dragover", observe),
      { once: true },
    );
  });
  await target.evaluate((element) =>
    element.setAttribute("data-rejected-drop", "true"),
  );
  await source.dragTo(target);
  await expect(target).not.toHaveClass(/drag-over/);
  const accepted = await page.evaluate(
    () =>
      (window as Window & { rejectedGroupOvers?: boolean[] })
        .rejectedGroupOvers ?? [],
  );
  expect(accepted.length).toBeGreaterThan(0);
  expect(accepted.every((value) => !value)).toBe(true);
  await target.evaluate((element) =>
    element.removeAttribute("data-rejected-drop"),
  );
}

test("workspace board moves apply target groups atomically and reject unavailable creator or project targets", async ({
  page,
  browser,
}) => {
  test.setTimeout(180_000);
  page.setDefaultTimeout(15_000);
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await login(page);
  const f = await workspaceFixture(page);
  const memberContext = await browser.newContext();
  const memberPage = await memberContext.newPage();
  memberPage.on("pageerror", (error) => errors.push(error.message));
  const mutations: { method: string; body: Record<string, unknown> }[] = [];
  let movingID = "";
  page.on("request", (event) => {
    if (
      movingID &&
      event.url().includes(`/issues/${movingID}`) &&
      ["POST", "PATCH"].includes(event.method())
    )
      mutations.push({ method: event.method(), body: event.postDataJSON() });
  });
  try {
    const member = await collaborator(memberPage);
    await data(
      page,
      "POST",
      `${f.api}/members`,
      { email: member.email, role: 15 },
      201,
    );
    const source = await projectFixture(page, f.api, f.route, "Origin");
    const destination = await projectFixture(
      page,
      f.api,
      f.route,
      "Destination",
    );
    await data(
      page,
      "POST",
      `${source.api}/members`,
      { user_id: member.id, role: 15 },
      201,
    );
    await data(
      page,
      "POST",
      `${destination.api}/members`,
      { user_id: member.id, role: 5 },
      201,
    );
    const sourceStarted = source.states.find(
      (state) => state.group === "started",
    )!;
    const targetStarted = destination.states.find(
      (state) => state.group === "started",
    )!;
    const sourceCompleted = source.states.find(
      (state) => state.group === "completed",
    )!;
    // A different name earlier in the same stage must not displace an exact
    // workflow name match when the destination state is not explicitly chosen.
    await data(
      page,
      "POST",
      `${destination.api}/states`,
      {
        name: "Earlier alternative",
        group: "started",
        color: "#1a8bcd",
        position: -100,
      },
      201,
    );
    const sourceLabel = await data(
      page,
      "POST",
      `${source.api}/labels`,
      { name: "Origin label", color: "#8a50cc" },
      201,
    );
    const targetLabel = await data(
      page,
      "POST",
      `${destination.api}/labels`,
      { name: "Destination label", color: "#3b8f65" },
      201,
    );
    await data(
      page,
      "POST",
      `${destination.api}/issues`,
      { name: "Destination group sample", label_ids: [targetLabel.id] },
      201,
    );
    const moving = (await data(
      page,
      "POST",
      `${source.api}/issues`,
      {
        name: "Grouped moving work",
        priority: "high",
        state_id: sourceStarted.id,
        label_ids: [sourceLabel.id],
        assignee_ids: [member.id],
        position: 1024,
      },
      201,
    )) as WorkItem;
    movingID = moving.id;
    const ownNeighbor = (await data(
      page,
      "POST",
      `${source.api}/issues`,
      { name: "Own creator neighbor", position: 8192 },
      201,
    )) as WorkItem;
    const memberItem = (await data(
      memberPage,
      "POST",
      `${source.api}/issues`,
      { name: "Member owned work" },
      201,
    )) as WorkItem;
    const view = await data(
      page,
      "POST",
      `${f.api}/views`,
      {
        name: "Grouped board",
        layout: "kanban",
        filters: {},
        display: { group_by: "project_id", order_by: "position" },
      },
      201,
    );
    const viewPath = `${f.api}/views/${view.id}`;
    const viewRoute = `${f.route}/views/${view.id}`;
    const issuePath = (api: string) => `${api}/issues/${moving.id}`;
    const regroup = async (groupBy: string, subgroup = "") => {
      await data(page, "PATCH", viewPath, {
        display: {
          group_by: groupBy,
          sub_group_by: subgroup,
          order_by: "position",
        },
      });
      await page.goto(viewRoute);
      await expect(card(page, moving.id)).toBeVisible();
    };
    const oneMove = (projectID: string, changes: Record<string, unknown>) => {
      expect(mutations).toHaveLength(1);
      expect(mutations[0].method).toBe("POST");
      expect(mutations[0].body).toMatchObject({
        project_id: projectID,
        changes,
      });
      mutations.length = 0;
    };

    await test.step("preserve workflow stage and name with one atomic cross-project request", async () => {
      await page.goto(viewRoute);
      await card(page, moving.id).dragTo(leaf(page, destination.project.id));
      await expect(
        leaf(page, destination.project.id).locator(
          `[data-group-work-item="${moving.id}"]`,
        ),
      ).toBeVisible();
      const result = (await data(
        page,
        "GET",
        issuePath(destination.api),
      )) as WorkItem;
      expect(result).toMatchObject({
        state_id: targetStarted.id,
        priority: "high",
        version: moving.version + 1,
        label_ids: [],
        assignee_ids: [],
      });
      expect((await request(page, "GET", issuePath(source.api))).status).toBe(
        404,
      );
      await expect(card(page, moving.id)).toHaveAttribute(
        "href",
        `${destination.route}/issues/${moving.id}`,
      );
      oneMove(destination.project.id, { position: expect.any(Number) });
    });

    await test.step("move to the exact priority subgroup in the same transaction", async () => {
      await regroup("project_id", "priority");
      await card(page, moving.id).dragTo(leaf(page, source.project.id, "low"));
      await expect(
        leaf(page, source.project.id, "low").locator(
          `[data-group-work-item="${moving.id}"]`,
        ),
      ).toBeVisible();
      const result = (await data(
        page,
        "GET",
        issuePath(source.api),
      )) as WorkItem;
      expect(result).toMatchObject({
        state_id: sourceStarted.id,
        priority: "low",
        version: moving.version + 2,
      });
      oneMove(source.project.id, {
        priority: "low",
        position: expect.any(Number),
      });
    });

    await test.step("403 retains the source and real 409 reloads without a partial move", async () => {
      const before = (await data(
        page,
        "GET",
        issuePath(source.api),
      )) as WorkItem;
      const match = `**${issuePath(source.api)}/move`;
      await page.route(match, (route) =>
        route.fulfill({
          status: 403,
          contentType: "application/json",
          body: JSON.stringify({
            error: { code: "forbidden", message: "Grouped move rejected" },
          }),
        }),
      );
      try {
        await card(page, moving.id).dragTo(
          leaf(page, destination.project.id, "high"),
        );
        await expect(
          page.getByText("Grouped move rejected", { exact: true }),
        ).toBeVisible();
        expect(await data(page, "GET", issuePath(source.api))).toEqual(before);
        await expect(
          leaf(page, source.project.id, "low").locator(
            `[data-group-work-item="${moving.id}"]`,
          ),
        ).toBeVisible();
        oneMove(destination.project.id, { priority: "high" });
      } finally {
        await page.unroute(match);
      }
      const current = (await data(page, "PATCH", issuePath(source.api), {
        name: "Changed by another session",
        version: before.version,
      })) as WorkItem;
      mutations.length = 0;
      const conflict = page.waitForResponse(
        (response) =>
          response.url().endsWith(`${issuePath(source.api)}/move`) &&
          response.request().method() === "POST",
      );
      await card(page, moving.id).dragTo(
        leaf(page, destination.project.id, "high"),
      );
      expect((await conflict).status()).toBe(409);
      await expect(
        leaf(page, source.project.id, "low").locator(
          `[data-group-work-item="${moving.id}"]`,
        ),
      ).toContainText(current.name);
      expect(await data(page, "GET", issuePath(source.api))).toEqual(current);
      expect(
        (await request(page, "GET", issuePath(destination.api))).status,
      ).toBe(404);
      oneMove(destination.project.id, { priority: "high" });
    });

    await test.step("destination labels start with destination membership and explicit empty state groups accept work", async () => {
      const before = (await data(
        page,
        "GET",
        issuePath(source.api),
      )) as WorkItem;
      await data(page, "PATCH", issuePath(source.api), {
        label_ids: [sourceLabel.id],
        version: before.version,
      });
      mutations.length = 0;
      await regroup("project_id", "label_id");
      await card(page, moving.id).dragTo(
        leaf(page, destination.project.id, targetLabel.id),
      );
      await expect(
        leaf(page, destination.project.id, targetLabel.id).locator(
          `[data-group-work-item="${moving.id}"]`,
        ),
      ).toBeVisible();
      expect(
        (await data(page, "GET", issuePath(destination.api))).label_ids,
      ).toEqual([targetLabel.id]);
      oneMove(destination.project.id, { label_ids: [targetLabel.id] });

      await regroup("project_id", "state_id");
      await expect(leaf(page, source.project.id)).toHaveCount(
        source.states.length,
      );
      await expect(leaf(page, destination.project.id)).toHaveCount(
        destination.states.length + 1,
      );
      await card(page, moving.id).dragTo(
        leaf(page, source.project.id, sourceCompleted.id),
      );
      await expect(
        leaf(page, source.project.id, sourceCompleted.id).locator(
          `[data-group-work-item="${moving.id}"]`,
        ),
      ).toBeVisible();
      expect((await data(page, "GET", issuePath(source.api))).state_id).toBe(
        sourceCompleted.id,
      );
      oneMove(source.project.id, { position: expect.any(Number) });
    });

    await test.step("creator crossing has no drop affordance while same creator ordering persists", async () => {
      await regroup("created_by");
      const target = page.locator(
        `.remote-issue-leaf[data-group-key="${member.id}"]`,
      );
      const before = (await data(
        page,
        "GET",
        issuePath(source.api),
      )) as WorkItem;
      await rejectedDrag(page, card(page, moving.id), target);
      expect(mutations).toHaveLength(0);
      expect(await data(page, "GET", issuePath(source.api))).toEqual(before);
      await card(page, moving.id).dragTo(card(page, ownNeighbor.id), {
        targetPosition: { x: 60, y: 60 },
      });
      await expect
        .poll(
          async () => (await data(page, "GET", issuePath(source.api))).position,
        )
        .not.toBe(before.position);
      expect((await data(page, "GET", issuePath(source.api))).created_by).toBe(
        moving.created_by,
      );
      expect(mutations).toHaveLength(1);
      expect(mutations[0].method).toBe("PATCH");
      mutations.length = 0;
    });

    await test.step("a project Guest cannot receive cross-project work even when the source work is owned", async () => {
      await regroup("project_id");
      const attempts: string[] = [];
      memberPage.on("request", (event) => {
        if (
          ["POST", "PATCH"].includes(event.method()) &&
          event.url().includes(`/issues/${memberItem.id}`)
        )
          attempts.push(event.url());
      });
      await memberPage.goto(viewRoute);
      await expect(card(memberPage, memberItem.id)).toHaveAttribute(
        "draggable",
        "true",
      );
      await rejectedDrag(
        memberPage,
        card(memberPage, memberItem.id),
        leaf(memberPage, destination.project.id),
      );
      expect(attempts).toEqual([]);
      expect(
        (await data(page, "GET", `${source.api}/issues/${memberItem.id}`))
          .project_id,
      ).toBe(source.project.id);
    });
    expect(errors).toEqual([]);
  } finally {
    await memberContext.close();
    expect((await request(page, "DELETE", f.api)).status).toBe(204);
  }
});

test("group controls appear only in supported layouts and preserve their saved values", async ({
  page,
}) => {
  test.setTimeout(120_000);
  await login(page);
  const f = await workspaceFixture(page);
  try {
    const project = await projectFixture(page, f.api, f.route, "Layouts");
    await data(
      page,
      "POST",
      `${project.api}/issues`,
      { name: "Layout sample", priority: "high" },
      201,
    );
    await page.goto(`${project.route}/issues`);
    await page.getByRole("button", { name: "筛选", exact: true }).click();
    const group = page.getByRole("combobox", { name: "分组属性", exact: true });
    const subgroup = page.getByRole("combobox", {
      name: "子分组属性",
      exact: true,
    });
    await group.selectOption("priority");
    await subgroup.selectOption("state_id");
    for (const layout of ["表格", "日历", "时间线"]) {
      await page.getByRole("button", { name: layout, exact: true }).click();
      await expect(group).toHaveCount(0);
      await expect(subgroup).toHaveCount(0);
    }
    await page.getByRole("button", { name: "看板", exact: true }).click();
    await expect(group).toHaveValue("priority");
    await expect(subgroup).toHaveValue("state_id");
    await expect(
      page.locator(".remote-issue-group > .remote-group-heading strong"),
    ).toHaveText(["紧急", "高", "中", "低", "无优先级"]);
    await page.getByRole("button", { name: "表格", exact: true }).click();
    await expect
      .poll(
        async () =>
          (
            await data(
              page,
              "GET",
              `${f.api}/preferences/issues-${project.project.id}-active`,
            )
          ).value.layout,
      )
      .toBe("table");
    await page.reload();
    await page.getByRole("button", { name: "筛选", exact: true }).click();
    await expect(group).toHaveCount(0);
    await page.getByRole("button", { name: "列表", exact: true }).click();
    await expect(group).toHaveValue("priority");
    await expect(subgroup).toHaveValue("state_id");

    const view = await data(
      page,
      "POST",
      `${f.api}/views`,
      {
        name: "Layout-aware saved view",
        layout: "kanban",
        filters: {},
        display: {
          group_by: "priority",
          sub_group_by: "state_group",
          order_by: "position",
        },
      },
      201,
    );
    await page.goto(`${f.route}/views/${view.id}`);
    await page.getByRole("button", { name: "编辑视图", exact: true }).click();
    const modal = page.getByRole("dialog");
    const savedGroup = modal.getByRole("combobox", {
      name: "分组",
      exact: true,
    });
    const savedSubgroup = modal.getByRole("combobox", {
      name: "子分组",
      exact: true,
    });
    for (const layout of ["spreadsheet", "calendar", "gantt"]) {
      await modal
        .getByRole("combobox", { name: "布局", exact: true })
        .selectOption(layout);
      await expect(savedGroup).toHaveCount(0);
      await expect(savedSubgroup).toHaveCount(0);
    }
    await modal
      .getByRole("combobox", { name: "布局", exact: true })
      .selectOption("list");
    await expect(savedGroup).toHaveValue("priority");
    await expect(savedSubgroup).toHaveValue("state_group");
    await modal
      .getByRole("combobox", { name: "布局", exact: true })
      .selectOption("calendar");
    await modal.getByRole("button", { name: "保存视图", exact: true }).click();
    await expect(modal).toHaveCount(0);
    await page.reload();
    const persisted = await data(page, "GET", `${f.api}/views/${view.id}`);
    expect(persisted).toMatchObject({
      layout: "calendar",
      display: { group_by: "priority", sub_group_by: "state_group" },
    });
    await page.getByRole("button", { name: "编辑视图", exact: true }).click();
    await expect(savedGroup).toHaveCount(0);
    await modal
      .getByRole("combobox", { name: "布局", exact: true })
      .selectOption("kanban");
    await expect(savedGroup).toHaveValue("priority");
    await expect(savedSubgroup).toHaveValue("state_group");
  } finally {
    expect((await request(page, "DELETE", f.api)).status).toBe(204);
  }
});
