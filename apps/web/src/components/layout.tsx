import {
  lazy,
  Suspense,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { observer } from "mobx-react-lite";
import {
  Link,
  NavLink,
  Outlet,
  useLocation,
  useMatch,
  useNavigate,
  useParams,
} from "react-router-dom";
import {
  Archive,
  ArrowLeft,
  ArrowUpRight,
  BarChart3,
  Bell,
  BookOpen,
  Boxes,
  ChevronDown,
  ChevronRight,
  CircleHelp,
  Command,
  FileText,
  FolderKanban,
  Home,
  Inbox,
  LayoutGrid,
  ListTodo,
  LogOut,
  MoreHorizontal,
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  Search,
  Settings,
  Shield,
  Sparkles,
  UserRound,
  Users,
  X,
} from "lucide-react";
import { appStore } from "../stores/app-store";
import { ScopeContext } from "../lib/hooks";
import { api, errorMessage, workspacePath } from "../lib/api";
import type { WorkItem } from "../types";
import {
  Avatar,
  Button,
  EmptyState,
  ErrorBox,
  Input,
  Loading,
  Menu,
  Modal,
} from "./ui";
import { AppearanceControls, Brand } from "../features/auth";
import { SidebarProjects } from "./sidebar-projects";
const ProjectForm = lazy(() =>
  import("../features/projects").then((module) => ({
    default: module.ProjectForm,
  })),
);
const IssueForm = lazy(() =>
  import("../features/issues/issue-form").then((module) => ({
    default: module.IssueForm,
  })),
);
import { cn } from "../lib/utils";

const navItems = [
  { path: "home", zh: "首页", en: "Home", icon: Home },
  { path: "inbox", zh: "收件箱", en: "Inbox", icon: Inbox },
  { path: "my-work", zh: "我的工作", en: "My work", icon: ListTodo },
  { path: "projects", zh: "项目", en: "Projects", icon: FolderKanban },
  { path: "views", zh: "视图", en: "Views", icon: LayoutGrid },
  { path: "pages", zh: "知识文档", en: "Documents", icon: BookOpen },
  { path: "analytics", zh: "数据分析", en: "Analytics", icon: BarChart3 },
];

const projectItems = [
  { path: "issues", zh: "工作项", en: "Work items", icon: ListTodo },
  {
    path: "requirements",
    zh: "需求工作台",
    en: "Requirements",
    icon: LayoutGrid,
  },
  {
    path: "automation",
    zh: "AI 与自动化",
    en: "AI and automation",
    icon: Sparkles,
  },
  { path: "cycles", zh: "迭代周期", en: "Cycles", icon: Sparkles },
  { path: "modules", zh: "功能模块", en: "Modules", icon: Boxes },
  { path: "views", zh: "项目视图", en: "Views", icon: LayoutGrid },
  { path: "pages", zh: "项目文档", en: "Documents", icon: FileText },
  { path: "intake", zh: "需求收集", en: "Intake", icon: Inbox },
  { path: "archives", zh: "归档", en: "Archive", icon: Archive },
  { path: "settings", zh: "项目设置", en: "Settings", icon: Settings },
];

function useMobileNavigation() {
  const [mobile, setMobile] = useState(
    () => window.matchMedia("(max-width: 767px)").matches,
  );
  const [open, setOpen] = useState(false);
  const sidebarRef = useRef<HTMLElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const location = useLocation();
  useEffect(() => {
    const media = window.matchMedia("(max-width: 767px)");
    const changed = () => {
      setMobile(media.matches);
      setOpen(false);
    };
    media.addEventListener("change", changed);
    return () => media.removeEventListener("change", changed);
  }, []);
  useEffect(() => setOpen(false), [location.pathname]);
  useEffect(() => {
    if (!mobile || !open) return;
    const sidebar = sidebarRef.current;
    const focusables = () =>
      [
        ...(sidebar?.querySelectorAll<HTMLElement>(
          'button:not([disabled]), a[href], input:not([disabled]), [tabindex="0"]',
        ) ?? []),
      ].filter((element) => element.getClientRects().length > 0);
    focusables()[0]?.focus();
    const keydown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
        return;
      }
      if (event.key !== "Tab") return;
      const elements = focusables();
      const first = elements[0],
        last = elements.at(-1);
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last?.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first?.focus();
      }
    };
    window.addEventListener("keydown", keydown);
    return () => {
      window.removeEventListener("keydown", keydown);
      triggerRef.current?.focus();
    };
  }, [mobile, open]);
  return { mobile, open, setOpen, sidebarRef, triggerRef };
}

export const WorkspaceLayout = observer(function WorkspaceLayout() {
  const { workspaceSlug } = useParams();
  const projectMatch = useMatch("/w/:workspaceSlug/projects/:projectId/*");
  const navigate = useNavigate();
  const location = useLocation();
  const [projectsLoaded, setProjectsLoaded] = useState("");
  const [error, setError] = useState("");
  const [helpOpen, setHelpOpen] = useState(false);
  const navigation = useMobileNavigation();
  const workspace = [...appStore.workspaces.values()].find(
    (item) => item.slug === workspaceSlug,
  );
  const project = projectMatch
    ? appStore.projects.get(projectMatch.params.projectId ?? "")
    : undefined;
  const canCreate =
    !!workspace &&
    workspace.role !== 5 &&
    (workspace.role === 20 || (project?.role ?? workspace.role) >= 15) &&
    !project?.archived_at;
  const t = appStore.t;

  useEffect(() => {
    if (!workspace) return;
    setError("");
    appStore
      .loadProjects(workspace.id)
      .then(() => setProjectsLoaded(workspace.id))
      .catch((cause) => setError(errorMessage(cause)));
  }, [workspace?.id]);

  useEffect(() => {
    if (!workspace || (projectMatch && !project)) return;
    appStore
      .rememberLocation(workspace.id, project?.id ?? null)
      .catch(() => {});
  }, [workspace?.id, project?.id, !!projectMatch]);

  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement;
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        appStore.setSearchOpen(!appStore.searchOpen);
        return;
      }
      if (
        target.isContentEditable ||
        ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName) ||
        event.metaKey ||
        event.ctrlKey ||
        event.altKey
      )
        return;
      if (event.key === "c" && project && canCreate)
        appStore.setCreateIssueProject(project.id);
      if (event.key === "[") appStore.toggleSidebar();
    };
    window.addEventListener("keydown", keydown);
    return () => window.removeEventListener("keydown", keydown);
  }, [project, canCreate]);

  if (!workspace)
    return (
      <EmptyState
        icon={<FolderKanban size={30} />}
        title={t("找不到这个工作区", "Workspace not found")}
        description={t(
          "工作区可能已被移除，或你的访问权限已变更。",
          "The workspace may have been removed or your access may have changed.",
        )}
        action={
          <Button onClick={() => navigate("/")}>
            {t("返回首页", "Go home")}
          </Button>
        }
      />
    );

  const workspaceBase = `/w/${workspace.slug}`;
  const activePath =
    location.pathname.split("/").filter(Boolean).at(-1) ?? "home";
  const activeNav = [...navItems, ...projectItems].find(
    (item) => item.path === activePath,
  );

  return (
    <ScopeContext.Provider value={{ workspace, project }}>
      <div
        className={cn(
          "app-layout",
          appStore.sidebarCollapsed && "sidebar-is-collapsed",
          navigation.open && "mobile-navigation-open",
        )}
      >
        {navigation.mobile && navigation.open && (
          <button
            className="mobile-navigation-overlay"
            tabIndex={-1}
            aria-label={t("关闭导航", "Close navigation")}
            onClick={() => navigation.setOpen(false)}
          />
        )}
        <aside
          id="workspace-sidebar"
          ref={navigation.sidebarRef}
          className="sidebar"
          inert={navigation.mobile && !navigation.open}
          role={navigation.mobile ? "dialog" : undefined}
          aria-modal={navigation.mobile && navigation.open ? true : undefined}
          aria-label={t("工作区导航", "Workspace navigation")}
        >
          <div className="workspace-switcher">
            <Menu
              items={[
                ...[...appStore.workspaces.values()].map((item) => ({
                  label: item.name,
                  icon: <Avatar name={item.name} size="xs" />,
                  onSelect: () => navigate(`/w/${item.slug}/home`),
                })),
                {
                  label: t("创建工作区", "Create workspace"),
                  icon: <Plus size={15} />,
                  onSelect: () => navigate("/new-workspace"),
                  separator: true,
                },
              ]}
            >
              <button className="workspace-switcher-button">
                <span className="workspace-avatar">
                  {workspace.name.slice(0, 1).toUpperCase()}
                </span>
                <span className="workspace-name">{workspace.name}</span>
                <ChevronDown size={14} />
              </button>
            </Menu>
            <Button
              variant="ghost"
              size="icon"
              onClick={() =>
                navigation.mobile
                  ? navigation.setOpen(false)
                  : appStore.toggleSidebar()
              }
              aria-label={t("折叠侧栏", "Collapse sidebar")}
              className="sidebar-collapse"
            >
              <PanelLeftClose size={16} />
            </Button>
          </div>
          <div className="sidebar-quick-actions">
            <button
              className="sidebar-search"
              onClick={() => appStore.setSearchOpen(true)}
            >
              <Search size={15} />
              <span>{t("搜索", "Search")}</span>
              <kbd>⌘ K</kbd>
            </button>
            {canCreate && (
              <Button
                size="icon"
                onClick={() =>
                  project
                    ? appStore.setCreateIssueProject(project.id)
                    : appStore.setCreateProjectOpen(true)
                }
                aria-label={
                  project
                    ? t("新建工作项", "New work item")
                    : t("新建项目", "New project")
                }
              >
                <Plus size={16} />
              </Button>
            )}
          </div>
          <nav className="main-nav">
            {navItems.map((item) => (
              <NavLink
                key={item.path}
                to={`${workspaceBase}/${item.path}`}
                end={item.path === "projects"}
                className={({ isActive }) =>
                  cn("nav-link", isActive && "active")
                }
              >
                <item.icon size={16} />
                <span>{t(item.zh, item.en)}</span>
              </NavLink>
            ))}
          </nav>
          <SidebarProjects
            loaded={projectsLoaded === workspace.id}
            items={projectItems}
          />
          <div className="sidebar-bottom">
            <NavLink
              className={({ isActive }) => cn("nav-link", isActive && "active")}
              to={`${workspaceBase}/settings`}
            >
              <Settings size={16} />
              <span>{t("工作区设置", "Workspace settings")}</span>
            </NavLink>
            <div className="sidebar-user">
              <Menu
                items={[
                  {
                    label: t("个人设置", "My settings"),
                    icon: <UserRound size={15} />,
                    onSelect: () =>
                      navigate(`${workspaceBase}/settings/profile`),
                  },
                  {
                    label: t("我的主页", "My profile"),
                    icon: <UserRound size={14} />,
                    onSelect: () => navigate(`${workspaceBase}/members/me`),
                  },
                  ...(appStore.user?.is_instance_admin
                    ? [
                        {
                          label: t("实例管理", "Instance administration"),
                          icon: <Shield size={15} />,
                          onSelect: () => navigate("/admin"),
                        },
                      ]
                    : []),
                  {
                    label: t("退出登录", "Sign out"),
                    icon: <LogOut size={15} />,
                    separator: true,
                    onSelect: () => {
                      appStore
                        .logout()
                        .then(() => navigate("/login"))
                        .catch((cause) =>
                          appStore.notify(errorMessage(cause), "error"),
                        );
                    },
                  },
                ]}
              >
                <button className="user-menu-trigger">
                  <Avatar
                    name={appStore.user?.display_name ?? ""}
                    src={appStore.user?.avatar_url}
                    size="sm"
                  />
                  <span>{appStore.user?.display_name}</span>
                  <MoreHorizontal size={16} />
                </button>
              </Menu>
              <button
                className="help-button"
                onClick={() => setHelpOpen(true)}
                aria-label={t("帮助与快捷键", "Help and shortcuts")}
              >
                <CircleHelp size={17} />
              </button>
            </div>
          </div>
        </aside>
        <div className="app-main" inert={navigation.mobile && navigation.open}>
          <header className="topbar">
            <div className="breadcrumbs">
              <Button
                ref={navigation.triggerRef}
                className="mobile-navigation-trigger"
                size="icon"
                variant="ghost"
                aria-label={t("打开导航", "Open navigation")}
                aria-expanded={navigation.open}
                aria-controls="workspace-sidebar"
                onClick={() => navigation.setOpen(true)}
              >
                <PanelLeftOpen size={18} />
              </Button>
              {appStore.sidebarCollapsed && (
                <Button
                  variant="ghost"
                  size="icon"
                  className="desktop-navigation-trigger"
                  onClick={appStore.toggleSidebar}
                  aria-label={t("展开侧栏", "Expand sidebar")}
                >
                  <PanelLeftOpen size={17} />
                </Button>
              )}
              <Link to={`${workspaceBase}/home`}>{workspace.name}</Link>
              <ChevronRight size={13} />
              {project && (
                <>
                  <Link to={`${workspaceBase}/projects/${project.id}/issues`}>
                    <span
                      className="project-color"
                      style={{ backgroundColor: project.color || "#6875cf" }}
                    />
                    {project.name}
                  </Link>
                  <ChevronRight size={13} />
                </>
              )}
              <span>
                {activeNav
                  ? t(activeNav.zh, activeNav.en)
                  : activePath === "profile"
                    ? t("个人设置", "My settings")
                    : t("详情", "Details")}
              </span>
            </div>
            <div className="topbar-actions">
              <AppearanceControls />
              <Button
                size="icon"
                variant="ghost"
                aria-label={t("通知", "Notifications")}
                onClick={() => navigate(`${workspaceBase}/inbox`)}
              >
                <Bell size={16} />
              </Button>
              <Avatar name={appStore.user?.display_name ?? ""} size="xs" />
            </div>
          </header>
          <main className="content-area">
            {error ? (
              <ErrorBox
                message={error}
                retry={() => {
                  appStore
                    .loadProjects(workspace.id)
                    .then(() => {
                      setError("");
                      setProjectsLoaded(workspace.id);
                    })
                    .catch((cause) => setError(errorMessage(cause)));
                }}
              />
            ) : projectMatch && projectsLoaded !== workspace.id ? (
              <Loading />
            ) : projectMatch && !project ? (
              <EmptyState
                icon={<FolderKanban size={30} />}
                title={t("项目不可用", "Project unavailable")}
                description={t(
                  "请确认你仍然是项目成员。",
                  "Check that you still have access to this project.",
                )}
              />
            ) : (
              <Outlet />
            )}
          </main>
        </div>
      </div>
      <Suspense fallback={null}>
        {appStore.createProjectOpen && (
          <ProjectForm open onOpenChange={appStore.setCreateProjectOpen} />
        )}
        {appStore.createIssueProjectId && (
          <IssueForm
            projectId={appStore.createIssueProjectId}
            onClose={() => appStore.setCreateIssueProject(null)}
          />
        )}
      </Suspense>
      <SearchDialog workspaceId={workspace.id} workspaceSlug={workspace.slug} />
      <Modal
        open={helpOpen}
        onOpenChange={setHelpOpen}
        title={t("为专注而设计", "Designed for focus")}
      >
        <div className="help-content">
          <Brand />
          <p>
            {t(
              "使用快捷键更快地浏览与创建。",
              "Navigate and create with keyboard shortcuts.",
            )}
          </p>
          <div className="shortcut-row">
            <span>{t("搜索工作区", "Search workspace")}</span>
            <kbd>⌘ / Ctrl + K</kbd>
          </div>
          <div className="shortcut-row">
            <span>
              {t("在当前项目创建工作项", "Create a work item in this project")}
            </span>
            <kbd>C</kbd>
          </div>
          <div className="shortcut-row">
            <span>{t("展开或折叠侧栏", "Toggle sidebar")}</span>
            <kbd>[</kbd>
          </div>
          <div className="shortcut-row">
            <span>{t("关闭弹窗", "Close dialog")}</span>
            <kbd>Esc</kbd>
          </div>
        </div>
      </Modal>
    </ScopeContext.Provider>
  );
});

const SearchDialog = observer(function SearchDialog({
  workspaceId,
  workspaceSlug,
}: {
  workspaceId: string;
  workspaceSlug: string;
}) {
  const [query, setQuery] = useState("");
  const [items, setItems] = useState<WorkItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const navigate = useNavigate();
  const t = appStore.t;

  useEffect(() => {
    if (!appStore.searchOpen || !query.trim()) {
      setItems([]);
      return;
    }
    const controller = new AbortController();
    const timer = setTimeout(() => {
      setLoading(true);
      setError("");
      api
        .get<WorkItem[]>(
          `${workspacePath(workspaceId)}/issues?search=${encodeURIComponent(query)}&limit=30`,
          controller.signal,
        )
        .then((result) => {
          if (!controller.signal.aborted) setItems(result.data);
        })
        .catch((cause) => {
          if (!controller.signal.aborted) setError(errorMessage(cause));
        })
        .finally(() => {
          if (!controller.signal.aborted) setLoading(false);
        });
    }, 180);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [query, workspaceId, appStore.searchOpen]);

  return (
    <Modal
      open={appStore.searchOpen}
      onOpenChange={appStore.setSearchOpen}
      title={t("搜索工作区", "Search workspace")}
      className="search-dialog"
    >
      <div className="command-input">
        <Search size={19} />
        <Input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder={t(
            "搜索工作项名称或编号…",
            "Search work item names or identifiers…",
          )}
          autoFocus
          aria-label={t("搜索工作项", "Search work items")}
        />
        {query && (
          <Button
            variant="ghost"
            size="icon"
            onClick={() => setQuery("")}
            aria-label={t("清除搜索", "Clear search")}
          >
            <X size={14} />
          </Button>
        )}
      </div>
      <ErrorBox message={error} />
      {loading ? (
        <Loading />
      ) : (
        <div className="search-results">
          {items.map((item) => (
            <button
              key={item.id}
              onClick={() => {
                appStore.setSearchOpen(false);
                navigate(
                  `/w/${workspaceSlug}/projects/${item.project_id}/issues/${item.id}`,
                );
              }}
            >
              <ListTodo size={16} />
              <span className="issue-identifier">
                {appStore.projects.get(item.project_id)?.identifier}-
                {item.sequence_id}
              </span>
              <span>{item.name}</span>
              <ArrowUpRight size={14} />
            </button>
          ))}
          {!query && (
            <div className="command-hint">
              <Command size={22} />
              <p>
                {t(
                  "输入关键词，快速找到下一步工作。",
                  "Type a keyword to find your next piece of work.",
                )}
              </p>
            </div>
          )}
          {query && items.length === 0 && !error && (
            <div className="command-hint">
              <Search size={22} />
              <p>
                {t(
                  "没有匹配的工作项，试试其他关键词。",
                  "No matching work items. Try another keyword.",
                )}
              </p>
            </div>
          )}
        </div>
      )}
    </Modal>
  );
});

export const AdminLayout = observer(function AdminLayout() {
  const t = appStore.t;
  const navigation = useMobileNavigation();
  const items = [
    { path: "", zh: "实例概览", en: "Overview", icon: LayoutGrid },
    { path: "users", zh: "用户管理", en: "Users", icon: Users },
    {
      path: "workspaces",
      zh: "工作区管理",
      en: "Workspaces",
      icon: FolderKanban,
    },
    {
      path: "authentication",
      zh: "身份与安全",
      en: "Authentication",
      icon: Shield,
    },
    { path: "email", zh: "邮件服务", en: "Email", icon: Inbox },
    { path: "storage", zh: "文件存储", en: "Storage", icon: Archive },
    { path: "ai", zh: "AI 写作", en: "AI writing", icon: Sparkles },
    { path: "unsplash", zh: "图片搜索", en: "Image search", icon: Search },
  ];
  return (
    <div
      className={cn(
        "app-layout admin-layout",
        navigation.open && "mobile-navigation-open",
      )}
    >
      {navigation.mobile && navigation.open && (
        <button
          className="mobile-navigation-overlay"
          tabIndex={-1}
          aria-label={t("关闭导航", "Close navigation")}
          onClick={() => navigation.setOpen(false)}
        />
      )}
      <aside
        id="admin-sidebar"
        ref={navigation.sidebarRef}
        className="sidebar"
        inert={navigation.mobile && !navigation.open}
        role={navigation.mobile ? "dialog" : undefined}
        aria-modal={navigation.mobile && navigation.open ? true : undefined}
        aria-label={t("实例管理导航", "Instance navigation")}
      >
        <div className="admin-brand">
          <Brand />
          <BadgeLabel>{t("管理", "ADMIN")}</BadgeLabel>
          <Button
            className="mobile-navigation-trigger"
            size="icon"
            variant="ghost"
            aria-label={t("关闭导航", "Close navigation")}
            onClick={() => navigation.setOpen(false)}
          >
            <X size={16} />
          </Button>
        </div>
        <div className="sidebar-section-heading">
          {t("实例管理", "Instance administration")}
        </div>
        <nav className="main-nav">
          {items.map((item) => (
            <NavLink
              key={item.path}
              to={`/admin${item.path ? `/${item.path}` : ""}`}
              end={!item.path}
              className={({ isActive }) => cn("nav-link", isActive && "active")}
            >
              <item.icon size={16} />
              <span>{t(item.zh, item.en)}</span>
            </NavLink>
          ))}
        </nav>
        <div className="sidebar-bottom">
          <Link to="/" className="nav-link">
            <ArrowLeft size={16} />
            {t("返回工作区", "Back to workspace")}
          </Link>
        </div>
      </aside>
      <div className="app-main" inert={navigation.mobile && navigation.open}>
        <header className="topbar">
          <span className="breadcrumbs">
            <Button
              ref={navigation.triggerRef}
              className="mobile-navigation-trigger"
              size="icon"
              variant="ghost"
              aria-label={t("打开导航", "Open navigation")}
              aria-expanded={navigation.open}
              aria-controls="admin-sidebar"
              onClick={() => navigation.setOpen(true)}
            >
              <PanelLeftOpen size={18} />
            </Button>
            <Shield size={16} />
            {t("实例管理", "Instance administration")}
          </span>
          <AppearanceControls />
        </header>
        <main className="content-area">
          <Outlet />
        </main>
      </div>
    </div>
  );
});

function BadgeLabel({ children }: { children: ReactNode }) {
  return <span className="admin-label">{children}</span>;
}
