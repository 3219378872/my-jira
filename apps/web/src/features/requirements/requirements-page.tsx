import { useEffect, useState } from "react";
import { observer } from "mobx-react-lite";
import {
  Link,
  useNavigate,
  useParams,
  useSearchParams,
} from "react-router-dom";
import {
  Boxes,
  CalendarRange,
  GitBranch,
  Map,
  Plus,
  RefreshCw,
  Search,
  Shield,
  Users,
  X,
} from "lucide-react";
import { runInAction } from "mobx";
import { appStore } from "../../stores/app-store";
import { useScope } from "../../lib/hooks";
import { memberName } from "../../lib/utils";
import { requirementsEnabled } from "../../lib/project-features";
import {
  Button,
  EmptyState,
  ErrorBox,
  Input,
  Loading,
  Select,
} from "../../components/ui";
import type { WorkItem } from "../../types";
import { dateFromDay, dayNumber } from "./semantics";
import { requirementsStore as store } from "./store";
import { StoryMap } from "./story-map";
import { RequirementsGantt } from "./gantt";
import { ConflictPanel, MemberSchedule } from "./members";
import { RequirementEditor } from "./requirement-editor";
import { ScenariosView } from "./scenarios";
import "./requirements.css";

const views = [
  { id: "story-map", zh: "故事地图", en: "Story map", icon: Map },
  { id: "gantt", zh: "层级甘特", en: "Gantt", icon: CalendarRange },
  { id: "members", zh: "成员排期", en: "Members", icon: Users },
  {
    id: "scenarios",
    zh: "业务场景 / UML",
    en: "Scenarios / UML",
    icon: GitBranch,
  },
];

export const RequirementsPage = observer(function RequirementsPage() {
  const { workspace, project } = useScope();
  const { requirementView = "story-map" } = useParams();
  const navigate = useNavigate();
  const [search] = useSearchParams();
  const t = appStore.t;
  const canEdit = store.canEdit;
  const [editor, setEditor] = useState<{
    item?: WorkItem;
    seed?: Partial<WorkItem>;
  } | null>(null);
  const [start, setStart] = useState(new Date().toISOString().slice(0, 10));
  const [end, setEnd] = useState(dateFromDay(dayNumber(start) + 27));
  const route = `/w/${workspace.slug}/projects/${project?.id}/requirements`;
  const cachedRequirementsEnabled = project
    ? requirementsEnabled(project)
    : true;
  useEffect(() => {
    if (!project) return;
    let active = true;
    setEditor(null);
    void store.enter(workspace.id, project.id).then(async () => {
      if (!active || store.revoked) return;
      const selected = search.get("selected");
      if (selected && store.items.has(selected)) store.select(selected);
      await store.setDateRange(start, end);
    });
    return () => {
      active = false;
      store.leave();
    };
  }, [workspace.id, project?.id]);
  useEffect(() => {
    const selected = search.get("selected");
    if (selected && store.items.has(selected)) store.select(selected);
  }, [search]);
  useEffect(() => {
    setEditor(null);
    if (!project) return;
    runInAction(() => {
      for (const [id, item] of appStore.issues)
        if (item.project_id === project.id) appStore.issues.delete(id);
      appStore.projectIssueIds.delete(project.id);
    });
  }, [store.authorizationGeneration]);
  useEffect(() => {
    if (
      !project ||
      store.projectID !== project.id ||
      (!store.disabled && !store.snapshotLoaded)
    )
      return;
    const enabled = !store.disabled;
    const current = appStore.projects.get(project.id);
    if (current && requirementsEnabled(current) !== enabled)
      runInAction(() => {
        appStore.projects.set(current.id, {
          ...current,
          settings: { ...current.settings, requirements_enabled: enabled },
        });
      });
  }, [
    project?.id,
    cachedRequirementsEnabled,
    store.projectID,
    store.disabled,
    store.snapshotLoaded,
  ]);
  useEffect(() => {
    if (!project || store.projectID !== project.id) return;
    runInAction(() => {
      const current = new Set(store.snapshot.items.map((item) => item.id));
      for (const [id, item] of appStore.issues)
        if (item.project_id === project.id && !current.has(id))
          appStore.issues.delete(id);
      for (const item of store.snapshot.items)
        appStore.issues.set(item.id, item);
    });
    if (editor?.item) {
      const current = store.items.get(editor.item.id);
      if (!current || current.archived_at || current.is_draft) setEditor(null);
    }
  }, [store.snapshot.revision, store.snapshot.items, project?.id]);
  if (!project)
    return (
      <EmptyState
        icon={<Boxes size={30} />}
        title={t("请选择项目", "Select a project")}
        description={t(
          "在项目中打开需求工作台。",
          "Open the requirements workspace within a project.",
        )}
      />
    );
  if (store.revoked)
    return (
      <EmptyState
        icon={<Shield size={30} />}
        title={t("当前访问权限已变化", "Your access has changed")}
        description={t(
          "需求、场景和图缓存已清除。重新加载将按当前权限获取数据。",
          "Requirement, scenario and diagram caches have been cleared. Reload to request data with your current access.",
        )}
        action={
          <Button onClick={() => void store.enter(workspace.id, project.id)}>
            {t("重新检查权限", "Check access again")}
          </Button>
        }
      />
    );
  if (store.projectID !== project.id) return <Loading />;
  if (store.disabled)
    return (
      <EmptyState
        icon={<Boxes size={30} />}
        title={t("需求工作台已关闭", "Requirements workspace is disabled")}
        description={t(
          "项目管理员已关闭故事地图、层级甘特、成员排期和业务场景。已有数据保留，基础工作项仍可使用；管理员重新启用后本页面会自动恢复。",
          "A project administrator disabled the story map, Gantt, member schedule and business scenarios. Existing data is retained and basic work items remain available. This page reconnects when an administrator enables the workspace again.",
        )}
        action={
          <div className="req-actions">
            <Link to={`/w/${workspace.slug}/projects/${project.id}/issues`}>
              <Button>{t("打开工作项", "Open work items")}</Button>
            </Link>
            <Link to={`/w/${workspace.slug}/projects/${project.id}/settings`}>
              <Button>{t("项目设置", "Project settings")}</Button>
            </Link>
            <Button onClick={() => void store.reload()}>
              {t("重新检查", "Check again")}
            </Button>
          </div>
        }
      />
    );
  const states = store.states;
  const members = store.members;
  const openItem = (id: string) => {
    const item = store.items.get(id);
    if (item) {
      store.select(id);
      setEditor({ item });
    }
  };
  const createItem = (seed: Partial<WorkItem>) => setEditor({ seed });
  const hiddenSelected =
    store.selected &&
    !store.visibleItems.some((item) => item.id === store.selectedID);
  const skills = [
    ...new Set([
      ...(store.resources?.members.flatMap((member) => member.skills) ?? []),
      ...store.snapshot.items.flatMap((item) => item.required_skills ?? []),
    ]),
  ].sort();
  const selectedQuery = store.selectedID
    ? `?selected=${encodeURIComponent(store.selectedID)}`
    : "";
  return (
    <div className="requirements-page" data-testid="requirements-workspace">
      <header className="req-page-header">
        <div>
          <div className="req-eyebrow">
            {project.identifier} · {t("需求工作台", "Requirements workspace")}
          </div>
          <h1>
            {t("从用户价值到执行计划", "From user value to an executable plan")}
          </h1>
        </div>
        <div className="req-actions">
          <span
            className={`req-sync-status ${store.connected ? "is-live" : ""}`}
            role="status"
          >
            {store.loading
              ? t("同步中", "Syncing")
              : store.connected
                ? t("实时同步", "Live")
                : t("重新连接中", "Reconnecting")}{" "}
            · r{store.snapshot.revision}
          </span>
          <Button
            size="icon"
            aria-label={t("刷新需求", "Refresh requirements")}
            disabled={store.loading}
            onClick={() => void store.reload()}
          >
            <RefreshCw size={15} />
          </Button>
          {canEdit && (
            <Button
              variant="primary"
              onClick={() => createItem({ requirement_type: "story" })}
            >
              <Plus size={15} />
              {t("新建需求", "New requirement")}
            </Button>
          )}
        </div>
      </header>
      <nav
        className="req-view-tabs"
        aria-label={t("需求视图", "Requirement views")}
      >
        {views.map((view) => (
          <Link
            key={view.id}
            className={requirementView === view.id ? "is-active" : ""}
            to={`${route}/${view.id}${selectedQuery}`}
            aria-current={requirementView === view.id ? "page" : undefined}
          >
            <view.icon size={16} />
            {t(view.zh, view.en)}
          </Link>
        ))}
        <Link to={`/w/${workspace.slug}/projects/${project.id}/automation`}>
          {t("AI 与自动化", "AI and automation")}
        </Link>
      </nav>
      <div className="req-filters" aria-label={t("共享筛选", "Shared filters")}>
        <div className="req-search">
          <Search size={14} />
          <Input
            aria-label={t("搜索需求", "Search requirements")}
            placeholder={t(
              "搜索标题、角色、目标…",
              "Search title, role, goal…",
            )}
            value={store.filters.query}
            onChange={(event) => store.setFilter("query", event.target.value)}
          />
        </div>
        <Select
          aria-label={t("筛选 Epic", "Filter epic")}
          value={store.filters.epic}
          onChange={(event) => store.setFilter("epic", event.target.value)}
        >
          <option value="">{t("所有 Epic", "All epics")}</option>
          {store.snapshot.items
            .filter((item) => item.requirement_type === "epic")
            .map((item) => (
              <option key={item.id} value={item.id}>
                {item.name}
              </option>
            ))}
        </Select>
        <Select
          aria-label={t("筛选 Sprint", "Filter sprint")}
          value={store.filters.cycle}
          onChange={(event) => store.setFilter("cycle", event.target.value)}
        >
          <option value="">{t("所有 Sprint", "All sprints")}</option>
          <option value="backlog">Backlog</option>
          {store.snapshot.cycles
            .filter((cycle) => !cycle.archived_at)
            .map((cycle) => (
              <option key={cycle.id} value={cycle.id}>
                {cycle.name}
              </option>
            ))}
        </Select>
        <Select
          aria-label={t("筛选状态", "Filter status")}
          value={store.filters.state}
          onChange={(event) => store.setFilter("state", event.target.value)}
        >
          <option value="">{t("所有状态", "All states")}</option>
          {states.map((state) => (
            <option key={state.id} value={state.id}>
              {state.name}
            </option>
          ))}
        </Select>
        <Select
          aria-label={t("筛选负责人", "Filter assignee")}
          value={store.filters.member}
          onChange={(event) => store.setFilter("member", event.target.value)}
        >
          <option value="">{t("所有成员", "All members")}</option>
          {members.map((member) => (
            <option key={member.user_id} value={member.user_id}>
              {memberName(member)}
            </option>
          ))}
        </Select>
        <Select
          aria-label={t("筛选技能", "Filter skill")}
          value={store.filters.skill}
          onChange={(event) => store.setFilter("skill", event.target.value)}
        >
          <option value="">{t("所有技能", "All skills")}</option>
          {skills.map((skill) => (
            <option value={skill} key={skill}>
              {skill}
            </option>
          ))}
        </Select>
        {Object.values(store.filters).some(Boolean) && (
          <Button
            size="icon"
            variant="ghost"
            aria-label={t("清除筛选", "Clear filters")}
            onClick={store.clearFilters}
          >
            <X size={14} />
          </Button>
        )}
      </div>
      <div className="req-context">
        <span>
          {store.visibleItems.length} / {store.snapshot.items.length}{" "}
          {t("工作项", "work items")}
        </span>
        {store.selected && (
          <>
            <span>
              · {t("已选择", "Selected")}:{" "}
              <button onClick={() => openItem(store.selectedID)}>
                {store.selected.name}
              </button>
            </span>
            {hiddenSelected && (
              <Button size="sm" onClick={store.clearFilters}>
                {t(
                  "当前筛选隐藏了所选对象 · 显示",
                  "Selection is hidden by filters · reveal",
                )}
              </Button>
            )}
          </>
        )}
        <span className="req-spacer" />
        <label>
          {t("容量日期范围", "Capacity date range")}{" "}
          <Input
            type="date"
            aria-label={t("容量开始日期", "Capacity start date")}
            value={start}
            onChange={(event) => setStart(event.target.value)}
          />
        </label>
        <span>→</span>
        <Input
          type="date"
          aria-label={t("容量结束日期", "Capacity end date")}
          value={end}
          min={start}
          max={start ? dateFromDay(dayNumber(start) + 365) : undefined}
          onChange={(event) => setEnd(event.target.value)}
        />
        <Button
          size="sm"
          disabled={
            !start ||
            !end ||
            start > end ||
            dayNumber(end) - dayNumber(start) > 365 ||
            store.loading
          }
          onClick={() => void store.setDateRange(start, end)}
        >
          {t("应用日期", "Apply dates")}
        </Button>
      </div>
      <ErrorBox message={store.error} retry={() => void store.reload()} />
      <ErrorBox
        message={store.auxiliaryError}
        retry={() => void store.reloadAuxiliary()}
      />
      {store.loading &&
      !store.snapshot.items.length &&
      !store.snapshot.revision ? (
        <Loading />
      ) : (
        <div key={store.authorizationGeneration} className="req-view-content">
          {requirementView === "story-map" ? (
            <StoryMap openItem={openItem} createItem={createItem} />
          ) : requirementView === "gantt" ? (
            <RequirementsGantt openItem={openItem} />
          ) : requirementView === "members" ? (
            <MemberSchedule openItem={openItem} />
          ) : requirementView === "scenarios" ? (
            <ScenariosView openItem={openItem} />
          ) : (
            <EmptyState
              icon={<Map size={30} />}
              title={t("找不到此视图", "View not found")}
              description={t(
                "从故事地图继续查看需求。",
                "Continue from the story map.",
              )}
              action={
                <Link to={`${route}/story-map`}>
                  {t("打开故事地图", "Open story map")}
                </Link>
              }
            />
          )}
        </div>
      )}
      <ConflictPanel
        openItem={openItem}
        locateMember={(id) => {
          store.setFilter("member", id);
          navigate(`${route}/members${selectedQuery}`);
        }}
      />
      {editor && (
        <RequirementEditor
          key={`${editor.item?.id ?? JSON.stringify(editor.seed)}-${store.authorizationGeneration}`}
          item={editor.item}
          seed={editor.seed}
          close={() => setEditor(null)}
          openItem={openItem}
          createChild={(parent) =>
            createItem({
              requirement_type: "task",
              parent_id: parent.id,
              cycle_id: parent.cycle_id,
            })
          }
        />
      )}
    </div>
  );
});
