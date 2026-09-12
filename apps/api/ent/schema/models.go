// This schema is independently authored for my-jira. Run go generate ./ent.
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
	"time"
)

func common() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
		field.Time("deleted_at").Optional().Nillable(),
	}
}
func scoped(project bool) []ent.Field {
	f := []ent.Field{ref("workspace_id")}
	if project {
		f = append(f, ref("project_id"))
	}
	return f
}
func ref(name string) ent.Field         { return field.UUID(name, uuid.UUID{}) }
func optionalRef(name string) ent.Field { return field.UUID(name, uuid.UUID{}).Optional().Nillable() }
func text(name string) ent.Field        { return field.String(name).Default("") }
func object(name string) ent.Field {
	return field.JSON(name, map[string]any{}).Default(map[string]any{})
}
func instant(name string) ent.Field { return field.Time(name).Optional().Nillable() }
func date(name string) ent.Field {
	return field.Time(name).SchemaType(map[string]string{"postgres": "date"}).Optional().Nillable()
}
func position() ent.Field { return field.Float("position").Default(65536) }
func annotation(table string) []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: table}}
}
func fields(table string) []ent.Field {
	f := common()
	switch table {
	case "users":
		f = append(f, field.String("email"), field.String("password_hash").Sensitive(), field.Bool("password_set").Default(true), text("display_name"), text("first_name"), text("last_name"), text("avatar_url"), field.String("timezone").Default("UTC"), field.Bool("is_active").Default(true), field.Bool("is_instance_admin").Default(false), field.Bool("email_verified").Default(false), object("preferences"))
	case "sessions":
		f = append(f, ref("user_id"), field.String("token_hash").Sensitive(), field.String("csrf_hash").Sensitive(), field.Time("expires_at"), instant("revoked_at"), text("user_agent"), text("ip_address"))
	case "instances":
		f = append(f, field.Bool("singleton").Default(true), field.String("name"), field.Bool("registration_enabled").Default(true), ref("setup_by"), object("settings"))
	case "auth_challenges":
		f = append(f, optionalRef("user_id"), field.String("email"), field.String("purpose"), field.String("token_hash").Sensitive(), field.Time("expires_at"), instant("consumed_at"), field.Int("attempts").Default(0), object("metadata"))
	case "oauth_accounts":
		f = append(f, ref("user_id"), field.String("provider"), field.String("subject"), field.String("email"), object("metadata"))
	case "auth_rate_limits":
		f = append(f, field.String("key").Unique(), field.Time("window_started").Default(time.Now), field.Int("attempts").Default(0))
	case "workspaces":
		f = append(f, field.String("name"), field.String("slug"), ref("owner_id"), field.String("timezone").Default("UTC"), text("logo_url"), text("description"))
	case "workspace_members":
		f = append(f, ref("workspace_id"), ref("user_id"), field.Int("role").Default(15), field.Bool("is_active").Default(true))
	case "invitations":
		f = append(f, ref("workspace_id"), field.String("email"), field.Int("role").Default(15), field.String("token_hash").Sensitive(), ref("invited_by"), field.Time("expires_at"), instant("accepted_at"), instant("revoked_at"))
	case "projects":
		f = append(f, ref("workspace_id"), field.String("name"), field.String("identifier"), text("description"), field.String("network").Default("public"), field.Bool("guest_can_view_all").Default(false), optionalRef("lead_id"), optionalRef("default_assignee_id"), field.String("timezone").Default("UTC"), text("icon"), field.String("color").Default("#6d5efc"), instant("archived_at"), field.Int64("next_sequence").Default(1), object("settings"))
		f = append(f, optionalRef("estimate_id"))
	case "project_members":
		f = append(f, scoped(true)...)
		f = append(f, ref("user_id"), field.Int("role").Default(15), field.Bool("is_active").Default(true))
	case "states":
		f = append(f, scoped(true)...)
		f = append(f, field.String("name"), field.String("color").Default("#64748b"), field.String("group_name").Default("unstarted"), position(), field.Bool("is_default").Default(false))
	case "labels":
		f = append(f, ref("workspace_id"), optionalRef("project_id"), optionalRef("parent_id"), field.String("name"), field.String("color").Default("#64748b"), text("description"), position())
	case "work_items":
		f = append(f, scoped(true)...)
		f = append(f, ref("state_id"), optionalRef("parent_id"), ref("created_by"), ref("updated_by"), field.String("name"), field.String("description_html").Default("<p></p>"), object("description_json"), field.Bytes("description_binary").Optional(), field.String("priority").Default("none"), field.Int64("sequence_id"), position(), date("start_date"), date("target_date"), instant("completed_at"), instant("archived_at"), field.Bool("is_draft").Default(false), field.Float("estimate").Optional().Nillable(), text("type_name"), field.Int64("version").Default(1))
		f = append(f, optionalRef("estimate_point_id"))
		f = append(f, field.String("requirement_type").Optional().Nillable(), text("story_role"), text("story_goal"), text("story_benefit"), field.JSON("acceptance_criteria", []string{}).Default([]string{}), optionalRef("activity_id"), field.Float("map_position").Default(1024), field.Int("estimated_minutes").Optional().Nillable(), field.Int("remaining_minutes").Optional().Nillable(), field.JSON("required_skills", []string{}).Default([]string{}), field.JSON("allocation_weights", []map[string]any{}).Default([]map[string]any{}), field.Bool("planning_locked").Default(false))
	case "estimates":
		f = append(f, scoped(true)...)
		f = append(f, field.String("name"), text("description"), field.String("kind").Default("points"))
	case "estimate_points":
		f = append(f, scoped(true)...)
		f = append(f, ref("estimate_id"), field.Float("position").Default(1024), field.String("label").MaxLen(20), field.Float("numeric_value").Optional().Nillable())
	case "work_item_assignees", "work_item_subscribers":
		f = append(f, scoped(true)...)
		f = append(f, ref("work_item_id"), ref("user_id"))
	case "work_item_labels":
		f = append(f, scoped(true)...)
		f = append(f, ref("work_item_id"), ref("label_id"))
	case "work_item_relations":
		f = append(f, scoped(true)...)
		f = append(f, ref("source_id"), ref("target_id"), field.String("relation_type"))
	case "work_item_links":
		f = append(f, scoped(true)...)
		f = append(f, ref("work_item_id"), text("title"), field.String("url"), ref("created_by"))
	case "work_item_versions":
		f = append(f, scoped(true)...)
		f = append(f, ref("work_item_id"), ref("saved_by"), field.Int64("version"), object("snapshot"))
	case "comments":
		f = append(f, scoped(true)...)
		f = append(f, ref("work_item_id"), optionalRef("parent_id"), ref("author_id"), field.String("body_html").Default("<p></p>"), object("body_json"), instant("edited_at"), field.Bool("is_public").Default(false))
	case "public_votes":
		f = append(f, scoped(true)...)
		f = append(f, ref("work_item_id"), ref("user_id"))
	case "reactions":
		f = append(f, scoped(true)...)
		f = append(f, optionalRef("work_item_id"), optionalRef("comment_id"), ref("user_id"), field.String("emoji"))
	case "activities":
		f = append(f, ref("workspace_id"), optionalRef("project_id"), optionalRef("work_item_id"), ref("actor_id"), field.String("action"), text("field_name"), field.JSON("old_value", new(any)).Optional(), field.JSON("new_value", new(any)).Optional(), object("metadata"))
	case "cycles":
		f = append(f, scoped(true)...)
		f = append(f, field.String("name"), text("description"), date("start_date"), date("end_date"), ref("owner_id"), position(), instant("archived_at"), object("settings"))
		f = append(f, field.JSON("progress_snapshot", map[string]any{}).Optional())
	case "cycle_items":
		f = append(f, scoped(true)...)
		f = append(f, ref("cycle_id"), ref("work_item_id"))
	case "modules":
		f = append(f, scoped(true)...)
		f = append(f, field.String("name"), text("description"), field.String("status").Default("planned"), date("start_date"), date("target_date"), optionalRef("lead_id"), position(), instant("archived_at"), object("settings"))
	case "module_items":
		f = append(f, scoped(true)...)
		f = append(f, ref("module_id"), ref("work_item_id"))
	case "module_members":
		f = append(f, scoped(true)...)
		f = append(f, ref("module_id"), ref("user_id"))
	case "module_links":
		f = append(f, scoped(true)...)
		f = append(f, ref("module_id"), ref("created_by"), field.String("title").MaxLen(255).Default(""), field.String("url").MaxLen(4096))
	case "saved_analyses":
		f = append(f, scoped(false)...)
		f = append(f, field.String("name").MaxLen(255), text("description"), ref("owner_id"), object("query"))
	case "saved_views":
		f = append(f, ref("workspace_id"), optionalRef("project_id"), ref("owner_id"), field.String("name"), text("description"), field.String("layout").Default("list"), object("filters"), object("display"), field.Bool("is_private").Default(false), position())
	case "pages":
		f = append(f, ref("workspace_id"), optionalRef("project_id"), optionalRef("parent_id"), ref("owner_id"), field.String("name"), field.String("content_html").Default("<p></p>"), object("content_json"), field.Bytes("content_binary").Optional(), field.Bool("is_private").Default(false), field.Bool("is_locked").Default(false), instant("archived_at"), position(), text("icon"), field.Int64("version").Default(1))
	case "page_versions":
		f = append(f, ref("workspace_id"), ref("page_id"), ref("saved_by"), field.String("name"), field.String("content_html").Default("<p></p>"), object("content_json"), field.Bytes("content_binary").Optional(), field.Int64("version"))
	case "page_comments":
		f = append(f, ref("workspace_id"), optionalRef("project_id"), ref("page_id"), ref("author_id"), optionalRef("parent_id"), field.String("body_html").Default("<p></p>"), object("body_json"), instant("edited_at"))
	case "notifications":
		f = append(f, ref("workspace_id"), optionalRef("project_id"), ref("user_id"), optionalRef("actor_id"), field.String("entity_type"), optionalRef("entity_id"), field.String("title"), text("body"), object("data"), instant("read_at"), instant("archived_at"), instant("snoozed_until"))
	case "file_assets":
		f = append(f, optionalRef("workspace_id"), optionalRef("project_id"), optionalRef("work_item_id"), optionalRef("page_id"), ref("uploaded_by"), field.String("filename"), field.String("content_type"), field.Int64("size_bytes"), field.String("object_key"), field.String("upload_status").Default("pending"), object("metadata"))
	case "favorites":
		f = append(f, ref("workspace_id"), ref("user_id"), field.String("entity_type"), ref("entity_id"), position())
	case "recent_visits":
		f = append(f, ref("workspace_id"), ref("user_id"), field.String("entity_type"), ref("entity_id"), field.Time("visited_at").Default(time.Now))
	case "stickies":
		f = append(f, ref("workspace_id"), ref("user_id"), text("title"), object("content_json"), field.String("content_html").Default("<p></p>"), field.String("color").Default("#fef3c7"), position(), field.Bool("is_archived").Default(false))
	case "preferences":
		f = append(f, optionalRef("workspace_id"), optionalRef("project_id"), ref("user_id"), field.String("scope"), object("value"))
	case "intake_items":
		f = append(f, scoped(true)...)
		f = append(f, ref("work_item_id"), field.String("status").Default("pending"), field.String("source").Default("app"), ref("submitted_by"), optionalRef("duplicate_of"), instant("snoozed_until"), object("metadata"))
	case "api_tokens":
		f = append(f, ref("user_id"), ref("workspace_id"), field.String("name"), field.String("token_hash").Sensitive(), field.String("prefix"), instant("last_used_at"), instant("expires_at"), instant("revoked_at"))
	case "webhooks":
		f = append(f, ref("workspace_id"), ref("created_by"), field.String("url"), field.String("secret").Sensitive(), field.JSON("events", []string{}).Default([]string{}), field.Bool("is_active").Default(true))
	case "webhook_deliveries":
		f = append(f, ref("workspace_id"), ref("webhook_id"), ref("event_id"), object("request"), field.Int("response_status").Optional().Nillable(), text("response_body"), field.Int("attempts").Default(0), instant("delivered_at"))
	case "exports":
		f = append(f, ref("workspace_id"), optionalRef("project_id"), ref("requested_by"), field.String("format"), object("filters"), field.String("status").Default("pending"), text("object_key"), text("error_message"), instant("completed_at"))
	case "public_sites":
		f = append(f, ref("workspace_id"), ref("project_id"), field.String("slug"), field.String("title"), text("description"), field.Bool("is_enabled").Default(true), field.Bool("comments_enabled").Default(false), field.Bool("reactions_enabled").Default(false), field.Bool("votes_enabled").Default(false), field.Bool("intake_enabled").Default(false), object("settings"))
	case "outbox_events":
		f = append(f, field.String("topic"), object("payload"), field.String("deduplication_key").Unique(), instant("dispatched_at"), field.Int("attempts").Default(0), text("last_error"))
	}
	return f
}

type User struct{ ent.Schema }

func (User) Fields() []ent.Field              { return fields("users") }
func (User) Annotations() []schema.Annotation { return annotation("users") }

type Session struct{ ent.Schema }

func (Session) Fields() []ent.Field              { return fields("sessions") }
func (Session) Annotations() []schema.Annotation { return annotation("sessions") }

type Instance struct{ ent.Schema }

func (Instance) Fields() []ent.Field              { return fields("instances") }
func (Instance) Annotations() []schema.Annotation { return annotation("instances") }
func (Instance) Indexes() []ent.Index             { return []ent.Index{index.Fields("singleton").Unique()} }

type Workspace struct{ ent.Schema }

func (Workspace) Fields() []ent.Field              { return fields("workspaces") }
func (Workspace) Annotations() []schema.Annotation { return annotation("workspaces") }

type WorkspaceMember struct{ ent.Schema }

func (WorkspaceMember) Fields() []ent.Field              { return fields("workspace_members") }
func (WorkspaceMember) Annotations() []schema.Annotation { return annotation("workspace_members") }

type Invitation struct{ ent.Schema }

func (Invitation) Fields() []ent.Field              { return fields("invitations") }
func (Invitation) Annotations() []schema.Annotation { return annotation("invitations") }

type Project struct{ ent.Schema }

func (Project) Fields() []ent.Field              { return fields("projects") }
func (Project) Annotations() []schema.Annotation { return annotation("projects") }

type ProjectMember struct{ ent.Schema }

func (ProjectMember) Fields() []ent.Field              { return fields("project_members") }
func (ProjectMember) Annotations() []schema.Annotation { return annotation("project_members") }

type State struct{ ent.Schema }

func (State) Fields() []ent.Field              { return fields("states") }
func (State) Annotations() []schema.Annotation { return annotation("states") }

type Label struct{ ent.Schema }

func (Label) Fields() []ent.Field              { return fields("labels") }
func (Label) Annotations() []schema.Annotation { return annotation("labels") }

type WorkItem struct{ ent.Schema }

func (WorkItem) Fields() []ent.Field              { return fields("work_items") }
func (WorkItem) Annotations() []schema.Annotation { return annotation("work_items") }

type WorkItemAssignee struct{ ent.Schema }

func (WorkItemAssignee) Fields() []ent.Field              { return fields("work_item_assignees") }
func (WorkItemAssignee) Annotations() []schema.Annotation { return annotation("work_item_assignees") }

type WorkItemLabel struct{ ent.Schema }

func (WorkItemLabel) Fields() []ent.Field              { return fields("work_item_labels") }
func (WorkItemLabel) Annotations() []schema.Annotation { return annotation("work_item_labels") }

type WorkItemRelation struct{ ent.Schema }

func (WorkItemRelation) Fields() []ent.Field              { return fields("work_item_relations") }
func (WorkItemRelation) Annotations() []schema.Annotation { return annotation("work_item_relations") }

type WorkItemSubscriber struct{ ent.Schema }

func (WorkItemSubscriber) Fields() []ent.Field { return fields("work_item_subscribers") }
func (WorkItemSubscriber) Annotations() []schema.Annotation {
	return annotation("work_item_subscribers")
}

type WorkItemLink struct{ ent.Schema }

func (WorkItemLink) Fields() []ent.Field              { return fields("work_item_links") }
func (WorkItemLink) Annotations() []schema.Annotation { return annotation("work_item_links") }

type WorkItemVersion struct{ ent.Schema }

func (WorkItemVersion) Fields() []ent.Field              { return fields("work_item_versions") }
func (WorkItemVersion) Annotations() []schema.Annotation { return annotation("work_item_versions") }

type Comment struct{ ent.Schema }

func (Comment) Fields() []ent.Field              { return fields("comments") }
func (Comment) Annotations() []schema.Annotation { return annotation("comments") }

type PublicVote struct{ ent.Schema }

func (PublicVote) Fields() []ent.Field              { return fields("public_votes") }
func (PublicVote) Annotations() []schema.Annotation { return annotation("public_votes") }

type Reaction struct{ ent.Schema }

func (Reaction) Fields() []ent.Field              { return fields("reactions") }
func (Reaction) Annotations() []schema.Annotation { return annotation("reactions") }

type Activity struct{ ent.Schema }

func (Activity) Fields() []ent.Field              { return fields("activities") }
func (Activity) Annotations() []schema.Annotation { return annotation("activities") }

type Cycle struct{ ent.Schema }

func (Cycle) Fields() []ent.Field              { return fields("cycles") }
func (Cycle) Annotations() []schema.Annotation { return annotation("cycles") }

type CycleItem struct{ ent.Schema }

func (CycleItem) Fields() []ent.Field              { return fields("cycle_items") }
func (CycleItem) Annotations() []schema.Annotation { return annotation("cycle_items") }

type Module struct{ ent.Schema }

func (Module) Fields() []ent.Field              { return fields("modules") }
func (Module) Annotations() []schema.Annotation { return annotation("modules") }

type ModuleItem struct{ ent.Schema }

func (ModuleItem) Fields() []ent.Field              { return fields("module_items") }
func (ModuleItem) Annotations() []schema.Annotation { return annotation("module_items") }

type ModuleMember struct{ ent.Schema }

func (ModuleMember) Fields() []ent.Field              { return fields("module_members") }
func (ModuleMember) Annotations() []schema.Annotation { return annotation("module_members") }

type SavedView struct{ ent.Schema }

func (SavedView) Fields() []ent.Field              { return fields("saved_views") }
func (SavedView) Annotations() []schema.Annotation { return annotation("saved_views") }

type Page struct{ ent.Schema }

func (Page) Fields() []ent.Field              { return fields("pages") }
func (Page) Annotations() []schema.Annotation { return annotation("pages") }

type PageVersion struct{ ent.Schema }

func (PageVersion) Fields() []ent.Field              { return fields("page_versions") }
func (PageVersion) Annotations() []schema.Annotation { return annotation("page_versions") }

type PageComment struct{ ent.Schema }

func (PageComment) Fields() []ent.Field              { return fields("page_comments") }
func (PageComment) Annotations() []schema.Annotation { return annotation("page_comments") }

type Notification struct{ ent.Schema }

func (Notification) Fields() []ent.Field              { return fields("notifications") }
func (Notification) Annotations() []schema.Annotation { return annotation("notifications") }

type FileAsset struct{ ent.Schema }

func (FileAsset) Fields() []ent.Field              { return fields("file_assets") }
func (FileAsset) Annotations() []schema.Annotation { return annotation("file_assets") }

type Favorite struct{ ent.Schema }

func (Favorite) Fields() []ent.Field              { return fields("favorites") }
func (Favorite) Annotations() []schema.Annotation { return annotation("favorites") }

type RecentVisit struct{ ent.Schema }

func (RecentVisit) Fields() []ent.Field              { return fields("recent_visits") }
func (RecentVisit) Annotations() []schema.Annotation { return annotation("recent_visits") }

type Sticky struct{ ent.Schema }

func (Sticky) Fields() []ent.Field              { return fields("stickies") }
func (Sticky) Annotations() []schema.Annotation { return annotation("stickies") }

type Preference struct{ ent.Schema }

func (Preference) Fields() []ent.Field              { return fields("preferences") }
func (Preference) Annotations() []schema.Annotation { return annotation("preferences") }

type IntakeItem struct{ ent.Schema }

func (IntakeItem) Fields() []ent.Field              { return fields("intake_items") }
func (IntakeItem) Annotations() []schema.Annotation { return annotation("intake_items") }

type APIToken struct{ ent.Schema }

func (APIToken) Fields() []ent.Field              { return fields("api_tokens") }
func (APIToken) Annotations() []schema.Annotation { return annotation("api_tokens") }

type Webhook struct{ ent.Schema }

func (Webhook) Fields() []ent.Field              { return fields("webhooks") }
func (Webhook) Annotations() []schema.Annotation { return annotation("webhooks") }

type WebhookDelivery struct{ ent.Schema }

func (WebhookDelivery) Fields() []ent.Field              { return fields("webhook_deliveries") }
func (WebhookDelivery) Annotations() []schema.Annotation { return annotation("webhook_deliveries") }

type Export struct{ ent.Schema }

func (Export) Fields() []ent.Field              { return fields("exports") }
func (Export) Annotations() []schema.Annotation { return annotation("exports") }

type PublicSite struct{ ent.Schema }

func (PublicSite) Fields() []ent.Field              { return fields("public_sites") }
func (PublicSite) Annotations() []schema.Annotation { return annotation("public_sites") }

type OutboxEvent struct{ ent.Schema }

func (OutboxEvent) Fields() []ent.Field              { return fields("outbox_events") }
func (OutboxEvent) Annotations() []schema.Annotation { return annotation("outbox_events") }

type AuthChallenge struct{ ent.Schema }

func (AuthChallenge) Fields() []ent.Field              { return fields("auth_challenges") }
func (AuthChallenge) Annotations() []schema.Annotation { return annotation("auth_challenges") }

type OAuthAccount struct{ ent.Schema }

func (OAuthAccount) Fields() []ent.Field              { return fields("oauth_accounts") }
func (OAuthAccount) Annotations() []schema.Annotation { return annotation("oauth_accounts") }

type AuthRateLimit struct{ ent.Schema }

func (AuthRateLimit) Fields() []ent.Field              { return fields("auth_rate_limits") }
func (AuthRateLimit) Annotations() []schema.Annotation { return annotation("auth_rate_limits") }

type Estimate struct{ ent.Schema }

func (Estimate) Fields() []ent.Field              { return fields("estimates") }
func (Estimate) Annotations() []schema.Annotation { return annotation("estimates") }

type EstimatePoint struct{ ent.Schema }

func (EstimatePoint) Fields() []ent.Field              { return fields("estimate_points") }
func (EstimatePoint) Annotations() []schema.Annotation { return annotation("estimate_points") }

type ModuleLink struct{ ent.Schema }

func (ModuleLink) Fields() []ent.Field              { return fields("module_links") }
func (ModuleLink) Annotations() []schema.Annotation { return annotation("module_links") }

type SavedAnalysis struct{ ent.Schema }

func (SavedAnalysis) Fields() []ent.Field              { return fields("saved_analyses") }
func (SavedAnalysis) Annotations() []schema.Annotation { return annotation("saved_analyses") }
