import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
} from "react";
import { api, APIError, errorMessage } from "./api";
import type { APIResponse, Project, WorkItem, Workspace } from "../types";
import { appStore } from "../stores/app-store";

export const ScopeContext = createContext<{
  workspace: Workspace;
  project?: Project;
} | null>(null);

export function useScope() {
  const context = useContext(ScopeContext);
  if (!context) throw new Error("This screen requires a workspace");
  return context;
}

export function useCanEditProject(projectID?: string) {
  const { workspace, project } = useScope();
  const target = projectID ? appStore.projects.get(projectID) : project;
  const role =
    workspace.role === 5
      ? 5
      : workspace.role === 20
        ? 20
        : (target?.role ?? workspace.role);
  return role >= 15 && !target?.archived_at;
}

export function useCanEditWorkItem() {
  const { workspace } = useScope();
  return (item: WorkItem | undefined) => {
    if (!item) return false;
    const project = appStore.projects.get(item.project_id);
    if (project?.archived_at) return false;
    const role =
      workspace.role === 5
        ? 5
        : workspace.role === 20
          ? 20
          : (project?.role ?? workspace.role);
    return role >= 15 || item.created_by === appStore.user?.id;
  };
}

export function useRemote<T>(
  path: string | null,
  refreshKey: number | string = 0,
) {
  const [result, setResult] = useState<APIResponse<T> | null>(null);
  const [resultPath, setResultPath] = useState<string | null>(null);
  const [loading, setLoading] = useState(!!path);
  const [error, setError] = useState("");
  const [errorStatus, setErrorStatus] = useState<number | null>(null);
  const [revision, setRevision] = useState(0);
  const refresh = useCallback(() => setRevision((value) => value + 1), []);

  useEffect(() => {
    setError("");
    setErrorStatus(null);
    if (!path) {
      setResult(null);
      setResultPath(null);
      setLoading(false);
      return;
    }
    const controller = new AbortController();
    setLoading(true);
    setError("");
    api
      .get<T>(path, controller.signal)
      .then((response) => {
        if (!controller.signal.aborted) {
          setResultPath(path);
          setResult(response);
        }
      })
      .catch((cause: unknown) => {
        if (!controller.signal.aborted) {
          setError(errorMessage(cause));
          setErrorStatus(cause instanceof APIError ? cause.status : null);
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [path, revision, refreshKey]);

  const current = resultPath === path ? result : null;
  return {
    data: current?.data ?? null,
    pagination: current?.pagination,
    totalItems: current?.total_items,
    loading: loading || (!!path && resultPath !== path && !error),
    error,
    errorStatus,
    refresh,
    setResult,
  };
}

export function useDebouncedValue<T>(value: T, delay = 300): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timeout = window.setTimeout(() => setDebounced(value), delay);
    return () => window.clearTimeout(timeout);
  }, [value, delay]);
  return debounced;
}

export function useMutation() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const execute = async <T>(
    action: () => Promise<T>,
    success?: string,
  ): Promise<T | undefined> => {
    setBusy(true);
    setError("");
    try {
      const result = await action();
      if (success) appStore.notify(success);
      return result;
    } catch (cause) {
      setError(errorMessage(cause));
      return undefined;
    } finally {
      setBusy(false);
    }
  };
  return { busy, error, execute, setError };
}
