import { makeAutoObservable, runInAction } from "mobx";
import { api, APIError, projectPath, setCSRF, workspacePath } from "../lib/api";
import type {
  ID,
  Instance,
  Label,
  Locale,
  Member,
  Project,
  State,
  Theme,
  User,
  WorkItem,
  WorkItemMoveChanges,
  Workspace,
} from "../types";

export interface Toast {
  id: string;
  title: string;
  kind: "success" | "error" | "info";
}

export class AppStore {
  ready = false;
  bootError = "";
  user: User | null = null;
  instance: Instance | null = null;
  locale: Locale =
    (localStorage.getItem("my-jira.locale") as Locale) || "zh-CN";
  theme: Theme = (localStorage.getItem("my-jira.theme") as Theme) || "light";
  sidebarCollapsed = localStorage.getItem("my-jira.sidebar") === "collapsed";
  workspaces = new Map<ID, Workspace>();
  projects = new Map<ID, Project>();
  states = new Map<ID, State[]>();
  labels = new Map<ID, Label[]>();
  members = new Map<ID, Member[]>();
  issues = new Map<ID, WorkItem>();
  projectIssueIds = new Map<ID, ID[]>();
  movingIssueIds = new Set<ID>();
  toasts: Toast[] = [];
  createIssueProjectId: ID | null = null;
  createProjectOpen = false;
  searchOpen = false;
  lastVisited: { workspace_id: ID | null; project_id: ID | null } | null = null;

  constructor() {
    makeAutoObservable(this, {}, { autoBind: true });
  }

  t(zh: string, en: string): string {
    return this.locale === "zh-CN" ? zh : en;
  }

  setLocale(locale: Locale, persist = true) {
    this.locale = locale;
    localStorage.setItem("my-jira.locale", locale);
    document.documentElement.lang = locale;
    if (persist) this.persistAppearance({ locale });
  }

  setTheme(theme: Theme, persist = true) {
    this.theme = theme;
    localStorage.setItem("my-jira.theme", theme);
    const isDark =
      theme === "dark" ||
      (theme === "system" &&
        matchMedia("(prefers-color-scheme: dark)").matches);
    document.documentElement.dataset.theme = isDark ? "dark" : "light";
    document.documentElement.style.colorScheme = isDark ? "dark" : "light";
    if (persist) this.persistAppearance({ theme });
  }

  toggleSidebar() {
    this.sidebarCollapsed = !this.sidebarCollapsed;
    localStorage.setItem(
      "my-jira.sidebar",
      this.sidebarCollapsed ? "collapsed" : "expanded",
    );
    this.persistAppearance({ sidebar_collapsed: this.sidebarCollapsed });
  }

  persistAppearance(changes: Record<string, unknown>) {
    if (!this.user) return;
    const userID = this.user.id;
    api
      .patch<User>("/auth/me", { preferences: { appearance: changes } })
      .then(() => {
        runInAction(() => {
          if (this.user?.id === userID)
            this.user.preferences = {
              ...this.user.preferences,
              appearance: {
                ...((this.user.preferences.appearance as Record<
                  string,
                  unknown
                >) ?? {}),
                ...changes,
              },
            };
        });
      })
      .catch(() =>
        this.notify(
          this.t(
            "偏好已在此浏览器应用，但账户同步失败。",
            "Preferences were applied in this browser, but account sync failed.",
          ),
          "error",
        ),
      );
  }

  applyAccountAppearance() {
    const appearance = this.user?.preferences.appearance as
      Record<string, unknown> | undefined;
    if (!appearance) return;
    if (["light", "dark", "system"].includes(String(appearance.theme)))
      this.setTheme(appearance.theme as Theme, false);
    if (["zh-CN", "en"].includes(String(appearance.locale)))
      this.setLocale(appearance.locale as Locale, false);
    if (typeof appearance.sidebar_collapsed === "boolean") {
      this.sidebarCollapsed = appearance.sidebar_collapsed;
      localStorage.setItem(
        "my-jira.sidebar",
        this.sidebarCollapsed ? "collapsed" : "expanded",
      );
    }
  }

  notify(title: string, kind: Toast["kind"] = "success") {
    const id = crypto.randomUUID();
    this.toasts.push({ id, title, kind });
    setTimeout(() => this.dismissToast(id), kind === "error" ? 8000 : 4500);
  }

  dismissToast(id: string) {
    this.toasts = this.toasts.filter((toast) => toast.id !== id);
  }
  setSearchOpen(open: boolean) {
    this.searchOpen = open;
  }
  setCreateProjectOpen(open: boolean) {
    this.createProjectOpen = open;
  }
  setCreateIssueProject(id: ID | null) {
    this.createIssueProjectId = id;
  }

  async bootstrap() {
    this.ready = false;
    this.bootError = "";
    this.setTheme(this.theme, false);
    this.setLocale(this.locale, false);
    try {
      const instance = await api.get<Instance>("/instance");
      runInAction(() => {
        this.instance = instance.data;
      });
      if (instance.data.is_setup_done) {
        try {
          const response = await api.get<User>("/auth/me");
          runInAction(() => {
            this.user = response.data;
            this.applyAccountAppearance();
          });
          await this.loadWorkspaces();
          await this.loadLastVisited();
        } catch (error) {
          if (!(error instanceof APIError && error.status === 401)) throw error;
        }
      }
    } catch (error) {
      runInAction(() => {
        this.bootError =
          error instanceof Error ? error.message : "Unable to start";
      });
    } finally {
      runInAction(() => {
        this.ready = true;
      });
    }
  }

  async authenticate(
    kind: "login" | "register" | "setup",
    data: Record<string, string>,
  ) {
    const path = kind === "setup" ? "/instance/setup" : `/auth/${kind}`;
    const response = await api.post<{ user: User; csrf_token: string }>(
      path,
      data,
    );
    setCSRF(response.data.csrf_token);
    runInAction(() => {
      this.user = response.data.user;
      this.applyAccountAppearance();
      if (this.instance) this.instance.is_setup_done = true;
    });
    await this.loadWorkspaces();
    await this.loadLastVisited();
  }

  async logout() {
    await api.post<void>("/auth/logout");
    runInAction(() => {
      this.user = null;
      this.lastVisited = null;
      this.workspaces.clear();
      this.projects.clear();
      this.issues.clear();
      this.states.clear();
      this.labels.clear();
      this.members.clear();
      this.projectIssueIds.clear();
    });
    setCSRF("");
  }

  async updateProfile(data: Partial<User>) {
    const result = await api.patch<User>("/auth/me", data);
    runInAction(() => {
      this.user = result.data;
    });
    return result.data;
  }

  async loadLastVisited() {
    const response = await api.get<{
      workspace_id: ID | null;
      project_id: ID | null;
    }>("/auth/last-visited");
    runInAction(() => {
      this.lastVisited = response.data;
    });
  }

  async rememberLocation(workspace_id: ID, project_id: ID | null) {
    const response = await api.patch<{
      workspace_id: ID | null;
      project_id: ID | null;
    }>("/auth/last-visited", { workspace_id, project_id });
    runInAction(() => {
      this.lastVisited = response.data;
    });
  }

  async loadWorkspaces() {
    const response = await api.get<Workspace[]>("/workspaces");
    runInAction(() => {
      this.workspaces = new Map(
        response.data.map((workspace) => [workspace.id, workspace]),
      );
    });
    return response.data;
  }

  async createWorkspace(data: {
    name: string;
    slug: string;
    timezone: string;
  }) {
    const result = await api.post<Workspace>("/workspaces", data);
    runInAction(() => {
      this.workspaces.set(result.data.id, result.data);
    });
    return result.data;
  }

  async loadProjects(workspaceId: ID) {
    const response = await api.get<Project[]>(
      `${workspacePath(workspaceId)}/projects`,
    );
    runInAction(() => {
      for (const [id, project] of this.projects)
        if (project.workspace_id === workspaceId) this.projects.delete(id);
      for (const project of response.data)
        this.projects.set(project.id, project);
    });
    return response.data;
  }

  async createProject(workspaceId: ID, data: Partial<Project>) {
    const result = await api.post<Project>(
      `${workspacePath(workspaceId)}/projects`,
      data,
    );
    runInAction(() => {
      this.projects.set(result.data.id, result.data);
    });
    return result.data;
  }

  async loadProject(workspaceId: ID, projectId: ID) {
    const result = await api.get<Project>(projectPath(workspaceId, projectId));
    runInAction(() => this.projects.set(projectId, result.data));
    return result.data;
  }

  async loadProjectResources(workspaceId: ID, projectId: ID) {
    const base = projectPath(workspaceId, projectId);
    const [states, labels, members] = await Promise.all([
      api.get<State[]>(`${base}/states`),
      api.get<Label[]>(`${base}/labels`),
      api.get<Member[]>(`${base}/members`),
    ]);
    runInAction(() => {
      this.states.set(
        projectId,
        states.data.sort((a, b) => a.position - b.position),
      );
      this.labels.set(projectId, labels.data);
      this.members.set(projectId, members.data);
    });
  }

  async loadIssues(
    workspaceId: ID,
    projectId: ID,
    query = "",
    signal?: AbortSignal,
  ) {
    const result = await api.get<WorkItem[]>(
      `${projectPath(workspaceId, projectId)}/issues${query ? `?${query}` : ""}`,
      signal,
    );
    if (signal?.aborted) return result;
    runInAction(() => {
      for (const item of result.data) this.issues.set(item.id, item);
      this.projectIssueIds.set(
        projectId,
        result.data.map((item) => item.id),
      );
    });
    return result;
  }

  async createIssue(workspaceId: ID, projectId: ID, data: Partial<WorkItem>) {
    const result = await api.post<WorkItem>(
      `${projectPath(workspaceId, projectId)}/issues`,
      data,
    );
    runInAction(() => {
      this.issues.set(result.data.id, result.data);
      this.projectIssueIds.set(projectId, [
        ...(this.projectIssueIds.get(projectId) ?? []),
        result.data.id,
      ]);
    });
    return result.data;
  }

  async loadIssue(workspaceId: ID, projectId: ID, issueId: ID) {
    const result = await api.get<WorkItem>(
      `${projectPath(workspaceId, projectId)}/issues/${issueId}`,
    );
    runInAction(() => {
      this.issues.set(result.data.id, result.data);
    });
    return result.data;
  }

  async updateIssue(issueId: ID, patch: Partial<WorkItem>) {
    const original = this.issues.get(issueId);
    if (!original) throw new Error("Work item is not loaded");
    const optimistic = { ...original, ...patch };
    this.issues.set(issueId, optimistic);
    try {
      const result = await api.patch<WorkItem>(
        `${projectPath(original.workspace_id, original.project_id)}/issues/${issueId}`,
        { ...patch, version: original.version },
      );
      runInAction(() => {
        this.issues.set(issueId, result.data);
      });
      return result.data;
    } catch (error) {
      runInAction(() => {
        this.issues.set(issueId, original);
      });
      if (error instanceof APIError && error.status === 409)
        await this.loadIssue(
          original.workspace_id,
          original.project_id,
          issueId,
        );
      throw error;
    }
  }

  async deleteIssue(issueId: ID) {
    const item = this.issues.get(issueId);
    if (!item) return;
    await api.delete(
      `${projectPath(item.workspace_id, item.project_id)}/issues/${issueId}`,
    );
    runInAction(() => {
      this.issues.delete(issueId);
      this.projectIssueIds.set(
        item.project_id,
        (this.projectIssueIds.get(item.project_id) ?? []).filter(
          (id) => id !== issueId,
        ),
      );
    });
  }

  async moveIssue(
    issueId: ID,
    projectId: ID,
    stateId: ID,
    changes: WorkItemMoveChanges,
  ) {
    const original = this.issues.get(issueId);
    if (!original) throw new Error("Work item is not loaded");
    if (this.movingIssueIds.has(issueId))
      throw new Error("This work item is already being moved");
    this.movingIssueIds.add(issueId);
    try {
      const result = await api.post<WorkItem>(
        `${projectPath(original.workspace_id, original.project_id)}/issues/${issueId}/move`,
        {
          project_id: projectId,
          state_id: stateId,
          version: original.version,
          changes,
        },
      );
      runInAction(() => {
        this.issues.set(issueId, result.data);
        this.projectIssueIds.set(
          original.project_id,
          (this.projectIssueIds.get(original.project_id) ?? []).filter(
            (id) => id !== issueId,
          ),
        );
        this.projectIssueIds.set(projectId, [
          ...new Set([...(this.projectIssueIds.get(projectId) ?? []), issueId]),
        ]);
      });
      return result.data;
    } catch (error) {
      if (error instanceof APIError && error.status === 409) {
        // The source may have moved elsewhere. Keep the conflict as the
        // reported error while the grouped view refreshes its current scope.
        await this.loadIssue(
          original.workspace_id,
          original.project_id,
          issueId,
        ).catch(() => undefined);
      }
      throw error;
    } finally {
      runInAction(() => this.movingIssueIds.delete(issueId));
    }
  }

  projectIssues(projectId: ID): WorkItem[] {
    return (this.projectIssueIds.get(projectId) ?? [])
      .map((id) => this.issues.get(id))
      .filter(
        (issue): issue is WorkItem => !!issue && issue.project_id === projectId,
      );
  }

  cacheIssues(items: WorkItem[]) {
    for (const item of items)
      if ((this.issues.get(item.id)?.version ?? 0) <= item.version)
        this.issues.set(item.id, item);
  }

  workspaceProjects(workspaceId: ID): Project[] {
    return [...this.projects.values()].filter(
      (project) => project.workspace_id === workspaceId,
    );
  }
}

export const appStore = new AppStore();
