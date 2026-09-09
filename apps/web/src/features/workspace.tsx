import { useEffect, useState, type FormEvent } from "react";
import { observer } from "mobx-react-lite";
import { Link, useNavigate } from "react-router-dom";
import {
  Archive,
  ArrowRight,
  BarChart3,
  Bell,
  Check,
  CheckCheck,
  CheckCircle2,
  ChevronRight,
  Circle,
  Clock3,
  FileText,
  FolderKanban,
  Inbox,
  ListTodo,
  Plus,
  Search,
  Star,
  StickyNote,
  Trash2,
} from "lucide-react";
import { appStore } from "../stores/app-store";
import { api, workspacePath } from "../lib/api";
import { useMutation, useRemote, useScope } from "../lib/hooks";
import { cn, dateTime, safeHTML, shortDate } from "../lib/utils";
import {
  Badge,
  Button,
  EmptyState,
  ErrorBox,
  Input,
  Loading,
  Modal,
  MultiSelect,
  PageHeader,
  PriorityIcon,
  Select,
  StateIcon,
} from "../components/ui";
import type { Notification, State, WorkItem } from "../types";
import { AnalyticsExplorer } from "./analytics-explorer";
import { useWorkspacePreference } from "../lib/preferences";
import {
  HomeCustomization,
  homeDefaults,
  OrderedHomeWidgets,
  QuickLinks,
} from "../components/home-customization";
import { useFavorites } from "../lib/bookmarks";

interface AnalyticsData {
  total: number;
  completed: number;
  started: number;
  overdue: number;
  by_state: { state_id: string; name: string; color: string; count: number }[];
  by_priority: { priority: string; count: number }[];
  by_project: {
    project_id: string;
    name: string;
    identifier: string;
    total: number;
    completed: number;
  }[];
  trend: { date: string; created: number; completed: number }[];
}
interface Bookmark {
  id: string;
  entity_id: string;
  entity_type: string;
  entity: { name: string; project_id?: string | null; id?: string };
}
interface Sticky {
  id: string;
  title: string;
  content_html: string;
  color: string;
  is_archived: boolean;
}
type EnrichedWorkItem = WorkItem & { state_detail?: State };

export const HomePage = observer(function HomePage() {
  const { workspace } = useScope();
  const base = workspacePath(workspace.id);
  const home = useWorkspacePreference("home", homeDefaults);
  const stats = useRemote<AnalyticsData>(`${base}/analytics`);
  const assigned = useRemote<EnrichedWorkItem[]>(
    `${base}/issues?assignee_id=${appStore.user!.id}&limit=6&order_by=target_date`,
  );
  const favorites = useFavorites();
  const recent = useRemote<Bookmark[]>(`${base}/recent-visits`);
  const stickies = useRemote<Sticky[]>(`${base}/stickies`);
  const [stickyOpen, setStickyOpen] = useState(false);
  const [stickyTitle, setStickyTitle] = useState("");
  const [stickyContent, setStickyContent] = useState("");
  const mutation = useMutation();
  const t = appStore.t;
  const projects = appStore
    .workspaceProjects(workspace.id)
    .filter((project) => !project.archived_at);

  const bookmarkRoute = (bookmark: Bookmark) => {
    const prefix = `/w/${workspace.slug}`;
    const projectPrefix = bookmark.entity.project_id
      ? `${prefix}/projects/${bookmark.entity.project_id}`
      : prefix;
    if (bookmark.entity_type === "project")
      return `${prefix}/projects/${bookmark.entity_id}/issues`;
    return `${projectPrefix}/${({ issue: "issues", cycle: "cycles", module: "modules", view: "views", page: "pages" } as Record<string, string>)[bookmark.entity_type] ?? "home"}/${bookmark.entity_id}`;
  };

  const createSticky = async (event: FormEvent) => {
    event.preventDefault();
    const element = document.createElement("div");
    element.textContent = stickyContent;
    await mutation.execute(async () => {
      await api.post(`${base}/stickies`, {
        title: stickyTitle,
        content_html: `<p>${element.innerHTML.replace(/\n/g, "</p><p>")}</p>`,
        color: "#f4df9d",
      });
      setStickyOpen(false);
      setStickyTitle("");
      setStickyContent("");
      stickies.refresh();
    });
  };

  return (
    <div className="page-scroll">
      <div className="home-content">
        <section className="welcome-section">
          <div>
            <p className="eyebrow">
              {new Intl.DateTimeFormat(appStore.locale, {
                month: "long",
                day: "numeric",
                weekday: "long",
              }).format(new Date())}
            </p>
            <h1>
              {t("你好", "Hello")}, {appStore.user!.display_name}
              <span className="welcome-dot">.</span>
            </h1>
            <p>
              {t(
                "留一点空间给思考，再从最重要的一件事开始。",
                "Make a little room to think. Then start with what matters.",
              )}
            </p>
          </div>
          <div className="header-actions">
            <HomeCustomization
              value={home.value}
              save={home.save}
              disabled={home.loading}
            />
            <Button
              variant="primary"
              onClick={() =>
                projects.length
                  ? appStore.setCreateIssueProject(projects[0].id)
                  : appStore.setCreateProjectOpen(true)
              }
            >
              <Plus size={15} />
              {projects.length
                ? t("新建工作项", "New work item")
                : t("创建项目", "Create project")}
            </Button>
          </div>
        </section>
        <ErrorBox message={stats.error || home.error} />
        <OrderedHomeWidgets value={home.value}>
          <div className="home-stat-grid" data-widget="stats">
            {[
              {
                value: stats.data?.total,
                label: t("工作项总数", "Total work items"),
                icon: ListTodo,
                color: "blue",
              },
              {
                value: stats.data?.started,
                label: t("正在进行", "In progress"),
                icon: Circle,
                color: "violet",
              },
              {
                value: stats.data?.completed,
                label: t("已完成", "Completed"),
                icon: CheckCircle2,
                color: "green",
              },
              {
                value: stats.data?.overdue,
                label: t("需要关注", "Overdue"),
                icon: Clock3,
                color: "amber",
              },
            ].map((item) => (
              <div className="home-stat-card" key={item.label}>
                <div>
                  <span className={cn("stat-icon", item.color)}>
                    <item.icon size={16} />
                  </span>
                  <span>{item.label}</span>
                </div>
                <strong>{item.value ?? "—"}</strong>
              </div>
            ))}
          </div>
          <section className="home-section" data-widget="upcoming">
            <div className="section-title">
              <h2>
                <ListTodo size={17} />
                {t("我的近期工作", "My upcoming work")}
              </h2>
              <Link to={`/w/${workspace.slug}/my-work`}>
                {t("查看全部", "View all")}
                <ChevronRight size={14} />
              </Link>
            </div>
            <ErrorBox message={assigned.error} />
            {assigned.loading ? (
              <Loading />
            ) : assigned.data?.length ? (
              <div className="home-work-list">
                {assigned.data.map((item) => (
                  <Link
                    key={item.id}
                    to={`/w/${workspace.slug}/projects/${item.project_id}/issues/${item.id}`}
                  >
                    <StateIcon state={item.state_detail} />
                    <div>
                      <strong>{item.name}</strong>
                      <span>
                        {appStore.projects.get(item.project_id)?.identifier}-
                        {item.sequence_id}
                      </span>
                    </div>
                    <PriorityIcon priority={item.priority} />
                    <time>{shortDate(item.target_date, appStore.locale)}</time>
                    <ChevronRight size={14} />
                  </Link>
                ))}
              </div>
            ) : (
              <div className="home-empty">
                <CheckCheck size={27} />
                <h3>{t("留出空间，迎接下一步", "Room for your next step")}</h3>
                <p>
                  {t(
                    "分配给你的工作项会出现在这里。",
                    "Work assigned to you will appear here.",
                  )}
                </p>
              </div>
            )}
          </section>
          <section className="home-section" data-widget="projects">
            <div className="section-title">
              <h2>
                <FolderKanban size={17} />
                {t("团队项目", "Team projects")}
              </h2>
              <button onClick={() => appStore.setCreateProjectOpen(true)}>
                <Plus size={14} />
                {t("新建", "New")}
              </button>
            </div>
            <div className="home-projects">
              {projects.slice(0, 6).map((project) => (
                <Link
                  key={project.id}
                  to={`/w/${workspace.slug}/projects/${project.id}/issues`}
                >
                  <span
                    className="project-tile"
                    style={{
                      color: project.color || "#6875cf",
                      backgroundColor: `${project.color || "#6875cf"}15`,
                    }}
                  >
                    <FolderKanban size={19} />
                  </span>
                  <span>
                    <strong>{project.name}</strong>
                    <small>{project.identifier}</small>
                  </span>
                  <ArrowRight size={15} />
                </Link>
              ))}
              {!projects.length && (
                <button
                  className="home-new-project"
                  onClick={() => appStore.setCreateProjectOpen(true)}
                >
                  <Plus size={20} />
                  <span>
                    {t("创建第一个项目", "Create your first project")}
                  </span>
                </button>
              )}
            </div>
          </section>
          <section className="home-section" data-widget="favorites">
            <div className="section-title">
              <h2>
                <Star size={16} />
                {t("收藏", "Favorites")}
              </h2>
            </div>
            <ErrorBox message={favorites.error} />
            {favorites.data?.length ? (
              <div className="bookmark-list">
                {favorites.data.slice(0, 6).map((item) => (
                  <div key={item.id}>
                    <FileText size={14} />
                    <Link to={bookmarkRoute(item)}>{item.entity.name}</Link>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={t("取消收藏", "Remove favorite")}
                      onClick={() => {
                        mutation.execute(async () => {
                          await api.delete(`${base}/favorites/${item.id}`);
                          favorites.refresh();
                        });
                      }}
                    >
                      <Star size={13} fill="currentColor" />
                    </Button>
                  </div>
                ))}
              </div>
            ) : (
              <p className="home-side-hint">
                {t(
                  "收藏常用的项目与文档，让重要内容触手可及。",
                  "Favorite useful projects and documents to keep them close.",
                )}
              </p>
            )}
          </section>
          <section className="home-section" data-widget="recent">
            <div className="section-title">
              <h2>
                <Clock3 size={16} />
                {t("最近访问", "Recently visited")}
              </h2>
            </div>
            {recent.data?.length ? (
              <div className="bookmark-list">
                {recent.data.slice(0, 5).map((item) => (
                  <div key={item.id}>
                    <FileText size={14} />
                    <Link to={bookmarkRoute(item)}>{item.entity.name}</Link>
                    <ChevronRight size={13} />
                  </div>
                ))}
              </div>
            ) : (
              <p className="home-side-hint">
                {t(
                  "你访问过的内容会在这里留下足迹。",
                  "Find your way back to recently visited work.",
                )}
              </p>
            )}
          </section>
          <section className="home-section" data-widget="stickies">
            <div className="section-title">
              <h2>
                <StickyNote size={16} />
                {t("个人便笺", "Personal notes")}
              </h2>
              <Button
                variant="ghost"
                size="icon"
                aria-label={t("添加便笺", "Add note")}
                onClick={() => setStickyOpen(true)}
              >
                <Plus size={14} />
              </Button>
            </div>
            <ErrorBox message={stickies.error} />
            {stickies.data?.length ? (
              <div className="sticky-list">
                {stickies.data.map((sticky) => (
                  <article key={sticky.id} className="sticky-card">
                    <header>
                      <strong>{sticky.title}</strong>
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={t("删除便笺", "Delete note")}
                        onClick={() => {
                          mutation.execute(async () => {
                            await api.delete(`${base}/stickies/${sticky.id}`);
                            stickies.refresh();
                          });
                        }}
                      >
                        <Trash2 size={12} />
                      </Button>
                    </header>
                    <div
                      className="prose-content"
                      dangerouslySetInnerHTML={{
                        __html: safeHTML(sticky.content_html),
                      }}
                    />
                  </article>
                ))}
              </div>
            ) : (
              <button
                className="new-sticky"
                onClick={() => setStickyOpen(true)}
              >
                <Plus size={16} />
                {t("记下一个想法…", "Capture a thought…")}
              </button>
            )}
          </section>
          <section className="home-section" data-widget="links">
            <QuickLinks
              links={home.value.quick_links}
              save={(quick_links) => home.save({ quick_links })}
            />
          </section>
        </OrderedHomeWidgets>
      </div>
      <Modal
        open={stickyOpen}
        onOpenChange={setStickyOpen}
        title={t("记录一个想法", "Capture a thought")}
      >
        <form onSubmit={createSticky} className="form-stack modal-body">
          <Input
            aria-label={t("便笺标题", "Note title")}
            value={stickyTitle}
            onChange={(event) => setStickyTitle(event.target.value)}
            placeholder={t("标题", "Title")}
            required
            autoFocus
          />
          <textarea
            className="input textarea"
            value={stickyContent}
            onChange={(event) => setStickyContent(event.target.value)}
            placeholder={t(
              "想到什么，就写下来。",
              "Write down what's on your mind.",
            )}
            rows={6}
          />
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button type="submit" variant="primary" busy={mutation.busy}>
              {t("保存便笺", "Save note")}
            </Button>
          </div>
        </form>
      </Modal>
    </div>
  );
});

export const MyWorkPage = observer(function MyWorkPage() {
  const { workspace } = useScope();
  const [tab, setTab] = useState("assigned");
  const [query, setQuery] = useState("");
  const parameters = new URLSearchParams({
    limit: "100",
    search: query,
    ...(tab === "assigned"
      ? { assignee_id: appStore.user!.id }
      : tab === "created"
        ? { created_by: appStore.user!.id }
        : tab === "subscribed"
          ? { subscriber_id: appStore.user!.id }
          : tab === "mentions"
            ? { mention_id: appStore.user!.id }
            : { draft: "true" }),
  });
  const issues = useRemote<EnrichedWorkItem[]>(
    `${workspacePath(workspace.id)}/issues?${parameters}`,
  );
  const t = appStore.t;
  return (
    <div className="page-scroll">
      <PageHeader
        title={t("我的工作", "My work")}
        description={t(
          "跨越项目边界，专注于与你有关的工作。",
          "Focus on your work, across all your projects.",
        )}
      >
        <div className="page-tabs">
          {[
            { id: "assigned", zh: "分配给我", en: "Assigned to me" },
            { id: "created", zh: "我创建的", en: "Created by me" },
            { id: "subscribed", zh: "我订阅的", en: "Following" },
            { id: "mentions", zh: "提及我的", en: "Mentioned" },
            { id: "drafts", zh: "草稿", en: "Drafts" },
          ].map((item) => (
            <button
              className={tab === item.id ? "active" : ""}
              key={item.id}
              onClick={() => setTab(item.id)}
            >
              {t(item.zh, item.en)}
            </button>
          ))}
          <span className="flex-spacer" />
          <div className="search-input compact">
            <Search size={14} />
            <Input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder={t("搜索…", "Search…")}
              aria-label={t("搜索我的工作", "Search my work")}
            />
          </div>
        </div>
      </PageHeader>
      <ErrorBox message={issues.error} retry={issues.refresh} />
      {issues.loading ? (
        <Loading />
      ) : !issues.data?.length ? (
        <EmptyState
          icon={<ListTodo size={30} />}
          title={t("这里暂时没有工作项", "No work items here yet")}
          description={t(
            "创建或分配工作项后，就能在这里集中查看。",
            "Create or assign work items to see them together here.",
          )}
        />
      ) : (
        <div className="page-body">
          <div className="workspace-work-table">
            <header>
              <span>{t("工作项", "Work item")}</span>
              <span>{t("项目", "Project")}</span>
              <span>{t("状态", "State")}</span>
              <span>{t("目标日期", "Due date")}</span>
            </header>
            {issues.data.map((item) => (
              <Link
                key={item.id}
                to={`/w/${workspace.slug}/projects/${item.project_id}/issues/${item.id}`}
              >
                <div>
                  <PriorityIcon priority={item.priority} />
                  <span>
                    <strong>{item.name}</strong>
                    <small>
                      {appStore.projects.get(item.project_id)?.identifier}-
                      {item.sequence_id}
                    </small>
                  </span>
                </div>
                <span>{appStore.projects.get(item.project_id)?.name}</span>
                <span className="inline-property">
                  <StateIcon state={item.state_detail} />
                  {item.state_detail?.name}
                </span>
                <span
                  className={
                    item.target_date &&
                    item.target_date < new Date().toISOString().slice(0, 10)
                      ? "overdue"
                      : ""
                  }
                >
                  {shortDate(item.target_date, appStore.locale)}
                </span>
              </Link>
            ))}
          </div>
        </div>
      )}
    </div>
  );
});

export const InboxPage = observer(function InboxPage() {
  const { workspace } = useScope();
  const [filter, setFilter] = useState("all");
  const [selected, setSelected] = useState<Notification | null>(null);
  const [reasons, setReasons] = useState<string[]>([]);
  const [projectFilter, setProjectFilter] = useState("");
  const [offset, setOffset] = useState(0);
  const base = `${workspacePath(workspace.id)}/notifications`;
  const scopeParams = new URLSearchParams();
  if (filter === "unread") scopeParams.set("read", "false");
  if (filter === "read") scopeParams.set("read", "true");
  if (filter === "archived") scopeParams.set("archived", "true");
  if (filter === "snoozed") scopeParams.set("snoozed", "true");
  if (reasons.length) scopeParams.set("reason", reasons.join(","));
  if (projectFilter) scopeParams.set("project_id", projectFilter);
  const items = useRemote<Notification[]>(
    `${base}?${scopeParams}&limit=50&offset=${offset}`,
  );
  const mutation = useMutation();
  const navigate = useNavigate();
  const t = appStore.t;
  useEffect(() => {
    setOffset(0);
    setSelected(null);
  }, [filter, reasons, projectFilter]);
  const patch = async (item: Notification, data: Record<string, unknown>) =>
    mutation.execute(async () => {
      const result = await api.patch<Notification>(`${base}/${item.id}`, data);
      items.refresh();
      if (selected?.id === item.id) setSelected(result.data);
    });
  return (
    <div className="inbox-page">
      <PageHeader
        title={t("收件箱", "Inbox")}
        actions={
          <Button
            size="sm"
            onClick={() => {
              mutation.execute(
                async () => {
                  await api.post(`${base}/mark-all-read?${scopeParams}`);
                  items.refresh();
                  setSelected((item) =>
                    item
                      ? { ...item, read_at: new Date().toISOString() }
                      : null,
                  );
                },
                t(
                  "当前筛选中的通知已标为已读",
                  "Matching notifications marked as read",
                ),
              );
            }}
          >
            <CheckCheck size={14} />
            {t("全部已读", "Mark all read")}
          </Button>
        }
      >
        <div className="page-tabs">
          {[
            { id: "all", zh: "全部", en: "All" },
            { id: "unread", zh: "未读", en: "Unread" },
            { id: "read", zh: "已读", en: "Read" },
            { id: "snoozed", zh: "稍后提醒", en: "Snoozed" },
            { id: "archived", zh: "已归档", en: "Archived" },
          ].map((item) => (
            <button
              key={item.id}
              className={filter === item.id ? "active" : ""}
              onClick={() => {
                setFilter(item.id);
                setSelected(null);
              }}
            >
              {t(item.zh, item.en)}
            </button>
          ))}
        </div>
        <div className="filter-bar">
          <MultiSelect
            value={reasons}
            onChange={setReasons}
            options={[
              { value: "mentions", label: t("提及我", "Mentions") },
              { value: "assigned", label: t("分配给我", "Assignments") },
              { value: "created", label: t("我创建的工作", "Work I created") },
              {
                value: "subscribed",
                label: t("我订阅的工作", "Work I follow"),
              },
            ]}
            placeholder={t("所有通知来源", "All notification reasons")}
          />
          <Select
            aria-label={t("通知项目", "Notification project")}
            value={projectFilter}
            onChange={(event) => setProjectFilter(event.target.value)}
          >
            <option value="">{t("所有项目", "All projects")}</option>
            {appStore.workspaceProjects(workspace.id).map((project) => (
              <option key={project.id} value={project.id}>
                {project.name}
              </option>
            ))}
          </Select>
          <Badge>{items.pagination?.total ?? 0}</Badge>
        </div>
      </PageHeader>
      <ErrorBox message={items.error || mutation.error} retry={items.refresh} />
      {items.loading ? (
        <Loading />
      ) : !items.data?.length ? (
        <EmptyState
          icon={<Inbox size={32} />}
          title={t("收件箱已清空", "You're all caught up")}
          description={t(
            "提及、分配与你订阅的更新会在这里汇聚。",
            "Mentions, assignments, and updates you follow will arrive here.",
          )}
        />
      ) : (
        <div className="inbox-columns">
          <div className="notification-list">
            {items.data.map((item) => (
              <button
                key={item.id}
                className={cn(
                  "notification-item",
                  !item.read_at && "unread",
                  selected?.id === item.id && "selected",
                )}
                onClick={() => {
                  setSelected(item);
                  if (!item.read_at) patch(item, { read: true });
                }}
              >
                <span className="notification-icon">
                  <Bell size={16} />
                </span>
                <div>
                  <strong>{item.title}</strong>
                  <p>{item.body}</p>
                  <time>{dateTime(item.created_at, appStore.locale)}</time>
                </div>
                {!item.read_at && <span className="unread-dot" />}
              </button>
            ))}
            {(offset > 0 || items.pagination?.has_more) && (
              <div className="pagination-footer">
                <Button
                  size="sm"
                  disabled={!offset}
                  onClick={() => setOffset(Math.max(0, offset - 50))}
                >
                  {t("上一页", "Previous")}
                </Button>
                <span>
                  {offset + 1}–{offset + items.data.length} /{" "}
                  {items.pagination?.total}
                </span>
                <Button
                  size="sm"
                  disabled={!items.pagination?.has_more}
                  onClick={() =>
                    setOffset(items.pagination?.next_offset ?? offset + 50)
                  }
                >
                  {t("下一页", "Next")}
                </Button>
              </div>
            )}
          </div>
          <section className="notification-detail">
            {selected ? (
              <>
                <div className="notification-detail-actions">
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => patch(selected, { read: !selected.read_at })}
                  >
                    <Check size={14} />
                    {selected.read_at
                      ? t("标为未读", "Mark unread")
                      : t("标为已读", "Mark read")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() =>
                      patch(selected, { archived: !selected.archived_at })
                    }
                  >
                    <Archive size={14} />
                    {selected.archived_at
                      ? t("取消归档", "Unarchive")
                      : t("归档", "Archive")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() =>
                      patch(selected, {
                        snoozed_until: new Date(
                          Date.now() + 86400000,
                        ).toISOString(),
                      })
                    }
                  >
                    <Clock3 size={14} />
                    {t("明天提醒", "Tomorrow")}
                  </Button>
                </div>
                <h2>{selected.title}</h2>
                <p>{selected.body}</p>
                <time>{dateTime(selected.created_at, appStore.locale)}</time>
                {selected.entity_type === "issue" &&
                  selected.entity_id &&
                  selected.project_id && (
                    <Button
                      variant="primary"
                      onClick={() =>
                        navigate(
                          `/w/${workspace.slug}/projects/${selected.project_id}/issues/${selected.entity_id}`,
                        )
                      }
                    >
                      {t("查看工作项", "View work item")}
                      <ArrowRight size={14} />
                    </Button>
                  )}
              </>
            ) : (
              <div className="notification-placeholder">
                <Inbox size={36} />
                <p>
                  {t(
                    "选择一条通知查看详情",
                    "Select a notification to see its details",
                  )}
                </p>
              </div>
            )}
          </section>
        </div>
      )}
    </div>
  );
});

export const AnalyticsPage = observer(function AnalyticsPage() {
  const { workspace } = useScope();
  const [mode, setMode] = useState("overview");
  const [projectId, setProjectId] = useState("");
  const result = useRemote<AnalyticsData>(
    mode === "overview"
      ? `${workspacePath(workspace.id)}/analytics${projectId ? `?project_id=${projectId}` : ""}`
      : null,
  );
  const t = appStore.t;
  const stats = result.data;
  const maxTrend = Math.max(
    1,
    ...(stats?.trend ?? []).flatMap((day) => [day.created, day.completed]),
  );
  const trendLine = (field: "created" | "completed") =>
    (stats?.trend ?? [])
      .map(
        (day, index) =>
          `${(index / Math.max(1, (stats?.trend.length ?? 1) - 1)) * 640},${160 - (day[field] / maxTrend) * 130}`,
      )
      .join(" ");
  return (
    <div className="page-scroll">
      <PageHeader
        title={t("数据分析", "Analytics")}
        description={t(
          "从真实进展中发现节奏，为下一次决策提供依据。",
          "Understand your team's rhythm through real progress.",
        )}
        actions={
          <Select
            aria-label={t("分析项目", "Analyze project")}
            value={projectId}
            onChange={(event) => setProjectId(event.target.value)}
          >
            <option value="">{t("整个工作区", "Entire workspace")}</option>
            {appStore.workspaceProjects(workspace.id).map((project) => (
              <option key={project.id} value={project.id}>
                {project.name}
              </option>
            ))}
          </Select>
        }
      >
        <div className="page-tabs">
          <button
            className={mode === "overview" ? "active" : ""}
            onClick={() => setMode("overview")}
          >
            {t("概览", "Overview")}
          </button>
          <button
            className={mode === "custom" ? "active" : ""}
            onClick={() => setMode("custom")}
          >
            {t("自定义分析", "Custom analysis")}
          </button>
        </div>
      </PageHeader>
      <div className="page-body">
        {mode === "custom" ? (
          <AnalyticsExplorer />
        ) : (
          <>
            <ErrorBox message={result.error} retry={result.refresh} />
            {result.loading ? (
              <Loading />
            ) : (
              stats && (
                <>
                  <div className="analytics-stat-grid">
                    {[
                      {
                        value: stats.total,
                        name: t("工作项总数", "Total work items"),
                        icon: ListTodo,
                      },
                      {
                        value: stats.completed,
                        name: t("已完成", "Completed"),
                        icon: CheckCircle2,
                      },
                      {
                        value: stats.started,
                        name: t("进行中", "In progress"),
                        icon: Circle,
                      },
                      {
                        value: stats.overdue,
                        name: t("已逾期", "Overdue"),
                        icon: Clock3,
                      },
                    ].map((card) => (
                      <div className="analytics-stat" key={card.name}>
                        <span>
                          <card.icon size={16} />
                          {card.name}
                        </span>
                        <strong>{card.value}</strong>
                      </div>
                    ))}
                  </div>
                  <section className="chart-card">
                    <header>
                      <h2>{t("工作流动趋势", "Work over time")}</h2>
                      <Badge>{t("最近 30 天", "Last 30 days")}</Badge>
                    </header>
                    <div className="chart-legend">
                      <span>
                        <i className="legend-created" />
                        {t("新建工作项", "Created")}
                      </span>
                      <span>
                        <i className="legend-completed" />
                        {t("完成工作项", "Completed")}
                      </span>
                    </div>
                    <div className="trend-chart">
                      <div className="chart-y-labels">
                        <span>{maxTrend}</span>
                        <span>{Math.round(maxTrend / 2)}</span>
                        <span>0</span>
                      </div>
                      <svg
                        viewBox="0 0 650 185"
                        role="img"
                        aria-label={t(
                          "最近三十天工作项创建和完成趋势",
                          "Created and completed work items over the last thirty days",
                        )}
                      >
                        <line
                          x1="0"
                          x2="640"
                          y1="30"
                          y2="30"
                          className="chart-gridline"
                        />
                        <line
                          x1="0"
                          x2="640"
                          y1="95"
                          y2="95"
                          className="chart-gridline"
                        />
                        <line
                          x1="0"
                          x2="640"
                          y1="160"
                          y2="160"
                          className="chart-gridline"
                        />
                        <polyline
                          points={trendLine("created")}
                          className="trend-created"
                        />
                        <polyline
                          points={trendLine("completed")}
                          className="trend-completed"
                        />
                        {stats.trend.map((day, index) => (
                          <g key={day.date}>
                            <circle
                              cx={
                                (index / Math.max(1, stats.trend.length - 1)) *
                                640
                              }
                              cy={160 - (day.created / maxTrend) * 130}
                              r="4"
                              className="trend-point"
                            >
                              <title>
                                {day.date}: {day.created} {t("新建", "created")}
                                , {day.completed} {t("完成", "completed")}
                              </title>
                            </circle>
                            {index % 7 === 0 && (
                              <text
                                x={
                                  (index /
                                    Math.max(1, stats.trend.length - 1)) *
                                  640
                                }
                                y="182"
                                className="chart-axis-text"
                              >
                                {day.date.slice(5)}
                              </text>
                            )}
                          </g>
                        ))}
                      </svg>
                    </div>
                  </section>
                  <div className="analytics-columns">
                    <section className="chart-card">
                      <header>
                        <h2>{t("状态分布", "Work by state")}</h2>
                        <BarChart3 size={16} />
                      </header>
                      <div className="state-distribution">
                        {stats.by_state.map((state) => (
                          <div key={state.state_id}>
                            <span>
                              <i style={{ backgroundColor: state.color }} />
                              {state.name}
                            </span>
                            <div className="distribution-track">
                              <i
                                style={{
                                  width: `${(state.count / Math.max(1, stats.total)) * 100}%`,
                                  backgroundColor: state.color,
                                }}
                              />
                            </div>
                            <strong>{state.count}</strong>
                          </div>
                        ))}
                        {!stats.by_state.length && (
                          <p className="text-muted">
                            {t(
                              "添加工作项后显示状态分布。",
                              "State distribution will appear once work items are added.",
                            )}
                          </p>
                        )}
                      </div>
                    </section>
                    <section className="chart-card">
                      <header>
                        <h2>{t("项目进展", "Project progress")}</h2>
                        <FolderKanban size={16} />
                      </header>
                      <div className="project-progress-list">
                        {stats.by_project.map((project) => (
                          <Link
                            key={project.project_id}
                            to={`/w/${workspace.slug}/projects/${project.project_id}/issues`}
                          >
                            <div>
                              <strong>{project.name}</strong>
                              <span>
                                {project.completed} / {project.total}
                              </span>
                            </div>
                            <div className="progress-track">
                              <i
                                style={{
                                  width: `${(project.completed / Math.max(1, project.total)) * 100}%`,
                                }}
                              />
                            </div>
                          </Link>
                        ))}
                        {!stats.by_project.length && (
                          <p className="text-muted">
                            {t(
                              "项目进展会随着工作推进而呈现。",
                              "Project progress will appear as your work moves forward.",
                            )}
                          </p>
                        )}
                      </div>
                    </section>
                  </div>
                </>
              )
            )}
          </>
        )}
      </div>
    </div>
  );
});
