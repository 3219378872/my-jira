import type { JSONContent } from "@tiptap/core";
import { createHmac } from "node:crypto";

const id = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function documentPath(name: string): string {
  const [workspace, project, page, extra] = name.split(":");
  if (extra !== undefined || !workspace || !page || !id.test(workspace) || !id.test(page) || (project !== "workspace" && !id.test(project ?? ""))) {
    throw new Error("Invalid document identifier");
  }
  const scope = project === "workspace" ? "" : `/projects/${project}`;
  return `/api/v1/workspaces/${workspace}${scope}/pages/${page}`;
}

export function csrfFromCookie(cookie: string): string | null {
  const entry = cookie.split(";").map((part) => part.trim()).find((part) => part.startsWith("mj_csrf="));
  if (!entry) return null;
  try { return decodeURIComponent(entry.slice("mj_csrf=".length)); } catch { return null; }
}

export type PageContent = {
  name: string;
  content_binary: string | null;
  content_json: JSONContent;
  content_html: string;
  version: number;
  is_locked: boolean;
  archived_at: string | null;
  can_edit: boolean;
};

export type SessionContext = {
  cookie: string;
  csrf: string;
  origin: string;
  path: string;
  user: { id: string; display_name: string; avatar_url?: string };
};

export async function api<T>(base: string, context: SessionContext, path: string, body?: unknown): Promise<T> {
  const content = body === undefined ? undefined : JSON.stringify(body);
  const headers: Record<string,string> = { Cookie: context.cookie, Origin: context.origin, "X-CSRF-Token": context.csrf, "Content-Type": "application/json" };
  if (content !== undefined) {
    const secret = process.env.LIVE_SERVICE_KEY || process.env.APP_ENCRYPTION_KEY || "";
    if (secret.length < 32) throw new Error("A collaboration service key of at least 32 characters is required to store document state");
    const timestamp = String(Math.floor(Date.now()/1000));
    headers["X-MyJira-Live-Time"] = timestamp;
    headers["X-MyJira-Live-Signature"] = createHmac("sha256",secret).update(`my-jira:live:v1\n${timestamp}\nPUT\n${new URL(path,base).pathname}\n`).update(content).digest("hex");
  }
  const response = await fetch(new URL(path, base), {
    method: body === undefined ? "GET" : "PUT",
    headers,
    body: content,
    signal: AbortSignal.timeout(10_000),
    redirect: "error",
  });
  if (!response.ok) throw new Error(`Document API refused the request (${response.status})`);
  const result = await response.json() as { data: T };
  return result.data;
}
