import { test } from "node:test";
import assert from "node:assert/strict";
import { TiptapTransformer } from "@hocuspocus/transformer";
import { generateHTML, generateJSON } from "@tiptap/html/server";
import * as Y from "yjs";
import { extensions } from "./schema.js";

test("original editor content survives HTML, JSON and Yjs persistence", () => {
  const source = `<h2>Shared decisions</h2><aside data-callout="warning"><p>Check <span data-mention-id="member" data-mention-label="Team">@Team</span> ✨</p></aside><details data-disclosure-title="Context"><summary>Context</summary><div><p>Original context.</p></div></details><table><tbody><tr><td><p>Reliable</p></td></tr></tbody></table><img src="/api/v1/auth/assets/asset/download" alt="Original attachment">`;
  const json = generateJSON(source, extensions);
  const document = TiptapTransformer.toYdoc(json, "default", extensions);
  const restored = new Y.Doc(); Y.applyUpdate(restored, Y.encodeStateAsUpdate(document));
  const roundtrip = TiptapTransformer.fromYdoc(restored, "default");
  const html = generateHTML(roundtrip, extensions);
  assert.deepEqual(generateJSON(html, extensions), json);
  for (const fragment of ["Shared decisions",'data-callout="warning"','data-mention-id="member"',"✨",'data-disclosure-title="Context"',"Reliable",'alt="Original attachment"']) assert.ok(html.includes(fragment),fragment);
  document.destroy();restored.destroy();
});
