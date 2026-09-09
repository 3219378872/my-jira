import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { observer } from "mobx-react-lite";
import { Link, NavLink, useLocation, useNavigate } from "react-router-dom";
import {
  Archive,
  Bell,
  Circle,
  Copy,
  Download,
  ExternalLink,
  FileDown,
  Globe2,
  KeyRound,
  Link2,
  LogOut,
  Mail,
  Palette,
  Plus,
  Search,
  Send,
  Settings,
  Shield,
  Tags,
  Trash2,
  UserRound,
  Users,
  Upload,
  Webhook,
} from "lucide-react";
import { appStore } from "../stores/app-store";
import {
  api,
  errorMessage,
  projectPath,
  request,
  workspacePath,
} from "../lib/api";
import { useMutation, useRemote, useScope } from "../lib/hooks";
import { dateTime, memberName } from "../lib/utils";
import {
  Avatar,
  Badge,
  Button,
  Confirm,
  ErrorBox,
  Field,
  Input,
  Loading,
  Menu,
  Modal,
  MultiSelect,
  Select,
  StateIcon,
} from "../components/ui";
import type {
  Label,
  Member,
  Project,
  Role,
  State,
  StateGroup,
  Workspace,
} from "../types";
import { AutomationSettings, EstimateSettings } from "./estimate-settings";

const stateGroupLabels: Record<StateGroup, [string, string]> = {
  backlog: ["待整理", "Backlog"],
  unstarted: ["待开始", "Unstarted"],
  started: ["进行中", "Started"],
  completed: ["已完成", "Completed"],
  cancelled: ["已取消", "Cancelled"],
};

export const SettingsPage = observer(function SettingsPage() {
  const { workspace, project } = useScope();
  const location = useLocation();
  const segment =
    location.pathname.split("/settings")[1]?.split("/").filter(Boolean)[0] ??
    "general";
  const route = `/w/${workspace.slug}${project ? `/projects/${project.id}` : ""}/settings`;
  const t = appStore.t;
  const common = [
    { id: "general", zh: "常规", en: "General", icon: Settings },
    { id: "members", zh: "成员", en: "Members", icon: Users },
  ];
  const items = project
    ? [
        ...common,
        { id: "states", zh: "工作流状态", en: "Workflow states", icon: Circle },
        { id: "labels", zh: "标签", en: "Labels", icon: Tags },
        { id: "estimates", zh: "估算方案", en: "Estimates", icon: Circle },
        { id: "automation", zh: "自动化", en: "Automation", icon: Settings },
        { id: "site", zh: "公开分享", en: "Public sharing", icon: Globe2 },
        { id: "exports", zh: "导出", en: "Exports", icon: FileDown },
      ]
    : [
        { id: "profile", zh: "个人资料", en: "Profile", icon: UserRound },
        { id: "security", zh: "账户安全", en: "Security", icon: Shield },
        { id: "appearance", zh: "外观与语言", en: "Appearance", icon: Palette },
        {
          id: "notifications",
          zh: "通知偏好",
          en: "Notifications",
          icon: Bell,
        },
        ...common,
        { id: "labels", zh: "工作区标签", en: "Workspace labels", icon: Tags },
        { id: "api-tokens", zh: "API 令牌", en: "API tokens", icon: KeyRound },
        { id: "webhooks", zh: "Webhooks", en: "Webhooks", icon: Webhook },
        { id: "exports", zh: "数据导出", en: "Data exports", icon: Download },
      ];
  return (
    <div className="settings-layout">
      <nav className="settings-nav">
        <h3>
          {project ? t("项目设置", "Project settings") : t("设置", "Settings")}
        </h3>
        {items.map((item) => (
          <NavLink
            key={item.id}
            to={item.id === "general" ? route : `${route}/${item.id}`}
            end={item.id === "general"}
            className={({ isActive }) => (isActive ? "active" : "")}
          >
            <item.icon size={14} />
            {t(item.zh, item.en)}
          </NavLink>
        ))}
      </nav>
      <div className="settings-content">
        {segment === "general" ? (
          <GeneralSettings />
        ) : segment === "profile" ? (
          <ProfileSettings />
        ) : segment === "security" ? (
          <SecuritySettings />
        ) : segment === "appearance" ? (
          <AppearanceSettings />
        ) : segment === "notifications" ? (
          <NotificationSettings />
        ) : segment === "members" ? (
          <MembersSettings />
        ) : segment === "states" || segment === "labels" ? (
          <CatalogSettings kind={segment} />
        ) : segment === "api-tokens" ? (
          <TokenSettings />
        ) : segment === "webhooks" ? (
          <WebhookSettings />
        ) : segment === "exports" ? (
          <ExportsSettings />
        ) : segment === "site" ? (
          <SiteSettings />
        ) : segment === "estimates" && project ? (
          <EstimateSettings />
        ) : segment === "automation" && project ? (
          <AutomationSettings />
        ) : (
          <GeneralSettings />
        )}
      </div>
    </div>
  );
});

function Section({
  title,
  description,
  children,
  wide = false,
}: {
  title: string;
  description: string;
  children: ReactNode;
  wide?: boolean;
}) {
  return (
    <section className={`settings-section${wide ? " settings-wide" : ""}`}>
      <h1>{title}</h1>
      <p className="page-description">{description}</p>
      {children}
    </section>
  );
}

function Panel({
  title,
  children,
  danger = false,
}: {
  title: string;
  children: ReactNode;
  danger?: boolean;
}) {
  return (
    <section className={`settings-panel${danger ? " danger-zone" : ""}`}>
      <div className="settings-panel-heading">
        <h2>{title}</h2>
      </div>
      <div className="settings-panel-body">{children}</div>
    </section>
  );
}

export function ToggleSetting({
  label,
  description,
  checked,
  onChange,
}: {
  label: string;
  description?: string;
  checked: boolean;
  onChange: (value: boolean) => void;
}) {
  return (
    <label className="settings-toggle-row">
      <div>
        <strong>{label}</strong>
        {description && <p>{description}</p>}
      </div>
      <input
        type="checkbox"
        role="switch"
        checked={checked}
        onChange={(event) => onChange(event.target.checked)}
      />
    </label>
  );
}

const GeneralSettings = observer(function GeneralSettings() {
  const { workspace, project } = useScope();
  const resource = project ?? workspace;
  const [form, setForm] = useState({
    name: resource.name,
    description: resource.description,
    slug: workspace.slug,
    identifier: project?.identifier ?? "",
    network: project?.network ?? "public",
    guest_can_view_all: project?.guest_can_view_all ?? false,
    timezone: workspace.timezone,
    cover_image_url: project?.cover_image_url ?? "",
    features: {
      cycles: true,
      modules: true,
      pages: true,
      views: true,
      intake: true,
      ...project?.features,
    },
  });
  const [leaveOpen, setLeaveOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleteName, setDeleteName] = useState("");
  const mutation = useMutation();
  const navigate = useNavigate();
  const t = appStore.t;
  useEffect(() => {
    setForm({
      name: resource.name,
      description: resource.description,
      slug: workspace.slug,
      identifier: project?.identifier ?? "",
      network: project?.network ?? "public",
      guest_can_view_all: project?.guest_can_view_all ?? false,
      timezone: workspace.timezone,
      cover_image_url: project?.cover_image_url ?? "",
      features: {
        cycles: true,
        modules: true,
        pages: true,
        views: true,
        intake: true,
        ...project?.features,
      },
    });
  }, [resource.id]);
  const base = project
    ? projectPath(workspace.id, project.id)
    : workspacePath(workspace.id);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    await mutation.execute(
      async () => {
        await api.patch<Workspace | Project>(
          base,
          project
            ? {
                name: form.name,
                description: form.description,
                identifier: form.identifier,
                network: form.network,
                guest_can_view_all: form.guest_can_view_all,
                cover_image_url: form.cover_image_url || null,
                features: form.features,
              }
            : {
                name: form.name,
                description: form.description,
                slug: form.slug,
                timezone: form.timezone,
              },
        );
        await appStore.loadWorkspaces();
        await appStore.loadProjects(workspace.id);
        if (!project && workspace.slug !== form.slug)
          navigate(`/w/${form.slug}/settings`);
      },
      t("设置已保存", "Settings saved"),
    );
  };
  return (
    <Section
      title={
        project
          ? t("项目设置", "Project settings")
          : t("工作区设置", "Workspace settings")
      }
      description={t(
        "完善基本信息，让团队更容易理解这个空间。",
        "Give your team the context they need about this space.",
      )}
    >
      <form onSubmit={submit} className="form-stack">
        <Field label={t("名称", "Name")}>
          <Input
            value={form.name}
            onChange={(event) => setForm({ ...form, name: event.target.value })}
            required
            maxLength={150}
          />
        </Field>
        {project ? (
          <div className="form-row">
            <Field label={t("项目标识", "Identifier")}>
              <Input
                value={form.identifier}
                onChange={(event) =>
                  setForm({
                    ...form,
                    identifier: event.target.value.toUpperCase(),
                  })
                }
                required
                pattern="[A-Z0-9]+"
                maxLength={10}
              />
            </Field>
            <Field label={t("可见范围", "Visibility")}>
              <Select
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
                  {t("仅项目成员可见", "Project members only")}
                </option>
              </Select>
            </Field>
          </div>
        ) : (
          <div className="form-row">
            <Field label={t("工作区地址", "Workspace address")}>
              <Input
                value={form.slug}
                onChange={(event) =>
                  setForm({ ...form, slug: event.target.value })
                }
                required
                pattern="[a-z0-9]+(?:-[a-z0-9]+)*"
              />
            </Field>
            <Field label={t("时区", "Timezone")}>
              <Input
                value={form.timezone}
                onChange={(event) =>
                  setForm({ ...form, timezone: event.target.value })
                }
                required
              />
            </Field>
          </div>
        )}
        <Field label={t("说明", "Description")}>
          <textarea
            className="input textarea"
            value={form.description}
            onChange={(event) =>
              setForm({ ...form, description: event.target.value })
            }
            rows={4}
          />
        </Field>
        {project && (
          <>
            <Field label={t("项目封面", "Project cover")}>
              <Input
                type="url"
                value={form.cover_image_url}
                onChange={(event) =>
                  setForm({ ...form, cover_image_url: event.target.value })
                }
                placeholder="https://…"
              />
              {form.cover_image_url && (
                <img
                  className="settings-cover-preview"
                  src={form.cover_image_url}
                  alt={t("项目封面预览", "Project cover preview")}
                />
              )}
              <label className="button button-secondary button-md upload-button">
                <Upload size={14} />
                {t("上传封面", "Upload cover")}
                <input
                  type="file"
                  accept="image/*"
                  onChange={(event) => {
                    const file = event.target.files?.[0];
                    if (!file) return;
                    const data = new FormData();
                    data.append("file", file);
                    mutation.execute(async () => {
                      const result = await request<{ download_url: string }>(
                        `${base}/assets`,
                        { method: "POST", body: data },
                      );
                      setForm({
                        ...form,
                        cover_image_url: new URL(
                          `${result.data.download_url}?inline=true`,
                          window.location.origin,
                        ).href,
                      });
                    });
                  }}
                />
              </label>
            </Field>
            <div className="settings-panel">
              <div className="settings-panel-heading">
                <h2>{t("项目功能", "Project features")}</h2>
              </div>
              <div className="settings-panel-body">
                <ToggleSetting
                  label={t(
                    "来宾可以查看项目全部工作",
                    "Guests can view all project work",
                  )}
                  checked={form.guest_can_view_all}
                  onChange={(guest_can_view_all) =>
                    setForm({ ...form, guest_can_view_all })
                  }
                />
                <p className="settings-note">
                  {t(
                    "允许项目来宾阅读其他成员的工作项和团队文档。私有文档仍仅所有者可见，来宾的写入权限不会改变。",
                    "Let project guests read other members' work items and team documents. Private documents remain owner-only and guest editing permissions stay the same.",
                  )}
                </p>
                {(
                  [
                    { key: "cycles", zh: "迭代周期", en: "Cycles" },
                    { key: "modules", zh: "功能模块", en: "Modules" },
                    { key: "pages", zh: "知识文档", en: "Documents" },
                    { key: "views", zh: "项目视图", en: "Views" },
                    { key: "intake", zh: "需求收集", en: "Intake" },
                  ] as const
                ).map((feature) => (
                  <ToggleSetting
                    key={feature.key}
                    label={t(feature.zh, feature.en)}
                    checked={form.features[feature.key]}
                    onChange={(enabled) =>
                      setForm({
                        ...form,
                        features: { ...form.features, [feature.key]: enabled },
                      })
                    }
                  />
                ))}
              </div>
            </div>
          </>
        )}
        <ErrorBox message={mutation.error} />
        <div className="settings-actions">
          <Button type="submit" variant="primary" busy={mutation.busy}>
            {t("保存更改", "Save changes")}
          </Button>
        </div>
      </form>
      {project && (
        <Panel title={t("项目归档", "Project archive")}>
          <p className="settings-note">
            {t(
              "归档后，项目会移入已归档列表。你可以随时恢复。",
              "Move this project to the archived list. You can restore it later.",
            )}
          </p>
          <Button
            onClick={() => {
              mutation.execute(async () => {
                await api.patch(base, {
                  archived_at: project.archived_at
                    ? null
                    : new Date().toISOString(),
                });
                await appStore.loadProjects(workspace.id);
              });
            }}
          >
            <Archive size={14} />
            {project.archived_at
              ? t("恢复项目", "Restore project")
              : t("归档项目", "Archive project")}
          </Button>
        </Panel>
      )}
      {(!project || project.is_member !== false) && (
        <Panel
          title={
            project
              ? t("退出项目", "Leave project")
              : t("退出工作区", "Leave workspace")
          }
        >
          <p className="settings-note">
            {t(
              "退出后将移除你的成员身份，重新加入需要可用的访问权限。",
              "Leaving removes your membership. Access is required to join again.",
            )}
          </p>
          <Button onClick={() => setLeaveOpen(true)}>
            <LogOut size={14} />
            {t("退出", "Leave")}
          </Button>
          <Confirm
            open={leaveOpen}
            onOpenChange={setLeaveOpen}
            title={
              project
                ? t("退出此项目？", "Leave this project?")
                : t("退出此工作区？", "Leave this workspace?")
            }
            description={t(
              "你创建的工作内容会保留在团队中。",
              "The work you created will stay with your team.",
            )}
            busy={mutation.busy}
            onConfirm={() =>
              mutation.execute(async () => {
                await api.post(`${base}/leave`);
                await appStore.loadWorkspaces();
                if (project) await appStore.loadProjects(workspace.id);
                await appStore.loadLastVisited();
                setLeaveOpen(false);
                navigate(project ? `/w/${workspace.slug}/projects` : "/");
              })
            }
          />
        </Panel>
      )}
      <Panel title={t("删除", "Delete")} danger>
        <p className="settings-note">
          {project
            ? t(
                "删除此项目及关联数据。请先导出需要保留的信息。",
                "Delete this project and its associated data. Export anything you need first.",
              )
            : t(
                "删除整个工作区及其项目。此操作会影响工作区内所有成员。",
                "Delete this workspace and its projects. This affects every workspace member.",
              )}
        </p>
        <Button variant="danger" onClick={() => setDeleteOpen(true)}>
          <Trash2 size={14} />
          {project
            ? t("删除项目", "Delete project")
            : t("删除工作区", "Delete workspace")}
        </Button>
      </Panel>
      <Modal
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title={t("确认删除", "Confirm deletion")}
        description={t(
          `输入「${resource.name}」以确认删除。`,
          `Type “${resource.name}” to confirm deletion.`,
        )}
      >
        <div className="form-stack modal-body">
          <Input
            value={deleteName}
            onChange={(event) => setDeleteName(event.target.value)}
            aria-label={t("输入名称确认", "Type name to confirm")}
          />
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button
              variant="danger"
              disabled={deleteName !== resource.name}
              busy={mutation.busy}
              onClick={() => {
                mutation.execute(async () => {
                  await api.delete(base);
                  await appStore.loadWorkspaces();
                  if (project) await appStore.loadProjects(workspace.id);
                  setDeleteOpen(false);
                  navigate(project ? `/w/${workspace.slug}/projects` : "/");
                });
              }}
            >
              {t("删除", "Delete")}
            </Button>
          </div>
        </div>
      </Modal>
    </Section>
  );
});

const ProfileSettings = observer(function ProfileSettings() {
  const user = appStore.user!;
  const [form, setForm] = useState({
    display_name: user.display_name,
    first_name: user.first_name,
    last_name: user.last_name,
    timezone: user.timezone,
    avatar_url: user.avatar_url,
  });
  const mutation = useMutation();
  const t = appStore.t;
  return (
    <Section
      title={t("个人资料", "Your profile")}
      description={t("让团队更好地认识你。", "Help your team get to know you.")}
    >
      <div className="inline-property" style={{ marginBottom: 26 }}>
        <Avatar name={form.display_name} src={form.avatar_url} size="lg" />
        <div>
          <strong>{user.display_name}</strong>
          <p className="settings-note">{user.email}</p>
        </div>
      </div>
      <form
        className="form-stack"
        onSubmit={(event) => {
          event.preventDefault();
          mutation.execute(
            () => appStore.updateProfile(form),
            t("个人资料已更新", "Profile updated"),
          );
        }}
      >
        <Field label={t("显示名称", "Display name")}>
          <Input
            value={form.display_name}
            onChange={(event) =>
              setForm({ ...form, display_name: event.target.value })
            }
            required
          />
        </Field>
        <div className="form-row">
          <Field label={t("名", "First name")}>
            <Input
              value={form.first_name}
              onChange={(event) =>
                setForm({ ...form, first_name: event.target.value })
              }
            />
          </Field>
          <Field label={t("姓", "Last name")}>
            <Input
              value={form.last_name}
              onChange={(event) =>
                setForm({ ...form, last_name: event.target.value })
              }
            />
          </Field>
        </div>
        <Field label={t("头像地址", "Avatar URL")}>
          <Input
            type="url"
            value={form.avatar_url}
            onChange={(event) =>
              setForm({ ...form, avatar_url: event.target.value })
            }
            placeholder="https://…"
          />
          <label className="button button-secondary button-md upload-button">
            <Upload size={14} />
            {t("上传头像", "Upload avatar")}
            <input
              type="file"
              accept="image/*"
              onChange={(event) => {
                const file = event.target.files?.[0];
                if (!file) return;
                const data = new FormData();
                data.append("file", file);
                mutation.execute(async () => {
                  const result = await request<{ download_url: string }>(
                    "/auth/assets",
                    { method: "POST", body: data },
                  );
                  setForm({
                    ...form,
                    avatar_url: new URL(
                      `${result.data.download_url}?inline=true`,
                      window.location.origin,
                    ).href,
                  });
                });
              }}
            />
          </label>
        </Field>
        <Field label={t("时区", "Timezone")}>
          <Select
            value={form.timezone}
            onChange={(event) =>
              setForm({ ...form, timezone: event.target.value })
            }
          >
            {[
              ...new Set([
                form.timezone,
                "UTC",
                "Asia/Shanghai",
                "Asia/Tokyo",
                "Asia/Singapore",
                "Europe/London",
                "Europe/Berlin",
                "America/New_York",
                "America/Los_Angeles",
              ]),
            ].map((zone) => (
              <option key={zone} value={zone}>
                {zone}
              </option>
            ))}
          </Select>
        </Field>
        <ErrorBox message={mutation.error} />
        <div className="settings-actions">
          <Button type="submit" variant="primary" busy={mutation.busy}>
            {t("保存资料", "Save profile")}
          </Button>
        </div>
      </form>
    </Section>
  );
});

const SecuritySettings = observer(function SecuritySettings() {
  const [password, setPassword] = useState({
    current_password: "",
    new_password: "",
  });
  const [email, setEmail] = useState("");
  const sessions = useRemote<
    {
      id: string;
      user_agent: string;
      ip_address: string;
      created_at: string;
      expires_at: string;
      is_current?: boolean;
    }[]
  >("/auth/sessions");
  const accounts =
    useRemote<{ id: string; provider: string; email: string }[]>(
      "/auth/accounts",
    );
  const mutation = useMutation();
  const t = appStore.t;
  return (
    <Section
      title={t("账户安全", "Account security")}
      description={t(
        "管理密码、电子邮箱和已登录设备。",
        "Manage your password, email address, and signed-in devices.",
      )}
    >
      <Panel title={t("修改密码", "Change password")}>
        <form
          className="form-stack"
          onSubmit={(event) => {
            event.preventDefault();
            mutation.execute(
              async () => {
                await api.post("/auth/password", password);
                setPassword({ current_password: "", new_password: "" });
                sessions.refresh();
              },
              t(
                "密码已更新，其他设备会话已撤销",
                "Password updated. Other sessions have been revoked.",
              ),
            );
          }}
        >
          {appStore.user?.password_set !== false && (
            <Field label={t("当前密码", "Current password")}>
              <Input
                type="password"
                autoComplete="current-password"
                value={password.current_password}
                onChange={(event) =>
                  setPassword({
                    ...password,
                    current_password: event.target.value,
                  })
                }
                required
              />
            </Field>
          )}
          <Field label={t("新密码", "New password")}>
            <Input
              type="password"
              autoComplete="new-password"
              minLength={12}
              value={password.new_password}
              onChange={(event) =>
                setPassword({ ...password, new_password: event.target.value })
              }
              required
            />
          </Field>
          <Button variant="primary" type="submit" busy={mutation.busy}>
            {t("更新密码", "Update password")}
          </Button>
        </form>
      </Panel>
      <Panel title={t("邮箱与验证", "Email and verification")}>
        <p className="settings-note">{appStore.user!.email}</p>
        <Button
          onClick={() => {
            mutation.execute(
              () => api.post("/auth/email-verification/request"),
              t("验证邮件已发送", "Verification email sent"),
            );
          }}
        >
          <Mail size={14} />
          {t("发送验证邮件", "Send verification email")}
        </Button>
        <form
          className="form-stack"
          style={{ marginTop: 22 }}
          onSubmit={(event) => {
            event.preventDefault();
            mutation.execute(
              () => api.post("/auth/email-change/request", { email }),
              t(
                "已向新邮箱发送确认邮件",
                "Confirmation sent to your new email address",
              ),
            );
          }}
        >
          <Field label={t("更换邮箱", "Change email")}>
            <Input
              type="email"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
              required
              placeholder="new@company.com"
            />
          </Field>
          <Button type="submit" busy={mutation.busy}>
            {t("发送确认邮件", "Send confirmation")}
          </Button>
        </form>
      </Panel>
      <Panel title={t("登录设备", "Signed-in devices")}>
        <ErrorBox message={sessions.error} />
        {sessions.loading ? (
          <Loading />
        ) : (
          sessions.data?.map((session) => (
            <div className="catalog-row" key={session.id}>
              <Shield size={16} />
              <div className="flex-spacer">
                <strong>
                  {session.user_agent || t("未知设备", "Unknown device")}
                </strong>
                <p className="settings-note">
                  {session.ip_address} ·{" "}
                  {dateTime(session.created_at, appStore.locale)}
                </p>
              </div>
              {session.is_current ? (
                <Badge>{t("当前设备", "This device")}</Badge>
              ) : (
                <Button
                  size="sm"
                  onClick={() => {
                    mutation.execute(async () => {
                      await api.delete(`/auth/sessions/${session.id}`);
                      sessions.refresh();
                    });
                  }}
                >
                  {t("退出", "Revoke")}
                </Button>
              )}
            </div>
          ))
        )}
      </Panel>
      <Panel title={t("关联账户", "Connected accounts")}>
        <ErrorBox message={accounts.error} />
        {accounts.data?.length ? (
          accounts.data.map((account) => (
            <div className="catalog-row" key={account.id}>
              <Link2 size={16} />
              <strong>{account.provider}</strong>
              <span className="text-muted">{account.email}</span>
              <span className="flex-spacer" />
              <Button
                size="sm"
                onClick={() => {
                  mutation.execute(async () => {
                    await api.delete(`/auth/accounts/${account.id}`);
                    accounts.refresh();
                  });
                }}
              >
                {t("解除关联", "Disconnect")}
              </Button>
            </div>
          ))
        ) : (
          <p className="settings-note">
            {t(
              "还没有关联第三方登录账户。",
              "No third-party accounts are connected.",
            )}
          </p>
        )}
      </Panel>
      <ErrorBox message={mutation.error} />
    </Section>
  );
});

const AppearanceSettings = observer(function AppearanceSettings() {
  const t = appStore.t;
  return (
    <Section
      title={t("外观与语言", "Appearance and language")}
      description={t(
        "选择舒适的工作环境，这些偏好会同步到你的账户。",
        "Choose a comfortable workspace. These preferences sync to your account.",
      )}
    >
      <div className="form-stack">
        <Field label={t("颜色主题", "Color theme")}>
          <Select
            value={appStore.theme}
            onChange={(event) =>
              appStore.setTheme(
                event.target.value as "light" | "dark" | "system",
              )
            }
          >
            <option value="light">{t("浅色", "Light")}</option>
            <option value="dark">{t("深色", "Dark")}</option>
            <option value="system">{t("跟随系统", "System")}</option>
          </Select>
        </Field>
        <Field label={t("界面语言", "Interface language")}>
          <Select
            value={appStore.locale}
            onChange={(event) =>
              appStore.setLocale(event.target.value as "zh-CN" | "en")
            }
          >
            <option value="zh-CN">简体中文</option>
            <option value="en">English</option>
          </Select>
        </Field>
        <ToggleSetting
          label={t("收起侧栏", "Collapse sidebar")}
          description={t(
            "为项目内容留出更多空间。",
            "Make more room for your project content.",
          )}
          checked={appStore.sidebarCollapsed}
          onChange={() => appStore.toggleSidebar()}
        />
      </div>
    </Section>
  );
});

const NotificationSettings = observer(function NotificationSettings() {
  const { workspace } = useScope();
  const base = `${workspacePath(workspace.id)}/preferences/notifications`;
  type Preferences = Record<
    "in_app" | "email" | "mentions" | "assigned" | "subscribed" | "created",
    boolean
  >;
  const defaults: Preferences = {
    in_app: true,
    email: true,
    mentions: true,
    assigned: true,
    subscribed: true,
    created: true,
  };
  const current = useRemote<{ value: Partial<Preferences> }>(base);
  const [form, setForm] = useState(defaults);
  const mutation = useMutation();
  const t = appStore.t;
  useEffect(() => {
    if (current.data) setForm({ ...defaults, ...current.data.value });
  }, [current.data]);
  return (
    <Section
      title={t("通知偏好", "Notification preferences")}
      description={t(
        "选择这个工作区中希望收到的更新，以及接收方式。",
        "Choose the updates you want from this workspace and how to receive them.",
      )}
    >
      <ErrorBox message={current.error || mutation.error} />
      {current.loading ? (
        <Loading />
      ) : (
        <form
          className="form-stack"
          onSubmit={(event) => {
            event.preventDefault();
            mutation.execute(
              async () => {
                const result = await api.patch<{ value: Partial<Preferences> }>(
                  base,
                  { value: form },
                );
                current.setResult(result);
              },
              t("通知偏好已保存", "Notification preferences saved"),
            );
          }}
        >
          {(
            [
              {
                key: "in_app",
                zh: "在收件箱接收通知",
                en: "Receive notifications in the inbox",
              },
              {
                key: "email",
                zh: "通过邮件接收通知",
                en: "Receive email notifications",
              },
              { key: "mentions", zh: "有人提及我", en: "Someone mentions me" },
              {
                key: "assigned",
                zh: "我负责的工作项发生变化",
                en: "Changes to work assigned to me",
              },
              {
                key: "subscribed",
                zh: "我订阅的工作项发生变化",
                en: "Changes to work I follow",
              },
              {
                key: "created",
                zh: "我创建的工作项发生变化",
                en: "Changes to work I created",
              },
            ] as const
          ).map((setting) => (
            <ToggleSetting
              key={setting.key}
              label={t(setting.zh, setting.en)}
              checked={form[setting.key]}
              onChange={(value) => setForm({ ...form, [setting.key]: value })}
            />
          ))}
          <div className="settings-actions">
            <Button type="submit" variant="primary" busy={mutation.busy}>
              {t("保存偏好", "Save preferences")}
            </Button>
          </div>
        </form>
      )}
    </Section>
  );
});

const MembersSettings = observer(function MembersSettings() {
  const { workspace, project } = useScope();
  const base = project
    ? projectPath(workspace.id, project.id)
    : workspacePath(workspace.id);
  const members = useRemote<Member[]>(`${base}/members`);
  const workspaceMembers = useRemote<Member[]>(
    project ? `${workspacePath(workspace.id)}/members` : null,
  );
  const invitations = useRemote<
    {
      id: string;
      email: string;
      role: Role;
      expires_at: string;
      accepted_at: string | null;
      revoked_at: string | null;
    }[]
  >(!project ? `${base}/invitations` : null);
  const [open, setOpen] = useState(false);
  const [email, setEmail] = useState("");
  const [userId, setUserId] = useState("");
  const [role, setRole] = useState<Role>(15);
  const [inviteLink, setInviteLink] = useState("");
  const [query, setQuery] = useState("");
  const [removing, setRemoving] = useState<Member | null>(null);
  const mutation = useMutation();
  const t = appStore.t;
  const roles = [
    { value: 20, label: t("管理员", "Admin") },
    { value: 15, label: t("成员", "Member") },
    { value: 5, label: t("访客", "Guest") },
  ];

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    await mutation.execute(
      async () => {
        if (project)
          await api.post(`${base}/members`, { user_id: userId, role });
        else {
          const result = await api.post<{ token?: string }>(
            `${base}/invitations`,
            { email, role },
          );
          if (result.data.token)
            setInviteLink(
              `${window.location.origin}/invitations/${result.data.token}`,
            );
        }
        setOpen(false);
        setEmail("");
        setUserId("");
        members.refresh();
        invitations.refresh();
      },
      project
        ? t("成员已加入项目", "Member added to project")
        : t("邀请已创建", "Invitation created"),
    );
  };

  return (
    <Section
      title={t("成员管理", "Members")}
      description={t(
        "邀请伙伴加入，按职责分配合适的访问权限。",
        "Invite your team and give each person the access they need.",
      )}
      wide
    >
      <div className="settings-toolbar">
        <div className="search-input">
          <Search size={14} />
          <Input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={t("搜索成员…", "Search members…")}
          />
        </div>
        <Button variant="primary" onClick={() => setOpen(true)}>
          <Plus size={14} />
          {project
            ? t("添加成员", "Add member")
            : t("邀请成员", "Invite member")}
        </Button>
      </div>
      <ErrorBox message={members.error || mutation.error} />
      {members.loading ? (
        <Loading />
      ) : (
        <table className="data-table">
          <thead>
            <tr>
              <th>{t("成员", "Member")}</th>
              <th>{t("角色", "Role")}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {members.data
              ?.filter((member) =>
                `${memberName(member)} ${member.email || member.user?.email}`
                  .toLowerCase()
                  .includes(query.toLowerCase()),
              )
              .map((member) => (
                <tr key={member.id}>
                  <td>
                    <div className="user-cell">
                      <Avatar name={memberName(member)} size="sm" />
                      <div>
                        <strong>
                          <Link
                            to={`/w/${workspace.slug}/members/${member.user_id}`}
                          >
                            {memberName(member)}
                          </Link>
                          {member.user_id === appStore.user!.id && (
                            <Badge>{t("你", "You")}</Badge>
                          )}
                        </strong>
                        <small>{member.email || member.user?.email}</small>
                      </div>
                    </div>
                  </td>
                  <td>
                    <Select
                      value={member.role}
                      aria-label={t("成员角色", "Member role")}
                      onChange={(event) => {
                        mutation.execute(async () => {
                          await api.patch(`${base}/members/${member.id}`, {
                            role: Number(event.target.value),
                          });
                          members.refresh();
                        });
                      }}
                    >
                      {roles.map((item) => (
                        <option value={item.value} key={item.value}>
                          {item.label}
                        </option>
                      ))}
                    </Select>
                  </td>
                  <td>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={t("移除成员", "Remove member")}
                      onClick={() => setRemoving(member)}
                    >
                      <Trash2 size={14} />
                    </Button>
                  </td>
                </tr>
              ))}
          </tbody>
        </table>
      )}
      {!project && (
        <Panel title={t("待接受的邀请", "Pending invitations")}>
          <ErrorBox message={invitations.error} />
          {invitations.data
            ?.filter(
              (invitation) => !invitation.accepted_at && !invitation.revoked_at,
            )
            .map((invitation) => (
              <div className="catalog-row" key={invitation.id}>
                <Mail size={14} />
                <strong>{invitation.email}</strong>
                <Badge>
                  {roles.find((item) => item.value === invitation.role)?.label}
                </Badge>
                <Button
                  size="sm"
                  onClick={() => {
                    mutation.execute(async () => {
                      await api.delete(`${base}/invitations/${invitation.id}`);
                      invitations.refresh();
                    });
                  }}
                >
                  {t("撤销", "Revoke")}
                </Button>
              </div>
            ))}
        </Panel>
      )}
      <Modal
        open={open}
        onOpenChange={setOpen}
        title={
          project
            ? t("添加项目成员", "Add project member")
            : t("邀请成员", "Invite member")
        }
      >
        <form className="form-stack modal-body" onSubmit={submit}>
          {project ? (
            <Field label={t("工作区成员", "Workspace member")}>
              <Select
                value={userId}
                onChange={(event) => setUserId(event.target.value)}
                required
              >
                <option value="">{t("选择成员", "Select member")}</option>
                {workspaceMembers.data
                  ?.filter(
                    (member) =>
                      !members.data?.some(
                        (existing) => existing.user_id === member.user_id,
                      ),
                  )
                  .map((member) => (
                    <option key={member.user_id} value={member.user_id}>
                      {memberName(member)} · {member.email}
                    </option>
                  ))}
              </Select>
            </Field>
          ) : (
            <Field label={t("邮箱", "Email")}>
              <Input
                type="email"
                value={email}
                onChange={(event) => setEmail(event.target.value)}
                required
                autoFocus
              />
            </Field>
          )}
          <Field label={t("角色", "Role")}>
            <Select
              value={role}
              onChange={(event) => setRole(Number(event.target.value) as Role)}
            >
              {roles.map((item) => (
                <option key={item.value} value={item.value}>
                  {item.label}
                </option>
              ))}
            </Select>
          </Field>
          <ErrorBox message={mutation.error || workspaceMembers.error} />
          <div className="modal-footer">
            <Button type="submit" variant="primary" busy={mutation.busy}>
              {project
                ? t("添加成员", "Add member")
                : t("创建邀请", "Create invitation")}
            </Button>
          </div>
        </form>
      </Modal>
      <Modal
        open={!!inviteLink}
        onOpenChange={(value) => {
          if (!value) setInviteLink("");
        }}
        title={t("邀请已创建", "Invitation created")}
        description={t(
          "复制此链接发给受邀成员。只有对应邮箱的账户可以接受。",
          "Share this link with your invitee. Only the matching email account can accept it.",
        )}
      >
        <div className="modal-body form-stack">
          <div className="secret-result">{inviteLink}</div>
          <Button
            onClick={() =>
              navigator.clipboard
                .writeText(inviteLink)
                .then(() =>
                  appStore.notify(
                    t("邀请链接已复制", "Invitation link copied"),
                  ),
                )
                .catch((cause) => appStore.notify(errorMessage(cause), "error"))
            }
          >
            <Copy size={14} />
            {t("复制链接", "Copy link")}
          </Button>
        </div>
      </Modal>
      <Confirm
        open={!!removing}
        onOpenChange={(value) => {
          if (!value) setRemoving(null);
        }}
        title={t("移除此成员？", "Remove this member?")}
        description={t(
          "成员将失去此空间的访问权限。",
          "This member will lose access to this space.",
        )}
        busy={mutation.busy}
        onConfirm={() => {
          mutation.execute(async () => {
            await api.delete(`${base}/members/${removing!.id}`);
            setRemoving(null);
            members.refresh();
          });
        }}
      />
    </Section>
  );
});

const CatalogSettings = observer(function CatalogSettings({
  kind,
}: {
  kind: "states" | "labels";
}) {
  const { workspace, project } = useScope();
  const base = `${project ? projectPath(workspace.id, project.id) : workspacePath(workspace.id)}/${kind}`;
  const records = useRemote<(State & Label)[]>(base);
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<(State & Label) | null>(null);
  const [deleting, setDeleting] = useState<(State & Label) | null>(null);
  const [bulkOpen, setBulkOpen] = useState(false);
  const [bulkNames, setBulkNames] = useState("");
  const [form, setForm] = useState({
    name: "",
    color: "#7b88cf",
    description: "",
    group: "unstarted" as StateGroup,
    is_default: false,
    parent_id: "",
  });
  const mutation = useMutation();
  const t = appStore.t;
  const isState = kind === "states";
  const recordBase = (item: State & Label) =>
    kind === "labels" && item.project_id
      ? `${projectPath(workspace.id, item.project_id)}/labels`
      : base;
  const start = (item?: State & Label) => {
    setEditing(item ?? null);
    setForm({
      name: item?.name ?? "",
      color: item?.color ?? "#7b88cf",
      description: item?.description ?? "",
      group: item?.group ?? "unstarted",
      is_default: item?.is_default ?? false,
      parent_id: item?.parent_id ?? "",
    });
    setOpen(true);
  };
  const save = async (event: FormEvent) => {
    event.preventDefault();
    const payload = isState
      ? {
          name: form.name,
          color: form.color,
          group: form.group,
          is_default: form.is_default,
          position: editing?.position ?? (records.data?.length ?? 0) * 1000,
        }
      : {
          name: form.name,
          color: form.color,
          description: form.description,
          parent_id: form.parent_id || null,
        };
    await mutation.execute(
      async () => {
        if (editing)
          await api.patch(`${recordBase(editing)}/${editing.id}`, payload);
        else await api.post(base, payload);
        setOpen(false);
        records.refresh();
        if (project)
          await appStore.loadProjectResources(workspace.id, project.id);
      },
      t("已保存", "Saved"),
    );
  };
  const descendantOf = (item: Label, ancestor: string): boolean => {
    const seen = new Set<string>();
    let parent = item.parent_id;
    while (parent && !seen.has(parent)) {
      if (parent === ancestor) return true;
      seen.add(parent);
      parent =
        records.data?.find((entry) => entry.id === parent)?.parent_id ?? null;
    }
    return false;
  };
  const labelDepth = (item: Label) => {
    let depth = 0;
    const seen = new Set([item.id]);
    let parent = item.parent_id;
    while (parent && !seen.has(parent)) {
      seen.add(parent);
      depth++;
      parent =
        records.data?.find((entry) => entry.id === parent)?.parent_id ?? null;
    }
    return depth;
  };
  const labelPath = (item: Label): string => {
    const names = [item.name];
    const seen = new Set([item.id]);
    let parent = item.parent_id;
    while (parent && !seen.has(parent)) {
      seen.add(parent);
      const entry = records.data?.find((value) => value.id === parent);
      if (!entry) break;
      names.unshift(entry.name);
      parent = entry.parent_id;
    }
    return names.join(" / ");
  };
  const orderedRecords = isState
    ? records.data
    : [...(records.data ?? [])].sort((a, b) =>
        labelPath(a).localeCompare(labelPath(b), appStore.locale),
      );
  return (
    <Section
      title={isState ? t("工作流状态", "Workflow states") : t("标签", "Labels")}
      description={
        isState
          ? t(
              "定义工作从想法到完成的每一步。",
              "Define each step from an idea to completed work.",
            )
          : t(
              "用有意义的标签，帮助团队分类与查找工作。",
              "Use meaningful labels to organize and find work.",
            )
      }
    >
      <div className="settings-toolbar">
        <Badge>{records.data?.length ?? 0}</Badge>
        {!isState && (
          <Button onClick={() => setBulkOpen(true)}>
            {t("批量创建", "Bulk create")}
          </Button>
        )}
        <Button variant="primary" onClick={() => start()}>
          <Plus size={14} />
          {isState ? t("添加状态", "Add state") : t("添加标签", "Add label")}
        </Button>
      </div>
      <ErrorBox message={records.error || mutation.error} />
      {records.loading ? (
        <Loading />
      ) : (
        (isState ? Object.keys(stateGroupLabels) : ["all"]).map((group) => (
          <div className="catalog-group" key={group}>
            {isState && <h3>{t(...stateGroupLabels[group as StateGroup])}</h3>}
            {orderedRecords
              ?.filter((item) => !isState || item.group === group)
              .map((item) => (
                <div
                  className="catalog-row"
                  key={item.id}
                  style={
                    !isState
                      ? { paddingLeft: 12 + labelDepth(item) * 20 }
                      : undefined
                  }
                >
                  {isState ? (
                    <StateIcon state={item} />
                  ) : (
                    <span
                      className="label-dot"
                      style={{ backgroundColor: item.color }}
                    />
                  )}
                  <strong>{item.name}</strong>
                  {!isState && !project && item.project_id && (
                    <Badge>
                      {appStore.projects.get(item.project_id)?.identifier ??
                        t("项目标签", "Project label")}
                    </Badge>
                  )}
                  {isState && item.is_default && (
                    <Badge>{t("默认", "Default")}</Badge>
                  )}
                  <span className="flex-spacer" />
                  {!isState && project && item.project_id === null ? (
                    <NavLink
                      className="text-muted"
                      to={`/w/${workspace.slug}/settings/labels`}
                    >
                      {t("工作区标签", "Workspace label")}
                    </NavLink>
                  ) : (
                    <Menu
                      items={[
                        {
                          label: t("编辑", "Edit"),
                          onSelect: () => start(item),
                        },
                        {
                          label: t("删除", "Delete"),
                          danger: true,
                          onSelect: () => setDeleting(item),
                        },
                      ]}
                    />
                  )}
                </div>
              ))}
          </div>
        ))
      )}
      <Modal
        open={open}
        onOpenChange={setOpen}
        title={
          editing
            ? t("编辑", "Edit")
            : isState
              ? t("添加状态", "Add state")
              : t("添加标签", "Add label")
        }
      >
        <form className="form-stack modal-body" onSubmit={save}>
          <Field label={t("名称", "Name")}>
            <Input
              value={form.name}
              onChange={(event) =>
                setForm({ ...form, name: event.target.value })
              }
              required
              autoFocus
            />
          </Field>
          <div className="form-row">
            <Field label={t("颜色", "Color")}>
              <Input
                type="color"
                value={form.color}
                onChange={(event) =>
                  setForm({ ...form, color: event.target.value })
                }
              />
            </Field>
            {isState && (
              <Field label={t("所属阶段", "State group")}>
                <Select
                  value={form.group}
                  onChange={(event) =>
                    setForm({
                      ...form,
                      group: event.target.value as StateGroup,
                    })
                  }
                >
                  {Object.entries(stateGroupLabels).map(([key, value]) => (
                    <option key={key} value={key}>
                      {t(...value)}
                    </option>
                  ))}
                </Select>
              </Field>
            )}
          </div>
          {isState ? (
            <label className="checkbox-label">
              <input
                type="checkbox"
                checked={form.is_default}
                onChange={(event) =>
                  setForm({ ...form, is_default: event.target.checked })
                }
              />
              {t(
                "设为新工作项的默认状态",
                "Use as the default for new work items",
              )}
            </label>
          ) : (
            <>
              <Field label={t("父标签", "Parent label")}>
                <Select
                  value={form.parent_id}
                  onChange={(event) =>
                    setForm({ ...form, parent_id: event.target.value })
                  }
                >
                  <option value="">{t("顶层标签", "Top-level label")}</option>
                  {orderedRecords
                    ?.filter(
                      (item) =>
                        (item.project_id ?? null) ===
                          (editing?.project_id ?? project?.id ?? null) &&
                        item.id !== editing?.id &&
                        (!editing || !descendantOf(item, editing.id)),
                    )
                    .map((item) => (
                      <option key={item.id} value={item.id}>
                        {labelPath(item)}
                      </option>
                    ))}
                </Select>
              </Field>
              <Field label={t("说明", "Description")}>
                <Input
                  value={form.description}
                  onChange={(event) =>
                    setForm({ ...form, description: event.target.value })
                  }
                />
              </Field>
            </>
          )}
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button variant="primary" type="submit" busy={mutation.busy}>
              {t("保存", "Save")}
            </Button>
          </div>
        </form>
      </Modal>
      {!isState && (
        <Modal
          open={bulkOpen}
          onOpenChange={setBulkOpen}
          title={t("批量创建标签", "Create labels in bulk")}
          description={t(
            "每行一个标签名称，一次最多创建 100 个。",
            "Enter one label name per line, up to 100 labels.",
          )}
        >
          <form
            className="modal-body form-stack"
            onSubmit={(event) => {
              event.preventDefault();
              mutation.execute(async () => {
                const labels = bulkNames
                  .split(/\r?\n/)
                  .map((name) => name.trim())
                  .filter(Boolean)
                  .map((name) => ({ name }));
                if (!labels.length || labels.length > 100)
                  throw new Error(
                    t(
                      "请输入 1 至 100 个标签名称。",
                      "Enter between 1 and 100 label names.",
                    ),
                  );
                await api.post(`${base}/bulk`, { labels });
                setBulkOpen(false);
                setBulkNames("");
                records.refresh();
                if (project)
                  await appStore.loadProjectResources(workspace.id, project.id);
              });
            }}
          >
            <textarea
              className="input textarea"
              value={bulkNames}
              onChange={(event) => setBulkNames(event.target.value)}
              rows={8}
              aria-label={t("标签名称，每行一个", "Label names, one per line")}
              required
              autoFocus
            />
            <ErrorBox message={mutation.error} />
            <div className="modal-footer">
              <Button type="submit" variant="primary" busy={mutation.busy}>
                {t("创建标签", "Create labels")}
              </Button>
            </div>
          </form>
        </Modal>
      )}
      <Confirm
        open={!!deleting}
        onOpenChange={(value) => {
          if (!value) setDeleting(null);
        }}
        title={t("删除此条目？", "Delete this item?")}
        description={
          isState
            ? t(
                "被工作项使用的状态需要先迁移工作项。",
                "Move work items out of this state before deleting it.",
              )
            : t(
                "此标签将从关联工作项中移除。",
                "The label will be removed from associated work items.",
              )
        }
        busy={mutation.busy}
        onConfirm={() => {
          mutation.execute(async () => {
            await api.delete(`${recordBase(deleting!)}/${deleting!.id}`);
            setDeleting(null);
            records.refresh();
            if (project)
              await appStore.loadProjectResources(workspace.id, project.id);
          });
        }}
      />
    </Section>
  );
});

const TokenSettings = observer(function TokenSettings() {
  const { workspace } = useScope();
  const base = `${workspacePath(workspace.id)}/api-tokens`;
  const tokens = useRemote<
    {
      id: string;
      name: string;
      prefix: string;
      created_at: string;
      last_used_at: string | null;
      expires_at: string | null;
    }[]
  >(base);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [expiry, setExpiry] = useState("");
  const [secret, setSecret] = useState("");
  const [revoke, setRevoke] = useState<string | null>(null);
  const mutation = useMutation();
  const t = appStore.t;
  return (
    <Section
      title={t("API 令牌", "API tokens")}
      description={t(
        "用个人令牌连接工具，权限始终受你的工作区角色约束。",
        "Connect your tools with a personal token. Access follows your workspace role.",
      )}
    >
      <div className="settings-toolbar">
        <span className="text-muted">
          {tokens.data?.length ?? 0} {t("个令牌", "tokens")}
        </span>
        <Button variant="primary" onClick={() => setOpen(true)}>
          <Plus size={14} />
          {t("创建令牌", "Create token")}
        </Button>
      </div>
      <ErrorBox message={tokens.error || mutation.error} />
      {tokens.loading ? (
        <Loading />
      ) : (
        tokens.data?.map((token) => (
          <div className="catalog-row" key={token.id}>
            <KeyRound size={16} />
            <div className="flex-spacer">
              <strong>{token.name}</strong>
              <p className="settings-note">
                {token.prefix}… · {t("创建于", "Created")}{" "}
                {dateTime(token.created_at, appStore.locale)}
              </p>
            </div>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setRevoke(token.id)}
            >
              {t("撤销", "Revoke")}
            </Button>
          </div>
        ))
      )}
      <Modal
        open={open}
        onOpenChange={setOpen}
        title={t("创建 API 令牌", "Create API token")}
      >
        <form
          className="form-stack modal-body"
          onSubmit={(event) => {
            event.preventDefault();
            mutation.execute(async () => {
              const result = await api.post<{ token: string }>(base, {
                name,
                expires_at: expiry
                  ? new Date(`${expiry}T23:59:59`).toISOString()
                  : null,
              });
              setOpen(false);
              setName("");
              setSecret(result.data.token);
              tokens.refresh();
            });
          }}
        >
          <Field label={t("令牌名称", "Token name")}>
            <Input
              value={name}
              onChange={(event) => setName(event.target.value)}
              required
              autoFocus
            />
          </Field>
          <Field label={t("到期日期（可选）", "Expiry date (optional)")}>
            <Input
              type="date"
              value={expiry}
              onChange={(event) => setExpiry(event.target.value)}
              min={new Date().toISOString().slice(0, 10)}
            />
          </Field>
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button variant="primary" type="submit" busy={mutation.busy}>
              {t("创建", "Create")}
            </Button>
          </div>
        </form>
      </Modal>
      <Modal
        open={!!secret}
        onOpenChange={(value) => {
          if (!value) setSecret("");
        }}
        title={t("保存你的令牌", "Save your token")}
        description={t(
          "完整令牌只会显示这一次，请保存在安全的位置。",
          "The complete token is shown only once. Keep it in a safe place.",
        )}
      >
        <div className="modal-body form-stack">
          <div className="secret-result">{secret}</div>
          <Button
            onClick={() =>
              navigator.clipboard
                .writeText(secret)
                .then(() => appStore.notify(t("令牌已复制", "Token copied")))
                .catch((cause) => appStore.notify(errorMessage(cause), "error"))
            }
          >
            <Copy size={14} />
            {t("复制令牌", "Copy token")}
          </Button>
        </div>
      </Modal>
      <Confirm
        open={!!revoke}
        onOpenChange={(value) => {
          if (!value) setRevoke(null);
        }}
        title={t("撤销令牌？", "Revoke token?")}
        description={t(
          "使用此令牌的工具将立即失去访问权限。",
          "Tools using this token will immediately lose access.",
        )}
        busy={mutation.busy}
        onConfirm={() => {
          mutation.execute(async () => {
            await api.delete(`${base}/${revoke}`);
            setRevoke(null);
            tokens.refresh();
          });
        }}
      />
    </Section>
  );
});

const WebhookSettings = observer(function WebhookSettings() {
  const { workspace } = useScope();
  const base = `${workspacePath(workspace.id)}/webhooks`;
  const hooks =
    useRemote<
      { id: string; url: string; events: string[]; is_active: boolean }[]
    >(base);
  const [open, setOpen] = useState(false);
  const [url, setUrl] = useState("");
  const [editingId, setEditingId] = useState<string | null>(null);
  const [events, setEvents] = useState<string[]>(["work_item.changed"]);
  const [active, setActive] = useState(true);
  const [rotatingId, setRotatingId] = useState<string | null>(null);
  const [secret, setSecret] = useState("");
  const [deliveryId, setDeliveryId] = useState<string | null>(null);
  const deliveries = useRemote<
    {
      id: string;
      response_status: number;
      attempts: number;
      created_at: string;
      delivered_at: string | null;
    }[]
  >(deliveryId ? `${base}/${deliveryId}/deliveries` : null);
  const mutation = useMutation();
  const t = appStore.t;
  const eventOptions = [
    { value: "*", label: t("所有事件", "All events") },
    { value: "project.changed", label: t("项目更改", "Project changes") },
    { value: "work_item.changed", label: t("工作项更改", "Work item changes") },
    { value: "comment.changed", label: t("评论更改", "Comment changes") },
    { value: "cycle.changed", label: t("周期更改", "Cycle changes") },
    {
      value: "cycle.items_changed",
      label: t("周期工作项更改", "Cycle item changes"),
    },
    { value: "module.changed", label: t("模块更改", "Module changes") },
    {
      value: "module.items_changed",
      label: t("模块工作项更改", "Module item changes"),
    },
  ];
  return (
    <Section
      title="Webhooks"
      description={t(
        "把工作项变化发送给团队使用的其他工具。",
        "Send work item changes to the other tools your team uses.",
      )}
    >
      <div className="settings-toolbar">
        <Badge>{hooks.data?.length ?? 0}</Badge>
        <Button
          variant="primary"
          onClick={() => {
            setEditingId(null);
            setUrl("");
            setEvents(["work_item.changed"]);
            setActive(true);
            setOpen(true);
          }}
        >
          <Plus size={14} />
          {t("添加 Webhook", "Add webhook")}
        </Button>
      </div>
      <ErrorBox message={hooks.error || mutation.error} />
      {hooks.loading ? (
        <Loading />
      ) : (
        hooks.data?.map((hook) => (
          <div className="catalog-row" key={hook.id}>
            <Webhook size={17} />
            <div className="flex-spacer">
              <strong>{hook.url}</strong>
              <p className="settings-note">{hook.events.join(", ")}</p>
            </div>
            <Badge>
              {hook.is_active ? t("启用", "Active") : t("暂停", "Paused")}
            </Badge>
            <Menu
              items={[
                {
                  label: t("编辑", "Edit"),
                  onSelect: () => {
                    setEditingId(hook.id);
                    setUrl(hook.url);
                    setEvents(hook.events);
                    setActive(hook.is_active);
                    setOpen(true);
                  },
                },
                {
                  label: hook.is_active
                    ? t("暂停", "Pause")
                    : t("启用", "Enable"),
                  onSelect: () => {
                    mutation.execute(async () => {
                      await api.patch(`${base}/${hook.id}`, {
                        is_active: !hook.is_active,
                      });
                      hooks.refresh();
                    });
                  },
                },
                {
                  label: t("发送测试事件", "Send test event"),
                  icon: <Send size={14} />,
                  onSelect: () => {
                    mutation.execute(
                      () => api.post(`${base}/${hook.id}/test`),
                      t("测试事件已加入发送队列", "Test event queued"),
                    );
                  },
                },
                {
                  label: t("投递记录", "Delivery history"),
                  onSelect: () => setDeliveryId(hook.id),
                },
                {
                  label: t("重置签名密钥", "Rotate signing secret"),
                  onSelect: () => setRotatingId(hook.id),
                },
                {
                  label: t("删除", "Delete"),
                  danger: true,
                  onSelect: () => {
                    mutation.execute(async () => {
                      await api.delete(`${base}/${hook.id}`);
                      hooks.refresh();
                    });
                  },
                },
              ]}
            />
          </div>
        ))
      )}
      <Modal
        open={open}
        onOpenChange={setOpen}
        title={
          editingId
            ? t("编辑 Webhook", "Edit webhook")
            : t("添加 Webhook", "Add webhook")
        }
      >
        <form
          className="form-stack modal-body"
          onSubmit={(event) => {
            event.preventDefault();
            mutation.execute(async () => {
              if (!events.length)
                throw new Error(
                  t("请选择至少一种事件。", "Choose at least one event."),
                );
              const body = {
                url,
                events: events.includes("*") ? ["*"] : events,
                is_active: active,
              };
              if (editingId) await api.patch(`${base}/${editingId}`, body);
              else {
                const result = await api.post<{ secret: string }>(base, body);
                setSecret(result.data.secret);
              }
              setOpen(false);
              setUrl("");
              hooks.refresh();
            });
          }}
        >
          <Field label={t("接收地址", "Destination URL")}>
            <Input
              value={url}
              onChange={(event) => setUrl(event.target.value)}
              type="url"
              placeholder="https://example.com/hooks"
              required
              autoFocus
            />
          </Field>
          <Field label={t("订阅事件", "Subscribed events")}>
            <MultiSelect
              value={events}
              options={eventOptions}
              onChange={setEvents}
              placeholder={t("选择事件", "Choose events")}
            />
          </Field>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={active}
              onChange={(event) => setActive(event.target.checked)}
            />
            {t("启用事件投递", "Enable event delivery")}
          </label>
          <p className="settings-note">
            {t(
              "只投递订阅的事件。接收方应使用签名密钥校验请求。",
              "Only subscribed events are delivered. Verify requests with the signing secret.",
            )}
          </p>
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button variant="primary" type="submit" busy={mutation.busy}>
              {editingId ? t("保存", "Save") : t("创建", "Create")}
            </Button>
          </div>
        </form>
      </Modal>
      <Confirm
        open={!!rotatingId}
        onOpenChange={(value) => {
          if (!value) setRotatingId(null);
        }}
        title={t("重置签名密钥？", "Rotate signing secret?")}
        description={t(
          "新密钥会立即生效。请同步更新接收方用于验证请求的密钥。",
          "The new secret takes effect immediately. Update the receiving service's verification secret.",
        )}
        busy={mutation.busy}
        onConfirm={() =>
          mutation.execute(async () => {
            const result = await api.post<{ secret: string }>(
              `${base}/${rotatingId}/rotate-secret`,
            );
            setSecret(result.data.secret);
            setRotatingId(null);
          })
        }
      />
      <Modal
        open={!!secret}
        onOpenChange={(value) => {
          if (!value) setSecret("");
        }}
        title={t("Webhook 签名密钥", "Webhook signing secret")}
      >
        <div className="modal-body form-stack">
          <div className="secret-result">{secret}</div>
          <p className="settings-note">
            {t(
              "请保存此密钥，用于校验收到的事件。",
              "Save this secret to verify incoming events.",
            )}
          </p>
        </div>
      </Modal>
      <Modal
        open={!!deliveryId}
        onOpenChange={(value) => {
          if (!value) setDeliveryId(null);
        }}
        title={t("投递记录", "Delivery history")}
      >
        <div className="modal-body">
          <ErrorBox message={deliveries.error} />
          <Button size="sm" onClick={deliveries.refresh}>
            {t("刷新", "Refresh")}
          </Button>
          {deliveries.loading ? (
            <Loading />
          ) : (
            deliveries.data?.map((delivery) => (
              <div className="catalog-row" key={delivery.id}>
                <Badge>
                  {delivery.response_status || t("等待中", "Pending")}
                </Badge>
                <span>{dateTime(delivery.created_at, appStore.locale)}</span>
                <span className="text-muted">
                  {delivery.attempts} {t("次尝试", "attempts")}
                </span>
              </div>
            ))
          )}
        </div>
      </Modal>
    </Section>
  );
});

export const ExportsSettings = observer(function ExportsSettings() {
  const { workspace, project } = useScope();
  const base = `${workspacePath(workspace.id)}/exports`;
  const exports = useRemote<
    {
      id: string;
      format: string;
      status: string;
      created_at: string;
      error_message?: string;
    }[]
  >(base);
  const [format, setFormat] = useState("csv");
  const mutation = useMutation();
  const t = appStore.t;
  useEffect(() => {
    if (
      !exports.data?.some(
        (item) => item.status === "pending" || item.status === "processing",
      )
    )
      return;
    const timer = setInterval(exports.refresh, 4000);
    return () => clearInterval(timer);
  }, [exports.data, exports.refresh]);
  return (
    <Section
      title={t("数据导出", "Data exports")}
      description={t(
        "创建可下载的数据副本，用于报告、备份或后续分析。",
        "Create a downloadable copy for reporting, backup, or further analysis.",
      )}
    >
      <form
        className="form-row"
        onSubmit={(event) => {
          event.preventDefault();
          mutation.execute(
            async () => {
              await api.post(base, {
                format,
                project_id: project?.id ?? null,
                filters: {},
              });
              exports.refresh();
            },
            t("导出任务已创建", "Export job created"),
          );
        }}
      >
        <Field label={t("文件格式", "File format")}>
          <Select
            value={format}
            onChange={(event) => setFormat(event.target.value)}
          >
            <option value="csv">CSV</option>
            <option value="xlsx">Excel (.xlsx)</option>
            <option value="json">JSON</option>
          </Select>
        </Field>
        <div style={{ alignSelf: "end" }}>
          <Button variant="primary" type="submit" busy={mutation.busy}>
            <Download size={14} />
            {t("导出工作项", "Export work items")}
          </Button>
        </div>
      </form>
      <ErrorBox message={exports.error || mutation.error} />
      <Panel title={t("导出记录", "Export history")}>
        <Button size="sm" onClick={exports.refresh}>
          {t("刷新状态", "Refresh status")}
        </Button>
        {exports.loading ? (
          <Loading />
        ) : (
          exports.data?.map((item) => (
            <div className="catalog-row" key={item.id}>
              <FileDown size={16} />
              <div className="flex-spacer">
                <strong>{item.format.toUpperCase()}</strong>
                <p className="settings-note">
                  {dateTime(item.created_at, appStore.locale)}
                  {item.error_message && (
                    <span className="text-danger"> · {item.error_message}</span>
                  )}
                </p>
              </div>
              <Badge>{item.status}</Badge>
              {item.status === "completed" && (
                <a
                  href={`/api/v1${base}/${item.id}/download`}
                  className="button button-secondary button-sm"
                >
                  <Download size={13} />
                  {t("下载", "Download")}
                </a>
              )}
            </div>
          ))
        )}
      </Panel>
    </Section>
  );
});

interface SiteConfiguration {
  slug: string;
  title: string;
  description: string;
  is_enabled: boolean;
  comments_enabled: boolean;
  reactions_enabled: boolean;
  votes_enabled: boolean;
  intake_enabled: boolean;
}
const SiteSettings = observer(function SiteSettings() {
  const { workspace, project } = useScope();
  const base = `${projectPath(workspace.id, project!.id)}/site`;
  const current = useRemote<SiteConfiguration>(base);
  const [form, setForm] = useState<SiteConfiguration>({
    slug: `${workspace.slug}-${project!.identifier.toLowerCase()}`,
    title: project!.name,
    description: project!.description,
    is_enabled: false,
    comments_enabled: false,
    reactions_enabled: false,
    votes_enabled: false,
    intake_enabled: false,
  });
  const mutation = useMutation();
  const t = appStore.t;
  useEffect(() => {
    if (current.data && current.data.slug) setForm(current.data);
  }, [current.data]);
  return (
    <Section
      title={t("公开分享", "Public sharing")}
      description={t(
        "为项目创建公开页面，让外部伙伴了解进展并参与反馈。",
        "Publish your project so people outside your workspace can follow progress and share feedback.",
      )}
    >
      <ErrorBox
        message={
          (current.errorStatus === 404 ? "" : current.error) || mutation.error
        }
      />
      <form
        className="form-stack"
        onSubmit={(event) => {
          event.preventDefault();
          mutation.execute(
            async () => {
              await api.put(base, form);
              current.refresh();
            },
            t("公开页面设置已保存", "Public sharing settings saved"),
          );
        }}
      >
        <ToggleSetting
          label={t("发布项目", "Publish project")}
          description={t(
            "启用后，持有链接的任何人都可查看公开工作项。",
            "When enabled, anyone with the link can view public work items.",
          )}
          checked={form.is_enabled}
          onChange={(value) => setForm({ ...form, is_enabled: value })}
        />
        <Field label={t("页面标题", "Page title")}>
          <Input
            value={form.title}
            onChange={(event) =>
              setForm({ ...form, title: event.target.value })
            }
            required
          />
        </Field>
        <Field label={t("公开地址", "Public address")}>
          <div className="input-prefix">
            <span>/public/</span>
            <Input
              value={form.slug}
              onChange={(event) =>
                setForm({ ...form, slug: event.target.value })
              }
              required
              pattern="[a-z0-9]+(?:-[a-z0-9]+)*"
            />
          </div>
        </Field>
        <Field label={t("介绍", "Introduction")}>
          <textarea
            className="input textarea"
            value={form.description}
            onChange={(event) =>
              setForm({ ...form, description: event.target.value })
            }
            rows={3}
          />
        </Field>
        <div>
          <ToggleSetting
            label={t("允许评论", "Allow comments")}
            description={t(
              "登录用户可以在公开页面讨论工作项。",
              "Signed-in users can discuss work items on the public page.",
            )}
            checked={form.comments_enabled}
            onChange={(value) => setForm({ ...form, comments_enabled: value })}
          />
          <ToggleSetting
            label={t("允许投票", "Allow voting")}
            checked={form.votes_enabled}
            onChange={(value) => setForm({ ...form, votes_enabled: value })}
          />
          <ToggleSetting
            label={t("允许表情回应", "Allow reactions")}
            checked={form.reactions_enabled}
            onChange={(value) => setForm({ ...form, reactions_enabled: value })}
          />
          <ToggleSetting
            label={t("收集新需求", "Accept new requests")}
            checked={form.intake_enabled}
            onChange={(value) => setForm({ ...form, intake_enabled: value })}
          />
        </div>
        <div className="settings-actions">
          {current.data?.is_enabled && (
            <a
              className="button button-secondary button-md"
              style={{ marginRight: "auto" }}
              href={`/public/${current.data.slug}`}
              target="_blank"
              rel="noreferrer"
            >
              <ExternalLink size={14} />
              {t("查看公开页面", "View public page")}
            </a>
          )}
          <Button type="submit" variant="primary" busy={mutation.busy}>
            {t("保存设置", "Save settings")}
          </Button>
        </div>
      </form>
    </Section>
  );
});
