import { timingSafeEqual } from "node:crypto";
import type { IncomingMessage, ServerResponse } from "node:http";
import { generateHTML, generateJSON } from "@tiptap/html/server";
import { TiptapTransformer } from "@hocuspocus/transformer";
import { chromium, type Browser } from "playwright-core";
import * as Y from "yjs";
import { api, csrfFromCookie, documentPath, type PageContent, type SessionContext } from "./protocol.js";
import { extensions } from "./schema.js";

const maximumBody = 2 * 1024 * 1024;
let browser: Promise<Browser> | undefined;
let activePDF = 0;

function json(response: ServerResponse, status: number, data: unknown) {
  response.writeHead(status, { "Content-Type": "application/json", "Cache-Control": "no-store" });
  response.end(JSON.stringify(data));
}

async function readJSON(request: IncomingMessage): Promise<Record<string, unknown>> {
  const chunks: Buffer[] = []; let bytes = 0;
  for await (const value of request) {
    const chunk = Buffer.from(value); bytes += chunk.length;
    if (bytes > maximumBody) throw new Error("Request exceeds 2 MiB");
    chunks.push(chunk);
  }
  return JSON.parse(Buffer.concat(chunks).toString("utf8")) as Record<string, unknown>;
}

const escape = (value: string) => value.replace(/[&<>"']/g, (character) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[character]!);

export async function handleDocumentHTTP(request: IncomingMessage, response: ServerResponse, apiURL: string, origins: Set<string>): Promise<boolean> {
  const url = new URL(request.url ?? "/", "http://live.local");
  const match = /^\/documents\/([^/]+)\/pdf$/.exec(url.pathname);
  if (!match && url.pathname !== "/documents/convert") return false;
  if ((match && request.method !== "GET") || (!match && request.method !== "POST")) { json(response, 405, { error: { message: "Method not allowed" } }); return true; }
  const origin = request.headers.origin ?? [...origins][0] ?? "";
  if (!origins.has(origin)) { json(response, 403, { error: { message: "Origin is not permitted" } }); return true; }
  const cookie = request.headers.cookie ?? "";
  const csrfCookie = csrfFromCookie(cookie) ?? "";
  const csrf = request.headers["x-csrf-token"];
  if (!match && (typeof csrf !== "string" || !csrfCookie || csrf.length !== csrfCookie.length || !timingSafeEqual(Buffer.from(csrf), Buffer.from(csrfCookie)))) { json(response, 403, { error: { message: "Invalid CSRF token" } }); return true; }
  let page;
  try {
    const input = match ? {} : await readJSON(request);
    const name = match ? decodeURIComponent(match[1]!) : input.document_name;
    if (typeof name !== "string") throw new Error("Supply a document identifier");
    const context: SessionContext = { cookie, csrf: csrfCookie, origin, path: documentPath(name), user: { id: "", display_name: "" } };
    context.user = await api<SessionContext["user"]>(apiURL, context, "/api/v1/auth/me");
    const content = await api<PageContent>(apiURL, context, `${context.path}/content`);
    if (!match) {
      if (!content.can_edit || content.is_locked || content.archived_at) { json(response, 403, { error: { message: "This document cannot be edited" } }); return true; }
      if (typeof input.html !== "string" || input.html.length > maximumBody) throw new Error("Supply at most 2 MiB of HTML");
      const value = generateJSON(input.html, extensions);
      const document = TiptapTransformer.toYdoc(value, "default", extensions);
      const binary = Buffer.from(Y.encodeStateAsUpdate(document)).toString("base64"); document.destroy();
      json(response, 200, { data: { content_json: value, content_binary: binary, content_html: generateHTML(value, extensions) } }); return true;
    }
    if (activePDF >= 2) { json(response, 429, { error: { message: "Two documents are currently being exported. Try again shortly." } }); return true; }
    activePDF++;
    try {
      browser ??= chromium.launch({ executablePath: process.env.CHROMIUM_PATH || undefined, args: ["--no-sandbox", "--disable-dev-shm-usage"], headless: true }).catch((error) => { browser = undefined; throw error; });
      page = await (await browser).newPage({ javaScriptEnabled: false });
      // Only authenticated local assets and the configured stock-image CDN are
      // fetched. A document cannot use export to probe other internal services.
      await page.route("**/*", async (route) => {
        const target = new URL(route.request().url());
        if (target.origin === "http://document.local" && /^\/api\/v1\/(workspaces|auth)\//.test(target.pathname) && /\/assets\/[0-9a-f-]+\/download$/.test(target.pathname)) {
          try {
            const asset = await fetch(new URL(target.pathname + target.search, apiURL), { headers: { Cookie: cookie, Origin: origin }, redirect: "error", signal: AbortSignal.timeout(8000) });
            const type = asset.headers.get("content-type") ?? "";
            if (!asset.ok || !/^image\/(png|jpeg|webp|gif)$/.test(type) || Number(asset.headers.get("content-length") ?? 0) > 25 * 1024 * 1024) { await route.abort(); return; }
            await route.fulfill({ status: 200, contentType: type, body: Buffer.from(await asset.arrayBuffer()) });
          } catch { await route.abort(); }
        } else if (target.protocol === "https:" && target.hostname === "images.unsplash.com") { await route.continue(); }
        else { await route.abort(); }
      });
      const value = content.content_json?.type === "doc" ? content.content_json : generateJSON(content.content_html, extensions);
      const html = generateHTML(value, extensions);
      await page.setContent(`<!doctype html><html><head><meta charset="utf-8"><base href="http://document.local"><style>body{font:12px/1.7 sans-serif;color:#17202e}h1{font-size:26px}h2{font-size:20px}h3{font-size:16px}img{max-width:100%;max-height:600px}table{width:100%;border-collapse:collapse}th,td{border:1px solid #ddd;padding:7px}pre,aside{background:#f4f5f8;padding:14px;white-space:pre-wrap}blockquote{border-left:3px solid #9aa5c3;padding-left:14px}iframe{display:none}a{color:#5369cf}p,li{orphans:3;widows:3}</style></head><body><h1>${escape(content.name ?? "Document")}</h1>${html}</body></html>`, { waitUntil: "networkidle", timeout: 15_000 });
      const pdf = await page.pdf({ format: "A4", printBackground: true, margin: { top: "18mm", bottom: "18mm", left: "18mm", right: "18mm" } });
      // Access can change during rendering; validate once more before responding.
      await api<PageContent>(apiURL, context, `${context.path}/content`);
      response.writeHead(200, { "Content-Type": "application/pdf", "Content-Disposition": `attachment; filename="document.pdf"; filename*=UTF-8''${encodeURIComponent((content.name ?? "Document") + ".pdf")}`, "Cache-Control": "private, no-store", "X-Content-Type-Options": "nosniff" }); response.end(pdf);
    } finally { activePDF--; await page?.close(); }
  } catch (error) {
    const message = error instanceof Error ? error.message : "Document operation failed";
    const status = /API refused/.test(message) ? (message.includes("401") ? 401 : 403) : 400;
    if (!response.headersSent) json(response, status, { error: { message } });
  }
  return true;
}

export async function closePDFBrowser(): Promise<void> { await (await browser)?.close(); browser = undefined; }
