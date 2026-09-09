import {
  lazy,
  Suspense,
  useEffect,
  useMemo,
  useRef,
  useState,
  type DragEvent,
  type RefObject,
} from "react";
import { observer } from "mobx-react-lite";
import { Link, useParams } from "react-router-dom";
import {
  draggable,
  dropTargetForElements,
  monitorForElements,
} from "@atlaskit/pragmatic-drag-and-drop/element/adapter";
import { combine } from "@atlaskit/pragmatic-drag-and-drop/combine";
import {
  flexRender,
  getCoreRowModel,
  useReactTable,
  type ColumnDef,
} from "@tanstack/react-table";
import { useVirtualizer } from "@tanstack/react-virtual";
import {
  Archive,
  ArrowDownWideNarrow,
  CalendarDays,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Columns3,
  Filter,
  GanttChart,
  GripVertical,
  List,
  ListTodo,
  Plus,
  Search,
  Table2,
  X,
} from "lucide-react";
import { appStore } from "../../stores/app-store";
import {
  useCanEditProject,
  useCanEditWorkItem,
  useRemote,
  useScope,
} from "../../lib/hooks";
import { api, APIError, errorMessage, projectPath } from "../../lib/api";
import { cn, memberName, priorities, shortDate } from "../../lib/utils";
import {
  Avatar,
  Badge,
  Button,
  EmptyState,
  ErrorBox,
  Input,
  Loading,
  Menu,
  MultiSelect,
  PageHeader,
  PriorityIcon,
  priorityLabels,
  Select,
  StateIcon,
  Tooltip,
} from "../../components/ui";
import type {
  APIResponse,
  Cycle,
  EstimateScheme,
  Layout,
  Module,
  Project,
  State,
  WorkItem,
} from "../../types";
import { issueComparator, positionForDrop } from "./ordering";
import { useWorkspacePreference } from "../../lib/preferences";
import {
  AdvancedFilterBuilder,
  type FilterExpression,
} from "../../components/advanced-filters";
const IssueDetail = lazy(() =>
  import("./issue-detail").then((module) => ({ default: module.IssueDetail })),
);

const layouts: { id: Layout; zh: string; en: string; icon: typeof List }[] = [
  { id: "list", zh: "列表", en: "List", icon: List },
  { id: "board", zh: "看板", en: "Board", icon: Columns3 },
  { id: "calendar", zh: "日历", en: "Calendar", icon: CalendarDays },
  { id: "timeline", zh: "时间线", en: "Timeline", icon: GanttChart },
  { id: "table", zh: "表格", en: "Spreadsheet", icon: Table2 },
];
export const groupChoices = [
  { value: "state_id", zh: "状态", en: "State" },
  { value: "state_group", zh: "工作阶段", en: "State group" },
  { value: "priority", zh: "优先级", en: "Priority" },
  { value: "project_id", zh: "项目", en: "Project" },
  { value: "assignee_id", zh: "负责人", en: "Assignee" },
  { value: "label_id", zh: "标签", en: "Label" },
  { value: "cycle_id", zh: "周期", en: "Cycle" },
  { value: "module_id", zh: "模块", en: "Module" },
  { value: "created_by", zh: "创建人", en: "Creator" },
  { value: "estimate_point_id", zh: "估算", en: "Estimate" },
];

export const IssuesPage = observer(function IssuesPage({
  archived = false,
  embedded = false,
  extraQuery = "",
  title,
  defaultLayout = "list",
  defaultGroupBy = "state_id",
  defaultSubGroupBy = "",
  defaultOrder = "position",
}: {
  archived?: boolean;
  embedded?: boolean;
  extraQuery?: string;
  title?: string;
  defaultLayout?: Layout;
  defaultGroupBy?: string;
  defaultSubGroupBy?: string;
  defaultOrder?: string;
}) {
  const { workspace, project } = useScope();
  const canCreate = useCanEditProject();
  const { issueId } = useParams();
  const [layout, setLayout] = useState<Layout>(defaultLayout);
  const [query, setQuery] = useState("");
  const [stateFilter, setStateFilter] = useState("");
  const [priorityFilter, setPriorityFilter] = useState("");
  const [assigneeFilter, setAssigneeFilter] = useState("");
  const [labelFilter, setLabelFilter] = useState("");
  const [order, setOrder] = useState(defaultOrder);
  const [showFilters, setShowFilters] = useState(false);
  const [advancedFilter, setAdvancedFilter] = useState<FilterExpression | null>(
    null,
  );
  const [groupBy, setGroupBy] = useState(defaultGroupBy);
  const [subGroupBy, setSubGroupBy] = useState(defaultSubGroupBy);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const [total, setTotal] = useState(0);
  const [cursor, setCursor] = useState<string | null>(null);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [hasMore, setHasMore] = useState(false);
  const [revision, setRevision] = useState(0);
  const t = appStore.t;
  const projectId = project?.id;
  const preferenceKey = `issues-${projectId}-${archived ? "archived" : "active"}`;
  const displayPreference = useWorkspacePreference(
    preferenceKey,
    {
      layout: defaultLayout,
      group_by: defaultGroupBy,
      sub_group_by: defaultSubGroupBy,
      order_by: defaultOrder,
    },
    !embedded && !!projectId,
  );
  const appliedPreference = useRef("");
  const states = appStore.states.get(projectId ?? "") ?? [];
  const members = appStore.members.get(projectId ?? "") ?? [];
  const labels = appStore.labels.get(projectId ?? "") ?? [];
  const serverGrouped =
    ["list", "board"].includes(layout) &&
    (groupBy !== "state_id" || !!subGroupBy);
  const issues = appStore
    .projectIssues(projectId ?? "")
    .filter(
      (item) =>
        !item.is_draft &&
        !!item.archived_at === archived &&
        (!stateFilter || item.state_id === stateFilter) &&
        (!priorityFilter || item.priority === priorityFilter) &&
        (!query ||
          `${item.name} ${project?.identifier}-${item.sequence_id}`
            .toLowerCase()
            .includes(query.toLowerCase())) &&
        (!assigneeFilter ||
          (item.assignee_ids ?? []).includes(assigneeFilter)) &&
        (!labelFilter || (item.label_ids ?? []).includes(labelFilter)),
    )
    .sort(issueComparator(order));

  const requestParameters = new URLSearchParams(extraQuery);
  requestParameters.set("limit", "100");
  requestParameters.set("order_by", order);
  requestParameters.set("archived", String(archived));
  if (query) requestParameters.set("search", query);
  if (stateFilter) requestParameters.set("state_id", stateFilter);
  if (priorityFilter) requestParameters.set("priority", priorityFilter);
  if (assigneeFilter) requestParameters.set("assignee_id", assigneeFilter);
  if (labelFilter) requestParameters.set("label_id", labelFilter);
  if (advancedFilter) {
    const inherited = requestParameters.get("filter");
    let expression = advancedFilter;
    if (inherited) {
      try {
        expression = { and: [JSON.parse(inherited), advancedFilter] };
      } catch {
        /* Preserve validation on the server. */
      }
    }
    requestParameters.set("filter", JSON.stringify(expression));
  }
  const requestQuery = requestParameters.toString();

  useEffect(() => {
    if (!projectId) return;
    if (serverGrouped) {
      setLoading(false);
      appStore
        .loadProjectResources(workspace.id, projectId)
        .catch((cause) => setError(errorMessage(cause)));
      return;
    }
    let active = true;
    const controller = new AbortController();
    setLoading(true);
    setError("");
    const parameters = new URLSearchParams(requestQuery);
    if (cursor) parameters.set("cursor", cursor);
    const timer = setTimeout(
      () => {
        Promise.all([
          appStore.loadProjectResources(workspace.id, projectId),
          appStore.loadIssues(
            workspace.id,
            projectId,
            parameters.toString(),
            controller.signal,
          ),
        ])
          .then(([, result]) => {
            if (active) {
              setTotal(result.pagination?.total ?? result.data.length);
              setNextCursor(result.pagination?.next_cursor ?? null);
              setHasMore(result.pagination?.has_more ?? false);
            }
          })
          .catch((cause) => {
            if (active) setError(errorMessage(cause));
          })
          .finally(() => {
            if (active) setLoading(false);
          });
      },
      query ? 200 : 0,
    );
    return () => {
      active = false;
      controller.abort();
      clearTimeout(timer);
    };
  }, [
    projectId,
    workspace.id,
    query,
    stateFilter,
    priorityFilter,
    assigneeFilter,
    labelFilter,
    advancedFilter,
    order,
    archived,
    extraQuery,
    revision,
    cursor,
    serverGrouped,
    requestQuery,
  ]);

  useEffect(() => {
    setCursor(null);
    setSelected([]);
  }, [
    projectId,
    query,
    stateFilter,
    priorityFilter,
    assigneeFilter,
    labelFilter,
    advancedFilter,
    archived,
    extraQuery,
    order,
    layout,
    groupBy,
    subGroupBy,
  ]);
  useEffect(() => {
    setLayout(defaultLayout);
  }, [defaultLayout]);
  useEffect(() => {
    setGroupBy(defaultGroupBy);
    setSubGroupBy(defaultSubGroupBy);
    setOrder(defaultOrder);
  }, [defaultGroupBy, defaultSubGroupBy, defaultOrder]);
  useEffect(() => {
    if (
      embedded ||
      displayPreference.loading ||
      displayPreference.error ||
      appliedPreference.current === preferenceKey
    )
      return;
    appliedPreference.current = preferenceKey;
    if (layouts.some((item) => item.id === displayPreference.value.layout))
      setLayout(displayPreference.value.layout);
    if (
      groupChoices.some(
        (item) => item.value === displayPreference.value.group_by,
      )
    )
      setGroupBy(displayPreference.value.group_by);
    setSubGroupBy(displayPreference.value.sub_group_by);
    setOrder(displayPreference.value.order_by);
  }, [
    embedded,
    preferenceKey,
    displayPreference.loading,
    displayPreference.error,
    displayPreference.value.layout,
    displayPreference.value.group_by,
    displayPreference.value.sub_group_by,
    displayPreference.value.order_by,
  ]);

  if (!project) return null;
  const refresh = () => setRevision((value) => value + 1);
  const saveDisplay = (changes: Partial<typeof displayPreference.value>) => {
    if (!embedded)
      displayPreference
        .save(changes)
        .catch((cause) => setError(errorMessage(cause)));
  };
  const changeOrder = (order: string) => {
    setOrder(order);
    saveDisplay({ order_by: order });
  };
  const base = `/w/${workspace.slug}/projects/${project.id}/issues`;
  const activeFilters = [
    stateFilter,
    priorityFilter,
    assigneeFilter,
    labelFilter,
    advancedFilter,
  ].filter(Boolean).length;
  const updateIssue = async (item: WorkItem, patch: Partial<WorkItem>) => {
    try {
      await appStore.updateIssue(item.id, patch);
    } catch (cause) {
      appStore.notify(errorMessage(cause), "error");
    }
  };
  const bulkUpdate = async (patch: Record<string, unknown>) => {
    try {
      await api.patch(`${projectPath(workspace.id, project.id)}/issues/bulk`, {
        ids: selected,
        changes: patch,
        versions: Object.fromEntries(
          selected.map((id) => [id, appStore.issues.get(id)?.version]),
        ),
      });
      setSelected([]);
      refresh();
      appStore.notify(t("工作项已更新", "Work items updated"));
    } catch (cause) {
      appStore.notify(errorMessage(cause), "error");
    }
  };

  const toolbar = (
    <div className="issues-toolbar">
      <div
        className="layout-switcher"
        role="group"
        aria-label={t("视图布局", "View layout")}
      >
        {layouts.map((item) => (
          <Tooltip key={item.id} label={t(item.zh, item.en)}>
            <button
              aria-label={t(item.zh, item.en)}
              aria-pressed={layout === item.id}
              className={layout === item.id ? "active" : ""}
              onClick={() => {
                setLayout(item.id);
                saveDisplay({ layout: item.id });
              }}
            >
              <item.icon size={16} />
            </button>
          </Tooltip>
        ))}
      </div>
      <span className="toolbar-divider" />
      <Button
        variant={showFilters ? "secondary" : "ghost"}
        size="sm"
        onClick={() => setShowFilters(!showFilters)}
      >
        <Filter size={14} />
        {t("筛选", "Filters")}
        {activeFilters > 0 && <Badge>{activeFilters}</Badge>}
      </Button>
      <Menu
        items={[
          {
            label: t("自定义排序", "Manual order"),
            onSelect: () => changeOrder("position"),
          },
          {
            label: t("最新创建", "Newest first"),
            onSelect: () => changeOrder("-created_at"),
          },
          {
            label: t("最近更新", "Recently updated"),
            onSelect: () => changeOrder("-updated_at"),
          },
          {
            label: t("目标日期", "Due date"),
            onSelect: () => changeOrder("target_date"),
          },
          {
            label: t("优先级", "Priority"),
            onSelect: () => changeOrder("priority"),
          },
        ]}
      >
        <Button variant="ghost" size="sm">
          <ArrowDownWideNarrow size={14} />
          {t("排序", "Sort")}
        </Button>
      </Menu>
      <span className="flex-spacer" />
      <div className="search-input compact">
        <Search size={14} />
        <Input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder={t("搜索工作项…", "Search work items…")}
          aria-label={t("搜索工作项", "Search work items")}
        />
      </div>
    </div>
  );

  return (
    <div className={cn("issues-page", embedded && "issues-embedded")}>
      {!embedded && (
        <PageHeader
          title={
            title ??
            (archived
              ? t("已归档工作项", "Archived work items")
              : t("工作项", "Work items"))
          }
          actions={
            <>
              <Badge>{total}</Badge>
              {!archived && canCreate && (
                <Button
                  variant="primary"
                  size="sm"
                  onClick={() => appStore.setCreateIssueProject(project.id)}
                >
                  <Plus size={15} />
                  {t("添加工作项", "Add work item")}
                </Button>
              )}
            </>
          }
        >
          {toolbar}
        </PageHeader>
      )}
      {embedded && toolbar}
      {showFilters && (
        <div className="issues-filter-panel">
          <div className="filter-bar">
            <Select
              value={stateFilter}
              onChange={(event) => setStateFilter(event.target.value)}
              aria-label={t("按状态筛选", "Filter by state")}
            >
              <option value="">{t("所有状态", "All states")}</option>
              {states.map((state) => (
                <option key={state.id} value={state.id}>
                  {state.name}
                </option>
              ))}
            </Select>
            <Select
              value={priorityFilter}
              onChange={(event) => setPriorityFilter(event.target.value)}
              aria-label={t("按优先级筛选", "Filter by priority")}
            >
              <option value="">{t("所有优先级", "All priorities")}</option>
              {priorities.map((priority) => (
                <option key={priority} value={priority}>
                  {t(...priorityLabels[priority])}
                </option>
              ))}
            </Select>
            <Select
              value={assigneeFilter}
              onChange={(event) => setAssigneeFilter(event.target.value)}
              aria-label={t("按负责人筛选", "Filter by assignee")}
            >
              <option value="">{t("所有成员", "All members")}</option>
              {members.map((member) => (
                <option key={member.user_id} value={member.user_id}>
                  {memberName(member)}
                </option>
              ))}
            </Select>
            <Select
              value={labelFilter}
              onChange={(event) => setLabelFilter(event.target.value)}
              aria-label={t("按标签筛选", "Filter by label")}
            >
              <option value="">{t("所有标签", "All labels")}</option>
              {labels.map((label) => (
                <option key={label.id} value={label.id}>
                  {label.name}
                </option>
              ))}
            </Select>
            {activeFilters > 0 && (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setStateFilter("");
                  setPriorityFilter("");
                  setAssigneeFilter("");
                  setLabelFilter("");
                  setAdvancedFilter(null);
                }}
              >
                <X size={13} />
                {t("清除", "Clear")}
              </Button>
            )}
          </div>
          <AdvancedFilterBuilder
            value={advancedFilter}
            onChange={setAdvancedFilter}
          />
          {["list", "board"].includes(layout) && (
            <div className="filter-bar">
              <span className="text-muted">{t("分组", "Group by")}</span>
              <Select
                aria-label={t("分组属性", "Grouping property")}
                value={groupBy}
                onChange={(event) => {
                  setGroupBy(event.target.value);
                  if (subGroupBy === event.target.value) setSubGroupBy("");
                  saveDisplay({
                    group_by: event.target.value,
                    ...(subGroupBy === event.target.value
                      ? { sub_group_by: "" }
                      : {}),
                  });
                }}
              >
                {groupChoices.map((choice) => (
                  <option key={choice.value} value={choice.value}>
                    {t(choice.zh, choice.en)}
                  </option>
                ))}
              </Select>
              <span className="text-muted">{t("子分组", "Subgroup")}</span>
              <Select
                aria-label={t("子分组属性", "Subgroup property")}
                value={subGroupBy}
                onChange={(event) => {
                  setSubGroupBy(event.target.value);
                  saveDisplay({ sub_group_by: event.target.value });
                }}
              >
                <option value="">{t("无", "None")}</option>
                {groupChoices
                  .filter((choice) => choice.value !== groupBy)
                  .map((choice) => (
                    <option key={choice.value} value={choice.value}>
                      {t(choice.zh, choice.en)}
                    </option>
                  ))}
              </Select>
            </div>
          )}
        </div>
      )}
      <ErrorBox message={error} retry={refresh} />
      {loading ? (
        <Loading />
      ) : serverGrouped ? (
        <GroupedIssueResults
          key={`${groupBy}:${subGroupBy}`}
          endpoint={`${projectPath(workspace.id, project.id)}/issues`}
          query={requestQuery}
          groupBy={groupBy}
          subGroupBy={subGroupBy}
          layout={layout}
          revision={revision}
          base={base}
          onTotal={setTotal}
          selected={selected}
          onSelect={(id, checked) =>
            setSelected(
              checked
                ? [...selected, id]
                : selected.filter((item) => item !== id),
            )
          }
        />
      ) : issues.length === 0 && layout !== "board" && layout !== "calendar" ? (
        <EmptyState
          icon={archived ? <Archive size={30} /> : <ListTodo size={30} />}
          title={
            activeFilters || query
              ? t("没有符合条件的工作项", "No work items match")
              : archived
                ? t("还没有归档的工作项", "No archived work items")
                : t("从一件小事开始", "Start with one small step")
          }
          description={
            activeFilters || query
              ? t(
                  "调整筛选条件，或尝试其他关键词。",
                  "Adjust your filters or try a different keyword.",
                )
              : t(
                  "明确下一步工作，让想法逐渐变成成果。",
                  "Define the next piece of work and turn your ideas into progress.",
                )
          }
          action={
            !archived && canCreate && !activeFilters && !query ? (
              <Button
                variant="primary"
                onClick={() => appStore.setCreateIssueProject(project.id)}
              >
                <Plus size={15} />
                {t("创建工作项", "Create work item")}
              </Button>
            ) : undefined
          }
        />
      ) : (
        <>
          {layout === "list" && (
            <div className="issue-list-scroll">
              {states
                .filter((state) => !stateFilter || state.id === stateFilter)
                .map((state) => (
                  <IssueGroup
                    key={state.id}
                    state={state}
                    issues={issues.filter((item) => item.state_id === state.id)}
                    base={base}
                    selected={selected}
                    onSelect={(id, checked) =>
                      setSelected(
                        checked
                          ? [...selected, id]
                          : selected.filter((item) => item !== id),
                      )
                    }
                    onUpdate={updateIssue}
                  />
                ))}
            </div>
          )}
          {layout === "board" && (
            <Board
              issues={issues}
              states={states.filter(
                (state) => !stateFilter || state.id === stateFilter,
              )}
              base={base}
              onUpdate={updateIssue}
            />
          )}
          {layout === "table" && (
            <IssueTable issues={issues} base={base} onUpdate={updateIssue} />
          )}
          {layout === "calendar" && (
            <IssueCalendar issues={issues} base={base} onUpdate={updateIssue} />
          )}
          {layout === "timeline" && (
            <IssueTimeline issues={issues} base={base} onUpdate={updateIssue} />
          )}
        </>
      )}
      {!serverGrouped && (hasMore || cursor) && (
        <div className="pagination-footer">
          <span>
            {t("当前页", "Current page")}: {issues.length} · {t("共", "Total")}{" "}
            {total}
          </span>
          {cursor && (
            <Button size="sm" onClick={() => setCursor(null)}>
              {t("返回首页", "First page")}
            </Button>
          )}
          <Button
            size="sm"
            disabled={!hasMore}
            onClick={() => setCursor(nextCursor)}
          >
            {t("下一页", "Next page")}
            <ChevronRight size={14} />
          </Button>
        </div>
      )}
      {selected.length > 0 && (
        <div className="bulk-toolbar">
          <Button
            variant="ghost"
            size="icon"
            onClick={() => setSelected([])}
            aria-label={t("清除选择", "Clear selection")}
          >
            <X size={14} />
          </Button>
          <span>
            {selected.length} {t("个已选", "selected")}
          </span>
          <Select
            value=""
            onChange={(event) => {
              if (event.target.value)
                bulkUpdate({ state_id: event.target.value });
            }}
            aria-label={t("批量修改状态", "Change selected states")}
          >
            <option value="">{t("修改状态", "Change state")}</option>
            {states.map((state) => (
              <option key={state.id} value={state.id}>
                {state.name}
              </option>
            ))}
          </Select>
          <Select
            value=""
            onChange={(event) => {
              if (event.target.value)
                bulkUpdate({ priority: event.target.value });
            }}
            aria-label={t("批量修改优先级", "Change selected priorities")}
          >
            <option value="">{t("修改优先级", "Change priority")}</option>
            {priorities.map((priority) => (
              <option value={priority} key={priority}>
                {t(...priorityLabels[priority])}
              </option>
            ))}
          </Select>
          <Button
            size="sm"
            onClick={() =>
              bulkUpdate({
                archived_at: archived ? null : new Date().toISOString(),
              })
            }
          >
            <Archive size={14} />
            {archived ? t("恢复", "Restore") : t("归档", "Archive")}
          </Button>
        </div>
      )}
      {issueId && (
        <Suspense fallback={<Loading />}>
          <IssueDetail issueId={issueId} onChanged={refresh} />
        </Suspense>
      )}
    </div>
  );
});

interface RemoteIssueGroup {
  key: string;
  label: string;
  total: number;
  items?: WorkItem[];
  groups?: RemoteIssueGroup[];
  pagination?: APIResponse<unknown>["pagination"];
}

interface GroupedIssueDrag {
  id: string;
  groups: Record<string, string>;
}

interface GroupedDragSession {
  source: GroupedIssueDrag | null;
  pending: Set<string>;
}

export const GroupedIssueResults = observer(function GroupedIssueResults({
  endpoint,
  query,
  groupBy,
  subGroupBy,
  layout,
  revision,
  base,
  onTotal,
  selected,
  onSelect,
  hrefFor,
}: {
  endpoint: string;
  query: string;
  groupBy: string;
  subGroupBy: string;
  layout: Layout;
  revision: number;
  base: string;
  onTotal: (total: number) => void;
  selected: string[];
  onSelect: (id: string, checked: boolean) => void;
  hrefFor?: (item: WorkItem) => string;
}) {
  const { workspace, project } = useScope();
  const dragSession = useRef<GroupedDragSession>({
    source: null,
    pending: new Set(),
  });
  const [groupCursor, setGroupCursor] = useState("");
  const params = new URLSearchParams(query);
  params.set("group_by", groupBy);
  params.set("show_empty", "true");
  params.set("limit", "50");
  if (subGroupBy) params.set("sub_group_by", subGroupBy);
  if (groupCursor) params.set("group_cursor", groupCursor);
  const remote = useRemote<RemoteIssueGroup[]>(
    `${endpoint}?${params}`,
    revision,
  );
  const projectEndpoint = project ? projectPath(workspace.id, project.id) : "";
  const cycles = useRemote<Cycle[]>(
    project && [groupBy, subGroupBy].includes("cycle_id")
      ? `${projectEndpoint}/cycles`
      : null,
  );
  const modules = useRemote<Module[]>(
    project && [groupBy, subGroupBy].includes("module_id")
      ? `${projectEndpoint}/modules`
      : null,
  );
  const schemes = useRemote<EstimateScheme[]>(
    project && [groupBy, subGroupBy].includes("estimate_point_id")
      ? `${projectEndpoint}/estimates`
      : null,
  );
  const t = appStore.t;
  useEffect(() => setGroupCursor(""), [query, groupBy, subGroupBy]);
  useEffect(() => {
    if (!remote.data) return;
    const flatten = (group: RemoteIssueGroup): WorkItem[] => [
      ...(group.items ?? []),
      ...(group.groups ?? []).flatMap(flatten),
    ];
    const loadedItems = remote.data.flatMap(flatten);
    appStore.cacheIssues(loadedItems);
    if (!project) {
      const projectIds = [
        ...new Set(loadedItems.map((item) => item.project_id)),
      ];
      Promise.all(
        projectIds
          .filter((id) => !appStore.states.has(id))
          .map((id) => appStore.loadProjectResources(workspace.id, id)),
      ).catch((cause) => appStore.notify(errorMessage(cause), "error"));
    }
    onTotal(
      remote.totalItems ??
        remote.data.reduce((count, group) => count + group.total, 0),
    );
  }, [remote.data, remote.totalItems, onTotal, project?.id, workspace.id]);
  const label = (field: string, group: RemoteIssueGroup) => {
    if (field === "priority")
      return t(...priorityLabels[group.key as keyof typeof priorityLabels]);
    if (group.key === "none") return t("未设置", "Not set");
    if (field === "state_group") {
      const names: Record<string, [string, string]> = {
        backlog: ["待整理", "Backlog"],
        unstarted: ["待开始", "Unstarted"],
        started: ["进行中", "Started"],
        completed: ["已完成", "Completed"],
        cancelled: ["已取消", "Cancelled"],
      };
      return names[group.key] ? t(...names[group.key]) : group.label;
    }
    if (field === "state_id")
      return (
        appStore.states
          .get(project?.id ?? "")
          ?.find((state) => state.id === group.key)?.name ?? group.label
      );
    if (field === "project_id")
      return appStore.projects.get(group.key)?.name ?? group.label;
    if (["assignee_id", "created_by"].includes(field))
      return memberName(
        appStore.members
          .get(project?.id ?? "")
          ?.find((member) => member.user_id === group.key) ?? {
          display_name: group.label,
        },
      );
    if (field === "label_id")
      return (
        appStore.labels
          .get(project?.id ?? "")
          ?.find((entry) => entry.id === group.key)?.name ?? group.label
      );
    if (field === "cycle_id")
      return (
        cycles.data?.find((entry) => entry.id === group.key)?.name ??
        group.label
      );
    if (field === "module_id")
      return (
        modules.data?.find((entry) => entry.id === group.key)?.name ??
        group.label
      );
    if (field === "estimate_point_id")
      return (
        schemes.data
          ?.flatMap((scheme) => scheme.points)
          .find((point) => point.id === group.key)?.label ?? group.label
      );
    return group.label;
  };
  return (
    <div className="grouped-results">
      <ErrorBox message={remote.error} retry={remote.refresh} />
      {remote.loading ? (
        <Loading />
      ) : !remote.data?.length ? (
        <EmptyState
          icon={<ListTodo size={28} />}
          title={t("没有符合条件的工作项", "No matching work items")}
          description={t("调整筛选条件后重试。", "Try adjusting your filters.")}
        />
      ) : (
        <div
          className={
            layout === "board" ? "custom-group-board" : "custom-group-list"
          }
        >
          {remote.data.map((group) => (
            <section key={group.key} className="remote-issue-group">
              {group.groups ? (
                <>
                  <header className="remote-group-heading">
                    <strong>{label(groupBy, group)}</strong>
                    <Badge>{group.total}</Badge>
                  </header>
                  {group.groups.map((subgroup) => (
                    <RemoteIssueLeaf
                      key={subgroup.key}
                      group={subgroup}
                      title={label(subGroupBy, subgroup)}
                      endpoint={endpoint}
                      query={params.toString()}
                      parentKey={group.key}
                      base={base}
                      board={layout === "board"}
                      selected={selected}
                      onSelect={onSelect}
                      refresh={remote.refresh}
                      hrefFor={hrefFor}
                      dragSession={dragSession}
                    />
                  ))}
                </>
              ) : (
                <RemoteIssueLeaf
                  group={group}
                  title={label(groupBy, group)}
                  endpoint={endpoint}
                  query={params.toString()}
                  base={base}
                  board={layout === "board"}
                  selected={selected}
                  onSelect={onSelect}
                  refresh={remote.refresh}
                  hrefFor={hrefFor}
                  dragSession={dragSession}
                />
              )}
            </section>
          ))}
        </div>
      )}
      {(remote.pagination?.has_more || groupCursor) && (
        <div className="pagination-footer">
          <Button
            size="sm"
            disabled={!groupCursor}
            onClick={() => setGroupCursor("")}
          >
            {t("首批分组", "First groups")}
          </Button>
          <Button
            size="sm"
            disabled={!remote.pagination?.has_more}
            onClick={() => setGroupCursor(remote.pagination?.next_cursor ?? "")}
          >
            {t("更多分组", "More groups")}
          </Button>
        </div>
      )}
    </div>
  );
});

const RemoteIssueLeaf = observer(function RemoteIssueLeaf({
  group,
  title,
  endpoint,
  query,
  parentKey,
  base,
  board,
  selected,
  onSelect,
  refresh,
  hrefFor,
  dragSession,
}: {
  group: RemoteIssueGroup;
  title: string;
  endpoint: string;
  query: string;
  parentKey?: string;
  base: string;
  board: boolean;
  selected: string[];
  onSelect: (id: string, checked: boolean) => void;
  refresh: () => void;
  hrefFor?: (item: WorkItem) => string;
  dragSession: RefObject<GroupedDragSession>;
}) {
  const [items, setItems] = useState(group.items ?? []);
  const [pagination, setPagination] = useState(group.pagination);
  const [collapsed, setCollapsed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [dragOver, setDragOver] = useState(false);
  const canEdit = useCanEditWorkItem();
  const { workspace } = useScope();
  const t = appStore.t;
  const groupParameters = new URLSearchParams(query);
  const grouping = groupParameters.get("group_by") ?? "state_id";
  const subgrouping = groupParameters.get("sub_group_by") ?? "";
  const targetGroups: Record<string, string> =
    parentKey === undefined
      ? { [grouping]: group.key }
      : { [grouping]: parentKey, [subgrouping]: group.key };
  const canMoveProject = (candidate: Project | undefined) =>
    !!candidate &&
    candidate.workspace_id === workspace.id &&
    workspace.role >= 15 &&
    candidate.role >= 15 &&
    !candidate.archived_at;
  const acceptsDrop = (source: GroupedIssueDrag | null) => {
    if (!source || dragSession.current.pending.has(source.id)) return false;
    const item = appStore.issues.get(source.id);
    if (!item || !canEdit(item) || appStore.movingIssueIds.has(item.id))
      return false;
    if (targetGroups.created_by && targetGroups.created_by !== item.created_by)
      return false;
    const destination = targetGroups.project_id ?? item.project_id;
    if (
      destination !== item.project_id &&
      (!canMoveProject(appStore.projects.get(item.project_id)) ||
        !canMoveProject(appStore.projects.get(destination)))
    )
      return false;
    const states = appStore.states.get(destination);
    if (
      states &&
      ((targetGroups.state_id &&
        !states.some((state) => state.id === targetGroups.state_id)) ||
        (targetGroups.state_group &&
          !states.some((state) => state.group === targetGroups.state_group)))
    )
      return false;
    return true;
  };
  useEffect(() => {
    setItems(group.items ?? []);
    setPagination(group.pagination);
  }, [group]);
  const update = async (item: WorkItem, changes: Partial<WorkItem>) => {
    try {
      await appStore.updateIssue(item.id, changes);
      refresh();
    } catch (cause) {
      setError(errorMessage(cause));
    }
  };
  const more = async () => {
    setBusy(true);
    setError("");
    try {
      const parameters = new URLSearchParams(query);
      parameters.delete("group_cursor");
      parameters.set("group_key", parentKey ?? group.key);
      if (parentKey !== undefined) parameters.set("sub_group_key", group.key);
      parameters.set("cursor", pagination?.next_cursor ?? "");
      const result = await api.get<RemoteIssueGroup[]>(
        `${endpoint}?${parameters}`,
      );
      const parent = result.data.find(
        (entry) => entry.key === (parentKey ?? group.key),
      );
      const next =
        parentKey !== undefined
          ? parent?.groups?.find((entry) => entry.key === group.key)
          : parent;
      appStore.cacheIssues(next?.items ?? []);
      setItems([
        ...new Map(
          [...items, ...(next?.items ?? [])].map((item) => [item.id, item]),
        ).values(),
      ]);
      setPagination(next?.pagination);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  };
  const dropIssue = async (event: DragEvent<HTMLElement>) => {
    if (!event.dataTransfer.types.includes("application/x-myjira-grouped-work"))
      return;
    event.preventDefault();
    event.stopPropagation();
    setDragOver(false);
    const source = dragSession.current.source;
    if (!source || !acceptsDrop(source)) return;
    const item = appStore.issues.get(source.id);
    if (!item) return;
    const target = (event.target as HTMLElement).closest<HTMLElement>(
      "[data-group-work-item]",
    );
    const box = target?.getBoundingClientRect();
    const position = positionForDrop(
      items,
      item.id,
      target?.dataset.groupWorkItem,
      box && event.clientY < box.top + box.height / 2 ? "before" : "after",
    );
    dragSession.current.pending.add(source.id);
    setBusy(true);
    setError("");
    try {
      const destination = targetGroups.project_id ?? item.project_id;
      const crossProject = destination !== item.project_id;
      if (crossProject) {
        const [origin, targetProject] = await Promise.all([
          appStore.loadProject(workspace.id, item.project_id),
          appStore.loadProject(workspace.id, destination),
          appStore.loadProjectResources(workspace.id, destination),
          appStore.loadProjectResources(workspace.id, item.project_id),
        ]);
        if (!canMoveProject(origin) || !canMoveProject(targetProject))
          throw new Error(
            t(
              "需要拥有来源和目标项目的成员权限才能移动工作项。",
              "Moving work requires member access to both projects.",
            ),
          );
      }
      const destinationStates = appStore.states.get(destination) ?? [];
      const previousState = appStore.states
        .get(item.project_id)
        ?.find((state) => state.id === item.state_id);
      const patch: Partial<WorkItem> = {};
      for (const [field, key] of Object.entries(targetGroups)) {
        if (!crossProject && source.groups[field] === key) continue;
        if (field === "priority") patch.priority = key as WorkItem["priority"];
        else if (field === "state_id" || field === "state_group") {
          const state =
            field === "state_id"
              ? destinationStates.find((entry) => entry.id === key)
              : (destinationStates.find(
                  (entry) =>
                    entry.group === key && entry.name === previousState?.name,
                ) ?? destinationStates.find((entry) => entry.group === key));
          if (!state)
            throw new Error(
              t(
                "目标项目没有对应的工作流状态。",
                "The destination project has no matching workflow state.",
              ),
            );
          patch.state_id = state.id;
        } else if (field === "cycle_id")
          patch.cycle_id = key === "none" ? null : key;
        else if (field === "estimate_point_id")
          patch.estimate_point_id = key === "none" ? null : key;
        else if (["assignee_id", "label_id", "module_id"].includes(field)) {
          const property = (
            {
              assignee_id: "assignee_ids",
              label_id: "label_ids",
              module_id: "module_ids",
            } as const
          )[field as "assignee_id" | "label_id" | "module_id"];
          patch[property] =
            key === "none"
              ? []
              : [
                  ...new Set([
                    ...(crossProject ? [] : (item[property] ?? [])).filter(
                      (id) => id !== source.groups[field],
                    ),
                    key,
                  ]),
                ];
        } else if (field === "created_by" && key !== item.created_by)
          throw new Error(
            t(
              "创建人不可修改，可以在同一分组内排序。",
              "The creator cannot be changed. Reorder within the same group.",
            ),
          );
      }
      if (
        !crossProject &&
        !Object.keys(patch).length &&
        groupParameters.get("order_by") !== "position"
      )
        return;
      patch.position = position;
      if (crossProject) {
        const stateId =
          patch.state_id ??
          (
            destinationStates.find(
              (state) =>
                state.group === previousState?.group &&
                state.name === previousState?.name,
            ) ??
            destinationStates.find(
              (state) => state.group === previousState?.group,
            ) ??
            destinationStates.find((state) => state.is_default) ??
            destinationStates[0]
          )?.id;
        if (!stateId)
          throw new Error(
            t(
              "目标项目需要工作流状态。",
              "The destination project needs a workflow state.",
            ),
          );
        const { state_id: _stateId, ...changes } = patch;
        await appStore.moveIssue(item.id, destination, stateId, changes);
      } else {
        await appStore.updateIssue(item.id, patch);
      }
      refresh();
    } catch (cause) {
      setError(errorMessage(cause));
      if (cause instanceof APIError && cause.status === 409) {
        appStore.notify(errorMessage(cause), "error");
        refresh();
      }
    } finally {
      dragSession.current.pending.delete(source.id);
      setBusy(false);
    }
  };
  return (
    <section
      className={cn(
        "remote-issue-leaf",
        board && "group-drop-target",
        dragOver && "drag-over",
      )}
      aria-busy={busy}
      data-group-by={grouping}
      data-group-key={parentKey ?? group.key}
      data-sub-group-key={parentKey === undefined ? undefined : group.key}
      onDragOver={
        board
          ? (event) => {
              if (
                event.dataTransfer.types.includes(
                  "application/x-myjira-grouped-work",
                ) &&
                acceptsDrop(dragSession.current.source)
              ) {
                event.preventDefault();
                event.dataTransfer.dropEffect = "move";
                setDragOver(true);
              } else {
                event.dataTransfer.dropEffect = "none";
                setDragOver(false);
              }
            }
          : undefined
      }
      onDragLeave={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null))
          setDragOver(false);
      }}
      onDrop={board ? dropIssue : undefined}
    >
      <button
        className="remote-group-heading"
        aria-expanded={!collapsed}
        onClick={() => setCollapsed(!collapsed)}
      >
        <ChevronDown
          size={13}
          className={collapsed ? "rotate-collapsed" : ""}
        />
        <strong>{title}</strong>
        <Badge>{group.total}</Badge>
      </button>
      {!collapsed && (
        <>
          <ErrorBox message={error} />
          {items.map((source) => {
            const item = appStore.issues.get(source.id) ?? source;
            return board ? (
              <Link
                key={item.id}
                className="remote-group-card"
                draggable={
                  canEdit(item) && !appStore.movingIssueIds.has(item.id)
                }
                data-group-work-item={item.id}
                onDragStart={(event) => {
                  if (
                    !canEdit(item) ||
                    dragSession.current.pending.has(item.id)
                  ) {
                    event.preventDefault();
                    return;
                  }
                  dragSession.current.source = {
                    id: item.id,
                    groups: targetGroups,
                  };
                  event.dataTransfer.effectAllowed = "move";
                  event.dataTransfer.setData(
                    "application/x-myjira-grouped-work",
                    JSON.stringify({ id: item.id, groups: targetGroups }),
                  );
                  event.dataTransfer.setData("text/plain", item.name);
                }}
                onDragEnd={() => {
                  dragSession.current.source = null;
                  setDragOver(false);
                }}
                to={hrefFor?.(item) ?? `${base}/${item.id}`}
              >
                <span>
                  <PriorityIcon priority={item.priority} />
                  <GripVertical size={12} className="text-muted" />
                  <small>
                    {appStore.projects.get(item.project_id)?.identifier}-
                    {item.sequence_id}
                  </small>
                </span>
                <strong>{item.name}</strong>
                {item.target_date && (
                  <time>{shortDate(item.target_date, appStore.locale)}</time>
                )}
              </Link>
            ) : (
              <IssueRow
                key={item.id}
                issue={item}
                base={base}
                selected={selected.includes(item.id)}
                onSelect={onSelect}
                onUpdate={update}
                href={hrefFor?.(item)}
              />
            );
          })}
          {pagination?.has_more && (
            <Button
              className="group-load-more"
              size="sm"
              variant="ghost"
              busy={busy}
              onClick={more}
            >
              {t("加载更多工作项", "Load more work items")} ({items.length}/
              {group.total})
            </Button>
          )}
        </>
      )}
    </section>
  );
});

const IssueGroup = observer(function IssueGroup({
  state,
  issues,
  base,
  selected,
  onSelect,
  onUpdate,
}: {
  state: State;
  issues: WorkItem[];
  base: string;
  selected: string[];
  onSelect: (id: string, checked: boolean) => void;
  onUpdate: (item: WorkItem, patch: Partial<WorkItem>) => void;
}) {
  const [collapsed, setCollapsed] = useState(false);
  const { project } = useScope();
  const canCreate = useCanEditProject();
  const t = appStore.t;
  return (
    <section className="issue-group">
      <div className="issue-group-heading">
        <button
          onClick={() => setCollapsed(!collapsed)}
          aria-expanded={!collapsed}
        >
          <ChevronDown
            size={13}
            className={collapsed ? "rotate-collapsed" : ""}
          />
          <StateIcon state={state} />
          <span>{state.name}</span>
          <Badge>{issues.length}</Badge>
        </button>
        {canCreate && (
          <Button
            variant="ghost"
            size="icon"
            aria-label={t("添加工作项", "Add work item")}
            onClick={() => appStore.setCreateIssueProject(project!.id)}
          >
            <Plus size={14} />
          </Button>
        )}
      </div>
      {!collapsed && (
        <>
          {issues.map((issue) => (
            <IssueRow
              key={issue.id}
              issue={issue}
              base={base}
              selected={selected.includes(issue.id)}
              onSelect={onSelect}
              onUpdate={onUpdate}
            />
          ))}
          {issues.length === 0 && canCreate && (
            <button
              className="empty-group-row"
              onClick={() => appStore.setCreateIssueProject(project!.id)}
            >
              <Plus size={13} />
              {t("添加工作项", "Add work item")}
            </button>
          )}
        </>
      )}
    </section>
  );
});

const IssueRow = observer(function IssueRow({
  issue,
  base,
  selected,
  onSelect,
  onUpdate,
  href,
}: {
  issue: WorkItem;
  base: string;
  selected: boolean;
  onSelect: (id: string, checked: boolean) => void;
  onUpdate: (item: WorkItem, patch: Partial<WorkItem>) => void;
  href?: string;
}) {
  const { project } = useScope();
  const canEdit = useCanEditWorkItem()(issue);
  const canSelect = useCanEditProject(issue.project_id);
  const states = appStore.states.get(issue.project_id) ?? [];
  const labels = appStore.labels.get(issue.project_id) ?? [];
  const members = appStore.members.get(issue.project_id) ?? [];
  const t = appStore.t;
  return (
    <div className={cn("issue-row", selected && "selected")}>
      <input
        type="checkbox"
        disabled={!canSelect}
        checked={selected}
        onChange={(event) => onSelect(issue.id, event.target.checked)}
        aria-label={`${t("选择", "Select")} ${issue.name}`}
      />
      <Menu
        items={states.map((state) => ({
          label: state.name,
          icon: <StateIcon state={state} />,
          onSelect: () => onUpdate(issue, { state_id: state.id }),
        }))}
      >
        <button
          className="state-trigger"
          disabled={!canEdit}
          aria-label={t("更改状态", "Change state")}
        >
          <StateIcon
            state={states.find((state) => state.id === issue.state_id)}
          />
        </button>
      </Menu>
      <Link className="issue-row-link" to={href ?? `${base}/${issue.id}`}>
        <span className="issue-identifier">
          {appStore.projects.get(issue.project_id)?.identifier ??
            project?.identifier}
          -{issue.sequence_id}
        </span>
        <span className="issue-row-title">{issue.name}</span>
      </Link>
      <div className="issue-row-labels">
        {(issue.label_ids ?? []).slice(0, 2).map((id) => {
          const label = labels.find((item) => item.id === id);
          return label ? (
            <span key={id} className="issue-label">
              <span style={{ backgroundColor: label.color }} />
              {label.name}
            </span>
          ) : null;
        })}
      </div>
      {issue.target_date && (
        <span
          className={cn(
            "issue-date",
            issue.target_date < new Date().toISOString().slice(0, 10) &&
              "overdue",
          )}
        >
          <CalendarDays size={12} />
          {shortDate(issue.target_date, appStore.locale)}
        </span>
      )}
      <Menu
        items={priorities.map((priority) => ({
          label: t(...priorityLabels[priority]),
          icon: <PriorityIcon priority={priority} />,
          onSelect: () => onUpdate(issue, { priority }),
        }))}
      >
        <button
          className="priority-trigger"
          disabled={!canEdit}
          aria-label={t("更改优先级", "Change priority")}
        >
          <PriorityIcon priority={issue.priority} />
        </button>
      </Menu>
      <div className="avatar-group">
        {(issue.assignee_ids ?? []).slice(0, 2).map((id) => {
          const member = members.find((item) => item.user_id === id);
          return (
            <Avatar
              key={id}
              name={member ? memberName(member) : "?"}
              size="xs"
            />
          );
        })}
        {!(issue.assignee_ids ?? []).length && (
          <span
            className="unassigned-avatar"
            title={t("未分配", "Unassigned")}
          />
        )}
      </div>
    </div>
  );
});

const Board = observer(function Board({
  issues,
  states,
  base,
  onUpdate,
}: {
  issues: WorkItem[];
  states: State[];
  base: string;
  onUpdate: (item: WorkItem, patch: Partial<WorkItem>) => void;
}) {
  useEffect(
    () =>
      monitorForElements({
        canMonitor: ({ source }) => source.data.kind === "my-jira-issue",
        onDrop: ({ source, location }) => {
          const target = location.current.dropTargets[0];
          if (!target) return;
          const issue = issues.find((item) => item.id === source.data.issueId);
          if (
            issue &&
            typeof target.data.stateId === "string" &&
            target.data.targetId !== issue.id
          ) {
            const stateId = target.data.stateId;
            const position = positionForDrop(
              issues.filter((item) => item.state_id === stateId),
              issue.id,
              typeof target.data.targetId === "string"
                ? target.data.targetId
                : undefined,
              target.data.edge === "before" ? "before" : "after",
            );
            onUpdate(issue, { state_id: stateId, position });
          }
        },
      }),
    [issues, onUpdate],
  );
  return (
    <div className="kanban-board">
      {states.map((state) => (
        <BoardColumn
          key={state.id}
          state={state}
          issues={issues.filter((item) => item.state_id === state.id)}
          base={base}
          onUpdate={onUpdate}
        />
      ))}
    </div>
  );
});

const BoardColumn = observer(function BoardColumn({
  state,
  issues,
  base,
  onUpdate,
}: {
  state: State;
  issues: WorkItem[];
  base: string;
  onUpdate: (item: WorkItem, patch: Partial<WorkItem>) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [over, setOver] = useState(false);
  const { project } = useScope();
  const canCreate = useCanEditProject();
  const t = appStore.t;
  useEffect(() => {
    if (!ref.current) return;
    return dropTargetForElements({
      element: ref.current,
      getData: () => ({ stateId: state.id }),
      canDrop: ({ source }) => source.data.kind === "my-jira-issue",
      onDragEnter: () => setOver(true),
      onDragLeave: () => setOver(false),
      onDrop: () => setOver(false),
    });
  }, [state.id]);
  return (
    <section className={cn("kanban-column", over && "drag-over")} ref={ref}>
      <header>
        <StateIcon state={state} />
        <h2>{state.name}</h2>
        <Badge>{issues.length}</Badge>
        <span className="flex-spacer" />
        {canCreate && (
          <Button
            size="icon"
            variant="ghost"
            aria-label={t("添加工作项", "Add work item")}
            onClick={() => appStore.setCreateIssueProject(project!.id)}
          >
            <Plus size={15} />
          </Button>
        )}
      </header>
      <div className="kanban-cards">
        {issues.map((issue) => (
          <BoardCard
            key={issue.id}
            issue={issue}
            base={base}
            onUpdate={onUpdate}
          />
        ))}
        {canCreate && (
          <button
            className="kanban-add"
            onClick={() => appStore.setCreateIssueProject(project!.id)}
          >
            <Plus size={14} />
            {t("添加工作项", "Add work item")}
          </button>
        )}
      </div>
    </section>
  );
});

const BoardCard = observer(function BoardCard({
  issue,
  base,
  onUpdate,
}: {
  issue: WorkItem;
  base: string;
  onUpdate: (item: WorkItem, patch: Partial<WorkItem>) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const handleRef = useRef<HTMLButtonElement>(null);
  const [dragging, setDragging] = useState(false);
  const { project } = useScope();
  const canEdit = useCanEditWorkItem()(issue);
  const states = appStore.states.get(issue.project_id) ?? [];
  const labels = appStore.labels.get(issue.project_id) ?? [];
  const members = appStore.members.get(issue.project_id) ?? [];
  const t = appStore.t;
  useEffect(() => {
    if (!ref.current || !handleRef.current) return;
    return combine(
      draggable({
        element: ref.current,
        canDrag: () => canEdit,
        dragHandle: handleRef.current,
        getInitialData: () => ({ kind: "my-jira-issue", issueId: issue.id }),
        onDragStart: () => setDragging(true),
        onDrop: () => setDragging(false),
      }),
      dropTargetForElements({
        element: ref.current,
        canDrop: ({ source }) =>
          source.data.kind === "my-jira-issue" &&
          source.data.issueId !== issue.id,
        getData: ({ input, element }) => ({
          stateId: issue.state_id,
          targetId: issue.id,
          edge:
            input.clientY <
            element.getBoundingClientRect().top +
              element.getBoundingClientRect().height / 2
              ? "before"
              : "after",
        }),
      }),
    );
  }, [issue.id, issue.state_id, canEdit]);
  return (
    <div ref={ref} className={cn("kanban-card", dragging && "dragging")}>
      <div className="kanban-card-top">
        <span className="issue-identifier">
          {project?.identifier}-{issue.sequence_id}
        </span>
        <button
          ref={handleRef}
          disabled={!canEdit}
          className="drag-handle"
          aria-label={t("拖动工作项", "Drag work item")}
        >
          <GripVertical size={14} />
        </button>
      </div>
      <Link to={`${base}/${issue.id}`} className="kanban-title">
        {issue.name}
      </Link>
      {(issue.label_ids ?? []).length > 0 && (
        <div className="kanban-labels">
          {issue.label_ids.slice(0, 3).map((id) => {
            const label = labels.find((item) => item.id === id);
            return label ? (
              <span key={id} className="issue-label">
                <span style={{ backgroundColor: label.color }} />
                {label.name}
              </span>
            ) : null;
          })}
        </div>
      )}
      <div className="kanban-card-bottom">
        <PriorityIcon priority={issue.priority} />
        <Menu
          items={states.map((state) => ({
            label: state.name,
            icon: <StateIcon state={state} />,
            onSelect: () => onUpdate(issue, { state_id: state.id }),
          }))}
        >
          <button
            className="state-trigger"
            disabled={!canEdit}
            aria-label={t("移动到其他状态", "Move to another state")}
          >
            <StateIcon
              state={states.find((state) => state.id === issue.state_id)}
            />
          </button>
        </Menu>
        {issue.target_date && (
          <span className="issue-date">
            {shortDate(issue.target_date, appStore.locale)}
          </span>
        )}
        <span className="flex-spacer" />
        <div className="avatar-group">
          {(issue.assignee_ids ?? []).slice(0, 3).map((id) => (
            <Avatar
              key={id}
              name={memberName(
                members.find((member) => member.user_id === id) ?? {},
              )}
              size="xs"
            />
          ))}
        </div>
      </div>
    </div>
  );
});

export const IssueTable = observer(function IssueTable({
  issues,
  base,
  onUpdate,
  hrefFor,
}: {
  issues: WorkItem[];
  base: string;
  onUpdate: (item: WorkItem, patch: Partial<WorkItem>) => void;
  hrefFor?: (item: WorkItem) => string;
}) {
  const { project } = useScope();
  const canEdit = useCanEditWorkItem();
  const t = appStore.t;
  const columns = useMemo<ColumnDef<WorkItem>[]>(
    () => [
      {
        accessorKey: "sequence_id",
        header: t("编号", "ID"),
        size: 110,
        cell: ({ row }) => (
          <span className="issue-identifier">
            {appStore.projects.get(row.original.project_id)?.identifier ??
              project?.identifier}
            -{row.original.sequence_id}
          </span>
        ),
      },
      {
        accessorKey: "name",
        header: t("工作项", "Work item"),
        size: 360,
        cell: ({ row }) => (
          <Link to={hrefFor?.(row.original) ?? `${base}/${row.original.id}`}>
            {row.original.name}
          </Link>
        ),
      },
      {
        accessorKey: "state_id",
        header: t("状态", "State"),
        size: 150,
        cell: ({ row }) => (
          <Select
            disabled={!canEdit(row.original)}
            value={row.original.state_id}
            aria-label={t("状态", "State")}
            onChange={(event) =>
              onUpdate(row.original, { state_id: event.target.value })
            }
          >
            {(appStore.states.get(row.original.project_id) ?? []).map(
              (state) => (
                <option key={state.id} value={state.id}>
                  {state.name}
                </option>
              ),
            )}
          </Select>
        ),
      },
      {
        accessorKey: "priority",
        header: t("优先级", "Priority"),
        size: 120,
        cell: ({ row }) => (
          <span className="inline-property">
            <PriorityIcon priority={row.original.priority} />
            {t(...priorityLabels[row.original.priority])}
          </span>
        ),
      },
      {
        accessorKey: "assignee_ids",
        header: t("负责人", "Assignees"),
        size: 180,
        cell: ({ row }) => (
          <MultiSelect
            disabled={!canEdit(row.original)}
            value={row.original.assignee_ids ?? []}
            onChange={(value) =>
              onUpdate(row.original, { assignee_ids: value })
            }
            options={(appStore.members.get(row.original.project_id) ?? []).map(
              (member) => ({
                value: member.user_id,
                label: memberName(member),
              }),
            )}
            placeholder={t("未分配", "Unassigned")}
          />
        ),
      },
      {
        accessorKey: "target_date",
        header: t("目标日期", "Due date"),
        size: 150,
        cell: ({ row }) => (
          <Input
            disabled={!canEdit(row.original)}
            type="date"
            value={row.original.target_date ?? ""}
            aria-label={t("目标日期", "Due date")}
            onChange={(event) =>
              onUpdate(row.original, {
                target_date: event.target.value || null,
              })
            }
          />
        ),
      },
    ],
    [base, project?.identifier, appStore.locale, onUpdate, hrefFor, canEdit],
  );
  const table = useReactTable({
    data: issues,
    columns,
    getCoreRowModel: getCoreRowModel(),
  });
  const scrollRef = useRef<HTMLDivElement>(null);
  const rows = table.getRowModel().rows;
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 43,
    overscan: 8,
  });
  return (
    <div className="spreadsheet-scroll" ref={scrollRef}>
      <div
        role="table"
        className="spreadsheet"
        style={{ width: table.getTotalSize() }}
      >
        <div role="row" className="spreadsheet-header">
          {table.getHeaderGroups()[0].headers.map((header) => (
            <div
              role="columnheader"
              key={header.id}
              style={{ width: header.getSize() }}
            >
              {flexRender(header.column.columnDef.header, header.getContext())}
            </div>
          ))}
        </div>
        <div
          className="spreadsheet-body"
          style={{ height: virtualizer.getTotalSize() }}
        >
          {virtualizer.getVirtualItems().map((virtual) => {
            const row = rows[virtual.index];
            return (
              <div
                role="row"
                key={row.id}
                className="spreadsheet-row"
                style={{ transform: `translateY(${virtual.start}px)` }}
              >
                {row.getVisibleCells().map((cell) => (
                  <div
                    role="cell"
                    key={cell.id}
                    style={{ width: cell.column.getSize() }}
                  >
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </div>
                ))}
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
});

export const IssueCalendar = observer(function IssueCalendar({
  issues,
  base,
  hrefFor,
  onUpdate,
}: {
  issues: WorkItem[];
  base: string;
  hrefFor?: (item: WorkItem) => string;
  onUpdate?: (item: WorkItem, patch: Partial<WorkItem>) => void;
}) {
  const canEdit = useCanEditWorkItem();
  const [month, setMonth] = useState(
    new Date(new Date().getFullYear(), new Date().getMonth(), 1),
  );
  const t = appStore.t;
  const offset = (month.getDay() + 6) % 7;
  const days = Array.from(
    { length: 42 },
    (_, index) =>
      new Date(month.getFullYear(), month.getMonth(), index - offset + 1),
  );
  const dateKey = (day: Date) =>
    `${day.getFullYear()}-${String(day.getMonth() + 1).padStart(2, "0")}-${String(day.getDate()).padStart(2, "0")}`;
  const dayLabels =
    appStore.locale === "zh-CN"
      ? ["周一", "周二", "周三", "周四", "周五", "周六", "周日"]
      : ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
  const unscheduled = issues.filter((issue) => !issue.target_date);
  return (
    <div className="calendar-view">
      <div className="calendar-toolbar">
        <h2>
          {new Intl.DateTimeFormat(appStore.locale, {
            year: "numeric",
            month: "long",
          }).format(month)}
        </h2>
        <Button
          size="sm"
          onClick={() =>
            setMonth(
              new Date(new Date().getFullYear(), new Date().getMonth(), 1),
            )
          }
        >
          {t("今天", "Today")}
        </Button>
        <Button
          variant="ghost"
          size="icon"
          onClick={() =>
            setMonth(new Date(month.getFullYear(), month.getMonth() - 1, 1))
          }
          aria-label={t("上个月", "Previous month")}
        >
          <ChevronLeft size={16} />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          onClick={() =>
            setMonth(new Date(month.getFullYear(), month.getMonth() + 1, 1))
          }
          aria-label={t("下个月", "Next month")}
        >
          <ChevronRight size={16} />
        </Button>
        <span className="flex-spacer" />
        <span className="text-muted">
          {unscheduled.length}{" "}
          {t("项未设置目标日期", "items without a due date")}
        </span>
      </div>
      <div className="calendar-grid">
        {dayLabels.map((label) => (
          <div className="calendar-weekday" key={label}>
            {label}
          </div>
        ))}
        {days.map((day) => {
          const key = dateKey(day);
          const today = key === dateKey(new Date());
          return (
            <div
              key={key}
              data-calendar-date={key}
              onDragOver={(event) => {
                if (
                  onUpdate &&
                  event.dataTransfer.types.includes(
                    "application/x-myjira-calendar",
                  )
                ) {
                  event.preventDefault();
                  event.dataTransfer.dropEffect = "move";
                }
              }}
              onDrop={(event) => {
                const id = event.dataTransfer.getData(
                  "application/x-myjira-calendar",
                );
                const item = issues.find((item) => item.id === id);
                if (!item || !onUpdate) return;
                event.preventDefault();
                const patch: Partial<WorkItem> = { target_date: key };
                if (item.start_date && item.target_date) {
                  const duration =
                    new Date(item.target_date).getTime() -
                    new Date(item.start_date).getTime();
                  patch.start_date = new Date(
                    new Date(key).getTime() - duration,
                  )
                    .toISOString()
                    .slice(0, 10);
                } else if (item.start_date && item.start_date > key)
                  patch.start_date = key;
                onUpdate(item, patch);
              }}
              className={cn(
                "calendar-day",
                day.getMonth() !== month.getMonth() && "outside-month",
              )}
            >
              <span className={cn("calendar-date", today && "today")}>
                {day.getDate()}
              </span>
              {issues
                .filter((issue) => issue.target_date === key)
                .map((issue) => (
                  <Link
                    key={issue.id}
                    to={hrefFor?.(issue) ?? `${base}/${issue.id}`}
                    className="calendar-issue"
                    draggable={!!onUpdate && canEdit(issue)}
                    onDragStart={(event) => {
                      event.dataTransfer.effectAllowed = "move";
                      event.dataTransfer.setData(
                        "application/x-myjira-calendar",
                        issue.id,
                      );
                    }}
                  >
                    <PriorityIcon priority={issue.priority} />
                    <span>{issue.name}</span>
                  </Link>
                ))}
            </div>
          );
        })}
      </div>
      {unscheduled.length > 0 && (
        <div className="unscheduled-items">
          <h3>{t("待安排", "Unscheduled")}</h3>
          {unscheduled.map((issue) => (
            <Link
              key={issue.id}
              to={hrefFor?.(issue) ?? `${base}/${issue.id}`}
              draggable={!!onUpdate && canEdit(issue)}
              onDragStart={(event) => {
                event.dataTransfer.effectAllowed = "move";
                event.dataTransfer.setData(
                  "application/x-myjira-calendar",
                  issue.id,
                );
              }}
            >
              {issue.name}
              <CalendarDays size={13} />
            </Link>
          ))}
        </div>
      )}
    </div>
  );
});

export const IssueTimeline = observer(function IssueTimeline({
  issues,
  base,
  hrefFor,
  onUpdate,
}: {
  issues: WorkItem[];
  base: string;
  hrefFor?: (item: WorkItem) => string;
  onUpdate?: (item: WorkItem, patch: Partial<WorkItem>) => void;
}) {
  const t = appStore.t;
  const canEdit = useCanEditWorkItem();
  const [scale, setScale] = useState("week");
  const [month, setMonth] = useState(
    new Date(
      Date.UTC(new Date().getUTCFullYear(), new Date().getUTCMonth(), 1),
    ),
  );
  const [dragDay, setDragDay] = useState<{ id: string; day: number } | null>(
    null,
  );
  const scheduled = issues.filter(
    (issue) => issue.start_date || issue.target_date,
  );
  const dayMS = 86400000;
  const months =
    scale === "day" ? 1 : scale === "week" ? 3 : scale === "month" ? 6 : 12;
  const unit =
    scale === "day" ? 32 : scale === "week" ? 16 : scale === "month" ? 8 : 4;
  const tick =
    scale === "day" ? 1 : scale === "week" ? 7 : scale === "month" ? 15 : 30;
  const start = month.getTime();
  const end = Date.UTC(month.getUTCFullYear(), month.getUTCMonth() + months, 1);
  const totalDays = Math.round((end - start) / dayMS);
  const dateAt = (index: number) =>
    new Date(start + index * dayMS).toISOString().slice(0, 10);
  const dayIndex = (date: string) =>
    Math.round((new Date(date).getTime() - start) / dayMS);
  const pointerDay = (event: DragEvent<HTMLElement>) =>
    Math.max(
      0,
      Math.min(
        totalDays - 1,
        Math.floor(
          (event.clientX - event.currentTarget.getBoundingClientRect().left) /
            unit,
        ),
      ),
    );
  const drag = (
    event: DragEvent<HTMLElement>,
    issue: WorkItem,
    mode: "move" | "start" | "end",
  ) => {
    if (!onUpdate) return;
    event.stopPropagation();
    const track = event.currentTarget.closest<HTMLElement>(".timeline-track")!;
    const from = dayIndex(issue.start_date || issue.target_date || dateAt(0));
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData(
      "application/x-myjira-timeline",
      JSON.stringify({
        id: issue.id,
        mode,
        offset:
          Math.floor(
            (event.clientX - track.getBoundingClientRect().left) / unit,
          ) - from,
      }),
    );
  };
  const drop = (event: DragEvent<HTMLElement>, issue: WorkItem) => {
    if (
      !onUpdate ||
      !event.dataTransfer.types.includes("application/x-myjira-timeline")
    )
      return;
    event.preventDefault();
    setDragDay(null);
    let source: { id: string; mode: string; offset: number };
    try {
      source = JSON.parse(
        event.dataTransfer.getData("application/x-myjira-timeline"),
      );
    } catch {
      return;
    }
    if (source.id !== issue.id) return;
    const date = pointerDay(event);
    const from = dayIndex(
      issue.start_date || issue.target_date || dateAt(date),
    );
    const to = dayIndex(issue.target_date || issue.start_date || dateAt(date));
    if (source.mode === "start")
      onUpdate(issue, { start_date: dateAt(Math.min(date, to)) });
    else if (source.mode === "end")
      onUpdate(issue, { target_date: dateAt(Math.max(date, from)) });
    else {
      const next = date - source.offset;
      onUpdate(issue, {
        start_date: dateAt(next),
        target_date: dateAt(next + Math.max(0, to - from)),
      });
    }
  };
  return (
    <div className="timeline-view">
      <div className="timeline-caption">
        <GanttChart size={16} />
        <Button
          variant="ghost"
          size="icon"
          aria-label={t("上一段时间", "Previous period")}
          onClick={() =>
            setMonth(
              new Date(
                Date.UTC(
                  month.getUTCFullYear(),
                  month.getUTCMonth() - months,
                  1,
                ),
              ),
            )
          }
        >
          <ChevronLeft size={14} />
        </Button>
        <span>
          {shortDate(dateAt(0), appStore.locale)} —{" "}
          {shortDate(dateAt(totalDays - 1), appStore.locale)}
        </span>
        <Button
          variant="ghost"
          size="icon"
          aria-label={t("下一段时间", "Next period")}
          onClick={() =>
            setMonth(
              new Date(
                Date.UTC(
                  month.getUTCFullYear(),
                  month.getUTCMonth() + months,
                  1,
                ),
              ),
            )
          }
        >
          <ChevronRight size={14} />
        </Button>
        <Button
          size="sm"
          onClick={() =>
            setMonth(
              new Date(
                Date.UTC(
                  new Date().getUTCFullYear(),
                  new Date().getUTCMonth(),
                  1,
                ),
              ),
            )
          }
        >
          {t("今天", "Today")}
        </Button>
        <span className="flex-spacer" />
        <Badge>
          {scheduled.length} / {issues.length}
        </Badge>
        <Select
          aria-label={t("甘特图缩放", "Gantt scale")}
          value={scale}
          onChange={(event) => setScale(event.target.value)}
        >
          <option value="day">{t("日", "Day")}</option>
          <option value="week">{t("周", "Week")}</option>
          <option value="month">{t("月", "Month")}</option>
          <option value="quarter">{t("季度", "Quarter")}</option>
        </Select>
      </div>
      <div className="timeline-scroll">
        <div
          className="timeline-content"
          style={{
            width: 240 + totalDays * unit,
            minWidth: 240 + totalDays * unit,
          }}
        >
          <div className="timeline-header">
            <span>{t("工作项", "Work item")}</span>
            <div style={{ width: totalDays * unit }}>
              {Array.from(
                { length: Math.ceil(totalDays / tick) },
                (_, index) => (
                  <span
                    key={index}
                    style={{ width: tick * unit, flexBasis: tick * unit }}
                  >
                    {scale === "day"
                      ? new Date(start + index * tick * dayMS).getUTCDate()
                      : shortDate(dateAt(index * tick), appStore.locale)}
                  </span>
                ),
              )}
            </div>
          </div>
          {issues.map((issue) => {
            const from = dayIndex(
              issue.start_date || issue.target_date || dateAt(0),
            );
            const to = dayIndex(
              issue.target_date || issue.start_date || dateAt(0),
            );
            const left = Math.max(0, from);
            const width = Math.min(totalDays, to + 1) - left;
            return (
              <div key={issue.id} className="timeline-row">
                <div className="timeline-row-label">
                  <Link to={hrefFor?.(issue) ?? `${base}/${issue.id}`}>
                    {issue.name}
                  </Link>
                  {onUpdate && canEdit(issue) && (
                    <Input
                      type="date"
                      aria-label={`${t("工作项目标日期", "Work item due date")} ${issue.name}`}
                      value={issue.target_date ?? ""}
                      onChange={(event) =>
                        onUpdate(issue, {
                          target_date: event.target.value || null,
                        })
                      }
                    />
                  )}
                </div>
                <div
                  className="timeline-track"
                  data-timeline-item={issue.id}
                  style={{
                    width: totalDays * unit,
                    backgroundImage:
                      "linear-gradient(90deg, var(--border) 1px, transparent 1px)",
                    backgroundSize: `${unit}px 100%`,
                  }}
                  onDragOver={(event) => {
                    if (
                      onUpdate &&
                      event.dataTransfer.types.includes(
                        "application/x-myjira-timeline",
                      )
                    ) {
                      event.preventDefault();
                      event.dataTransfer.dropEffect = "move";
                      setDragDay({ id: issue.id, day: pointerDay(event) });
                    }
                  }}
                  onDragLeave={(event) => {
                    if (
                      !event.currentTarget.contains(
                        event.relatedTarget as Node | null,
                      )
                    )
                      setDragDay(null);
                  }}
                  onDrop={(event) => drop(event, issue)}
                >
                  {dragDay?.id === issue.id && (
                    <span
                      className="timeline-drop-day"
                      style={{ left: dragDay.day * unit }}
                    >
                      {shortDate(dateAt(dragDay.day), appStore.locale)}
                    </span>
                  )}
                  {(issue.start_date || issue.target_date) && width > 0 && (
                    <div
                      style={{
                        left: left * unit,
                        width: Math.max(unit - 2, width * unit - 2),
                      }}
                      className="timeline-bar"
                      draggable={!!onUpdate && canEdit(issue)}
                      onDragStart={(event) => drag(event, issue, "move")}
                      onDragEnd={() => setDragDay(null)}
                      title={`${issue.name}: ${issue.start_date || "—"} → ${issue.target_date || "—"}`}
                    >
                      {onUpdate && canEdit(issue) && (
                        <button
                          type="button"
                          className="timeline-resize-handle start"
                          draggable
                          aria-label={`${t("调整开始日期", "Resize start date")} ${issue.name}`}
                          onDragStart={(event) => drag(event, issue, "start")}
                          onKeyDown={(event) => {
                            if (
                              ["ArrowLeft", "ArrowRight"].includes(event.key)
                            ) {
                              event.preventDefault();
                              onUpdate(issue, {
                                start_date: dateAt(
                                  Math.min(
                                    to,
                                    from + (event.key === "ArrowLeft" ? -1 : 1),
                                  ),
                                ),
                              });
                            }
                          }}
                        />
                      )}
                      <Link
                        draggable={false}
                        to={hrefFor?.(issue) ?? `${base}/${issue.id}`}
                      >
                        {issue.name}
                      </Link>
                      {onUpdate && canEdit(issue) && (
                        <button
                          type="button"
                          className="timeline-resize-handle end"
                          draggable
                          aria-label={`${t("调整结束日期", "Resize end date")} ${issue.name}`}
                          onDragStart={(event) => drag(event, issue, "end")}
                          onKeyDown={(event) => {
                            if (
                              ["ArrowLeft", "ArrowRight"].includes(event.key)
                            ) {
                              event.preventDefault();
                              onUpdate(issue, {
                                target_date: dateAt(
                                  Math.max(
                                    from,
                                    to + (event.key === "ArrowLeft" ? -1 : 1),
                                  ),
                                ),
                              });
                            }
                          }}
                        />
                      )}
                    </div>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
});
