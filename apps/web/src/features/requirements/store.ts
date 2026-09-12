import { makeAutoObservable, runInAction } from "mobx";
import { api, APIError, errorMessage, projectPath } from "../../lib/api";
import type { Member, State, WorkItem } from "../../types";
import { blankFilters, matchesFilters } from "./semantics";
import type {
  PlanningCommand,
  RequirementFilters,
  RequirementsSnapshot,
  ResourceConfiguration,
  ResourceLoad,
  Scenario,
  UserActivity,
} from "./types";

type Transport = Pick<typeof api, "get" | "post" | "patch" | "put" | "delete">;
type StreamFactory = (url: string) => EventSource;

export class RequirementsStore {
  base = "";
  projectID = "";
  snapshot: RequirementsSnapshot = {
    revision: 0,
    items: [],
    activities: [],
    cycles: [],
    dependencies: [],
  };
  scenarios: Scenario[] = [];
  resources: ResourceConfiguration | null = null;
  load: ResourceLoad | null = null;
  states: State[] = [];
  members: Member[] = [];
  filters = blankFilters();
  selectedID = "";
  selectedIDs = new Set<string>();
  loading = false;
  busy = false;
  error = "";
  auxiliaryError = "";
  connected = false;
  revoked = false;
  disabled = false;
  snapshotLoaded = false;
  authorizationGeneration = 0;
  private epoch = 0;
  private requestSequence = 0;
  private auxiliarySequence = 0;
  private stream: EventSource | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private invalidation = 0;
  private metadataCursor = 0;
  private savedFilters = new Map<
    string,
    { filters: RequirementFilters; selectedID: string }
  >();
  private controller: AbortController | null = null;
  private refreshPromise: Promise<void> | null = null;
  private dateRange = { start: "", end: "" };
  private filterTimer: ReturnType<typeof setTimeout> | null = null;
  private pendingChanges = new Map<
    string,
    {
      idempotency_key: string;
      expected_revision: number;
      commands: PlanningCommand[];
    }
  >();

  constructor(
    private transport: Transport = api,
    private streamFactory: StreamFactory = (url) =>
      new EventSource(url, { withCredentials: true }),
  ) {
    makeAutoObservable(this, {}, { autoBind: true });
  }
  get items() {
    return new Map(this.snapshot.items.map((item) => [item.id, item]));
  }
  get visibleItems() {
    return this.snapshot.items.filter((item) =>
      matchesFilters(item, this.filters, this.items),
    );
  }
  get selected() {
    return this.items.get(this.selectedID);
  }
  get canEdit() {
    return this.snapshot.permissions?.can_edit ?? false;
  }
  get canAdmin() {
    return this.snapshot.permissions?.can_admin ?? false;
  }
  setFilter(field: keyof RequirementFilters, value: string) {
    this.filters = { ...this.filters, [field]: value };
    this.refreshFilteredLoad();
  }
  clearFilters() {
    this.filters = blankFilters();
    this.refreshFilteredLoad();
  }
  private refreshFilteredLoad() {
    if (this.filterTimer) clearTimeout(this.filterTimer);
    const epoch = this.epoch;
    this.filterTimer = setTimeout(() => {
      if (epoch === this.epoch) void this.reloadAuxiliary();
    }, 250);
  }
  select(id: string) {
    this.selectedID = id;
  }
  toggleSelected(id: string) {
    this.selectedIDs.has(id)
      ? this.selectedIDs.delete(id)
      : this.selectedIDs.add(id);
  }
  setError(message: string) {
    this.error = message;
  }

  async enter(workspaceID: string, projectID: string) {
    this.leave();
    this.base = projectPath(workspaceID, projectID);
    this.projectID = projectID;
    this.revoked = false;
    this.error = "";
    const saved = this.savedFilters.get(this.base);
    this.filters = saved?.filters ?? blankFilters();
    this.selectedID = saved?.selectedID ?? "";
    await this.reload();
    if (!this.revoked && this.base) this.connect();
  }
  leave() {
    if (this.base && !this.revoked)
      this.savedFilters.set(this.base, {
        filters: { ...this.filters },
        selectedID: this.selectedID,
      });
    this.epoch++;
    this.controller?.abort();
    this.stream?.close();
    this.stream = null;
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    if (this.filterTimer) clearTimeout(this.filterTimer);
    this.filterTimer = null;
    this.pendingChanges.clear();
    this.reconnectTimer = null;
    this.connected = false;
    this.busy = false;
    this.loading = false;
    this.base = "";
    this.projectID = "";
    this.refreshPromise = null;
    this.invalidation = 0;
    this.metadataCursor = 0;
    this.disabled = false;
    this.snapshotLoaded = false;
    this.snapshot = {
      revision: 0,
      items: [],
      activities: [],
      cycles: [],
      dependencies: [],
    };
    this.scenarios = [];
    this.resources = null;
    this.load = null;
    this.states = [];
    this.members = [];
    this.selectedIDs.clear();
    this.auxiliaryError = "";
  }
  revoke() {
    const oldBase = this.base;
    this.leave();
    this.savedFilters.delete(oldBase);
    this.selectedID = "";
    this.filters = blankFilters();
    this.revoked = true;
    this.authorizationGeneration++;
    this.loading = false;
    this.busy = false;
  }
  disable() {
    const base = this.base,
      projectID = this.projectID,
      cursor = Math.max(this.metadataCursor, this.snapshot.revision);
    this.revoke();
    this.base = base;
    this.projectID = projectID;
    this.metadataCursor = cursor;
    this.revoked = false;
    this.disabled = true;
    this.error = "";
    this.connect();
  }
  async reload() {
    if (!this.base || this.revoked) return;
    const epoch = this.epoch,
      sequence = ++this.requestSequence,
      base = this.base;
    this.controller?.abort();
    const controller = new AbortController();
    this.controller = controller;
    this.loading = true;
    try {
      const result = await this.transport.get<RequirementsSnapshot>(
        `${base}/requirements/snapshot`,
        controller.signal,
      );
      if (
        epoch !== this.epoch ||
        sequence !== this.requestSequence ||
        controller.signal.aborted
      )
        return;
      runInAction(() => {
        if (result.data.revision < this.snapshot.revision) return;
        this.snapshot = {
          ...result.data,
          items: result.data.items ?? [],
          activities: result.data.activities ?? [],
          cycles: result.data.cycles ?? [],
          dependencies: result.data.dependencies ?? [],
        };
        this.disabled = false;
        this.snapshotLoaded = true;
        this.metadataCursor = result.data.revision;
        this.error = "";
        const current = new Set(
          this.snapshot.items
            .filter((item) => !item.archived_at && !item.is_draft)
            .map((item) => item.id),
        );
        if (this.selectedID && !current.has(this.selectedID))
          this.selectedID = "";
        for (const id of this.selectedIDs)
          if (!current.has(id)) this.selectedIDs.delete(id);
      });
      await this.reloadAuxiliary();
    } catch (cause) {
      if (
        epoch !== this.epoch ||
        sequence !== this.requestSequence ||
        controller.signal.aborted
      )
        return;
      if (cause instanceof APIError && cause.code === "requirements_disabled")
        this.disable();
      else if (
        cause instanceof APIError &&
        [401, 403, 404].includes(cause.status)
      )
        this.revoke();
      else
        runInAction(() => {
          this.error = errorMessage(cause);
        });
    } finally {
      if (epoch === this.epoch && sequence === this.requestSequence)
        runInAction(() => {
          this.loading = false;
        });
    }
  }
  async reloadAuxiliary() {
    if (!this.base || this.revoked || this.disabled) return;
    const epoch = this.epoch,
      sequence = ++this.auxiliarySequence,
      base = this.base;
    const loadQuery = new URLSearchParams({
      start_date: this.dateRange.start,
      end_date: this.dateRange.end,
    });
    for (const [field, value] of Object.entries({
      member_id: this.filters.member,
      skill: this.filters.skill,
      commitment_cycle_id: this.filters.cycle,
      state_id: this.filters.state,
      epic_id: this.filters.epic,
      search: this.filters.query,
    }))
      if (value) loadQuery.set(field, value);
    const results = await Promise.allSettled([
      this.transport.get<Scenario[]>(`${base}/scenarios`),
      this.transport.get<ResourceConfiguration>(`${base}/resources`),
      this.transport.get<State[]>(`${base}/states`),
      this.transport.get<Member[]>(`${base}/members`),
      this.dateRange.start
        ? this.transport.get<ResourceLoad>(
            `${base}/resources/load?${loadQuery}`,
          )
        : Promise.resolve({ data: null }),
    ]);
    if (epoch !== this.epoch || sequence !== this.auxiliarySequence) return;
    for (const result of results)
      if (
        result.status === "rejected" &&
        result.reason instanceof APIError &&
        result.reason.code === "requirements_disabled"
      ) {
        this.disable();
        return;
      }
    for (const result of results)
      if (
        result.status === "rejected" &&
        result.reason instanceof APIError &&
        [401, 403, 404].includes(result.reason.status)
      ) {
        this.revoke();
        return;
      }
    runInAction(() => {
      const [scenarios, resources, states, members, load] = results;
      this.scenarios =
        scenarios.status === "fulfilled"
          ? (scenarios.value.data as Scenario[])
          : [];
      this.resources =
        resources.status === "fulfilled"
          ? (resources.value.data as ResourceConfiguration)
          : null;
      this.load = load?.status === "fulfilled" ? load.value.data : null;
      this.states = states.status === "fulfilled" ? states.value.data : [];
      this.members = members.status === "fulfilled" ? members.value.data : [];
      this.auxiliaryError = results
        .filter((result) => result.status === "rejected")
        .map((result) => errorMessage((result as PromiseRejectedResult).reason))
        .join(" · ");
    });
  }
  async setDateRange(start: string, end: string) {
    this.dateRange = { start, end };
    await this.reloadAuxiliary();
  }
  handleEvent(
    event: { revision?: number; kind?: string; reconnect?: boolean },
    type = "change",
  ) {
    if (
      type === "authorization_changed" ||
      event.kind === "authorization_changed"
    ) {
      const base = this.base,
        projectID = this.projectID,
        cursor = Math.max(
          this.metadataCursor,
          this.snapshot.revision,
          Number(event.revision ?? 0),
        );
      this.revoke();
      if (event.reconnect && base) {
        this.base = base;
        this.projectID = projectID;
        this.metadataCursor = cursor;
        this.revoked = false;
        void this.reload().then(() => this.connect());
      }
      return;
    }
    if (type === "cursor_expired" || event.kind === "cursor_expired") {
      if (this.disabled)
        this.metadataCursor = Math.max(
          this.metadataCursor,
          Number(event.revision ?? 0),
        );
      this.stream?.close();
      this.stream = null;
      void this.reload().then(() => this.connect());
      return;
    }
    const revision = Number(event.revision ?? 0);
    if (this.disabled) {
      this.metadataCursor = Math.max(this.metadataCursor, revision);
      return;
    }
    if (
      !revision ||
      revision <= this.snapshot.revision ||
      revision <= this.invalidation
    )
      return;
    this.invalidation = revision;
    if (!this.refreshPromise) {
      const epoch = this.epoch;
      this.refreshPromise = (async () => {
        do {
          const target = this.invalidation;
          await this.reload();
          if (
            epoch === this.epoch &&
            !this.revoked &&
            this.snapshot.revision < target
          ) {
            runInAction(() => {
              this.invalidation = this.snapshot.revision;
              this.stream?.close();
              this.stream = null;
              this.connected = false;
            });
            this.reconnectTimer = setTimeout(() => {
              if (epoch === this.epoch) this.connect();
            }, 1500);
            break;
          }
          if (
            this.snapshot.revision >= this.invalidation ||
            this.invalidation === target
          )
            break;
        } while (epoch === this.epoch && !this.revoked);
      })().finally(() => {
        if (epoch === this.epoch)
          runInAction(() => {
            this.refreshPromise = null;
          });
      });
    }
  }
  connect() {
    if (!this.base || this.revoked || this.stream) return;
    const epoch = this.epoch;
    const stream = this.streamFactory(
      `/api/v1${this.base}/requirements/events?cursor=${Math.max(this.snapshot.revision, this.metadataCursor)}`,
    );
    this.stream = stream;
    stream.onopen = () => {
      if (epoch === this.epoch)
        runInAction(() => {
          this.connected = true;
        });
    };
    const receive = (event: MessageEvent, type: string) => {
      if (epoch !== this.epoch) return;
      try {
        this.handleEvent(JSON.parse(event.data || "{}"), type);
      } catch {
        void this.reload();
      }
    };
    for (const type of ["change", "authorization_changed", "cursor_expired"])
      stream.addEventListener(type, (event) =>
        receive(event as MessageEvent, type),
      );
    stream.onerror = () => {
      stream.close();
      if (epoch !== this.epoch) return;
      runInAction(() => {
        this.stream = null;
        this.connected = false;
      });
      this.reconnectTimer = setTimeout(() => {
        if (epoch === this.epoch) this.connect();
      }, 1500);
    };
  }
  async mutate(action: () => Promise<unknown>): Promise<boolean> {
    if (!this.base || this.revoked || this.disabled || this.busy) return false;
    const epoch = this.epoch;
    this.busy = true;
    this.error = "";
    try {
      await action();
      if (epoch !== this.epoch) return false;
      await this.reload();
      if (epoch !== this.epoch) return false;
      return !this.revoked;
    } catch (cause) {
      if (epoch !== this.epoch) return false;
      if (cause instanceof APIError && cause.code === "requirements_disabled")
        this.disable();
      else if (cause instanceof APIError && [401, 403].includes(cause.status))
        this.revoke();
      else {
        if (cause instanceof APIError && cause.status === 409)
          await this.reload();
        if (epoch !== this.epoch) return false;
        runInAction(() => {
          this.error = errorMessage(cause);
        });
      }
      return false;
    } finally {
      if (epoch === this.epoch)
        runInAction(() => {
          this.busy = false;
        });
    }
  }
  async change(commands: PlanningCommand[]) {
    if (!this.base || this.revoked || this.disabled || this.busy) return false;
    const epoch = this.epoch;
    const signature = JSON.stringify(commands);
    const payload = this.pendingChanges.get(signature) ?? {
      idempotency_key: crypto.randomUUID(),
      expected_revision: this.snapshot.revision,
      commands,
    };
    this.pendingChanges.set(signature, payload);
    let retryable = false;
    const result = await this.mutate(async () => {
      try {
        return await this.transport.post(
          `${this.base}/planning/changesets`,
          payload,
        );
      } catch (cause) {
        retryable =
          cause instanceof APIError &&
          (cause.status === 0 || cause.status >= 500);
        throw cause;
      }
    });
    if (epoch === this.epoch && (!retryable || result))
      this.pendingChanges.delete(signature);
    return result;
  }
  async update(item: WorkItem, fields: Partial<WorkItem>) {
    return this.change([
      { operation: "update", id: item.id, version: item.version, fields },
    ]);
  }
  async create(fields: Partial<WorkItem>) {
    return this.change([
      { operation: "create", client_id: "new_requirement", fields },
    ]);
  }
  async saveActivity(
    activity: UserActivity | null,
    fields: Partial<UserActivity>,
  ) {
    return this.mutate(() =>
      activity
        ? this.transport.patch(
            `${this.base}/requirements/activities/${activity.id}`,
            { ...fields, version: activity.version },
          )
        : this.transport.post(`${this.base}/requirements/activities`, fields),
    );
  }
}

export const requirementsStore = new RequirementsStore();
