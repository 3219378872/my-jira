import { expect, type Page } from "@playwright/test";

export const email = "demo@myjira.local";
export const password = "MyJira-Local-2026!";
export const workspace = "93facd49-fcff-469e-814e-90fd667820c8";
export const project = "f785ccab-22ca-490a-b0c4-a57098258cfb";
export const projectRoute = `/w/studio/projects/${project}`;
export const projectAPI = `/api/v1/workspaces/${workspace}/projects/${project}`;

export async function login(page: Page) {
  await page.goto("/login");
  await page.locator("input[type=email]").fill(email);
  await page.locator("input[type=password]").fill(password);
  await page.locator("button[type=submit]").click();
  await page.waitForURL(/\/w\/studio\//);
}

export async function request(page: Page, method: string, path: string, body?: unknown) {
  return page.evaluate(async ({ method, path, body }) => {
    if (!["GET", "HEAD", "OPTIONS"].includes(method) && !document.cookie.includes("mj_csrf=")) {
      const csrf = await fetch("/api/v1/auth/csrf");
      if (!csrf.ok) throw new Error(`Could not establish CSRF: ${csrf.status}`);
    }
    const cookie = document.cookie.split("; ").find((entry) => entry.startsWith("mj_csrf="));
    const response = await fetch(path, { method, headers: { "Content-Type": "application/json", "X-CSRF-Token": cookie ? decodeURIComponent(cookie.slice(8)) : "" }, body: body === undefined ? undefined : JSON.stringify(body) });
    return { status: response.status, body: response.status === 204 ? null : await response.json() };
  }, { method, path, body });
}

export async function collaborator(page: Page) {
  await page.goto("/login");
  await expect(page.locator("input[type=email]")).toBeVisible();
  const email = "collaborator@myjira.local";
  let result = await request(page,"POST","/api/v1/auth/login",{email,password});
  if (result.status===401) result=await request(page,"POST","/api/v1/auth/register",{email,password,display_name:"协作演示成员"});
  expect([200,201]).toContain(result.status);
  return result.body.data.user as {id:string,email:string};
}
