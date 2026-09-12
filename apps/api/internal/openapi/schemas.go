package openapi

func ref(name string) S { return S{"$ref": "#/components/schemas/" + name} }
func stringSchema() S   { return S{"type": "string"} }
func uuidSchema() S     { return S{"type": "string", "format": "uuid"} }
func timestamp() S      { return S{"type": "string", "format": "date-time"} }
func dateSchema() S     { return S{"type": "string", "format": "date"} }
func boolSchema() S     { return S{"type": "boolean"} }
func numberSchema() S   { return S{"type": "number"} }
func integer() S        { return S{"type": "integer", "format": "int64"} }
func version() S {
	return S{"type": "integer", "format": "int64", "minimum": 1, "description": "Current persisted version. Stale writes return 409 without replacing content."}
}
func array(items S) S         { return S{"type": "array", "items": items} }
func nullable(value S) S      { return S{"anyOf": []any{value, S{"type": "null"}}} }
func enum(values ...string) S { return S{"type": "string", "enum": values} }
func freeObject() S {
	return S{"type": "object", "additionalProperties": true, "description": "Application-specific JSON values; surrounding typed fields still define the operation contract."}
}
func object(properties S, required ...string) S {
	result := S{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}
func entity(properties S) S {
	base := S{"id": uuidSchema(), "created_at": timestamp(), "updated_at": timestamp(), "deleted_at": nullable(timestamp())}
	for key, value := range properties {
		base[key] = value
	}
	result := object(base, "id")
	result["additionalProperties"] = true
	return result
}
func properties(name string, source S) S {
	result := S{}
	for key, value := range source[name].(S)["properties"].(S) {
		result[key] = value
	}
	return result
}
func scoped(properties S) S {
	copy := S{"workspace_id": uuidSchema(), "project_id": nullable(uuidSchema())}
	for key, value := range properties {
		copy[key] = value
	}
	return entity(copy)
}

func schemas() S {
	s := S{}
	role := S{"type": "integer", "enum": []int{5, 15, 20}, "description": "Guest=5, Member=15, Admin=20. A workspace Guest cannot gain project Member access."}
	name := S{"type": "string", "minLength": 1, "maxLength": 255}
	email := S{"type": "string", "format": "email"}
	password := S{"type": "string", "format": "password", "minLength": 10, "maxLength": 72, "writeOnly": true, "description": "The server enforces 10–72 UTF-8 bytes."}
	color := S{"type": "string", "pattern": "^#[0-9a-fA-F]{6}$"}
	s["Error"] = object(S{"error": object(S{"code": stringSchema(), "message": stringSchema(), "fields": S{"type": "object", "additionalProperties": stringSchema()}}, "code", "message")}, "error")
	s["Pagination"] = object(S{"next_cursor": nullable(stringSchema()), "has_more": boolSchema(), "total": integer()}, "next_cursor", "has_more", "total")
	s["Message"] = object(S{"message": stringSchema()}, "message")
	s["Location"] = object(S{"workspace_id": nullable(uuidSchema()), "project_id": nullable(uuidSchema())})
	s["User"] = entity(S{"email": email, "display_name": stringSchema(), "first_name": stringSchema(), "last_name": stringSchema(), "avatar_url": stringSchema(), "timezone": stringSchema(), "is_active": boolSchema(), "is_instance_admin": boolSchema(), "email_verified": boolSchema(), "password_set": boolSchema(), "preferences": freeObject(), "workspace_count": integer()})
	s["UserSummary"] = object(S{"id": uuidSchema(), "display_name": stringSchema(), "avatar_url": stringSchema()})
	s["UserPatch"] = object(S{"display_name": name, "first_name": stringSchema(), "last_name": stringSchema(), "avatar_url": stringSchema(), "timezone": stringSchema(), "preferences": freeObject()})
	s["Credentials"] = object(S{"email": email, "password": password}, "email", "password")
	s["Register"] = object(S{"email": email, "password": password, "display_name": name}, "email", "password", "display_name")
	s["Setup"] = object(S{"instance_name": name, "email": email, "password": password, "display_name": name}, "instance_name", "email", "password", "display_name")
	s["AuthResult"] = object(S{"user": ref("User"), "csrf_token": stringSchema()}, "user", "csrf_token")
	s["Instance"] = object(S{"is_setup_done": boolSchema(), "name": stringSchema(), "registration_enabled": boolSchema(), "auth_methods": array(stringSchema())})
	s["InstancePatch"] = object(S{"name": name, "registration_enabled": boolSchema()})
	s["InstanceConfiguration"] = object(S{"name": name, "registration_enabled": boolSchema(), "settings": object(S{"allow_workspace_creation": boolSchema(), "magic_login_enabled": boolSchema()}), "configured_auth_methods": array(stringSchema()), "email_configured": boolSchema()})
	s["InstanceConfigurationPatch"] = object(S{"name": name, "registration_enabled": boolSchema(), "settings": object(S{"allow_workspace_creation": boolSchema(), "magic_login_enabled": boolSchema()})})
	s["Session"] = entity(S{"expires_at": timestamp(), "user_agent": stringSchema(), "ip_address": stringSchema(), "is_current": boolSchema()})
	s["ConnectedAccount"] = entity(S{"provider": enum("google", "github", "gitlab", "gitea"), "email": email, "subject": stringSchema()})
	s["AdminStats"] = object(S{"users": integer(), "active_users": integer(), "workspaces": integer(), "projects": integer(), "work_items": integer(), "storage_bytes": integer(), "pending_jobs": integer(), "active_sessions": integer()})
	workspaceFields := S{"name": name, "slug": S{"type": "string", "pattern": "^[a-z0-9]+(?:-[a-z0-9]+)*$", "maxLength": 48}, "timezone": stringSchema(), "description": stringSchema(), "logo_url": stringSchema()}
	s["WorkspaceCreate"], s["WorkspacePatch"] = object(workspaceFields, "name", "slug"), object(workspaceFields)
	workspaceResponse := S{}
	for key, value := range workspaceFields {
		workspaceResponse[key] = value
	}
	workspaceResponse["owner_id"], workspaceResponse["role"] = uuidSchema(), role
	s["Workspace"] = entity(workspaceResponse)
	s["Membership"] = scoped(S{"user_id": uuidSchema(), "role": role, "is_active": boolSchema(), "email": email, "display_name": stringSchema(), "avatar_url": stringSchema()})
	s["Invitation"] = entity(S{"workspace_id": uuidSchema(), "email": email, "role": role, "invited_by": uuidSchema(), "expires_at": timestamp(), "accepted_at": nullable(timestamp()), "revoked_at": nullable(timestamp())})
	s["InviteInput"] = object(S{"email": email, "role": role}, "email")
	s["ProjectFeatures"] = object(S{"cycles": boolSchema(), "modules": boolSchema(), "pages": boolSchema(), "views": boolSchema(), "intake": boolSchema()})
	projectFields := S{"name": name, "identifier": S{"type": "string", "pattern": "^[A-Za-z][A-Za-z0-9]{0,11}$"}, "description": stringSchema(), "network": enum("public", "private"), "guest_can_view_all": S{"type": "boolean", "default": false, "description": "Expands Guest read visibility within this accessible project without increasing the Guest's write role. Existing project privacy and resource ownership restrictions still apply."}, "icon": stringSchema(), "color": color, "features": ref("ProjectFeatures"), "cover_image_url": nullable(stringSchema())}
	s["ProjectCreate"] = object(projectFields, "name", "identifier")
	projectPatch := S{}
	for key, value := range projectFields {
		projectPatch[key] = value
	}
	projectPatch["timezone"], projectPatch["lead_id"], projectPatch["default_assignee_id"], projectPatch["settings"] = stringSchema(), nullable(uuidSchema()), nullable(uuidSchema()), freeObject()
	projectPatch["archived_at"] = nullable(enum("now"))
	s["ProjectPatch"] = object(projectPatch)
	projectResponse := S{}
	for key, value := range projectPatch {
		projectResponse[key] = value
	}
	projectResponse["archived_at"], projectResponse["role"], projectResponse["is_member"], projectResponse["estimate_id"] = nullable(timestamp()), role, boolSchema(), nullable(uuidSchema())
	s["Project"] = scoped(projectResponse)
	stateFields := S{"name": name, "color": color, "group": enum("backlog", "unstarted", "started", "completed", "cancelled"), "position": numberSchema(), "is_default": boolSchema()}
	s["StateCreate"], s["StateInput"], s["State"] = object(stateFields, "name"), object(stateFields), scoped(stateFields)
	labelFields := S{"name": name, "color": color, "description": stringSchema(), "parent_id": nullable(uuidSchema()), "position": numberSchema()}
	s["LabelCreate"], s["LabelInput"], s["Label"] = object(labelFields, "name"), object(labelFields), scoped(labelFields)
	s["RichTextNode"] = object(S{"type": stringSchema(), "text": stringSchema(), "attrs": freeObject(), "content": array(ref("RichTextNode")), "marks": array(object(S{"type": stringSchema(), "attrs": freeObject()}, "type"))})
	s["RichTextNode"].(S)["additionalProperties"] = true
	issueFields := S{"name": name, "description_html": stringSchema(), "description_json": ref("RichTextNode"), "state_id": uuidSchema(), "priority": enum("none", "low", "medium", "high", "urgent"), "parent_id": nullable(uuidSchema()), "position": numberSchema(), "start_date": nullable(dateSchema()), "target_date": nullable(dateSchema()), "estimate": nullable(numberSchema()), "estimate_point_id": nullable(uuidSchema()), "assignee_ids": array(uuidSchema()), "label_ids": array(uuidSchema()), "cycle_id": nullable(uuidSchema()), "module_ids": array(uuidSchema()), "is_draft": boolSchema(), "archived_at": nullable(timestamp()), "type_name": stringSchema()}
	for field, schema := range requirementItemFields() {
		issueFields[field] = schema
	}
	s["WorkItemCreate"] = object(issueFields, "name")
	s["WorkItemChanges"] = object(issueFields)
	issuePatch := S{}
	for key, value := range issueFields {
		issuePatch[key] = value
	}
	issuePatch["version"] = version()
	s["WorkItemPatch"] = object(issuePatch, "version")
	issueResponse := S{}
	for key, value := range issuePatch {
		issueResponse[key] = value
	}
	issueResponse["sequence_id"], issueResponse["created_by"], issueResponse["updated_by"] = integer(), uuidSchema(), uuidSchema()
	issueResponse["completed_at"], issueResponse["archived_at"] = nullable(timestamp()), nullable(timestamp())
	issueResponse["state_detail"], issueResponse["project_detail"], issueResponse["estimate_point_detail"] = ref("State"), ref("Project"), nullable(ref("EstimatePoint"))
	issueResponse["assignee_details"], issueResponse["label_details"] = array(ref("UserSummary")), array(ref("Label"))
	issueResponse["sub_item_count"], issueResponse["comment_count"], issueResponse["attachment_count"] = integer(), integer(), integer()
	s["WorkItem"] = scoped(issueResponse)
	s["WorkItemPage"] = object(S{"data": array(ref("WorkItem")), "pagination": ref("Pagination")}, "data", "pagination")
	s["WorkItemGroup"] = object(S{"key": stringSchema(), "label": stringSchema(), "total": integer(), "items": array(ref("WorkItem")), "groups": array(ref("WorkItemGroup")), "pagination": ref("Pagination")}, "key", "label", "total")
	s["GroupedWorkItems"] = object(S{"data": array(ref("WorkItemGroup")), "group_by": stringSchema(), "sub_group_by": stringSchema(), "total_items": integer(), "pagination": ref("Pagination")}, "data", "group_by", "pagination")
	s["FilterExpression"] = S{"oneOf": []any{object(S{"and": array(ref("FilterExpression"))}, "and"), object(S{"or": array(ref("FilterExpression"))}, "or"), object(S{"not": ref("FilterExpression")}, "not"), object(S{"field": stringSchema(), "op": enum("eq", "ne", "in", "not_in", "all", "is_empty", "not_empty", "contains", "starts_with", "gt", "gte", "lt", "lte"), "value": S{}}, "field")}, "description": "Boolean filter tree: at most 80 nodes, depth six, 200 inclusion values and 32 KiB encoded JSON."}
	s["BulkWorkItems"] = object(S{"ids": S{"type": "array", "items": uuidSchema(), "minItems": 1, "maxItems": 200, "uniqueItems": true}, "changes": ref("WorkItemChanges"), "versions": S{"type": "object", "additionalProperties": version(), "propertyNames": uuidSchema()}}, "ids", "versions")
	s["Activity"] = scoped(S{"work_item_id": nullable(uuidSchema()), "actor_id": uuidSchema(), "action": stringSchema(), "field_name": stringSchema(), "old_value": S{}, "new_value": S{}, "metadata": freeObject()})
	s["WorkItemVersion"] = scoped(S{"work_item_id": uuidSchema(), "saved_by": uuidSchema(), "version": version(), "snapshot": ref("WorkItem")})
	commentFields := S{"body_html": stringSchema(), "body_json": ref("RichTextNode"), "parent_id": nullable(uuidSchema())}
	s["CommentInput"] = object(commentFields, "body_html")
	s["Comment"] = scoped(S{"work_item_id": nullable(uuidSchema()), "page_id": nullable(uuidSchema()), "parent_id": nullable(uuidSchema()), "author_id": uuidSchema(), "author": ref("UserSummary"), "body_html": stringSchema(), "body_json": ref("RichTextNode"), "edited_at": nullable(timestamp()), "is_public": boolSchema()})
	s["LinkInput"] = object(S{"title": stringSchema(), "url": S{"type": "string", "format": "uri"}}, "url")
	s["Link"] = scoped(S{"work_item_id": nullable(uuidSchema()), "module_id": nullable(uuidSchema()), "created_by": uuidSchema(), "title": stringSchema(), "url": stringSchema()})
	s["RelationInput"] = object(S{"target_id": uuidSchema(), "relation_type": enum("related", "blocks", "blocked_by", "duplicate")}, "target_id", "relation_type")
	s["Relation"] = scoped(S{"source_id": uuidSchema(), "target_id": uuidSchema(), "relation_type": stringSchema(), "target": ref("WorkItem")})
	s["Subscriber"] = scoped(S{"work_item_id": uuidSchema(), "user_id": uuidSchema(), "display_name": stringSchema(), "avatar_url": stringSchema()})
	s["ReactionInput"] = object(S{"emoji": S{"type": "string", "minLength": 1, "maxLength": 32}, "comment_id": nullable(uuidSchema())}, "emoji")
	s["Reaction"] = scoped(S{"work_item_id": nullable(uuidSchema()), "comment_id": nullable(uuidSchema()), "user_id": uuidSchema(), "emoji": stringSchema()})
	addPlanningSchemas(s, name)
	addSupportSchemas(s, name, color)
	addIntegrationSchemas(s, name)
	addServiceSchemas(s)
	addRequirementsSchemas(s)
	return s
}
