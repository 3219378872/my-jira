import { afterEach, describe, expect, it, vi } from "vitest";
import { api, APIError } from "../lib/api";
import type { WorkItem } from "../types";

vi.stubGlobal("localStorage", { getItem: () => null, setItem: () => {} });
const { AppStore } = await import("./app-store");

const item = {
  id: "issue",
  workspace_id: "workspace",
  project_id: "project",
  name: "Original title",
  version: 4,
  state_id: "todo",
  priority: "none",
  position: 100,
} as WorkItem;
afterEach(() => vi.restoreAllMocks());

describe("optimistic work item writes", () => {
  it("rolls back failed writes and sends the observed version", async () => {
    const store = new AppStore();
    store.issues.set(item.id, item);
    const update = vi
      .spyOn(api, "patch")
      .mockRejectedValue(new APIError("Forbidden", 403, "forbidden"));
    await expect(
      store.updateIssue(item.id, { name: "Unsaved title" }),
    ).rejects.toThrow("Forbidden");
    expect(store.issues.get(item.id)?.name).toBe("Original title");
    expect(update).toHaveBeenCalledWith(
      "/workspaces/workspace/projects/project/issues/issue",
      { name: "Unsaved title", version: 4 },
    );
  });
  it("reloads the server version after a conflict without marking the failed edit as saved", async () => {
    const store = new AppStore();
    store.issues.set(item.id, item);
    vi.spyOn(api, "patch").mockRejectedValue(
      new APIError("Changed elsewhere", 409, "conflict"),
    );
    vi.spyOn(api, "get").mockResolvedValue({
      data: { ...item, name: "A teammate's title", version: 5 },
    });
    await expect(
      store.updateIssue(item.id, { name: "Conflicting edit" }),
    ).rejects.toThrow("Changed elsewhere");
    expect(store.issues.get(item.id)?.name).toBe("A teammate's title");
    expect(store.issues.get(item.id)?.version).toBe(5);
  });
});

describe("cross-project work item moves", () => {
  it("changes project caches only after one successful move and preserves them on rejection", async () => {
    const store = new AppStore();
    store.issues.set(item.id, item);
    store.projectIssueIds.set("project", [item.id]);
    store.projectIssueIds.set("target", ["other-item"]);
    let rejectMove!: (error: Error) => void;
    const mutation = vi.spyOn(api, "post").mockImplementationOnce(
      () =>
        new Promise((_resolve, reject) => {
          rejectMove = reject;
        }),
    );
    const patch = vi.spyOn(api, "patch");
    const changes = {
      priority: "high" as const,
      label_ids: ["target-label"],
      position: 2048,
    };
    const moving = store.moveIssue(item.id, "target", "target-state", changes);
    expect(store.movingIssueIds.has(item.id)).toBe(true);
    expect(store.issues.get(item.id)?.project_id).toBe("project");
    expect(store.projectIssueIds.get("project")).toEqual([item.id]);
    rejectMove(new APIError("Move denied", 403, "forbidden"));
    await expect(moving).rejects.toThrow("Move denied");
    expect(store.projectIssueIds.get("project")).toEqual([item.id]);
    expect(store.projectIssueIds.get("target")).toEqual(["other-item"]);
    expect(store.movingIssueIds.has(item.id)).toBe(false);

    mutation.mockResolvedValueOnce({
      data: {
        ...item,
        ...changes,
        project_id: "target",
        state_id: "target-state",
        version: 5,
      },
    });
    await store.moveIssue(item.id, "target", "target-state", changes);
    expect(mutation).toHaveBeenLastCalledWith(
      "/workspaces/workspace/projects/project/issues/issue/move",
      { project_id: "target", state_id: "target-state", version: 4, changes },
    );
    expect(patch).not.toHaveBeenCalled();
    expect(store.projectIssues("project")).toEqual([]);
    expect(store.projectIssueIds.get("target")).toEqual([
      "other-item",
      item.id,
    ]);
    expect(store.issues.get(item.id)).toMatchObject({
      project_id: "target",
      label_ids: ["target-label"],
      version: 5,
    });
  });
});
