import { useState, type FormEvent } from "react";
import { observer } from "mobx-react-lite";
import {
  Link,
  useNavigate,
  useParams,
  useSearchParams,
} from "react-router-dom";
import { ArrowLeft, CheckCircle2, KeyRound, Mail, Send } from "lucide-react";
import { appStore } from "../stores/app-store";
import { api, setCSRF } from "../lib/api";
import { useMutation } from "../lib/hooks";
import { Button, ErrorBox, Field, Input } from "../components/ui";
import { AppearanceControls, Brand } from "./auth";

export const AuthRecoveryPage = observer(function AuthRecoveryPage({
  kind,
}: {
  kind: "forgot" | "reset" | "magic" | "verify" | "invitation";
}) {
  const [params] = useSearchParams();
  const { invitationToken } = useParams();
  const navigate = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [token, setToken] = useState(
    params.get("token") ?? invitationToken ?? "",
  );
  const [code, setCode] = useState("");
  const [challenge, setChallenge] = useState("");
  const [done, setDone] = useState(false);
  const mutation = useMutation();
  const t = appStore.t;
  const title =
    kind === "forgot"
      ? t("找回账户访问权限", "Recover your account")
      : kind === "reset"
        ? t("设置新密码", "Set a new password")
        : kind === "magic"
          ? t("使用邮箱验证码登录", "Sign in with an email code")
          : kind === "invitation"
            ? t("加入团队", "Join your team")
            : t("验证电子邮箱", "Verify your email");

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    await mutation.execute(async () => {
      if (kind === "forgot") {
        await api.post("/auth/forgot-password", { email });
        setDone(true);
      }
      if (kind === "reset") {
        await api.post("/auth/reset-password", {
          token,
          new_password: password,
        });
        setDone(true);
      }
      if (kind === "verify") {
        await api.post(
          params.get("change")
            ? "/auth/email-change/confirm"
            : "/auth/email-verification/confirm",
          { token },
        );
        setDone(true);
      }
      if (kind === "invitation") {
        await api.post("/invitations/accept", { token });
        await appStore.loadWorkspaces();
        navigate("/");
      }
      if (kind === "magic") {
        if (!challenge) {
          const result = await api.post<{ challenge_id: string }>(
            "/auth/magic/request",
            { email },
          );
          setChallenge(result.data.challenge_id);
        } else {
          const result = await api.post<{ csrf_token: string }>(
            "/auth/magic/verify",
            { challenge_id: challenge, code },
          );
          setCSRF(result.data.csrf_token);
          await appStore.bootstrap();
          navigate("/");
        }
      }
    });
  };

  return (
    <div className="auth-page">
      <div className="auth-top">
        <Brand />
        <AppearanceControls />
      </div>
      <div className="workspace-onboarding">
        <span className="onboarding-icon">
          {done ? (
            <CheckCircle2 size={29} />
          ) : kind === "reset" ? (
            <KeyRound size={29} />
          ) : (
            <Mail size={29} />
          )}
        </span>
        <h1>{done ? t("下一步已准备好", "Your next step is ready") : title}</h1>
        <p className="page-description">
          {done
            ? kind === "forgot"
              ? t(
                  "如果该邮箱对应有效账户，你将收到一封包含下一步操作的邮件。",
                  "If the email belongs to an active account, you will receive a message with the next steps.",
                )
              : t(
                  "操作已完成，你可以返回工作区。",
                  "You're all set. You can return to your workspace.",
                )
            : kind === "invitation"
              ? t(
                  "确认邀请后，就可以与团队一起推进工作。",
                  "Accept the invitation to start working with your team.",
                )
              : kind === "magic" && challenge
                ? t(
                    "请输入邮件中的六位验证码。",
                    "Enter the six-digit code from your email.",
                  )
                : t(
                    "使用账户邮箱，安全地继续下一步。",
                    "Use your account email to continue securely.",
                  )}
        </p>
        {!done && (
          <form className="form-stack" onSubmit={submit}>
            {(kind === "forgot" || (kind === "magic" && !challenge)) && (
              <Field label={t("电子邮箱", "Email address")}>
                <Input
                  type="email"
                  value={email}
                  onChange={(event) => setEmail(event.target.value)}
                  required
                  autoFocus
                />
              </Field>
            )}
            {["reset", "verify", "invitation"].includes(kind) && (
              <Field
                label={
                  kind === "invitation"
                    ? t("邀请令牌", "Invitation token")
                    : t("验证令牌", "Verification token")
                }
              >
                <Input
                  value={token}
                  onChange={(event) => setToken(event.target.value)}
                  required
                />
              </Field>
            )}
            {kind === "reset" && (
              <Field label={t("新密码", "New password")}>
                <Input
                  type="password"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  minLength={12}
                  required
                  autoComplete="new-password"
                />
              </Field>
            )}
            {kind === "magic" && challenge && (
              <Field label={t("验证码", "Verification code")}>
                <Input
                  value={code}
                  onChange={(event) =>
                    setCode(event.target.value.replace(/\D/g, "").slice(0, 6))
                  }
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  minLength={6}
                  maxLength={6}
                  required
                  autoFocus
                />
              </Field>
            )}
            <ErrorBox message={mutation.error} />
            <Button type="submit" variant="primary" busy={mutation.busy}>
              <Send size={14} />
              {kind === "forgot"
                ? t("发送恢复邮件", "Send recovery email")
                : kind === "invitation"
                  ? t("接受邀请", "Accept invitation")
                  : kind === "magic" && !challenge
                    ? t("发送验证码", "Send code")
                    : t("确认并继续", "Confirm and continue")}
            </Button>
          </form>
        )}
        <Link
          className="inline-property settings-note"
          style={{ marginTop: 25 }}
          to={appStore.user ? "/" : "/login"}
        >
          <ArrowLeft size={14} />
          {appStore.user
            ? t("返回工作区", "Back to workspace")
            : t("返回登录", "Back to sign in")}
        </Link>
      </div>
    </div>
  );
});
