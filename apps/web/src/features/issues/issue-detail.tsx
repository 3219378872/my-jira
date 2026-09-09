import { useEffect, useState, type ChangeEvent, type FormEvent } from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { observer } from "mobx-react-lite";
import { FavoriteButton } from "../../components/favorite-button";
import { Link, useNavigate } from "react-router-dom";
import {
  Archive,
  ArrowRight,
  Bell,
  Check,
  ChevronRight,
  Copy,
  ExternalLink,
  File,
  GitBranch,
  History,
  Link2,
  Maximize2,
  MessageSquare,
  Users,
  Paperclip,
  Plus,
  Send,
  Trash2,
  X,
} from "lucide-react";
import { appStore } from "../../stores/app-store";
import {
  useCanEditProject,
  useCanEditWorkItem,
  useMutation,
  useRemote,
  useScope,
} from "../../lib/hooks";
import { api, errorMessage, projectPath, request } from "../../lib/api";
import { dateTime, memberName, priorities, safeHTML } from "../../lib/utils";
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
  priorityLabels,
  Select,
} from "../../components/ui";
import { RichEditor } from "../../components/editor";
import { IssueForm } from "./issue-form";
import type {
  Activity,
  Comment,
  Cycle,
  EstimateScheme,
  Module,
  Reaction,
  WorkItem,
} from "../../types";

interface Attachment {
  id: string;
  filename: string;
  size_bytes: number;
  download_url?: string;
  url?: string;
  metadata?: { public?: boolean };
}
interface IssueLink {
  id: string;
  title: string;
  url: string;
}
interface IssueRelation {
  id: string;
  target_id: string;
  relation_type: string;
  target?: WorkItem;
}

export const IssueDetail = observer(function IssueDetail({
  issueId,
  onChanged,
}: {
  issueId: string;
  onChanged?: () => void;
}) {
  const { workspace, project } = useScope();
  const navigate = useNavigate();
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [tab, setTab] = useState("comments");
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [descriptionJSON, setDescriptionJSON] = useState<unknown>({});
  const [descriptionDirty, setDescriptionDirty] = useState(false);
  const [comment, setComment] = useState("");
  const [commentJSON, setCommentJSON] = useState<unknown>({});
  const [replyTo, setReplyTo] = useState<Comment | null>(null);
  const [showDeletedFiles, setShowDeletedFiles] = useState(false);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [childOpen, setChildOpen] = useState(false);
  const [moveOpen, setMoveOpen] = useState(false);
  const [moveProject, setMoveProject] = useState("");
  const [linkOpen, setLinkOpen] = useState(false);
  const [linkForm, setLinkForm] = useState({ title: "", url: "https://" });
  const [relationOpen, setRelationOpen] = useState(false);
  const [relationType, setRelationType] = useState("relates_to");
  const [relationTarget, setRelationTarget] = useState("");
  const [subscribersOpen, setSubscribersOpen] = useState(false);
  const [subscriberID, setSubscriberID] = useState("");
  const mutation = useMutation();
  const item = appStore.issues.get(issueId);
  const canEdit = useCanEditWorkItem()(item);
  const canManage = useCanEditProject(item?.project_id);
  const projectId = project?.id ?? item?.project_id ?? "";
  const base = projectPath(workspace.id, projectId);
  const itemBase = `${base}/issues/${issueId}`;
  const comments = useRemote<Comment[]>(`${itemBase}/comments`);
  const activities = useRemote<Activity[]>(
    tab === "activity" ? `${itemBase}/activities` : null,
  );
  const attachments = useRemote<Attachment[]>(`${itemBase}/attachments`);
  const deletedFiles = useRemote<Attachment[]>(
    showDeletedFiles
      ? `${base}/assets?deleted=true&work_item_id=${issueId}`
      : null,
  );
  const estimateSettings = useRemote<{
    estimate_id: string | null;
    estimate: EstimateScheme | null;
  }>(`${base}/estimate-settings`);
  const reactions = useRemote<Reaction[]>(`${itemBase}/reactions`);
  const versions = useRemote<
    {
      id: string;
      version: number;
      created_at: string;
      snapshot: WorkItem;
      data?: WorkItem;
    }[]
  >(historyOpen ? `${itemBase}/versions` : null);
  const links = useRemote<IssueLink[]>(`${itemBase}/links`);
  const relations = useRemote<IssueRelation[]>(`${itemBase}/relations`);
  const subscribers = useRemote<{ user_id: string; display_name: string }[]>(
    `${itemBase}/subscribers`,
  );
  const cycles = useRemote<Cycle[]>(`${base}/cycles`);
  const modules = useRemote<Module[]>(`${base}/modules`);
  const states = appStore.states.get(projectId) ?? [];
  const members = appStore.members.get(projectId) ?? [];
  const labels = appStore.labels.get(projectId) ?? [];
  const t = appStore.t;
  const canManageSubscribers =
    (workspace.role === 5
      ? 5
      : workspace.role === 20
        ? 20
        : (project?.role ?? workspace.role)) >= 15;
  const isSubscribed =
    subscribers.data?.some(
      (subscriber) => subscriber.user_id === appStore.user?.id,
    ) ?? false;
  const close = () =>
    navigate(`/w/${workspace.slug}/projects/${projectId}/issues`);

  useEffect(() => {
    setLoading(true);
    setLoadError("");
    setDescriptionDirty(false);
    appStore
      .loadIssue(workspace.id, projectId, issueId)
      .then((result) => {
        setTitle(result.name);
        setDescription(result.description_html ?? "");
        setDescriptionJSON(result.description_json ?? {});
      })
      .catch((cause) => setLoadError(errorMessage(cause)))
      .finally(() => setLoading(false));
  }, [workspace.id, projectId, issueId]);

  const update = async (patch: Partial<WorkItem>) => {
    const result = await mutation.execute(() =>
      appStore.updateIssue(issueId, patch),
    );
    if (result) {
      onChanged?.();
      activities.refresh();
    }
    return result;
  };

  const saveDescription = async () => {
    if (
      await update({
        description_html: description,
        description_json: descriptionJSON,
      })
    ) {
      setDescriptionDirty(false);
      appStore.notify(t("描述已保存", "Description saved"));
    }
  };

  const submitComment = async (event: FormEvent) => {
    event.preventDefault();
    if (!comment.replace(/<[^>]*>/g, "").trim()) return;
    await mutation.execute(async () => {
      await api.post(`${itemBase}/comments`, {
        body_html: comment,
        body_json: commentJSON,
        parent_id: replyTo?.id ?? null,
      });
      setComment("");
      setCommentJSON({});
      setReplyTo(null);
      comments.refresh();
    });
  };

  const upload = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) return;
    const form = new FormData();
    form.append("file", file);
    await mutation.execute(
      async () => {
        await request(`${itemBase}/attachments`, {
          method: "POST",
          body: form,
        });
        attachments.refresh();
      },
      t("附件已上传", "Attachment uploaded"),
    );
    event.target.value = "";
  };

  const addLink = async (event: FormEvent) => {
    event.preventDefault();
    await mutation.execute(async () => {
      await api.post(`${itemBase}/links`, linkForm);
      links.refresh();
      setLinkOpen(false);
      setLinkForm({ title: "", url: "https://" });
    });
  };

  const deleteItem = () => {
    mutation.execute(async () => {
      await appStore.deleteIssue(issueId);
      setDeleteOpen(false);
      onChanged?.();
      close();
      appStore.notify(t("工作项已删除", "Work item deleted"));
    });
  };

  return (
    <>
      <DialogPrimitive.Root
        open
        onOpenChange={(open) => {
          if (!open) close();
        }}
      >
        <DialogPrimitive.Portal>
          <DialogPrimitive.Overlay className="detail-overlay" />
          <DialogPrimitive.Content
            className="detail-drawer"
            aria-describedby={undefined}
          >
            <div className="detail-topbar">
              <div className="breadcrumbs">
                <span
                  className="project-color"
                  style={{ backgroundColor: project?.color || "#6875cf" }}
                />
                {project?.identifier}
                <ChevronRight size={12} />
                <DialogPrimitive.Title>
                  {project?.identifier}-{item?.sequence_id ?? "…"}
                </DialogPrimitive.Title>
              </div>
              <div className="detail-topbar-actions">
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={t("复制链接", "Copy link")}
                  onClick={() => {
                    navigator.clipboard
                      .writeText(window.location.href)
                      .then(() =>
                        appStore.notify(t("链接已复制", "Link copied")),
                      )
                      .catch((cause) =>
                        appStore.notify(errorMessage(cause), "error"),
                      );
                  }}
                >
                  <Link2 size={15} />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={t("在新标签页打开", "Open in new tab")}
                  onClick={() =>
                    window.open(
                      window.location.href,
                      "_blank",
                      "noopener,noreferrer",
                    )
                  }
                >
                  <Maximize2 size={15} />
                </Button>
                <FavoriteButton entityType="issue" entityId={issueId} />
                <Menu
                  items={[
                    {
                      label: t("复制编号链接", "Copy identifier link"),
                      icon: <Link2 size={14} />,
                      onSelect: () => {
                        navigator.clipboard
                          .writeText(
                            `${window.location.origin}/w/${workspace.slug}/browse/${project?.identifier}-${item?.sequence_id}`,
                          )
                          .then(() =>
                            appStore.notify(
                              t("编号链接已复制", "Identifier link copied"),
                            ),
                          )
                          .catch((cause) =>
                            appStore.notify(errorMessage(cause), "error"),
                          );
                      },
                    },
                    ...(item?.is_draft
                      ? [
                          {
                            label: t("发布草稿", "Publish draft"),
                            disabled: !canManage,
                            onSelect: () => update({ is_draft: false }),
                          },
                        ]
                      : []),
                    {
                      label: t("复制工作项", "Duplicate work item"),
                      disabled: !canManage,
                      icon: <Copy size={14} />,
                      onSelect: () => {
                        mutation.execute(async () => {
                          const result = await api.post<WorkItem>(
                            `${itemBase}/duplicate`,
                          );
                          onChanged?.();
                          navigate(
                            `/w/${workspace.slug}/projects/${projectId}/issues/${result.data.id}`,
                          );
                        });
                      },
                    },
                    {
                      label: t("移动到其他项目", "Move to another project"),
                      disabled: !canManage,
                      icon: <ArrowRight size={14} />,
                      onSelect: () => setMoveOpen(true),
                    },
                    {
                      label: item?.archived_at
                        ? t("恢复工作项", "Restore work item")
                        : t("归档工作项", "Archive work item"),
                      icon: <Archive size={14} />,
                      disabled: !canEdit,
                      onSelect: () => {
                        update({
                          archived_at: item?.archived_at
                            ? null
                            : new Date().toISOString(),
                        });
                      },
                    },
                    {
                      label: t("版本历史", "Version history"),
                      icon: <History size={14} />,
                      onSelect: () => setHistoryOpen(true),
                    },
                    {
                      label: t("删除工作项", "Delete work item"),
                      disabled: !canEdit,
                      icon: <Trash2 size={14} />,
                      danger: true,
                      separator: true,
                      onSelect: () => setDeleteOpen(true),
                    },
                  ]}
                />
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={t("关闭详情", "Close details")}
                  onClick={close}
                >
                  <X size={18} />
                </Button>
              </div>
            </div>
            {loading ? (
              <Loading />
            ) : loadError ? (
              <ErrorBox message={loadError} />
            ) : (
              item && (
                <div className="detail-body">
                  <div className="detail-main">
                    <div className="detail-title-section">
                      {item.archived_at && (
                        <Badge>
                          <Archive size={12} />
                          {t("已归档", "Archived")}
                        </Badge>
                      )}
                      <textarea
                        readOnly={!canEdit}
                        className="detail-title-input"
                        aria-label={t("工作项标题", "Work item title")}
                        value={title}
                        onChange={(event) => setTitle(event.target.value)}
                        onBlur={() => {
                          if (title.trim() && title !== item.name)
                            update({ name: title.trim() });
                        }}
                        rows={2}
                        maxLength={250}
                      />
                      <RichEditor
                        editable={canEdit}
                        workItemId={issueId}
                        value={description}
                        onChange={(html, json) => {
                          setDescription(html);
                          setDescriptionJSON(json);
                          setDescriptionDirty(true);
                        }}
                        placeholder={t(
                          "添加描述，让协作更清楚…",
                          "Add a description to make collaboration clearer…",
                        )}
                        compact
                      />
                      {descriptionDirty && (
                        <div className="description-save">
                          <span>
                            {t(
                              "描述有未保存的更改",
                              "Description has unsaved changes",
                            )}
                          </span>
                          <Button
                            variant="primary"
                            size="sm"
                            busy={mutation.busy}
                            onClick={saveDescription}
                          >
                            {t("保存描述", "Save description")}
                          </Button>
                        </div>
                      )}
                    </div>
                    <div className="detail-related">
                      <div className="detail-section-heading">
                        <h3>
                          <GitBranch size={15} />
                          {t("子工作项", "Sub-items")}
                        </h3>
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={!canManage}
                          onClick={() => setChildOpen(true)}
                        >
                          <Plus size={13} />
                          {t("添加", "Add")}
                        </Button>
                      </div>
                      {appStore
                        .projectIssues(projectId)
                        .filter((issue) => issue.parent_id === item.id)
                        .map((child) => (
                          <Link
                            key={child.id}
                            to={`/w/${workspace.slug}/projects/${projectId}/issues/${child.id}`}
                            className="subitem-row"
                          >
                            <span className="issue-identifier">
                              {project?.identifier}-{child.sequence_id}
                            </span>
                            <span>{child.name}</span>
                            <ChevronRight size={13} />
                          </Link>
                        ))}
                      {item.parent_id && (
                        <Link
                          className="parent-link"
                          to={`/w/${workspace.slug}/projects/${projectId}/issues/${item.parent_id}`}
                        >
                          <GitBranch size={13} />
                          {t("查看父工作项", "View parent work item")}
                        </Link>
                      )}
                      <div className="detail-section-heading">
                        <h3>
                          <Link2 size={15} />
                          {t("关联工作项", "Relations")}
                        </h3>
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={!canManage}
                          onClick={() => setRelationOpen(true)}
                        >
                          <Plus size={13} />
                          {t("添加", "Add")}
                        </Button>
                      </div>
                      <ErrorBox message={relations.error} />
                      {relations.data?.map((relation) => (
                        <div key={relation.id} className="related-row">
                          <Badge>{relation.relation_type}</Badge>
                          <Link
                            to={`/w/${workspace.slug}/projects/${projectId}/issues/${relation.target_id}`}
                          >
                            {relation.target?.name ??
                              relation.target_id.slice(0, 8)}
                          </Link>
                          <Button
                            variant="ghost"
                            size="icon"
                            disabled={!canManage}
                            aria-label={t("删除关联", "Remove relation")}
                            onClick={() => {
                              mutation.execute(async () => {
                                await api.delete(
                                  `${itemBase}/relations/${relation.id}`,
                                );
                                relations.refresh();
                              });
                            }}
                          >
                            <X size={13} />
                          </Button>
                        </div>
                      ))}
                      <div className="detail-section-heading">
                        <h3>
                          <Paperclip size={15} />
                          {t("附件与链接", "Attachments & links")}
                        </h3>
                        <div className="inline-actions">
                          <Button
                            size="sm"
                            variant="ghost"
                            onClick={() => setLinkOpen(true)}
                          >
                            <Link2 size={13} />
                            {t("链接", "Link")}
                          </Button>
                          <label className="button button-ghost button-sm upload-button">
                            <Paperclip size={13} />
                            {t("上传", "Upload")}
                            <input
                              type="file"
                              onChange={upload}
                              disabled={mutation.busy}
                            />
                          </label>
                        </div>
                      </div>
                      <ErrorBox message={attachments.error || links.error} />
                      {attachments.data?.map((attachment) => (
                        <div key={attachment.id} className="attachment-row">
                          <File size={17} />
                          <a
                            href={
                              attachment.download_url ||
                              attachment.url ||
                              `/api/v1${base}/assets/${attachment.id}/download`
                            }
                            target="_blank"
                            rel="noreferrer"
                          >
                            {attachment.filename}
                            <span>
                              {Math.max(
                                1,
                                Math.round(attachment.size_bytes / 1024),
                              )}{" "}
                              KB
                            </span>
                          </a>
                          <Menu
                            items={[
                              {
                                label: attachment.metadata?.public
                                  ? t("取消公开附件", "Make attachment private")
                                  : t(
                                      "允许在公开页面下载",
                                      "Allow public download",
                                    ),
                                onSelect: () =>
                                  mutation.execute(async () => {
                                    await api.patch(
                                      `${base}/assets/${attachment.id}`,
                                      { public: !attachment.metadata?.public },
                                    );
                                    attachments.refresh();
                                  }),
                              },
                              {
                                label: t("重命名附件", "Rename attachment"),
                                onSelect: () => {
                                  const filename = window.prompt(
                                    t("附件名称", "Attachment name"),
                                    attachment.filename,
                                  );
                                  if (filename?.trim())
                                    mutation.execute(async () => {
                                      await api.patch(
                                        `${base}/assets/${attachment.id}`,
                                        { filename: filename.trim() },
                                      );
                                      attachments.refresh();
                                    });
                                },
                              },
                              {
                                label: t("删除附件", "Delete attachment"),
                                danger: true,
                                onSelect: () =>
                                  mutation.execute(async () => {
                                    await api.delete(
                                      `${base}/assets/${attachment.id}`,
                                    );
                                    attachments.refresh();
                                    deletedFiles.refresh();
                                  }),
                              },
                            ]}
                          />
                        </div>
                      ))}
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => setShowDeletedFiles(!showDeletedFiles)}
                      >
                        {t("已删除的附件", "Deleted attachments")}
                      </Button>
                      {showDeletedFiles && (
                        <>
                          <ErrorBox message={deletedFiles.error} />
                          {deletedFiles.data?.map((asset) => (
                            <div key={asset.id} className="attachment-row">
                              <File size={16} />
                              <span className="flex-spacer">
                                {asset.filename}
                              </span>
                              <Button
                                size="sm"
                                busy={mutation.busy}
                                onClick={() =>
                                  mutation.execute(async () => {
                                    await api.post(
                                      `${base}/assets/${asset.id}/restore`,
                                    );
                                    attachments.refresh();
                                    deletedFiles.refresh();
                                  })
                                }
                              >
                                {t("恢复", "Restore")}
                              </Button>
                            </div>
                          ))}
                          {!deletedFiles.loading &&
                            !deletedFiles.data?.length && (
                              <p className="settings-note">
                                {t(
                                  "没有可恢复的附件。",
                                  "No attachments to restore.",
                                )}
                              </p>
                            )}
                        </>
                      )}
                      {links.data?.map((link) => (
                        <div key={link.id} className="attachment-row">
                          <ExternalLink size={16} />
                          <a
                            href={
                              /^https?:\/\//i.test(link.url) ? link.url : "#"
                            }
                            target="_blank"
                            rel="noreferrer"
                          >
                            {link.title || link.url}
                            <span>{link.url}</span>
                          </a>
                          <Button
                            variant="ghost"
                            size="icon"
                            aria-label={t("删除链接", "Delete link")}
                            onClick={() => {
                              mutation.execute(async () => {
                                await api.delete(
                                  `${itemBase}/links/${link.id}`,
                                );
                                links.refresh();
                              });
                            }}
                          >
                            <X size={13} />
                          </Button>
                        </div>
                      ))}
                    </div>
                    <ErrorBox message={mutation.error} />
                    <div className="detail-conversation">
                      <div className="conversation-tabs">
                        <button
                          className={tab === "comments" ? "active" : ""}
                          onClick={() => setTab("comments")}
                        >
                          <MessageSquare size={14} />
                          {t("评论", "Comments")}
                          <Badge>{comments.data?.length ?? 0}</Badge>
                        </button>
                        <button
                          className={tab === "activity" ? "active" : ""}
                          onClick={() => setTab("activity")}
                        >
                          {t("活动记录", "Activity")}
                        </button>
                        <span className="flex-spacer" />
                        <Button
                          size="icon"
                          variant="ghost"
                          aria-label={t("管理订阅者", "Manage subscribers")}
                          onClick={() => setSubscribersOpen(true)}
                        >
                          <Users size={14} />
                        </Button>
                        <Button
                          variant={isSubscribed ? "secondary" : "ghost"}
                          size="sm"
                          onClick={() => {
                            mutation.execute(async () => {
                              if (isSubscribed)
                                await api.delete(`${itemBase}/subscribers`);
                              else
                                await api.post(`${itemBase}/subscribers`, {
                                  user_id: appStore.user!.id,
                                });
                              subscribers.refresh();
                            });
                          }}
                        >
                          <Bell size={13} />
                          {isSubscribed
                            ? t("已订阅", "Subscribed")
                            : t("订阅", "Subscribe")}
                        </Button>
                      </div>
                      <ReactionBar
                        reactions={reactions.data ?? []}
                        itemBase={itemBase}
                        refresh={reactions.refresh}
                      />
                      {tab === "comments" ? (
                        <>
                          <ErrorBox message={comments.error} />
                          {comments.loading ? (
                            <Loading />
                          ) : (
                            comments.data?.map((entry) => (
                              <CommentItem
                                workItemId={issueId}
                                key={entry.id}
                                comment={entry}
                                itemBase={itemBase}
                                members={members}
                                refresh={comments.refresh}
                                onReply={setReplyTo}
                              />
                            ))
                          )}
                          <form
                            className="comment-composer"
                            onSubmit={submitComment}
                          >
                            <Avatar
                              name={appStore.user!.display_name}
                              size="sm"
                            />
                            <div>
                              {replyTo && (
                                <div className="inline-property settings-note">
                                  {t("回复", "Replying to")}{" "}
                                  {replyTo.author?.display_name ||
                                    memberName(
                                      members.find(
                                        (member) =>
                                          member.user_id === replyTo.author_id,
                                      ) ?? {},
                                    )}
                                  <Button
                                    type="button"
                                    variant="ghost"
                                    size="icon"
                                    aria-label={t("取消回复", "Cancel reply")}
                                    onClick={() => setReplyTo(null)}
                                  >
                                    <X size={12} />
                                  </Button>
                                </div>
                              )}
                              <RichEditor
                                workItemId={issueId}
                                value={comment}
                                onChange={(html, json) => {
                                  setComment(html);
                                  setCommentJSON(json);
                                }}
                                compact
                                placeholder={t(
                                  "分享进展、提问或留下建议…",
                                  "Share an update, ask a question, or add a thought…",
                                )}
                              />
                              <div className="comment-send">
                                <Button
                                  type="submit"
                                  size="sm"
                                  variant="primary"
                                  busy={mutation.busy}
                                  disabled={
                                    !comment.replace(/<[^>]*>/g, "").trim()
                                  }
                                >
                                  <Send size={13} />
                                  {t("发送评论", "Comment")}
                                </Button>
                              </div>
                            </div>
                          </form>
                        </>
                      ) : (
                        <>
                          <ErrorBox message={activities.error} />
                          {activities.loading ? (
                            <Loading />
                          ) : (
                            <div className="activity-list">
                              {activities.data?.map((activity) => (
                                <div key={activity.id} className="activity-row">
                                  <span className="activity-dot" />
                                  <div>
                                    <span>
                                      {memberName(
                                        members.find(
                                          (member) =>
                                            member.user_id ===
                                            activity.actor_id,
                                        ) ?? {},
                                      )}
                                    </span>{" "}
                                    {activity.action}{" "}
                                    {activity.field_name && (
                                      <Badge>{activity.field_name}</Badge>
                                    )}
                                    <time>
                                      {dateTime(
                                        activity.created_at,
                                        appStore.locale,
                                      )}
                                    </time>
                                  </div>
                                </div>
                              ))}
                            </div>
                          )}
                        </>
                      )}
                    </div>
                  </div>
                  <fieldset
                    disabled={!canEdit}
                    className="detail-properties"
                    style={{
                      margin: 0,
                      minWidth: 0,
                      borderTop: 0,
                      borderRight: 0,
                      borderBottom: 0,
                    }}
                  >
                    <h3>{t("属性", "Properties")}</h3>
                    <Field label={t("状态", "State")}>
                      <Select
                        value={item.state_id}
                        onChange={(event) =>
                          update({ state_id: event.target.value })
                        }
                      >
                        {states.map((state) => (
                          <option key={state.id} value={state.id}>
                            {state.name}
                          </option>
                        ))}
                      </Select>
                    </Field>
                    <Field label={t("优先级", "Priority")}>
                      <Select
                        value={item.priority}
                        onChange={(event) =>
                          update({
                            priority: event.target
                              .value as WorkItem["priority"],
                          })
                        }
                      >
                        {priorities.map((priority) => (
                          <option value={priority} key={priority}>
                            {t(...priorityLabels[priority])}
                          </option>
                        ))}
                      </Select>
                    </Field>
                    <Field label={t("负责人", "Assignees")}>
                      <MultiSelect
                        value={item.assignee_ids ?? []}
                        onChange={(value) => update({ assignee_ids: value })}
                        options={members.map((member) => ({
                          value: member.user_id,
                          label: memberName(member),
                        }))}
                        placeholder={t("未分配", "Unassigned")}
                      />
                    </Field>
                    <Field label={t("标签", "Labels")}>
                      <MultiSelect
                        value={item.label_ids ?? []}
                        onChange={(value) => update({ label_ids: value })}
                        options={labels.map((label) => ({
                          value: label.id,
                          label: label.name,
                          color: label.color,
                        }))}
                        placeholder={t("添加标签", "Add labels")}
                      />
                    </Field>
                    <Field label={t("迭代周期", "Cycle")}>
                      <Select
                        value={item.cycle_id ?? ""}
                        onChange={(event) =>
                          update({ cycle_id: event.target.value || null })
                        }
                      >
                        <option value="">{t("未设置", "None")}</option>
                        {cycles.data
                          ?.filter((cycle) => !cycle.archived_at)
                          .map((cycle) => (
                            <option key={cycle.id} value={cycle.id}>
                              {cycle.name}
                            </option>
                          ))}
                      </Select>
                    </Field>
                    <Field label={t("功能模块", "Modules")}>
                      <MultiSelect
                        value={item.module_ids ?? []}
                        onChange={(value) => update({ module_ids: value })}
                        options={(modules.data ?? []).map((module) => ({
                          value: module.id,
                          label: module.name,
                        }))}
                        placeholder={t("添加模块", "Add modules")}
                      />
                    </Field>
                    <Field label={t("开始日期", "Start date")}>
                      <Input
                        type="date"
                        value={item.start_date ?? ""}
                        onChange={(event) =>
                          update({ start_date: event.target.value || null })
                        }
                      />
                    </Field>
                    <Field label={t("目标日期", "Due date")}>
                      <Input
                        type="date"
                        value={item.target_date ?? ""}
                        onChange={(event) =>
                          update({ target_date: event.target.value || null })
                        }
                      />
                    </Field>
                    <Field label={t("预估点数", "Estimate")}>
                      {estimateSettings.data?.estimate ? (
                        <Select
                          aria-label={t("估算值", "Estimate value")}
                          value={item.estimate_point_id ?? ""}
                          onChange={(event) =>
                            update({
                              estimate_point_id: event.target.value || null,
                            })
                          }
                        >
                          <option value="">—</option>
                          {item.estimate_point_id &&
                            !estimateSettings.data.estimate.points.some(
                              (point) => point.id === item.estimate_point_id,
                            ) && (
                              <option value={item.estimate_point_id}>
                                {item.estimate_point_detail?.label ??
                                  item.estimate ??
                                  "—"}{" "}
                                · {t("历史方案", "Previous scheme")}
                              </option>
                            )}
                          {estimateSettings.data.estimate.points.map(
                            (point) => (
                              <option key={point.id} value={point.id}>
                                {point.label}
                              </option>
                            ),
                          )}
                        </Select>
                      ) : (
                        <Input
                          type="number"
                          min={0}
                          max={9999}
                          step={0.5}
                          value={item.estimate ?? ""}
                          onChange={(event) =>
                            update({
                              estimate: event.target.value
                                ? Number(event.target.value)
                                : null,
                            })
                          }
                          placeholder="—"
                        />
                      )}
                    </Field>
                    <div className="detail-timestamps">
                      <p>
                        {t("创建于", "Created")}
                        <span>
                          {dateTime(item.created_at, appStore.locale)}
                        </span>
                      </p>
                      <p>
                        {t("更新于", "Updated")}
                        <span>
                          {dateTime(item.updated_at, appStore.locale)}
                        </span>
                      </p>
                    </div>
                  </fieldset>
                </div>
              )
            )}
          </DialogPrimitive.Content>
        </DialogPrimitive.Portal>
      </DialogPrimitive.Root>
      <Modal
        open={subscribersOpen}
        onOpenChange={setSubscribersOpen}
        title={t("工作项订阅者", "Work item subscribers")}
      >
        <div className="modal-body form-stack">
          <ErrorBox message={subscribers.error || mutation.error} />
          {subscribers.data?.map((subscriber) => (
            <div className="catalog-row" key={subscriber.user_id}>
              <span className="flex-spacer">{subscriber.display_name}</span>
              {(canManageSubscribers ||
                subscriber.user_id === appStore.user?.id) && (
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() =>
                    mutation.execute(async () => {
                      await api.delete(
                        `${itemBase}/subscribers/${subscriber.user_id}`,
                      );
                      subscribers.refresh();
                    })
                  }
                >
                  {t("移除", "Remove")}
                </Button>
              )}
            </div>
          ))}
          {!subscribers.data?.length && (
            <p className="settings-note">
              {t("还没有订阅者。", "No subscribers yet.")}
            </p>
          )}
          {canManageSubscribers && (
            <form
              className="form-row"
              onSubmit={(event) => {
                event.preventDefault();
                mutation.execute(async () => {
                  await api.post(`${itemBase}/subscribers`, {
                    user_id: subscriberID,
                  });
                  setSubscriberID("");
                  subscribers.refresh();
                });
              }}
            >
              <Field label={t("添加订阅者", "Add subscriber")}>
                <Select
                  value={subscriberID}
                  onChange={(event) => setSubscriberID(event.target.value)}
                  required
                >
                  <option value="">{t("选择成员", "Choose member")}</option>
                  {members
                    .filter(
                      (member) =>
                        !subscribers.data?.some(
                          (subscriber) => subscriber.user_id === member.user_id,
                        ),
                    )
                    .map((member) => (
                      <option key={member.user_id} value={member.user_id}>
                        {member.display_name || member.email}
                      </option>
                    ))}
                </Select>
              </Field>
              <Button
                type="submit"
                busy={mutation.busy}
                disabled={!subscriberID}
              >
                {t("添加", "Add")}
              </Button>
            </form>
          )}
        </div>
      </Modal>
      <Confirm
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title={t("删除这个工作项？", "Delete this work item?")}
        description={t(
          "工作项将从项目中移除。请确认已保存需要保留的信息。",
          "This work item will be removed from the project. Keep any information you need before continuing.",
        )}
        onConfirm={deleteItem}
        busy={mutation.busy}
      />
      <IssueForm
        projectId={childOpen ? projectId : null}
        parentId={issueId}
        onClose={() => setChildOpen(false)}
      />
      <Modal
        open={linkOpen}
        onOpenChange={setLinkOpen}
        title={t("添加相关链接", "Add a related link")}
      >
        <form onSubmit={addLink} className="form-stack modal-body">
          <Field label={t("标题", "Title")}>
            <Input
              value={linkForm.title}
              onChange={(event) =>
                setLinkForm({ ...linkForm, title: event.target.value })
              }
              required
            />
          </Field>
          <Field label={t("链接地址", "URL")}>
            <Input
              type="url"
              value={linkForm.url}
              onChange={(event) =>
                setLinkForm({ ...linkForm, url: event.target.value })
              }
              pattern="https?://.*"
              required
            />
          </Field>
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button type="submit" variant="primary" busy={mutation.busy}>
              {t("添加链接", "Add link")}
            </Button>
          </div>
        </form>
      </Modal>
      <Modal
        open={historyOpen}
        onOpenChange={setHistoryOpen}
        title={t("工作项版本历史", "Work item version history")}
      >
        <div className="modal-body form-stack">
          <ErrorBox message={versions.error} />
          {versions.loading ? (
            <Loading />
          ) : (
            versions.data?.map((version) => (
              <article key={version.id} className="settings-panel">
                <div className="settings-panel-heading">
                  <Badge>v{version.version}</Badge>
                  <time>{dateTime(version.created_at, appStore.locale)}</time>
                </div>
                <div className="settings-panel-body">
                  <strong>{(version.snapshot ?? version.data)?.name}</strong>
                  <div
                    className="prose-content"
                    dangerouslySetInnerHTML={{
                      __html: safeHTML(
                        (version.snapshot ?? version.data)?.description_html ??
                          "",
                      ),
                    }}
                  />
                </div>
              </article>
            ))
          )}
        </div>
      </Modal>
      <Modal
        open={moveOpen}
        onOpenChange={setMoveOpen}
        title={t("移动工作项", "Move work item")}
      >
        <div className="form-stack modal-body">
          <Field label={t("目标项目", "Destination project")}>
            <Select
              value={moveProject}
              onChange={(event) => setMoveProject(event.target.value)}
            >
              <option value="">{t("选择项目", "Select a project")}</option>
              {appStore
                .workspaceProjects(workspace.id)
                .filter((candidate) => candidate.id !== projectId)
                .map((candidate) => (
                  <option key={candidate.id} value={candidate.id}>
                    {candidate.name}
                  </option>
                ))}
            </Select>
          </Field>
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button
              variant="primary"
              busy={mutation.busy}
              disabled={!moveProject}
              onClick={() => {
                mutation.execute(async () => {
                  const result = await api.post<WorkItem>(`${itemBase}/move`, {
                    project_id: moveProject,
                    version: item?.version,
                  });
                  setMoveOpen(false);
                  onChanged?.();
                  navigate(
                    `/w/${workspace.slug}/projects/${moveProject}/issues/${result.data.id}`,
                  );
                });
              }}
            >
              {t("移动", "Move")}
            </Button>
          </div>
        </div>
      </Modal>
      <Modal
        open={relationOpen}
        onOpenChange={setRelationOpen}
        title={t("关联工作项", "Link work items")}
      >
        <div className="form-stack modal-body">
          <Field label={t("关系类型", "Relation type")}>
            <Select
              value={relationType}
              onChange={(event) => setRelationType(event.target.value)}
            >
              <option value="relates_to">{t("相关", "Relates to")}</option>
              <option value="blocks">{t("阻塞", "Blocks")}</option>
              <option value="blocked_by">{t("被阻塞", "Blocked by")}</option>
              <option value="duplicates">{t("重复", "Duplicates")}</option>
            </Select>
          </Field>
          <Field label={t("工作项", "Work item")}>
            <Select
              value={relationTarget}
              onChange={(event) => setRelationTarget(event.target.value)}
            >
              <option value="">{t("选择工作项", "Select work item")}</option>
              {appStore
                .projectIssues(projectId)
                .filter((candidate) => candidate.id !== issueId)
                .map((candidate) => (
                  <option key={candidate.id} value={candidate.id}>
                    {project?.identifier}-{candidate.sequence_id} ·{" "}
                    {candidate.name}
                  </option>
                ))}
            </Select>
          </Field>
          <ErrorBox message={mutation.error} />
          <div className="modal-footer">
            <Button
              variant="primary"
              disabled={!relationTarget}
              busy={mutation.busy}
              onClick={() => {
                mutation.execute(async () => {
                  await api.post(`${itemBase}/relations`, {
                    target_id: relationTarget,
                    relation_type: relationType,
                  });
                  relations.refresh();
                  setRelationOpen(false);
                });
              }}
            >
              {t("添加关联", "Add relation")}
            </Button>
          </div>
        </div>
      </Modal>
    </>
  );
});

const CommentItem = observer(function CommentItem({
  workItemId,
  comment,
  itemBase,
  members,
  refresh,
  onReply,
}: {
  workItemId: string;
  comment: Comment;
  itemBase: string;
  members: { user_id: string; display_name: string; email: string }[];
  refresh: () => void;
  onReply: (comment: Comment) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(comment.body_html);
  const [valueJSON, setValueJSON] = useState<unknown>({});
  const mutation = useMutation();
  const t = appStore.t;
  const author =
    comment.author?.display_name ||
    memberName(
      members.find((member) => member.user_id === comment.author_id) ?? {},
    );
  return (
    <article
      className={
        comment.parent_id ? "comment-item comment-reply" : "comment-item"
      }
    >
      <Avatar name={author} size="sm" />
      <div className="comment-body">
        <header>
          <strong>{author}</strong>
          <time>{dateTime(comment.created_at, appStore.locale)}</time>
          {comment.edited_at && (
            <span className="text-muted">{t("已编辑", "edited")}</span>
          )}
          {comment.author_id === appStore.user?.id && (
            <Menu
              items={[
                { label: t("编辑", "Edit"), onSelect: () => setEditing(true) },
                {
                  label: t("删除", "Delete"),
                  danger: true,
                  onSelect: () => {
                    mutation.execute(async () => {
                      await api.delete(`${itemBase}/comments/${comment.id}`);
                      refresh();
                    });
                  },
                },
              ]}
            />
          )}
        </header>
        {editing ? (
          <>
            <RichEditor
              workItemId={workItemId}
              value={value}
              onChange={(html, json) => {
                setValue(html);
                setValueJSON(json);
              }}
              compact
            />
            <div className="inline-actions">
              <Button size="sm" onClick={() => setEditing(false)}>
                {t("取消", "Cancel")}
              </Button>
              <Button
                size="sm"
                variant="primary"
                busy={mutation.busy}
                onClick={() => {
                  mutation.execute(async () => {
                    await api.patch(`${itemBase}/comments/${comment.id}`, {
                      body_html: value,
                      body_json: valueJSON,
                    });
                    setEditing(false);
                    refresh();
                  });
                }}
              >
                <Check size={13} />
                {t("保存", "Save")}
              </Button>
            </div>
          </>
        ) : (
          <div
            className="prose-content"
            dangerouslySetInnerHTML={{ __html: safeHTML(comment.body_html) }}
          />
        )}
        <div className="inline-actions">
          <Button variant="ghost" size="sm" onClick={() => onReply(comment)}>
            {t("回复", "Reply")}
          </Button>
          <ReactionBar
            reactions={comment.reactions ?? []}
            itemBase={itemBase}
            commentID={comment.id}
            refresh={refresh}
          />
        </div>
        <ErrorBox message={mutation.error} />
      </div>
    </article>
  );
});

const ReactionBar = observer(function ReactionBar({
  reactions,
  itemBase,
  commentID,
  refresh,
}: {
  reactions: Reaction[];
  itemBase: string;
  commentID?: string;
  refresh: () => void;
}) {
  const mutation = useMutation();
  return (
    <div className="reaction-bar">
      {[
        ...new Set([
          "👍",
          "❤️",
          "🎉",
          "👀",
          ...reactions.map((reaction) => reaction.emoji),
        ]),
      ].map((emoji) => {
        const matches = reactions.filter(
          (reaction) => reaction.emoji === emoji,
        );
        const own = matches.find(
          (reaction) => reaction.user_id === appStore.user?.id,
        );
        return (
          <button
            key={emoji}
            aria-label={`${emoji} ${appStore.t("回应", "reaction")}`}
            aria-pressed={!!own}
            className={own ? "active" : ""}
            disabled={mutation.busy}
            onClick={() =>
              mutation.execute(async () => {
                if (own) await api.delete(`${itemBase}/reactions/${own.id}`);
                else
                  await api.post(`${itemBase}/reactions`, {
                    emoji,
                    comment_id: commentID,
                  });
                refresh();
              })
            }
          >
            {emoji} <span>{matches.length || ""}</span>
          </button>
        );
      })}
      <ErrorBox message={mutation.error} />
    </div>
  );
});
