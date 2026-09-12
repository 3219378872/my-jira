import type { WorkItem } from "../../types";
import type { RequirementFilters } from "./types";

export const blankFilters = (): RequirementFilters => ({
  epic: "",
  cycle: "",
  state: "",
  member: "",
  skill: "",
  query: "",
});
export const dayNumber = (date: string): number =>
  Math.floor(Date.parse(`${date.slice(0, 10)}T00:00:00Z`) / 86400000);
export const dateFromDay = (day: number): string =>
  new Date(day * 86400000).toISOString().slice(0, 10);
export const shiftDate = (
  date: string | null | undefined,
  days: number,
): string | null => (date ? dateFromDay(dayNumber(date) + days) : null);
export const hours = (minutes: number | null | undefined): string =>
  minutes == null ? "?" : Number((minutes / 60).toFixed(2)).toString();
export const parseHours = (value: string): number | null =>
  value.trim() === "" ? null : Math.round(Number(value) * 60);

export function epicOf(
  item: WorkItem,
  items: ReadonlyMap<string, WorkItem>,
): string | null {
  let current: WorkItem | undefined = item;
  const seen = new Set<string>();
  while (current && !seen.has(current.id)) {
    if (current.requirement_type === "epic") return current.id;
    seen.add(current.id);
    current = current.parent_id ? items.get(current.parent_id) : undefined;
  }
  return null;
}
export function matchesFilters(
  item: WorkItem,
  filters: RequirementFilters,
  items: ReadonlyMap<string, WorkItem>,
): boolean {
  return (
    !item.archived_at &&
    !item.is_draft &&
    (!filters.epic || epicOf(item, items) === filters.epic) &&
    (!filters.cycle ||
      (filters.cycle === "backlog"
        ? !commitmentCycle(item, items)
        : commitmentCycle(item, items) === filters.cycle)) &&
    (!filters.state || item.state_id === filters.state) &&
    (!filters.member || item.assignee_ids.includes(filters.member)) &&
    (!filters.skill || (item.required_skills ?? []).includes(filters.skill)) &&
    (!filters.query ||
      `${item.name} ${item.sequence_id} ${item.story_role ?? ""} ${item.story_goal ?? ""}`
        .toLocaleLowerCase()
        .includes(filters.query.toLocaleLowerCase()))
  );
}

export function commitmentCycle(
  item: WorkItem,
  items: ReadonlyMap<string, WorkItem>,
): string | null {
  let current: WorkItem | undefined = item;
  const seen = new Set<string>();
  while (current && !seen.has(current.id)) {
    if (current.requirement_type === "story") return current.cycle_id;
    seen.add(current.id);
    current = current.parent_id ? items.get(current.parent_id) : undefined;
  }
  return item.cycle_id;
}
export function storyOf(
  item: WorkItem,
  items: ReadonlyMap<string, WorkItem>,
): string | undefined {
  let current: WorkItem | undefined = item;
  const seen = new Set<string>();
  while (current && !seen.has(current.id)) {
    if (current.requirement_type === "story") return current.id;
    seen.add(current.id);
    current = current.parent_id ? items.get(current.parent_id) : undefined;
  }
  return undefined;
}
export function descendantIDs(
  parentID: string,
  items: readonly WorkItem[],
): string[] {
  const seen = new Set([parentID]);
  const result: string[] = [];
  for (let index = 0, queue = [parentID]; index < queue.length; index++) {
    for (const item of items) {
      if (item.parent_id === queue[index] && !seen.has(item.id)) {
        seen.add(item.id);
        result.push(item.id);
        queue.push(item.id);
      }
    }
  }
  return result;
}
export function derivedRange(
  item: WorkItem,
  items: readonly WorkItem[],
): { start: string; end: string } | null {
  const ids = new Set(descendantIDs(item.id, items));
  const children = items.filter(
    (entry) =>
      ids.has(entry.id) &&
      !entry.archived_at &&
      !entry.is_draft &&
      entry.start_date &&
      entry.target_date,
  );
  if (!children.length) return null;
  return {
    start: children.map((entry) => entry.start_date!).sort()[0],
    end: children
      .map((entry) => entry.target_date!)
      .sort()
      .at(-1)!,
  };
}

/** Integer apportionment keeps the task total intact. ID order breaks remainder ties. */
export function splitMinutes(
  total: number,
  assignees: string[],
  weights: { member_id: string; weight: number }[] = [],
): { member_id: string; minutes: number }[] {
  if (!assignees.length) return [];
  const members = [...new Set(assignees)].sort();
  const entries = members.map((member_id) => ({
    member_id,
    weight: Math.max(
      0,
      weights.find((weight) => weight.member_id === member_id)?.weight ?? 1,
    ),
  }));
  const sum = entries.reduce((value, entry) => value + entry.weight, 0);
  if (!sum) return splitMinutes(total, assignees);
  const result = entries.map((entry) => ({
    member_id: entry.member_id,
    minutes: Math.floor((total * entry.weight) / sum),
  }));
  let remainder =
    total - result.reduce((value, entry) => value + entry.minutes, 0);
  const fractions = entries
    .map((entry, index) => ({ index, fraction: (total * entry.weight) % sum }))
    .sort((a, b) => b.fraction - a.fraction || a.index - b.index);
  for (let index = 0; remainder > 0; index++, remainder--)
    result[fractions[index % result.length].index].minutes++;
  return result;
}
