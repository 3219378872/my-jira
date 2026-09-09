import { useEffect } from "react";
import {
  NodeViewWrapper,
  ReactNodeViewRenderer,
  type NodeViewProps,
} from "@tiptap/react";
import { originalEditorBlocks, WorkItemReference } from "@myjira/editor-schema";
import { ExternalLink, ListTodo, LockKeyhole } from "lucide-react";
import { appStore } from "../stores/app-store";
import { projectPath } from "../lib/api";
import { useRemote } from "../lib/hooks";
import { PriorityIcon } from "./ui";
import type { WorkItem } from "../types";

function WorkItemCard({ node, selected }: NodeViewProps) {
  const { workspaceId, projectId, issueId } = node.attrs as Record<
    string,
    string
  >;
  const valid = [workspaceId, projectId, issueId].every((value) =>
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
      value ?? "",
    ),
  );
  const issue = useRemote<WorkItem>(
    valid
      ? `${projectPath(workspaceId, projectId)}/issues/${encodeURIComponent(issueId)}`
      : null,
  );
  useEffect(() => {
    const refresh = () => {
      if (document.visibilityState === "visible") issue.refresh();
    };
    const timer = window.setInterval(refresh, 30000);
    window.addEventListener("focus", refresh);
    return () => {
      window.clearInterval(timer);
      window.removeEventListener("focus", refresh);
    };
  }, [issue.refresh]);
  const t = appStore.t;
  const allowed = valid && !issue.error && !!issue.data;
  return (
    <NodeViewWrapper
      className={`editor-work-item${selected ? " is-selected" : ""}`}
      contentEditable={false}
    >
      {allowed ? (
        <a
          href={`/go/work-item/${workspaceId}/${projectId}/${issueId}`}
          target="_blank"
          rel="noreferrer"
        >
          <ListTodo size={17} />
          <div>
            <small>
              {appStore.projects.get(projectId)?.identifier ??
                t("工作项", "Work item")}
              -{issue.data!.sequence_id}
            </small>
            <strong>{issue.data!.name}</strong>
          </div>
          <PriorityIcon priority={issue.data!.priority} />
          <ExternalLink size={13} />
        </a>
      ) : (
        <div className="editor-work-item-unavailable">
          <LockKeyhole size={16} />
          <span>
            {issue.loading
              ? t("正在获取工作项…", "Loading work item…")
              : t("工作项不可访问或已移除", "Work item unavailable or removed")}
          </span>
        </div>
      )}
    </NodeViewWrapper>
  );
}

export const webEditorBlocks = originalEditorBlocks.map((extension) =>
  extension.name === "workItem"
    ? WorkItemReference.extend({
        addNodeView() {
          return ReactNodeViewRenderer(WorkItemCard);
        },
      })
    : extension,
);
