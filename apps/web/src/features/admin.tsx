import { useEffect, useState, type FormEvent } from "react";
import { observer } from "mobx-react-lite";
import { useParams } from "react-router-dom";
import {
  Archive,
  CheckCircle2,
  FolderKanban,
  HardDrive,
  Mail,
  Search,
  Send,
  Settings,
  Shield,
  Users,
} from "lucide-react";
import { appStore } from "../stores/app-store";
import { api } from "../lib/api";
import { useMutation, useRemote } from "../lib/hooks";
import {
  Avatar,
  Badge,
  Button,
  ErrorBox,
  Field,
  Input,
  Loading,
  Menu,
  Select,
} from "../components/ui";
import { ToggleSetting } from "./settings";
import type { User } from "../types";

interface AdminConfiguration {
  name: string;
  registration_enabled: boolean;
  settings: { allow_workspace_creation: boolean; magic_login_enabled: boolean };
}
interface ServiceConfiguration {
  secret_storage_enabled: boolean;
  email: {
    configured: boolean;
    host: string;
    port: number;
    from: string;
    user: string;
    secure: boolean;
    password_configured: boolean;
  };
  oauth: Record<
    string,
    {
      configured: boolean;
      client_id: string;
      base_url: string;
      client_secret_configured: boolean;
    }
  >;
  storage: {
    configured: boolean;
    endpoint: string;
    bucket: string;
    secure: boolean;
  };
}

export const AdminPage = observer(function AdminPage() {
  const { adminSection = "overview" } = useParams();
  const t = appStore.t;
  return (
    <section className="settings-section settings-wide">
      {adminSection === "users" ? (
        <AdminUsers />
      ) : adminSection === "workspaces" ? (
        <AdminWorkspaces />
      ) : ["storage", "ai", "unsplash"].includes(adminSection) ? (
        <ManagedServiceSettings
          key={adminSection}
          service={adminSection as "storage" | "ai" | "unsplash"}
        />
      ) : ["email", "authentication"].includes(adminSection) ? (
        <AdminServices section={adminSection} />
      ) : (
        <>
          <h1>{t("实例概览", "Instance overview")}</h1>
          <p className="page-description">
            {t(
              "了解整个实例的运行情况，管理团队的共同工作环境。",
              "Understand your instance and manage your team's shared environment.",
            )}
          </p>
          <AdminOverview />
          <AdminGeneral />
        </>
      )}
    </section>
  );
});

const AdminOverview = observer(function AdminOverview() {
  const stats = useRemote<{
    users: number;
    active_users: number;
    workspaces: number;
    projects: number;
    work_items: number;
    storage_bytes: number;
    pending_jobs: number;
    active_sessions: number;
  }>("/admin/stats");
  const t = appStore.t;
  return (
    <>
      <ErrorBox message={stats.error} retry={stats.refresh} />
      {stats.loading ? (
        <Loading />
      ) : (
        stats.data && (
          <>
            <div className="analytics-stat-grid">
              {[
                {
                  label: t("用户", "Users"),
                  value: stats.data.users,
                  icon: Users,
                },
                {
                  label: t("工作区", "Workspaces"),
                  value: stats.data.workspaces,
                  icon: FolderKanban,
                },
                {
                  label: t("项目", "Projects"),
                  value: stats.data.projects,
                  icon: Archive,
                },
                {
                  label: t("工作项", "Work items"),
                  value: stats.data.work_items,
                  icon: CheckCircle2,
                },
              ].map((card) => (
                <div className="analytics-stat" key={card.label}>
                  <span>
                    <card.icon size={15} />
                    {card.label}
                  </span>
                  <strong>{card.value}</strong>
                </div>
              ))}
            </div>
            <div className="catalog-row">
              <HardDrive size={16} />
              <span>{t("附件存储", "Attachment storage")}</span>
              <Badge>
                {(stats.data.storage_bytes / 1048576).toFixed(1)} MB
              </Badge>
              <span className="flex-spacer" />
              <span>{t("等待发送的事件", "Pending events")}</span>
              <Badge>{stats.data.pending_jobs}</Badge>
              <span>{t("活跃会话", "Active sessions")}</span>
              <Badge>{stats.data.active_sessions}</Badge>
            </div>
          </>
        )
      )}
    </>
  );
});

const AdminGeneral = observer(function AdminGeneral() {
  const current = useRemote<AdminConfiguration>("/admin/configuration");
  const [form, setForm] = useState<AdminConfiguration>({
    name: "",
    registration_enabled: true,
    settings: { allow_workspace_creation: true, magic_login_enabled: true },
  });
  const mutation = useMutation();
  const t = appStore.t;
  useEffect(() => {
    if (current.data) setForm(current.data);
  }, [current.data]);
  return (
    <section className="settings-panel">
      <div className="settings-panel-heading">
        <h2 className="inline-property">
          <Settings size={15} />
          {t("实例设置", "Instance settings")}
        </h2>
      </div>
      <form
        className="settings-panel-body form-stack"
        onSubmit={(event) => {
          event.preventDefault();
          mutation.execute(
            async () => {
              await api.patch("/admin/configuration", form);
              current.refresh();
            },
            t("实例设置已保存", "Instance settings saved"),
          );
        }}
      >
        <Field label={t("实例名称", "Instance name")}>
          <Input
            value={form.name}
            onChange={(event) => setForm({ ...form, name: event.target.value })}
            required
          />
        </Field>
        <div>
          <ToggleSetting
            label={t("允许账户注册", "Allow account registration")}
            description={t(
              "关闭后，新成员需要通过管理员邀请加入。",
              "When disabled, new members need an invitation from an administrator.",
            )}
            checked={form.registration_enabled}
            onChange={(value) =>
              setForm({ ...form, registration_enabled: value })
            }
          />
          <ToggleSetting
            label={t("允许成员创建工作区", "Allow workspace creation")}
            checked={form.settings.allow_workspace_creation}
            onChange={(value) =>
              setForm({
                ...form,
                settings: { ...form.settings, allow_workspace_creation: value },
              })
            }
          />
          <ToggleSetting
            label={t("启用邮箱验证码登录", "Enable email code sign-in")}
            checked={form.settings.magic_login_enabled}
            onChange={(value) =>
              setForm({
                ...form,
                settings: { ...form.settings, magic_login_enabled: value },
              })
            }
          />
        </div>
        <ErrorBox message={current.error || mutation.error} />
        <div className="settings-actions">
          <Button type="submit" variant="primary" busy={mutation.busy}>
            {t("保存设置", "Save settings")}
          </Button>
        </div>
      </form>
    </section>
  );
});

const AdminUsers = observer(function AdminUsers() {
  const [query, setQuery] = useState("");
  const users = useRemote<
    (User & { is_active: boolean; workspace_count: number })[]
  >(`/admin/users?search=${encodeURIComponent(query)}`);
  const mutation = useMutation();
  const t = appStore.t;
  return (
    <>
      <h1>{t("用户管理", "Users")}</h1>
      <p className="page-description">
        {t(
          "管理实例账户与管理员权限。暂停账户会撤销其登录会话。",
          "Manage instance accounts and administrators. Suspending an account revokes its sessions.",
        )}
      </p>
      <div className="settings-toolbar">
        <div className="search-input">
          <Search size={14} />
          <Input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={t("搜索名称或邮箱…", "Search name or email…")}
          />
        </div>
        <Badge>{users.data?.length ?? 0}</Badge>
      </div>
      <ErrorBox message={users.error || mutation.error} />
      {users.loading ? (
        <Loading />
      ) : (
        <table className="data-table">
          <thead>
            <tr>
              <th>{t("用户", "User")}</th>
              <th>{t("工作区", "Workspaces")}</th>
              <th>{t("状态", "Status")}</th>
              <th>{t("权限", "Access")}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {users.data?.map((user) => (
              <tr key={user.id}>
                <td>
                  <div className="user-cell">
                    <Avatar name={user.display_name} size="sm" />
                    <div>
                      <strong>{user.display_name}</strong>
                      <small>{user.email}</small>
                    </div>
                  </div>
                </td>
                <td>{user.workspace_count}</td>
                <td>
                  <Badge>
                    {user.is_active
                      ? t("正常", "Active")
                      : t("已暂停", "Suspended")}
                  </Badge>
                </td>
                <td>
                  {user.is_instance_admin ? (
                    <span className="inline-property">
                      <Shield size={13} />
                      {t("管理员", "Admin")}
                    </span>
                  ) : (
                    t("用户", "User")
                  )}
                </td>
                <td>
                  <Menu
                    items={[
                      {
                        label: user.is_active
                          ? t("暂停账户", "Suspend account")
                          : t("恢复账户", "Reactivate account"),
                        danger: user.is_active,
                        onSelect: () => {
                          mutation.execute(async () => {
                            await api.patch(`/admin/users/${user.id}`, {
                              is_active: !user.is_active,
                            });
                            users.refresh();
                          });
                        },
                      },
                      {
                        label: user.is_instance_admin
                          ? t("移除管理员权限", "Remove administrator access")
                          : t("设为管理员", "Make administrator"),
                        onSelect: () => {
                          mutation.execute(async () => {
                            await api.patch(`/admin/users/${user.id}`, {
                              is_instance_admin: !user.is_instance_admin,
                            });
                            users.refresh();
                          });
                        },
                      },
                    ]}
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
});

const AdminWorkspaces = observer(function AdminWorkspaces() {
  const workspaces = useRemote<
    {
      id: string;
      name: string;
      slug: string;
      project_count: number;
      member_count: number;
    }[]
  >("/admin/workspaces");
  const t = appStore.t;
  return (
    <>
      <h1>{t("工作区管理", "Workspaces")}</h1>
      <p className="page-description">
        {t(
          "查看实例中的组织结构与资源分布。",
          "Review the workspaces and resources across your instance.",
        )}
      </p>
      <ErrorBox message={workspaces.error} retry={workspaces.refresh} />
      {workspaces.loading ? (
        <Loading />
      ) : (
        <table className="data-table">
          <thead>
            <tr>
              <th>{t("工作区", "Workspace")}</th>
              <th>{t("地址", "Address")}</th>
              <th>{t("成员", "Members")}</th>
              <th>{t("项目", "Projects")}</th>
            </tr>
          </thead>
          <tbody>
            {workspaces.data?.map((workspace) => (
              <tr key={workspace.id}>
                <td>
                  <span className="inline-property">
                    <FolderKanban size={15} />
                    {workspace.name}
                  </span>
                </td>
                <td>{workspace.slug}</td>
                <td>{workspace.member_count}</td>
                <td>{workspace.project_count}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
});

const AdminServices = observer(function AdminServices({
  section,
}: {
  section: string;
}) {
  const services = useRemote<ServiceConfiguration>("/admin/services");
  const [smtp, setSmtp] = useState({
    host: "",
    port: 587,
    from: "",
    user: "",
    secure: false,
    password: "",
  });
  const [provider, setProvider] = useState("google");
  const [oauth, setOauth] = useState({
    client_id: "",
    base_url: "",
    client_secret: "",
  });
  const [testEmail, setTestEmail] = useState(appStore.user!.email);
  const mutation = useMutation();
  const t = appStore.t;
  useEffect(() => {
    if (services.data?.email) setSmtp({ ...services.data.email, password: "" });
  }, [services.data]);
  useEffect(() => {
    const value = services.data?.oauth?.[provider];
    setOauth({
      client_id: value?.client_id ?? "",
      base_url: value?.base_url ?? "",
      client_secret: "",
    });
  }, [services.data, provider]);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const body =
      section === "email"
        ? {
            host: smtp.host,
            port: Number(smtp.port),
            from: smtp.from,
            user: smtp.user,
            secure: smtp.secure,
            ...(smtp.password ? { password: smtp.password } : {}),
          }
        : {
            client_id: oauth.client_id,
            base_url: oauth.base_url,
            ...(oauth.client_secret
              ? { client_secret: oauth.client_secret }
              : {}),
          };
    await mutation.execute(
      async () => {
        await api.patch(
          `/admin/services/${section === "email" ? "email" : provider}`,
          body,
        );
        services.refresh();
      },
      t("服务设置已保存", "Service configuration saved"),
    );
  };
  return (
    <>
      <h1>
        {section === "email"
          ? t("邮件服务", "Email service")
          : section === "storage"
            ? t("文件存储", "File storage")
            : t("身份与安全", "Authentication")}
      </h1>
      <p className="page-description">
        {section === "email"
          ? t(
              "配置用于邀请、通知和账户恢复的邮件服务。",
              "Configure email delivery for invitations, notifications, and account recovery.",
            )
          : section === "storage"
            ? t(
                "查看实例正在使用的附件存储服务。",
                "Review the file storage used by this instance.",
              )
            : t(
                "配置团队使用的第三方登录方式。",
                "Configure the sign-in providers your team uses.",
              )}
      </p>
      <ErrorBox
        message={services.error || mutation.error}
        retry={services.refresh}
      />
      {services.loading ? (
        <Loading />
      ) : section === "storage" ? (
        services.data?.storage && (
          <section className="settings-panel">
            <div className="settings-panel-heading">
              <h2 className="inline-property">
                <HardDrive size={16} />
                {t("对象存储", "Object storage")}
              </h2>
              <Badge>
                {services.data.storage.configured
                  ? t("已配置", "Configured")
                  : t("未配置", "Not configured")}
              </Badge>
            </div>
            <div className="settings-panel-body form-stack">
              <Field label={t("服务地址", "Endpoint")}>
                <Input value={services.data.storage.endpoint} readOnly />
              </Field>
              <Field label={t("存储桶", "Bucket")}>
                <Input value={services.data.storage.bucket} readOnly />
              </Field>
              <span className="inline-property">
                <Shield size={14} />
                TLS:{" "}
                {services.data.storage.secure
                  ? t("启用", "Enabled")
                  : t("未启用", "Disabled")}
              </span>
              <p className="settings-note">
                {t(
                  "存储服务由实例部署配置管理。修改地址前，请先迁移现有附件。",
                  "Storage is managed through deployment configuration. Migrate existing attachments before changing its location.",
                )}
              </p>
            </div>
          </section>
        )
      ) : (
        <form className="form-stack" onSubmit={submit}>
          {section === "email" ? (
            <>
              <Badge>
                {services.data?.email.configured
                  ? t("已配置邮件服务", "Email configured")
                  : t("尚未配置", "Not configured")}
              </Badge>
              <div className="form-row">
                <Field label={t("SMTP 主机", "SMTP host")}>
                  <Input
                    value={smtp.host}
                    onChange={(event) =>
                      setSmtp({ ...smtp, host: event.target.value })
                    }
                    required
                    placeholder="smtp.example.com"
                  />
                </Field>
                <Field label={t("端口", "Port")}>
                  <Input
                    type="number"
                    min={1}
                    max={65535}
                    value={smtp.port}
                    onChange={(event) =>
                      setSmtp({ ...smtp, port: Number(event.target.value) })
                    }
                    required
                  />
                </Field>
              </div>
              <Field label={t("发件地址", "From address")}>
                <Input
                  type="email"
                  value={smtp.from}
                  onChange={(event) =>
                    setSmtp({ ...smtp, from: event.target.value })
                  }
                  required
                />
              </Field>
              <div className="form-row">
                <Field label={t("用户名", "Username")}>
                  <Input
                    value={smtp.user}
                    onChange={(event) =>
                      setSmtp({ ...smtp, user: event.target.value })
                    }
                    autoComplete="off"
                  />
                </Field>
                <Field
                  label={t("密码", "Password")}
                  hint={
                    services.data?.email.password_configured
                      ? t(
                          "已保存密码；留空以保留。",
                          "A password is saved. Leave blank to keep it.",
                        )
                      : undefined
                  }
                >
                  <Input
                    type="password"
                    value={smtp.password}
                    onChange={(event) =>
                      setSmtp({ ...smtp, password: event.target.value })
                    }
                    autoComplete="new-password"
                  />
                </Field>
              </div>
              <ToggleSetting
                label={t("使用安全连接", "Use a secure connection")}
                checked={smtp.secure}
                onChange={(value) => setSmtp({ ...smtp, secure: value })}
              />
            </>
          ) : (
            <>
              <Field label={t("登录服务", "Identity provider")}>
                <Select
                  value={provider}
                  onChange={(event) => setProvider(event.target.value)}
                >
                  <option value="google">Google</option>
                  <option value="github">GitHub</option>
                  <option value="gitlab">GitLab</option>
                  <option value="gitea">Gitea</option>
                </Select>
              </Field>
              <Badge>
                {services.data?.oauth?.[provider]?.configured
                  ? t("已配置", "Configured")
                  : t("未配置", "Not configured")}
              </Badge>
              <Field label="Client ID">
                <Input
                  value={oauth.client_id}
                  onChange={(event) =>
                    setOauth({ ...oauth, client_id: event.target.value })
                  }
                  required
                />
              </Field>
              <Field
                label="Client secret"
                hint={
                  services.data?.oauth?.[provider]?.client_secret_configured
                    ? t(
                        "已保存密钥；留空以保留。",
                        "A secret is saved. Leave blank to keep it.",
                      )
                    : undefined
                }
              >
                <Input
                  type="password"
                  value={oauth.client_secret}
                  onChange={(event) =>
                    setOauth({ ...oauth, client_secret: event.target.value })
                  }
                  autoComplete="new-password"
                />
              </Field>
              {["gitlab", "gitea"].includes(provider) && (
                <Field label={t("服务地址", "Provider base URL")}>
                  <Input
                    type="url"
                    value={oauth.base_url}
                    onChange={(event) =>
                      setOauth({ ...oauth, base_url: event.target.value })
                    }
                    placeholder={
                      provider === "gitlab"
                        ? "https://gitlab.com"
                        : "https://gitea.example.com"
                    }
                  />
                </Field>
              )}
              <p className="settings-note">
                {t("回调地址", "Callback URL")}: {window.location.origin}
                /api/v1/auth/oauth/{provider}/callback
              </p>
            </>
          )}
          <div className="settings-actions">
            <Button type="submit" variant="primary" busy={mutation.busy}>
              {t("保存配置", "Save configuration")}
            </Button>
          </div>
        </form>
      )}
      {section === "email" && (
        <section className="settings-panel">
          <div className="settings-panel-heading">
            <h2 className="inline-property">
              <Mail size={15} />
              {t("测试邮件", "Test email")}
            </h2>
          </div>
          <form
            className="settings-panel-body form-stack"
            onSubmit={(event) => {
              event.preventDefault();
              mutation.execute(
                () => api.post("/admin/email/test", { email: testEmail }),
                t("测试邮件已发送", "Test email sent"),
              );
            }}
          >
            <Input
              type="email"
              value={testEmail}
              onChange={(event) => setTestEmail(event.target.value)}
              required
              aria-label={t("测试收件人", "Test recipient")}
            />
            <Button type="submit" busy={mutation.busy}>
              <Send size={14} />
              {t("发送测试邮件", "Send test email")}
            </Button>
          </form>
        </section>
      )}
    </>
  );
});

const ManagedServiceSettings = observer(function ManagedServiceSettings({
  service,
}: {
  service: "storage" | "ai" | "unsplash";
}) {
  const current =
    useRemote<Record<string, Record<string, unknown> | boolean>>(
      "/admin/services",
    );
  const defaults: Record<string, string | boolean> =
    service === "storage"
      ? {
          endpoint: "",
          public_endpoint: "",
          bucket: "",
          region: "",
          secure: false,
          access_key: "",
          secret_key: "",
        }
      : service === "ai"
        ? {
            provider: "openai",
            base_url: "",
            model: "gpt-5.6-terra",
            wire_api: "responses",
            api_key: "",
          }
        : { access_key: "" };
  const [form, setForm] = useState(defaults);
  const [cleared, setCleared] = useState<string[]>([]);
  const [testResult, setTestResult] = useState("");
  const mutation = useMutation();
  const t = appStore.t;
  const entry = current.data?.[service] as Record<string, unknown> | undefined;
  const secret = (key: string) =>
    ["access_key", "secret_key", "api_key"].includes(key);
  useEffect(() => {
    if (!entry) return;
    setForm(
      Object.fromEntries(
        Object.entries(defaults).map(([key, value]) => [
          key,
          secret(key)
            ? ""
            : typeof entry[key] === typeof value
              ? entry[key]
              : value,
        ]),
      ) as Record<string, string | boolean>,
    );
    setCleared([]);
  }, [current.data, service]);
  const title =
    service === "storage"
      ? t("文件存储", "File storage")
      : service === "ai"
        ? t("AI 写作服务", "AI writing service")
        : t("图片搜索", "Image search");
  const labels: Record<string, string> = {
    endpoint: t("对象存储地址", "Storage endpoint"),
    public_endpoint: t("公共访问地址", "Public endpoint"),
    bucket: t("存储桶", "Bucket"),
    region: t("区域", "Region"),
    access_key: "Access key",
    secret_key: "Secret key",
    api_key: "API key",
    provider: t("服务商", "Provider"),
    base_url: t("API 服务地址", "API base URL"),
    model: t("模型", "Model"),
    wire_api: t("API 协议", "API protocol"),
  };
  return (
    <>
      <h1>{title}</h1>
      <p className="page-description">
        {service === "storage"
          ? t(
              "管理附件与文档图片使用的对象存储。",
              "Manage the object storage for attachments and document images.",
            )
          : service === "ai"
            ? t(
                "为文档和工作项提供润色、总结与写作辅助。",
                "Enable rewriting, summaries, and writing assistance for documents and work items.",
              )
            : t(
                "通过 Unsplash 为团队搜索文档与项目配图。",
                "Find images for documents and projects through Unsplash.",
              )}
      </p>
      <ErrorBox
        message={current.error || mutation.error}
        retry={current.refresh}
      />
      {current.loading ? (
        <Loading />
      ) : (
        <form
          className="form-stack settings-panel-body"
          onSubmit={(event) => {
            event.preventDefault();
            const body: Record<string, unknown> = Object.fromEntries(
              Object.entries(form).filter(
                ([key, value]) => !secret(key) || value !== "",
              ),
            );
            for (const key of cleared) body[key] = null;
            mutation.execute(
              async () => {
                await api.patch(`/admin/services/${service}`, body);
                current.refresh();
              },
              t("服务设置已保存", "Service settings saved"),
            );
          }}
        >
          <Badge>
            {entry?.configured
              ? t("已配置", "Configured")
              : t("尚未配置", "Not configured")}
          </Badge>
          {Object.entries(form).map(([key, value]) =>
            key === "wire_api" ? null : typeof value === "boolean" ? (
              <ToggleSetting
                key={key}
                label={t("启用 TLS 加密连接", "Use TLS encryption")}
                checked={value}
                onChange={(next) => setForm({ ...form, [key]: next })}
              />
            ) : (
              <Field
                key={key}
                label={labels[key]}
                hint={
                  secret(key) && entry?.[`${key}_configured`]
                    ? t(
                        "已保存凭据；留空以保留。",
                        "Credentials are saved. Leave blank to keep them.",
                      )
                    : key === "endpoint"
                      ? t(
                          "填写主机名与端口，例如 localhost:9000。",
                          "Enter the hostname and port, e.g. localhost:9000.",
                        )
                      : undefined
                }
              >
                <Input
                  type={
                    secret(key)
                      ? "password"
                      : key.endsWith("url")
                        ? "url"
                        : "text"
                  }
                  value={value}
                  readOnly={key === "model"}
                  onChange={(event) =>
                    setForm({ ...form, [key]: event.target.value })
                  }
                  autoComplete={secret(key) ? "new-password" : "off"}
                  disabled={cleared.includes(key)}
                />
                {secret(key) && !!entry?.[`${key}_configured`] && (
                  <label className="checkbox-label">
                    <input
                      type="checkbox"
                      checked={cleared.includes(key)}
                      onChange={(event) =>
                        setCleared(
                          event.target.checked
                            ? [...cleared, key]
                            : cleared.filter((item) => item !== key),
                        )
                      }
                    />
                    {t("清除已保存凭据", "Clear saved credentials")}
                  </label>
                )}
              </Field>
            ),
          )}
          {current.data?.secret_storage_enabled === false && (
            <ErrorBox
              message={t(
                "实例尚未启用凭据存储，请管理员配置实例加密密钥后保存。",
                "Credential storage is unavailable. Configure the instance encryption key before saving.",
              )}
            />
          )}
          <div className="settings-actions">
            <Button
              variant="primary"
              type="submit"
              busy={mutation.busy}
              disabled={current.data?.secret_storage_enabled === false}
            >
              {t("保存配置", "Save configuration")}
            </Button>
            {service === "storage" && (
              <Button
                type="button"
                busy={mutation.busy}
                onClick={() =>
                  mutation.execute(async () => {
                    const result = await api.post<{
                      connected: boolean;
                      bucket_exists: boolean;
                    }>("/admin/storage/test");
                    setTestResult(
                      result.data.connected && result.data.bucket_exists
                        ? t(
                            "连接成功，存储桶可访问。",
                            "Connected. The bucket is accessible.",
                          )
                        : t(
                            "服务已连接，但存储桶不存在或无法访问。",
                            "Connected, but the bucket is missing or inaccessible.",
                          ),
                    );
                  })
                }
              >
                {t("测试连接", "Test connection")}
              </Button>
            )}
          </div>
          {testResult && <p className="settings-note">{testResult}</p>}
        </form>
      )}
    </>
  );
});
