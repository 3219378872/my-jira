import {
  Component,
  Suspense,
  lazy,
  type ErrorInfo,
  type ReactNode,
} from "react";
import { createRoot } from "react-dom/client";
import {
  BrowserRouter,
  Link,
  Navigate,
  Outlet,
  Route,
  Routes,
} from "react-router-dom";
import { observer } from "mobx-react-lite";
import * as TooltipPrimitive from "@radix-ui/react-tooltip";
import { AlertTriangle, Compass, Shield } from "lucide-react";
import { appStore } from "./stores/app-store";
import { AuthPage, CreateWorkspacePage } from "./features/auth";
import { AdminLayout, WorkspaceLayout } from "./components/layout";
import { Button, EmptyState, ErrorBox, Loading, Toasts } from "./components/ui";
import "./styles.css";

const HomePage = lazy(() =>
  import("./features/workspace").then((module) => ({
    default: module.HomePage,
  })),
);
const InboxPage = lazy(() =>
  import("./features/workspace").then((module) => ({
    default: module.InboxPage,
  })),
);
const MyWorkPage = lazy(() =>
  import("./features/workspace").then((module) => ({
    default: module.MyWorkPage,
  })),
);
const AnalyticsPage = lazy(() =>
  import("./features/workspace").then((module) => ({
    default: module.AnalyticsPage,
  })),
);
const MemberProfilePage = lazy(() =>
  import("./features/profile").then((module) => ({
    default: module.MemberProfilePage,
  })),
);
const ProjectsPage = lazy(() =>
  import("./features/projects").then((module) => ({
    default: module.ProjectsPage,
  })),
);
const IssuesPage = lazy(() =>
  import("./features/issues/issue-list").then((module) => ({
    default: module.IssuesPage,
  })),
);
const PlanningPage = lazy(() =>
  import("./features/planning").then((module) => ({
    default: module.PlanningPage,
  })),
);
const RequirementsPage = lazy(() =>
  import("./features/requirements/requirements-page").then((module) => ({
    default: module.RequirementsPage,
  })),
);
const AutomationPage = lazy(() =>
  import("./features/automation/automation-page").then((module) => ({
    default: module.AutomationPage,
  })),
);
const ViewsPage = lazy(() =>
  import("./features/planning").then((module) => ({
    default: module.ViewsPage,
  })),
);
const PagesPage = lazy(() =>
  import("./features/pages").then((module) => ({ default: module.PagesPage })),
);
const SettingsPage = lazy(() =>
  import("./features/settings").then((module) => ({
    default: module.SettingsPage,
  })),
);
const AdminPage = lazy(() =>
  import("./features/admin").then((module) => ({ default: module.AdminPage })),
);
const PublicPage = lazy(() =>
  import("./features/public").then((module) => ({
    default: module.PublicPage,
  })),
);
const IntakePage = lazy(() =>
  import("./features/public").then((module) => ({
    default: module.IntakePage,
  })),
);
const AuthRecoveryPage = lazy(() =>
  import("./features/auth-recovery").then((module) => ({
    default: module.AuthRecoveryPage,
  })),
);
const WorkItemReferenceRedirect = lazy(() =>
  import("./features/work-item-links").then((module) => ({
    default: module.WorkItemReferenceRedirect,
  })),
);
const WorkItemIdentifierRedirect = lazy(() =>
  import("./features/work-item-links").then((module) => ({
    default: module.WorkItemIdentifierRedirect,
  })),
);

const Protected = observer(function Protected() {
  return appStore.user ? (
    <Outlet />
  ) : (
    <Navigate
      to={`/login?next=${encodeURIComponent(window.location.pathname)}`}
      replace
    />
  );
});
const AdminProtected = observer(function AdminProtected() {
  return appStore.user?.is_instance_admin ? (
    <AdminLayout />
  ) : (
    <EmptyState
      icon={<Shield size={30} />}
      title={appStore.t("需要管理员权限", "Administrator access required")}
      description={appStore.t(
        "当前账户没有实例管理权限。",
        "Your account does not have instance administration access.",
      )}
      action={
        <Link to="/">{appStore.t("返回工作区", "Back to workspace")}</Link>
      }
    />
  );
});
const StartPage = observer(function StartPage() {
  if (!appStore.instance?.is_setup_done)
    return <Navigate to="/setup" replace />;
  if (!appStore.user) return <Navigate to="/login" replace />;
  const last = appStore.lastVisited;
  const first =
    (last?.workspace_id ? appStore.workspaces.get(last.workspace_id) : null) ??
    [...appStore.workspaces.values()][0];
  return (
    <Navigate
      to={
        first
          ? `/w/${first.slug}/${last?.workspace_id === first.id && last.project_id ? `projects/${last.project_id}/issues` : "home"}`
          : "/new-workspace"
      }
      replace
    />
  );
});

class ErrorBoundary extends Component<
  { children: ReactNode },
  { error: string }
> {
  state = { error: "" };
  static getDerivedStateFromError(error: Error) {
    return { error: error.message };
  }
  componentDidCatch(_error: Error, _info: ErrorInfo) {
    /* Keep user content and credentials out of diagnostic logs. */
  }
  render() {
    if (this.state.error)
      return (
        <div className="not-found">
          <EmptyState
            icon={<AlertTriangle size={30} />}
            title={appStore.t(
              "这个页面遇到了问题",
              "This page encountered a problem",
            )}
            description={this.state.error}
            action={
              <Button onClick={() => window.location.reload()}>
                {appStore.t("重新加载", "Reload page")}
              </Button>
            }
          />
        </div>
      );
    return this.props.children;
  }
}

const App = observer(function App() {
  if (!appStore.ready) return <Loading full />;
  if (appStore.bootError)
    return (
      <div className="workspace-onboarding">
        <h1>{appStore.t("暂时无法连接工作区", "Unable to connect")}</h1>
        <ErrorBox message={appStore.bootError} retry={appStore.bootstrap} />
      </div>
    );
  return (
    <TooltipPrimitive.Provider delayDuration={400}>
      <BrowserRouter>
        <ErrorBoundary>
          <Suspense fallback={<Loading />}>
            <Routes>
              <Route path="/" element={<StartPage />} />
              <Route path="/login" element={<AuthPage kind="login" />} />
              <Route path="/register" element={<AuthPage kind="register" />} />
              <Route path="/setup" element={<AuthPage kind="setup" />} />
              <Route
                path="/forgot-password"
                element={<AuthRecoveryPage kind="forgot" />}
              />
              <Route
                path="/reset-password"
                element={<AuthRecoveryPage kind="reset" />}
              />
              <Route
                path="/magic-link"
                element={<AuthRecoveryPage kind="magic" />}
              />
              <Route
                path="/verify-email"
                element={<AuthRecoveryPage kind="verify" />}
              />
              <Route path="/public/:siteSlug" element={<PublicPage />} />
              <Route
                path="/public/:siteSlug/issues/:publicIssueId"
                element={<PublicPage />}
              />
              <Route element={<Protected />}>
                <Route
                  path="/go/work-item/:workspaceId/:projectId/:issueId"
                  element={<WorkItemReferenceRedirect />}
                />
                <Route
                  path="/new-workspace"
                  element={<CreateWorkspacePage />}
                />
                <Route
                  path="/invitations/:invitationToken"
                  element={<AuthRecoveryPage kind="invitation" />}
                />
                <Route path="/w/:workspaceSlug" element={<WorkspaceLayout />}>
                  <Route index element={<Navigate to="home" replace />} />
                  <Route path="home" element={<HomePage />} />
                  <Route
                    path="browse/:identifier"
                    element={<WorkItemIdentifierRedirect />}
                  />
                  <Route path="inbox" element={<InboxPage />} />
                  <Route path="my-work" element={<MyWorkPage />} />
                  <Route path="analytics" element={<AnalyticsPage />} />
                  <Route
                    path="members/:userId"
                    element={<MemberProfilePage />}
                  />
                  <Route path="projects" element={<ProjectsPage />} />
                  <Route path="views" element={<ViewsPage />} />
                  <Route path="views/:viewId" element={<ViewsPage />} />
                  <Route path="pages" element={<PagesPage />} />
                  <Route path="pages/:pageId" element={<PagesPage />} />
                  <Route path="settings/*" element={<SettingsPage />} />
                  <Route
                    path="projects/:projectId/issues"
                    element={<IssuesPage />}
                  />
                  <Route
                    path="projects/:projectId/issues/:issueId"
                    element={<IssuesPage />}
                  />
                  <Route
                    path="projects/:projectId/archives"
                    element={<IssuesPage archived />}
                  />
                  <Route
                    path="projects/:projectId/requirements"
                    element={<Navigate to="story-map" replace />}
                  />
                  <Route
                    path="projects/:projectId/requirements/:requirementView"
                    element={<RequirementsPage />}
                  />
                  <Route
                    path="projects/:projectId/automation"
                    element={<AutomationPage />}
                  />
                  <Route
                    path="projects/:projectId/cycles"
                    element={<PlanningPage kind="cycles" />}
                  />
                  <Route
                    path="projects/:projectId/cycles/:planningId"
                    element={<PlanningPage kind="cycles" />}
                  />
                  <Route
                    path="projects/:projectId/modules"
                    element={<PlanningPage kind="modules" />}
                  />
                  <Route
                    path="projects/:projectId/modules/:planningId"
                    element={<PlanningPage kind="modules" />}
                  />
                  <Route
                    path="projects/:projectId/views"
                    element={<ViewsPage />}
                  />
                  <Route
                    path="projects/:projectId/views/:viewId"
                    element={<ViewsPage />}
                  />
                  <Route
                    path="projects/:projectId/pages"
                    element={<PagesPage />}
                  />
                  <Route
                    path="projects/:projectId/pages/:pageId"
                    element={<PagesPage />}
                  />
                  <Route
                    path="projects/:projectId/intake"
                    element={<IntakePage />}
                  />
                  <Route
                    path="projects/:projectId/settings/*"
                    element={<SettingsPage />}
                  />
                </Route>
                <Route path="/admin" element={<AdminProtected />}>
                  <Route index element={<AdminPage />} />
                  <Route path=":adminSection" element={<AdminPage />} />
                </Route>
              </Route>
              <Route
                path="*"
                element={
                  <div className="not-found">
                    <EmptyState
                      icon={<Compass size={30} />}
                      title={appStore.t(
                        "这条路还没有通往这里",
                        "This page could not be found",
                      )}
                      description={appStore.t(
                        "检查地址，或回到你的工作区继续探索。",
                        "Check the address or return to your workspace.",
                      )}
                      action={
                        <Link to="/">
                          <Button>{appStore.t("返回首页", "Go home")}</Button>
                        </Link>
                      }
                    />
                  </div>
                }
              />
            </Routes>
          </Suspense>
        </ErrorBoundary>
      </BrowserRouter>
      <Toasts />
    </TooltipPrimitive.Provider>
  );
});

void appStore.bootstrap();
createRoot(document.getElementById("root")!).render(<App />);
