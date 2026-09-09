import type { beforeUnloadDocumentPayload } from "@hocuspocus/server";

export async function beforeUnloadDocument({ instance, documentName, document }: beforeUnloadDocumentPayload): Promise<void> {
  // Hocuspocus deletes by name after this hook. A delayed cleanup for an old
  // instance must never remove the replacement's connections or cached state.
  if (instance.documents.get(documentName) !== document) {
    throw new Error("Ignoring cleanup for a document instance that was already unloaded");
  }
}
