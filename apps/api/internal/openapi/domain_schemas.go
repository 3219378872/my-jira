package openapi

func addPlanningSchemas(s S, name S) {
	cycle := S{"name": name, "description": stringSchema(), "start_date": nullable(dateSchema()), "end_date": nullable(dateSchema()), "owner_id": uuidSchema(), "position": numberSchema(), "settings": freeObject(), "archived": boolSchema()}
	s["CycleCreate"], s["CyclePatch"] = object(cycle, "name"), object(cycle)
	cycleResponse := S{}
	for key, value := range cycle {
		if key != "archived" {
			cycleResponse[key] = value
		}
	}
	cycleResponse["archived_at"], cycleResponse["work_item_count"] = nullable(timestamp()), integer()
	s["Cycle"] = scoped(cycleResponse)
	module := S{"name": name, "description": stringSchema(), "start_date": nullable(dateSchema()), "target_date": nullable(dateSchema()), "lead_id": nullable(uuidSchema()), "status": enum("backlog", "planned", "in-progress", "paused", "completed", "cancelled"), "member_ids": array(uuidSchema()), "position": numberSchema(), "settings": freeObject(), "archived": boolSchema()}
	s["ModuleCreate"], s["ModulePatch"] = object(module, "name"), object(module)
	moduleResponse := S{}
	for key, value := range module {
		if key != "archived" {
			moduleResponse[key] = value
		}
	}
	moduleResponse["archived_at"], moduleResponse["work_item_count"] = nullable(timestamp()), integer()
	s["Module"] = scoped(moduleResponse)
	view := S{"name": name, "description": stringSchema(), "layout": enum("list", "kanban", "calendar", "spreadsheet", "gantt"), "filters": freeObject(), "display": freeObject(), "is_private": boolSchema(), "position": numberSchema()}
	s["ViewCreate"], s["ViewPatch"] = object(view, "name"), object(view)
	viewResponse := S{}
	for key, value := range view {
		viewResponse[key] = value
	}
	viewResponse["owner_id"] = uuidSchema()
	s["View"] = scoped(viewResponse)
	progressBucket := S{"id": nullable(uuidSchema()), "name": stringSchema(), "group": stringSchema(), "color": stringSchema(), "avatar_url": stringSchema(), "count": integer(), "completed": integer(), "cancelled": integer(), "started": integer(), "estimate": numberSchema(), "estimate_completed": numberSchema()}
	progressState := S{}
	for key, value := range progressBucket {
		progressState[key] = value
	}
	progressState["state_id"] = uuidSchema()
	optionalDate := S{"anyOf": []any{dateSchema(), enum("")}, "description": "Empty when the planning resource is unscheduled."}
	s["Progress"] = object(S{
		"total": integer(), "completed": integer(), "cancelled": integer(), "started": integer(), "backlog": integer(), "unstarted": integer(), "overdue": integer(),
		"completion_percentage": numberSchema(), "estimate_total": numberSchema(), "estimate_completed": numberSchema(),
		"states":             array(object(progressState, "id", "state_id", "name", "count", "completed", "cancelled", "started", "estimate", "estimate_completed")),
		"assignees":          array(object(progressBucket, "id", "name", "count", "completed", "cancelled", "started", "estimate", "estimate_completed")),
		"labels":             array(object(progressBucket, "id", "name", "count", "completed", "cancelled", "started", "estimate", "estimate_completed")),
		"burndown":           array(object(S{"date": dateSchema(), "remaining": nullable(integer()), "estimate_remaining": nullable(numberSchema()), "ideal": numberSchema(), "estimate_ideal": numberSchema()}, "date", "remaining", "estimate_remaining", "ideal", "estimate_ideal")),
		"burndown_step_days": S{"type": "integer", "minimum": 1}, "start_date": optionalDate, "end_date": optionalDate, "is_snapshot": boolSchema(), "snapshot_at": nullable(timestamp()),
	}, "total", "completed", "cancelled", "started", "backlog", "unstarted", "overdue", "completion_percentage", "estimate_total", "estimate_completed", "states", "assignees", "labels", "burndown", "burndown_step_days", "start_date", "end_date", "is_snapshot", "snapshot_at")
	s["Progress"].(S)["description"] = "Scoped progress and estimates. Cycles retain the first pre-transfer snapshot by default; live=true returns current items. Guest snapshots include only the caller's created items. Missing assignee or label buckets use null IDs. Future actual burndown values are null; long periods use the stated day stride."
	point := S{"label": S{"type": "string", "minLength": 1, "maxLength": 20}, "numeric_value": nullable(S{"type": "number", "minimum": 0}), "position": numberSchema()}
	s["EstimatePointInput"], s["EstimatePointPatch"] = object(point, "label"), object(point)
	pointResponse := S{"estimate_id": uuidSchema()}
	for key, value := range point {
		pointResponse[key] = value
	}
	s["EstimatePoint"] = scoped(pointResponse)
	s["EstimateCreate"] = object(S{"name": name, "description": stringSchema(), "kind": enum("points", "categories"), "points": S{"type": "array", "items": ref("EstimatePointInput"), "minItems": 1, "maxItems": 100}}, "name", "points")
	s["EstimatePatch"] = object(S{"name": name, "description": stringSchema()})
	s["Estimate"] = scoped(S{"name": name, "description": stringSchema(), "kind": enum("points", "categories"), "points": array(ref("EstimatePoint"))})
	s["EstimateTemplate"] = object(S{"id": stringSchema(), "name": stringSchema(), "kind": enum("points", "categories"), "points": array(ref("EstimatePointInput"))})
	s["EstimateSettings"] = object(S{"estimate_id": nullable(uuidSchema()), "estimate": nullable(ref("Estimate"))})
	s["CycleDateCheck"] = object(S{"start_date": dateSchema(), "end_date": dateSchema(), "exclude_cycle_id": nullable(uuidSchema())}, "start_date", "end_date")
	s["CycleDateResult"] = object(S{"available": boolSchema(), "conflicts": array(ref("Cycle"))}, "available", "conflicts")
	s["Automation"] = object(S{"archive_after_months": S{"type": "integer", "minimum": 0, "maximum": 12}, "close_after_months": S{"type": "integer", "minimum": 0, "maximum": 12}, "close_state_id": nullable(uuidSchema())})
	s["AutomationResult"] = object(S{"archived": integer(), "closed": integer()}, "archived", "closed")
	page := S{"name": name, "content_html": stringSchema(), "content_json": ref("RichTextNode"), "content_binary": S{"type": "string", "contentEncoding": "base64", "description": "Yjs encoded document state."}, "parent_id": nullable(uuidSchema()), "is_private": boolSchema(), "is_locked": boolSchema(), "archived": boolSchema(), "position": numberSchema(), "icon": stringSchema()}
	s["PageCreate"] = object(page)
	pagePatch := S{"version": version()}
	for key, value := range page {
		pagePatch[key] = value
	}
	s["PagePatch"] = object(pagePatch)
	s["PagePatch"].(S)["allOf"] = []any{S{"if": S{"anyOf": []any{S{"required": []string{"name"}}, S{"required": []string{"content_html"}}, S{"required": []string{"content_json"}}, S{"required": []string{"content_binary"}}}}, "then": S{"required": []string{"version"}}}}
	pageResponse := S{"version": version(), "owner_id": uuidSchema(), "archived_at": nullable(timestamp())}
	for key, value := range page {
		if key != "archived" {
			pageResponse[key] = value
		}
	}
	s["Page"] = scoped(pageResponse)
	s["PageContent"] = object(S{"content_html": stringSchema(), "content_json": ref("RichTextNode"), "content_binary": nullable(S{"type": "string", "contentEncoding": "base64"}), "version": version(), "owner_id": uuidSchema(), "can_edit": boolSchema(), "is_locked": boolSchema(), "is_private": boolSchema(), "archived_at": nullable(timestamp())})
	s["PageContentInput"] = object(S{"content_html": stringSchema(), "content_json": ref("RichTextNode"), "content_binary": S{"type": "string", "contentEncoding": "base64"}, "version": version()}, "version")
	s["PageVersion"] = entity(S{"workspace_id": uuidSchema(), "page_id": uuidSchema(), "saved_by": uuidSchema(), "name": stringSchema(), "content_html": stringSchema(), "content_json": ref("RichTextNode"), "content_binary": nullable(S{"type": "string", "contentEncoding": "base64"}), "version": version()})
}

func addSupportSchemas(s S, name, color S) {
	s["Notification"] = scoped(S{"user_id": uuidSchema(), "actor_id": nullable(uuidSchema()), "entity_type": stringSchema(), "entity_id": nullable(uuidSchema()), "title": stringSchema(), "body": stringSchema(), "data": object(S{"event_id": uuidSchema(), "action": stringSchema(), "work_item_id": uuidSchema(), "reasons": array(enum("mentions", "assigned", "subscribed", "created")), "silent": boolSchema()}), "read_at": nullable(timestamp()), "archived_at": nullable(timestamp()), "snoozed_until": nullable(timestamp())})
	s["NotificationPatch"] = object(S{"read": boolSchema(), "archived": boolSchema(), "snoozed_until": nullable(timestamp())})
	s["NotificationPage"] = object(S{"data": array(ref("Notification")), "pagination": object(S{"total": integer(), "limit": integer(), "offset": integer(), "next_offset": nullable(integer()), "has_more": boolSchema()}, "total", "limit", "offset", "next_offset", "has_more")}, "data", "pagination")
	s["NotificationPreferences"] = object(S{"in_app": boolSchema(), "email": boolSchema(), "mentions": boolSchema(), "assigned": boolSchema(), "subscribed": boolSchema(), "created": boolSchema()})
	entityType := enum("project", "issue", "cycle", "module", "view", "page")
	s["BookmarkInput"] = object(S{"entity_type": entityType, "entity_id": uuidSchema(), "position": numberSchema()}, "entity_type", "entity_id")
	resolved := S{"anyOf": []any{ref("Project"), ref("WorkItem"), ref("Cycle"), ref("Module"), ref("View"), ref("Page")}}
	s["Favorite"] = entity(S{"workspace_id": uuidSchema(), "user_id": uuidSchema(), "entity_type": entityType, "entity_id": uuidSchema(), "position": numberSchema(), "entity": resolved})
	s["RecentVisit"] = entity(S{"workspace_id": uuidSchema(), "user_id": uuidSchema(), "entity_type": entityType, "entity_id": uuidSchema(), "visited_at": timestamp(), "entity": resolved})
	s["Preference"] = scoped(S{"user_id": uuidSchema(), "scope": stringSchema(), "value": freeObject()})
	s["PreferenceResult"] = S{"anyOf": []any{ref("Preference"), object(S{"scope": stringSchema(), "value": freeObject()}, "scope", "value")}}
	sticky := S{"title": stringSchema(), "content_html": stringSchema(), "content_json": ref("RichTextNode"), "color": color, "position": numberSchema(), "is_archived": boolSchema()}
	s["StickyInput"], s["Sticky"] = object(sticky), scoped(sticky)
	s["SearchResults"] = object(S{"projects": array(ref("Project")), "issues": array(ref("WorkItem")), "cycles": array(ref("Cycle")), "modules": array(ref("Module")), "pages": array(ref("Page"))})
	count := object(S{"state_id": uuidSchema(), "project_id": uuidSchema(), "name": stringSchema(), "identifier": stringSchema(), "count": integer(), "color": stringSchema(), "priority": stringSchema(), "group": stringSchema(), "total": integer(), "completed": integer(), "created": integer(), "assigned": integer(), "pending": integer()})
	s["Analytics"] = object(S{"total": integer(), "completed": integer(), "started": integer(), "overdue": integer(), "estimate": numberSchema(), "x_axis": stringSchema(), "segment": stringSchema(), "metric": enum("count", "estimate"), "interval": enum("day", "week", "month"), "from": stringSchema(), "to": stringSchema(), "date_field": stringSchema(), "distribution": array(object(S{"key": stringSchema(), "label": stringSchema(), "segment_key": stringSchema(), "segment_label": stringSchema(), "count": integer(), "estimate": numberSchema(), "value": numberSchema()})), "by_state": array(count), "by_priority": array(count), "by_project": array(count), "trend": array(object(S{"date": dateSchema(), "created": integer(), "completed": integer()}))})
	analysisQuery := S{"x_axis": stringSchema(), "segment": stringSchema(), "metric": enum("count", "estimate"), "interval": enum("day", "week", "month"), "date_field": enum("created_at", "updated_at", "completed_at", "start_date", "target_date"), "from": stringSchema(), "to": stringSchema()}
	for _, key := range []string{"id", "project_id", "state_id", "state_group", "parent_id", "created_by", "priority", "sequence_id", "estimate", "estimate_point_id", "assignee_id", "label_id", "cycle_id", "module_id", "subscriber_id", "mention_id", "search", "archived", "draft", "deleted", "include_subitems", "scheduled", "filter", "intake_status"} {
		analysisQuery[key] = stringSchema()
	}
	for _, key := range []string{"start_date", "target_date", "created_at", "updated_at", "completed_at"} {
		analysisQuery[key], analysisQuery[key+"_before"], analysisQuery[key+"_after"] = stringSchema(), stringSchema(), stringSchema()
	}
	s["AnalyticsQuery"] = object(analysisQuery)
	s["AnalyticsQuery"].(S)["description"] = "A saved map of query-parameter strings, not arrays or numbers. The filter value is itself a JSON-encoded FilterExpression string. At most 60 keys and 40,000 encoded bytes."
	s["SavedAnalysisInput"] = object(S{"name": name, "description": stringSchema(), "query": ref("AnalyticsQuery")})
	s["SavedAnalysis"] = entity(S{"workspace_id": uuidSchema(), "owner_id": uuidSchema(), "name": name, "description": stringSchema(), "query": ref("AnalyticsQuery")})
	s["ProfileStats"] = object(S{"created": integer(), "assigned": integer(), "pending": integer(), "completed": integer(), "subscribed": integer(), "by_state": array(count), "by_priority": array(count), "by_project": array(count), "cycles": array(object(S{"id": uuidSchema(), "project_id": uuidSchema(), "name": stringSchema(), "start_date": nullable(dateSchema()), "end_date": nullable(dateSchema()), "status": enum("current", "upcoming"), "total": integer(), "completed": integer()}))})
	s["Profile"] = object(S{"user": object(S{"id": uuidSchema(), "display_name": stringSchema(), "first_name": stringSchema(), "last_name": stringSchema(), "avatar_url": stringSchema(), "timezone": stringSchema(), "joined_at": timestamp()}), "stats": ref("ProfileStats")}, "user", "stats")
	s["ProfileActivities"] = object(S{"items": array(ref("Activity")), "total": integer(), "limit": integer(), "offset": integer(), "has_more": boolSchema()}, "items", "total", "limit", "offset", "has_more")
}

func addIntegrationSchemas(s S, name S) {
	s["Asset"] = scoped(S{"workspace_id": nullable(uuidSchema()), "work_item_id": nullable(uuidSchema()), "page_id": nullable(uuidSchema()), "uploaded_by": uuidSchema(), "filename": stringSchema(), "content_type": stringSchema(), "size_bytes": integer(), "upload_status": stringSchema(), "metadata": object(S{"public": boolSchema()}), "download_url": stringSchema(), "url": stringSchema()})
	s["AssetBatchEntry"] = object(S{"entity_type": enum("work_item", "page"), "entity_id": uuidSchema(), "assets": array(ref("Asset"))}, "entity_type", "entity_id", "assets")
	s["AssetUpload"] = object(S{"file": S{"type": "string", "format": "binary", "description": "1 byte to 25 MiB; content type is detected from bytes."}, "work_item_id": uuidSchema(), "page_id": uuidSchema()}, "file")
	s["AssetPatch"] = object(S{"filename": name, "public": boolSchema()})
	s["APITokenInput"] = object(S{"name": S{"type": "string", "minLength": 1, "maxLength": 100}, "expires_at": nullable(timestamp())}, "name")
	s["APIToken"] = entity(S{"workspace_id": uuidSchema(), "user_id": uuidSchema(), "name": stringSchema(), "prefix": stringSchema(), "token": S{"type": "string", "description": "Returned once by creation only. Store securely."}, "last_used_at": nullable(timestamp()), "expires_at": nullable(timestamp()), "revoked_at": nullable(timestamp())})
	webhook := S{"url": S{"type": "string", "format": "uri"}, "events": array(enum("project.changed", "work_item.changed", "comment.changed", "cycle.changed", "cycle.items_changed", "module.changed", "module.items_changed", "*")), "is_active": boolSchema()}
	s["WebhookInput"] = object(webhook)
	webhookResponse := S{"workspace_id": uuidSchema(), "created_by": uuidSchema(), "secret": S{"type": "string", "description": "Signing secret is returned only when a webhook is created."}}
	for key, value := range webhook {
		webhookResponse[key] = value
	}
	s["Webhook"] = entity(webhookResponse)
	s["WebhookDelivery"] = entity(S{"workspace_id": uuidSchema(), "webhook_id": uuidSchema(), "event_id": uuidSchema(), "request": freeObject(), "response_status": nullable(integer()), "response_body": stringSchema(), "attempts": integer(), "delivered_at": nullable(timestamp())})
	s["QueuedEvent"] = object(S{"event_id": uuidSchema(), "status": enum("queued")}, "event_id", "status")
	s["ExportInput"] = object(S{"format": enum("csv", "json", "xlsx"), "project_id": nullable(uuidSchema()), "filters": freeObject()}, "format")
	s["Export"] = scoped(S{"requested_by": uuidSchema(), "format": enum("csv", "json", "xlsx"), "filters": freeObject(), "status": enum("queued", "processing", "completed", "failed"), "error_message": stringSchema(), "completed_at": nullable(timestamp()), "download_url": stringSchema()})
	publicSite := S{"slug": S{"type": "string", "minLength": 3, "maxLength": 64, "pattern": "^[a-z0-9-]+$"}, "title": name, "description": stringSchema(), "is_enabled": boolSchema(), "comments_enabled": boolSchema(), "reactions_enabled": boolSchema(), "votes_enabled": boolSchema(), "intake_enabled": boolSchema(), "settings": freeObject()}
	s["PublicSiteInput"] = object(publicSite, "slug", "title")
	s["PublicSite"] = scoped(publicSite)
	s["PublicSiteSummary"] = object(publicSite)
	s["PublicWorkItem"] = entity(S{"name": name, "description_html": stringSchema(), "sequence_id": integer(), "priority": enum("none", "low", "medium", "high", "urgent"), "start_date": nullable(dateSchema()), "target_date": nullable(dateSchema()), "state": ref("State"), "vote_count": integer(), "voted": boolSchema(), "labels": array(ref("Label"))})
	s["Vote"] = object(S{"vote_count": integer(), "voted": boolSchema()}, "vote_count", "voted")
	s["IntakeItem"] = scoped(S{"work_item_id": uuidSchema(), "submitted_by": uuidSchema(), "status": enum("pending", "accepted", "rejected", "duplicate", "snoozed"), "source": stringSchema(), "duplicate_of": nullable(uuidSchema()), "snoozed_until": nullable(timestamp()), "metadata": freeObject(), "work_item": ref("WorkItem")})
	s["IntakeResolution"] = object(S{"status": enum("accepted", "rejected", "duplicate", "snoozed"), "duplicate_of": nullable(uuidSchema()), "snoozed_until": nullable(timestamp())}, "status")
	s["ImageSearchResult"] = object(S{"id": stringSchema(), "alt": nullable(stringSchema()), "preview_url": stringSchema(), "url": stringSchema(), "author": stringSchema(), "author_url": stringSchema(), "source_url": stringSchema()})
	s["AITextInput"] = object(S{"instruction": S{"type": "string", "minLength": 1, "maxLength": 4000}, "content": S{"type": "string", "maxLength": 50000}}, "instruction")
	s["AITextResult"] = object(S{"text": stringSchema(), "model": stringSchema()}, "text", "model")
}

func addServiceSchemas(s S) {
	credential := nullable(S{"type": "string", "writeOnly": true, "description": "Omit to preserve the stored credential; null explicitly clears it. Never returned by read APIs."})
	email := S{"host": stringSchema(), "port": S{"anyOf": []any{S{"type": "integer", "minimum": 1, "maximum": 65535}, S{"type": "string", "pattern": "^[0-9]+$"}}}, "from": stringSchema(), "user": stringSchema(), "password": credential, "secure": boolSchema()}
	storage := S{"endpoint": stringSchema(), "public_endpoint": stringSchema(), "bucket": stringSchema(), "access_key": credential, "secret_key": credential, "secure": boolSchema(), "region": stringSchema()}
	oauth := S{"client_id": stringSchema(), "client_secret": credential, "base_url": stringSchema(), "authorize_url": stringSchema(), "token_url": stringSchema(), "userinfo_url": stringSchema(), "emails_url": stringSchema()}
	ai := S{"provider": stringSchema(), "base_url": stringSchema(), "model": enum("gpt-5.6-terra"), "wire_api": enum("responses"), "api_key": credential}
	unsplash := S{"access_key": credential}
	for name, fields := range map[string]S{"EmailService": email, "StorageService": storage, "OAuthService": oauth, "AIService": ai, "UnsplashService": unsplash} {
		s[name+"Input"] = object(fields)
		public := S{"configured": boolSchema(), "source": enum("environment", "instance"), "error": stringSchema()}
		for key, value := range fields {
			if key == "password" || key == "access_key" || key == "secret_key" || key == "client_secret" || key == "api_key" {
				public[key+"_configured"] = boolSchema()
			} else {
				public[key] = value
			}
		}
		s[name] = object(public)
	}
	// Stable order keeps generated artifacts deterministic despite Go map order.
	s["ServicePatch"] = S{"anyOf": []any{ref("EmailServiceInput"), ref("StorageServiceInput"), ref("OAuthServiceInput"), ref("AIServiceInput"), ref("UnsplashServiceInput")}}
	s["ServiceConfiguration"] = S{"anyOf": []any{ref("EmailService"), ref("StorageService"), ref("OAuthService"), ref("AIService"), ref("UnsplashService")}}
	s["Services"] = object(S{"secret_storage_enabled": boolSchema(), "email": ref("EmailService"), "storage": ref("StorageService"), "ai": ref("AIService"), "unsplash": ref("UnsplashService"), "oauth": object(S{"google": ref("OAuthService"), "github": ref("OAuthService"), "gitlab": ref("OAuthService"), "gitea": ref("OAuthService")})})
}
