import { expect, test } from "@playwright/test";
import { login, request, workspace } from "./helpers";

test("personal home layout and consecutive favorite assignments persist without losing changes", async ({
  page,
}) => {
  test.setTimeout(90_000);
  await login(page);
  const base = `/api/v1/workspaces/${workspace}`;
  const stamp = Date.now();
  const profile = (await request(page, "GET", "/api/v1/auth/me")).body.data;
  const previousHome = (await request(page, "GET", `${base}/preferences/home`))
    .body.data.value;
  const previousSidebar = (
    await request(page, "GET", `${base}/preferences/sidebar`)
  ).body.data.value;
  const projects: { id: string; name: string }[] = [];
  const favorites: string[] = [];
  try {
    await request(page, "PATCH", "/api/v1/auth/me", {
      preferences: { appearance: { theme: "light", locale: "zh-CN" } },
    });
    for (const suffix of ["A", "B"]) {
      const created = await request(page, "POST", `${base}/projects`, {
        name: `Preferences ${suffix} ${stamp}`,
        identifier: `P${suffix}${stamp.toString(36).toUpperCase()}`,
      });
      expect(created.status).toBe(201);
      projects.push(created.body.data);
    }
    await page.goto("/w/studio/home");
    await page.getByRole("button", { name: "自定义首页", exact: true }).click();
    const layout = page.getByRole("dialog", {
      name: "自定义首页",
      exact: true,
    });
    await layout.getByRole("button", { name: "重置布局", exact: true }).click();
    await layout.getByLabel("最近访问", { exact: true }).uncheck();
    await layout
      .getByRole("button", { name: "上移 快捷链接", exact: true })
      .click();
    await layout.getByRole("button", { name: "保存", exact: true }).click();
    await expect(layout).toBeHidden();
    await page.reload();
    await expect(page.locator('[data-widget="recent"]')).toHaveCount(0);
    await expect
      .poll(() =>
        page
          .locator(".home-widgets > [data-widget]")
          .evaluateAll((items) =>
            items.map((item) => item.getAttribute("data-widget")),
          ),
      )
      .toEqual([
        "stats",
        "upcoming",
        "projects",
        "favorites",
        "links",
        "stickies",
      ]);

    await page
      .getByRole("button", { name: "添加快捷链接", exact: true })
      .click();
    const quickLink = page.getByRole("dialog", {
      name: "添加快捷链接",
      exact: true,
    });
    await quickLink
      .getByLabel("名称", { exact: true })
      .fill(`Preferences link ${stamp}`);
    await quickLink
      .getByLabel("链接", { exact: true })
      .fill("https://example.com/preferences");
    await quickLink.getByRole("button", { name: "保存", exact: true }).click();
    await expect(quickLink).toBeHidden();
    await page.reload();
    await expect(
      page.getByRole("link", {
        name: `Preferences link ${stamp}`,
        exact: true,
      }),
    ).toHaveAttribute("href", "https://example.com/preferences");

    for (const project of projects) {
      await page
        .locator(".project-nav .sidebar-entry")
        .filter({ hasText: project.name })
        .getByRole("button", { name: "更多操作", exact: true })
        .click();
      await page
        .getByRole("menuitem", { name: "收藏项目", exact: true })
        .click();
      await expect(
        page
          .locator(".sidebar-favorites")
          .getByRole("link", { name: project.name, exact: true }),
      ).toBeVisible();
    }
    const records = (await request(page, "GET", `${base}/favorites`)).body
      .data as { id: string; entity_id: string }[];
    favorites.push(
      ...records
        .filter((record) =>
          projects.some((project) => project.id === record.entity_id),
        )
        .map((record) => record.id),
    );
    expect(favorites).toHaveLength(2);
    await page
      .getByRole("button", { name: "管理收藏分组", exact: true })
      .click();
    const dialog = page.getByRole("dialog", {
      name: "管理收藏分组",
      exact: true,
    });
    const groupName = `Preferences group ${stamp}`;
    const groupRow = dialog.locator("strong").filter({hasText: groupName}).locator("..");
    await dialog.getByLabel("新分组", { exact: true }).fill(groupName);
    await dialog.getByRole("button", { name: "添加分组", exact: true }).click();
    await expect(groupRow).toBeVisible();
    const groups = (await request(page, "GET", `${base}/preferences/sidebar`))
      .body.data.value.groups as { id: string; name: string }[];
    const groupID = groups.find((group) => group.name === groupName)!.id;

    // Keep the first write in flight while a second controlled selector changes.
    // Both requests must derive nested mappings from the latest committed value.
    let delayed = false;
    await page.route(
      `**/workspaces/${workspace}/preferences/sidebar`,
      async (route) => {
        if (route.request().method() === "PATCH" && !delayed) {
          delayed = true;
          await new Promise((resolve) => setTimeout(resolve, 180));
        }
        await route.continue();
      },
    );
    await dialog
      .getByLabel(`分组 ${projects[0].name}`, { exact: true })
      .selectOption(groupID);
    await dialog
      .getByLabel(`分组 ${projects[1].name}`, { exact: true })
      .selectOption(groupID);
    await expect
      .poll(async () => {
        const mapping =
          (
            await request(page, "GET", `${base}/preferences/sidebar`)
          ).body.data.value.favorite_groups ?? {};
        return favorites.every((id) => mapping[id] === groupID);
      })
      .toBe(true);
    await page.unroute(`**/workspaces/${workspace}/preferences/sidebar`);
    await page.keyboard.press("Escape");
    await page.reload();
    const group = page
      .locator(".sidebar-favorite-group")
      .filter({ hasText: groupName })
      .locator("..");
    await expect(group.locator(".sidebar-entry")).toHaveCount(2);
    await group
      .locator(".sidebar-entry")
      .filter({ hasText: projects[1].name })
      .getByRole("button", { name: "更多操作", exact: true })
      .click();
    await page.getByRole("menuitem", { name: "上移", exact: true }).click();
    await expect(group.locator(".sidebar-entry").first()).toContainText(
      projects[1].name,
    );
    await page.reload();
    await expect(group.locator(".sidebar-entry").first()).toContainText(
      projects[1].name,
    );

    await page
      .getByRole("button", { name: "管理收藏分组", exact: true })
      .click();
    await groupRow
      .getByRole("button", { name: "删除分组", exact: true })
      .click();
    await expect(groupRow).toHaveCount(0);
    await page.keyboard.press("Escape");
    for (const project of projects)
      await expect(
        page
          .locator(".sidebar-favorites")
          .getByRole("link", { name: project.name, exact: true }),
      ).toBeVisible();
  } finally {
    await page.unroute(`**/workspaces/${workspace}/preferences/sidebar`);
    for (const id of favorites)
      await request(page, "DELETE", `${base}/favorites/${id}`);
    for (const project of projects)
      await request(page, "DELETE", `${base}/projects/${project.id}`);
    await request(page, "PATCH", `${base}/preferences/home`, {
      value: {
        widgets: [
          "stats",
          "upcoming",
          "projects",
          "favorites",
          "recent",
          "stickies",
          "links",
        ],
        hidden: [],
        quick_links: [],
        ...previousHome,
      },
    });
    await request(page, "PATCH", `${base}/preferences/sidebar`, {
      value: {
        pinned: [],
        order: [],
        groups: [],
        favorite_groups: {},
        ...previousSidebar,
      },
    });
    await request(page, "PATCH", "/api/v1/auth/me", {
      preferences: {
        appearance: profile.preferences.appearance ?? {
          theme: "light",
          locale: "zh-CN",
        },
      },
    });
  }
});
