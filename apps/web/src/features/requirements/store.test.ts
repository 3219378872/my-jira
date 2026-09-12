import { afterEach, describe, expect, it, vi } from "vitest";
import { APIError } from "../../lib/api";
import type { APIResponse, WorkItem } from "../../types";
import { RequirementsStore } from "./store";
import type { RequirementsSnapshot } from "./types";

const issue = (id = "story", version = 1): WorkItem => ({
  id,
  name: `Story ${version}`,
  version,
  project_id: "project",
  workspace_id: "workspace",
  requirement_type: "story",
  parent_id: null,
  cycle_id: "sprint-one",
  assignee_ids: [],
  label_ids: [],
  module_ids: [],
  state_id: "todo",
  priority: "none",
  archived_at: null,
  is_draft: false,
  sequence_id: 1,
  description_html: "",
  estimate: null,
  start_date: null,
  target_date: null,
  position: 1,
  created_by: "member",
  created_at: "2026-09-12",
  updated_at: "2026-09-12",
});
const snapshot = (
  revision: number,
  items = [issue()],
): RequirementsSnapshot => ({
  revision,
  items,
  activities: [],
  cycles: [],
  dependencies: [],
  permissions: { can_edit: true, can_admin: true },
});
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (cause: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
const stores: RequirementsStore[] = [];
function fixture(
  main: (
    path: string,
  ) => Promise<APIResponse<RequirementsSnapshot>> = async () => ({
    data: snapshot(1),
  }),
) {
  const get = vi.fn(async (path: string) =>
    path.endsWith("/requirements/snapshot")
      ? main(path)
      : {
          data: path.endsWith("/resources")
            ? { timezone: "UTC", members: [] }
            : [],
        },
  );
  const post = vi.fn(async () => ({ data: {} }));
  const close = vi.fn();
  const urls: string[] = [];
  const factory = (url: string) => {
    urls.push(url);
    return {
      close,
      addEventListener: vi.fn(),
      onopen: null,
      onerror: null,
    } as unknown as EventSource;
  };
  const store = new RequirementsStore(
    {
      get,
      post,
      patch: post,
      put: post,
      delete: post,
    } as unknown as ConstructorParameters<typeof RequirementsStore>[0],
    factory,
  );
  stores.push(store);
  return { store, get, post, close, urls };
}
afterEach(() => {
  for (const store of stores.splice(0)) store.leave();
  vi.restoreAllMocks();
});

describe("requirements snapshot and permission boundaries", () => {
  it("clears a disabled bundle, keeps only its metadata cursor, and reloads automatically after re-enabling", async () => {
    let enabled = true,
      revision = 19;
    const { store, get, urls, post } = fixture(async () => {
      if (!enabled)
        throw new APIError(
          "Requirements views are disabled for this project",
          403,
          "requirements_disabled",
        );
      return { data: snapshot(revision) };
    });
    await store.enter("workspace", "project");
    store.select("story");
    enabled = false;
    revision = 20;
    store.handleEvent({ revision, reconnect: true }, "authorization_changed");
    expect(store.snapshot.items).toEqual([]);
    expect(store.selectedID).toBe("");
    await vi.waitFor(() => expect(store.disabled).toBe(true));
    expect(store.revoked).toBe(false);
    expect(store.canEdit).toBe(false);
    expect(store.resources).toBeNull();
    expect(urls.at(-1)).toContain("cursor=20");
    const reads = get.mock.calls.length;
    store.handleEvent({ revision: 21 });
    await store.reloadAuxiliary();
    expect(get.mock.calls).toHaveLength(reads);
    expect(await store.create({ name: "Disabled create" })).toBe(false);
    expect(post).not.toHaveBeenCalled();
    enabled = true;
    revision = 22;
    store.handleEvent({ revision, reconnect: true }, "authorization_changed");
    await vi.waitFor(() => expect(store.snapshot.revision).toBe(22));
    expect(store.disabled).toBe(false);
    expect(store.snapshotLoaded).toBe(true);
    expect(store.canEdit).toBe(true);
    expect(store.items.get("story")).toBeDefined();
    expect(store.selectedID).toBe("");
  });
  it("starts an already disabled project without restoring cached entities and advances an expired metadata cursor", async () => {
    const { store, urls } = fixture(async () => {
      throw new APIError("Disabled", 403, "requirements_disabled");
    });
    await store.enter("workspace", "project");
    expect(store.disabled).toBe(true);
    expect(store.snapshotLoaded).toBe(false);
    expect(store.snapshot.items).toEqual([]);
    store.handleEvent({ revision: 90 }, "cursor_expired");
    await vi.waitFor(() => expect(urls.at(-1)).toContain("cursor=90"));
    expect(store.disabled).toBe(true);
    expect(store.revoked).toBe(false);
  });
  it("ignores an older response even when the transport does not honor cancellation", async () => {
    const old = deferred<APIResponse<RequirementsSnapshot>>(),
      fresh = deferred<APIResponse<RequirementsSnapshot>>();
    let calls = 0;
    const { store } = fixture(() =>
      ++calls === 1 ? old.promise : fresh.promise,
    );
    const entering = store.enter("workspace", "project");
    const reload = store.reload();
    fresh.resolve({ data: snapshot(8, [issue("story", 8)]) });
    await reload;
    old.resolve({ data: snapshot(3, [issue("story", 3)]) });
    await entering;
    expect(store.snapshot.revision).toBe(8);
    expect(store.selected).toBeUndefined();
    expect(store.items.get("story")?.version).toBe(8);
  });
  it("removes deleted and archived selections from an authoritative snapshot", async () => {
    let current = snapshot(4, [
      issue("deleted"),
      issue("archived"),
      issue("retained"),
    ]);
    const { store } = fixture(async () => ({ data: current }));
    await store.enter("workspace", "project");
    store.select("deleted");
    store.toggleSelected("deleted");
    store.toggleSelected("archived");
    current = snapshot(5, [
      { ...issue("archived"), archived_at: "2026-09-12" },
      issue("retained"),
    ]);
    await store.reload();
    expect(store.selectedID).toBe("");
    expect(store.selectedIDs.size).toBe(0);
    expect(store.visibleItems.map((item) => item.id)).toEqual(["retained"]);
  });
  it("purges all caches immediately and rejects an auxiliary response arriving after revocation", async () => {
    const scenarios = deferred<APIResponse<unknown>>();
    const { store, get } = fixture();
    get.mockImplementation(async (path) =>
      path.endsWith("/requirements/snapshot")
        ? { data: snapshot(2) }
        : path.endsWith("/scenarios")
          ? (scenarios.promise as never)
          : { data: [] },
    );
    const entering = store.enter("workspace", "project");
    await vi.waitFor(() => expect(store.snapshot.revision).toBe(2));
    store.select("story");
    store.setFilter("query", "private");
    store.handleEvent({}, "authorization_changed");
    expect(store.snapshot.items).toEqual([]);
    expect(store.selectedID).toBe("");
    expect(store.filters.query).toBe("");
    expect(store.canEdit).toBe(false);
    scenarios.resolve({ data: [{ id: "private-scenario" }] });
    await entering;
    expect(store.revoked).toBe(true);
    expect(store.scenarios).toEqual([]);
    expect(store.members).toEqual([]);
    expect(store.states).toEqual([]);
    expect(store.resources).toBeNull();
  });
  it("reauthorizes from an empty cache when the event allows reconnect and applies the fresh role", async () => {
    const fresh = deferred<APIResponse<RequirementsSnapshot>>();
    let calls = 0;
    const { store } = fixture(async () =>
      ++calls === 1 ? { data: snapshot(2) } : fresh.promise,
    );
    await store.enter("workspace", "project");
    store.select("story");
    store.handleEvent(
      { reconnect: true, revision: 3 },
      "authorization_changed",
    );
    expect(store.snapshot.items).toEqual([]);
    expect(store.selectedID).toBe("");
    expect(store.canAdmin).toBe(false);
    fresh.resolve({
      data: {
        ...snapshot(3),
        permissions: { can_edit: false, can_admin: false },
      },
    });
    await vi.waitFor(() => expect(store.snapshot.revision).toBe(3));
    expect(store.canEdit).toBe(false);
    expect(store.canAdmin).toBe(false);
    expect(store.revoked).toBe(false);
  });
  it("never commits a mutation response into another project scope", async () => {
    const pending = deferred<APIResponse<unknown>>();
    const { store, post } = fixture();
    await store.enter("workspace", "project");
    post.mockImplementationOnce(() => pending.promise as never);
    const save = store.update(store.items.get("story")!, {
      name: "Private edit",
    });
    await store.enter("workspace", "other-project");
    pending.resolve({ data: {} });
    expect(await save).toBe(false);
    expect(store.projectID).toBe("other-project");
    expect(store.busy).toBe(false);
    expect(store.items.get("story")?.name).toBe("Story 1");
  });
  it("preserves common filters when returning to the same scope and clears them on revocation", async () => {
    const { store } = fixture();
    await store.enter("workspace", "project");
    store.setFilter("cycle", "sprint-one");
    store.select("story");
    store.leave();
    await store.enter("workspace", "project");
    expect(store.filters.cycle).toBe("sprint-one");
    expect(store.selectedID).toBe("story");
    store.revoke();
    await store.enter("workspace", "project");
    expect(store.filters.cycle).toBe("");
    expect(store.selectedID).toBe("");
  });
  it("does not finish an old save successfully when navigation occurs during its snapshot refresh", async () => {
    const pending = deferred<APIResponse<RequirementsSnapshot>>();
    let calls = 0;
    const { store } = fixture(async () =>
      ++calls === 2 ? pending.promise : { data: snapshot(calls) },
    );
    await store.enter("workspace", "project");
    const saving = store.update(store.items.get("story")!, {
      name: "Saved in original scope",
    });
    await vi.waitFor(() => expect(calls).toBe(2));
    await store.enter("workspace", "new-project");
    pending.resolve({ data: snapshot(2) });
    expect(await saving).toBe(false);
    expect(store.projectID).toBe("new-project");
    expect(store.busy).toBe(false);
    expect(store.snapshot.revision).toBe(3);
  });
});

describe("requirements cursor recovery and writes", () => {
  it("reuses the exact changeset after an uncertain network failure, even if the live revision advances", async () => {
    let current = snapshot(2);
    const { store, post } = fixture(async () => ({ data: current }));
    await store.enter("workspace", "project");
    post.mockRejectedValueOnce(
      new APIError("Response lost", 0, "network_error"),
    );
    expect(
      await store.create({ name: "Create once", requirement_type: "story" }),
    ).toBe(false);
    const original = post.mock.calls[0];
    current = snapshot(3);
    await store.reload();
    expect(
      await store.create({ name: "Create once", requirement_type: "story" }),
    ).toBe(true);
    expect(post.mock.calls[1]).toEqual(original);
    expect(
      await store.create({ name: "Create once", requirement_type: "story" }),
    ).toBe(true);
    expect(post.mock.calls[2]).not.toEqual(original);
  });
  it("deduplicates events and catches a revision arriving during refresh", async () => {
    const first = deferred<APIResponse<RequirementsSnapshot>>();
    let call = 0;
    const { store, get } = fixture(async () =>
      ++call === 1
        ? { data: snapshot(10) }
        : call === 2
          ? first.promise
          : { data: snapshot(12) },
    );
    await store.enter("workspace", "project");
    store.handleEvent({ revision: 9 });
    store.handleEvent({ revision: 11 });
    store.handleEvent({ revision: 11 });
    store.handleEvent({ revision: 12 });
    first.resolve({ data: snapshot(11) });
    await vi.waitFor(() => expect(store.snapshot.revision).toBe(12));
    expect(
      get.mock.calls.filter(([path]) =>
        path.endsWith("/requirements/snapshot"),
      ),
    ).toHaveLength(3);
  });
  it("replaces expired cursors with a new snapshot before subscribing", async () => {
    let revision = 10;
    const { store, urls, close } = fixture(async () => ({
      data: snapshot(revision),
    }));
    await store.enter("workspace", "project");
    revision = 44;
    store.handleEvent({}, "cursor_expired");
    await vi.waitFor(() => expect(urls.at(-1)).toContain("cursor=44"));
    expect(close).toHaveBeenCalled();
    expect(store.snapshot.revision).toBe(44);
  });
  it("allows the same change to be retried after a failed catch-up", async () => {
    let call = 0;
    const { store } = fixture(async () => {
      if (++call === 2) throw new APIError("Disconnected", 0, "network_error");
      return { data: snapshot(call === 1 ? 10 : 11) };
    });
    await store.enter("workspace", "project");
    store.handleEvent({ revision: 11 });
    await vi.waitFor(() => expect(store.error).toBe("Disconnected"));
    store.handleEvent({ revision: 11 });
    await vi.waitFor(() => expect(store.snapshot.revision).toBe(11));
  });
  it("submits one batch with observed entity and snapshot versions and reloads a conflict without claiming success", async () => {
    let current = snapshot(8, [issue("story", 4)]);
    const { store, post } = fixture(async () => ({ data: current }));
    await store.enter("workspace", "project");
    post.mockImplementationOnce(async () => {
      current = snapshot(9, [issue("story", 5)]);
      throw new APIError("Changed elsewhere", 409, "conflict");
    });
    expect(
      await store.update(store.items.get("story")!, { cycle_id: "sprint-two" }),
    ).toBe(false);
    expect(post).toHaveBeenCalledWith(
      "/workspaces/workspace/projects/project/planning/changesets",
      expect.objectContaining({
        expected_revision: 8,
        idempotency_key: expect.any(String),
        commands: [
          {
            operation: "update",
            id: "story",
            version: 4,
            fields: { cycle_id: "sprint-two" },
          },
        ],
      }),
    );
    expect(store.items.get("story")?.version).toBe(5);
    expect(store.error).toBe("Changed elsewhere");
  });
});
