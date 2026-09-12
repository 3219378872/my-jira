import { useState } from "react";
import { observer } from "mobx-react-lite";
import { CalendarDays, Plus, Settings, X } from "lucide-react";
import { appStore } from "../../stores/app-store";
import { api } from "../../lib/api";
import { useScope } from "../../lib/hooks";
import {
  Button,
  ErrorBox,
  Field,
  Input,
  Modal,
  Select,
} from "../../components/ui";
import {
  dateFromDay,
  dayNumber,
  descendantIDs,
  hours,
  parseHours,
} from "./semantics";
import { requirementsStore as store } from "./store";
import type { LoadConflict, LoadDay, ResourceMember } from "./types";

export const MemberSchedule = observer(function MemberSchedule({
  openItem,
}: {
  openItem: (id: string) => void;
}) {
  const t = appStore.t;
  const { workspace } = useScope();
  const canEdit = store.canEdit;
  const canAdmin = store.canAdmin;
  const [editing, setEditing] = useState<ResourceMember | null>(null);
  const [weekMode, setWeekMode] = useState(false);
  const [detail, setDetail] = useState<{
    member: ResourceMember;
    day: LoadDay;
  } | null>(null);
  const [timezoneEdit, setTimezoneEdit] = useState(false);
  const [timezone, setTimezone] = useState(
    store.resources?.timezone ?? workspace.timezone ?? "UTC",
  );
  const start = store.load?.start_date ?? new Date().toISOString().slice(0, 10);
  const end = store.load?.end_date ?? dateFromDay(dayNumber(start) + 27);
  const members = (store.load?.members ?? []).filter(
    (member) =>
      (!store.filters.member || member.member_id === store.filters.member) &&
      (!store.filters.skill || member.skills.includes(store.filters.skill)),
  );
  const visible = new Set(store.visibleItems.map((item) => item.id));
  const selectedTasks = new Set(
    store.selectedID
      ? [
          store.selectedID,
          ...descendantIDs(store.selectedID, store.snapshot.items),
        ]
      : [],
  );
  const labelWidth = 235,
    dayWidth = weekMode ? 145 : 58;
  const periods = members[0]
    ? weekMode
      ? members[0].weeks.map((week) => week.start_date)
      : members[0].days.map((day) => day.date)
    : [];
  return (
    <section
      className="req-members"
      aria-label={t("成员排期与负载", "Member schedule and workload")}
    >
      <div className="req-view-heading">
        <div>
          <h2>
            {t(
              "让工作量与可用容量相匹配",
              "Match remaining work with available capacity",
            )}
          </h2>
          <p>
            {t(
              "负载来自完整授权任务集合，仅计算叶任务；多人分摊保留整数分钟总量。",
              "Loads cover all authorized tasks and count executable leaves once. Shared assignments conserve integer minutes.",
            )}
          </p>
        </div>
        <div className="req-actions">
          <Button
            size="sm"
            disabled={!store.selectedID}
            onClick={async () => {
              store.clearFilters();
              await store.reloadAuxiliary();
              requestAnimationFrame(() =>
                document
                  .querySelector<HTMLElement>(".req-member-task.is-selected")
                  ?.scrollIntoView({
                    block: "center",
                    inline: "nearest",
                    behavior: "smooth",
                  }),
              );
            }}
          >
            {t("定位选中及子任务", "Locate selection and children")}
          </Button>
          <Button size="sm" onClick={() => setWeekMode((old) => !old)}>
            {weekMode
              ? t("每日负载", "Daily load")
              : t("每周负载", "Weekly load")}
          </Button>
          {canAdmin && (
            <Button size="sm" onClick={() => setTimezoneEdit(true)}>
              <Settings size={14} />
              {t("项目时区", "Project timezone")}
            </Button>
          )}
        </div>
      </div>
      <div className="req-capacity-legend">
        <span>{store.load?.timezone ?? store.resources?.timezone}</span>
        <span className="req-load-good">{t("有容量", "Within capacity")}</span>
        <span className="req-load-over">{t("超负荷", "Over capacity")}</span>
        <span className="req-load-unknown">
          {t("未知 / 无法排期", "Unknown / unschedulable")}
        </span>
        <span>
          {t(
            "单元格：项目总占用 / 容量；筛选小计另列，超负荷不因筛选隐藏",
            "Cells show project total / capacity; filtered work is separate so overload remains visible",
          )}
        </span>
      </div>
      {!store.resources?.members.length && (
        <p className="req-empty-inline">
          {t(
            "项目尚无可排期成员。请先加入项目成员。",
            "This project has no schedulable members. Add project members first.",
          )}
        </p>
      )}
      <div className="req-member-scroll">
        <div
          className="req-member-canvas"
          style={{ width: labelWidth + Math.max(periods.length, 1) * dayWidth }}
        >
          <div className="req-member-header">
            <strong style={{ width: labelWidth }}>
              {t("成员 / 技能 / 任务", "Member / skills / tasks")}
            </strong>
            {periods.map((date) => (
              <span key={date} style={{ width: dayWidth }}>
                {date.slice(5)}
              </span>
            ))}
          </div>
          {members.map((member) => {
            const tasks = (store.load?.tasks ?? []).filter(
              (item) =>
                item.executable &&
                item.assignee_ids?.includes(member.member_id) &&
                visible.has(item.id),
            );
            const periodsData = weekMode
              ? member.weeks.map((week) => ({
                  ...week,
                  date: week.start_date,
                  task_ids: member.days
                    .filter(
                      (day) =>
                        day.date >= week.start_date &&
                        day.date < dateFromDay(dayNumber(week.start_date) + 7),
                    )
                    .flatMap((day) => day.task_ids),
                }))
              : member.days;
            return (
              <div
                key={member.member_id}
                className="req-member-lane"
                data-testid={`member-lane-${member.member_id}`}
                onDragOver={(event) => {
                  if (canEdit) event.preventDefault();
                }}
                onDrop={(event) => {
                  event.preventDefault();
                  const item = store.items.get(
                    event.dataTransfer.getData(
                      "application/x-myjira-requirement",
                    ),
                  );
                  if (canEdit && item)
                    void store.update(item, {
                      assignee_ids: [member.member_id],
                      allocation_weights: [
                        { member_id: member.member_id, weight: 1 },
                      ],
                    });
                }}
              >
                <div className="req-member-capacity-row">
                  <div
                    className="req-member-info"
                    style={{ width: labelWidth }}
                  >
                    <strong>{member.display_name}</strong>
                    <small>
                      {member.skills.length
                        ? member.skills.join(" · ")
                        : t("未录入技能", "No skills recorded")}
                    </small>
                    {canAdmin && (
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => setEditing(member)}
                      >
                        <CalendarDays size={13} />
                        {t("日历和容量", "Calendar and capacity")}
                      </Button>
                    )}
                  </div>
                  {periodsData.map((day) => (
                    <button
                      key={day.date}
                      className={`req-load-cell ${day.unknown || day.capacity_minutes === null ? "req-load-unknown" : day.over_capacity ? "req-load-over" : day.capacity_minutes === 0 ? "req-load-off" : "req-load-good"}`}
                      style={{ width: dayWidth }}
                      onClick={() => setDetail({ member, day })}
                      title={`${member.display_name} ${day.date}: ${day.allocated_minutes} / ${day.capacity_minutes ?? "?"} ${t("分钟", "minutes")}`}
                    >
                      <strong>{hours(day.allocated_minutes)}</strong>
                      <small>/ {hours(day.capacity_minutes)}h</small>
                      {Object.values(store.filters).some(Boolean) &&
                        day.selected_minutes !== undefined && (
                          <small>
                            {t("筛选", "Filtered")}{" "}
                            {day.selected_unknown
                              ? "?"
                              : hours(day.selected_minutes)}
                            h
                          </small>
                        )}
                      {day.unknown && (
                        <span aria-label={t("未知负载", "Unknown load")}>
                          ?
                        </span>
                      )}
                    </button>
                  ))}
                </div>
                {tasks.map((task) => {
                  const item = store.items.get(task.id);
                  if (!item) return null;
                  const left = task.start_date
                    ? (Math.max(
                        0,
                        dayNumber(task.start_date) - dayNumber(start),
                      ) /
                        (weekMode ? 7 : 1)) *
                      dayWidth
                    : 0;
                  const length =
                    task.start_date && task.target_date
                      ? (Math.max(
                          1,
                          Math.min(
                            dayNumber(end),
                            dayNumber(task.target_date),
                          ) -
                            Math.max(
                              dayNumber(start),
                              dayNumber(task.start_date),
                            ) +
                            1,
                        ) /
                          (weekMode ? 7 : 1)) *
                        dayWidth
                      : 0;
                  const inRange =
                    task.start_date &&
                    task.target_date &&
                    task.target_date >= start &&
                    task.start_date <= end;
                  return (
                    <div
                      key={task.id}
                      className={`req-member-task ${selectedTasks.has(task.id) ? "is-selected" : ""}`}
                    >
                      <button
                        className="req-member-task-name"
                        style={{ width: labelWidth }}
                        onClick={() => openItem(task.id)}
                      >
                        {task.name}
                        <small>
                          {hours(task.remaining_minutes)}h ·{" "}
                          {task.assignee_ids?.length ?? 0}{" "}
                          {t("人分摊", "assignees")}
                        </small>
                      </button>
                      <div className="req-member-task-track">
                        {inRange ? (
                          <button
                            draggable={canEdit}
                            onDragStart={(event) => {
                              event.dataTransfer.setData(
                                "application/x-myjira-requirement",
                                task.id,
                              );
                            }}
                            className="req-member-task-bar"
                            style={{ left, width: Math.max(20, length) }}
                            onClick={() => openItem(task.id)}
                          >
                            {task.name}
                          </button>
                        ) : (
                          <button
                            className="req-unscheduled-link"
                            onClick={() => openItem(task.id)}
                          >
                            {!task.start_date || !task.target_date
                              ? t("未排期", "Unscheduled")
                              : t("当前日期范围外", "Outside displayed dates")}
                          </button>
                        )}
                      </div>
                    </div>
                  );
                })}
                {!tasks.length && (
                  <p className="req-empty-inline">
                    {t(
                      "当前筛选没有执行任务",
                      "No executable tasks match the current filters",
                    )}
                  </p>
                )}
              </div>
            );
          })}
        </div>
      </div>
      <div className="req-resource-sets">
        {[
          [t("未分配", "Unassigned"), store.load?.unassigned],
          [t("未估算", "Unestimated"), store.load?.unestimated],
          [t("未排期", "Unscheduled"), store.load?.unscheduled],
        ].map(([label, ids]) => (
          <div key={label as string}>
            <strong>
              {label as string} ({(ids as string[] | undefined)?.length ?? 0})
            </strong>
            {(ids as string[] | undefined)?.map((id) => (
              <Button
                key={id}
                size="sm"
                variant="ghost"
                onClick={() => openItem(id)}
              >
                {store.items.get(id)?.name ?? id.slice(0, 8)}
              </Button>
            ))}
          </div>
        ))}
      </div>
      {editing && (
        <CalendarEditor member={editing} close={() => setEditing(null)} />
      )}
      {detail && (
        <Modal
          open
          onOpenChange={(open) => {
            if (!open) setDetail(null);
          }}
          title={`${detail.member.display_name} · ${detail.day.date}`}
        >
          <div className="req-form">
            <p>
              {t("已分配", "Allocated")}: {detail.day.allocated_minutes}{" "}
              {t("分钟", "min")} · {t("容量", "Capacity")}:{" "}
              {detail.day.capacity_minutes ?? "?"} {t("分钟", "min")}
            </p>
            {detail.day.unknown && (
              <p className="req-warning">
                {t(
                  "存在未知工作量或容量，不能按零负载处理。",
                  "Effort or capacity is unknown; this is not zero load.",
                )}
              </p>
            )}
            {[...new Set(detail.day.task_ids)].map((id) => (
              <Button
                key={id}
                onClick={() => {
                  setDetail(null);
                  openItem(id);
                }}
              >
                {store.items.get(id)?.name ?? id.slice(0, 8)}
                {detail.day.task_minutes?.[id] !== undefined && (
                  <span>
                    {" "}
                    · {detail.day.task_minutes[id]} {t("分钟", "min")}
                  </span>
                )}
                {detail.day.unknown_task_ids?.includes(id) && (
                  <span> · {t("未知工作量", "Unknown effort")}</span>
                )}
              </Button>
            ))}
          </div>
        </Modal>
      )}
      {timezoneEdit && (
        <Modal
          open
          onOpenChange={(open) => {
            if (!open) setTimezoneEdit(false);
          }}
          title={t("项目时区", "Project timezone")}
        >
          <form
            className="req-form"
            onSubmit={async (event) => {
              event.preventDefault();
              if (
                await store.mutate(() =>
                  api.patch(`${store.base}/resources`, { timezone }),
                )
              )
                setTimezoneEdit(false);
            }}
          >
            <Field
              label={t("IANA 时区", "IANA timezone")}
              hint="Asia/Shanghai, UTC, America/New_York"
            >
              <Input
                required
                value={timezone}
                onChange={(event) => setTimezone(event.target.value)}
              />
            </Field>
            <ErrorBox message={store.error} />
            <Button type="submit" variant="primary" busy={store.busy}>
              {t("保存时区", "Save timezone")}
            </Button>
          </form>
        </Modal>
      )}
    </section>
  );
});

const CalendarEditor = observer(function CalendarEditor({
  member,
  close,
}: {
  member: ResourceMember;
  close: () => void;
}) {
  const t = appStore.t;
  const [skills, setSkills] = useState(member.skills.join(", "));
  const [days, setDays] = useState(
    member.weekday_minutes.map((value) => (value == null ? "" : hours(value))),
  );
  const [quota, setQuota] = useState(
    member.project_minutes_per_day == null
      ? ""
      : hours(member.project_minutes_per_day),
  );
  const [exceptions, setExceptions] = useState(
    Object.entries(member.exceptions ?? {}).map(([date, minutes]) => ({
      date,
      hours: minutes == null ? "" : hours(minutes),
    })),
  );
  const [error, setError] = useState("");
  return (
    <Modal
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
      title={`${member.display_name} · ${t("技能与工作日历", "Skills and working calendar")}`}
      className="req-detail-modal"
    >
      <form
        className="req-form"
        onSubmit={async (event) => {
          event.preventDefault();
          const weekday_minutes = days.map(parseHours);
          const project_minutes_per_day = parseHours(quota);
          const values = [
            ...weekday_minutes,
            project_minutes_per_day,
            ...exceptions.map((entry) => parseHours(entry.hours)),
          ];
          if (
            values.some(
              (value) =>
                value !== null &&
                (!Number.isFinite(value) || value < 0 || value > 1440),
            )
          ) {
            setError(
              t(
                "每天小时数须为 0–24 或留空",
                "Daily hours must be between 0 and 24, or empty",
              ),
            );
            return;
          }
          if (
            new Set(exceptions.map((entry) => entry.date)).size !==
            exceptions.length
          ) {
            setError(t("日期例外不能重复", "Exception dates must be unique"));
            return;
          }
          if (
            await store.mutate(() =>
              api.put(`${store.base}/resources/members/${member.member_id}`, {
                version: member.version,
                skills: skills
                  .split(/[,，]/)
                  .map((value) => value.trim())
                  .filter(Boolean),
                weekday_minutes,
                project_minutes_per_day,
                exceptions: Object.fromEntries(
                  exceptions
                    .filter((entry) => entry.date)
                    .map((entry) => [entry.date, parseHours(entry.hours)]),
                ),
              }),
            )
          )
            close();
        }}
      >
        <Field
          label={t("成员技能（逗号分隔）", "Member skills (comma separated)")}
        >
          <Input
            value={skills}
            onChange={(event) => setSkills(event.target.value)}
          />
        </Field>
        <p className="field-hint">
          {t(
            "留空表示未知；0 表示确定不可用。项目每日容量为日历与项目额度的较小值。",
            "Empty means unknown; zero means unavailable. Daily project capacity is the lower of calendar availability and project quota.",
          )}
        </p>
        <div className="req-weekday-grid">
          {[
            ["日", "Sun"],
            ["一", "Mon"],
            ["二", "Tue"],
            ["三", "Wed"],
            ["四", "Thu"],
            ["五", "Fri"],
            ["六", "Sat"],
          ].map(([zh, en], index) => (
            <Field key={en} label={t(`周${zh}（小时）`, `${en} (hours)`)}>
              <Input
                type="number"
                min="0"
                max="24"
                step="any"
                value={days[index] ?? ""}
                onChange={(event) =>
                  setDays((old) =>
                    old.map((value, day) =>
                      day === index ? event.target.value : value,
                    ),
                  )
                }
              />
            </Field>
          ))}
        </div>
        <Field
          label={t(
            "本项目每日可用额度（小时）",
            "Daily capacity allocated to this project (hours)",
          )}
        >
          <Input
            type="number"
            min="0"
            max="24"
            step="any"
            value={quota}
            onChange={(event) => setQuota(event.target.value)}
          />
        </Field>
        <fieldset className="req-fieldset">
          <legend>{t("日期例外 / 休假", "Date exceptions / leave")}</legend>
          {exceptions.map((entry, index) => (
            <div key={index} className="req-exception">
              <Field label={t("日期", "Date")}>
                <Input
                  type="date"
                  required
                  value={entry.date}
                  onChange={(event) =>
                    setExceptions((old) =>
                      old.map((value, position) =>
                        position === index
                          ? { ...value, date: event.target.value }
                          : value,
                      ),
                    )
                  }
                />
              </Field>
              <Field label={t("可用小时", "Available hours")}>
                <Input
                  type="number"
                  min="0"
                  max="24"
                  step="any"
                  value={entry.hours}
                  onChange={(event) =>
                    setExceptions((old) =>
                      old.map((value, position) =>
                        position === index
                          ? { ...value, hours: event.target.value }
                          : value,
                      ),
                    )
                  }
                />
              </Field>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={t("删除例外", "Remove exception")}
                onClick={() =>
                  setExceptions((old) =>
                    old.filter((_, position) => position !== index),
                  )
                }
              >
                <X size={14} />
              </Button>
            </div>
          ))}
          <Button
            type="button"
            size="sm"
            onClick={() =>
              setExceptions((old) => [...old, { date: "", hours: "0" }])
            }
          >
            <Plus size={14} />
            {t("添加例外", "Add exception")}
          </Button>
        </fieldset>
        <ErrorBox message={error || store.error} />
        <Button type="submit" variant="primary" busy={store.busy}>
          {t("保存日历与技能", "Save calendar and skills")}
        </Button>
      </form>
    </Modal>
  );
});

export const ConflictPanel = observer(function ConflictPanel({
  openItem,
  locateMember,
}: {
  openItem: (id: string) => void;
  locateMember: (id: string) => void;
}) {
  const t = appStore.t;
  const [kind, setKind] = useState("");
  const conflicts = store.load?.conflicts ?? [];
  const labels: Record<string, [string, string]> = {
    unknown_estimate: ["未知工时", "Unknown effort"],
    unknown_capacity: ["未知容量", "Unknown capacity"],
    no_working_days: ["无可用工作日", "No working days"],
    unassigned: ["未分配", "Unassigned"],
    member_removed: ["成员已移除", "Member removed"],
    skill_mismatch: ["技能不匹配", "Skill mismatch"],
    over_capacity: ["超负荷", "Over capacity"],
    date_order: ["日期顺序", "Date order"],
    dependency: ["依赖冲突", "Dependency conflict"],
    commitment: ["承诺越界", "Commitment overrun"],
    unscheduled: ["未排期", "Unscheduled"],
  };
  const label = (conflict: LoadConflict) =>
    labels[conflict.type] ? t(...labels[conflict.type]) : conflict.type;
  return (
    <details
      className="req-conflicts"
      open={conflicts.length > 0 && conflicts.length < 5}
    >
      <summary>
        {t("日期、依赖和资源冲突", "Date, dependency and resource conflicts")}{" "}
        <span>{conflicts.length}</span>
      </summary>
      <div className="req-conflicts-body">
        <Select
          aria-label={t("冲突类型", "Conflict type")}
          value={kind}
          onChange={(event) => setKind(event.target.value)}
        >
          <option value="">{t("所有类型", "All types")}</option>
          {[...new Set(conflicts.map((conflict) => conflict.type))].map(
            (type) => (
              <option key={type} value={type}>
                {labels[type] ? t(...labels[type]) : type}
              </option>
            ),
          )}
        </Select>
        {conflicts
          .filter((conflict) => !kind || conflict.type === kind)
          .map((conflict, index) => (
            <div
              className="req-conflict"
              key={`${conflict.type}-${conflict.task_id}-${conflict.member_id}-${conflict.date}-${index}`}
            >
              <strong>{label(conflict)}</strong>
              <span>
                {conflict.date} {conflict.message}
              </span>
              <div className="req-actions">
                {conflict.task_id && (
                  <Button size="sm" onClick={() => openItem(conflict.task_id!)}>
                    {store.items.get(conflict.task_id)?.name ??
                      t("定位任务", "Open task")}
                  </Button>
                )}
                {conflict.related_task_id && (
                  <Button
                    size="sm"
                    onClick={() => openItem(conflict.related_task_id!)}
                  >
                    {t("定位前置任务", "Open predecessor")}
                  </Button>
                )}
                {conflict.member_id && (
                  <Button
                    size="sm"
                    onClick={() => locateMember(conflict.member_id!)}
                  >
                    {t("定位成员", "Locate member")}
                  </Button>
                )}
              </div>
            </div>
          ))}
        {!conflicts.length && (
          <p>
            {store.load
              ? t(
                  "当前日期范围内没有已识别冲突。",
                  "No identified conflicts in the current date range.",
                )
              : t(
                  "正在取得服务端容量计算。",
                  "Waiting for server capacity calculations.",
                )}
          </p>
        )}
      </div>
    </details>
  );
});
