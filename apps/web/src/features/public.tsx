import { lazy, Suspense, useState, type FormEvent } from "react";
import { observer } from "mobx-react-lite";
import { Link, useParams } from "react-router-dom";
import {
  ArrowLeft,
  Check,
  ChevronRight,
  ChevronUp,
  Clock3,
  Globe2,
  Inbox,
  History,
  File,
  MessageSquare,
  Plus,
  Search,
  Send,
  X,
} from "lucide-react";
import { appStore } from "../stores/app-store";
import { api, projectPath, request } from "../lib/api";
import { useMutation, useRemote, useScope } from "../lib/hooks";
import { dateTime, safeHTML } from "../lib/utils";
import {
  Avatar,
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
  PageHeader,
  PriorityIcon,
  Select,
  StateIcon,
} from "../components/ui";
import { AppearanceControls, Brand } from "./auth";
import type { Comment, State, WorkItem } from "../types";
const IntakeEditor = lazy(() =>
  import("../components/editor").then((module) => ({
    default: module.RichEditor,
  })),
);

interface PublicSite {
  slug: string;
  title: string;
  description: string;
  comments_enabled: boolean;
  votes_enabled: boolean;
  reactions_enabled: boolean;
  intake_enabled: boolean;
}
interface PublicIssue extends WorkItem {
  state: State;
  vote_count: number;
  voted?: boolean;
}

export const PublicPage = observer(function PublicPage() {
  const { siteSlug, publicIssueId } = useParams();
  const [search, setSearch] = useState("");
  const [requestOpen, setRequestOpen] = useState(false);
  const [requestName, setRequestName] = useState("");
  const [requestDescription, setRequestDescription] = useState("");
  const [comment, setComment] = useState("");
  const [voted, setVoted] = useState<string[]>([]);
  const [editingComment, setEditingComment] = useState<Comment | null>(null);
  const [editText, setEditText] = useState("");
  const base = `/public/${encodeURIComponent(siteSlug ?? "")}`;
  const site = useRemote<PublicSite>(base);
  const issues = useRemote<PublicIssue[]>(
    `${base}/issues?search=${encodeURIComponent(search)}`,
  );
  const detail = useRemote<PublicIssue>(
    publicIssueId ? `${base}/issues/${publicIssueId}` : null,
  );
  const comments = useRemote<Comment[]>(
    publicIssueId && site.data?.comments_enabled
      ? `${base}/issues/${publicIssueId}/comments`
      : null,
  );
  const mutation = useMutation();
  const reactions = useRemote<
    { emoji: string; count: number; reacted?: boolean }[]
  >(
    publicIssueId && site.data?.reactions_enabled
      ? `${base}/issues/${publicIssueId}/reactions`
      : null,
  );
  const attachments = useRemote<
    { id: string; filename: string; size_bytes: number; download_url: string }[]
  >(publicIssueId ? `${base}/issues/${publicIssueId}/attachments` : null);
  const [reacted, setReacted] = useState<Record<string, boolean>>({});
  const t = appStore.t;
  const vote = async (item: PublicIssue) => {
    if (!appStore.user) {
      appStore.notify(t("登录后可以参与投票", "Sign in to vote"), "info");
      return;
    }
    await mutation.execute(async () => {
      if (item.voted ?? voted.includes(item.id)) {
        await api.delete(`${base}/issues/${item.id}/vote`);
        setVoted(voted.filter((id) => id !== item.id));
      } else {
        await api.post(`${base}/issues/${item.id}/vote`);
        setVoted([...voted, item.id]);
      }
      issues.refresh();
      detail.refresh();
    });
  };
  const plainHTML = (text: string) => {
    const element = document.createElement("div");
    element.textContent = text;
    return `<p>${element.innerHTML.replace(/\n/g, "</p><p>")}</p>`;
  };
  const submitRequest = async (event: FormEvent) => {
    event.preventDefault();
    await mutation.execute(
      async () => {
        await api.post(`${base}/intake`, {
          name: requestName,
          description_html: plainHTML(requestDescription),
        });
        setRequestOpen(false);
        setRequestName("");
        setRequestDescription("");
      },
      t(
        "需求已提交，团队会进行评估",
        "Your request has been submitted for review",
      ),
    );
  };
  return (
    <div className="public-page">
      <header className="public-topbar">
        <Link to={`/public/${siteSlug}`}>
          <Brand />
        </Link>
        <div>
          <AppearanceControls />
          {appStore.user ? (
            <Avatar name={appStore.user.display_name} size="sm" />
          ) : (
            <Link
              className="button button-secondary button-sm"
              to={`/login?next=${encodeURIComponent(window.location.pathname)}`}
            >
              {t("登录", "Sign in")}
            </Link>
          )}
        </div>
      </header>
      <main className="public-content">
        {site.loading ? (
          <Loading />
        ) : site.error ? (
          <EmptyState
            icon={<Globe2 size={30} />}
            title={t("公开页面暂时不可用", "This public page is unavailable")}
            description={site.error}
          />
        ) : (
          site.data && (
            <>
              <section className="public-title">
                <Badge>
                  <Globe2 size={12} />
                  {t("公开项目", "Public project")}
                </Badge>
                <h1>{site.data.title}</h1>
                <p>{site.data.description}</p>
              </section>
              <ErrorBox message={mutation.error} />
              {publicIssueId ? (
                <>
                  <Link
                    className="inline-property settings-note"
                    to={`/public/${siteSlug}`}
                  >
                    <ArrowLeft size={14} />
                    {t("返回工作项列表", "Back to work items")}
                  </Link>
                  <ErrorBox message={detail.error} />
                  {detail.loading ? (
                    <Loading />
                  ) : (
                    detail.data && (
                      <article className="public-issue">
                        <div
                          className="inline-property"
                          style={{ marginBottom: 20 }}
                        >
                          <StateIcon state={detail.data.state} />
                          <Badge>{detail.data.state.name}</Badge>
                          <PriorityIcon priority={detail.data.priority} />
                        </div>
                        <h1>{detail.data.name}</h1>
                        <div
                          className="prose-content"
                          dangerouslySetInnerHTML={{
                            __html: safeHTML(
                              detail.data.description_html ?? "",
                            ),
                          }}
                        />
                        {site.data.votes_enabled && (
                          <Button
                            style={{ marginTop: 24 }}
                            onClick={() => vote(detail.data!)}
                          >
                            <ChevronUp size={15} />
                            {detail.data.vote_count} ·{" "}
                            {(detail.data.voted ??
                            voted.includes(detail.data.id))
                              ? t("取消投票", "Remove vote")
                              : t("支持这个工作项", "Vote for this work")}
                          </Button>
                        )}
                        {site.data.reactions_enabled && (
                          <div className="reaction-bar">
                            <ErrorBox message={reactions.error} />
                            {[
                              ...new Set([
                                "👍",
                                "❤️",
                                "🎉",
                                "👀",
                                ...(reactions.data ?? []).map(
                                  (reaction) => reaction.emoji,
                                ),
                              ]),
                            ].map((emoji) => {
                              const reaction = reactions.data?.find(
                                (entry) => entry.emoji === emoji,
                              );
                              const active =
                                reaction?.reacted ??
                                reacted[`${publicIssueId}:${emoji}`] ??
                                false;
                              return (
                                <button
                                  key={emoji}
                                  className={active ? "active" : ""}
                                  aria-label={`${emoji} ${t("回应", "reaction")}`}
                                  aria-pressed={active}
                                  onClick={() => {
                                    if (!appStore.user) {
                                      appStore.notify(
                                        t("登录后可以回应", "Sign in to react"),
                                        "info",
                                      );
                                      return;
                                    }
                                    mutation.execute(async () => {
                                      await request(
                                        `${base}/issues/${publicIssueId}/reactions`,
                                        {
                                          method: active ? "DELETE" : "POST",
                                          body: JSON.stringify({ emoji }),
                                        },
                                      );
                                      setReacted({
                                        ...reacted,
                                        [`${publicIssueId}:${emoji}`]: !active,
                                      });
                                      reactions.refresh();
                                    });
                                  }}
                                >
                                  {emoji} <span>{reaction?.count || ""}</span>
                                </button>
                              );
                            })}
                          </div>
                        )}
                        <ErrorBox message={attachments.error} />
                        {attachments.data?.map((asset) => (
                          <div className="attachment-row" key={asset.id}>
                            <File size={16} />
                            <a
                              href={asset.download_url}
                              target="_blank"
                              rel="noreferrer"
                            >
                              {asset.filename}
                              <span>
                                {Math.max(
                                  1,
                                  Math.round(asset.size_bytes / 1024),
                                )}{" "}
                                KB
                              </span>
                            </a>
                          </div>
                        ))}
                        {site.data.comments_enabled && (
                          <section className="detail-conversation">
                            <div className="section-title">
                              <h2>
                                <MessageSquare size={16} />
                                {t("公开讨论", "Public discussion")}
                              </h2>
                            </div>
                            <ErrorBox message={comments.error} />
                            {comments.data?.map((entry) => (
                              <article className="comment-item" key={entry.id}>
                                <Avatar
                                  name={entry.author?.display_name ?? "Member"}
                                  size="sm"
                                />
                                <div className="comment-body">
                                  <header>
                                    <strong>
                                      {entry.author?.display_name}
                                    </strong>
                                    <time>
                                      {dateTime(
                                        entry.created_at,
                                        appStore.locale,
                                      )}
                                    </time>
                                    {entry.author_id === appStore.user?.id && (
                                      <Menu
                                        items={[
                                          {
                                            label: t("编辑", "Edit"),
                                            onSelect: () => {
                                              const container =
                                                document.createElement("div");
                                              container.innerHTML = safeHTML(
                                                entry.body_html,
                                              );
                                              setEditText(
                                                container.textContent ?? "",
                                              );
                                              setEditingComment(entry);
                                            },
                                          },
                                          {
                                            label: t("删除", "Delete"),
                                            danger: true,
                                            onSelect: () =>
                                              mutation.execute(async () => {
                                                await api.delete(
                                                  `${base}/issues/${publicIssueId}/comments/${entry.id}`,
                                                );
                                                comments.refresh();
                                              }),
                                          },
                                        ]}
                                      />
                                    )}
                                  </header>
                                  <div
                                    className="prose-content"
                                    dangerouslySetInnerHTML={{
                                      __html: safeHTML(entry.body_html),
                                    }}
                                  />
                                </div>
                              </article>
                            ))}
                            {appStore.user ? (
                              <form
                                className="form-stack"
                                onSubmit={(event) => {
                                  event.preventDefault();
                                  mutation.execute(async () => {
                                    await api.post(
                                      `${base}/issues/${publicIssueId}/comments`,
                                      { body_html: plainHTML(comment) },
                                    );
                                    setComment("");
                                    comments.refresh();
                                  });
                                }}
                              >
                                <textarea
                                  className="input textarea"
                                  value={comment}
                                  onChange={(event) =>
                                    setComment(event.target.value)
                                  }
                                  required
                                  rows={3}
                                  placeholder={t(
                                    "分享你的反馈…",
                                    "Share your feedback…",
                                  )}
                                />
                                <div>
                                  <Button
                                    variant="primary"
                                    type="submit"
                                    busy={mutation.busy}
                                  >
                                    <Send size={13} />
                                    {t("发表评论", "Post comment")}
                                  </Button>
                                </div>
                              </form>
                            ) : (
                              <p className="settings-note">
                                {t(
                                  "登录后可以参与讨论。",
                                  "Sign in to join the discussion.",
                                )}
                              </p>
                            )}
                          </section>
                        )}
                      </article>
                    )
                  )}
                </>
              ) : (
                <>
                  <div className="settings-toolbar">
                    <div className="search-input">
                      <Search size={15} />
                      <Input
                        value={search}
                        onChange={(event) => setSearch(event.target.value)}
                        placeholder={t(
                          "搜索公开工作项…",
                          "Search public work items…",
                        )}
                      />
                    </div>
                    {site.data.intake_enabled && (
                      <Button
                        variant="primary"
                        onClick={() =>
                          appStore.user
                            ? setRequestOpen(true)
                            : appStore.notify(
                                t(
                                  "请先登录，再提交需求",
                                  "Sign in to submit a request",
                                ),
                                "info",
                              )
                        }
                      >
                        <Plus size={14} />
                        {t("提交需求", "Submit a request")}
                      </Button>
                    )}
                  </div>
                  <ErrorBox message={issues.error} />
                  {issues.loading ? (
                    <Loading />
                  ) : !issues.data?.length ? (
                    <EmptyState
                      icon={<Inbox size={30} />}
                      title={t(
                        "还没有公开的工作项",
                        "No public work items yet",
                      )}
                      description={t(
                        "团队发布的工作进展会出现在这里。",
                        "Work published by the team will appear here.",
                      )}
                    />
                  ) : (
                    <div className="public-work-list">
                      {issues.data.map((item) => (
                        <div className="public-work-row" key={item.id}>
                          {site.data!.votes_enabled && (
                            <button
                              className={`vote-button${(item.voted ?? voted.includes(item.id)) ? " active" : ""}`}
                              onClick={() => vote(item)}
                              aria-label={t("投票", "Vote")}
                            >
                              <ChevronUp size={15} />
                              {item.vote_count}
                            </button>
                          )}
                          <StateIcon state={item.state} />
                          <Link to={`/public/${siteSlug}/issues/${item.id}`}>
                            {item.name}
                          </Link>
                          <Badge>{item.state.name}</Badge>
                          <ChevronRight size={14} />
                        </div>
                      ))}
                    </div>
                  )}
                </>
              )}
            </>
          )
        )}
      </main>
      <footer className="public-footer">
        {t("想法汇聚，进展可见。", "Ideas together. Progress in view.")} · My
        Jira
      </footer>
      <Modal
        open={requestOpen}
        onOpenChange={setRequestOpen}
        title={t("提交一个新需求", "Submit a new request")}
      >
        <form className="form-stack modal-body" onSubmit={submitRequest}>
          <Field label={t("标题", "Title")}>
            <Input
              value={requestName}
              onChange={(event) => setRequestName(event.target.value)}
              required
              maxLength={250}
              autoFocus
            />
          </Field>
          <Field label={t("需求说明", "Description")}>
            <textarea
              className="input textarea"
              value={requestDescription}
              onChange={(event) => setRequestDescription(event.target.value)}
              rows={5}
              required
            />
          </Field>
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button variant="primary" type="submit" busy={mutation.busy}>
              {t("提交需求", "Submit request")}
            </Button>
          </div>
        </form>
      </Modal>
      <Modal
        open={!!editingComment}
        onOpenChange={(open) => {
          if (!open) setEditingComment(null);
        }}
        title={t("编辑评论", "Edit comment")}
      >
        <form
          className="form-stack modal-body"
          onSubmit={(event) => {
            event.preventDefault();
            mutation.execute(async () => {
              await api.patch(
                `${base}/issues/${publicIssueId}/comments/${editingComment!.id}`,
                { body_html: plainHTML(editText) },
              );
              setEditingComment(null);
              comments.refresh();
            });
          }}
        >
          <textarea
            className="input textarea"
            value={editText}
            onChange={(event) => setEditText(event.target.value)}
            rows={5}
            required
          />
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button variant="primary" type="submit" busy={mutation.busy}>
              {t("保存评论", "Save comment")}
            </Button>
          </div>
        </form>
      </Modal>
    </div>
  );
});

export const IntakePage = observer(function IntakePage() {
  const { workspace, project } = useScope();
  const base = `${projectPath(workspace.id, project!.id)}/intake`;
  type Intake = {
    id: string;
    status: string;
    source: string;
    created_at: string;
    work_item: WorkItem;
  };
  const requests = useRemote<Intake[]>(base);
  const [filter, setFilter] = useState("pending");
  const [duplicate, setDuplicate] = useState<string | null>(null);
  const [original, setOriginal] = useState("");
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<Intake | null>(null);
  const [form, setForm] = useState({
    name: "",
    description_html: "",
    description_json: {} as unknown,
  });
  const [deleting, setDeleting] = useState<Intake | null>(null);
  const [history, setHistory] = useState<Intake | null>(null);
  const versions = useRemote<
    { id: string; version: number; created_at: string; snapshot: WorkItem }[]
  >(history ? `${base}/${history.id}/versions` : null);
  const role =
    workspace.role === 5 || workspace.role === 20
      ? workspace.role
      : project!.role;
  const isAdmin = role === 20;
  const available = useRemote<WorkItem[]>(
    duplicate ? `${projectPath(workspace.id, project!.id)}/issues` : null,
  );
  const mutation = useMutation();
  const t = appStore.t;
  const start = (item?: Intake) => {
    setEditing(item ?? null);
    setForm({
      name: item?.work_item.name ?? "",
      description_html: item?.work_item.description_html ?? "",
      description_json: item?.work_item.description_json ?? {
        type: "doc",
        content: [{ type: "paragraph" }],
      },
    });
    setFormOpen(true);
  };
  const resolve = (id: string, status: string, extra = {}) =>
    mutation.execute(
      async () => {
        await api.post(`${base}/${id}/resolve`, {
          status,
          version: requests.data?.find((item) => item.id === id)?.work_item
            .version,
          ...extra,
        });
        requests.refresh();
        if (status === "duplicate") setDuplicate(null);
      },
      t("需求已更新", "Request updated"),
    );
  const tabs = [
    { id: "pending", zh: "待评估", en: "Pending" },
    { id: "accepted", zh: "已接受", en: "Accepted" },
    { id: "snoozed", zh: "稍后处理", en: "Snoozed" },
    { id: "rejected", zh: "已拒绝", en: "Rejected" },
    { id: "duplicate", zh: "重复", en: "Duplicate" },
  ];
  const items = requests.data?.filter((item) => item.status === filter) ?? [];
  return (
    <div className="page-scroll">
      <PageHeader
        title={t("需求收集", "Intake")}
        description={t(
          "让外部反馈经过讨论，成为清晰可执行的工作。",
          "Turn outside feedback into clear, actionable work.",
        )}
        actions={
          <Button variant="primary" onClick={() => start()}>
            <Plus size={14} />
            {t("提交需求", "Submit request")}
          </Button>
        }
      >
        <div className="page-tabs">
          {tabs.map((tab) => (
            <button
              key={tab.id}
              className={filter === tab.id ? "active" : ""}
              onClick={() => setFilter(tab.id)}
            >
              {t(tab.zh, tab.en)}
            </button>
          ))}
        </div>
      </PageHeader>
      <div className="page-body">
        <ErrorBox message={requests.error || mutation.error} />
        {requests.loading ? (
          <Loading />
        ) : !items.length ? (
          <EmptyState
            icon={<Inbox size={30} />}
            title={t("这里暂时没有需求", "No requests here")}
            description={t(
              "开启公开分享中的需求收集后，反馈会进入这里等待评估。",
              "Enable intake in public sharing to collect requests for your team to review.",
            )}
          />
        ) : (
          <div className="view-list">
            {items.map((item) => (
              <div className="view-row" key={item.id}>
                <Inbox size={17} />
                <div className="flex-spacer">
                  {item.status === "accepted" ? (
                    <Link
                      to={`/w/${workspace.slug}/projects/${project!.id}/issues/${item.work_item.id}`}
                    >
                      <strong>{item.work_item.name}</strong>
                    </Link>
                  ) : (
                    <strong>{item.work_item.name}</strong>
                  )}
                  <p className="settings-note">
                    {dateTime(item.created_at, appStore.locale)} · {item.source}
                  </p>
                  <div
                    className="prose-content"
                    dangerouslySetInnerHTML={{
                      __html: safeHTML(item.work_item.description_html ?? ""),
                    }}
                  />
                </div>
                <Menu
                  items={[
                    ...(isAdmin ||
                    item.work_item.created_by === appStore.user?.id
                      ? [
                          {
                            label: t("编辑需求", "Edit request"),
                            onSelect: () => start(item),
                          },
                          {
                            label: t("删除需求", "Delete request"),
                            danger: true,
                            onSelect: () => setDeleting(item),
                          },
                        ]
                      : []),
                    {
                      label: t("版本历史", "Version history"),
                      icon: <History size={14} />,
                      onSelect: () => setHistory(item),
                    },
                    ...(isAdmin
                      ? [
                          {
                            label: t("接受需求", "Accept request"),
                            icon: <Check size={14} />,
                            onSelect: () => {
                              resolve(item.id, "accepted");
                            },
                          },
                          {
                            label: t("明天再处理", "Snooze until tomorrow"),
                            icon: <Clock3 size={14} />,
                            onSelect: () => {
                              resolve(item.id, "snoozed", {
                                snoozed_until: new Date(
                                  Date.now() + 86400000,
                                ).toISOString(),
                              });
                            },
                          },
                          {
                            label: t("标为重复", "Mark duplicate"),
                            onSelect: () => setDuplicate(item.id),
                          },
                          {
                            label: t("拒绝", "Reject"),
                            icon: <X size={14} />,
                            danger: true,
                            onSelect: () => {
                              resolve(item.id, "rejected");
                            },
                          },
                          ...(item.status !== "pending"
                            ? [
                                {
                                  label: t("重新评估", "Return to pending"),
                                  onSelect: () => resolve(item.id, "pending"),
                                },
                              ]
                            : []),
                        ]
                      : []),
                  ]}
                />
              </div>
            ))}
          </div>
        )}
      </div>
      <Modal
        open={!!duplicate}
        onOpenChange={(value) => {
          if (!value) setDuplicate(null);
        }}
        title={t("标记重复需求", "Mark duplicate request")}
      >
        <div className="form-stack modal-body">
          <Field label={t("原始工作项", "Original work item")}>
            <Select
              value={original}
              onChange={(event) => setOriginal(event.target.value)}
            >
              <option value="">{t("选择工作项", "Select work item")}</option>
              {available.data?.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name}
                </option>
              ))}
            </Select>
          </Field>
          <ErrorBox message={available.error || mutation.error} />
          <div className="modal-footer">
            <Button
              variant="primary"
              disabled={!original}
              busy={mutation.busy}
              onClick={async () => {
                await resolve(duplicate!, "duplicate", {
                  duplicate_of: original,
                });
              }}
            >
              {t("确认", "Confirm")}
            </Button>
          </div>
        </div>
      </Modal>
      <Modal
        open={formOpen}
        onOpenChange={setFormOpen}
        title={
          editing
            ? t("编辑需求", "Edit request")
            : t("提交新需求", "Submit a new request")
        }
      >
        <form
          className="form-stack modal-body"
          onSubmit={(event) => {
            event.preventDefault();
            mutation.execute(
              async () => {
                if (editing)
                  await api.patch(`${base}/${editing.id}`, {
                    ...form,
                    version: editing.work_item.version,
                  });
                else await api.post(base, form);
                setFormOpen(false);
                requests.refresh();
              },
              editing
                ? t("需求已保存", "Request saved")
                : t("需求已提交", "Request submitted"),
            );
          }}
        >
          <Field label={t("标题", "Title")}>
            <Input
              value={form.name}
              onChange={(event) =>
                setForm({ ...form, name: event.target.value })
              }
              required
              maxLength={255}
              autoFocus
            />
          </Field>
          <Field label={t("需求说明", "Description")}>
            <Suspense fallback={<Loading />}>
              <IntakeEditor
                value={form.description_html}
                onChange={(html, json) =>
                  setForm({
                    ...form,
                    description_html: html,
                    description_json: json,
                  })
                }
                projectId={project!.id}
                workItemId={editing?.work_item.id}
              />
            </Suspense>
          </Field>
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button variant="primary" type="submit" busy={mutation.busy}>
              {editing ? t("保存", "Save") : t("提交", "Submit")}
            </Button>
          </div>
        </form>
      </Modal>
      <Confirm
        open={!!deleting}
        onOpenChange={(open) => {
          if (!open) setDeleting(null);
        }}
        title={t("删除此需求？", "Delete this request?")}
        description={t(
          "此需求及关联工作项将从列表移除。",
          "This request and its associated work item will be removed.",
        )}
        busy={mutation.busy}
        onConfirm={() =>
          mutation.execute(async () => {
            await api.delete(`${base}/${deleting!.id}`);
            setDeleting(null);
            requests.refresh();
          })
        }
      />
      <Modal
        open={!!history}
        onOpenChange={(open) => {
          if (!open) setHistory(null);
        }}
        title={t("需求版本历史", "Request version history")}
      >
        <div className="form-stack modal-body">
          <ErrorBox message={versions.error} />
          {versions.loading ? (
            <Loading />
          ) : (
            versions.data?.map((version) => (
              <article className="settings-panel" key={version.id}>
                <div className="settings-panel-heading">
                  <Badge>v{version.version}</Badge>
                  <time>{dateTime(version.created_at, appStore.locale)}</time>
                </div>
                <div className="settings-panel-body">
                  <strong>{version.snapshot.name}</strong>
                  <div
                    className="prose-content"
                    dangerouslySetInnerHTML={{
                      __html: safeHTML(version.snapshot.description_html ?? ""),
                    }}
                  />
                </div>
              </article>
            ))
          )}
        </div>
      </Modal>
    </div>
  );
});
