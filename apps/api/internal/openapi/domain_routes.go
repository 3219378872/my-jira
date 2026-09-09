package openapi

func addWorkItemRoutes(b catalogBuilder, w, p string) {
	issues := p + "/issues"
	b.crud(issues, "issueID", "Work items", "WorkItem", "WorkItemCreate", "WorkItemPatch")
	b.add("GET", w+"/issues", "Work items", "List visible work items across the workspace", nil, ref("WorkItemPage"), 200)
	b.add("GET", w+"/issues/lookup/:identifier", "Work items", "Resolve a visible work item by project identifier and sequence", nil, ref("WorkItem"), 200)
	b.query("GET", w+"/issues/lookup/:identifier", query("project_archived", enum("true", "false", "all"), "Default false; project archive visibility."), query("archived", enum("true", "false", "all"), "Default false; work-item archive visibility."), query("draft", enum("true", "false", "all"), "Default false; draft visibility."), query("filter", stringSchema(), "Optional work-item FilterExpression."))
	for _, path := range []string{issues, w + "/issues"} {
		b.edit("GET", path, func(v *operation) {
			v.Response, v.Raw = S{"oneOf": []any{ref("WorkItemPage"), ref("GroupedWorkItems")}}, true
		})
		b.query("GET", path, issueQueries()...)
		b.describe("GET", path, "Lists apply current tenant, private-project and Guest-own-item access before filtering. Unaccepted intake records, drafts, deleted and archived items are excluded by default. Normal lists return data plus pagination; group_by returns nested groups and independent leaf cursors. Ordering is stable over current data, not a historical snapshot across concurrent edits.")
	}
	b.describe("PATCH", issues+"/:issueID", "The current version is required. All property/relationship changes, activity, version history and outbox events commit atomically. Stale versions return 409. Choose estimate_point_id when a scheme is active; estimate and estimate_point_id cannot be supplied together.")
	b.add("PATCH", issues+"/bulk", "Work items", "Atomically edit up to 200 work items", ref("BulkWorkItems"), array(ref("WorkItem")), 200)
	b.add("DELETE", issues+"/bulk", "Work items", "Atomically delete up to 200 work items", ref("BulkWorkItems"), nil, 204)
	b.describe("PATCH", issues+"/bulk", "versions maps every selected UUID to its current integer version. A failed item rolls back the entire batch. changes contains ordinary editable fields without its own version.")
	item := issues + "/:issueID"
	b.add("POST", item+"/duplicate", "Work items", "Duplicate a work item and its supported associations", nil, ref("WorkItem"), 201)
	moveChanges := object(S{"position": numberSchema(), "priority": enum("urgent", "high", "medium", "low", "none"), "assignee_ids": array(uuidSchema()), "label_ids": array(uuidSchema()), "cycle_id": nullable(uuidSchema()), "module_ids": array(uuidSchema()), "estimate_point_id": nullable(uuidSchema())})
	b.add("POST", item+"/move", "Work items", "Move a work item into another project in this workspace", object(S{"project_id": uuidSchema(), "state_id": uuidSchema(), "version": version(), "changes": moveChanges}, "project_id", "version"), ref("WorkItem"), 200)
	b.describe("POST", item+"/move", "Requires Member access to the source and target. The current version is checked. Target state must be in the destination project; omitted state selects its default. Old project-specific associations are cleared. Optional changes selects destination groups and position in the same transaction; a rejected field, association or version rolls back the entire move, sequence allocation and events.")
	b.add("GET", item+"/activities", "Work items", "Read work-item activity", nil, array(ref("Activity")), 200)
	b.add("GET", item+"/versions", "Work items", "Read work-item version snapshots", nil, array(ref("WorkItemVersion")), 200)
	comments(b, item, "Work items")
	for _, spec := range []struct{ path, parameter, name, input string }{{"links", "linkID", "Link", "LinkInput"}, {"relations", "relationID", "Relation", "RelationInput"}, {"reactions", "reactionID", "Reaction", "ReactionInput"}} {
		b.add("GET", item+"/"+spec.path, "Work items", "List work-item "+spec.path, nil, array(ref(spec.name)), 200)
		b.add("POST", item+"/"+spec.path, "Work items", "Add work-item "+spec.path, ref(spec.input), ref(spec.name), 201)
		b.add("DELETE", item+"/"+spec.path+"/:"+spec.parameter, "Work items", "Remove work-item "+spec.path, nil, nil, 204)
	}
	b.add("GET", item+"/subscribers", "Work items", "List work-item subscribers", nil, array(ref("Subscriber")), 200)
	b.add("POST", item+"/subscribers", "Work items", "Subscribe the current user or an eligible project member", object(S{"user_id": nullable(uuidSchema())}), object(S{"subscribed": boolSchema(), "user_id": uuidSchema()}, "subscribed", "user_id"), 200)
	b.edit("POST", item+"/subscribers", func(v *operation) { v.RequestOptional = true })
	b.describe("POST", item+"/subscribers", "With no user_id, subscribes the current user. Managing another subscriber requires Member; the target must currently belong to the project and be eligible to see this work item.")
	b.add("DELETE", item+"/subscribers", "Work items", "Unsubscribe the current user", nil, nil, 204)
	b.add("DELETE", item+"/subscribers/:userID", "Work items", "Remove a subscriber with current Member permission", nil, nil, 204)
	b.add("GET", p+"/automation", "Automation", "Read project automation", nil, ref("Automation"), 200)
	b.add("PATCH", p+"/automation", "Automation", "Configure automatic closing and archiving", ref("Automation"), ref("Automation"), 200)
	b.describe("PATCH", p+"/automation", "Requires Admin. Month counts are 0–12; zero disables an operation and one month means 30 days. The close target must be a completed/cancelled state in this project. Cycles/modules without an expired ending date and pending/snoozed intake prevent maintenance of their items.")
	b.add("POST", p+"/automation/run", "Automation", "Run this project's current automation rules", nil, ref("AutomationResult"), 200)
	b.add("GET", p+"/intake", "Intake", "List project intake submissions", nil, array(ref("IntakeItem")), 200)
	b.query("GET", p+"/intake", query("status", enum("pending", "accepted", "rejected", "duplicate", "snoozed"), "Optional intake status."))
	b.add("POST", p+"/intake", "Intake", "Create a project intake submission", ref("WorkItemCreate"), ref("IntakeItem"), 201)
	b.add("GET", p+"/intake/:intakeID", "Intake", "Read an intake submission with its work item", nil, ref("IntakeItem"), 200)
	b.add("PATCH", p+"/intake/:intakeID", "Intake", "Edit an intake submission with version protection", ref("WorkItemPatch"), ref("IntakeItem"), 200)
	b.add("DELETE", p+"/intake/:intakeID", "Intake", "Remove a permitted intake submission", nil, nil, 204)
	b.add("GET", p+"/intake/:intakeID/versions", "Intake", "List intake work-item history", nil, array(ref("WorkItemVersion")), 200)
	b.add("POST", p+"/intake/:intakeID/resolve", "Intake", "Accept, reject, duplicate or snooze an intake submission", object(S{"status": enum("accepted", "rejected", "duplicate", "snoozed"), "duplicate_of": nullable(uuidSchema()), "snoozed_until": nullable(timestamp()), "state_id": nullable(uuidSchema()), "version": version()}, "status"), ref("IntakeItem"), 200)
}

func comments(b catalogBuilder, prefix, tag string) {
	b.add("GET", prefix+"/comments", tag, "List comments", nil, array(ref("Comment")), 200)
	b.add("POST", prefix+"/comments", tag, "Create a comment", ref("CommentInput"), ref("Comment"), 201)
	b.add("PATCH", prefix+"/comments/:commentID", tag, "Edit an owned comment", ref("CommentInput"), ref("Comment"), 200)
	b.add("DELETE", prefix+"/comments/:commentID", tag, "Delete a comment with author or administrator permission", nil, nil, 204)
}

func issueQueries() []any {
	result := []any{
		query("limit", S{"type": "integer", "minimum": 1, "maximum": 500, "default": 100}, "Items per page, or per leaf when grouped."),
		query("cursor", stringSchema(), "Opaque continuation returned by the corresponding page; keep filters and ordering unchanged."),
		query("order_by", stringSchema(), "Up to four comma-separated fields: position,created_at,updated_at,name,sequence_id,start_date,target_date,completed_at,estimate,state,priority; prefix - for descending."),
		query("search", stringSchema(), "Match title or project identifier and sequence."),
		query("filter", stringSchema(), "URL-encoded JSON FilterExpression; boolean trees, relative dates, comparisons and association all/in/not_in are supported."),
		query("include_subitems", boolSchema(), "False selects root items only; defaults to true."),
		query("scheduled", boolSchema(), "True requires both start and target dates; false selects incomplete schedules."),
		query("intake_status", stringSchema(), "Comma-separated pending,accepted,rejected,duplicate,snoozed; default excludes unaccepted submissions."),
		query("group_by", enum("state_id", "state_group", "priority", "project_id", "assignee_id", "label_id", "cycle_id", "module_id", "created_by", "estimate_point_id"), "Return grouped pages using the selected dimension."),
		query("sub_group_by", enum("state_id", "state_group", "priority", "project_id", "assignee_id", "label_id", "cycle_id", "module_id", "created_by", "estimate_point_id"), "Optional second dimension; must differ from group_by."),
		query("group_key", stringSchema(), "Continue a single group leaf; none selects a null association bucket."),
		query("sub_group_key", stringSchema(), "Continue one subgroup leaf."),
		query("group_limit", S{"type": "integer", "minimum": 1, "maximum": 200, "default": 100}, "Parent groups per page."),
		query("group_cursor", stringSchema(), "Opaque parent-group continuation."),
		query("show_empty", boolSchema(), "Include standard priority/state-group buckets, project states and empty association buckets."),
	}
	for _, key := range []string{"id", "project_id", "state_id", "state_group", "parent_id", "created_by", "priority", "estimate", "estimate_point_id", "sequence_id", "assignee_id", "label_id", "cycle_id", "module_id", "subscriber_id", "mention_id"} {
		result = append(result, query(key, stringSchema(), "Comma-separated inclusion filter. User references accept me; nullable references accept null/none. mention_id matches structured rich-text mentions."))
	}
	for _, key := range []string{"start_date", "target_date", "created_at", "updated_at", "completed_at"} {
		result = append(result, query(key, stringSchema(), "One or more YYYY-MM-DD values; timestamp fields are compared by UTC date."), query(key+"_before", dateSchema(), "Inclusive upper date bound."), query(key+"_after", dateSchema(), "Inclusive lower date bound."))
	}
	for _, key := range []string{"archived", "draft", "deleted", "project_archived"} {
		result = append(result, query(key, enum("true", "false", "all"), "Default false; deleted collection access requires Member."))
	}
	return result
}

func addPlanningRoutes(b catalogBuilder, w, p string) {
	for _, spec := range []struct{ path, parameter, name string }{{"cycles", "cycleID", "Cycle"}, {"modules", "moduleID", "Module"}, {"views", "viewID", "View"}} {
		b.crud(p+"/"+spec.path, spec.parameter, "Planning", spec.name, spec.name+"Create", spec.name+"Patch")
		b.query("GET", p+"/"+spec.path, query("search", stringSchema(), "Name search."), query("archived", boolSchema(), "True lists archived cycles/modules."))
		if spec.name == "View" {
			continue
		}
		item := p + "/" + spec.path + "/:" + spec.parameter
		b.add("GET", item+"/items", "Planning", "List visible work items in the collection", nil, array(ref("WorkItem")), 200)
		b.add("POST", item+"/items", "Planning", "Add work items to the collection transactionally", object(S{"work_item_ids": array(uuidSchema())}, "work_item_ids"), object(S{"work_item_ids": array(uuidSchema())}, "work_item_ids"), 200)
		b.add("DELETE", item+"/items/:itemID", "Planning", "Remove one work-item association", nil, nil, 204)
		b.add("GET", item+"/progress", "Planning", "Calculate visible progress and estimate totals", nil, ref("Progress"), 200)
		if spec.name == "Cycle" {
			b.query("GET", item+"/progress", query("live", boolSchema(), "True calculates current membership progress instead of a stored transfer snapshot. Current caller visibility applies to both modes."))
		}
	}
	b.crud(w+"/views", "viewID", "Planning", "View", "ViewCreate", "ViewPatch")
	b.add("POST", p+"/cycles/:cycleID/transfer", "Planning", "Transfer selected or all cycle work items", object(S{"target_cycle_id": uuidSchema(), "work_item_ids": array(uuidSchema())}, "target_cycle_id"), object(S{"source_cycle_id": uuidSchema(), "target_cycle_id": uuidSchema(), "work_item_ids": array(uuidSchema())}), 200)
	b.add("POST", p+"/cycles/check-dates", "Planning", "Check cycle date overlaps without changing a cycle", ref("CycleDateCheck"), ref("CycleDateResult"), 200)
	b.describe("POST", p+"/cycles/check-dates", "An advisory inclusive date-overlap check, including archived cycles. Creating or updating a cycle may still explicitly save overlap. Both boundaries are required when scheduling a cycle.")
	b.add("GET", p+"/estimate-templates", "Estimates", "List numeric and category estimate templates", nil, array(ref("EstimateTemplate")), 200)
	b.crud(p+"/estimates", "estimateID", "Estimates", "Estimate", "EstimateCreate", "EstimatePatch")
	b.add("GET", p+"/estimate-settings", "Estimates", "Read the active estimate scheme", nil, ref("EstimateSettings"), 200)
	b.add("PATCH", p+"/estimate-settings", "Estimates", "Switch or disable the active estimate scheme", object(S{"estimate_id": nullable(uuidSchema())}, "estimate_id"), ref("EstimateSettings"), 200)
	b.add("POST", p+"/estimates/:estimateID/points", "Estimates", "Create an estimate point", ref("EstimatePointInput"), ref("EstimatePoint"), 201)
	b.add("PATCH", p+"/estimates/:estimateID/points/:pointID", "Estimates", "Update a point and linked numeric projections", ref("EstimatePointPatch"), ref("EstimatePoint"), 200)
	b.add("DELETE", p+"/estimates/:estimateID/points/:pointID", "Estimates", "Delete a point and clear or replace current assignments", nil, nil, 204)
	b.query("DELETE", p+"/estimates/:estimateID/points/:pointID", query("replacement_id", uuidSchema(), "Optional replacement point in the same live scheme."))
	for _, prefix := range []string{w, p} {
		b.crud(prefix+"/pages", "pageID", "Pages", "Page", "PageCreate", "PagePatch")
		b.add("GET", prefix+"/pages/summary", "Pages", "Count visible root pages by privacy and archive state", nil, object(S{"public_pages": integer(), "private_pages": integer(), "archived_pages": integer(), "total": integer()}, "public_pages", "private_pages", "archived_pages", "total"), 200)
		b.query("GET", prefix+"/pages", query("search", stringSchema(), "Page name search."), query("archived", boolSchema(), "Select archived pages."))
		page := prefix + "/pages/:pageID"
		b.describe("PATCH", page, "Content/title changes require the current version and return 409 on conflict. Private pages are owner-only. Lock/archive/visibility changes require the owner or permitted admin; editing content requires an unlocked active page.")
		b.describe("DELETE", page, "The page must be archived and have no active child pages. Private-page and current membership checks still apply.")
		b.add("GET", page+"/content", "Pages", "Read current editor content and edit permissions", nil, ref("PageContent"), 200)
		b.add("GET", page+"/resources", "Pages", "Read a page's links, headings and authorized attached assets", nil, object(S{"page_id": uuidSchema(), "version": version(), "links": array(object(S{"url": stringSchema(), "text": stringSchema(), "kind": enum("link", "image", "embed")}, "url", "text", "kind")), "headings": array(object(S{"level": S{"type": "integer", "minimum": 1, "maximum": 6}, "text": stringSchema(), "id": stringSchema()}, "level", "text", "id")), "assets": array(ref("Asset"))}, "page_id", "version", "links", "headings", "assets"), 200)
		b.add("PUT", page+"/content", "Pages", "Persist editor content with optimistic concurrency", ref("PageContentInput"), ref("Page"), 200)
		b.add("POST", page+"/duplicate", "Pages", "Duplicate a page under the current owner", nil, ref("Page"), 201)
		b.add("GET", page+"/versions", "Pages", "List page snapshots", nil, array(ref("PageVersion")), 200)
		b.add("GET", page+"/versions/:versionID", "Pages", "Read one page snapshot", nil, ref("PageVersion"), 200)
		b.add("POST", page+"/versions/:versionID/restore", "Pages", "Restore a snapshot as a new current version", object(S{"version": version()}, "version"), ref("Page"), 200)
		comments(b, page, "Pages")
	}
	links := p + "/modules/:moduleID/links"
	b.add("GET", links, "Planning", "List module links", nil, array(ref("Link")), 200)
	b.add("POST", links, "Planning", "Create a module link", ref("LinkInput"), ref("Link"), 201)
	b.add("PATCH", links+"/:linkID", "Planning", "Update a module link", object(S{"title": stringSchema(), "url": S{"type": "string", "format": "uri"}}), ref("Link"), 200)
	b.add("DELETE", links+"/:linkID", "Planning", "Delete a module link", nil, nil, 204)
}
