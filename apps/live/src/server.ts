import { createHash, timingSafeEqual } from "node:crypto";
import { Server, type Document } from "@hocuspocus/server";
import { TiptapTransformer } from "@hocuspocus/transformer";
import { generateHTML } from "@tiptap/html/server";
import type { JSONContent } from "@tiptap/core";
import * as Y from "yjs";
import { api, csrfFromCookie, documentPath, type PageContent, type SessionContext } from "./protocol.js";
import { extensions } from "./schema.js";
import { closePDFBrowser, handleDocumentHTTP } from "./http.js";
import { beforeUnloadDocument } from "./lifecycle.js";

const apiURL = process.env.LIVE_API_URL ?? "http://127.0.0.1:8088";
const origins = new Set((process.env.ALLOWED_ORIGINS ?? "http://127.0.0.1:4173,http://localhost:4173").split(",").map((origin) => origin.trim()));
type Persisted = { binary: string | null; version: number; contentHash: string };
const persisted = new Map<string, Persisted>();
const invalidated = new Set<string>();

function stableJSON(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(stableJSON);
  if (value && typeof value === "object") return Object.fromEntries(Object.entries(value).sort(([left],[right])=>left.localeCompare(right)).map(([key,child])=>[key,stableJSON(child)]));
  return value;
}

function snapshot(page: PageContent): Persisted {
  return { binary: page.content_binary?.replace(/\s/g, "") || null, version: page.version, contentHash: createHash("sha256").update(page.content_html).update(JSON.stringify(stableJSON(page.content_json))).digest("hex") };
}

function replaced(page: PageContent, previous: Persisted): boolean {
  const current = snapshot(page);
  return page.version > previous.version && (current.binary !== previous.binary || current.contentHash !== previous.contentHash);
}

async function content(context: SessionContext): Promise<PageContent> {
  const page = await api<PageContent>(apiURL, context, `${context.path}/content`);
  // PostgreSQL base64 encoding may insert line breaks. Compare binary content
  // independently of that transport formatting before detecting replacement.
  page.content_binary = page.content_binary?.replace(/\s/g, "") ?? null;
  return page;
}

async function revalidate(document: Document): Promise<void> {
  await Promise.all(document.getConnections().map(async (connection) => {
    try {
      const page = await content(connection.context as SessionContext);
      connection.readOnly = !page.can_edit || page.is_locked || page.archived_at !== null;
      connection.sendStateless(JSON.stringify({ type: "permissions", can_edit: !connection.readOnly }));
      const previous = persisted.get(document.name);
      if (previous && page.version > previous.version) {
        if (replaced(page, previous)) invalidate(document);
        else document.broadcastStateless(JSON.stringify({ type: "metadata", version: page.version, name: page.name, is_locked: page.is_locked, archived_at: page.archived_at }));
      }
    } catch {
      connection.close({ code: 4403, reason: "Document access is no longer available" });
    }
  }));
}

function invalidate(document: Document): void {
  if (invalidated.has(document.name)) return;
  invalidated.add(document.name);
  document.broadcastStateless(JSON.stringify({ type: "reload_required", reason: "Document content was restored or replaced" }));
  // Closing the last connection lets Hocuspocus finish any pending store and
  // unload this instance. A second delayed unload can race with its replacement.
  server.hocuspocus.closeConnections(document.name);
}

const server = new Server<SessionContext>({
  port: Number(process.env.LIVE_PORT ?? 3101),
  address: process.env.LIVE_ADDR ?? "127.0.0.1",
  name: "my-jira",
  debounce: 800,
  maxDebounce: 3000,
  quiet: true,
  stopOnSignals: true,
  websocketOptions: { maxPayload: 2 * 1024 * 1024 },
  async onRequest({ request, response }) {
    if (request.url === "/healthz") {
      response.writeHead(200, { "Content-Type": "application/json" });
      response.end(JSON.stringify({ status: "ok", documents: server.hocuspocus.documents.size }));
      throw null;
    }
    if (await handleDocumentHTTP(request, response, apiURL, origins)) throw null;
  },
  async onAuthenticate({ documentName, requestHeaders, token, connectionConfig }) {
    // A replacement can cause clients to reconnect before the previous store
    // hook finishes unloading. Wait briefly for that owned document to unload.
    for (let attempt = 0; invalidated.has(documentName) && attempt < 20; attempt++) {
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
    if (invalidated.has(documentName)) throw new Error("Document is reloading. Reconnect shortly.");
    const origin = requestHeaders.get("origin") ?? "";
    if (!origins.has(origin)) throw new Error("Origin is not permitted");
    const cookie = requestHeaders.get("cookie") ?? "";
    const cookieCSRF = csrfFromCookie(cookie);
    let csrf: unknown;
    try { csrf = (JSON.parse(token) as { csrf_token: unknown }).csrf_token; } catch { throw new Error("Invalid authentication payload"); }
    if (typeof csrf !== "string" || !cookieCSRF || csrf.length !== cookieCSRF.length || !timingSafeEqual(Buffer.from(csrf), Buffer.from(cookieCSRF))) throw new Error("Invalid CSRF token");
    const context: SessionContext = { cookie, csrf, origin, path: documentPath(documentName), user: { id: "", display_name: "" } };
    context.user = await api<SessionContext["user"]>(apiURL, context, "/api/v1/auth/me");
    const page = await content(context);
    connectionConfig.readOnly = !page.can_edit || page.is_locked || page.archived_at !== null;
    return context;
  },
  async onLoadDocument({ documentName, context, document }) {
    const page = await content(context);
    if (page.content_binary) Y.applyUpdate(document, Buffer.from(page.content_binary, "base64"));
    else {
      const json = page.content_json?.type === "doc" ? page.content_json : { type: "doc", content: [{ type: "paragraph" }] };
      const initial = TiptapTransformer.toYdoc(json, "default", extensions);
      Y.applyUpdate(document, Y.encodeStateAsUpdate(initial));
      initial.destroy();
    }
    persisted.set(documentName, snapshot(page));
    return document;
  },
  async beforeSync({ document, context, connection, type }) {
    // Recheck every recipient before an edit can broadcast document data.
    await revalidate(document);
    if (invalidated.has(document.name)) throw new Error("Document was replaced; reload required");
    const page = await content(context);
    connection.readOnly = !page.can_edit || page.is_locked || page.archived_at !== null;
    if (type === 2 && connection.readOnly) throw new Error("This document is read-only");
  },
  async beforeHandleAwareness({ document, context, states }) {
    await revalidate(document);
    if (!context) return;
    await content(context);
    for (const [clientID, state] of states) {
      if (state) states.set(clientID, { ...state, user: { id: context.user.id, name: context.user.display_name, color: "#5b6fe8" } });
    }
  },
  async onStoreDocument({ documentName, document, lastContext }) {
    if (invalidated.has(documentName)) return;
    let remote: PageContent | undefined;
    let writer: SessionContext | undefined;
    let uncertainAccess: unknown;
    // An accepted edit can outlive its writer's session before the debounce
    // flush. Another currently authorized editor can persist the shared state.
    const candidates = [lastContext, ...document.getConnections().map((connection) => connection.context as SessionContext)];
    for (const candidate of candidates) {
      if (!candidate) continue;
      try {
        const page = await content(candidate);
        if (page.can_edit && !page.is_locked && page.archived_at === null) { remote = page; writer = candidate; break; }
      } catch (error) {
        if (!(error instanceof Error) || !/API refused the request \((401|403|404)\)/.test(error.message)) uncertainAccess = error;
      }
    }
    if (!remote || !writer) {
      if (uncertainAccess) throw uncertainAccess;
      invalidate(document);
      return;
    }
    const previous = persisted.get(documentName);
    if (previous && replaced(remote, previous)) {
      invalidate(document);
      return;
    }
    const json = TiptapTransformer.fromYdoc(document, "default") as JSONContent;
    const binary = Buffer.from(Y.encodeStateAsUpdate(document)).toString("base64");
    const updated = await api<PageContent>(apiURL, writer, `${writer.path}/content`, {
      content_binary: binary,
      content_json: json,
      content_html: generateHTML(json, extensions),
      version: remote.version,
    });
    persisted.set(documentName, snapshot(updated));
    document.broadcastStateless(JSON.stringify({ type: "saved", version: updated.version, name: updated.name }));
  },
  beforeUnloadDocument,
  async afterUnloadDocument({ documentName }) { persisted.delete(documentName); invalidated.delete(documentName); },
  async onDestroy() { clearInterval(accessCheck); await closePDFBrowser(); },
});

const accessCheck = setInterval(() => {
  for (const document of server.hocuspocus.documents.values()) void revalidate(document).catch(() => {});
}, 5000);
accessCheck.unref();
await server.listen();
console.info(`Collaboration server listening on ${server.httpURL}`);
