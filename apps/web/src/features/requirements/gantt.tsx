import { useRef, useState, type PointerEvent } from "react";
import { observer } from "mobx-react-lite";
import {
  ChevronDown,
  ChevronRight,
  Link2,
  LocateFixed,
  MoveHorizontal,
  Plus,
  X,
} from "lucide-react";
import { appStore } from "../../stores/app-store";
import {
  Button,
  ErrorBox,
  Field,
  Input,
  Modal,
  Select,
} from "../../components/ui";
import type { WorkItem } from "../../types";
import type { PlanningCommand } from "./types";
import {
  dateFromDay,
  dayNumber,
  derivedRange,
  descendantIDs,
  shiftDate,
} from "./semantics";
import { requirementsStore as store } from "./store";

export const RequirementsGantt = observer(function RequirementsGantt({
  openItem,
}: {
  openItem: (id: string) => void;
}) {
  const t = appStore.t;
  const canEdit = store.canEdit;
  const [zoom, setZoom] = useState(24);
  const [collapsed, setCollapsed] = useState(new Set<string>());
  const [shiftOpen, setShiftOpen] = useState(false);
  const [dependencyOpen, setDependencyOpen] = useState(false);
  const scroll = useRef<HTMLDivElement>(null);
  const today = dayNumber(new Date().toISOString().slice(0, 10));
  const items = store.snapshot.items;
  const visible = new Set(store.visibleItems.map((item) => item.id));
  const contextual = new Set(visible);
  for (const item of store.visibleItems) {
    let parent = item.parent_id;
    const visited = new Set<string>();
    while (parent && !visited.has(parent)) {
      visited.add(parent);
      if (!store.items.get(parent)?.archived_at) contextual.add(parent);
      parent = store.items.get(parent)?.parent_id ?? null;
    }
  }
  const relevant = items.filter((item) => contextual.has(item.id));
  const rows: { item: WorkItem; depth: number }[] = [];
  const walk = (parent: string | null, depth: number, visited: Set<string>) => {
    for (const item of relevant
      .filter((entry) =>
        parent
          ? entry.parent_id === parent
          : !entry.parent_id || !contextual.has(entry.parent_id),
      )
      .sort(
        (a, b) =>
          (a.map_position ?? a.position) - (b.map_position ?? b.position),
      )) {
      if (visited.has(item.id)) continue;
      visited.add(item.id);
      rows.push({ item, depth });
      if (!collapsed.has(item.id)) walk(item.id, depth + 1, visited);
    }
  };
  walk(null, 0, new Set());
  const finiteDates = items
    .flatMap((item) => [item.start_date, item.target_date])
    .filter((date): date is string => !!date)
    .map(dayNumber)
    .filter(Number.isFinite);
  const rangeStart = Math.min(today - 7, ...finiteDates) - 2;
  const rangeEnd = Math.max(today + 35, ...finiteDates) + 3;
  const totalDays = Math.min(1096, rangeEnd - rangeStart + 1);
  const width = totalDays * zoom;
  const dateLabelInterval = zoom >= 32 ? 1 : zoom >= 20 ? 2 : 7;
  const labelWidth = 350;
  const rowHeight = 68;
  const locate = (date: string | null | undefined, id?: string) => {
    if (!date || !scroll.current) return;
    scroll.current.scrollTo({
      left: Math.max(0, (dayNumber(date) - rangeStart) * zoom - 100),
      top: id
        ? Math.max(
            0,
            (scroll.current.querySelector<HTMLElement>(
              `[data-testid="gantt-row-${id}"]`,
            )?.offsetTop ?? 0) - 120,
          )
        : scroll.current.scrollTop,
      behavior: "smooth",
    });
  };
  const toggle = (id: string) =>
    setCollapsed((old) => {
      const next = new Set(old);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  const unscheduled = store.visibleItems.filter(
    (item) => item.requirement_type === "epic" ? !derivedRange(item, items) : !item.start_date || !item.target_date,
  );
  return (
    <section
      className="req-gantt"
      aria-label={t("需求甘特图", "Requirements Gantt")}
    >
      <div className="req-view-heading">
        <div>
          <h2>
            {t(
              "承诺与执行在同一时间轴",
              "Commitments and execution on one timeline",
            )}
          </h2>
          <p>
            {t(
              "实线为已录入日期；虚线为子任务派生范围。拖动或使用日期输入调整任务。",
              "Solid bars are recorded dates; dashed bars summarize children. Drag a task or use its date inputs.",
            )}
          </p>
        </div>
        <div className="req-actions">
          <Select
            aria-label={t("时间缩放", "Timeline zoom")}
            value={zoom}
            onChange={(event) => setZoom(Number(event.target.value))}
          >
            <option value={36}>{t("周", "Week")}</option>
            <option value={24}>{t("月", "Month")}</option>
            <option value={10}>{t("季度", "Quarter")}</option>
          </Select>
          <Button size="sm" onClick={() => locate(dateFromDay(today))}>
            <LocateFixed size={14} />
            {t("今天", "Today")}
          </Button>
          <Button
            size="sm"
            disabled={!store.selected}
            onClick={() => {
              if (store.selected) {
                const selected = store.selected;
                if (!visible.has(store.selectedID)) store.clearFilters();
                setCollapsed(new Set());
                requestAnimationFrame(() =>
                  locate(
                    selected.start_date ?? derivedRange(selected, items)?.start,
                    selected.id,
                  ),
                );
              }
            }}
          >
            {t("定位选中", "Locate selected")}
          </Button>
          {canEdit && (
            <>
              <Button
                size="sm"
                disabled={!store.selectedIDs.size}
                onClick={() => setShiftOpen(true)}
              >
                <MoveHorizontal size={14} />
                {t("批量平移", "Shift selected")} ({store.selectedIDs.size})
              </Button>
              <Button size="sm" onClick={() => setDependencyOpen(true)}>
                <Link2 size={14} />
                {t("依赖", "Dependencies")}
              </Button>
            </>
          )}
        </div>
      </div>
      <div className="req-gantt-scroll" ref={scroll}>
        <div
          className="req-gantt-canvas"
          style={{
            width: labelWidth + width,
            minHeight: 70 + rows.length * rowHeight,
          }}
        >
          <div
            className="req-gantt-header"
            style={{ height: 58, width: labelWidth + width }}
          >
            <div
              className="req-gantt-label req-gantt-column"
              style={{ width: labelWidth }}
            >
              {t("层级 / 日期", "Hierarchy / dates")}
            </div>
            <div
              className="req-gantt-dates"
              style={{ left: labelWidth, width }}
            >
              {Array.from({ length: totalDays }, (_, index) => index)
                .filter((index) => index % dateLabelInterval === 0)
                .map((index) => {
                  const date = dateFromDay(rangeStart + index);
                  return (
                    <span
                      key={index}
                      aria-label={date}
                      title={date}
                      style={{
                        left: index * zoom,
                        width: zoom * dateLabelInterval,
                      }}
                    >
                      {date.slice(5)}
                    </span>
                  );
                })}
              {store.snapshot.cycles
                .filter((cycle) => cycle.start_date && cycle.end_date)
                .map((cycle) => (
                  <span
                    className="req-sprint-window"
                    key={cycle.id}
                    title={`${cycle.name}: ${cycle.start_date} → ${cycle.end_date}`}
                    style={{
                      left: (dayNumber(cycle.start_date!) - rangeStart) * zoom,
                      width:
                        (dayNumber(cycle.end_date!) -
                          dayNumber(cycle.start_date!) +
                          1) *
                        zoom,
                    }}
                  >
                    {cycle.name}
                  </span>
                ))}
            </div>
          </div>
          <div
            className="req-today-line"
            style={{
              left: labelWidth + (today - rangeStart) * zoom,
              height: rows.length * rowHeight + 58,
            }}
            aria-hidden="true"
          />
          {rows.map(({ item, depth }) => {
            const children = items.filter(
              (child) => child.parent_id === item.id,
            );
            const derived = derivedRange(item, items);
            const state = store.states.find((state) => state.id === item.state_id);
            const completed = state?.group === "completed";
            const descendantSet = new Set(descendantIDs(item.id, items));
            const executable = items.filter((entry) => descendantSet.has(entry.id) && !entry.archived_at && !entry.is_draft && !items.some((child) => child.parent_id === entry.id && !child.archived_at) && store.states.find((candidate) => candidate.id === entry.state_id)?.group !== "cancelled");
            const completedCount = executable.filter((entry) => store.states.find((candidate) => candidate.id === entry.state_id)?.group === "completed").length;
            return (
              <div
                key={item.id}
                className={`req-gantt-row ${store.selectedID === item.id ? "is-selected" : ""} ${!visible.has(item.id) ? "is-context" : ""}`}
                style={{ height: rowHeight, width: labelWidth + width }}
                data-testid={`gantt-row-${item.id}`}
              >
                <div className="req-gantt-label" style={{ width: labelWidth }}>
                  <div
                    className="req-gantt-item"
                    style={{ paddingLeft: depth * 15 }}
                  >
                    {canEdit && (
                      <input
                        type="checkbox"
                        aria-label={`${t("选择", "Select")} ${item.name}`}
                        checked={store.selectedIDs.has(item.id)}
                        onChange={() => store.toggleSelected(item.id)}
                      />
                    )}
                    {children.length > 0 ? (
                      <button
                        aria-label={
                          collapsed.has(item.id)
                            ? t("展开子项", "Expand children")
                            : t("折叠子项", "Collapse children")
                        }
                        onClick={() => toggle(item.id)}
                      >
                        {collapsed.has(item.id) ? (
                          <ChevronRight size={14} />
                        ) : (
                          <ChevronDown size={14} />
                        )}
                      </button>
                    ) : (
                      <span className="req-tree-spacer" />
                    )}
                    <span
                      className={`req-kind req-kind-${item.requirement_type ?? "legacy"}`}
                    >
                      {item.requirement_type?.slice(0, 1).toUpperCase() ?? "·"}
                    </span>
                    <button
                      className="req-gantt-name"
                      onClick={() => openItem(item.id)}
                      title={item.name}
                    >
                      {item.name}
                    </button>
                    <span className="req-gantt-status" style={{ color: state?.color }} title={executable.length ? t(`已完成 ${completedCount}/${executable.length} 个执行任务；当前状态 ${state?.name ?? ""}`, `${completedCount}/${executable.length} executable tasks completed; status ${state?.name ?? ""}`) : state?.name}>{executable.length ? `${completedCount}/${executable.length}` : state?.name}</span>
                  </div>
                  <div className="req-date-inputs">
                    <input
                      type="date"
                      aria-label={`${item.name} ${t("开始日期", "start date")}`}
                      value={item.start_date?.slice(0, 10) ?? ""}
                      disabled={
                        !canEdit ||
                        item.requirement_type === "epic" ||
                        store.busy
                      }
                      onChange={(event) =>
                        void store.update(item, {
                          start_date: event.target.value || null,
                        })
                      }
                    />
                    <span>→</span>
                    <input
                      type="date"
                      aria-label={`${item.name} ${t("结束日期", "end date")}`}
                      value={item.target_date?.slice(0, 10) ?? ""}
                      disabled={
                        !canEdit ||
                        item.requirement_type === "epic" ||
                        store.busy
                      }
                      onChange={(event) =>
                        void store.update(item, {
                          target_date: event.target.value || null,
                        })
                      }
                    />
                  </div>
                </div>
                <div
                  className="req-gantt-track"
                  style={{
                    left: labelWidth,
                    width,
                    backgroundSize: `${zoom * 7}px 100%`,
                  }}
                >
                  {item.start_date &&
                    item.target_date &&
                    item.requirement_type !== "epic" && (
                      <DateBar
                        item={item}
                        start={item.start_date}
                        end={item.target_date}
                        rangeStart={rangeStart}
                        zoom={zoom}
                        editable={canEdit && !store.busy}
                        completed={completed}
                        openItem={openItem}
                      />
                    )}
                  {derived && (
                    <div
                      className="req-derived-bar"
                      title={`${t("子任务汇总", "Child task summary")}: ${derived.start} → ${derived.end}`}
                      style={{
                        left: (dayNumber(derived.start) - rangeStart) * zoom,
                        width: Math.max(
                          4,
                          (dayNumber(derived.end) -
                            dayNumber(derived.start) +
                            1) *
                            zoom,
                        ),
                        top: item.requirement_type === "epic" ? 20 : 43,
                      }}
                    >
                      <span>
                        {item.requirement_type === "epic"
                          ? item.name
                          : t("子任务", "Children")}
                      </span>
                    </div>
                  )}
                  {!item.start_date && !derived && (
                    <button
                      className="req-unscheduled-link"
                      onClick={() => openItem(item.id)}
                    >
                      {t("未排期 · 录入日期", "Unscheduled · set dates")}
                    </button>
                  )}
                </div>
              </div>
            );
          })}
          <svg
            className="req-dependency-lines"
            style={{ left: labelWidth, top: 58 }}
            width={width}
            height={rows.length * rowHeight}
            aria-label={t("阻塞依赖线", "Blocking dependency lines")}
          >
            <defs>
              <marker
                id="req-dependency-arrow"
                markerWidth="6"
                markerHeight="6"
                refX="5"
                refY="3"
                orient="auto"
              >
                <path d="M0 0 L6 3 L0 6" fill="currentColor" />
              </marker>
            </defs>
            {store.snapshot.dependencies.map((dependency) => {
              const source = rows.findIndex(
                  (row) => row.item.id === dependency.source_id,
                ),
                target = rows.findIndex(
                  (row) => row.item.id === dependency.target_id,
                );
              if (
                source < 0 ||
                target < 0 ||
                !rows[source].item.target_date ||
                !rows[target].item.start_date
              )
                return null;
              const x1 =
                  (dayNumber(rows[source].item.target_date!) - rangeStart + 1) *
                  zoom,
                x2 =
                  (dayNumber(rows[target].item.start_date!) - rangeStart) *
                  zoom,
                y1 = source * rowHeight + 27,
                y2 = target * rowHeight + 27;
              const conflict = x2 < x1;
              return (
                <path
                  key={dependency.id}
                  d={`M${x1} ${y1} H${Math.max(x1 + 12, x2 - 12)} V${y2} H${x2}`}
                  className={conflict ? "is-conflict" : ""}
                  fill="none"
                  markerEnd="url(#req-dependency-arrow)"
                >
                  <title>{`${rows[source].item.name} → ${rows[target].item.name}${conflict ? t("：日期冲突", ": date conflict") : ""}`}</title>
                </path>
              );
            })}
          </svg>
        </div>
      </div>
      <div className="req-unscheduled-set">
        <strong>
          {t("未排期集合", "Unscheduled")} ({unscheduled.length})
        </strong>
        {unscheduled.map((item) => (
          <Button
            key={item.id}
            size="sm"
            variant="ghost"
            onClick={() => openItem(item.id)}
          >
            {item.name}
          </Button>
        ))}
      </div>
      {shiftOpen && <BatchShift close={() => setShiftOpen(false)} />}
      {dependencyOpen && (
        <DependenciesEditor
          close={() => setDependencyOpen(false)}
          openItem={openItem}
        />
      )}
    </section>
  );
});

function DateBar({
  item,
  start,
  end,
  rangeStart,
  zoom,
  editable,
  completed,
  openItem,
}: {
  item: WorkItem;
  start: string;
  end: string;
  rangeStart: number;
  zoom: number;
  editable: boolean;
  completed: boolean;
  openItem: (id: string) => void;
}) {
  const drag = useRef<{ x: number; mode: "move" | "start" | "end" } | null>(
    null,
  );
  const [offset, setOffset] = useState(0);
  const t = appStore.t;
  const pointerDown = (
    event: PointerEvent<HTMLElement>,
    mode: "move" | "start" | "end",
  ) => {
    if (!editable || event.button !== 0) return;
    event.stopPropagation();
    drag.current = { x: event.clientX, mode };
    event.currentTarget.setPointerCapture(event.pointerId);
    store.select(item.id);
  };
  const pointerUp = (event: PointerEvent<HTMLElement>) => {
    if (!drag.current) return;
    const days = Math.round((event.clientX - drag.current.x) / zoom);
    const mode = drag.current.mode;
    drag.current = null;
    setOffset(0);
    if (!days) return;
    const newStart = mode === "end" ? start : shiftDate(start, days)!;
    const newEnd = mode === "start" ? end : shiftDate(end, days)!;
    if (newStart > newEnd) {
      store.setError(
        t("开始日期不能晚于结束日期", "Start date cannot follow end date"),
      );
      return;
    }
    void store.update(item, { start_date: newStart, target_date: newEnd });
  };
  return (
    <div
      className={`req-date-bar ${item.requirement_type === "story" ? "is-commitment" : ""} ${completed ? "is-completed" : ""}`}
      style={{
        left: (dayNumber(start) - rangeStart) * zoom + offset,
        width: Math.max(12, (dayNumber(end) - dayNumber(start) + 1) * zoom),
      }}
      onPointerDown={(event) => pointerDown(event, "move")}
      onPointerMove={(event) => {
        if (drag.current?.mode === "move")
          setOffset(Math.round((event.clientX - drag.current.x) / zoom) * zoom);
      }}
      onPointerUp={pointerUp}
      onPointerCancel={() => {
        drag.current = null;
        setOffset(0);
      }}
      title={`${item.name}: ${start.slice(0, 10)} → ${end.slice(0, 10)}`}
    >
      <button
        className="req-bar-handle"
        disabled={!editable}
        aria-label={`${t("调整开始日期", "Resize start")} ${item.name}`}
        onPointerDown={(event) => pointerDown(event, "start")}
        onPointerUp={pointerUp}
        onKeyDown={(event) => {
          if (["ArrowLeft", "ArrowRight"].includes(event.key)) {
            event.preventDefault();
            const date = shiftDate(start, event.key === "ArrowLeft" ? -1 : 1)!;
            if (date <= end) void store.update(item, { start_date: date });
          }
        }}
      />
      <button
        className="req-bar-content"
        onClick={() => store.select(item.id)}
        onDoubleClick={() => openItem(item.id)}
      >
        {item.name}
      </button>
      <button
        className="req-bar-handle"
        disabled={!editable}
        aria-label={`${t("调整结束日期", "Resize end")} ${item.name}`}
        onPointerDown={(event) => pointerDown(event, "end")}
        onPointerUp={pointerUp}
        onKeyDown={(event) => {
          if (["ArrowLeft", "ArrowRight"].includes(event.key)) {
            event.preventDefault();
            const date = shiftDate(end, event.key === "ArrowLeft" ? -1 : 1)!;
            if (date >= start) void store.update(item, { target_date: date });
          }
        }}
      />
    </div>
  );
}

const BatchShift = observer(function BatchShift({
  close,
}: {
  close: () => void;
}) {
  const t = appStore.t;
  const [days, setDays] = useState(1);
  const [includeChildren, setIncludeChildren] = useState(false);
  const ids = new Set(store.selectedIDs);
  if (includeChildren)
    for (const id of store.selectedIDs)
      for (const descendant of descendantIDs(id, store.snapshot.items))
        ids.add(descendant);
  const items = [...ids]
    .map((id) => store.items.get(id))
    .filter(
      (item): item is WorkItem =>
        !!item &&
        item.requirement_type !== "epic" &&
        (!!item.start_date || !!item.target_date),
    );
  return (
    <Modal
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
      title={t("批量平移日期", "Shift dates in one batch")}
    >
      <form
        className="req-form"
        onSubmit={async (event) => {
          event.preventDefault();
          const commands: PlanningCommand[] = items.map((item) => ({
            operation: "update",
            id: item.id,
            version: item.version,
            fields: {
              start_date: shiftDate(item.start_date, days),
              target_date: shiftDate(item.target_date, days),
            },
          }));
          if (await store.change(commands)) close();
        }}
      >
        <Field
          label={t(
            "平移天数（负数提前）",
            "Days to shift (negative moves earlier)",
          )}
        >
          <Input
            type="number"
            step="1"
            required
            value={days}
            onChange={(event) => setDays(Number(event.target.value))}
          />
        </Field>
        <label className="req-check">
          <input
            type="checkbox"
            checked={includeChildren}
            onChange={(event) => setIncludeChildren(event.target.checked)}
          />
          {t("同时选择后代任务", "Include descendant tasks")}
        </label>
        <p>
          {t(
            "以下日期将原子更新，周期归属不变；派生汇总自动计算。",
            "The dates below update atomically, preserving sprint membership. Summaries recalculate.",
          )}
        </p>
        <ul>
          {items.map((item) => (
            <li key={item.id}>
              {item.name}: {shiftDate(item.start_date, days) ?? "?"} →{" "}
              {shiftDate(item.target_date, days) ?? "?"}
            </li>
          ))}
        </ul>
        <ErrorBox message={store.error} />
        <Button
          type="submit"
          variant="primary"
          busy={store.busy}
          disabled={!items.length}
        >
          {t("提交日期变更", "Apply date changes")}
        </Button>
      </form>
    </Modal>
  );
});

const DependenciesEditor = observer(function DependenciesEditor({
  close,
  openItem,
}: {
  close: () => void;
  openItem: (id: string) => void;
}) {
  const t = appStore.t;
  const [source, setSource] = useState("");
  const [target, setTarget] = useState(store.selectedID);
  const update = async (targetID: string, sourceID: string, remove = false) => {
    const item = store.items.get(targetID);
    if (!item) return;
    const ids = store.snapshot.dependencies
      .filter((dependency) => dependency.target_id === targetID)
      .map((dependency) => dependency.source_id);
    await store.update(item, {
      dependency_ids: remove
        ? ids.filter((id) => id !== sourceID)
        : [...new Set([...ids, sourceID])],
    });
  };
  return (
    <Modal
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
      title={t("维护阻塞依赖", "Manage blocking dependencies")}
    >
      <div className="req-form">
        <p>
          {t(
            "前置任务阻塞后续任务；保存时检查依赖环。一般关联不会影响排期。",
            "A predecessor blocks a successor. Saving checks dependency cycles. General links do not constrain scheduling.",
          )}
        </p>
        <div className="req-form-grid">
          <Field label={t("前置任务", "Predecessor")}>
            <Select
              value={source}
              onChange={(event) => setSource(event.target.value)}
            >
              <option value="">{t("选择任务", "Choose a task")}</option>
              {store.snapshot.items
                .filter((item) => item.id !== target)
                .map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name}
                  </option>
                ))}
            </Select>
          </Field>
          <Field label={t("后续任务", "Successor")}>
            <Select
              value={target}
              onChange={(event) => setTarget(event.target.value)}
            >
              <option value="">{t("选择任务", "Choose a task")}</option>
              {store.snapshot.items
                .filter((item) => item.id !== source)
                .map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name}
                  </option>
                ))}
            </Select>
          </Field>
        </div>
        <Button
          disabled={!source || !target || store.busy}
          onClick={() => void update(target, source)}
        >
          <Plus size={14} />
          {t("添加依赖", "Add dependency")}
        </Button>
        <ErrorBox message={store.error} />
        {store.snapshot.dependencies.map((dependency) => (
          <div className="req-dependency-row" key={dependency.id}>
            <button
              onClick={() => {
                close();
                openItem(dependency.source_id);
              }}
            >
              {store.items.get(dependency.source_id)?.name}
            </button>
            <span>→</span>
            <button
              onClick={() => {
                close();
                openItem(dependency.target_id);
              }}
            >
              {store.items.get(dependency.target_id)?.name}
            </button>
            <Button
              size="icon"
              variant="ghost"
              aria-label={t("删除依赖", "Remove dependency")}
              disabled={store.busy}
              onClick={() =>
                void update(dependency.target_id, dependency.source_id, true)
              }
            >
              <X size={14} />
            </Button>
          </div>
        ))}
      </div>
    </Modal>
  );
});
