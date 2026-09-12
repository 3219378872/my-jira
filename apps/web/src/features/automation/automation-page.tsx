import { useEffect, useRef, useState } from "react";
import { observer } from "mobx-react-lite";
import { useSearchParams } from "react-router-dom";
import { Bot, LockKeyhole, RefreshCw, ShieldCheck } from "lucide-react";
import {
  Badge,
  Button,
  EmptyState,
  ErrorBox,
  Loading,
  PageHeader,
} from "../../components/ui";
import { projectPath } from "../../lib/api";
import { useRemote, useScope } from "../../lib/hooks";
import { appStore } from "../../stores/app-store";
import type { Project, Workspace } from "../../types";
import { ForecastPanel, ImprovementPanel, RiskPanel } from "./analytics-panels";
import type { AutomationPolicy, AutomationTab } from "./contracts";
import { PolicyPanel } from "./policy-panel";
import { QualityPanel } from "./quality-panel";
import { RunPanel } from "./run-panel";
import "./automation.css";

const tabs: { key: AutomationTab; zh: string; en: string }[] = [
  { key: "runs", zh: "运行与能力", en: "Runs & capabilities" },
  { key: "policy", zh: "授权策略", en: "Policy" },
  { key: "forecasts", zh: "交付预测", en: "Forecasts" },
  { key: "risks", zh: "风险应对", en: "Risks" },
  { key: "quality", zh: "质量与 GitHub", en: "Quality & GitHub" },
  { key: "improvements", zh: "效率改善", en: "Improvements" },
];

export const AutomationPage = observer(function AutomationPage() {
  const { workspace, project } = useScope();
  if (!project)
    return (
      <ErrorBox
        message={appStore.t("请先选择项目。", "Select a project first.")}
      />
    );
  return (
    <AutomationScreen
      key={`${workspace.id}:${project.id}`}
      workspace={workspace}
      project={project}
    />
  );
});

const AutomationScreen = observer(function AutomationScreen({
  workspace,
  project,
}: {
  workspace: Workspace;
  project: Project;
}) {
  const t = appStore.t;
  const base = projectPath(workspace.id, project.id);
  const route = `/w/${workspace.slug}/projects/${project.id}`;
  const effectiveRole =
    workspace.role === 5
      ? 5
      : workspace.role === 20
        ? 20
        : (project.role ?? workspace.role);
  const canRead = effectiveRole >= 15;
  const isAdmin = effectiveRole === 20;
  const canManage = isAdmin && !project.archived_at;
  const visibleTabs = isAdmin
    ? tabs
    : tabs.filter((item) => item.key !== "policy");
  const [refreshKey, setRefreshKey] = useState(0);
  const [accessLost, setAccessLost] = useState(false);
  const [authorizationEpoch, setAuthorizationEpoch] = useState(0);
  const cursor = useRef(0);
  const [params, setParams] = useSearchParams();
  const tab = visibleTabs.some((item) => item.key === params.get("tab"))
    ? (params.get("tab") as AutomationTab)
    : "runs";
  const select = (key: AutomationTab) => {
    const next = new URLSearchParams(params);
    next.set("tab", key);
    setParams(next, { replace: true });
  };
  const policy = useRemote<AutomationPolicy>(
    isAdmin ? `${base}/automation/policy` : null,
  );
  const capabilities = useRemote<
    Pick<AutomationPolicy, "enabled" | "allowed_kinds">
  >(canRead ? `${base}/automation/capabilities` : null);
  const refresh = () => {
    policy.refresh();
    capabilities.refresh();
    setRefreshKey((value) => value + 1);
  };
  const props = {
    base,
    route,
    canManage,
    canEdit: canRead && !project.archived_at,
    refreshKey,
  };
  useEffect(() => {
    if (!canRead || accessLost) return;
    const stream = new EventSource(
      `/api/v1${base}/requirements/events?cursor=${cursor.current}`,
      { withCredentials: true },
    );
    let timer: number | undefined;
    const invalidate = () => {
      if (timer !== undefined) window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        policy.refresh();
        capabilities.refresh();
        setRefreshKey((value) => value + 1);
      }, 150);
    };
    const receive = (event: MessageEvent, type: string) => {
      let payload: { revision?: number; reconnect?: boolean } = {};
      try {
        payload = JSON.parse(event.data || "{}");
      } catch {
        invalidate();
        return;
      }
      if (payload.revision && Number.isSafeInteger(payload.revision))
        cursor.current = Math.max(cursor.current, payload.revision);
      if (type === "authorization_changed") {
        stream.close();
        policy.setResult(null);
        capabilities.setResult(null);
        if (payload.reconnect) {
          policy.refresh();
          capabilities.refresh();
          setRefreshKey((value) => value + 1);
          setAuthorizationEpoch((value) => value + 1);
        } else setAccessLost(true);
        return;
      }
      if (type === "cursor_expired") {
        stream.close();
        policy.refresh();
        capabilities.refresh();
        setRefreshKey((value) => value + 1);
        setAuthorizationEpoch((value) => value + 1);
        return;
      }
      invalidate();
    };
    for (const type of ["change", "authorization_changed", "cursor_expired"])
      stream.addEventListener(type, (event) =>
        receive(event as MessageEvent, type),
      );
    return () => {
      stream.close();
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [
    base,
    canRead,
    accessLost,
    authorizationEpoch,
    policy.refresh,
    capabilities.refresh,
  ]);
  if (accessLost)
    return (
      <EmptyState
        icon={<LockKeyhole size={24} />}
        title={t("访问权限已变化", "Your access has changed")}
        description={t(
          "已清除本页的运行、输入与质量证据。重新加载以获取当前授权范围。",
          "Runs, inputs, and quality evidence have been cleared from this page. Reload to obtain your current authorized scope.",
        )}
        action={
          <Button onClick={() => window.location.reload()}>
            {t("重新加载", "Reload")}
          </Button>
        }
      />
    );
  if (!canRead)
    return (
      <EmptyState
        icon={<LockKeyhole size={24} />}
        title={t("需要项目成员权限", "Project membership required")}
        description={t(
          "自动化输入和质量证据仅供获授权的项目成员查看。访客无法访问。",
          "Automation inputs and quality evidence are available to authorized project members. Guests cannot access them.",
        )}
      />
    );
  return (
    <div className="page-scroll automation-page">
      <PageHeader
        eyebrow={project.name}
        title={t("项目自动化", "Project automation")}
        description={t(
          "从需求拆解到持续改善，保留每次自动动作的授权、来源与结果。",
          "From PRD decomposition to continuous improvement, retain the authorization, sources, and outcome of every automated action.",
        )}
        actions={
          <>
            {capabilities.data && (
              <Badge
                className={
                  capabilities.data.enabled ? "automation-status-positive" : ""
                }
              >
                <ShieldCheck size={13} />
                {capabilities.data.enabled
                  ? t("策略已启用", "Policy enabled")
                  : t("策略未启用", "Policy disabled")}
              </Badge>
            )}
            <Button size="sm" onClick={refresh}>
              <RefreshCw size={14} />
              {t("刷新", "Refresh")}
            </Button>
          </>
        }
      />
      <div className="automation-content">
        <ErrorBox
          message={policy.error || capabilities.error}
          retry={refresh}
        />
        {(policy.loading && !policy.data) ||
        (capabilities.loading && !capabilities.data) ? (
          <Loading />
        ) : (!isAdmin || policy.data) &&
          capabilities.data &&
          ![policy.errorStatus, capabilities.errorStatus].some((status) =>
            [401, 403, 404].includes(status ?? 0),
          ) ? (
          <>
            {!canManage && (
              <p className="automation-permission">
                <LockKeyhole size={14} />
                {project.archived_at
                  ? t(
                      "项目已归档，自动化配置和动作只读。",
                      "The project is archived. Automation configuration and actions are read-only.",
                    )
                  : t(
                      "可运行管理员已授权的能力并查看证据；策略、撤销与应对动作由管理员管理。",
                      "You can run administrator-authorized capabilities and review evidence. Administrators manage policy, undo, and response actions.",
                    )}
              </p>
            )}
            <div
              className="automation-tabs"
              role="tablist"
              aria-label={t("自动化视图", "Automation views")}
            >
              {visibleTabs.map((item) => (
                <button
                  type="button"
                  role="tab"
                  key={item.key}
                  id={`automation-tab-${item.key}`}
                  aria-selected={item.key === tab}
                  aria-controls={`automation-panel-${item.key}`}
                  className={item.key === tab ? "is-active" : ""}
                  onClick={() => select(item.key)}
                >
                  {item.key === "runs" && <Bot size={15} />}
                  {t(item.zh, item.en)}
                </button>
              ))}
            </div>
            <div
              role="tabpanel"
              id={`automation-panel-${tab}`}
              aria-labelledby={`automation-tab-${tab}`}
              className="automation-tab-panel"
              key={authorizationEpoch}
            >
              {tab === "runs" && (
                <RunPanel
                  base={base}
                  policy={capabilities.data}
                  canManage={canManage}
                  canRun={canRead && !project.archived_at}
                  refreshKey={refreshKey}
                  onQuality={() => select("quality")}
                  onPolicy={() => select("policy")}
                />
              )}
              {tab === "policy" && policy.data && (
                <PolicyPanel
                  base={base}
                  policy={policy.data}
                  canManage={canManage}
                  onSaved={refresh}
                />
              )}
              {tab === "forecasts" && <ForecastPanel {...props} />}
              {tab === "risks" && (
                <RiskPanel
                  {...props}
                  inboxRoute={`/w/${workspace.slug}/inbox`}
                />
              )}
              {tab === "quality" && <QualityPanel {...props} />}
              {tab === "improvements" && <ImprovementPanel {...props} />}
            </div>
          </>
        ) : null}
      </div>
    </div>
  );
});
