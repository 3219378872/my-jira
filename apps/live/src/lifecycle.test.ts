import { test } from "node:test";
import assert from "node:assert/strict";
import { Document, Hocuspocus } from "@hocuspocus/server";
import { beforeUnloadDocument } from "./lifecycle.js";

test("late cleanup of a restored document preserves the new live instance and its state", async () => {
  let unloads = 0;
  const server = new Hocuspocus({
    quiet: true,
    beforeUnloadDocument,
    async afterUnloadDocument() { unloads++; },
  });
  const original = new Document("restored-page");
  const replacement = new Document("restored-page");
  try {
    server.documents.set(original.name, original);
    await server.unloadDocument(original);
    assert.equal(original.isDestroyed, true);
    assert.equal(unloads, 1);

    replacement.getText("body").insert(0, "New edit after history restore");
    server.documents.set(replacement.name, replacement);
    await server.unloadDocument(original);
    assert.equal(server.documents.get(replacement.name), replacement);
    assert.equal(replacement.isDestroyed, false);
    assert.equal(replacement.getText("body").toString(), "New edit after history restore");
    assert.equal(unloads, 1, "stale cleanup must not discard the new persisted snapshot");

    await server.unloadDocument(replacement);
    assert.equal(server.documents.size, 0);
    assert.equal(replacement.isDestroyed, true);
    assert.equal(unloads, 2);
  } finally {
    original.destroy();
    replacement.destroy();
  }
});
