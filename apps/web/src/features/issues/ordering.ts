import type { WorkItem } from "../../types";

export function issueComparator(
  order: string,
): (a: WorkItem, b: WorkItem) => number {
  const fields = order
    .split(",")
    .slice(0, 4)
    .map((field) => ({
      field: field.replace(/^-/, ""),
      descending: field.startsWith("-"),
    }));
  const priorities = { urgent: 0, high: 1, medium: 2, low: 3, none: 4 };
  return (a, b) => {
    for (const { field, descending } of fields) {
      let value = 0;
      if (field === "priority")
        value = priorities[a.priority] - priorities[b.priority];
      else if (field === "target_date" || field === "start_date") {
        if (!a[field] && b[field]) return 1;
        if (a[field] && !b[field]) return -1;
        value = (a[field] ?? "").localeCompare(b[field] ?? "");
      } else if (
        field === "created_at" ||
        field === "updated_at" ||
        field === "name"
      )
        value = a[field].localeCompare(b[field]);
      else if (field === "sequence_id") value = a.sequence_id - b.sequence_id;
      else if (field === "estimate") {
        if (a.estimate === null && b.estimate !== null) return 1;
        if (a.estimate !== null && b.estimate === null) return -1;
        value = (a.estimate ?? 0) - (b.estimate ?? 0);
      } else value = a.position - b.position;
      if (value) return descending ? -value : value;
    }
    return a.sequence_id - b.sequence_id;
  };
}

export function positionForDrop(
  items: Pick<WorkItem, "id" | "position">[],
  movingId: string,
  targetId?: string,
  edge: "before" | "after" = "after",
): number {
  const remaining = items
    .filter((item) => item.id !== movingId)
    .sort((a, b) => a.position - b.position);
  const found = targetId
    ? remaining.findIndex((item) => item.id === targetId)
    : -1;
  const index =
    found === -1 ? remaining.length : found + (edge === "after" ? 1 : 0);
  const previous = remaining[index - 1]?.position;
  const next = remaining[index]?.position;
  if (previous === undefined && next === undefined) return 1024;
  if (previous === undefined) return next! - 1024;
  if (next === undefined) return previous + 1024;
  return previous + (next - previous) / 2;
}
