import { useEffect, useState } from "react";
import { observer } from "mobx-react-lite";
import { Link, useParams } from "react-router-dom";
import {
  Activity,
  ArrowRight,
  CalendarDays,
  Download,
  FolderKanban,
  UserRound,
} from "lucide-react";
import { appStore } from "../stores/app-store";
import { useRemote, useScope } from "../lib/hooks";
import { workspacePath } from "../lib/api";
import { dateTime, shortDate } from "../lib/utils";
import {
  Avatar,
  Badge,
  Button,
  EmptyState,
  ErrorBox,
  Input,
  Loading,
  PageHeader,
  PriorityIcon,
  Select,
} from "../components/ui";
import type { User, WorkItem } from "../types";

interface ProfileStatistics {
  created: number;
  assigned: number;
  pending: number;
  completed: number;
  subscribed: number;
  by_project: {
    project_id: string;
    name: string;
    identifier: string;
    created: number;
    assigned: number;
    pending: number;
    completed: number;
  }[];
  cycles: {
    id: string;
    project_id: string;
    name: string;
    start_date: string | null;
    end_date: string | null;
    status: string;
    total: number;
    completed: number;
  }[];
}
interface ProfileActivity {
  id: string;
  created_at: string;
  project_id: string;
  work_item_id: string;
  work_item_name: string;
  sequence_id: number;
  identifier: string;
  action: string;
  field_name: string;
}

export const MemberProfilePage = observer(function MemberProfilePage() {
  const { workspace } = useScope();
  const { userId = "me" } = useParams();
  const base = `${workspacePath(workspace.id)}/profiles/${encodeURIComponent(userId)}`;
  const profile = useRemote<{
    user: User & { joined_at: string };
    stats: ProfileStatistics;
  }>(base);
  const [tab, setTab] = useState("activity");
  const [projectID, setProjectID] = useState("");
  const [from, setFrom] = useState(
    new Date(Date.now() - 30 * 86400000).toISOString().slice(0, 10),
  );
  const [to, setTo] = useState(new Date().toISOString().slice(0, 10));
  const [offset, setOffset] = useState(0);
  const [issueCursor, setIssueCursor] = useState<string | null>(null);
  const [previousCursors, setPreviousCursors] = useState<(string | null)[]>([]);
  useEffect(() => {
    setIssueCursor(null);
    setPreviousCursors([]);
  }, [workspace.id, userId, tab, projectID]);
  const query = new URLSearchParams({
    from,
    to,
    limit: "50",
    offset: String(offset),
  });
  if (projectID) query.set("project_id", projectID);
  const activity = useRemote<{
    items: ProfileActivity[];
    total: number;
    has_more: boolean;
  }>(tab === "activity" ? `${base}/activities?${query}` : null);
  const issueQuery = new URLSearchParams({
    limit: "100",
    [tab === "created"
      ? "created_by"
      : tab === "subscribed"
        ? "subscriber_id"
        : "assignee_id"]: profile.data?.user.id ?? "me",
  });
  if (projectID) issueQuery.set("project_id", projectID);
  if (issueCursor) issueQuery.set("cursor", issueCursor);
  const issues = useRemote<WorkItem[]>(
    tab !== "activity" && profile.data
      ? `${workspacePath(workspace.id)}/issues?${issueQuery}`
      : null,
  );
  const t = appStore.t;
  const member = profile.data?.user;
  const stats = profile.data?.stats;
  return (
    <div className="page-scroll">
      <PageHeader
        title={member?.display_name ?? t("成员主页", "Member profile")}
        description={
          member
            ? t(
                `加入于 ${shortDate(member.joined_at, appStore.locale)} · ${member.timezone}`,
                `Joined ${shortDate(member.joined_at, appStore.locale)} · ${member.timezone}`,
              )
            : undefined
        }
        eyebrow={t("团队成员", "Team member")}
        actions={
          <Avatar
            name={member?.display_name ?? ""}
            src={member?.avatar_url}
            size="lg"
          />
        }
      />
      <div className="page-body">
        <ErrorBox message={profile.error} retry={profile.refresh} />
        {profile.loading ? (
          <Loading />
        ) : (
          stats && (
            <>
              <div className="analytics-stat-grid">
                {[
                  { key: "assigned", zh: "负责的工作", en: "Assigned" },
                  { key: "pending", zh: "待完成", en: "Pending" },
                  { key: "completed", zh: "已完成", en: "Completed" },
                  { key: "created", zh: "创建的工作", en: "Created" },
                ].map((stat) => (
                  <div className="analytics-stat" key={stat.key}>
                    <span>
                      <UserRound size={14} />
                      {t(stat.zh, stat.en)}
                    </span>
                    <strong>
                      {
                        stats[
                          stat.key as
                            "assigned" | "pending" | "completed" | "created"
                        ]
                      }
                    </strong>
                  </div>
                ))}
              </div>
              <div className="profile-overview-grid">
                <section className="home-panel">
                  <header>
                    <FolderKanban size={15} />
                    <h2>{t("参与的项目", "Projects")}</h2>
                  </header>
                  {stats.by_project.map((project) => (
                    <Link
                      className="catalog-row"
                      key={project.project_id}
                      to={`/w/${workspace.slug}/projects/${project.project_id}/issues`}
                    >
                      <strong className="flex-spacer">{project.name}</strong>
                      <span className="text-muted">
                        {project.completed}/{project.assigned}
                      </span>
                      <ArrowRight size={13} />
                    </Link>
                  ))}
                </section>
                <section className="home-panel">
                  <header>
                    <CalendarDays size={15} />
                    <h2>
                      {t("当前与即将开始的周期", "Current and upcoming cycles")}
                    </h2>
                  </header>
                  {stats.cycles.map((cycle) => (
                    <Link
                      className="catalog-row"
                      key={cycle.id}
                      to={`/w/${workspace.slug}/projects/${cycle.project_id}/cycles/${cycle.id}`}
                    >
                      <div className="flex-spacer">
                        <strong>{cycle.name}</strong>
                        <p className="settings-note">
                          {shortDate(cycle.start_date, appStore.locale)} —{" "}
                          {shortDate(cycle.end_date, appStore.locale)}
                        </p>
                      </div>
                      <Badge>
                        {cycle.completed}/{cycle.total}
                      </Badge>
                    </Link>
                  ))}
                  {!stats.cycles.length && (
                    <p className="settings-note">
                      {t("暂无进行中的周期。", "No active cycles.")}
                    </p>
                  )}
                </section>
              </div>
              <div className="page-tabs">
                {[
                  { key: "activity", zh: "活动", en: "Activity" },
                  { key: "assigned", zh: "负责的工作", en: "Assigned" },
                  { key: "created", zh: "创建的工作", en: "Created" },
                  { key: "subscribed", zh: "订阅的工作", en: "Following" },
                ].map((item) => (
                  <button
                    className={tab === item.key ? "active" : ""}
                    key={item.key}
                    onClick={() => {
                      setTab(item.key);
                      setOffset(0);
                    }}
                  >
                    {t(item.zh, item.en)}
                  </button>
                ))}
              </div>
              <div className="settings-toolbar">
                <Select
                  aria-label={t("筛选项目", "Filter project")}
                  value={projectID}
                  onChange={(event) => {
                    setProjectID(event.target.value);
                    setOffset(0);
                  }}
                >
                  <option value="">{t("所有项目", "All projects")}</option>
                  {appStore.workspaceProjects(workspace.id).map((project) => (
                    <option key={project.id} value={project.id}>
                      {project.name}
                    </option>
                  ))}
                </Select>
                {tab === "activity" && (
                  <>
                    <Input
                      type="date"
                      aria-label={t("开始日期", "From date")}
                      value={from}
                      max={to || undefined}
                      onChange={(event) => {
                        setFrom(event.target.value);
                        setOffset(0);
                      }}
                    />
                    <Input
                      type="date"
                      aria-label={t("结束日期", "To date")}
                      value={to}
                      min={from || undefined}
                      onChange={(event) => {
                        setTo(event.target.value);
                        setOffset(0);
                      }}
                    />
                    <a
                      className="button button-secondary button-md"
                      href={`/api/v1${base}/activities/export?${query}`}
                    >
                      <Download size={14} />
                      {t("导出活动", "Export activity")}
                    </a>
                  </>
                )}
              </div>
              <ErrorBox message={activity.error || issues.error} />
              {tab === "activity" ? (
                activity.loading ? (
                  <Loading />
                ) : (
                  <>
                    {activity.data?.items.map((entry) => (
                      <Link
                        className="profile-activity-row"
                        key={entry.id}
                        to={`/w/${workspace.slug}/projects/${entry.project_id}/issues/${entry.work_item_id}`}
                      >
                        <Activity size={14} />
                        <div>
                          <strong>{entry.work_item_name}</strong>
                          <p className="settings-note">
                            {entry.identifier}-{entry.sequence_id} ·{" "}
                            {entry.action.replace(/_/g, " ")}
                            {entry.field_name ? ` · ${entry.field_name}` : ""}
                          </p>
                        </div>
                        <time>
                          {dateTime(entry.created_at, appStore.locale)}
                        </time>
                      </Link>
                    ))}
                    {!activity.data?.items.length && (
                      <EmptyState
                        icon={<Activity size={26} />}
                        title={t(
                          "这段时间没有活动",
                          "No activity in this period",
                        )}
                        description={t(
                          "调整时间范围查看其他记录。",
                          "Choose another date range to see more activity.",
                        )}
                      />
                    )}
                    <div className="pagination-footer">
                      <span>
                        {activity.data?.total ?? 0} {t("条活动", "activities")}
                      </span>
                      <Button
                        size="sm"
                        disabled={!offset}
                        onClick={() => setOffset(Math.max(0, offset - 50))}
                      >
                        {t("上一页", "Previous")}
                      </Button>
                      <Button
                        size="sm"
                        disabled={!activity.data?.has_more}
                        onClick={() => setOffset(offset + 50)}
                      >
                        {t("下一页", "Next")}
                      </Button>
                    </div>
                  </>
                )
              ) : issues.loading ? (
                <Loading />
              ) : (
                <>
                  <div className="view-list">
                    {issues.data?.map((item) => (
                      <Link
                        className="workspace-issue-row"
                        key={item.id}
                        to={`/w/${workspace.slug}/projects/${item.project_id}/issues/${item.id}`}
                      >
                        <PriorityIcon priority={item.priority} />
                        <span className="issue-identifier">
                          {appStore.projects.get(item.project_id)?.identifier}-
                          {item.sequence_id}
                        </span>
                        <span>{item.name}</span>
                        <ArrowRight size={13} />
                      </Link>
                    ))}
                    {!issues.data?.length && (
                      <EmptyState
                        icon={<FolderKanban size={26} />}
                        title={t("这里还没有工作项", "No work items here")}
                        description={t(
                          "选择其他项目或标签页查看工作。",
                          "Choose another project or tab to see work.",
                        )}
                      />
                    )}
                  </div>
                  <div className="pagination-footer">
                    <span>
                      {issues.pagination?.total ?? issues.data?.length ?? 0}{" "}
                      {t("个工作项", "work items")}
                    </span>
                    <Button
                      size="sm"
                      disabled={!previousCursors.length}
                      onClick={() => {
                        setIssueCursor(previousCursors.at(-1) ?? null);
                        setPreviousCursors(previousCursors.slice(0, -1));
                      }}
                    >
                      {t("上一页", "Previous")}
                    </Button>
                    <Button
                      size="sm"
                      disabled={
                        !issues.pagination?.has_more ||
                        !issues.pagination.next_cursor
                      }
                      onClick={() => {
                        setPreviousCursors([...previousCursors, issueCursor]);
                        setIssueCursor(issues.pagination?.next_cursor ?? null);
                      }}
                    >
                      {t("下一页", "Next")}
                    </Button>
                  </div>
                </>
              )}
            </>
          )
        )}
      </div>
    </div>
  );
});
