import { useState, type FormEvent } from "react";
import { observer } from "mobx-react-lite";
import { Link, Navigate, useNavigate, useSearchParams } from "react-router-dom";
import {
  ArrowRight,
  Check,
  Eye,
  EyeOff,
  Globe2,
  Layers3,
  Moon,
  Sun,
} from "lucide-react";
import { appStore } from "../stores/app-store";
import { useDebouncedValue, useMutation, useRemote } from "../lib/hooks";
import { slugify } from "../lib/utils";
import { Button, ErrorBox, Field, Input } from "../components/ui";

export function Brand({ compact = false }: { compact?: boolean }) {
  return (
    <span className="brand">
      <span className="brand-mark">
        <Layers3 size={19} strokeWidth={2} />
      </span>
      {!compact && <span>My Jira</span>}
    </span>
  );
}

export const AppearanceControls = observer(function AppearanceControls() {
  return (
    <div className="appearance-controls">
      <Button
        variant="ghost"
        size="icon"
        aria-label={appStore.t("切换语言", "Change language")}
        onClick={() =>
          appStore.setLocale(appStore.locale === "zh-CN" ? "en" : "zh-CN")
        }
      >
        <Globe2 size={16} />
      </Button>
      <Button
        variant="ghost"
        size="icon"
        aria-label={appStore.t("切换主题", "Change theme")}
        onClick={() =>
          appStore.setTheme(appStore.theme === "dark" ? "light" : "dark")
        }
      >
        {appStore.theme === "dark" ? <Sun size={16} /> : <Moon size={16} />}
      </Button>
    </div>
  );
});

export const AuthPage = observer(function AuthPage({
  kind,
}: {
  kind: "login" | "register" | "setup";
}) {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const next = params.get("next");
  const destination =
    next?.startsWith("/") && !next.startsWith("//") ? next : "/";
  const mutation = useMutation();
  const [showPassword, setShowPassword] = useState(false);
  const [form, setForm] = useState({
    email: "",
    password: "",
    display_name: "",
    instance_name: "My Jira",
  });
  const t = appStore.t;

  if (appStore.user) return <Navigate to={destination} replace />;
  if (appStore.instance && !appStore.instance.is_setup_done && kind !== "setup")
    return <Navigate to="/setup" replace />;
  if (appStore.instance?.is_setup_done && kind === "setup")
    return <Navigate to="/login" replace />;

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    await mutation.execute(async () => {
      await appStore.authenticate(kind, form);
      navigate(destination, { replace: true });
    });
  };

  return (
    <div className="auth-page">
      <div className="auth-top">
        <Brand />
        <AppearanceControls />
      </div>
      <main className="auth-main">
        <section className="auth-story">
          <span className="eyebrow">
            {t("让团队的下一步，更清晰", "A clearer next step for your team")}
          </span>
          <h1>
            {t("好的工作，", "Good work.")}
            <br />
            <span>{t("从有序开始。", "A thoughtful start.")}</span>
          </h1>
          <p>
            {t(
              "把想法变成计划，把计划变成进展。你的项目、工作项和团队知识，在一个专注的空间里连接。",
              "Connect ideas, plans, and progress. A focused workspace for your projects, work items, and team knowledge.",
            )}
          </p>
          <div className="auth-visual" aria-hidden="true">
            <div className="visual-line">
              <span className="visual-dot" />
              <span className="visual-skeleton long" />
              <span className="visual-pill" />
            </div>
            <div className="visual-line">
              <Check size={14} />
              <span className="visual-skeleton" />
              <span className="visual-avatar" />
            </div>
            <div className="visual-line">
              <Check size={14} />
              <span className="visual-skeleton medium" />
              <span className="visual-avatar second" />
            </div>
            <div className="visual-line">
              <span className="visual-dot empty" />
              <span className="visual-skeleton long" />
              <span className="visual-avatar third" />
            </div>
            <div className="visual-progress">
              <i />
            </div>
            <div className="visual-caption">
              {t("每一次推进，都看得见。", "Every step forward, in view.")}
            </div>
          </div>
        </section>
        <section className="auth-card">
          <div className="auth-card-icon">
            <Layers3 size={25} />
          </div>
          <h2>
            {kind === "setup"
              ? t("创建你的工作环境", "Set up your instance")
              : kind === "register"
                ? t("开始一起创造", "Start building together")
                : t("欢迎回来", "Welcome back")}
          </h2>
          <p className="auth-subtitle">
            {kind === "setup"
              ? t(
                  "设置管理员账户，开启团队的第一段旅程。",
                  "Create the administrator account to get your team started.",
                )
              : kind === "register"
                ? t(
                    "创建账户，找到属于团队的工作节奏。",
                    "Create an account and find your team's rhythm.",
                  )
                : t(
                    "登录账户，继续推进有意义的工作。",
                    "Sign in to pick up where you left off.",
                  )}
          </p>
          <form onSubmit={submit} className="form-stack">
            {kind === "setup" && (
              <Field label={t("实例名称", "Instance name")}>
                <Input
                  name="instance_name"
                  value={form.instance_name}
                  onChange={(event) =>
                    setForm({ ...form, instance_name: event.target.value })
                  }
                  required
                  maxLength={120}
                />
              </Field>
            )}
            {kind !== "login" && (
              <Field label={t("你的称呼", "Your name")}>
                <Input
                  name="display_name"
                  autoComplete="name"
                  value={form.display_name}
                  onChange={(event) =>
                    setForm({ ...form, display_name: event.target.value })
                  }
                  placeholder={t(
                    "希望大家怎样称呼你？",
                    "How should we call you?",
                  )}
                  required
                  maxLength={100}
                />
              </Field>
            )}
            <Field label={t("电子邮箱", "Email address")}>
              <Input
                type="email"
                name="email"
                autoComplete="email"
                value={form.email}
                onChange={(event) =>
                  setForm({ ...form, email: event.target.value })
                }
                placeholder="you@company.com"
                required
              />
            </Field>
            <Field
              label={t("密码", "Password")}
              hint={
                kind !== "login"
                  ? t(
                      "至少 12 个字符，建议混合使用字母、数字和符号。",
                      "Use at least 12 characters, combining letters, numbers, and symbols.",
                    )
                  : undefined
              }
            >
              <div className="password-input">
                <Input
                  type={showPassword ? "text" : "password"}
                  name="password"
                  autoComplete={
                    kind === "login" ? "current-password" : "new-password"
                  }
                  value={form.password}
                  onChange={(event) =>
                    setForm({ ...form, password: event.target.value })
                  }
                  required
                  minLength={kind === "login" ? 1 : 12}
                />
                <button
                  type="button"
                  onClick={() => setShowPassword(!showPassword)}
                  aria-label={
                    showPassword
                      ? t("隐藏密码", "Hide password")
                      : t("显示密码", "Show password")
                  }
                >
                  {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
                </button>
              </div>
            </Field>
            <ErrorBox message={mutation.error} />
            <Button
              type="submit"
              variant="primary"
              busy={mutation.busy}
              className="auth-submit"
            >
              {kind === "setup"
                ? t("完成设置", "Complete setup")
                : kind === "register"
                  ? t("创建账户", "Create account")
                  : t("登录", "Sign in")}
              <ArrowRight size={16} />
            </Button>
          </form>
          {kind === "login" && (
            <div className="auth-recovery-links">
              <Link to="/forgot-password">
                {t("忘记密码？", "Forgot password?")}
              </Link>
              {appStore.instance?.auth_methods?.includes("magic") && (
                <Link to="/magic-link">
                  {t("邮箱验证码登录", "Use an email code")}
                </Link>
              )}
            </div>
          )}
          {kind === "login" &&
            (appStore.instance?.auth_methods ?? []).filter(
              (method) => !["password", "magic"].includes(method),
            ).length > 0 && (
              <div className="oauth-buttons">
                {appStore
                  .instance!.auth_methods!.filter(
                    (method) => !["password", "magic"].includes(method),
                  )
                  .map((provider) => (
                    <a
                      key={provider}
                      className="button button-secondary button-md"
                      href={`/api/v1/auth/oauth/${encodeURIComponent(provider)}`}
                    >
                      {t("使用", "Continue with")}{" "}
                      {provider.charAt(0).toUpperCase() + provider.slice(1)}
                    </a>
                  ))}
              </div>
            )}
          {kind === "login" && appStore.instance?.registration_enabled && (
            <p className="auth-switch">
              {t("还没有账户？", "New here?")}{" "}
              <Link to="/register">
                {t("创建一个账户", "Create an account")}
              </Link>
            </p>
          )}
          {kind === "register" && (
            <p className="auth-switch">
              {t("已经有账户？", "Already have an account?")}{" "}
              <Link to="/login">{t("前往登录", "Sign in")}</Link>
            </p>
          )}
        </section>
      </main>
      <footer className="auth-footer">
        {t("让专注留下空间。", "Make room for focus.")} <span>My Jira</span>
      </footer>
    </div>
  );
});

export const CreateWorkspacePage = observer(function CreateWorkspacePage() {
  const navigate = useNavigate();
  const mutation = useMutation();
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [slugEdited, setSlugEdited] = useState(false);
  const checkedSlug = useDebouncedValue(slug);
  const availability = useRemote<{ slug: string; available: boolean }>(
    /^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(checkedSlug)
      ? `/workspaces/availability?slug=${encodeURIComponent(checkedSlug)}`
      : null,
  );
  const slugTaken =
    availability.data?.slug === slug && !availability.data.available;
  const t = appStore.t;

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const workspace = await mutation.execute(() =>
      appStore.createWorkspace({
        name,
        slug,
        timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
      }),
    );
    if (workspace) navigate(`/w/${workspace.slug}/home`);
  };

  return (
    <div className="auth-page">
      <div className="auth-top">
        <Brand />
        <AppearanceControls />
      </div>
      <div className="workspace-onboarding">
        <span className="onboarding-icon">
          <Layers3 size={30} />
        </span>
        <p className="eyebrow">
          {t("一个属于团队的空间", "A place for your team")}
        </p>
        <h1>{t("创建工作区", "Create a workspace")}</h1>
        <p className="page-description">
          {t(
            "把相关的项目与伙伴聚在一起，下一步就从这里开始。",
            "Bring your projects and people together. Your next step starts here.",
          )}
        </p>
        <form onSubmit={submit} className="form-stack">
          <Field label={t("工作区名称", "Workspace name")}>
            <Input
              name="workspace_name"
              value={name}
              onChange={(event) => {
                setName(event.target.value);
                if (!slugEdited) setSlug(slugify(event.target.value));
              }}
              placeholder={t("例如：产品研发团队", "e.g. Product team")}
              required
              autoFocus
              maxLength={100}
            />
          </Field>
          <Field
            label={t("工作区地址", "Workspace address")}
            hint={
              slugTaken
                ? t(
                    "此地址已被使用，请换一个。",
                    "This address is already in use.",
                  )
                : t(
                    "使用小写英文字母、数字和连字符。",
                    "Use lowercase letters, numbers, and hyphens.",
                  )
            }
          >
            <div className="input-prefix">
              <span>/w/</span>
              <Input
                name="workspace_slug"
                value={slug}
                onChange={(event) => {
                  setSlugEdited(true);
                  setSlug(event.target.value);
                }}
                pattern="[a-z0-9]+(?:-[a-z0-9]+)*"
                required
                minLength={2}
                maxLength={48}
                placeholder="product-team"
              />
            </div>
          </Field>
          <ErrorBox message={mutation.error} />
          <Button
            type="submit"
            variant="primary"
            busy={mutation.busy}
            disabled={slugTaken}
          >
            {t("创建工作区", "Create workspace")}
            <ArrowRight size={16} />
          </Button>
          {appStore.workspaces.size > 0 && (
            <Button type="button" variant="ghost" onClick={() => navigate(-1)}>
              {t("返回", "Go back")}
            </Button>
          )}
        </form>
      </div>
    </div>
  );
});
