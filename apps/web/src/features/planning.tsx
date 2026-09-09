import { useEffect, useState, type FormEvent } from "react";
import { observer } from "mobx-react-lite";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  Archive,
  ArrowLeft,
  ArrowRight,
  Boxes,
  CalendarDays,
  CheckCircle2,
  CircleDashed,
  Copy,
  LayoutGrid,
  GanttChart,
  ExternalLink,
  Link2,
  LockKeyhole,
  Plus,
  Search,
  Sparkles,
  Trash2,
} from "lucide-react";
import { appStore } from "../stores/app-store";
import {
  useDebouncedValue,
  useMutation,
  useRemote,
  useScope,
} from "../lib/hooks";
import { api, errorMessage, projectPath, workspacePath } from "../lib/api";
import { memberName, shortDate } from "../lib/utils";
import {
  Badge,
  Button,
  Confirm,
  EmptyState,
  ErrorBox,
  Field,
  Input,
  Loading,
  Menu,
  Modal,
  MultiSelect,
  PageHeader,
  Select,
} from "../components/ui";
import {
  GroupedIssueResults,
  groupChoices,
  IssueCalendar,
  IssuesPage,
  IssueTable,
  IssueTimeline,
} from "./issues/issue-list";
import {
  AdvancedFilterBuilder,
  filtersToQuery,
  type FilterExpression,
} from "../components/advanced-filters";
import type { Cycle, Layout, Module, SavedView, WorkItem } from "../types";
import { FavoriteButton } from "../components/favorite-button";
import {
  PlanningProgress,
  type PlanningProgressData,
} from "./planning-progress";

type PlanningItem = Cycle & Module;

const moduleStatusLabels: Record<string, [string, string]> = {
  backlog: ["待整理", "Backlog"],
  planned: ["已计划", "Planned"],
  "in-progress": ["进行中", "In progress"],
  paused: ["暂停", "Paused"],
  completed: ["已完成", "Completed"],
  cancelled: ["已取消", "Cancelled"],
};
export const viewLayouts = [
  { value: "list", ui: "list" as Layout, zh: "列表", en: "List" },
  { value: "kanban", ui: "board" as Layout, zh: "看板", en: "Board" },
  { value: "calendar", ui: "calendar" as Layout, zh: "日历", en: "Calendar" },
  { value: "gantt", ui: "timeline" as Layout, zh: "时间线", en: "Timeline" },
  {
    value: "spreadsheet",
    ui: "table" as Layout,
    zh: "表格",
    en: "Spreadsheet",
  },
];

export const PlanningPage = observer(function PlanningPage({
  kind,
}: {
  kind: "cycles" | "modules";
}) {
  const { workspace, project } = useScope();
  const { planningId } = useParams();
  const navigate = useNavigate();
  const [search, setSearch] = useState("");
  const [archived, setArchived] = useState(false);
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<PlanningItem | null>(null);
  const [deleting, setDeleting] = useState<PlanningItem | null>(null);
  const [assignOpen, setAssignOpen] = useState(false);
  const [assignIds, setAssignIds] = useState<string[]>([]);
  const [assignSearch, setAssignSearch] = useState("");
  const assignQuery = useDebouncedValue(assignSearch);
  const [assignCursor, setAssignCursor] = useState("");
  const [availableItems, setAvailableItems] = useState<WorkItem[]>([]);
  const [availableCache, setAvailableCache] = useState<
    Record<string, WorkItem>
  >({});
  const [transferOpen, setTransferOpen] = useState(false);
  const [transferTarget, setTransferTarget] = useState("");
  const [revision, setRevision] = useState(0);
  const [overviewLayout, setOverviewLayout] = useState("grid");
  const [detailTab, setDetailTab] = useState("work");
  const [liveProgress, setLiveProgress] = useState(false);
  const mutation = useMutation();
  const base = projectPath(workspace.id, project!.id);
  const collection = `${base}/${kind}`;
  const items = useRemote<PlanningItem[]>(
    `${collection}?archived=${archived}&search=${encodeURIComponent(search)}`,
  );
  const detail = useRemote<PlanningItem>(
    planningId ? `${collection}/${planningId}` : null,
  );
  const progress = useRemote<PlanningProgressData>(
    planningId
      ? `${collection}/${planningId}/progress${liveProgress ? "?live=true" : ""}`
      : null,
  );
  const available = useRemote<WorkItem[]>(
    assignOpen
      ? `${base}/issues?limit=100&search=${encodeURIComponent(assignQuery)}${assignCursor ? `&cursor=${encodeURIComponent(assignCursor)}` : ""}`
      : null,
  );
  useEffect(() => {
    setAssignCursor("");
    setAvailableItems([]);
  }, [assignQuery, assignOpen]);
  useEffect(() => {
    if (!available.data) return;
    setAvailableItems((previous) => [
      ...new Map(
        [...(assignCursor ? previous : []), ...available.data!].map((item) => [
          item.id,
          item,
        ]),
      ).values(),
    ]);
    setAvailableCache((previous) => ({
      ...previous,
      ...Object.fromEntries(available.data!.map((item) => [item.id, item])),
    }));
  }, [available.data, assignCursor]);
  const t = appStore.t;
  const isCycle = kind === "cycles";
  const singular = isCycle ? t("迭代周期", "Cycle") : t("功能模块", "Module");
  const title = isCycle ? t("迭代周期", "Cycles") : t("功能模块", "Modules");
  const Icon = isCycle ? Sparkles : Boxes;
  const route = `/w/${workspace.slug}/projects/${project!.id}/${kind}`;
  const refresh = () => {
    items.refresh();
    detail.refresh();
    progress.refresh();
    setRevision((value) => value + 1);
  };

  if (planningId) {
    if (detail.loading) return <Loading />;
    if (detail.error || !detail.data)
      return (
        <ErrorBox
          message={
            detail.error || t("找不到这个规划", "Planning item not found")
          }
          retry={detail.refresh}
        />
      );
  }

  const menu = (item: PlanningItem) => [
    {
      label: t("编辑", "Edit"),
      onSelect: () => {
        setEditing(item);
        setFormOpen(true);
      },
    },
    {
      label: item.archived_at
        ? t("取消归档", "Unarchive")
        : t("归档", "Archive"),
      icon: <Archive size={14} />,
      onSelect: () => {
        mutation.execute(async () => {
          await api.patch(`${collection}/${item.id}`, {
            archived: !item.archived_at,
          });
          refresh();
        });
      },
    },
    {
      label: t("删除", "Delete"),
      icon: <Trash2 size={14} />,
      danger: true,
      onSelect: () => setDeleting(item),
    },
  ];

  return (
    <div className={planningId ? "planning-detail" : "page-scroll"}>
      <PageHeader
        eyebrow={planningId ? title : undefined}
        title={planningId ? detail.data!.name : title}
        description={
          planningId
            ? detail.data!.description
            : isCycle
              ? t(
                  "为团队设定节奏，专注完成一个阶段的目标。",
                  "Set a rhythm for your team and focus on the next milestone.",
                )
              : t(
                  "围绕功能与主题，把相关工作组织起来。",
                  "Organize related work around a feature or an initiative.",
                )
        }
        actions={
          planningId ? (
            <>
              <FavoriteButton
                entityType={isCycle ? "cycle" : "module"}
                entityId={planningId}
              />
              <Button variant="ghost" size="sm" onClick={() => navigate(route)}>
                <ArrowLeft size={14} />
                {t("返回", "Back")}
              </Button>
              <Button size="sm" onClick={() => setAssignOpen(true)}>
                <Plus size={14} />
                {t("添加已有工作项", "Add existing work")}
              </Button>
              {isCycle && (
                <Button size="sm" onClick={() => setTransferOpen(true)}>
                  <ArrowRight size={14} />
                  {t("转移工作项", "Transfer work")}
                </Button>
              )}
              <Menu items={menu(detail.data!)} />
            </>
          ) : (
            <Button
              variant="primary"
              onClick={() => {
                setEditing(null);
                setFormOpen(true);
              }}
            >
              <Plus size={15} />
              {t("新建", "New")} {singular}
            </Button>
          )
        }
      >
        {planningId ? (
          <div className="planning-summary">
            <span>
              <CalendarDays size={14} />
              {shortDate(detail.data!.start_date, appStore.locale)} —{" "}
              {shortDate(
                isCycle ? detail.data!.end_date : detail.data!.target_date,
                appStore.locale,
              )}
            </span>
            {progress.data && (
              <>
                <span>
                  <CheckCircle2 size={14} />
                  {progress.data.completed} / {progress.data.total}{" "}
                  {t("已完成", "completed")}
                </span>
                <div className="progress-track">
                  <i
                    style={{ width: `${progress.data.completion_percentage}%` }}
                  />
                </div>
                <Badge>
                  {Math.round(progress.data.completion_percentage)}%
                </Badge>
              </>
            )}
          </div>
        ) : (
          <div className="page-tabs">
            <button
              className={!archived ? "active" : ""}
              onClick={() => setArchived(false)}
            >
              {t("全部", "All")}
            </button>
            <button
              className={archived ? "active" : ""}
              onClick={() => setArchived(true)}
            >
              {t("已归档", "Archived")}
            </button>
            <span className="flex-spacer" />
            {!isCycle && (
              <div className="layout-switcher">
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={t("模块卡片", "Module cards")}
                  onClick={() => setOverviewLayout("grid")}
                >
                  <LayoutGrid size={14} />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={t("模块甘特图", "Module Gantt overview")}
                  onClick={() => setOverviewLayout("timeline")}
                >
                  <GanttChart size={14} />
                </Button>
              </div>
            )}
            <div className="search-input compact">
              <Search size={14} />
              <Input
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder={t("搜索…", "Search…")}
                aria-label={t("搜索规划", "Search planning items")}
              />
            </div>
          </div>
        )}
      </PageHeader>
      <ErrorBox message={items.error || mutation.error} />
      {planningId && (
        <div className="page-tabs planning-detail-tabs">
          <button
            className={detailTab === "work" ? "active" : ""}
            onClick={() => setDetailTab("work")}
          >
            {t("工作项", "Work items")}
          </button>
          <button
            className={detailTab === "progress" ? "active" : ""}
            onClick={() => setDetailTab("progress")}
          >
            {t("进度分析", "Progress analysis")}
          </button>
          {isCycle && (
            <label className="checkbox-label">
              <input
                type="checkbox"
                checked={liveProgress}
                onChange={(event) => setLiveProgress(event.target.checked)}
              />
              {t("当前剩余工作统计", "Current remaining work statistics")}
            </label>
          )}
        </div>
      )}
      {planningId && !isCycle && (
        <ModuleLinks collection={`${collection}/${planningId}/links`} />
      )}
      {planningId ? (
        detailTab === "progress" ? (
          <div className="page-scroll">
            <ErrorBox message={progress.error} />
            {progress.loading ? (
              <Loading />
            ) : (
              progress.data && <PlanningProgress data={progress.data} />
            )}
          </div>
        ) : (
          <IssuesPage
            key={`${planningId}-${revision}`}
            embedded
            title={detail.data?.name}
            extraQuery={`${isCycle ? "cycle_id" : "module_id"}=${planningId}`}
          />
        )
      ) : items.loading ? (
        <Loading />
      ) : (
        <div className="page-body">
          {!items.data?.length ? (
            <EmptyState
              icon={<Icon size={30} />}
              title={
                isCycle
                  ? t("给下一段工作一个节奏", "Give your next phase a rhythm")
                  : t("让相关工作聚在一起", "Bring related work together")
              }
              description={
                isCycle
                  ? t(
                      "创建周期，设定时间范围并分配工作项。",
                      "Create a cycle, set its dates, and add work items.",
                    )
                  : t(
                      "创建模块，将大目标拆成清晰的工作集合。",
                      "Create a module to break a big goal into a focused collection of work.",
                    )
              }
              action={
                <Button
                  variant="primary"
                  onClick={() => {
                    setEditing(null);
                    setFormOpen(true);
                  }}
                >
                  <Plus size={15} />
                  {t("新建", "New")} {singular}
                </Button>
              }
            />
          ) : !isCycle && overviewLayout === "timeline" ? (
            <ModuleOverviewTimeline
              items={items.data}
              route={route}
              onEdit={(item) => {
                setEditing(item);
                setFormOpen(true);
              }}
            />
          ) : (
            <div className="planning-grid">
              {items.data.map((item) => (
                <article key={item.id} className="planning-card">
                  <div className="planning-card-heading">
                    <span className="planning-icon">
                      <Icon size={20} />
                    </span>
                    {!isCycle && (
                      <Badge>
                        {t(
                          ...(moduleStatusLabels[item.status] ??
                            moduleStatusLabels.planned),
                        )}
                      </Badge>
                    )}
                    <span className="flex-spacer" />
                    <Menu items={menu(item)} />
                  </div>
                  <Link className="planning-title" to={`${route}/${item.id}`}>
                    {item.name}
                  </Link>
                  <p>
                    {item.description ||
                      t(
                        "添加说明，与团队分享这段工作的目标。",
                        "Add a description to share the purpose with your team.",
                      )}
                  </p>
                  <div className="planning-card-footer">
                    <CalendarDays size={14} />
                    <span>
                      {shortDate(item.start_date, appStore.locale)} —{" "}
                      {shortDate(
                        isCycle ? item.end_date : item.target_date,
                        appStore.locale,
                      )}
                    </span>
                    <span className="flex-spacer" />
                    <ArrowRight size={15} />
                  </div>
                </article>
              ))}
            </div>
          )}
        </div>
      )}
      <PlanningForm
        open={formOpen}
        onOpenChange={setFormOpen}
        kind={kind}
        base={collection}
        item={editing}
        onSaved={refresh}
      />
      <Confirm
        open={!!deleting}
        onOpenChange={(open) => {
          if (!open) setDeleting(null);
        }}
        title={t(
          `删除「${deleting?.name ?? ""}」？`,
          `Delete “${deleting?.name ?? ""}”?`,
        )}
        description={t(
          "相关工作项仍保留在项目中，这个规划及其关联将被移除。",
          "Work items stay in the project. This planning item and its associations will be removed.",
        )}
        busy={mutation.busy}
        onConfirm={() => {
          mutation.execute(async () => {
            await api.delete(`${collection}/${deleting!.id}`);
            setDeleting(null);
            refresh();
            if (planningId) navigate(route);
          });
        }}
      />
      <Modal
        open={assignOpen}
        onOpenChange={setAssignOpen}
        title={t("添加已有工作项", "Add existing work items")}
      >
        <div className="form-stack modal-body">
          <Input
            aria-label={t("搜索已有工作项", "Search existing work items")}
            placeholder={t("搜索名称或编号…", "Search title or identifier…")}
            value={assignSearch}
            onChange={(event) => setAssignSearch(event.target.value)}
          />
          <MultiSelect
            value={assignIds}
            onChange={setAssignIds}
            options={[
              ...new Map(
                [
                  ...availableItems,
                  ...assignIds
                    .map((id) => availableCache[id])
                    .filter((item): item is WorkItem => !!item),
                ].map((item) => [item.id, item]),
              ).values(),
            ].map((item) => ({
              value: item.id,
              label: `${project!.identifier}-${item.sequence_id} ${item.name}`,
            }))}
            placeholder={t("选择工作项", "Choose work items")}
          />
          <div className="settings-toolbar">
            <span className="text-muted">
              {availableItems.length} / {available.pagination?.total ?? 0}
            </span>
            {available.pagination?.has_more && (
              <Button
                size="sm"
                busy={available.loading}
                onClick={() =>
                  setAssignCursor(available.pagination?.next_cursor ?? "")
                }
              >
                {t("加载更多工作项", "Load more work items")}
              </Button>
            )}
          </div>
          <ErrorBox message={available.error || mutation.error} />
          <div className="modal-footer">
            <Button
              variant="primary"
              disabled={!assignIds.length}
              busy={mutation.busy}
              onClick={() => {
                mutation.execute(async () => {
                  await api.post(`${collection}/${planningId}/items`, {
                    work_item_ids: assignIds,
                  });
                  setAssignOpen(false);
                  setAssignIds([]);
                  refresh();
                });
              }}
            >
              {t("添加", "Add")}
            </Button>
          </div>
        </div>
      </Modal>
      <Modal
        open={transferOpen}
        onOpenChange={setTransferOpen}
        title={t("将工作项转移到另一个周期", "Transfer work to another cycle")}
      >
        <div className="form-stack modal-body">
          <p className="settings-note">
            {t(
              "转移未完成的工作项，并保留来源周期转移前的进度快照。已完成或已取消的工作会保留在来源周期。",
              "Move unfinished work and retain a progress snapshot of the source cycle. Completed and cancelled work stays in the source cycle.",
            )}
          </p>
          <Field label={t("目标周期", "Destination cycle")}>
            <Select
              value={transferTarget}
              onChange={(event) => setTransferTarget(event.target.value)}
            >
              <option value="">{t("选择周期", "Choose cycle")}</option>
              {items.data
                ?.filter(
                  (item) =>
                    item.id !== planningId &&
                    !item.archived_at &&
                    (!item.end_date ||
                      item.end_date >= new Date().toISOString().slice(0, 10)),
                )
                .map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name}
                  </option>
                ))}
            </Select>
          </Field>
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button
              variant="primary"
              disabled={!transferTarget}
              busy={mutation.busy}
              onClick={() => {
                mutation.execute(async () => {
                  await api.post(`${collection}/${planningId}/transfer`, {
                    target_cycle_id: transferTarget,
                  });
                  setTransferOpen(false);
                  refresh();
                });
              }}
            >
              {t("转移未完成工作项", "Transfer unfinished work")}
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  );
});

const ModuleLinks = observer(function ModuleLinks({
  collection,
}: {
  collection: string;
}) {
  type ResourceLink = { id: string; title: string; url: string };
  const links = useRemote<ResourceLink[]>(collection);
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<ResourceLink | null>(null);
  const [form, setForm] = useState({ title: "", url: "https://" });
  const mutation = useMutation();
  const t = appStore.t;
  return (
    <div className="module-resource-bar">
      <Link2 size={14} />
      {links.data?.map((link) => (
        <div className="inline-property" key={link.id}>
          <a
            href={/^https?:\/\//i.test(link.url) ? link.url : "#"}
            target="_blank"
            rel="noreferrer"
          >
            {link.title || link.url}
            <ExternalLink size={11} />
          </a>
          <Menu
            items={[
              {
                label: t("编辑链接", "Edit link"),
                onSelect: () => {
                  setEditing(link);
                  setForm({ title: link.title, url: link.url });
                  setOpen(true);
                },
              },
              {
                label: t("删除链接", "Delete link"),
                danger: true,
                onSelect: () =>
                  mutation.execute(async () => {
                    await api.delete(`${collection}/${link.id}`);
                    links.refresh();
                  }),
              },
            ]}
          />
        </div>
      ))}
      <Button
        variant="ghost"
        size="sm"
        onClick={() => {
          setEditing(null);
          setForm({ title: "", url: "https://" });
          setOpen(true);
        }}
      >
        <Plus size={12} />
        {t("添加模块链接", "Add module link")}
      </Button>
      <ErrorBox message={links.error || mutation.error} />
      <Modal
        open={open}
        onOpenChange={setOpen}
        title={t("模块链接", "Module link")}
      >
        <form
          className="form-stack modal-body"
          onSubmit={(event) => {
            event.preventDefault();
            mutation.execute(async () => {
              if (editing) await api.patch(`${collection}/${editing.id}`, form);
              else await api.post(collection, form);
              setOpen(false);
              links.refresh();
            });
          }}
        >
          <Field label={t("标题", "Title")}>
            <Input
              value={form.title}
              onChange={(event) =>
                setForm({ ...form, title: event.target.value })
              }
              required
            />
          </Field>
          <Field label={t("链接地址", "URL")}>
            <Input
              type="url"
              value={form.url}
              onChange={(event) =>
                setForm({ ...form, url: event.target.value })
              }
              required
            />
          </Field>
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button variant="primary" type="submit" busy={mutation.busy}>
              {t("保存", "Save")}
            </Button>
          </div>
        </form>
      </Modal>
    </div>
  );
});

const ModuleOverviewTimeline = observer(function ModuleOverviewTimeline({
  items,
  route,
  onEdit,
}: {
  items: PlanningItem[];
  route: string;
  onEdit: (item: PlanningItem) => void;
}) {
  const [zoom, setZoom] = useState("week");
  const day = 86400000;
  const dates = items.flatMap((item) =>
    [item.start_date, item.target_date]
      .filter((value): value is string => !!value)
      .map((date) => new Date(date).getTime()),
  );
  const start =
    Math.floor(Math.min(Date.now(), ...dates) / day) * day - 3 * day;
  const end = Math.max(start + 30 * day, ...dates) + 7 * day;
  const px = zoom === "day" ? 24 : zoom === "week" ? 9 : 3;
  const step = zoom === "day" ? 1 : zoom === "week" ? 7 : 30;
  const days = Math.ceil((end - start) / day);
  const width = Math.max(650, days * px);
  const t = appStore.t;
  return (
    <div className="module-timeline">
      <div className="settings-toolbar">
        <span className="text-muted">
          {t("模块时间安排", "Module schedule")}
        </span>
        <Select
          aria-label={t("时间缩放", "Timeline scale")}
          value={zoom}
          onChange={(event) => setZoom(event.target.value)}
        >
          <option value="day">{t("日", "Day")}</option>
          <option value="week">{t("周", "Week")}</option>
          <option value="month">{t("月", "Month")}</option>
        </Select>
      </div>
      <div className="module-timeline-scroll">
        <div style={{ width: width + 180 }}>
          <div className="module-timeline-row">
            <strong>{t("模块", "Module")}</strong>
            <div className="module-timeline-track" style={{ width }}>
              {Array.from({ length: Math.ceil(days / step) }, (_, index) => (
                <span
                  className="module-timeline-tick"
                  key={index}
                  style={{ left: index * step * px }}
                >
                  {shortDate(
                    new Date(start + index * step * day).toISOString(),
                    appStore.locale,
                  )}
                </span>
              ))}
            </div>
          </div>
          {items.map((item) => (
            <div className="module-timeline-row" key={item.id}>
              <div className="module-timeline-label">
                <Link to={`${route}/${item.id}`}>{item.name}</Link>
                <Button
                  size="icon"
                  variant="ghost"
                  aria-label={`${t("编辑日期", "Edit dates")} ${item.name}`}
                  onClick={() => onEdit(item)}
                >
                  <CalendarDays size={13} />
                </Button>
              </div>
              <div
                className="module-timeline-track"
                style={{ width, backgroundSize: `${step * px}px 100%` }}
              >
                {item.start_date && item.target_date ? (
                  <Link
                    className={`module-timeline-bar module-status-${item.status}`}
                    title={`${item.name}: ${item.start_date} — ${item.target_date}`}
                    to={`${route}/${item.id}`}
                    style={{
                      left:
                        ((new Date(item.start_date).getTime() - start) / day) *
                        px,
                      width: Math.max(
                        5,
                        ((new Date(item.target_date).getTime() -
                          new Date(item.start_date).getTime()) /
                          day +
                          1) *
                          px,
                      ),
                    }}
                  >
                    {item.name}
                  </Link>
                ) : (
                  <span className="module-unscheduled">
                    {t("未排期", "Unscheduled")}
                  </span>
                )}
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
});

const PlanningForm = observer(function PlanningForm({
  open,
  onOpenChange,
  kind,
  base,
  item,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (value: boolean) => void;
  kind: "cycles" | "modules";
  base: string;
  item: PlanningItem | null;
  onSaved: () => void;
}) {
  const { workspace, project } = useScope();
  const [form, setForm] = useState({
    name: "",
    description: "",
    start_date: "",
    end_date: "",
    status: "planned",
    owner_id: appStore.user!.id,
    lead_id: "",
    member_ids: [] as string[],
  });
  const [dateConflicts, setDateConflicts] = useState<
    { id: string; name: string }[]
  >([]);
  const [allowOverlap, setAllowOverlap] = useState(false);
  const members = appStore.members.get(project!.id) ?? [];
  const mutation = useMutation();
  const t = appStore.t;
  useEffect(() => {
    if (open) {
      setForm({
        name: item?.name ?? "",
        description: item?.description ?? "",
        start_date: item?.start_date ?? "",
        end_date:
          (kind === "cycles" ? item?.end_date : item?.target_date) ?? "",
        status: item?.status ?? "planned",
        owner_id: item?.owner_id ?? appStore.user!.id,
        lead_id: item?.lead_id ?? "",
        member_ids: item?.member_ids ?? [],
      });
      appStore.loadProjectResources(workspace.id, project!.id).catch(() => {});
      mutation.setError("");
    }
  }, [open, item, kind]);
  useEffect(() => {
    setDateConflicts([]);
    setAllowOverlap(false);
  }, [form.start_date, form.end_date, open]);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const data = {
      name: form.name,
      description: form.description,
      start_date: form.start_date || null,
      [kind === "cycles" ? "end_date" : "target_date"]: form.end_date || null,
      ...(kind === "modules"
        ? {
            status: form.status,
            lead_id: form.lead_id || null,
            member_ids: form.member_ids,
          }
        : { owner_id: form.owner_id }),
    };
    await mutation.execute(async () => {
      if (
        kind === "cycles" &&
        form.start_date &&
        form.end_date &&
        !allowOverlap
      ) {
        const result = await api.post<{
          available: boolean;
          conflicts: { id: string; name: string }[];
        }>(`${base}/check-dates`, {
          start_date: form.start_date,
          end_date: form.end_date,
          exclude_cycle_id: item?.id,
        });
        if (!result.data.available) {
          setDateConflicts(result.data.conflicts);
          return;
        }
      }
      if (item) await api.patch(`${base}/${item.id}`, data);
      else await api.post(base, data);
      onOpenChange(false);
      onSaved();
      appStore.notify(t("规划已保存", "Planning item saved"));
    });
  };
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={
        item
          ? t("编辑规划", "Edit planning item")
          : kind === "cycles"
            ? t("新建迭代周期", "Create cycle")
            : t("新建功能模块", "Create module")
      }
    >
      <form onSubmit={submit} className="form-stack modal-body">
        <Field label={t("名称", "Name")}>
          <Input
            name="name"
            value={form.name}
            onChange={(event) => setForm({ ...form, name: event.target.value })}
            required
            autoFocus
            maxLength={200}
          />
        </Field>
        <Field label={t("说明", "Description")}>
          <textarea
            name="description"
            className="input textarea"
            value={form.description}
            onChange={(event) =>
              setForm({ ...form, description: event.target.value })
            }
            rows={3}
          />
        </Field>
        <div className="form-row">
          <Field label={t("开始日期", "Start date")}>
            <Input
              type="date"
              value={form.start_date}
              required={kind === "cycles" && !!form.end_date}
              onChange={(event) =>
                setForm({ ...form, start_date: event.target.value })
              }
            />
          </Field>
          <Field label={t("结束日期", "End date")}>
            <Input
              type="date"
              min={form.start_date || undefined}
              value={form.end_date}
              required={kind === "cycles" && !!form.start_date}
              onChange={(event) =>
                setForm({ ...form, end_date: event.target.value })
              }
            />
          </Field>
        </div>
        {dateConflicts.length > 0 && (
          <div className="settings-panel-body">
            <p className="settings-note">
              {t("日期与以下迭代重叠：", "Dates overlap these cycles:")}{" "}
              {dateConflicts.map((cycle) => cycle.name).join("、")}
            </p>
            <label className="checkbox-label">
              <input
                type="checkbox"
                checked={allowOverlap}
                onChange={(event) => setAllowOverlap(event.target.checked)}
              />
              {t("仍使用此日期安排", "Keep these dates")}
            </label>
          </div>
        )}
        <Field
          label={
            kind === "cycles"
              ? t("周期负责人", "Cycle owner")
              : t("模块负责人", "Module lead")
          }
        >
          <Select
            value={kind === "cycles" ? form.owner_id : form.lead_id}
            onChange={(event) =>
              setForm({
                ...form,
                [kind === "cycles" ? "owner_id" : "lead_id"]:
                  event.target.value,
              })
            }
          >
            {kind === "modules" && (
              <option value="">{t("未指定", "Unassigned")}</option>
            )}
            {members.map((member) => (
              <option key={member.user_id} value={member.user_id}>
                {memberName(member)}
              </option>
            ))}
          </Select>
        </Field>
        {kind === "modules" && (
          <Field label={t("模块成员", "Module members")}>
            <MultiSelect
              value={form.member_ids}
              onChange={(member_ids) => setForm({ ...form, member_ids })}
              options={members.map((member) => ({
                value: member.user_id,
                label: memberName(member),
              }))}
              placeholder={t("选择参与成员", "Choose members")}
            />
          </Field>
        )}
        {kind === "modules" && (
          <Field label={t("模块状态", "Module status")}>
            <Select
              value={form.status}
              onChange={(event) =>
                setForm({ ...form, status: event.target.value })
              }
            >
              {Object.entries(moduleStatusLabels).map(([key, labels]) => (
                <option key={key} value={key}>
                  {t(...labels)}
                </option>
              ))}
            </Select>
          </Field>
        )}
        <ErrorBox message={mutation.error} />
        <div className="modal-footer">
          <Button type="button" onClick={() => onOpenChange(false)}>
            {t("取消", "Cancel")}
          </Button>
          <Button type="submit" variant="primary" busy={mutation.busy}>
            {item ? t("保存更改", "Save changes") : t("创建", "Create")}
          </Button>
        </div>
      </form>
    </Modal>
  );
});

export const ViewsPage = observer(function ViewsPage() {
  const { workspace, project } = useScope();
  const { viewId } = useParams();
  const navigate = useNavigate();
  const collection = `${project ? projectPath(workspace.id, project.id) : workspacePath(workspace.id)}/views`;
  const route = `/w/${workspace.slug}${project ? `/projects/${project.id}` : ""}/views`;
  const views = useRemote<SavedView[]>(collection);
  const selected = useRemote<SavedView>(
    viewId ? `${collection}/${viewId}` : null,
  );
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<SavedView | null>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [layout, setLayout] = useState("list");
  const [groupBy, setGroupBy] = useState(project ? "state_id" : "project_id");
  const [subGroupBy, setSubGroupBy] = useState("");
  const [order, setOrder] = useState("position");
  const [priority, setPriority] = useState("");
  const [filter, setFilter] = useState<FilterExpression | null>(null);
  const [privateView, setPrivateView] = useState(false);
  const [deleting, setDeleting] = useState<SavedView | null>(null);
  const mutation = useMutation();
  const t = appStore.t;

  const openForm = (view?: SavedView) => {
    setEditing(view ?? null);
    setName(view?.name ?? "");
    setDescription(view?.description ?? "");
    setLayout(view?.layout ?? "list");
    const savedGroup = String(
      view?.display.group_by ?? (project ? "state_id" : "project_id"),
    );
    setGroupBy(savedGroup === "state" ? "state_id" : savedGroup);
    setSubGroupBy(String(view?.display.sub_group_by ?? ""));
    setOrder(String(view?.display.order_by ?? "position"));
    setPriority(String(view?.filters.priority ?? ""));
    const savedFilter = view?.filters.filter;
    try {
      setFilter(
        typeof savedFilter === "string"
          ? JSON.parse(savedFilter)
          : ((savedFilter as FilterExpression) ?? null),
      );
    } catch {
      setFilter(null);
    }
    setPrivateView(view?.is_private ?? false);
    setFormOpen(true);
  };
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const body = {
      name,
      description,
      layout,
      filters: {
        ...editing?.filters,
        priority: priority || undefined,
        filter: filter || undefined,
      },
      display: {
        ...editing?.display,
        group_by: groupBy,
        sub_group_by: subGroupBy,
        order_by: order,
      },
      is_private: privateView,
    };
    await mutation.execute(
      async () => {
        if (editing) await api.patch(`${collection}/${editing.id}`, body);
        else await api.post(collection, body);
        setFormOpen(false);
        views.refresh();
        selected.refresh();
      },
      t("视图已保存", "View saved"),
    );
  };
  const query = selected.data ? filtersToQuery(selected.data.filters) : "";

  return (
    <div className={viewId && project ? "planning-detail" : "page-scroll"}>
      <PageHeader
        title={
          viewId
            ? (selected.data?.name ?? t("视图", "View"))
            : t("视图", "Views")
        }
        description={
          viewId
            ? selected.data?.description
            : t(
                "保存一组筛选条件，为团队保留一个观察工作的角度。",
                "Save a set of filters and give your team a useful perspective on work.",
              )
        }
        actions={
          viewId ? (
            <>
              <FavoriteButton entityType="view" entityId={viewId} />
              <Button size="sm" variant="ghost" onClick={() => navigate(route)}>
                <ArrowLeft size={14} />
                {t("返回", "Back")}
              </Button>
              <Button
                size="sm"
                onClick={() => selected.data && openForm(selected.data)}
              >
                {t("编辑视图", "Edit view")}
              </Button>
            </>
          ) : (
            <Button variant="primary" onClick={() => openForm()}>
              <Plus size={15} />
              {t("创建视图", "Create view")}
            </Button>
          )
        }
      />
      <ErrorBox message={views.error || selected.error || mutation.error} />
      {viewId ? (
        selected.loading ? (
          <Loading />
        ) : (
          selected.data &&
          (project ? (
            <IssuesPage
              key={`${selected.data.id}:${selected.data.updated_at}`}
              embedded
              extraQuery={query}
              defaultGroupBy={String(
                selected.data.display.group_by ?? "state_id",
              ).replace(/^state$/, "state_id")}
              defaultSubGroupBy={String(
                selected.data.display.sub_group_by ?? "",
              )}
              defaultOrder={String(
                selected.data.display.order_by ?? "position",
              )}
              defaultLayout={
                viewLayouts.find((item) => item.value === selected.data!.layout)
                  ?.ui ?? "list"
              }
            />
          ) : (
            <WorkspaceViewResults
              key={`${selected.data.id}:${selected.data.updated_at}`}
              query={query}
              layout={
                viewLayouts.find((item) => item.value === selected.data!.layout)
                  ?.ui ?? "list"
              }
              groupBy={String(
                selected.data.display.group_by ?? "project_id",
              ).replace(/^state$/, "state_id")}
              subGroupBy={String(selected.data.display.sub_group_by ?? "")}
              order={String(selected.data.display.order_by ?? "position")}
            />
          ))
        )
      ) : views.loading ? (
        <Loading />
      ) : (
        <div className="page-body">
          {!views.data?.length ? (
            <EmptyState
              icon={<LayoutGrid size={30} />}
              title={t(
                "为工作建立新的视角",
                "A fresh perspective on your work",
              )}
              description={t(
                "将重要的筛选与布局保存为视图，随时回到你关心的内容。",
                "Save important filters and layouts to return to what matters.",
              )}
              action={
                <Button variant="primary" onClick={() => openForm()}>
                  <Plus size={15} />
                  {t("创建视图", "Create view")}
                </Button>
              }
            />
          ) : (
            <div className="view-list">
              {views.data.map((view) => (
                <div key={view.id} className="view-row">
                  <span className="planning-icon">
                    <LayoutGrid size={18} />
                  </span>
                  <Link to={`${route}/${view.id}`}>
                    <strong>{view.name}</strong>
                    <span>
                      {view.description ||
                        t("自定义工作项视图", "Custom work item view")}
                    </span>
                  </Link>
                  {view.is_private && <LockKeyhole size={14} />}
                  <Badge>
                    {viewLayouts.find((item) => item.value === view.layout)?.[
                      appStore.locale === "zh-CN" ? "zh" : "en"
                    ] ?? view.layout}
                  </Badge>
                  <Menu
                    items={[
                      {
                        label: t("编辑", "Edit"),
                        onSelect: () => openForm(view),
                      },
                      {
                        label: t("复制视图", "Duplicate view"),
                        icon: <Copy size={14} />,
                        onSelect: () => {
                          mutation.execute(async () => {
                            await api.post(collection, {
                              name: `${view.name} ${t("副本", "copy")}`,
                              description: view.description,
                              layout: view.layout,
                              filters: view.filters,
                              display: view.display,
                              is_private: view.is_private,
                            });
                            views.refresh();
                          });
                        },
                      },
                      {
                        label: t("删除", "Delete"),
                        icon: <Trash2 size={14} />,
                        danger: true,
                        onSelect: () => setDeleting(view),
                      },
                    ]}
                  />
                </div>
              ))}
            </div>
          )}
        </div>
      )}
      <Modal
        open={formOpen}
        onOpenChange={setFormOpen}
        title={
          editing ? t("编辑视图", "Edit view") : t("创建视图", "Create view")
        }
      >
        <form className="form-stack modal-body" onSubmit={submit}>
          <Field label={t("视图名称", "View name")}>
            <Input
              value={name}
              onChange={(event) => setName(event.target.value)}
              required
              autoFocus
            />
          </Field>
          <Field label={t("说明", "Description")}>
            <Input
              value={description}
              onChange={(event) => setDescription(event.target.value)}
            />
          </Field>
          <div className="form-row">
            <Field label={t("布局", "Layout")}>
              <Select
                value={layout}
                onChange={(event) => setLayout(event.target.value)}
              >
                {viewLayouts.map((item) => (
                  <option value={item.value} key={item.value}>
                    {t(item.zh, item.en)}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label={t("优先级筛选", "Priority filter")}>
              <Select
                value={priority}
                onChange={(event) => setPriority(event.target.value)}
              >
                <option value="">{t("全部", "All")}</option>
                <option value="urgent">{t("紧急", "Urgent")}</option>
                <option value="high">{t("高", "High")}</option>
                <option value="medium">{t("中", "Medium")}</option>
                <option value="low">{t("低", "Low")}</option>
                <option value="none">{t("无优先级", "No priority")}</option>
              </Select>
            </Field>
          </div>
          <AdvancedFilterBuilder value={filter} onChange={setFilter} />
          {["list", "kanban"].includes(layout) && (
            <div className="form-row">
              <Field label={t("分组", "Group by")}>
                <Select
                  value={groupBy}
                  onChange={(event) => {
                    setGroupBy(event.target.value);
                    if (event.target.value === subGroupBy) setSubGroupBy("");
                  }}
                >
                  {groupChoices.map((choice) => (
                    <option key={choice.value} value={choice.value}>
                      {t(choice.zh, choice.en)}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field label={t("子分组", "Subgroup")}>
                <Select
                  value={subGroupBy}
                  onChange={(event) => setSubGroupBy(event.target.value)}
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
              </Field>
            </div>
          )}
          <Field label={t("工作项排序", "Work item order")}>
            <Select
              value={order}
              onChange={(event) => setOrder(event.target.value)}
            >
              <option value="position">{t("手动排序", "Manual order")}</option>
              <option value="priority">{t("优先级", "Priority")}</option>
              <option value="target_date">{t("目标日期", "Due date")}</option>
              <option value="-created_at">
                {t("最近创建", "Recently created")}
              </option>
              <option value="-updated_at">
                {t("最近更新", "Recently updated")}
              </option>
              <option value="name">{t("名称", "Name")}</option>
            </Select>
          </Field>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={privateView}
              onChange={(event) => setPrivateView(event.target.checked)}
            />
            {t("仅自己可见", "Private to me")}
          </label>
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button type="submit" variant="primary" busy={mutation.busy}>
              {t("保存视图", "Save view")}
            </Button>
          </div>
        </form>
      </Modal>
      <Confirm
        open={!!deleting}
        onOpenChange={(open) => {
          if (!open) setDeleting(null);
        }}
        title={t("删除视图？", "Delete view?")}
        description={t(
          "视图会被移除，工作项不受影响。",
          "The view will be removed. Its work items stay in place.",
        )}
        onConfirm={() => {
          mutation.execute(async () => {
            await api.delete(`${collection}/${deleting!.id}`);
            setDeleting(null);
            views.refresh();
          });
        }}
        busy={mutation.busy}
      />
    </div>
  );
});

const WorkspaceViewResults = observer(function WorkspaceViewResults({
  query,
  layout,
  groupBy,
  subGroupBy,
  order,
}: {
  query: string;
  layout: Layout;
  groupBy: string;
  subGroupBy: string;
  order: string;
}) {
  const { workspace } = useScope();
  const [cursor, setCursor] = useState("");
  const [items, setItems] = useState<WorkItem[]>([]);
  const [total, setTotal] = useState(0);
  const [error, setError] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const grouped = layout === "board" || layout === "list";
  const endpoint = `${workspacePath(workspace.id)}/issues`;
  const params = new URLSearchParams(query);
  params.set("order_by", order);
  params.set("limit", "100");
  if (cursor) params.set("cursor", cursor);
  const issues = useRemote<WorkItem[]>(
    grouped ? null : `${endpoint}?${params}`,
  );
  const t = appStore.t;
  useEffect(() => {
    setCursor("");
    setItems([]);
  }, [query, order, layout]);
  useEffect(() => {
    if (!issues.data) return;
    appStore.cacheIssues(issues.data);
    setItems((previous) => [
      ...new Map(
        [...(cursor ? previous : []), ...issues.data!].map((item) => [
          item.id,
          item,
        ]),
      ).values(),
    ]);
    setTotal(issues.pagination?.total ?? issues.data.length);
    const projectIds = [...new Set(issues.data.map((item) => item.project_id))];
    Promise.all(
      projectIds.map((id) => appStore.loadProjectResources(workspace.id, id)),
    ).catch((cause) => setError(errorMessage(cause)));
  }, [issues.data, issues.pagination, workspace.id, cursor]);
  const hrefFor = (item: WorkItem) =>
    `/w/${workspace.slug}/projects/${item.project_id}/issues/${item.id}`;
  const onUpdate = async (item: WorkItem, patch: Partial<WorkItem>) => {
    setError("");
    try {
      await appStore.updateIssue(item.id, patch);
    } catch (cause) {
      setError(errorMessage(cause));
    }
  };
  const currentItems = items.map(
    (item) => appStore.issues.get(item.id) ?? item,
  );
  return (
    <div className="page-body">
      <div className="settings-toolbar">
        <Badge>
          {total} {t("工作项", "work items")}
        </Badge>
      </div>
      <ErrorBox message={issues.error || error} />
      {grouped ? (
        <GroupedIssueResults
          endpoint={endpoint}
          query={params.toString()}
          groupBy={groupBy}
          subGroupBy={subGroupBy}
          layout={layout}
          revision={0}
          base=""
          hrefFor={hrefFor}
          onTotal={setTotal}
          selected={selected}
          onSelect={(id, checked) =>
            setSelected((previous) =>
              checked
                ? [...previous, id]
                : previous.filter((value) => value !== id),
            )
          }
        />
      ) : issues.loading && !items.length ? (
        <Loading />
      ) : !currentItems.length ? (
        <EmptyState
          icon={<CircleDashed size={30} />}
          title={appStore.t("没有符合条件的工作项", "No matching work items")}
          description={appStore.t(
            "修改视图筛选条件，或为项目添加工作项。",
            "Adjust the view filters or add work items to a project.",
          )}
        />
      ) : (
        <>
          {layout === "calendar" && (
            <IssueCalendar
              issues={currentItems}
              base=""
              hrefFor={hrefFor}
              onUpdate={onUpdate}
            />
          )}
          {layout === "timeline" && (
            <IssueTimeline
              issues={currentItems}
              base=""
              hrefFor={hrefFor}
              onUpdate={onUpdate}
            />
          )}
          {layout === "table" && (
            <IssueTable
              issues={currentItems}
              base=""
              hrefFor={hrefFor}
              onUpdate={onUpdate}
            />
          )}
          {issues.pagination?.has_more && (
            <div className="pagination-footer">
              <span>
                {items.length} / {total}
              </span>
              <Button
                busy={issues.loading}
                onClick={() => setCursor(issues.pagination?.next_cursor ?? "")}
              >
                {t("加载更多", "Load more")}
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  );
});
