import { describe, expect, it } from "vitest";
import { issueComparator, positionForDrop } from "./ordering";
import type { WorkItem } from "../../types";

describe("drag ordering", () => {
  const items = [
    { id: "a", position: 100 },
    { id: "b", position: 200 },
    { id: "c", position: 300 },
  ];
  it("moves a card between its neighbors without including its original position", () => {
    expect(positionForDrop(items, "a", "b", "after")).toBe(250);
    expect(positionForDrop(items, "c", "b", "before")).toBe(150);
  });
  it("supports empty columns and positions at either end", () => {
    expect(positionForDrop([], "a")).toBe(1024);
    expect(positionForDrop(items, "c", "a", "before")).toBeLessThan(100);
    expect(positionForDrop(items, "a")).toBeGreaterThan(300);
  });
});

describe("work item ordering", () => {
  const item = (patch: Partial<WorkItem>) =>
    ({
      id: "item",
      priority: "none",
      position: 1,
      sequence_id: 1,
      created_at: "2026-09-01",
      updated_at: "2026-09-01",
      target_date: null,
      ...patch,
    }) as WorkItem;
  it("ranks urgent work before high priority and leaves unscheduled work last", () => {
    const urgent = item({ priority: "urgent", target_date: "2026-09-09" });
    const high = item({ priority: "high" });
    expect([high, urgent].sort(issueComparator("priority"))[0]).toBe(urgent);
    expect([high, urgent].sort(issueComparator("target_date"))[1]).toBe(high);
  });
  it("updates the manual ordering immediately after an optimistic position change", () => {
    const first = item({ position: 200, sequence_id: 1 });
    const second = item({ position: 100, sequence_id: 2 });
    expect([first, second].sort(issueComparator("position"))).toEqual([
      second,
      first,
    ]);
  });
});
