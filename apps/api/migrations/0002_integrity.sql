-- Independently authored constraints and server-side defaults for SQL callers.
DO $$
DECLARE table_name text;
BEGIN
  FOR table_name IN SELECT tablename FROM pg_tables WHERE schemaname=current_schema()
    AND tablename <> 'schema_migrations'
  LOOP
    EXECUTE format('ALTER TABLE %I ALTER COLUMN id SET DEFAULT gen_random_uuid()', table_name);
    EXECUTE format('ALTER TABLE %I ALTER COLUMN created_at SET DEFAULT now()', table_name);
    EXECUTE format('ALTER TABLE %I ALTER COLUMN updated_at SET DEFAULT now()', table_name);
  END LOOP;
END $$;

DO $$
DECLARE column_info record;
BEGIN
  FOR column_info IN SELECT table_name,column_name FROM information_schema.columns
    WHERE table_schema=current_schema() AND data_type='jsonb' AND is_nullable='NO'
  LOOP
    EXECUTE format('ALTER TABLE %I ALTER COLUMN %I SET DEFAULT %L::jsonb',column_info.table_name,column_info.column_name,
      CASE WHEN column_info.table_name='webhooks' AND column_info.column_name='events' THEN '[]' ELSE '{}' END);
  END LOOP;
END $$;
ALTER TABLE recent_visits ALTER COLUMN visited_at SET DEFAULT now();

CREATE UNIQUE INDEX users_email_active ON users(lower(email)) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX sessions_token_hash_unique ON sessions(token_hash);
CREATE INDEX sessions_user_active ON sessions(user_id,expires_at) WHERE revoked_at IS NULL;
CREATE UNIQUE INDEX workspaces_slug_active ON workspaces(slug) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX workspace_members_active ON workspace_members(workspace_id,user_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX projects_identifier_active ON projects(workspace_id,identifier) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX projects_name_active ON projects(workspace_id,name) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX projects_scope ON projects(id,workspace_id);
CREATE UNIQUE INDEX project_members_active ON project_members(project_id,user_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX states_name_active ON states(project_id,name) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX states_default_active ON states(project_id) WHERE is_default AND deleted_at IS NULL;
CREATE UNIQUE INDEX states_scope ON states(id,project_id,workspace_id);
CREATE UNIQUE INDEX labels_name_active ON labels(workspace_id,COALESCE(project_id,'00000000-0000-0000-0000-000000000000'::uuid),name) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX labels_scope ON labels(id,workspace_id);
CREATE UNIQUE INDEX work_items_sequence ON work_items(project_id,sequence_id);
CREATE UNIQUE INDEX work_items_scope ON work_items(id,project_id,workspace_id);
CREATE INDEX work_items_project_list ON work_items(project_id,state_id,position,id) WHERE deleted_at IS NULL AND archived_at IS NULL AND NOT is_draft;
CREATE INDEX work_items_workspace_list ON work_items(workspace_id,updated_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX work_items_search ON work_items USING gin(to_tsvector('simple',name||' '||description_html)) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX work_item_assignees_active ON work_item_assignees(work_item_id,user_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX work_item_labels_active ON work_item_labels(work_item_id,label_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX work_item_subscribers_active ON work_item_subscribers(work_item_id,user_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX work_item_relations_active ON work_item_relations(source_id,target_id,relation_type) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX work_item_versions_unique ON work_item_versions(work_item_id,version);
CREATE UNIQUE INDEX comments_scope ON comments(id,project_id,workspace_id);
CREATE INDEX comments_work_item ON comments(work_item_id,created_at) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX reactions_issue_active ON reactions(work_item_id,user_id,emoji) WHERE deleted_at IS NULL AND work_item_id IS NOT NULL;
CREATE UNIQUE INDEX reactions_comment_active ON reactions(comment_id,user_id,emoji) WHERE deleted_at IS NULL AND comment_id IS NOT NULL;
CREATE UNIQUE INDEX public_votes_active ON public_votes(work_item_id,user_id) WHERE deleted_at IS NULL;
CREATE INDEX activities_work_item ON activities(work_item_id,created_at DESC);
CREATE UNIQUE INDEX cycles_scope ON cycles(id,project_id,workspace_id);
CREATE UNIQUE INDEX cycle_items_single_active ON cycle_items(work_item_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX cycle_items_active ON cycle_items(cycle_id,work_item_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX modules_scope ON modules(id,project_id,workspace_id);
CREATE UNIQUE INDEX module_items_active ON module_items(module_id,work_item_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX module_members_active ON module_members(module_id,user_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX pages_scope ON pages(id,workspace_id);
CREATE UNIQUE INDEX page_versions_unique ON page_versions(page_id,version);
CREATE UNIQUE INDEX favorites_active ON favorites(workspace_id,user_id,entity_type,entity_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX recent_visits_active ON recent_visits(workspace_id,user_id,entity_type,entity_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX preferences_active ON preferences(user_id,COALESCE(workspace_id,'00000000-0000-0000-0000-000000000000'::uuid),COALESCE(project_id,'00000000-0000-0000-0000-000000000000'::uuid),scope) WHERE deleted_at IS NULL;
CREATE INDEX notifications_inbox ON notifications(user_id,workspace_id,created_at DESC) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX api_tokens_hash ON api_tokens(token_hash);
CREATE UNIQUE INDEX webhook_deliveries_event ON webhook_deliveries(webhook_id,event_id);
CREATE UNIQUE INDEX public_sites_slug ON public_sites(slug) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX public_sites_project ON public_sites(project_id) WHERE deleted_at IS NULL;
CREATE INDEX outbox_pending ON outbox_events(created_at) WHERE dispatched_at IS NULL;

ALTER TABLE instances ADD CHECK(singleton);
ALTER TABLE workspace_members ADD CHECK(role IN(5,15,20));
ALTER TABLE project_members ADD CHECK(role IN(5,15,20));
ALTER TABLE invitations ADD CHECK(role IN(5,15,20));
ALTER TABLE projects ADD CHECK(network IN('private','public'));
ALTER TABLE projects ADD CHECK(next_sequence>0);
ALTER TABLE states ADD CHECK(group_name IN('backlog','unstarted','started','completed','cancelled'));
ALTER TABLE work_items ADD CHECK(priority IN('none','low','medium','high','urgent'));
ALTER TABLE work_items ADD CHECK(sequence_id>0 AND version>0);
ALTER TABLE work_items ADD CHECK(parent_id IS NULL OR parent_id<>id);
ALTER TABLE work_items ADD CHECK(start_date IS NULL OR target_date IS NULL OR start_date<=target_date);
ALTER TABLE work_item_relations ADD CHECK(source_id<>target_id);
ALTER TABLE reactions ADD CHECK((work_item_id IS NULL)<>(comment_id IS NULL));
ALTER TABLE cycles ADD CHECK(start_date IS NULL OR end_date IS NULL OR start_date<=end_date);
ALTER TABLE modules ADD CHECK(status IN('backlog','planned','in-progress','paused','completed','cancelled'));
ALTER TABLE modules ADD CHECK(start_date IS NULL OR target_date IS NULL OR start_date<=target_date);
ALTER TABLE intake_items ADD CHECK(status IN('pending','accepted','rejected','duplicate','snoozed'));
ALTER TABLE file_assets ADD CHECK(size_bytes>=0);

ALTER TABLE sessions ADD FOREIGN KEY(user_id) REFERENCES users(id);
ALTER TABLE instances ADD FOREIGN KEY(setup_by) REFERENCES users(id);
ALTER TABLE workspaces ADD FOREIGN KEY(owner_id) REFERENCES users(id);
ALTER TABLE workspace_members ADD FOREIGN KEY(workspace_id) REFERENCES workspaces(id), ADD FOREIGN KEY(user_id) REFERENCES users(id);
ALTER TABLE invitations ADD FOREIGN KEY(workspace_id) REFERENCES workspaces(id), ADD FOREIGN KEY(invited_by) REFERENCES users(id);
ALTER TABLE projects ADD FOREIGN KEY(workspace_id) REFERENCES workspaces(id), ADD FOREIGN KEY(lead_id) REFERENCES users(id), ADD FOREIGN KEY(default_assignee_id) REFERENCES users(id);
ALTER TABLE project_members ADD FOREIGN KEY(project_id,workspace_id) REFERENCES projects(id,workspace_id), ADD FOREIGN KEY(user_id) REFERENCES users(id);
ALTER TABLE states ADD FOREIGN KEY(project_id,workspace_id) REFERENCES projects(id,workspace_id);
ALTER TABLE labels ADD FOREIGN KEY(workspace_id) REFERENCES workspaces(id), ADD FOREIGN KEY(project_id,workspace_id) REFERENCES projects(id,workspace_id), ADD FOREIGN KEY(parent_id) REFERENCES labels(id);
ALTER TABLE work_items ADD FOREIGN KEY(project_id,workspace_id) REFERENCES projects(id,workspace_id), ADD FOREIGN KEY(state_id,project_id,workspace_id) REFERENCES states(id,project_id,workspace_id), ADD FOREIGN KEY(parent_id) REFERENCES work_items(id), ADD FOREIGN KEY(created_by) REFERENCES users(id), ADD FOREIGN KEY(updated_by) REFERENCES users(id);

DO $$
DECLARE table_name text;
BEGIN
  FOREACH table_name IN ARRAY ARRAY['work_item_assignees','work_item_labels','work_item_subscribers','work_item_links','work_item_versions','comments','cycle_items','module_items','intake_items','public_votes'] LOOP
    EXECUTE format('ALTER TABLE %I ADD FOREIGN KEY(work_item_id,project_id,workspace_id) REFERENCES work_items(id,project_id,workspace_id)',table_name);
  END LOOP;
  FOREACH table_name IN ARRAY ARRAY['work_item_assignees','work_item_subscribers','public_votes'] LOOP
    EXECUTE format('ALTER TABLE %I ADD FOREIGN KEY(user_id) REFERENCES users(id)',table_name);
  END LOOP;
END $$;

ALTER TABLE work_item_labels ADD FOREIGN KEY(label_id,workspace_id) REFERENCES labels(id,workspace_id);
ALTER TABLE work_item_relations ADD FOREIGN KEY(source_id,project_id,workspace_id) REFERENCES work_items(id,project_id,workspace_id), ADD FOREIGN KEY(target_id) REFERENCES work_items(id);
ALTER TABLE work_item_links ADD FOREIGN KEY(created_by) REFERENCES users(id);
ALTER TABLE work_item_versions ADD FOREIGN KEY(saved_by) REFERENCES users(id);
ALTER TABLE comments ADD FOREIGN KEY(parent_id) REFERENCES comments(id), ADD FOREIGN KEY(author_id) REFERENCES users(id);
ALTER TABLE reactions ADD FOREIGN KEY(work_item_id,project_id,workspace_id) REFERENCES work_items(id,project_id,workspace_id), ADD FOREIGN KEY(comment_id,project_id,workspace_id) REFERENCES comments(id,project_id,workspace_id), ADD FOREIGN KEY(user_id) REFERENCES users(id);
ALTER TABLE activities ADD FOREIGN KEY(workspace_id) REFERENCES workspaces(id), ADD FOREIGN KEY(actor_id) REFERENCES users(id), ADD FOREIGN KEY(work_item_id) REFERENCES work_items(id);
ALTER TABLE cycles ADD FOREIGN KEY(project_id,workspace_id) REFERENCES projects(id,workspace_id), ADD FOREIGN KEY(owner_id) REFERENCES users(id);
ALTER TABLE cycle_items ADD FOREIGN KEY(cycle_id,project_id,workspace_id) REFERENCES cycles(id,project_id,workspace_id);
ALTER TABLE modules ADD FOREIGN KEY(project_id,workspace_id) REFERENCES projects(id,workspace_id), ADD FOREIGN KEY(lead_id) REFERENCES users(id);
ALTER TABLE module_items ADD FOREIGN KEY(module_id,project_id,workspace_id) REFERENCES modules(id,project_id,workspace_id);
ALTER TABLE module_members ADD FOREIGN KEY(module_id,project_id,workspace_id) REFERENCES modules(id,project_id,workspace_id), ADD FOREIGN KEY(user_id) REFERENCES users(id);
ALTER TABLE saved_views ADD FOREIGN KEY(workspace_id) REFERENCES workspaces(id), ADD FOREIGN KEY(project_id,workspace_id) REFERENCES projects(id,workspace_id), ADD FOREIGN KEY(owner_id) REFERENCES users(id);
ALTER TABLE pages ADD FOREIGN KEY(workspace_id) REFERENCES workspaces(id), ADD FOREIGN KEY(project_id,workspace_id) REFERENCES projects(id,workspace_id), ADD FOREIGN KEY(parent_id,workspace_id) REFERENCES pages(id,workspace_id), ADD FOREIGN KEY(owner_id) REFERENCES users(id);
ALTER TABLE page_versions ADD FOREIGN KEY(page_id,workspace_id) REFERENCES pages(id,workspace_id), ADD FOREIGN KEY(saved_by) REFERENCES users(id);
ALTER TABLE page_comments ADD FOREIGN KEY(page_id,workspace_id) REFERENCES pages(id,workspace_id), ADD FOREIGN KEY(parent_id) REFERENCES page_comments(id), ADD FOREIGN KEY(author_id) REFERENCES users(id);
ALTER TABLE notifications ADD FOREIGN KEY(workspace_id) REFERENCES workspaces(id), ADD FOREIGN KEY(user_id) REFERENCES users(id), ADD FOREIGN KEY(actor_id) REFERENCES users(id);
ALTER TABLE file_assets ADD FOREIGN KEY(workspace_id) REFERENCES workspaces(id), ADD FOREIGN KEY(project_id,workspace_id) REFERENCES projects(id,workspace_id), ADD FOREIGN KEY(work_item_id,project_id,workspace_id) REFERENCES work_items(id,project_id,workspace_id), ADD FOREIGN KEY(page_id,workspace_id) REFERENCES pages(id,workspace_id), ADD FOREIGN KEY(uploaded_by) REFERENCES users(id);
ALTER TABLE favorites ADD FOREIGN KEY(workspace_id) REFERENCES workspaces(id), ADD FOREIGN KEY(user_id) REFERENCES users(id);
ALTER TABLE recent_visits ADD FOREIGN KEY(workspace_id) REFERENCES workspaces(id), ADD FOREIGN KEY(user_id) REFERENCES users(id);
ALTER TABLE stickies ADD FOREIGN KEY(workspace_id) REFERENCES workspaces(id), ADD FOREIGN KEY(user_id) REFERENCES users(id);
ALTER TABLE preferences ADD FOREIGN KEY(workspace_id) REFERENCES workspaces(id), ADD FOREIGN KEY(project_id,workspace_id) REFERENCES projects(id,workspace_id), ADD FOREIGN KEY(user_id) REFERENCES users(id);
ALTER TABLE intake_items ADD FOREIGN KEY(submitted_by) REFERENCES users(id), ADD FOREIGN KEY(duplicate_of) REFERENCES work_items(id);
ALTER TABLE api_tokens ADD FOREIGN KEY(workspace_id) REFERENCES workspaces(id), ADD FOREIGN KEY(user_id) REFERENCES users(id);
ALTER TABLE webhooks ADD FOREIGN KEY(workspace_id) REFERENCES workspaces(id), ADD FOREIGN KEY(created_by) REFERENCES users(id);
ALTER TABLE webhook_deliveries ADD FOREIGN KEY(workspace_id) REFERENCES workspaces(id), ADD FOREIGN KEY(webhook_id) REFERENCES webhooks(id);
ALTER TABLE exports ADD FOREIGN KEY(workspace_id) REFERENCES workspaces(id), ADD FOREIGN KEY(project_id,workspace_id) REFERENCES projects(id,workspace_id), ADD FOREIGN KEY(requested_by) REFERENCES users(id);
ALTER TABLE public_sites ADD FOREIGN KEY(project_id,workspace_id) REFERENCES projects(id,workspace_id);
