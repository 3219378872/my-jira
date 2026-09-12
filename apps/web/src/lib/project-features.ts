import type { Project } from "../types";

// Existing projects keep the bundle unless an administrator explicitly disables it.
export function requirementsEnabled(
  project: Pick<Project, "settings">,
): boolean {
  return project.settings?.requirements_enabled !== false;
}
