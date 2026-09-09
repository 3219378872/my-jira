import { useEffect } from "react";
import { workspacePath } from "./api";
import { useRemote, useScope } from "./hooks";

export interface Bookmark {
  id: string;
  entity_id: string;
  entity_type: string;
  position: number;
  entity: { name: string; project_id?: string | null; id?: string };
}

export function bookmarkRoute(slug: string, bookmark: Bookmark) {
  const base = `/w/${slug}`;
  if (bookmark.entity_type === "project")
    return `${base}/projects/${bookmark.entity_id}/issues`;
  const project = bookmark.entity.project_id
    ? `/projects/${bookmark.entity.project_id}`
    : "";
  const collection: Record<string, string> = {
    issue: "issues",
    cycle: "cycles",
    module: "modules",
    view: "views",
    page: "pages",
  };
  return `${base}${project}/${collection[bookmark.entity_type] ?? "home"}/${bookmark.entity_id}`;
}

export function useFavorites() {
  const { workspace } = useScope();
  const remote = useRemote<Bookmark[]>(
    `${workspacePath(workspace.id)}/favorites`,
  );
  useEffect(() => {
    window.addEventListener("myjira:favorites-changed", remote.refresh);
    return () =>
      window.removeEventListener("myjira:favorites-changed", remote.refresh);
  }, [remote.refresh]);
  return remote;
}
