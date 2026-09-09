import { Navigate, useParams } from "react-router-dom";
import { observer } from "mobx-react-lite";
import { appStore } from "../stores/app-store";
import { useRemote, useScope } from "../lib/hooks";
import { workspacePath } from "../lib/api";
import { ErrorBox, Loading } from "../components/ui";
import type { WorkItem } from "../types";

export const WorkItemReferenceRedirect = observer(
  function WorkItemReferenceRedirect() {
    const { workspaceId, projectId, issueId } = useParams();
    const workspace = appStore.workspaces.get(workspaceId ?? "");
    if (!workspace)
      return (
        <ErrorBox
          message={appStore.t(
            "无法访问这个工作区。",
            "This workspace is unavailable.",
          )}
        />
      );
    return (
      <Navigate
        to={`/w/${workspace.slug}/projects/${encodeURIComponent(projectId ?? "")}/issues/${encodeURIComponent(issueId ?? "")}`}
        replace
      />
    );
  },
);

export const WorkItemIdentifierRedirect = observer(
  function WorkItemIdentifierRedirect() {
    const { workspace } = useScope();
    const { identifier } = useParams();
    const issue = useRemote<WorkItem>(
      `${workspacePath(workspace.id)}/issues/lookup/${encodeURIComponent(identifier ?? "")}`,
    );
    if (issue.loading) return <Loading />;
    if (issue.error || !issue.data)
      return (
        <ErrorBox
          message={
            issue.error || appStore.t("找不到工作项", "Work item not found")
          }
        />
      );
    return (
      <Navigate
        to={`/w/${workspace.slug}/projects/${issue.data.project_id}/issues/${issue.data.id}`}
        replace
      />
    );
  },
);
