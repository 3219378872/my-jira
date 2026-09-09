import type { APIResponse } from "../types";

export class APIError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code: string,
    readonly fields?: Record<string, string[]>,
  ) {
    super(message);
    this.name = "APIError";
  }
}

let csrfToken = "";
let csrfRequest: Promise<string> | undefined;

function cookieToken(): string {
  const cookie = document.cookie
    .split("; ")
    .find((entry) => entry.startsWith("mj_csrf="));
  return cookie ? decodeURIComponent(cookie.slice("mj_csrf=".length)) : "";
}

export async function obtainCSRF(): Promise<string> {
  if (!csrfRequest) {
    csrfRequest = fetch("/api/v1/auth/csrf", { credentials: "include" })
      .then(async (response) => {
        if (!response.ok)
          throw new APIError(
            "Could not establish a secure session",
            response.status,
            "csrf_failed",
          );
        const result = (await response.json()) as APIResponse<{
          csrf_token: string;
        }>;
        csrfToken = result.data.csrf_token;
        return csrfToken;
      })
      .finally(() => {
        csrfRequest = undefined;
      });
  }
  return csrfRequest;
}

export function setCSRF(token: string): void {
  csrfToken = token;
}

export async function request<T>(
  path: string,
  options: RequestInit = {},
): Promise<APIResponse<T>> {
  const method = options.method?.toUpperCase() ?? "GET";
  const headers = new Headers(options.headers);
  if (options.body && !(options.body instanceof FormData))
    headers.set("Content-Type", "application/json");
  if (!["GET", "HEAD", "OPTIONS"].includes(method)) {
    const token = cookieToken() || csrfToken || (await obtainCSRF());
    headers.set("X-CSRF-Token", token);
  }
  let response: Response;
  try {
    response = await fetch(`/api/v1${path}`, {
      ...options,
      headers,
      credentials: "include",
    });
  } catch {
    throw new APIError(
      "Cannot connect to the server. Check your connection and try again.",
      0,
      "network_error",
    );
  }
  const changedFavorites = () => {
    if (
      !["GET", "HEAD", "OPTIONS"].includes(method) &&
      /\/favorites(?:\/|$)/.test(path)
    )
      window.dispatchEvent(new Event("myjira:favorites-changed"));
  };
  if (response.status === 204) {
    changedFavorites();
    return { data: undefined as T };
  }
  const contentType = response.headers.get("content-type") ?? "";
  if (!contentType.includes("application/json")) {
    throw new APIError(
      `The server returned an unexpected response (${response.status}).`,
      response.status,
      "invalid_response",
    );
  }
  const result = await response.json();
  if (!response.ok) {
    throw new APIError(
      result.error?.message ?? `Request failed (${response.status})`,
      response.status,
      result.error?.code ?? "request_failed",
      result.error?.fields,
    );
  }
  changedFavorites();
  return result as APIResponse<T>;
}

export const api = {
  get: <T>(path: string, signal?: AbortSignal) => request<T>(path, { signal }),
  post: <T>(path: string, data: unknown = {}) =>
    request<T>(path, { method: "POST", body: JSON.stringify(data) }),
  patch: <T>(path: string, data: unknown) =>
    request<T>(path, { method: "PATCH", body: JSON.stringify(data) }),
  put: <T>(path: string, data: unknown) =>
    request<T>(path, { method: "PUT", body: JSON.stringify(data) }),
  delete: (path: string) => request<void>(path, { method: "DELETE" }),
};

export const workspacePath = (workspaceId: string) =>
  `/workspaces/${encodeURIComponent(workspaceId)}`;
export const projectPath = (workspaceId: string, projectId: string) =>
  `${workspacePath(workspaceId)}/projects/${encodeURIComponent(projectId)}`;

export function errorMessage(error: unknown): string {
  return error instanceof Error
    ? error.message
    : "Something went wrong. Please try again.";
}
