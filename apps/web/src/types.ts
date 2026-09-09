export type ID = string;
export type Role = 5 | 15 | 20;
export type Locale = "zh-CN" | "en";
export type Theme = "light" | "dark" | "system";
export type Priority = "none" | "low" | "medium" | "high" | "urgent";
export type StateGroup =
  "backlog" | "unstarted" | "started" | "completed" | "cancelled";

export interface User {
  id: ID;
  email: string;
  display_name: string;
  first_name: string;
  last_name: string;
  avatar_url: string;
  timezone: string;
  is_instance_admin: boolean;
  password_set?: boolean;
  email_verified?: boolean;
  preferences: Record<string, unknown>;
}

export interface Instance {
  name: string;
  is_setup_done: boolean;
  registration_enabled: boolean;
  auth_methods?: string[];
}

export interface Workspace {
  id: ID;
  name: string;
  slug: string;
  description: string;
  timezone: string;
  logo_url: string;
  owner_id: ID;
  role: Role;
}

export interface Project {
  id: ID;
  workspace_id: ID;
  name: string;
  identifier: string;
  description: string;
  network: "private" | "public";
  role: Role;
  icon: string;
  color: string;
  archived_at: string | null;
  settings: Record<string, unknown>;
  features?: Partial<
    Record<"cycles" | "modules" | "pages" | "views" | "intake", boolean>
  >;
  cover_image_url?: string | null;
  is_member?: boolean;
  guest_can_view_all?: boolean;
}

export interface Member {
  id: ID;
  user_id: ID;
  role: Role;
  email: string;
  display_name: string;
  avatar_url?: string;
  user?: User;
}

export interface State {
  id: ID;
  project_id: ID;
  name: string;
  color: string;
  group: StateGroup;
  position: number;
  is_default: boolean;
}

export interface Label {
  id: ID;
  name: string;
  color: string;
  description: string;
  parent_id: ID | null;
  project_id?: ID | null;
}

export interface WorkItem {
  id: ID;
  workspace_id: ID;
  project_id: ID;
  name: string;
  sequence_id: number;
  description_html: string;
  description_json?: unknown;
  state_id: ID;
  priority: Priority;
  assignee_ids: ID[];
  label_ids: ID[];
  cycle_id: ID | null;
  module_ids: ID[];
  parent_id: ID | null;
  estimate: number | null;
  estimate_point_id?: ID | null;
  estimate_point_detail?: EstimatePoint | null;
  start_date: string | null;
  target_date: string | null;
  position: number;
  created_by: ID;
  created_at: string;
  updated_at: string;
  archived_at: string | null;
  is_draft: boolean;
  version: number;
}

export type WorkItemMoveChanges = Partial<
  Pick<
    WorkItem,
    | "position"
    | "priority"
    | "assignee_ids"
    | "label_ids"
    | "cycle_id"
    | "module_ids"
    | "estimate_point_id"
  >
>;

export interface EstimatePoint {
  id: ID;
  label: string;
  numeric_value: number | null;
  position: number;
}
export interface EstimateScheme {
  id: ID;
  name: string;
  description: string;
  kind: "points" | "categories";
  points: EstimatePoint[];
}
export interface Reaction {
  id: ID;
  emoji: string;
  user_id: ID;
}

export interface Comment {
  id: ID;
  author_id: ID;
  author?: User;
  body_html: string;
  created_at: string;
  edited_at: string | null;
  parent_id?: ID | null;
  reactions?: Reaction[];
}

export interface Activity {
  id: ID;
  actor_id: ID;
  action: string;
  field_name: string;
  old_value: unknown;
  new_value: unknown;
  created_at: string;
}

export interface Cycle {
  id: ID;
  name: string;
  description: string;
  start_date: string | null;
  end_date: string | null;
  owner_id: ID;
  archived_at: string | null;
  total_items?: number;
  completed_items?: number;
}

export interface Module {
  id: ID;
  name: string;
  description: string;
  status:
    | "backlog"
    | "planned"
    | "in-progress"
    | "paused"
    | "completed"
    | "cancelled";
  start_date: string | null;
  target_date: string | null;
  lead_id: ID | null;
  member_ids?: ID[];
  archived_at: string | null;
  total_items?: number;
  completed_items?: number;
}

export type Layout = "list" | "board" | "calendar" | "timeline" | "table";
export interface SavedView {
  id: ID;
  name: string;
  description: string;
  layout: string;
  filters: Record<string, unknown>;
  display: Record<string, unknown>;
  is_private: boolean;
  updated_at?: string;
}

export interface PageDocument {
  id: ID;
  owner_id: ID;
  name: string;
  content_html: string;
  content_json: unknown;
  project_id: ID | null;
  parent_id: ID | null;
  icon: string;
  is_private: boolean;
  is_locked: boolean;
  archived_at: string | null;
  version: number;
  created_at: string;
  updated_at: string;
}

export interface Notification {
  id: ID;
  title: string;
  body: string;
  entity_type: string;
  entity_id: ID | null;
  project_id: ID | null;
  data: Record<string, unknown>;
  read_at: string | null;
  archived_at: string | null;
  created_at: string;
}

export interface APIResponse<T> {
  data: T;
  total_items?: number;
  pagination?: {
    next_cursor?: string | null;
    has_more: boolean;
    total: number;
    limit?: number;
    offset?: number;
    next_offset?: number | null;
  };
}
