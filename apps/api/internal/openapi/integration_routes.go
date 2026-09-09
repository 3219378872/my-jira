package openapi

func addSupportRoutes(b catalogBuilder, w string) {
	b.add("GET", w+"/search", "Workspace tools", "Search the current user's visible workspace resources", nil, ref("SearchResults"), 200)
	b.query("GET", w+"/search", query("q", S{"type": "string", "maxLength": 200}, "Search text; omitted or empty returns empty categories. At most 25 visible results per resource category."))
	b.add("GET", w+"/analytics", "Analytics", "Calculate statistics from currently visible work items", nil, ref("Analytics"), 200)
	for _, value := range issueQueries() {
		name := value.(S)["name"].(string)
		if name != "limit" && name != "cursor" && name != "order_by" && name != "group_by" && name != "sub_group_by" && name != "group_key" && name != "sub_group_key" && name != "group_limit" && name != "group_cursor" && name != "show_empty" && name != "project_archived" {
			b.query("GET", w+"/analytics", value)
		}
	}
	axis := enum("priority", "state", "state_group", "project", "estimate", "label", "assignee", "cycle", "module", "start_date", "target_date", "created_at", "completed_at", "updated_at")
	b.query("GET", w+"/analytics", query("x_axis", axis, "Main analysis dimension; defaults to state_group."), query("segment", axis, "Optional distinct second dimension."), query("metric", enum("count", "estimate"), "Defaults to count."), query("interval", enum("day", "week", "month"), "Time grouping interval; defaults to day."), query("date_field", enum("created_at", "updated_at", "completed_at", "start_date", "target_date"), "Date filter field; defaults to created_at."), query("from", dateSchema(), "Inclusive start date; requested dates may span at most ten years."), query("to", dateSchema(), "Inclusive ending date."))
	b.add("GET", w+"/notifications", "Notifications", "List visible non-silent notifications with offset pagination", nil, ref("NotificationPage"), 200)
	b.edit("GET", w+"/notifications", func(v *operation) { v.Raw = true })
	b.add("GET", w+"/notifications/unread-count", "Notifications", "Count visible unread notifications", nil, object(S{"count": integer()}, "count"), 200)
	b.add("POST", w+"/notifications/mark-all-read", "Notifications", "Mark visible notifications read", nil, object(S{"updated": integer()}, "updated"), 200)
	for _, route := range []struct{ method, path string }{{"GET", w + "/notifications"}, {"POST", w + "/notifications/mark-all-read"}} {
		for _, key := range []string{"unread", "read", "archived", "snoozed"} {
			b.query(route.method, route.path, query(key, boolSchema(), "Select the corresponding notification state. Contradictory read/unread flags return 400."))
		}
		b.query(route.method, route.path, query("reason", stringSchema(), "Comma-separated mentions,assigned,created,subscribed; matches any selected recorded reason."), query("project_id", uuidSchema(), "Restrict to the selected visible project."))
	}
	b.query("GET", w+"/notifications", query("limit", S{"type": "integer", "minimum": 1, "maximum": 500, "default": 100}, "Notifications per page."), query("offset", S{"type": "integer", "minimum": 0, "maximum": 1000000, "default": 0}, "Continuation row offset."))
	b.add("PATCH", w+"/notifications/:notificationID", "Notifications", "Update an owned visible notification", ref("NotificationPatch"), ref("Notification"), 200)
	b.add("DELETE", w+"/notifications/:notificationID", "Notifications", "Delete an owned visible notification", nil, nil, 204)
	b.add("GET", w+"/favorites", "Workspace tools", "List favorites whose entities remain visible", nil, array(ref("Favorite")), 200)
	b.add("POST", w+"/favorites", "Workspace tools", "Favorite a visible entity", ref("BookmarkInput"), ref("Favorite"), 201)
	b.add("PATCH", w+"/favorites/:favoriteID", "Workspace tools", "Reorder an owned favorite", object(S{"position": numberSchema()}, "position"), ref("Favorite"), 200)
	b.add("DELETE", w+"/favorites/:favoriteID", "Workspace tools", "Remove an owned favorite", nil, nil, 204)
	b.add("GET", w+"/recent-visits", "Workspace tools", "List recent entities that remain visible", nil, array(ref("RecentVisit")), 200)
	b.add("POST", w+"/recent-visits", "Workspace tools", "Record a visit to a visible entity", object(S{"entity_type": enum("project", "issue", "cycle", "module", "view", "page"), "entity_id": uuidSchema()}, "entity_type", "entity_id"), ref("RecentVisit"), 200)
	b.add("GET", w+"/preferences", "Workspace tools", "List current user's workspace preferences", nil, array(ref("Preference")), 200)
	b.add("GET", w+"/preferences/:key", "Workspace tools", "Read one preference, defaulting to an empty value", nil, ref("PreferenceResult"), 200)
	for _, method := range []string{"PATCH", "PUT"} {
		b.add(method, w+"/preferences/:key", "Workspace tools", "Save a current-user preference", object(S{"value": freeObject()}, "value"), ref("Preference"), 200)
		b.describe(method, w+"/preferences/:key", "PATCH shallow-merges value; PUT replaces it. For key=notifications only in_app,email,mentions,assigned,subscribed,created boolean values are accepted, with omitted flags enabled by default. Caller identity always selects ownership.")
	}
	b.crud(w+"/stickies", "stickyID", "Workspace tools", "Sticky", "StickyInput", "StickyInput")
	b.query("GET", w+"/stickies", query("archived", boolSchema(), "Select archived notes."))
	// Additional analytics routes are registered by the owning module; these
	// reviewed definitions become visible only when the live routes exist.
	b.crud(w+"/analyses", "analysisID", "Analytics", "SavedAnalysis", "SavedAnalysisInput", "SavedAnalysisInput")
	b.add("GET", w+"/analyses/:analysisID/run", "Analytics", "Recalculate a saved analysis using current access", nil, ref("Analytics"), 200)
	b.add("POST", w+"/analytics/export", "Analytics", "Queue a CSV analysis report", object(S{"query": ref("AnalyticsQuery")}), ref("Export"), 202)
	b.add("GET", w+"/profiles/:userID", "Profiles", "Read an active member's profile and visible statistics", nil, ref("Profile"), 200)
	b.add("GET", w+"/profiles/:userID/stats", "Profiles", "Calculate a member's statistics within the caller's visible scope", nil, ref("ProfileStats"), 200)
	b.add("GET", w+"/profiles/:userID/activities", "Profiles", "Read permitted member activity with offset pagination", nil, ref("ProfileActivities"), 200)
	b.query("GET", w+"/profiles/:userID/activities", query("limit", S{"type": "integer", "minimum": 1, "maximum": 200, "default": 50}, "Activity rows per page."), query("offset", S{"type": "integer", "minimum": 0, "maximum": 1000000}, "Activity row offset."), query("from", dateSchema(), "Inclusive activity date bound."), query("to", dateSchema(), "Inclusive ending date."))
	b.add("GET", w+"/profiles/:userID/activities/export", "Profiles", "Export a bounded activity date range as CSV", nil, S{"type": "string", "format": "binary"}, 200)
	b.edit("GET", w+"/profiles/:userID/activities/export", func(v *operation) { v.Raw, v.ResponseMedia = true, "text/csv" })
	b.query("GET", w+"/profiles/:userID/activities/export", requiredQuery("from", dateSchema(), "Inclusive starting date."), requiredQuery("to", dateSchema(), "Inclusive ending date. At most 10,000 exported activities."))
}

func addIntegrationRoutes(b catalogBuilder, w, p string) {
	for _, prefix := range []string{"/auth", w, p} {
		assets := prefix + "/assets"
		b.add("GET", assets, "Files", "List authorized file assets", nil, array(ref("Asset")), 200)
		b.query("GET", assets, query("work_item_id", uuidSchema(), "Optional parent work item."), query("page_id", uuidSchema(), "Optional parent page."), query("deleted", boolSchema(), "List recoverable deleted assets."))
		b.add("POST", assets, "Files", "Upload a file into the requested authorized scope", ref("AssetUpload"), ref("Asset"), 201)
		b.edit("POST", assets, func(v *operation) { v.RequestMedia = "multipart/form-data" })
		b.add("GET", assets+"/:assetID/download", "Files", "Download a file after rechecking current access", nil, S{"type": "string", "format": "binary"}, 200)
		b.edit("GET", assets+"/:assetID/download", func(v *operation) { v.Raw, v.ResponseMedia = true, "application/octet-stream" })
		b.query("GET", assets+"/:assetID/download", query("inline", boolSchema(), "Only PNG/JPEG/GIF/WebP may be returned inline; other types use attachment disposition."))
		b.add("DELETE", assets+"/:assetID", "Files", "Soft-delete an owned file with a recovery window", nil, nil, 204)
		b.add("POST", assets+"/:assetID/restore", "Files", "Restore a file within the thirty-day recovery window", nil, ref("Asset"), 200)
		b.add("POST", assets+"/:assetID/copy", "Files", "Copy an authorized file to a permitted parent", object(S{"work_item_id": nullable(uuidSchema()), "page_id": nullable(uuidSchema())}), ref("Asset"), 201)
		b.add("PATCH", assets+"/:assetID", "Files", "Rename a file or change its public attachment visibility", ref("AssetPatch"), ref("Asset"), 200)
	}
	for _, prefix := range []string{w, p} {
		path := prefix + "/assets/batch"
		b.add("GET", path, "Files", "Read attachments grouped by currently visible parent entities", nil, array(ref("AssetBatchEntry")), 200)
		for _, name := range []string{"work_item_ids", "page_ids"} {
			parameter := query(name, S{"type": "array", "items": uuidSchema(), "minItems": 1, "maxItems": 100}, "Comma-separated nonzero UUIDs; repeated query parameters are also accepted.")
			parameter["style"], parameter["explode"] = "form", false
			b.query("GET", path, parameter)
		}
		b.query("GET", path, query("deleted", boolSchema(), "Default false selects live assets; true selects only deleted assets under the ordinary recovery-list rules."))
		b.describe("GET", path, "Supply at least one entity identifier and at most 100 in total across both parameters, counting duplicates. Repeated entities are returned once, with work items first and pages second, preserving each type's first-seen request order. Workspace requests may span visible projects and workspace pages; project requests require every parent to belong to that exact project. Each parent uses its current project and Guest/private-page rules. Any missing or inaccessible parent returns the same 404 for the entire batch, including parents with no assets; no partial result is returned. Authorized empty parents return assets: []. Each download URL uses the parent's actual project. Uncompleted uploads are excluded, and deleted assets are never mixed into a live result.")
	}
	attachments := p + "/issues/:issueID/attachments"
	b.add("GET", attachments, "Files", "List permitted work-item attachments", nil, array(ref("Asset")), 200)
	b.add("POST", attachments, "Files", "Upload a work-item attachment", ref("AssetUpload"), ref("Asset"), 201)
	b.edit("POST", attachments, func(v *operation) { v.RequestMedia = "multipart/form-data" })
	b.add("POST", w+"/ai/text", "Integrations", "Generate or transform text using configured AI", ref("AITextInput"), ref("AITextResult"), 200)
	b.add("GET", w+"/images", "Integrations", "Search configured image-provider results", nil, array(ref("ImageSearchResult")), 200)
	b.query("GET", w+"/images", requiredQuery("q", S{"type": "string", "minLength": 1, "maxLength": 200}, "Image search query."))
	b.add("GET", w+"/api-tokens", "Integrations", "List the current user's workspace API tokens", nil, array(ref("APIToken")), 200)
	b.add("POST", w+"/api-tokens", "Integrations", "Create a token and reveal its value once", ref("APITokenInput"), ref("APIToken"), 201)
	b.access("POST", w+"/api-tokens", "session")
	b.add("DELETE", w+"/api-tokens/:tokenID", "Integrations", "Revoke an owned workspace API token", nil, nil, 204)
	b.add("GET", w+"/webhooks", "Integrations", "List workspace webhooks without signing secrets", nil, array(ref("Webhook")), 200)
	b.add("POST", w+"/webhooks", "Integrations", "Create a webhook with a signing secret", ref("WebhookInput"), ref("Webhook"), 201)
	b.add("PATCH", w+"/webhooks/:webhookID", "Integrations", "Update a webhook's permitted destination and events", ref("WebhookInput"), ref("Webhook"), 200)
	b.add("DELETE", w+"/webhooks/:webhookID", "Integrations", "Remove a webhook", nil, nil, 204)
	b.add("GET", w+"/webhooks/:webhookID/deliveries", "Integrations", "Read webhook delivery attempts", nil, array(ref("WebhookDelivery")), 200)
	b.add("POST", w+"/webhooks/:webhookID/test", "Integrations", "Queue a signed webhook connection test", nil, ref("QueuedEvent"), 202)
	b.add("POST", w+"/webhooks/:webhookID/rotate-secret", "Integrations", "Rotate a webhook signing secret and reveal the new value once", nil, object(S{"id": uuidSchema(), "secret": stringSchema()}, "id", "secret"), 200)
	b.add("GET", w+"/exports", "Integrations", "List the current user's exports", nil, array(ref("Export")), 200)
	b.add("POST", w+"/exports", "Integrations", "Queue a scoped CSV, JSON or XLSX export", ref("ExportInput"), ref("Export"), 202)
	b.add("GET", w+"/exports/:exportID/download", "Integrations", "Download a ready owned export after current scope checks", nil, S{"type": "string", "format": "binary"}, 200)
	b.edit("GET", w+"/exports/:exportID/download", func(v *operation) { v.Raw, v.ResponseMedia = true, "application/octet-stream" })
	b.describe("GET", w+"/exports/:exportID/download", "Requires the requester and current Member access to every project represented by the file. Returns 409 until completed and 403 if scope changed since generation. Response content type matches csv/json/xlsx and the object URL is never made public.")
	b.add("GET", p+"/site", "Public sites", "Read project public-site configuration", nil, ref("PublicSite"), 200)
	b.add("PUT", p+"/site", "Public sites", "Configure the project public site and interaction flags", ref("PublicSiteInput"), ref("PublicSite"), 200)
	public := "/public/:slug"
	b.add("GET", public, "Public sites", "Read an enabled public site's visible configuration", nil, ref("PublicSiteSummary"), 200)
	b.add("GET", public+"/issues", "Public sites", "List public non-draft work items", nil, array(ref("PublicWorkItem")), 200)
	b.query("GET", public+"/issues", query("search", stringSchema(), "Title search among published work items."))
	b.add("GET", public+"/issues/:issueID", "Public sites", "Read a published work item", nil, ref("PublicWorkItem"), 200)
	item := public + "/issues/:issueID"
	publicComment := object(S{"body_html": S{"type": "string", "minLength": 1, "maxLength": 65536}}, "body_html")
	b.add("GET", item+"/comments", "Public sites", "List public comments", nil, array(ref("Comment")), 200)
	b.add("POST", item+"/comments", "Public sites", "Post an authenticated public comment", publicComment, ref("Comment"), 201)
	b.add("PATCH", item+"/comments/:commentID", "Public sites", "Edit the current user's public comment", publicComment, ref("Comment"), 200)
	b.add("DELETE", item+"/comments/:commentID", "Public sites", "Delete the current user's public comment", nil, nil, 204)
	publicReactions := array(object(S{"emoji": stringSchema(), "count": integer(), "reacted": boolSchema()}, "emoji", "count", "reacted"))
	b.add("GET", item+"/reactions", "Public sites", "Read grouped public reactions", nil, publicReactions, 200)
	for _, method := range []string{"POST", "DELETE"} {
		b.add(method, item+"/reactions", "Public sites", "Add or remove the current user's public reaction", object(S{"emoji": stringSchema()}, "emoji"), publicReactions, 200)
		b.add(method, item+"/vote", "Public sites", "Add or remove the current user's public vote", nil, ref("Vote"), 200)
	}
	b.add("GET", item+"/attachments", "Public sites", "List explicitly published work-item attachments", nil, array(ref("Asset")), 200)
	b.add("GET", item+"/attachments/:assetID/download", "Public sites", "Download an explicitly published attachment", nil, S{"type": "string", "format": "binary"}, 200)
	b.edit("GET", item+"/attachments/:assetID/download", func(v *operation) { v.Raw, v.ResponseMedia = true, "application/octet-stream" })
	b.add("POST", public+"/intake", "Public sites", "Submit an authenticated public intake item", object(S{"name": S{"type": "string", "minLength": 1, "maxLength": 255}, "description_html": S{"type": "string", "maxLength": 262144}}, "name"), object(S{"id": uuidSchema(), "status": enum("pending")}, "id", "status"), 201)
	for key, entry := range b.entries {
		if entry.Tag == "Public sites" && len(key) >= 12 && key[:12] == "GET /public/" {
			entry.Access = "optional"
			b.entries[key] = entry
		}
	}
}
