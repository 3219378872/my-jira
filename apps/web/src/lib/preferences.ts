import { useRemote, useScope } from "./hooks";
import { api, workspacePath } from "./api";
import { useEffect, useRef } from "react";

export function useWorkspacePreference<T extends object>(
  key: string,
  defaults: T,
  enabled = true,
) {
  const { workspace } = useScope();
  const path = `${workspacePath(workspace.id)}/preferences/${encodeURIComponent(key)}`;
  const remote = useRemote<{ value: Partial<T> }>(enabled ? path : null);
  const activePath = useRef(path);
  activePath.current = path;
  const pending = useRef<Promise<unknown>>(Promise.resolve());
  const current = useRef({
    path,
    value: { ...defaults, ...remote.data?.value } as T,
  });
  useEffect(() => {
    current.current = {
      path,
      value: { ...defaults, ...remote.data?.value } as T,
    };
  }, [path, remote.data]);
  return {
    value: { ...defaults, ...remote.data?.value } as T,
    loading: remote.loading,
    error: remote.error,
    save: (changes: Partial<T> | ((value: T) => Partial<T>)) => {
      const next = pending.current
        .catch(() => {})
        .then(async () => {
          if (!enabled) return {};
          const value =
            typeof changes === "function"
              ? changes(
                  current.current.path === path
                    ? current.current.value
                    : defaults,
                )
              : changes;
          const result = await api.patch<{ value: Partial<T> }>(path, {
            value,
          });
          if (activePath.current === path) {
            current.current = {
              path,
              value: { ...defaults, ...result.data.value } as T,
            };
            remote.setResult(result);
          }
          return result.data.value;
        });
      pending.current = next;
      return next;
    },
  };
}
