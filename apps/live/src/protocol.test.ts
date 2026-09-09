import { test } from "node:test";
import assert from "node:assert/strict";
import { csrfFromCookie, documentPath } from "./protocol.js";

test("document paths reject traversal and keep workspace/project scope", () => {
  const w = "a0618b84-dd9f-40b7-bb14-eaeb65cc173b";
  const p = "c064782d-e761-4aa3-99f2-4bd7c1c8d04e";
  const page = "2c723957-8480-4f14-9cbb-c57c23689612";
  assert.equal(documentPath(`${w}:${p}:${page}`), `/api/v1/workspaces/${w}/projects/${p}/pages/${page}`);
  assert.equal(documentPath(`${w}:workspace:${page}`), `/api/v1/workspaces/${w}/pages/${page}`);
  for (const value of ["../admin", `${w}:../:${page}`, `${w}:${p}:${page}:extra`, `${w}::${page}`]) assert.throws(() => documentPath(value));
});

test("CSRF cookie parsing does not accept partial names or malformed escapes", () => {
  assert.equal(csrfFromCookie("mj_session=secret; mj_csrf=abc-123; other=1"), "abc-123");
  assert.equal(csrfFromCookie("not_mj_csrf=abc"), null);
  assert.equal(csrfFromCookie("mj_csrf=%xx"), null);
});
