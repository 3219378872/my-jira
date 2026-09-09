import {
  NodeSelection,
  type EditorState,
  type Transaction,
} from "@tiptap/pm/state";

export function moveTopLevelBlock(
  state: EditorState,
  from: number,
  target: number,
): Transaction | null {
  const { doc } = state;
  if (
    from < 0 ||
    from >= doc.content.size ||
    target < 0 ||
    target > doc.content.size
  )
    return null;
  if (doc.resolve(from).depth !== 0 || doc.resolve(target).depth !== 0)
    return null;
  const node = doc.nodeAt(from);
  if (!node || !node.isBlock) return null;
  const end = from + node.nodeSize;
  if (target >= from && target <= end) return null;
  const insertion = target > from ? target - node.nodeSize : target;
  const transaction = state.tr.delete(from, end).insert(insertion, node);
  return transaction
    .setSelection(NodeSelection.create(transaction.doc, insertion))
    .scrollIntoView();
}
