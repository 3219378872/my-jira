import { describe, expect, it } from "vitest";
import { getSchema } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { TableKit } from "@tiptap/extension-table";
import { originalEditorBlocks } from "@myjira/editor-schema";
import { EditorState } from "@tiptap/pm/state";
import { moveTopLevelBlock } from "./editor-blocks";

const schema = getSchema([StarterKit, TableKit, ...originalEditorBlocks]);
const paragraph = (text: string) => ({
  type: "paragraph",
  content: [{ type: "text", text }],
});
const state = (content: unknown[]) =>
  EditorState.create({
    schema,
    doc: schema.nodeFromJSON({ type: "doc", content }),
  });

describe("whole document block movement", () => {
  it("keeps nested table and callout content intact and reverses as one transaction", () => {
    const callout = {
      type: "callout",
      attrs: { tone: "warning" },
      content: [
        paragraph("Keep the whole block"),
        {
          type: "table",
          content: [
            {
              type: "tableRow",
              content: [
                {
                  type: "tableCell",
                  content: [paragraph("nested table cell")],
                },
              ],
            },
          ],
        },
      ],
    };
    const before = state([paragraph("First"), callout, paragraph("Last")]);
    const source = before.doc.child(0).nodeSize;
    const move = moveTopLevelBlock(before, source, before.doc.content.size)!;
    expect(move.doc.child(2).toJSON()).toEqual(before.doc.child(1).toJSON());
    expect(move.doc.textContent).toBe(
      "FirstLastKeep the whole blocknested table cell",
    );
    let restored = move.doc;
    for (let index = move.steps.length - 1; index >= 0; index--)
      restored = move.steps[index]
        .invert(move.docs[index])
        .apply(restored).doc!;
    expect(restored.eq(before.doc)).toBe(true);
  });

  it("moves an atomic work-item reference without changing its identity", () => {
    const reference = {
      type: "workItem",
      attrs: {
        workspaceId: "11111111-1111-1111-1111-111111111111",
        projectId: "22222222-2222-2222-2222-222222222222",
        issueId: "33333333-3333-3333-3333-333333333333",
      },
    };
    const before = state([paragraph("Introduction"), reference]);
    const move = moveTopLevelBlock(before, before.doc.child(0).nodeSize, 0)!;
    expect(move.doc.firstChild?.toJSON()).toEqual(reference);
    expect(move.doc.childCount).toBe(2);
  });

  it("rejects nested and self destinations without deleting text", () => {
    const before = state([paragraph("First block"), paragraph("Second block")]);
    expect(moveTopLevelBlock(before, 1, before.doc.content.size)).toBeNull();
    expect(moveTopLevelBlock(before, 0, 2)).toBeNull();
    expect(
      moveTopLevelBlock(before, 0, before.doc.firstChild!.nodeSize),
    ).toBeNull();
    expect(moveTopLevelBlock(before, 0, 0)).toBeNull();
    expect(before.doc.textContent).toBe("First blockSecond block");
  });
});
