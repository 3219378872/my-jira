import { useState } from "react";
import { observer } from "mobx-react-lite";
import { NavLink } from "react-router-dom";
import {
  ArrowDown,
  ArrowUp,
  ChevronDown,
  ChevronRight,
  FileText,
  Pin,
  Plus,
  Settings2,
  Star,
  Trash2,
  type LucideIcon,
} from "lucide-react";
import { appStore } from "../stores/app-store";
import { api, workspacePath } from "../lib/api";
import { bookmarkRoute, useFavorites } from "../lib/bookmarks";
import { useMutation, useScope } from "../lib/hooks";
import { useWorkspacePreference } from "../lib/preferences";
import { cn } from "../lib/utils";
import { requirementsEnabled } from "../lib/project-features";
import { Button, ErrorBox, Field, Input, Menu, Modal, Select } from "./ui";

interface SidebarPreferences {
  pinned: string[];
  order: string[];
  groups: { id: string; name: string }[];
  favorite_groups: Record<string, string>;
}
const defaults: SidebarPreferences = {
  pinned: [],
  order: [],
  groups: [],
  favorite_groups: {},
};

export const SidebarProjects = observer(function SidebarProjects({
  loaded,
  items,
}: {
  loaded: boolean;
  items: { path: string; zh: string; en: string; icon: LucideIcon }[];
}) {
  const { workspace, project } = useScope();
  const preference = useWorkspacePreference("sidebar", defaults);
  const favorites = useFavorites();
  const mutation = useMutation();
  const [groupsOpen, setGroupsOpen] = useState(false);
  const [groupName, setGroupName] = useState("");
  const [editingGroup, setEditingGroup] = useState<string | null>(null);
  const t = appStore.t;
  const base = `/w/${workspace.slug}`;
  const allProjects = appStore
    .workspaceProjects(workspace.id)
    .filter((item) => !item.archived_at);
  const rank = (id: string) => {
    const index = preference.value.order.indexOf(id);
    return index === -1 ? Number.MAX_SAFE_INTEGER : index;
  };
  const projects = [...allProjects].sort(
    (a, b) =>
      Number(preference.value.pinned.includes(b.id)) -
        Number(preference.value.pinned.includes(a.id)) ||
      rank(a.id) - rank(b.id),
  );
  const groups = [
    { id: "", name: t("收藏", "Favorites") },
    ...preference.value.groups,
  ];
  const moveProject = (index: number, delta: number) => {
    const order = projects.map((item) => item.id);
    [order[index], order[index + delta]] = [order[index + delta], order[index]];
    return preference.save({ order });
  };
  const moveFavorite = async (id: string, direction: number) => {
    const list = favorites.data ?? [];
    const group = preference.value.favorite_groups[id] ?? "";
    const inGroup = list.filter(
      (item) => (preference.value.favorite_groups[item.id] ?? "") === group,
    );
    const index = inGroup.findIndex((item) => item.id === id);
    const target = inGroup[index + direction];
    if (!target) return;
    // Each record has a stable personal position. Renumber ties through the API
    // before calculating a new position so moves remain effective after reload.
    const next = [...list];
    const from = next.findIndex((item) => item.id === id);
    const to = next.findIndex((item) => item.id === target.id);
    const [item] = next.splice(from, 1);
    next.splice(to, 0, item);
    for (let offset = 0; offset < next.length; offset++) {
      if (next[offset].position !== (offset + 1) * 1000)
        await api.patch(
          `${workspacePath(workspace.id)}/favorites/${next[offset].id}`,
          { position: (offset + 1) * 1000 },
        );
    }
    favorites.refresh();
  };
  return (
    <>
      <div className="sidebar-section-heading">
        <span>{t("收藏", "Favorites")}</span>
        <Button
          size="icon"
          variant="ghost"
          aria-label={t("管理收藏分组", "Manage favorite groups")}
          onClick={() => setGroupsOpen(true)}
        >
          <Settings2 size={13} />
        </Button>
      </div>
      <nav className="sidebar-favorites" aria-label={t("收藏", "Favorites")}>
        <ErrorBox message={favorites.error} />
        {groups.map((group) => {
          const groupItems = (favorites.data ?? []).filter((item) => {
            const assigned = preference.value.favorite_groups[item.id] ?? "";
            return (
              group.id === assigned ||
              (!group.id && !groups.some((entry) => entry.id === assigned))
            );
          });
          if (!groupItems.length && !group.id) return null;
          return (
            <div key={group.id}>
              {group.id && (
                <p className="sidebar-favorite-group">{group.name}</p>
              )}
              {groupItems.map((item, index) => (
                <div className="sidebar-entry" key={item.id}>
                  <NavLink
                    className={({ isActive }) =>
                      cn("nav-link", isActive && "active")
                    }
                    to={bookmarkRoute(workspace.slug, item)}
                  >
                    <FileText size={13} />
                    <span>{item.entity.name}</span>
                  </NavLink>
                  <Menu
                    items={[
                      ...groups
                        .filter((entry) => entry.id !== group.id)
                        .map((entry) => ({
                          label: `${t("移至", "Move to")} ${entry.name}`,
                          onSelect: () => {
                            mutation.execute(() =>
                              preference.save((current) => ({
                                favorite_groups: {
                                  ...current.favorite_groups,
                                  [item.id]: entry.id,
                                },
                              })),
                            );
                          },
                        })),
                      ...(index
                        ? [
                            {
                              label: t("上移", "Move up"),
                              onSelect: () => {
                                mutation.execute(() =>
                                  moveFavorite(item.id, -1),
                                );
                              },
                            },
                          ]
                        : []),
                      ...(index < groupItems.length - 1
                        ? [
                            {
                              label: t("下移", "Move down"),
                              onSelect: () => {
                                mutation.execute(() =>
                                  moveFavorite(item.id, 1),
                                );
                              },
                            },
                          ]
                        : []),
                      {
                        label: t("取消收藏", "Remove favorite"),
                        icon: <Star size={13} />,
                        onSelect: () => {
                          mutation.execute(() =>
                            api.delete(
                              `${workspacePath(workspace.id)}/favorites/${item.id}`,
                            ),
                          );
                        },
                      },
                    ]}
                  />
                </div>
              ))}
            </div>
          );
        })}
        {!favorites.loading && !favorites.data?.length && (
          <p className="sidebar-favorite-group">
            {t(
              "收藏项目、工作项或文档",
              "Save projects, work items or documents",
            )}
          </p>
        )}
      </nav>
      <div className="sidebar-section-heading">
        <span>{t("工作区项目", "Workspace projects")}</span>
        {workspace.role >= 15 && (
          <Button
            size="icon"
            variant="ghost"
            aria-label={t("创建项目", "Create project")}
            onClick={() => appStore.setCreateProjectOpen(true)}
          >
            <Plus size={14} />
          </Button>
        )}
      </div>
      <ErrorBox message={preference.error || mutation.error} />
      <nav className="project-nav">
        {projects.map((item, index) => {
          const pinned = preference.value.pinned.includes(item.id);
          const favorite = favorites.data?.find(
            (entry) =>
              entry.entity_type === "project" && entry.entity_id === item.id,
          );
          return (
            <div key={item.id}>
              <div className="sidebar-entry">
                <NavLink
                  to={`${base}/projects/${item.id}/issues`}
                  className={cn(
                    "nav-link",
                    "project-nav-link",
                    project?.id === item.id && "project-expanded",
                  )}
                >
                  <span
                    className="project-color"
                    style={{ backgroundColor: item.color || "#6875cf" }}
                  />
                  <span>{item.name}</span>
                  {pinned && <Pin size={11} />}
                  {project?.id === item.id ? (
                    <ChevronDown size={12} />
                  ) : (
                    <ChevronRight size={12} />
                  )}
                </NavLink>
                <Menu
                  items={[
                    {
                      label: pinned
                        ? t("取消置顶", "Unpin")
                        : t("置顶项目", "Pin project"),
                      onSelect: () => {
                        mutation.execute(() =>
                          preference.save((current) => ({
                            pinned: pinned
                              ? current.pinned.filter((id) => id !== item.id)
                              : [...new Set([...current.pinned, item.id])],
                          })),
                        );
                      },
                    },
                    ...(index &&
                    preference.value.pinned.includes(projects[index - 1].id) ===
                      pinned
                      ? [
                          {
                            label: t("上移", "Move up"),
                            onSelect: () => {
                              mutation.execute(() => moveProject(index, -1));
                            },
                          },
                        ]
                      : []),
                    ...(index < projects.length - 1 &&
                    preference.value.pinned.includes(projects[index + 1].id) ===
                      pinned
                      ? [
                          {
                            label: t("下移", "Move down"),
                            onSelect: () => {
                              mutation.execute(() => moveProject(index, 1));
                            },
                          },
                        ]
                      : []),
                    {
                      label: favorite
                        ? t("取消收藏", "Remove favorite")
                        : t("收藏项目", "Favorite project"),
                      onSelect: () => {
                        mutation.execute(() =>
                          favorite
                            ? api.delete(
                                `${workspacePath(workspace.id)}/favorites/${favorite.id}`,
                              )
                            : api.post(
                                `${workspacePath(workspace.id)}/favorites`,
                                {
                                  entity_type: "project",
                                  entity_id: item.id,
                                  position:
                                    Math.max(
                                      0,
                                      ...(favorites.data ?? []).map(
                                        (entry) => entry.position,
                                      ),
                                    ) + 1000,
                                },
                              ),
                        );
                      },
                    },
                  ]}
                />
              </div>
              {project?.id === item.id && (
                <div className="project-subnav">
                  {items
                    .filter(
                      (child) =>
                        (child.path !== "requirements" ||
                          requirementsEnabled(item)) &&
                        item.features?.[
                          child.path as keyof NonNullable<typeof item.features>
                        ] !== false,
                    )
                    .map((child) => (
                      <NavLink
                        key={child.path}
                        to={`${base}/projects/${item.id}/${child.path}`}
                        className={({ isActive }) =>
                          cn("nav-link", isActive && "active")
                        }
                      >
                        <child.icon size={14} />
                        <span>{t(child.zh, child.en)}</span>
                      </NavLink>
                    ))}
                </div>
              )}
            </div>
          );
        })}
        {loaded && !projects.length && workspace.role >= 15 && (
          <button
            className="sidebar-empty"
            onClick={() => appStore.setCreateProjectOpen(true)}
          >
            <Plus size={14} />
            {t("创建第一个项目", "Create your first project")}
          </button>
        )}
      </nav>
      <Modal
        open={groupsOpen}
        onOpenChange={setGroupsOpen}
        title={t("管理收藏分组", "Manage favorite groups")}
      >
        <div className="modal-body form-stack">
          {preference.value.groups.map((group, index) => (
            <div className="catalog-row" key={group.id}>
              <strong className="flex-spacer">{group.name}</strong>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => {
                  setEditingGroup(group.id);
                  setGroupName(group.name);
                }}
              >
                {t("重命名", "Rename")}
              </Button>
              {[-1, 1].map((direction) => (
                <Button
                  key={direction}
                  size="icon"
                  variant="ghost"
                  disabled={
                    index + direction < 0 ||
                    index + direction >= preference.value.groups.length
                  }
                  aria-label={
                    direction < 0
                      ? t("上移分组", "Move group up")
                      : t("下移分组", "Move group down")
                  }
                  onClick={() =>
                    mutation.execute(() => {
                      const next = [...preference.value.groups];
                      [next[index], next[index + direction]] = [
                        next[index + direction],
                        next[index],
                      ];
                      return preference.save({ groups: next });
                    })
                  }
                >
                  {direction < 0 ? (
                    <ArrowUp size={13} />
                  ) : (
                    <ArrowDown size={13} />
                  )}
                </Button>
              ))}
              <Button
                size="icon"
                variant="ghost"
                aria-label={t("删除分组", "Delete group")}
                onClick={() =>
                  mutation.execute(() =>
                    preference.save((current) => ({
                      groups: current.groups.filter(
                        (entry) => entry.id !== group.id,
                      ),
                      favorite_groups: Object.fromEntries(
                        Object.entries(current.favorite_groups).filter(
                          ([, id]) => id !== group.id,
                        ),
                      ),
                    })),
                  )
                }
              >
                <Trash2 size={13} />
              </Button>
            </div>
          ))}
          <form
            className="form-stack"
            onSubmit={(event) => {
              event.preventDefault();
              mutation.execute(async () => {
                const name = groupName.trim();
                if (!name) return;
                await preference.save((current) => ({
                  groups: editingGroup
                    ? current.groups.map((group) =>
                        group.id === editingGroup ? { ...group, name } : group,
                      )
                    : [...current.groups, { id: crypto.randomUUID(), name }],
                }));
                setGroupName("");
                setEditingGroup(null);
              });
            }}
          >
            <Field
              label={
                editingGroup
                  ? t("分组名称", "Group name")
                  : t("新分组", "New group")
              }
            >
              <Input
                value={groupName}
                onChange={(event) => setGroupName(event.target.value)}
                required
                maxLength={60}
              />
            </Field>
            <Button type="submit" busy={mutation.busy}>
              {editingGroup
                ? t("保存名称", "Save name")
                : t("添加分组", "Add group")}
            </Button>
          </form>
          <ErrorBox message={mutation.error} />
          {(favorites.data ?? []).map((item) => (
            <div className="catalog-row" key={item.id}>
              <span className="flex-spacer">{item.entity.name}</span>
              <Select
                aria-label={`${t("分组", "Group")} ${item.entity.name}`}
                value={preference.value.favorite_groups[item.id] ?? ""}
                onChange={(event) => {
                  const groupID = event.target.value;
                  mutation.execute(() =>
                    preference.save((current) => ({
                      favorite_groups: {
                        ...current.favorite_groups,
                        [item.id]: groupID,
                      },
                    })),
                  );
                }}
              >
                {groups.map((group) => (
                  <option key={group.id} value={group.id}>
                    {group.name}
                  </option>
                ))}
              </Select>
            </div>
          ))}
        </div>
      </Modal>
    </>
  );
});
