import { useState, type FormEvent } from "react";
import { observer } from "mobx-react-lite";
import { Link, useNavigate } from "react-router-dom";
import {
  Archive,
  ArrowUpRight,
  ChevronRight,
  FolderKanban,
  Globe2,
  LockKeyhole,
  Plus,
  Search,
} from "lucide-react";
import { appStore } from "../stores/app-store";
import {
  useDebouncedValue,
  useMutation,
  useRemote,
  useScope,
} from "../lib/hooks";
import { api, projectPath, workspacePath } from "../lib/api";
import {
  Badge,
  Button,
  EmptyState,
  ErrorBox,
  Field,
  Input,
  Menu,
  Modal,
  PageHeader,
  Select,
} from "../components/ui";

const projectColors = [
  "#6875cf",
  "#4a8a80",
  "#ac7450",
  "#ab648c",
  "#708847",
  "#5584a4",
  "#806cad",
  "#bd675f",
];

export const ProjectForm = observer(function ProjectForm({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { workspace } = useScope();
  const mutation = useMutation();
  const navigate = useNavigate();
  const [form, setForm] = useState({
    name: "",
    identifier: "",
    description: "",
    network: "public" as "public" | "private",
    color: projectColors[0],
  });
  const t = appStore.t;
  const identifier = useDebouncedValue(form.identifier);
  const availability = useRemote<{ identifier: string; available: boolean }>(
    open && identifier
      ? `${workspacePath(workspace.id)}/projects/availability?identifier=${encodeURIComponent(identifier)}`
      : null,
  );
  const identifierTaken =
    availability.data?.identifier === form.identifier &&
    !availability.data.available;
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const result = await mutation.execute(() =>
      appStore.createProject(workspace.id, form),
    );
    if (result) {
      onOpenChange(false);
      setForm({
        name: "",
        identifier: "",
        description: "",
        network: "public",
        color: projectColors[0],
      });
      navigate(`/w/${workspace.slug}/projects/${result.id}/issues`);
      appStore.notify(t("项目已创建", "Project created"));
    }
  };
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={t("创建项目", "Create a project")}
      description={t(
        "围绕一个共同目标组织工作。",
        "Organize your work around a shared goal.",
      )}
    >
      <form onSubmit={submit} className="form-stack modal-body">
        <Field label={t("项目名称", "Project name")}>
          <Input
            name="project_name"
            value={form.name}
            onChange={(event) =>
              setForm({
                ...form,
                name: event.target.value,
                identifier:
                  form.identifier ||
                  event.target.value
                    .replace(/[^a-zA-Z0-9]/g, "")
                    .slice(0, 5)
                    .toUpperCase(),
              })
            }
            required
            autoFocus
            placeholder={t("例如：新版产品体验", "e.g. Product experience")}
            maxLength={150}
          />
        </Field>
        <div className="form-row">
          <Field
            label={t("项目标识", "Identifier")}
            hint={
              identifierTaken
                ? t(
                    "此标识已被使用，请换一个。",
                    "This identifier is already in use.",
                  )
                : t(
                    "用于工作项编号，例如 APP-12。",
                    "Used in work item IDs, e.g. APP-12.",
                  )
            }
          >
            <Input
              name="identifier"
              value={form.identifier}
              onChange={(event) =>
                setForm({
                  ...form,
                  identifier: event.target.value
                    .toUpperCase()
                    .replace(/[^A-Z0-9]/g, ""),
                })
              }
              required
              pattern="[A-Z0-9]+"
              maxLength={10}
              placeholder="APP"
            />
          </Field>
          <Field label={t("可见范围", "Visibility")}>
            <Select
              name="network"
              value={form.network}
              onChange={(event) =>
                setForm({
                  ...form,
                  network: event.target.value as "public" | "private",
                })
              }
            >
              <option value="public">
                {t("工作区成员可见", "Workspace members")}
              </option>
              <option value="private">
                {t("仅项目成员", "Project members only")}
              </option>
            </Select>
          </Field>
        </div>
        <Field label={t("项目说明", "Description")}>
          <textarea
            className="input textarea"
            name="description"
            value={form.description}
            onChange={(event) =>
              setForm({ ...form, description: event.target.value })
            }
            placeholder={t(
              "这个项目希望达成什么？",
              "What would you like to achieve?",
            )}
            rows={3}
          />
        </Field>
        <Field label={t("项目颜色", "Project color")}>
          <div className="color-picker">
            {projectColors.map((color) => (
              <button
                key={color}
                type="button"
                aria-label={color}
                aria-pressed={form.color === color}
                className={form.color === color ? "selected" : ""}
                style={{ backgroundColor: color }}
                onClick={() => setForm({ ...form, color })}
              />
            ))}
          </div>
        </Field>
        <ErrorBox message={mutation.error} />
        <div className="modal-footer">
          <Button type="button" onClick={() => onOpenChange(false)}>
            {t("取消", "Cancel")}
          </Button>
          <Button
            type="submit"
            variant="primary"
            busy={mutation.busy}
            disabled={identifierTaken}
          >
            {t("创建项目", "Create project")}
          </Button>
        </div>
      </form>
    </Modal>
  );
});

export const ProjectsPage = observer(function ProjectsPage() {
  const { workspace } = useScope();
  const [query, setQuery] = useState("");
  const [showArchived, setShowArchived] = useState(false);
  const mutation = useMutation();
  const t = appStore.t;
  const projects = appStore
    .workspaceProjects(workspace.id)
    .filter(
      (project) =>
        !!project.archived_at === showArchived &&
        project.name.toLowerCase().includes(query.toLowerCase()),
    );

  return (
    <div className="page-scroll">
      <PageHeader
        title={t("项目", "Projects")}
        description={t(
          "每个目标，都有一个清晰的起点。",
          "A clear starting point for every goal.",
        )}
        actions={
          <Button
            variant="primary"
            onClick={() => appStore.setCreateProjectOpen(true)}
          >
            <Plus size={15} />
            {t("创建项目", "Create project")}
          </Button>
        }
      >
        <div className="page-tabs">
          <button
            className={!showArchived ? "active" : ""}
            onClick={() => setShowArchived(false)}
          >
            {t("所有项目", "All projects")}{" "}
            <Badge>
              {
                appStore
                  .workspaceProjects(workspace.id)
                  .filter((project) => !project.archived_at).length
              }
            </Badge>
          </button>
          <button
            className={showArchived ? "active" : ""}
            onClick={() => setShowArchived(true)}
          >
            {t("已归档", "Archived")}
          </button>
          <div className="tabs-spacer" />
          <div className="search-input compact">
            <Search size={14} />
            <Input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder={t("搜索项目…", "Search projects…")}
              aria-label={t("搜索项目", "Search projects")}
            />
          </div>
        </div>
      </PageHeader>
      <div className="page-body">
        <ErrorBox message={mutation.error} />
        {projects.length === 0 ? (
          <EmptyState
            icon={<FolderKanban size={30} />}
            title={
              query
                ? t("没有找到匹配的项目", "No matching projects")
                : showArchived
                  ? t("这里还没有归档项目", "No archived projects")
                  : t("开启团队的第一个项目", "Start your first project")
            }
            description={t(
              "用项目划分目标，让团队成员知道下一步向哪里前进。",
              "Give your team a shared destination and a clear place to begin.",
            )}
            action={
              !query && !showArchived ? (
                <Button
                  variant="primary"
                  onClick={() => appStore.setCreateProjectOpen(true)}
                >
                  <Plus size={15} />
                  {t("创建项目", "Create project")}
                </Button>
              ) : undefined
            }
          />
        ) : (
          <div className="project-grid">
            {projects.map((project) => (
              <article className="project-card" key={project.id}>
                {project.cover_image_url && (
                  <img
                    className="project-card-cover"
                    src={project.cover_image_url}
                    alt=""
                  />
                )}
                <div className="project-card-top">
                  <span
                    className="project-tile"
                    style={{
                      backgroundColor: `${project.color || "#6875cf"}15`,
                      color: project.color || "#6875cf",
                    }}
                  >
                    <FolderKanban size={21} />
                  </span>
                  <Menu
                    items={[
                      {
                        label: t("项目设置", "Project settings"),
                        onSelect: () => {
                          window.location.assign(
                            `/w/${workspace.slug}/projects/${project.id}/settings`,
                          );
                        },
                      },
                      {
                        label: showArchived
                          ? t("恢复项目", "Restore project")
                          : t("归档项目", "Archive project"),
                        icon: <Archive size={14} />,
                        onSelect: () => {
                          mutation.execute(async () => {
                            await api.patch(
                              projectPath(workspace.id, project.id),
                              {
                                archived_at: showArchived
                                  ? null
                                  : new Date().toISOString(),
                              },
                            );
                            await appStore.loadProjects(workspace.id);
                          });
                        },
                      },
                    ]}
                  />
                </div>
                <Link
                  to={`/w/${workspace.slug}/projects/${project.id}/issues`}
                  className="project-card-title"
                >
                  {project.name}
                  <ArrowUpRight size={15} />
                </Link>
                <p>
                  {project.description ||
                    t(
                      "添加项目说明，帮助团队了解目标与背景。",
                      "Add a description to give your team context.",
                    )}
                </p>
                <div className="project-card-footer">
                  <Badge>{project.identifier}</Badge>
                  {project.is_member === false &&
                    project.network === "public" && (
                      <Button
                        size="sm"
                        busy={mutation.busy}
                        onClick={() =>
                          mutation.execute(
                            async () => {
                              await api.post(
                                `${projectPath(workspace.id, project.id)}/join`,
                              );
                              await appStore.loadProjects(workspace.id);
                            },
                            t("已加入项目", "Joined project"),
                          )
                        }
                      >
                        {t("加入项目", "Join project")}
                      </Button>
                    )}
                  <span>
                    {project.network === "private" ? (
                      <LockKeyhole size={13} />
                    ) : (
                      <Globe2 size={13} />
                    )}
                    {project.network === "private"
                      ? t("私有", "Private")
                      : t("工作区", "Workspace")}
                  </span>
                  <Link
                    to={`/w/${workspace.slug}/projects/${project.id}/issues`}
                    aria-label={t("进入项目", "Open project")}
                  >
                    <ChevronRight size={16} />
                  </Link>
                </div>
              </article>
            ))}
          </div>
        )}
      </div>
    </div>
  );
});
