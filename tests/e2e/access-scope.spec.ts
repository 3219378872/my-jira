import { expect, test, type Browser, type BrowserContext, type Page } from "@playwright/test";
import { login, password, request } from "./helpers";

type Entity = { id: string; name: string; version: number };
type Asset = { id: string; download_url: string };
type Actor = { page: Page; context: BrowserContext; id: string; email: string; membership?: string };
type ProjectFixture = {
  id: string;
  api: string;
  route: string;
  memberMembership: string;
  guestMembership: string;
  adminItem: Entity;
  guestItem: Entity;
  adminPage: Entity;
  privatePage: Entity;
  guestPage: Entity;
  asset: Asset;
};
type Group = { items?: Entity[]; groups?: Group[] };

async function data(page: Page, method: string, path: string, body?: unknown, status = 200) {
  const response = await request(page, method, path, body);
  expect(response.status, `${method} ${path}`).toBe(status);
  return response.body?.data;
}

function ids(values: { id: string }[]) {
  return values.map((value) => value.id).sort();
}

function groupedIDs(groups: Group[]): string[] {
  return groups.flatMap((group) => [...ids(group.items ?? []), ...groupedIDs(group.groups ?? [])]).sort();
}

async function denied(page: Page, method: string, path: string, body?: unknown) {
  const response = await request(page, method, path, body);
  expect([403, 404], `${method} ${path}`).toContain(response.status);
}

async function account(browser: Browser, label: string, stamp: string): Promise<Actor> {
  const context = await browser.newContext({ locale: "zh-CN", viewport: { width: 1440, height: 960 } });
  const page = await context.newPage();
  page.setDefaultTimeout(15_000);
  await page.goto("/login");
  const email = `e02-${label}-${stamp}@myjira.local`;
  const result = await data(page, "POST", "/api/v1/auth/register", { email, password, display_name: `E02 ${label}` }, 201);
  return { page, context, id: result.user.id, email };
}

async function upload(page: Page, path: string, filename: string, content: string): Promise<Asset> {
  const response = await page.evaluate(async ({ path, filename, content }) => {
    if (!document.cookie.includes("mj_csrf=")) await fetch("/api/v1/auth/csrf");
    const cookie = document.cookie.split("; ").find((entry) => entry.startsWith("mj_csrf="));
    const form = new FormData();
    form.append("file", new File([content], filename, { type: "text/plain" }));
    const result = await fetch(path, { method: "POST", headers: { "X-CSRF-Token": cookie ? decodeURIComponent(cookie.slice(8)) : "" }, body: form });
    return { status: result.status, body: await result.json() };
  }, { path, filename, content });
  expect(response.status, `upload ${path}`).toBe(201);
  return response.body.data;
}

test("browser sessions recheck guest visibility, ownership and private/public membership across every read surface", async ({ page: admin, browser }) => {
  test.setTimeout(240_000);
  admin.setDefaultTimeout(15_000);
  const stamp = String(Date.now());
  const month = new Date().toISOString().slice(0, 7);
  const actors: Actor[] = [];
  const projects: ProjectFixture[] = [];
  const errors: string[] = [];
  let workspaceAPI = "";
  let workspaceRoute = "";
  admin.on("pageerror", (error) => errors.push(error.message));
  await login(admin);

  try {
    const workspace = await data(admin, "POST", "/api/v1/workspaces", { name: `E02 scope ${stamp}`, slug: `e02-scope-${stamp}`, timezone: "UTC" }, 201);
    workspaceAPI = `/api/v1/workspaces/${workspace.id}`;
    workspaceRoute = `/w/${workspace.slug}`;
    for (const label of ["member", "guest", "outsider"]) {
      const actor = await account(browser, label, stamp);
      actor.page.on("pageerror", (error) => errors.push(error.message));
      actors.push(actor);
    }
    const [member, guest, outsider] = actors;
    for (const actor of [member, guest]) {
      actor.membership = (await data(admin, "POST", `${workspaceAPI}/members`, { email: actor.email, role: 15 }, 201)).id;
    }

    await test.step("seed independent private/public projects while future Guest can author content", async () => {
      for (const network of ["private", "public"] as const) {
        const project = await data(admin, "POST", `${workspaceAPI}/projects`, { name: `E02 ${network} ${stamp}`, identifier: network === "private" ? "PRIV" : "PUBL", network }, 201);
        const api = `${workspaceAPI}/projects/${project.id}`;
        const memberMembership = (await data(admin, "POST", `${api}/members`, { user_id: member.id, role: 15 }, 201)).id;
        const guestMembership = (await data(admin, "POST", `${api}/members`, { user_id: guest.id, role: 15 }, 201)).id;
        const adminItem = await data(admin, "POST", `${api}/issues`, { name: `E02 scope ${network} admin item`, priority: "high", start_date: `${month}-10`, target_date: `${month}-12`, assignee_ids: [member.id, guest.id] }, 201);
        const guestItem = await data(guest.page, "POST", `${api}/issues`, { name: `E02 scope ${network} guest item`, priority: "low", start_date: `${month}-10`, target_date: `${month}-12`, assignee_ids: [member.id] }, 201);
        const adminPage = await data(admin, "POST", `${api}/pages`, { name: `E02 scope ${network} shared page` }, 201);
        const privatePage = await data(admin, "POST", `${api}/pages`, { name: `E02 scope ${network} private page`, is_private: true }, 201);
        const guestPage = await data(guest.page, "POST", `${api}/pages`, { name: `E02 scope ${network} guest page` }, 201);
        const asset = await upload(admin, `${api}/issues/${adminItem.id}/attachments`, `e02-${network}.txt`, `E02 ${network} attachment ${stamp}`);
        projects.push({ id: project.id, api, route: `${workspaceRoute}/projects/${project.id}`, memberMembership, guestMembership, adminItem, guestItem, adminPage, privatePage, guestPage, asset });
      }
    });
    const [privateProject, publicProject] = projects;
    const allItems = projects.flatMap((project) => [project.adminItem, project.guestItem]);
    const guestItems = projects.map((project) => project.guestItem);
    const publicPages = projects.flatMap((project) => [project.adminPage, project.guestPage]);
    const ownPages = projects.map((project) => project.guestPage);

    async function workspaceReads(actor: Page, expectedItems: Entity[], expectedPages: Entity[]) {
      const list = await request(actor, "GET", `${workspaceAPI}/issues?limit=100`);
      expect(list.status).toBe(200);
      expect(ids(list.body.data)).toEqual(ids(expectedItems));
      expect(list.body.pagination.total).toBe(expectedItems.length);
      const grouped = await request(actor, "GET", `${workspaceAPI}/issues?group_by=priority&show_empty=true&limit=50`);
      expect(grouped.status).toBe(200);
      expect(groupedIDs(grouped.body.data)).toEqual(ids(expectedItems));
      expect(grouped.body.total_items).toBe(expectedItems.length);
      const search = await data(actor, "GET", `${workspaceAPI}/search?q=E02%20scope`);
      expect(ids(search.issues)).toEqual(ids(expectedItems));
      expect(ids(search.pages)).toEqual(ids(expectedPages));
      const analytics = await data(actor, "GET", `${workspaceAPI}/analytics`);
      expect(analytics.total).toBe(expectedItems.length);
      const profile = await data(actor, "GET", `${workspaceAPI}/profiles/${guest.id}/stats`);
      expect(profile.created).toBe(expectedItems.filter((item) => guestItems.some((own) => own.id === item.id)).length);
    }

    async function projectReads(actor: Page, project: ProjectFixture, expectedItems: Entity[], expectedPages: Entity[]) {
      const list = await data(actor, "GET", `${project.api}/issues?limit=100`);
      expect(ids(list)).toEqual(ids(expectedItems));
      const grouped = await request(actor, "GET", `${project.api}/issues?group_by=priority&sub_group_by=state_id&show_empty=true&limit=50`);
      expect(grouped.status).toBe(200);
      expect(groupedIDs(grouped.body.data)).toEqual(ids(expectedItems));
      expect(grouped.body.total_items).toBe(expectedItems.length);
      const pages = await data(actor, "GET", `${project.api}/pages`);
      expect(ids(pages)).toEqual(ids(expectedPages));
      const summary = await data(actor, "GET", `${project.api}/pages/summary`);
      expect(summary.total).toBe(expectedPages.length);
      for (const item of expectedItems) expect((await data(actor, "GET", `${project.api}/issues/${item.id}`)).id).toBe(item.id);
      for (const document of expectedPages) expect((await data(actor, "GET", `${project.api}/pages/${document.id}/content`)).name).toBe(document.name);
    }

    async function notificationIDs(actor: Page) {
      const notifications = await data(actor, "GET", `${workspaceAPI}/notifications?reason=assigned&limit=500`);
      return [...new Set<string>(notifications.map((notification: { entity_id: string }) => notification.entity_id))].sort();
    }

    await test.step("Admin, Member and nonmember sessions expose only their current resources", async () => {
      await workspaceReads(admin, allItems, projects.flatMap((project) => [project.adminPage, project.privatePage, project.guestPage]));
      await workspaceReads(member.page, allItems, publicPages);
      for (const project of projects) {
        expect((await data(admin, "GET", project.api)).role).toBe(20);
        expect((await data(member.page, "GET", project.api)).role).toBe(15);
        await projectReads(member.page, project, [project.adminItem, project.guestItem], [project.adminPage, project.guestPage]);
        await denied(member.page, "GET", `${project.api}/pages/${project.privatePage.id}`);
        await denied(outsider.page, "GET", project.api);
        await denied(outsider.page, "GET", `${project.api}/issues`);
        await denied(outsider.page, "GET", `${project.api}/issues/${project.adminItem.id}`);
        await denied(outsider.page, "GET", `${project.api}/issues?group_by=priority`);
        await denied(outsider.page, "GET", `${project.api}/pages`);
        await denied(outsider.page, "GET", `${project.api}/issues/${project.adminItem.id}/attachments`);
        const download = await outsider.page.request.get(project.asset.download_url);
        expect([403, 404]).toContain(download.status());
      }
      for (const path of ["issues", "search?q=E02", "analytics", "notifications", "notifications/unread-count", "exports"]) await denied(outsider.page, "GET", `${workspaceAPI}/${path}`);
      await expect.poll(() => notificationIDs(member.page), { timeout: 30_000 }).toEqual(ids(allItems));
      await expect.poll(() => notificationIDs(guest.page), { timeout: 30_000 }).toEqual(ids(projects.map((project) => project.adminItem)));
    });

    const exports: { id: string; download_url: string; scope: "private" | "public" | "workspace" }[] = [];
    await test.step("the live outbox, Redis worker and object store complete scoped exports", async () => {
      for (const scope of ["private", "public", "workspace"] as const) {
        const result = await data(member.page, "POST", `${workspaceAPI}/exports`, { format: "csv", ...(scope === "workspace" ? {} : { project_id: scope === "private" ? privateProject.id : publicProject.id }), filters: {} }, 202);
        exports.push({ ...result, scope });
      }
      await expect.poll(async () => {
        const history = await data(member.page, "GET", `${workspaceAPI}/exports`);
        return exports.map((item) => history.find((record: { id: string }) => record.id === item.id)?.status);
      }, { timeout: 45_000 }).toEqual(["completed", "completed", "completed"]);
      for (const item of exports) {
        const response = await member.page.request.get(item.download_url);
        expect(response.status()).toBe(200);
        const content = await response.text();
        for (const project of projects) {
          const included = item.scope === "workspace" || item.scope === (project === privateProject ? "private" : "public");
          expect(content.includes(project.adminItem.name)).toBe(included);
          expect(content.includes(project.guestItem.name)).toBe(included);
        }
        const otherOwner = await admin.request.get(item.download_url);
        expect(otherOwner.status()).toBe(404);
      }
    });

    await test.step("a real admin member-settings change demotes the future Guest", async () => {
      await admin.goto(`${workspaceRoute}/settings/members`);
      const row = admin.locator("tbody tr").filter({ hasText: guest.email });
      await expect(row).toHaveCount(1);
      const response = admin.waitForResponse((result) => result.request().method() === "PATCH" && result.url().endsWith(`/members/${guest.membership}`));
      await row.getByRole("combobox", { name: "成员角色", exact: true }).selectOption("5");
      expect((await response).status()).toBe(200);
      await expect(row.getByRole("combobox", { name: "成员角色", exact: true })).toHaveValue("5");
      expect((await data(guest.page, "GET", workspaceAPI)).role).toBe(5);
      await admin.screenshot({ path: ".local/evidence/browser-e02-member-role.png", fullPage: true });
    });

    async function guestReads(enabled: boolean) {
      await workspaceReads(guest.page, enabled ? allItems : guestItems, enabled ? publicPages : ownPages);
      for (const project of projects) {
        const detail = await data(guest.page, "GET", project.api);
        expect(detail.role).toBe(5);
        expect(detail.guest_can_view_all).toBe(enabled);
        await projectReads(guest.page, project, enabled ? [project.adminItem, project.guestItem] : [project.guestItem], enabled ? [project.adminPage, project.guestPage] : [project.guestPage]);
        await denied(guest.page, "GET", `${project.api}/pages/${project.privatePage.id}`);
        await denied(guest.page, "PATCH", project.api, { guest_can_view_all: !enabled });
        await denied(guest.page, "POST", `${project.api}/issues`, { name: "E02 forbidden Guest ordinary creation" });
        const attachments = await request(guest.page, "GET", `${project.api}/issues/${project.adminItem.id}/attachments`);
        const download = await guest.page.request.get(project.asset.download_url);
        if (enabled) {
          expect(attachments.status).toBe(200);
          expect(attachments.body.data.map((asset: Asset) => asset.id)).toContain(project.asset.id);
          expect(download.status()).toBe(200);
          expect(await download.text()).toContain(stamp);
          const current = await data(guest.page, "GET", `${project.api}/issues/${project.adminItem.id}`);
          const forbidden = await request(guest.page, "PATCH", `${project.api}/issues/${project.adminItem.id}`, { version: current.version, priority: "urgent" });
          expect(forbidden.status).toBe(403);
          const pageCurrent = await data(guest.page, "GET", `${project.api}/pages/${project.adminPage.id}`);
          await denied(guest.page, "PATCH", `${project.api}/pages/${project.adminPage.id}`, { version: pageCurrent.version, name: "E02 forbidden Guest page write" });
        } else {
          expect(attachments.status).toBe(404);
          expect(download.status()).toBe(404);
          await denied(guest.page, "GET", `${project.api}/issues/${project.adminItem.id}`);
          await denied(guest.page, "GET", `${project.api}/pages/${project.adminPage.id}`);
        }
      }
      expect(await notificationIDs(guest.page)).toEqual(enabled ? ids(projects.map((project) => project.adminItem)) : []);
      const notifications = await data(guest.page, "GET", `${workspaceAPI}/notifications?unread=true&limit=500`);
      expect((await data(guest.page, "GET", `${workspaceAPI}/notifications/unread-count`)).count).toBe(notifications.length);
      if (!enabled) expect(notifications).toEqual([]);
      await denied(guest.page, "POST", `${workspaceAPI}/exports`, { format: "csv", project_id: privateProject.id });
      await denied(guest.page, "GET", `${workspaceAPI}/exports`);
      await guest.page.goto(`${privateProject.route}/issues`);
      await guest.page.getByRole("button", { name: "列表", exact: true }).click();
      await expect(guest.page.locator(".issue-row")).toHaveCount(enabled ? 2 : 1);
      await expect(guest.page.getByRole("button", { name: "添加工作项", exact: true })).toHaveCount(0);
      await expect(guest.page.getByRole("button", { name: "新建工作项", exact: true })).toHaveCount(0);
      const ownRow = guest.page.locator(".issue-row").filter({ hasText: privateProject.guestItem.name });
      await expect(ownRow.getByRole("checkbox")).toBeDisabled();
      await expect(ownRow.getByRole("button", { name: "更改状态", exact: true })).toBeEnabled();
      await expect(ownRow.getByRole("button", { name: "更改优先级", exact: true })).toBeEnabled();
      await expect(guest.page.getByText(privateProject.guestItem.name, { exact: true })).toBeVisible();
      if (enabled) {
        const otherRow = guest.page.locator(".issue-row").filter({ hasText: privateProject.adminItem.name });
        await expect(otherRow).toBeVisible();
        await expect(otherRow.getByRole("checkbox")).toBeDisabled();
        await expect(otherRow.getByRole("button", { name: "更改状态", exact: true })).toBeDisabled();
        await expect(otherRow.getByRole("button", { name: "更改优先级", exact: true })).toBeDisabled();
      } else await expect(guest.page.getByText(privateProject.adminItem.name, { exact: true })).toHaveCount(0);
      await guest.page.screenshot({ path: `.local/evidence/browser-e02-guest-${enabled ? "all" : "owned"}.png`, fullPage: true });
    }

    async function guestEditingUI() {
      await guest.page.keyboard.press("c");
      await expect(guest.page.getByRole("dialog")).toHaveCount(0);
      await guest.page.getByRole("button", { name: "表格", exact: true }).click();
      const ownTableRow = guest.page.getByRole("row").filter({ hasText: privateProject.guestItem.name });
      const otherTableRow = guest.page.getByRole("row").filter({ hasText: privateProject.adminItem.name });
      await expect(otherTableRow.getByRole("combobox", { name: "状态", exact: true })).toBeDisabled();
      await expect(otherTableRow.getByLabel("目标日期", { exact: true })).toBeDisabled();
      await expect(ownTableRow.getByRole("combobox", { name: "状态", exact: true })).toBeEnabled();
      await expect(ownTableRow.getByLabel("目标日期", { exact: true })).toBeEnabled();
      await ownTableRow.getByLabel("目标日期", { exact: true }).fill(`${month}-14`);
      await expect.poll(async () => (await data(guest.page, "GET", `${privateProject.api}/issues/${privateProject.guestItem.id}`)).target_date).toBe(`${month}-14`);
      privateProject.guestItem.version = (await data(guest.page, "GET", `${privateProject.api}/issues/${privateProject.guestItem.id}`)).version;

      await guest.page.getByRole("button", { name: "看板", exact: true }).click();
      const ownCard = guest.page.locator(".kanban-card").filter({ hasText: privateProject.guestItem.name });
      const otherCard = guest.page.locator(".kanban-card").filter({ hasText: privateProject.adminItem.name });
      await expect(ownCard.getByRole("button", { name: "拖动工作项", exact: true })).toBeEnabled();
      await expect(otherCard.getByRole("button", { name: "拖动工作项", exact: true })).toBeDisabled();
      await expect(otherCard.getByRole("button", { name: "移动到其他状态", exact: true })).toBeDisabled();
      await expect(guest.page.getByRole("button", { name: "添加工作项", exact: true })).toHaveCount(0);

      await guest.page.getByRole("button", { name: "筛选", exact: true }).click();
      await guest.page.getByRole("combobox", { name: "分组属性", exact: true }).selectOption("priority");
      await expect(guest.page.locator(`[data-group-work-item="${privateProject.guestItem.id}"]`)).toHaveAttribute("draggable", "true");
      await expect(guest.page.locator(`[data-group-work-item="${privateProject.adminItem.id}"]`)).toHaveAttribute("draggable", "false");
      await guest.page.getByRole("combobox", { name: "分组属性", exact: true }).selectOption("state_id");
      await guest.page.getByRole("button", { name: "筛选", exact: true }).click();

      await guest.page.getByRole("button", { name: "日历", exact: true }).click();
      await expect(guest.page.locator(".calendar-issue").filter({ hasText: privateProject.guestItem.name })).toHaveAttribute("draggable", "true");
      await expect(guest.page.locator(".calendar-issue").filter({ hasText: privateProject.adminItem.name })).toHaveAttribute("draggable", "false");
      await guest.page.getByRole("button", { name: "时间线", exact: true }).click();
      const ownTimeline = guest.page.locator(`[data-timeline-item="${privateProject.guestItem.id}"]`);
      const otherTimeline = guest.page.locator(`[data-timeline-item="${privateProject.adminItem.id}"]`);
      await expect(ownTimeline.locator(".timeline-bar")).toHaveAttribute("draggable", "true");
      await expect(otherTimeline.locator(".timeline-bar")).toHaveAttribute("draggable", "false");
      await expect(ownTimeline.locator(".timeline-resize-handle")).toHaveCount(2);
      await expect(otherTimeline.locator(".timeline-resize-handle")).toHaveCount(0);

      await guest.page.goto(`${privateProject.route}/issues/${privateProject.adminItem.id}`);
      await expect(guest.page.getByRole("textbox", { name: "工作项标题", exact: true })).toHaveJSProperty("readOnly", true);
      await expect(guest.page.locator(".detail-title-section .tiptap")).toHaveAttribute("contenteditable", "false");
      await expect(guest.page.locator(".detail-properties select").first()).toBeDisabled();
      await guest.page.screenshot({ path: ".local/evidence/browser-e02-guest-ui-readonly.png", fullPage: true });
      await guest.page.goto(`${privateProject.route}/issues/${privateProject.guestItem.id}`);
      await expect(guest.page.getByRole("textbox", { name: "工作项标题", exact: true })).toBeEditable();
      await expect(guest.page.locator(".detail-title-section .tiptap")).toHaveAttribute("contenteditable", "true");
      await expect(guest.page.locator(".detail-properties select").first()).toBeEnabled();
      await guest.page.screenshot({ path: ".local/evidence/browser-e02-guest-ui-owner.png", fullPage: true });
    }

    await test.step("Guest false means owner-only reads and still permits the owner's work-item/page writes", async () => {
      await guestReads(false);
      const changedItem = await data(guest.page, "PATCH", `${privateProject.api}/issues/${privateProject.guestItem.id}`, { version: privateProject.guestItem.version, priority: "medium" });
      expect(changedItem.version).toBe(privateProject.guestItem.version + 1);
      privateProject.guestItem.version = changedItem.version;
      const changedPage = await data(guest.page, "PATCH", `${privateProject.api}/pages/${privateProject.guestPage.id}`, { version: privateProject.guestPage.version, name: privateProject.guestPage.name });
      expect(changedPage.version).toBe(privateProject.guestPage.version + 1);
      privateProject.guestPage.version = changedPage.version;
      const ownAsset = await upload(guest.page, `${privateProject.api}/issues/${privateProject.guestItem.id}/attachments`, "e02-guest-own.txt", `E02 owned Guest attachment ${stamp}`);
      expect((await guest.page.request.get(ownAsset.download_url)).status()).toBe(200);
      await data(guest.page, "DELETE", `${privateProject.api}/assets/${ownAsset.id}`, undefined, 204);
    });

    async function setVisibility(enabled: boolean) {
      await admin.goto(`${privateProject.route}/settings`);
      await admin.getByRole("switch", { name: "来宾可以查看项目全部工作", exact: true }).setChecked(enabled);
      const saved = admin.waitForResponse((response) => response.request().method() === "PATCH" && new URL(response.url()).pathname === privateProject.api);
      await admin.getByRole("button", { name: "保存更改", exact: true }).click();
      expect((await saved).status()).toBe(200);
      await data(admin, "PATCH", publicProject.api, { guest_can_view_all: enabled });
    }

    await test.step("the real project-settings switch expands reads and collaboration without elevating Guest", async () => {
      await setVisibility(true);
      await guestReads(true);
      await guestEditingUI();
      const collaborativeAsset = await upload(guest.page, `${privateProject.api}/issues/${privateProject.adminItem.id}/attachments`, "e02-guest-shared.txt", `E02 visible Guest collaboration ${stamp}`);
      await denied(guest.page, "DELETE", `${privateProject.api}/assets/${privateProject.asset.id}`);
      await data(guest.page, "DELETE", `${privateProject.api}/assets/${collaborativeAsset.id}`, undefined, 204);
      const current = await data(member.page, "GET", `${privateProject.api}/issues/${privateProject.adminItem.id}`);
      const updated = await data(member.page, "PATCH", `${privateProject.api}/issues/${privateProject.adminItem.id}`, { version: current.version, priority: "medium" });
      expect(updated.version).toBe(current.version + 1);
      privateProject.adminItem.version = updated.version;
    });

    await test.step("turning the setting off removes previously readable resources on subsequent API reads and UI reload", async () => {
      await setVisibility(false);
      await guestReads(false);
    });

    await test.step("private project removal revokes all private read surfaces and completed export downloads", async () => {
      await data(admin, "DELETE", `${privateProject.api}/members/${privateProject.memberMembership}`, undefined, 204);
      for (const suffix of ["", "/issues", `/issues/${privateProject.adminItem.id}`, "/issues?group_by=priority", "/pages", `/pages/${privateProject.adminPage.id}`, `/issues/${privateProject.adminItem.id}/attachments`]) await denied(member.page, "GET", `${privateProject.api}${suffix}`);
      expect([403, 404]).toContain((await member.page.request.get(privateProject.asset.download_url)).status());
      await workspaceReads(member.page, [publicProject.adminItem, publicProject.guestItem], [publicProject.adminPage, publicProject.guestPage]);
      expect(await notificationIDs(member.page)).toEqual(ids([publicProject.adminItem, publicProject.guestItem]));
      for (const item of exports) {
        const response = await member.page.request.get(item.download_url);
        if (item.scope === "public") expect(response.status()).toBe(200);
        else if (item.scope === "workspace") expect(response.status()).toBe(403);
        else expect([403, 404]).toContain(response.status());
      }
      await member.page.goto(`${workspaceRoute}/my-work`);
      await expect(member.page.locator(".workspace-work-table > a")).toHaveCount(2);
      await expect(member.page.getByText(privateProject.adminItem.name, { exact: true })).toHaveCount(0);
      await member.page.screenshot({ path: ".local/evidence/browser-e02-private-membership-revoked.png", fullPage: true });
    });

    await test.step("public project removal preserves workspace-visible issues and removes explicitly scoped Pages", async () => {
      await data(admin, "DELETE", `${publicProject.api}/members/${publicProject.memberMembership}`, undefined, 204);
      const detail = await data(member.page, "GET", publicProject.api);
      expect(detail.is_member).toBe(false);
      expect(ids(await data(member.page, "GET", `${publicProject.api}/issues`))).toEqual(ids([publicProject.adminItem, publicProject.guestItem]));
      expect((await data(member.page, "GET", `${publicProject.api}/issues/${publicProject.adminItem.id}`)).id).toBe(publicProject.adminItem.id);
      const groups = await request(member.page, "GET", `${publicProject.api}/issues?group_by=priority`);
      expect(groups.status).toBe(200);
      expect(groupedIDs(groups.body.data)).toEqual(ids([publicProject.adminItem, publicProject.guestItem]));
      await denied(member.page, "GET", `${publicProject.api}/pages`);
      await denied(member.page, "GET", `${publicProject.api}/pages/${publicProject.adminPage.id}/content`);
      await workspaceReads(member.page, [publicProject.adminItem, publicProject.guestItem], []);
      expect(await notificationIDs(member.page)).toEqual(ids([publicProject.adminItem, publicProject.guestItem]));
      expect((await member.page.request.get(publicProject.asset.download_url)).status()).toBe(200);
      expect((await member.page.request.get(exports.find((item) => item.scope === "public")!.download_url)).status()).toBe(200);
    });

    await test.step("workspace removal makes the former Member equivalent to an authenticated nonmember", async () => {
      await data(admin, "DELETE", `${workspaceAPI}/members/${member.membership}`, undefined, 204);
      for (const path of ["issues", "issues?group_by=priority", "search?q=E02", "analytics", "notifications", "notifications/unread-count", "exports"]) await denied(member.page, "GET", `${workspaceAPI}/${path}`);
      for (const project of projects) {
        for (const suffix of ["", "/issues", `/issues/${project.adminItem.id}`, "/issues?group_by=priority", "/pages", `/issues/${project.adminItem.id}/attachments`]) await denied(member.page, "GET", `${project.api}${suffix}`);
        expect([403, 404]).toContain((await member.page.request.get(project.asset.download_url)).status());
      }
      for (const item of exports) expect([403, 404]).toContain((await member.page.request.get(item.download_url)).status());
      await workspaceReads(admin, allItems, projects.flatMap((project) => [project.adminPage, project.privatePage, project.guestPage]));
      expect(errors).toEqual([]);
    });

    await test.step("Guest ownership never outlives required project or workspace membership", async () => {
      await data(admin, "DELETE", `${privateProject.api}/members/${privateProject.guestMembership}`, undefined, 204);
      for (const suffix of ["/issues", `/issues/${privateProject.guestItem.id}`, "/issues?group_by=priority", "/pages", `/pages/${privateProject.guestPage.id}/content`, `/issues/${privateProject.guestItem.id}/attachments`]) await denied(guest.page, "GET", `${privateProject.api}${suffix}`);
      await denied(guest.page, "PATCH", `${privateProject.api}/issues/${privateProject.guestItem.id}`, { version: privateProject.guestItem.version, priority: "urgent" });
      await denied(guest.page, "PATCH", `${privateProject.api}/pages/${privateProject.guestPage.id}`, { version: privateProject.guestPage.version, name: "E02 revoked owner write" });
      await workspaceReads(guest.page, [publicProject.guestItem], [publicProject.guestPage]);
      expect(await notificationIDs(guest.page)).toEqual([]);

      await data(admin, "DELETE", `${publicProject.api}/members/${publicProject.guestMembership}`, undefined, 204);
      await workspaceReads(guest.page, [publicProject.guestItem], []);
      await denied(guest.page, "GET", `${publicProject.api}/pages/${publicProject.guestPage.id}/content`);
      await denied(guest.page, "PATCH", `${publicProject.api}/pages/${publicProject.guestPage.id}`, { version: publicProject.guestPage.version, name: "E02 removed project page owner" });
      const current = await data(guest.page, "GET", `${publicProject.api}/issues/${publicProject.guestItem.id}`);
      expect((await data(guest.page, "PATCH", `${publicProject.api}/issues/${publicProject.guestItem.id}`, { version: current.version, priority: "medium" })).version).toBe(current.version + 1);

      await data(admin, "DELETE", `${workspaceAPI}/members/${guest.membership}`, undefined, 204);
      for (const path of ["issues", "issues?group_by=priority", "search?q=E02", "analytics", "notifications", "notifications/unread-count", "exports"]) await denied(guest.page, "GET", `${workspaceAPI}/${path}`);
      await denied(guest.page, "GET", `${publicProject.api}/issues/${publicProject.guestItem.id}`);
      await denied(guest.page, "PATCH", `${publicProject.api}/issues/${publicProject.guestItem.id}`, { version: current.version + 1, priority: "urgent" });
      await denied(guest.page, "GET", `${publicProject.api}/pages/${publicProject.guestPage.id}/content`);
      expect(ids(await data(admin, "GET", `${workspaceAPI}/issues`))).toEqual(ids(allItems));
      expect(errors).toEqual([]);
    });
  } finally {
    if (workspaceAPI) {
      for (const project of projects) await request(admin, "DELETE", project.api);
      await request(admin, "DELETE", workspaceAPI);
    }
    for (const actor of actors) {
      await request(actor.page, "POST", "/api/v1/auth/logout").catch(() => undefined);
      await actor.context.close();
    }
  }
});
