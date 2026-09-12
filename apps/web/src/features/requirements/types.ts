import type { Cycle, WorkItem } from "../../types";

export type RequirementView = "story-map" | "gantt" | "members" | "scenarios";
export interface UserActivity {
  id: string;
  epic_id: string | null;
  name: string;
  position: number;
  archived_at: string | null;
  version: number;
}
export interface Dependency {
  id: string;
  source_id: string;
  target_id: string;
  relation_type?: string;
}
export interface RequirementsSnapshot {
  revision: number;
  items: WorkItem[];
  activities: UserActivity[];
  cycles: (Cycle & { version?: number })[];
  dependencies: Dependency[];
  permissions?: { can_edit: boolean; can_admin: boolean };
}
export interface PlanningCommand {
  operation: "create" | "update" | "delete";
  id?: string;
  client_id?: string;
  version?: number;
  fields: Partial<WorkItem>;
}
export interface RequirementFilters {
  epic: string;
  cycle: string;
  state: string;
  member: string;
  skill: string;
  query: string;
}
export interface ResourceMember {
  member_id: string;
  display_name: string;
  skills: string[];
  weekday_minutes: (number | null)[];
  project_minutes_per_day: number | null;
  exceptions: Record<string, number | null>;
  version: number;
}
export interface ResourceConfiguration {
  timezone: string;
  members: ResourceMember[];
}
export interface LoadDay {
  date: string;
  capacity_minutes: number | null;
  allocated_minutes: number;
  unknown: boolean;
  over_capacity: boolean;
  task_ids: string[];
  selected_minutes?: number;
  selected_unknown?: boolean;
  task_minutes?: Record<string, number>;
  unknown_task_ids?: string[];
}
export interface LoadConflict {
  type: string;
  task_id?: string;
  member_id?: string;
  related_task_id?: string;
  date?: string;
  message: string;
  severity: string;
}
export interface ResourceLoad {
  timezone: string;
  start_date: string;
  end_date: string;
  members: (ResourceMember & {
    days: LoadDay[];
    weeks: (Omit<LoadDay, "date" | "task_ids"> & { start_date: string })[];
  })[];
  tasks: (Partial<WorkItem> & { id: string; executable: boolean })[];
  conflicts: LoadConflict[];
  unassigned: string[];
  unestimated: string[];
  unscheduled: string[];
}
export interface ScenarioParticipant {
  id: string;
  name: string;
  kind: "actor" | "system";
}
export interface ScenarioStep {
  id: string;
  kind: "call" | "return" | "alt" | "else" | "end";
  from_id?: string;
  to_id?: string;
  message: string;
  return_of?: string;
}
export interface ScenarioRelationship {
  id: string;
  kind: "association" | "include" | "extend" | "generalization";
  from_id: string;
  to_id: string;
}
export interface Scenario {
  id: string;
  story_id: string;
  name: string;
  goal: string;
  trigger: string;
  preconditions: string;
  outcome: string;
  system_boundary: string;
  bind_story_name: boolean;
  source_page_ids: string[];
  participants: ScenarioParticipant[];
  steps: ScenarioStep[];
  relationships: ScenarioRelationship[];
  version: number;
  review_needed: boolean;
  story: {
    id: string;
    name: string;
    state_id: string;
    state_name: string;
    state_group: string;
    version: number;
  };
  created_at: string;
  updated_at: string;
}
