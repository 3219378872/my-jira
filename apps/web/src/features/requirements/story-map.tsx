import { useState, type FormEvent } from "react";
import { observer } from "mobx-react-lite";
import { ArrowDown, ArrowUp, Flag, GripVertical, Plus } from "lucide-react";
import { appStore } from "../../stores/app-store";
import { useScope } from "../../lib/hooks";
import { api } from "../../lib/api";
import {
  Button,
  ErrorBox,
  Field,
  Input,
  Menu,
  Modal,
  Select,
} from "../../components/ui";
import type { Cycle, WorkItem } from "../../types";
import type { PlanningCommand, UserActivity } from "./types";
import { descendantIDs, hours, shiftDate } from "./semantics";
import { requirementsStore as store } from "./store";

interface Props {
  openItem: (id: string) => void;
  createItem: (seed: Partial<WorkItem>) => void;
}

export const StoryMap = observer(function StoryMap({
  openItem,
  createItem,
}: Props) {
  const { project } = useScope();
  const canEdit = store.canEdit;
  const t = appStore.t;
  const [activityEdit, setActivityEdit] = useState<{
    item: UserActivity | null;
    epic: string | null;
  } | null>(null);
  const [cycleEdit, setCycleEdit] = useState<{ item: Cycle | null } | null>(
    null,
  );
  const [moving, setMoving] = useState<WorkItem | null>(null);
  const epics = store.snapshot.items
    .filter(
      (item) =>
        !item.archived_at &&
        item.requirement_type === "epic" &&
        (!store.filters.epic || store.filters.epic === item.id),
    )
    .sort(
      (a, b) => (a.map_position ?? a.position) - (b.map_position ?? b.position),
    );
  const activities = store.snapshot.activities
    .filter((activity) => !activity.archived_at)
    .sort((a, b) => a.position - b.position);
  const columns = [
    ...epics.map((epic) => ({
      epic,
      activities: activities.filter((activity) => activity.epic_id === epic.id),
    })),
    ...(!store.filters.epic
      ? [
          {
            epic: null,
            activities: activities.filter((activity) => !activity.epic_id),
          },
        ]
      : []),
  ];
  const cycles = store.snapshot.cycles.filter(
    (cycle) =>
      !cycle.archived_at &&
      (!store.filters.cycle || store.filters.cycle === cycle.id),
  );
  const lanes = [
    ...(!store.filters.cycle || store.filters.cycle === "backlog"
      ? [{ id: "", name: "Backlog", start_date: null, end_date: null }]
      : []),
    ...cycles,
  ];
  const stories = store.visibleItems.filter(
    (item) => item.requirement_type === "story",
  );
  const unplaced = stories.filter(
    (story) =>
      !lanes.some((lane) => lane.id === (story.cycle_id ?? "")) ||
      !columns.some(
        (column) =>
          (column.epic?.id ?? "") === (story.parent_id ?? "") &&
          (!story.activity_id ||
            column.activities.some(
              (activity) => activity.id === story.activity_id,
            )),
      ),
  );
  const states = store.states;
  const move = (
    item: WorkItem,
    activity: UserActivity | null,
    epic: WorkItem | null,
    cycleID: string,
  ) => {
    const siblings = store.snapshot.items.filter(
      (entry) =>
        entry.requirement_type === "story" &&
        (entry.activity_id ?? "") === (activity?.id ?? "") &&
        (entry.parent_id ?? "") === (epic?.id ?? "") &&
        (entry.cycle_id ?? "") === cycleID,
    );
    void store.update(item, {
      activity_id: activity?.id ?? null,
      parent_id: epic?.id ?? null,
      cycle_id: cycleID || null,
      map_position:
        Math.max(
          0,
          ...siblings.map((entry) => entry.map_position ?? entry.position),
        ) + 65536,
    });
  };
  const reorder = (item: WorkItem, siblings: WorkItem[], direction: number) => {
    const index = siblings.findIndex((entry) => entry.id === item.id),
      target = siblings[index + direction];
    if (!target) return;
    if (
      new Set(siblings.map((entry) => entry.map_position ?? entry.position))
        .size !== siblings.length
    ) {
      const ordered = [...siblings];
      [ordered[index], ordered[index + direction]] = [
        ordered[index + direction],
        ordered[index],
      ];
      const commands: PlanningCommand[] = ordered.map((entry, position) => ({
        operation: "update",
        id: entry.id,
        version: entry.version,
        fields: { map_position: (position + 1) * 65536 },
      }));
      if (commands.length > 200) {
        store.setError(
          t(
            "此次排序需要更新超过 200 项，请先按 Epic 或 Sprint 缩小范围。",
            "This reorder would change more than 200 items. Narrow the group by epic or sprint.",
          ),
        );
        return;
      }
      void store.change(commands);
      return;
    }
    const beyond = siblings[index + direction * 2];
    const targetPosition = target.map_position ?? target.position;
    const position = beyond
      ? (targetPosition + (beyond.map_position ?? beyond.position)) / 2
      : targetPosition + direction * 65536;
    void store.update(item, { map_position: position });
  };
  return (
    <section
      aria-label={t("用户故事地图", "User story map")}
      className="req-story-map"
    >
      <div className="req-view-heading">
        <div>
          <h2>
            {t(
              "围绕用户活动安排交付",
              "Organize delivery around user activities",
            )}
          </h2>
          <p>
            {t(
              "横轴保留活动骨架，纵轴表示承诺 Sprint。移动故事保留子任务的执行安排。",
              "Activities form the horizontal backbone; rows represent commitment sprints. Moving a story preserves task schedules.",
            )}
          </p>
        </div>
        {canEdit && (
          <div className="req-actions">
            <Button
              size="sm"
              onClick={() => createItem({ requirement_type: "epic" })}
            >
              <Plus size={14} />
              Epic
            </Button>
            <Button size="sm" onClick={() => setCycleEdit({ item: null })}>
              <Plus size={14} />
              Sprint
            </Button>
          </div>
        )}
      </div>
      <div className="req-map-scroll">
        <div
          className="req-map-grid"
          style={{
            gridTemplateColumns: `150px ${columns
              .flatMap((column) => [...column.activities, null])
              .map(() => "minmax(230px, 1fr)")
              .join(" ")}`,
          }}
        >
          <div className="req-map-corner">
            {t("交付切片", "Delivery slices")}
          </div>
          {columns.map(({ epic, activities: groupActivities }, groupIndex) => (
            <div
              className="req-epic-heading"
              key={epic?.id ?? "none"}
              style={{ gridColumn: `span ${groupActivities.length + 1}` }}
            >
              <button
                type="button"
                onClick={() => epic && openItem(epic.id)}
                disabled={!epic}
              >
                <Flag size={15} />
                <strong>{epic?.name ?? t("未分配 Epic", "No epic")}</strong>
              </button>
              {canEdit && (
                <div className="req-actions">
                  {epic && (
                    <>
                      <Button
                        size="icon"
                        variant="ghost"
                        aria-label={t("Epic 左移", "Move epic left")}
                        disabled={groupIndex === 0}
                        onClick={() => reorder(epic, epics, -1)}
                      >
                        <ArrowUp size={13} />
                      </Button>
                      <Button
                        size="icon"
                        variant="ghost"
                        aria-label={t("Epic 右移", "Move epic right")}
                        disabled={groupIndex >= epics.length - 1}
                        onClick={() => reorder(epic, epics, 1)}
                      >
                        <ArrowDown size={13} />
                      </Button>
                    </>
                  )}
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() =>
                      setActivityEdit({ item: null, epic: epic?.id ?? null })
                    }
                  >
                    <Plus size={13} />
                    {t("活动", "Activity")}
                  </Button>
                </div>
              )}
            </div>
          ))}
          <div className="req-map-axis">Sprint</div>
          {columns.flatMap(({ epic, activities: groupActivities }) =>
            [...groupActivities, null].map((activity) => (
              <div
                key={`${epic?.id}-${activity?.id ?? "none"}`}
                className="req-activity-heading"
              >
                <span>{activity?.name ?? t("未分配活动", "No activity")}</span>
                {activity && canEdit && (
                  <Menu
                    label={`${t("活动操作", "Activity actions")} ${activity.name}`}
                    items={[
                      {
                        label: t(
                          "编辑 / 归档 / 删除",
                          "Edit / archive / delete",
                        ),
                        onSelect: () =>
                          setActivityEdit({
                            item: activity,
                            epic: epic?.id ?? null,
                          }),
                      },
                      {
                        label: t("向左移动", "Move left"),
                        disabled: groupActivities.indexOf(activity) === 0,
                        onSelect: () => {
                          const before =
                            groupActivities[
                              groupActivities.indexOf(activity) - 1
                            ];
                          if (before)
                            void store.saveActivity(activity, {
                              position: groupActivities[
                                groupActivities.indexOf(activity) - 2
                              ]
                                ? (before.position +
                                    groupActivities[
                                      groupActivities.indexOf(activity) - 2
                                    ].position) /
                                  2
                                : before.position - 65536,
                            });
                        },
                      },
                      {
                        label: t("向右移动", "Move right"),
                        disabled:
                          groupActivities.indexOf(activity) ===
                          groupActivities.length - 1,
                        onSelect: () => {
                          const after =
                            groupActivities[
                              groupActivities.indexOf(activity) + 1
                            ];
                          if (after)
                            void store.saveActivity(activity, {
                              position: groupActivities[
                                groupActivities.indexOf(activity) + 2
                              ]
                                ? (after.position +
                                    groupActivities[
                                      groupActivities.indexOf(activity) + 2
                                    ].position) /
                                  2
                                : after.position + 65536,
                            });
                        },
                      },
                    ]}
                  />
                )}
              </div>
            )),
          )}
          {lanes.map((lane) => (
            <MapLane
              key={lane.id || "backlog"}
              lane={lane}
              columns={columns}
              stories={stories}
              canEdit={canEdit}
              editCycle={() => setCycleEdit({ item: lane as Cycle })}
              renderCard={(item, siblings) => {
                const conflicts =
                  store.load?.conflicts.filter(
                    (conflict) =>
                      conflict.task_id === item.id ||
                      descendantIDs(item.id, store.snapshot.items).includes(
                        conflict.task_id ?? "",
                      ),
                  ) ?? [];
                const blocked = store.snapshot.dependencies.some(
                  (dependency) => dependency.target_id === item.id,
                );
                const children = store.snapshot.items.filter(
                  (child) => child.parent_id === item.id,
                );
                return (
                  <article
                    key={item.id}
                    data-testid={`story-card-${item.id}`}
                    draggable={canEdit && !store.busy}
                    onDragStart={(event) => {
                      event.dataTransfer.setData(
                        "application/x-myjira-requirement",
                        item.id,
                      );
                      event.dataTransfer.effectAllowed = "move";
                    }}
                    className={`req-story-card ${store.selectedID === item.id ? "is-selected" : ""}`}
                  >
                    <div className="req-card-top">
                      <span>
                        {project!.identifier}-{item.sequence_id}
                      </span>
                      {canEdit && (
                        <Menu
                          label={`${t("故事操作", "Story actions")} ${item.name}`}
                          items={[
                            {
                              label: t("编辑需求", "Edit requirement"),
                              onSelect: () => openItem(item.id),
                            },
                            {
                              label: t("移动到…", "Move to…"),
                              onSelect: () => setMoving(item),
                            },
                            {
                              label: t("向上排序", "Move up"),
                              disabled: siblings[0]?.id === item.id,
                              onSelect: () => reorder(item, siblings, -1),
                            },
                            {
                              label: t("向下排序", "Move down"),
                              disabled: siblings.at(-1)?.id === item.id,
                              onSelect: () => reorder(item, siblings, 1),
                            },
                            {
                              label: t("添加子任务", "Add task"),
                              onSelect: () =>
                                createItem({
                                  requirement_type: "task",
                                  parent_id: item.id,
                                  cycle_id: item.cycle_id,
                                }),
                            },
                          ]}
                        />
                      )}
                    </div>
                    <button
                      className="req-card-title"
                      onClick={() => openItem(item.id)}
                    >
                      {item.name}
                    </button>
                    {item.story_role && (
                      <p className="req-story-narrative">
                        {t("作为", "As")} {item.story_role} ·{" "}
                        {item.story_goal || item.story_benefit}
                      </p>
                    )}
                    <div className="req-card-facts">
                      <span>
                        {children.length
                          ? `${children.length} ${t("子任务", "tasks")}`
                          : t("未拆解", "Not decomposed")}
                      </span>
                      <span>{hours(item.remaining_minutes)}h</span>
                      {!item.assignee_ids.length && (
                        <span>{t("未分配", "Unassigned")}</span>
                      )}
                      {blocked && (
                        <span>{t("有前置依赖", "Has predecessors")}</span>
                      )}
                      {conflicts.length > 0 && (
                        <span className="req-warning">
                          {conflicts.length} {t("冲突", "conflicts")}
                        </span>
                      )}
                    </div>
                    <div className="req-card-status">
                      <Select
                        aria-label={`${t("状态", "Status")} ${item.name}`}
                        value={item.state_id}
                        disabled={!canEdit || store.busy}
                        onChange={(event) =>
                          void store.update(item, {
                            state_id: event.target.value,
                          })
                        }
                      >
                        {states.map((state) => (
                          <option key={state.id} value={state.id}>
                            {state.name}
                          </option>
                        ))}
                      </Select>
                      {canEdit && <GripVertical size={14} aria-hidden="true" />}
                    </div>
                  </article>
                );
              }}
              onDrop={move}
              createItem={createItem}
            />
          ))}
        </div>
      </div>
      {!stories.length && (
        <p className="req-empty-inline">
          {t(
            "当前筛选没有故事；空活动和 Sprint 会继续保留。",
            "No stories match the filters. Empty activities and sprints remain visible.",
          )}
        </p>
      )}
      {unplaced.length > 0 && (
        <div className="req-unplaced">
          <strong>
            {t(
              "归属骨架已归档或缺失的故事",
              "Stories whose backbone is archived or unavailable",
            )}{" "}
            ({unplaced.length})
          </strong>
          <p className="field-hint">
            {t(
              "故事和子任务仍然保留，可编辑归属后重新放入地图。",
              "Stories and tasks remain available. Move them into an active map location.",
            )}
          </p>
          {unplaced.map((story) => (
            <div key={story.id} className="req-actions">
              <Button variant="ghost" onClick={() => openItem(story.id)}>
                {story.name}
              </Button>
              {canEdit && (
                <Button size="sm" onClick={() => setMoving(story)}>
                  {t("移动到…", "Move to…")}
                </Button>
              )}
            </div>
          ))}
        </div>
      )}
      {activityEdit && (
        <ActivityEditor
          item={activityEdit.item}
          epic={activityEdit.epic}
          close={() => setActivityEdit(null)}
        />
      )}
      {cycleEdit && (
        <SprintEditor item={cycleEdit.item} close={() => setCycleEdit(null)} />
      )}
      {moving && <StoryMove item={moving} close={() => setMoving(null)} />}
    </section>
  );
});

function MapLane({
  lane,
  columns,
  stories,
  canEdit,
  editCycle,
  renderCard,
  onDrop,
  createItem,
}: {
  lane: Pick<Cycle, "id" | "name" | "start_date" | "end_date">;
  columns: { epic: WorkItem | null; activities: UserActivity[] }[];
  stories: WorkItem[];
  canEdit: boolean;
  editCycle: () => void;
  renderCard: (item: WorkItem, siblings: WorkItem[]) => React.ReactNode;
  onDrop: (
    item: WorkItem,
    activity: UserActivity | null,
    epic: WorkItem | null,
    cycleID: string,
  ) => void;
  createItem: Props["createItem"];
}) {
  const t = appStore.t;
  return (
    <>
      <div className="req-sprint-label">
        <strong>{lane.name}</strong>
        {lane.start_date && (
          <small>
            {lane.start_date.slice(0, 10)}
            <br />
            {lane.end_date?.slice(0, 10)}
          </small>
        )}
        {lane.id && canEdit && (
          <Button size="sm" variant="ghost" onClick={editCycle}>
            {t("编辑 Sprint", "Edit sprint")}
          </Button>
        )}
      </div>
      {columns.flatMap(({ epic, activities }) =>
        [...activities, null].map((activity) => {
          const siblings = stories
            .filter(
              (item) =>
                (item.cycle_id ?? "") === lane.id &&
                (item.parent_id ?? "") === (epic?.id ?? "") &&
                (item.activity_id ?? "") === (activity?.id ?? ""),
            )
            .sort(
              (a, b) =>
                (a.map_position ?? a.position) - (b.map_position ?? b.position),
            );
          return (
            <div
              key={`${epic?.id}-${activity?.id ?? "none"}`}
              className="req-map-cell"
              data-testid={`story-cell-${activity?.id ?? epic?.id ?? "none"}-${lane.id || "backlog"}`}
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
                if (canEdit && item?.requirement_type === "story")
                  onDrop(item, activity, epic, lane.id);
              }}
            >
              {siblings.map((item) => renderCard(item, siblings))}
              {canEdit && (
                <Button
                  className="req-cell-add"
                  variant="ghost"
                  size="sm"
                  onClick={() =>
                    createItem({
                      requirement_type: "story",
                      parent_id: epic?.id ?? null,
                      activity_id: activity?.id ?? null,
                      cycle_id: lane.id || null,
                      map_position:
                        Math.max(
                          0,
                          ...siblings.map(
                            (item) => item.map_position ?? item.position,
                          ),
                        ) + 65536,
                    })
                  }
                >
                  <Plus size={13} />
                  {t("添加故事", "Add story")}
                </Button>
              )}
            </div>
          );
        }),
      )}
    </>
  );
}

const ActivityEditor = observer(function ActivityEditor({
  item,
  epic,
  close,
}: {
  item: UserActivity | null;
  epic: string | null;
  close: () => void;
}) {
  const t = appStore.t;
  const [name, setName] = useState(item?.name ?? "");
  const [destination, setDestination] = useState("null");
  const populated =
    !!item &&
    store.snapshot.items.some((story) => story.activity_id === item.id);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (
      await store.saveActivity(item, {
        name,
        epic_id: epic,
        position:
          item?.position ??
          Math.max(
            0,
            ...store.snapshot.activities
              .filter((entry) => entry.epic_id === epic)
              .map((entry) => entry.position),
          ) + 65536,
      })
    )
      close();
  };
  return (
    <Modal
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
      title={
        item
          ? t("编辑用户活动", "Edit user activity")
          : t("创建用户活动", "Create user activity")
      }
    >
      <form className="req-form" onSubmit={submit}>
        <Field label={t("活动名称", "Activity name")}>
          <Input
            autoFocus
            value={name}
            required
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        {item && (
          <Field
            label={t("删除时迁移故事到", "When deleting, migrate stories to")}
            hint={
              populated
                ? t(
                    "故事将原子迁移后再删除活动。",
                    "Stories move atomically before this activity is deleted.",
                  )
                : undefined
            }
          >
            <Select
              value={destination}
              onChange={(event) => setDestination(event.target.value)}
            >
              <option value="null">
                {t("同 Epic 的未分配活动", "No activity within the same epic")}
              </option>
              {store.snapshot.activities
                .filter(
                  (entry) =>
                    entry.id !== item.id &&
                    entry.epic_id === epic &&
                    !entry.archived_at,
                )
                .map((entry) => (
                  <option key={entry.id} value={entry.id}>
                    {entry.name}
                  </option>
                ))}
            </Select>
          </Field>
        )}
        <ErrorBox message={store.error} />
        <div className="req-form-footer">
          {item && (
            <>
              <Button
                type="button"
                variant="danger"
                disabled={store.busy}
                onClick={async () => {
                  if (
                    window.confirm(
                      t(
                        "删除此活动并迁移其中故事？",
                        "Delete this activity and migrate its stories?",
                      ),
                    ) &&
                    (await store.mutate(() =>
                      api.delete(
                        `${store.base}/requirements/activities/${item.id}?version=${item.version}&migrate_to=${destination}`,
                      ),
                    ))
                  )
                    close();
                }}
              >
                {t("删除", "Delete")}
              </Button>
              <Button
                type="button"
                disabled={store.busy || populated}
                title={
                  populated
                    ? t("请先迁移故事后归档", "Move stories before archiving")
                    : undefined
                }
                onClick={async () => {
                  if (
                    await store.saveActivity(item, {
                      archived_at: new Date().toISOString(),
                    })
                  )
                    close();
                }}
              >
                {t("归档", "Archive")}
              </Button>
            </>
          )}
          <span className="req-spacer" />
          <Button type="submit" variant="primary" busy={store.busy}>
            {t("保存活动", "Save activity")}
          </Button>
        </div>
      </form>
    </Modal>
  );
});

export const SprintEditor = observer(function SprintEditor({
  item,
  close,
}: {
  item: Cycle | null;
  close: () => void;
}) {
  const t = appStore.t;
  const [name, setName] = useState(item?.name ?? "");
  const [start, setStart] = useState(item?.start_date?.slice(0, 10) ?? "");
  const [end, setEnd] = useState(item?.end_date?.slice(0, 10) ?? "");
  return (
    <Modal
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
      title={
        item
          ? t("编辑 Sprint", "Edit sprint")
          : t("创建 Sprint", "Create sprint")
      }
    >
      <form
        className="req-form"
        onSubmit={async (event) => {
          event.preventDefault();
          const fields = {
            name,
            start_date: start || null,
            end_date: end || null,
            owner_id: item?.owner_id ?? appStore.user!.id,
          };
          if (
            await store.mutate(() =>
              item
                ? api.patch(`${store.base}/cycles/${item.id}`, fields)
                : api.post(`${store.base}/cycles`, fields),
            )
          )
            close();
        }}
      >
        <Field label={t("Sprint 名称", "Sprint name")}>
          <Input
            required
            autoFocus
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <div className="req-form-grid">
          <Field label={t("开始日期", "Start date")}>
            <Input
              type="date"
              value={start}
              onChange={(event) => setStart(event.target.value)}
            />
          </Field>
          <Field label={t("结束日期", "End date")}>
            <Input
              type="date"
              min={start}
              value={end}
              onChange={(event) => setEnd(event.target.value)}
            />
          </Field>
        </div>
        <p className="field-hint">
          {t(
            "Sprint 可以重叠，任务归属不会根据日期自动推断。",
            "Sprints may overlap. Task membership is always explicit.",
          )}
        </p>
        <ErrorBox message={store.error} />
        <div className="req-form-footer">
          {item && (
            <Button
              type="button"
              variant="danger"
              onClick={async () => {
                if (
                  window.confirm(
                    t(
                      "删除 Sprint？工作项将返回 Backlog。",
                      "Delete this sprint? Work items return to Backlog.",
                    ),
                  ) &&
                  (await store.mutate(() =>
                    api.delete(`${store.base}/cycles/${item.id}`),
                  ))
                )
                  close();
              }}
            >
              {t("删除 Sprint", "Delete sprint")}
            </Button>
          )}
          <span className="req-spacer" />
          <Button type="submit" variant="primary" busy={store.busy}>
            {t("保存 Sprint", "Save sprint")}
          </Button>
        </div>
      </form>
    </Modal>
  );
});

const StoryMove = observer(function StoryMove({
  item,
  close,
}: {
  item: WorkItem;
  close: () => void;
}) {
  const t = appStore.t;
  const [epic, setEpic] = useState(item.parent_id ?? "");
  const [activity, setActivity] = useState(item.activity_id ?? "");
  const [cycle, setCycle] = useState(item.cycle_id ?? "");
  const [children, setChildren] = useState(false);
  const [days, setDays] = useState(0);
  const descendants = descendantIDs(item.id, store.snapshot.items);
  return (
    <Modal
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
      title={t("移动故事", "Move story")}
    >
      <form
        className="req-form"
        onSubmit={async (event) => {
          event.preventDefault();
          const commands: PlanningCommand[] = [
            {
              operation: "update",
              id: item.id,
              version: item.version,
              fields: {
                parent_id: epic || null,
                activity_id: activity || null,
                cycle_id: cycle || null,
                map_position:
                  Math.max(
                    0,
                    ...store.snapshot.items.map(
                      (entry) => entry.map_position ?? entry.position,
                    ),
                  ) + 65536,
              },
            },
          ];
          if (children)
            for (const id of descendants) {
              const child = store.items.get(id)!;
              commands.push({
                operation: "update",
                id,
                version: child.version,
                fields: {
                  cycle_id: cycle || null,
                  ...(days
                    ? {
                        start_date: shiftDate(child.start_date, days),
                        target_date: shiftDate(child.target_date, days),
                      }
                    : {}),
                },
              });
            }
          if (await store.change(commands)) close();
        }}
      >
        <Field label="Epic">
          <Select
            value={epic}
            onChange={(event) => {
              setEpic(event.target.value);
              setActivity("");
            }}
          >
            <option value="">{t("未分配", "Unassigned")}</option>
            {store.snapshot.items
              .filter((entry) => entry.requirement_type === "epic")
              .map((entry) => (
                <option key={entry.id} value={entry.id}>
                  {entry.name}
                </option>
              ))}
          </Select>
        </Field>
        <Field label={t("用户活动", "User activity")}>
          <Select
            value={activity}
            onChange={(event) => setActivity(event.target.value)}
          >
            <option value="">{t("未分配", "Unassigned")}</option>
            {store.snapshot.activities
              .filter(
                (entry) => !entry.archived_at && (entry.epic_id ?? "") === epic,
              )
              .map((entry) => (
                <option key={entry.id} value={entry.id}>
                  {entry.name}
                </option>
              ))}
          </Select>
        </Field>
        <Field label={t("承诺 Sprint", "Commitment sprint")}>
          <Select
            value={cycle}
            onChange={(event) => setCycle(event.target.value)}
          >
            <option value="">Backlog</option>
            {store.snapshot.cycles
              .filter((entry) => !entry.archived_at)
              .map((entry) => (
                <option key={entry.id} value={entry.id}>
                  {entry.name}
                </option>
              ))}
          </Select>
        </Field>
        <p>
          {t(
            "默认仅更新故事的交付承诺，保留子任务日期和执行 Sprint。",
            "By default, only the story commitment changes; task dates and execution sprints remain.",
          )}
        </p>
        <label className="req-check">
          <input
            type="checkbox"
            checked={children}
            onChange={(event) => setChildren(event.target.checked)}
          />
          {t(
            `同时迁移 ${descendants.length} 个子任务的执行 Sprint`,
            `Also move ${descendants.length} child task execution sprints`,
          )}
        </label>
        {children && (
          <Field
            label={t(
              "子任务日期平移（天，可为负数）",
              "Shift child task dates (days, may be negative)",
            )}
          >
            <Input
              type="number"
              step="1"
              value={days}
              onChange={(event) => setDays(Number(event.target.value))}
            />
          </Field>
        )}
        <ErrorBox message={store.error} />
        <Button type="submit" variant="primary" busy={store.busy}>
          {t("原子提交移动", "Apply move atomically")}
        </Button>
      </form>
    </Modal>
  );
});
