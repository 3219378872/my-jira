import { describe, expect, it } from "vitest";
import type { WorkItem } from "../../types";
import {
  blankFilters,
  commitmentCycle,
  derivedRange,
  descendantIDs,
  epicOf,
  hours,
  matchesFilters,
  parseHours,
  shiftDate,
  splitMinutes,
} from "./semantics";

const item = (
  id: string,
  parent_id: string | null,
  requirement_type: WorkItem["requirement_type"],
  extra: Partial<WorkItem> = {},
) =>
  ({
    id,
    parent_id,
    requirement_type,
    cycle_id: null,
    assignee_ids: [],
    name: id,
    sequence_id: 1,
    archived_at: null,
    is_draft: false,
    ...extra,
  }) as WorkItem;

describe("four-view business semantics", () => {
  it("filters task context by the nearest Story commitment without changing its execution Sprint", () => {
    const epic = item("epic", null, "epic"),
      story = item("story", "epic", "story", { cycle_id: "commitment" }),
      task = item("task", "story", "task", { cycle_id: "execution" });
    const items = new Map(
      [epic, story, task].map((entry) => [entry.id, entry]),
    );
    expect(epicOf(task, items)).toBe("epic");
    expect(commitmentCycle(task, items)).toBe("commitment");
    expect(
      matchesFilters(task, { ...blankFilters(), cycle: "commitment" }, items),
    ).toBe(true);
    expect(
      matchesFilters(task, { ...blankFilters(), cycle: "execution" }, items),
    ).toBe(false);
    expect(task.cycle_id).toBe("execution");
    items.set(story.id, { ...story, cycle_id: null });
    expect(
      matchesFilters(task, { ...blankFilters(), cycle: "backlog" }, items),
    ).toBe(true);
  });
  it("keeps old unclassified work items unclassified and excludes archives and drafts", () => {
    const old = item("Epic-looking title", null, null, {
      cycle_id: "legacy-cycle",
    });
    const items = new Map([[old.id, old]]);
    expect(epicOf(old, items)).toBeNull();
    expect(commitmentCycle(old, items)).toBe("legacy-cycle");
    expect(matchesFilters(old, blankFilters(), items)).toBe(true);
    expect(
      matchesFilters(
        { ...old, archived_at: "2026-09-12" },
        blankFilters(),
        items,
      ),
    ).toBe(false);
    expect(
      matchesFilters({ ...old, is_draft: true }, blankFilters(), items),
    ).toBe(false);
  });
  it("derives execution dates independently from the Story commitment and ignores archived children", () => {
    const story = item("story", null, "story", {
      start_date: "2026-09-01",
      target_date: "2026-09-05",
    });
    const tasks = [
      story,
      item("task-a", "story", "task", {
        start_date: "2026-09-02",
        target_date: "2026-09-04",
      }),
      item("task-b", "task-a", "task", {
        start_date: "2026-09-06",
        target_date: "2026-09-08",
      }),
      item("archive", "story", "task", {
        archived_at: "2026-01-01",
        start_date: "2025-01-01",
        target_date: "2028-01-01",
      }),
    ];
    expect(derivedRange(story, tasks)).toEqual({
      start: "2026-09-02",
      end: "2026-09-08",
    });
    expect(story.target_date).toBe("2026-09-05");
    expect(descendantIDs("story", tasks)).toEqual([
      "task-a",
      "archive",
      "task-b",
    ]);
  });
  it("uses exact day arithmetic across month and daylight-saving boundaries", () => {
    expect(shiftDate("2026-03-07", 2)).toBe("2026-03-09");
    expect(shiftDate("2026-03-01", -1)).toBe("2026-02-28");
    expect(shiftDate(null, 10)).toBeNull();
  });
  it("preserves every integer minute when displaying and saving hours", () => {
    for (let minutes = 0; minutes < 1440; minutes++)
      expect(parseHours(hours(minutes))).toBe(minutes);
    expect(parseHours("")).toBeNull();
    expect(hours(null)).toBe("?");
  });
  it("conserves shared effort using largest remainder and stable identity order", () => {
    expect(splitMinutes(5, ["c", "b", "a"])).toEqual([
      { member_id: "a", minutes: 2 },
      { member_id: "b", minutes: 2 },
      { member_id: "c", minutes: 1 },
    ]);
    expect(
      splitMinutes(
        8,
        ["c", "a", "b"],
        [
          { member_id: "a", weight: 1 },
          { member_id: "b", weight: 3 },
          { member_id: "c", weight: 2 },
        ],
      ),
    ).toEqual([
      { member_id: "a", minutes: 1 },
      { member_id: "b", minutes: 4 },
      { member_id: "c", minutes: 3 },
    ]);
    expect(splitMinutes(1, [])).toEqual([]);
    expect(
      splitMinutes(0, ["a", "b"]).reduce(
        (sum, entry) => sum + entry.minutes,
        0,
      ),
    ).toBe(0);
  });
});
