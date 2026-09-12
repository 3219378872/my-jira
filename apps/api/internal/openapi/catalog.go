package openapi

import "strings"

type catalogBuilder struct{ entries map[string]operation }

func (b catalogBuilder) add(method, path, tag, summary string, request, response S, status int) {
	access := "session"
	if strings.HasPrefix(path, "/workspaces") {
		access = "workspace"
	}
	b.entries[method+" "+path] = operation{Summary: summary, Tag: tag, Request: request, Response: response, Status: status, Access: access}
}
func (b catalogBuilder) edit(method, path string, change func(*operation)) {
	key := method + " " + path
	value := b.entries[key]
	change(&value)
	b.entries[key] = value
}
func (b catalogBuilder) access(method, path, access string) {
	b.edit(method, path, func(v *operation) { v.Access = access })
}
func (b catalogBuilder) describe(method, path, text string) {
	b.edit(method, path, func(v *operation) { v.Description = text })
}
func (b catalogBuilder) query(method, path string, values ...any) {
	b.edit(method, path, func(v *operation) { v.Queries = append(v.Queries, values...) })
}
func query(name string, schema S, description string) S {
	return S{"name": name, "in": "query", "required": false, "schema": schema, "description": description}
}
func requiredQuery(name string, schema S, description string) S {
	value := query(name, schema, description)
	value["required"] = true
	return value
}
func (b catalogBuilder) crud(path, parameter, tag, noun, create, patch string) {
	b.add("GET", path, tag, "List "+strings.ToLower(noun)+" records", nil, array(ref(noun)), 200)
	b.add("POST", path, tag, "Create "+strings.ToLower(noun), ref(create), ref(noun), 201)
	item := path + "/:" + parameter
	b.add("GET", item, tag, "Get "+strings.ToLower(noun), nil, ref(noun), 200)
	b.add("PATCH", item, tag, "Update "+strings.ToLower(noun), ref(patch), ref(noun), 200)
	b.add("DELETE", item, tag, "Delete "+strings.ToLower(noun), nil, nil, 204)
}

func routeCatalog() map[string]operation {
	b := catalogBuilder{entries: map[string]operation{}}
	b.add("GET", "/openapi.json", "API", "Get the generated OpenAPI contract", nil, object(S{"openapi": stringSchema(), "info": object(S{"title": stringSchema(), "version": stringSchema(), "description": stringSchema()}), "paths": freeObject(), "components": freeObject(), "servers": array(object(S{"url": stringSchema(), "description": stringSchema()})), "jsonSchemaDialect": stringSchema()}, "openapi", "info", "paths"), 200)
	b.edit("GET", "/openapi.json", func(v *operation) { v.Access, v.Raw = "anonymous", true })
	b.add("GET", "/instance", "Authentication", "Read instance setup and sign-in availability", nil, ref("Instance"), 200)
	b.access("GET", "/instance", "anonymous")
	b.add("PATCH", "/instance", "Administration", "Update instance name and registration", ref("InstancePatch"), ref("Instance"), 200)
	for _, item := range []struct {
		path, request string
		status        int
	}{{"/instance/setup", "Setup", 201}, {"/auth/register", "Register", 201}, {"/auth/login", "Credentials", 200}} {
		b.add("POST", item.path, "Authentication", "Authenticate and create a browser session", ref(item.request), ref("AuthResult"), item.status)
		b.access("POST", item.path, "csrf")
	}
	b.add("GET", "/auth/csrf", "Authentication", "Get a CSRF token and matching cookie", nil, object(S{"csrf_token": stringSchema()}, "csrf_token"), 200)
	b.access("GET", "/auth/csrf", "anonymous")
	b.add("POST", "/auth/logout", "Authentication", "Revoke the current browser session", nil, nil, 204)
	b.add("GET", "/auth/me", "Account", "Get the current user", nil, ref("User"), 200)
	b.add("PATCH", "/auth/me", "Account", "Update the current user and merge preferences", ref("UserPatch"), ref("User"), 200)
	for _, method := range []string{"GET", "PATCH"} {
		var request S
		if method == "PATCH" {
			request = ref("Location")
		}
		b.add(method, "/auth/last-visited", "Account", "Read or save the last accessible workspace and project", request, ref("Location"), 200)
	}
	b.add("POST", "/auth/password", "Account", "Set or change the password and revoke other sessions", object(S{"current_password": S{"type": "string", "writeOnly": true}, "new_password": S{"type": "string", "minLength": 10, "maxLength": 72, "writeOnly": true}}, "new_password"), nil, 204)
	b.describe("POST", "/auth/password", "An existing password requires current_password. Accounts created through magic login or OAuth may set their first password. Password limits are 10–72 UTF-8 bytes.")
	b.add("GET", "/auth/sessions", "Account", "List the current user's sessions", nil, array(ref("Session")), 200)
	b.add("DELETE", "/auth/sessions/:sessionID", "Account", "Revoke one owned session", nil, nil, 204)
	b.add("GET", "/auth/accounts", "Account", "List connected identity providers", nil, array(ref("ConnectedAccount")), 200)
	b.add("DELETE", "/auth/accounts/:accountID", "Account", "Unlink a provider while preserving an authentication method", nil, nil, 204)
	emailInput := object(S{"email": S{"type": "string", "format": "email"}}, "email")
	tokenInput := object(S{"token": stringSchema()}, "token")
	b.add("POST", "/auth/forgot-password", "Authentication", "Request a password reset email without revealing account existence", emailInput, ref("Message"), 200)
	b.add("POST", "/auth/reset-password", "Authentication", "Redeem a one-use reset token and revoke all sessions", object(S{"token": stringSchema(), "new_password": S{"type": "string", "minLength": 10, "maxLength": 72, "writeOnly": true}}, "token", "new_password"), nil, 204)
	b.add("POST", "/auth/magic/request", "Authentication", "Request a six-digit email sign-in code", emailInput, object(S{"challenge_id": uuidSchema()}, "challenge_id"), 200)
	b.add("POST", "/auth/magic/verify", "Authentication", "Redeem an email sign-in code", object(S{"challenge_id": uuidSchema(), "code": S{"type": "string", "pattern": "^[0-9]{6}$"}, "display_name": stringSchema()}, "challenge_id", "code"), ref("AuthResult"), 200)
	for _, path := range []string{"/auth/forgot-password", "/auth/reset-password", "/auth/magic/request", "/auth/magic/verify"} {
		b.access("POST", path, "csrf")
	}
	for _, path := range []string{"/auth/oauth/:provider", "/auth/oauth/:provider/callback"} {
		b.add("GET", path, "Authentication", "Complete provider sign-in using state and PKCE", nil, nil, 302)
		b.access("GET", path, "anonymous")
	}
	b.query("GET", "/auth/oauth/:provider/callback", query("code", stringSchema(), "Provider authorization code."), query("state", stringSchema(), "One-use state matching the browser's OAuth cookie."), query("error", stringSchema(), "Provider denial code."))
	for _, prefix := range []string{"/auth/email-verification", "/auth/email-change"} {
		var input S
		if prefix == "/auth/email-change" {
			input = emailInput
		}
		b.add("POST", prefix+"/request", "Account", "Send an email confirmation link", input, ref("Message"), 200)
		b.add("POST", prefix+"/confirm", "Account", "Confirm a one-use email token", tokenInput, nil, 204)
	}
	b.add("GET", "/admin/stats", "Administration", "Read instance administration statistics", nil, ref("AdminStats"), 200)
	b.add("GET", "/admin/users", "Administration", "List instance users", nil, array(ref("User")), 200)
	b.query("GET", "/admin/users", query("search", stringSchema(), "Search email or display name; at most 1,000 results."))
	b.add("PATCH", "/admin/users/:userID", "Administration", "Suspend or promote a user while preserving the final active admin", object(S{"is_active": boolSchema(), "is_instance_admin": boolSchema()}), ref("User"), 200)
	b.add("GET", "/admin/workspaces", "Administration", "List instance workspaces and resource counts", nil, array(ref("Workspace")), 200)
	b.add("GET", "/admin/configuration", "Administration", "Read instance behavior settings", nil, ref("InstanceConfiguration"), 200)
	b.add("PATCH", "/admin/configuration", "Administration", "Update instance behavior settings", ref("InstanceConfigurationPatch"), ref("InstanceConfiguration"), 200)
	b.add("GET", "/admin/services", "Services", "Inspect service configuration without credential values", nil, ref("Services"), 200)
	b.add("PATCH", "/admin/services/:service", "Services", "Persist encrypted service configuration", ref("ServicePatch"), ref("ServiceConfiguration"), 200)
	b.describe("PATCH", "/admin/services/:service", "Select fields for the service named in the path. Omitted credentials are preserved; null explicitly clears them. APP_ENCRYPTION_KEY must be configured. Updates merge under an instance lock and immediately override environment defaults.")
	b.add("POST", "/admin/email/test", "Services", "Send a test message through the configured SMTP server", emailInput, object(S{"accepted": boolSchema(), "message": stringSchema()}, "accepted", "message"), 200)
	b.describe("POST", "/admin/email/test", "Success means the SMTP server accepted the message; it does not prove inbox delivery.")
	b.add("POST", "/admin/storage/test", "Services", "Check object storage connectivity and bucket existence", nil, object(S{"connected": boolSchema(), "bucket_exists": boolSchema()}, "connected", "bucket_exists"), 200)
	b.crud("/workspaces", "workspaceID", "Workspaces", "Workspace", "WorkspaceCreate", "WorkspacePatch")
	b.access("POST", "/workspaces", "session")
	b.add("GET", "/workspaces/availability", "Workspaces", "Check a normalized workspace slug", nil, object(S{"slug": stringSchema(), "available": boolSchema()}, "slug", "available"), 200)
	b.access("GET", "/workspaces/availability", "session")
	b.query("GET", "/workspaces/availability", requiredQuery("slug", stringSchema(), "Candidate slug; normalized to lowercase."))
	w := "/workspaces/:workspaceID"
	p := w + "/projects/:projectID"
	b.add("POST", w+"/leave", "Workspaces", "Leave a workspace and its projects", nil, nil, 204)
	b.describe("POST", w+"/leave", "Removes current memberships atomically. The workspace and every active project must retain an active admin; otherwise returns 409.")
	b.add("POST", w+"/join", "Workspaces", "Accept an invitation for this workspace", tokenInput, ref("Workspace"), 200)
	b.add("POST", "/invitations/accept", "Workspaces", "Accept a recipient-bound workspace invitation", tokenInput, ref("Workspace"), 200)
	for _, prefix := range []string{w, p} {
		b.add("GET", prefix+"/members", "Memberships", "List active members", nil, array(ref("Membership")), 200)
		input := ref("InviteInput")
		if prefix == p {
			input = object(S{"user_id": uuidSchema(), "role": S{"type": "integer", "enum": []int{5, 15, 20}}}, "user_id")
		}
		b.add("POST", prefix+"/members", "Memberships", "Add a member", input, ref("Membership"), 201)
		b.add("PATCH", prefix+"/members/:memberID", "Memberships", "Change a membership role", object(S{"role": S{"type": "integer", "enum": []int{5, 15, 20}}}, "role"), ref("Membership"), 200)
		b.add("DELETE", prefix+"/members/:memberID", "Memberships", "Remove a member while preserving administrators", nil, nil, 204)
		for _, path := range []string{prefix + "/labels", prefix + "/states"} {
			if strings.HasSuffix(path, "/states") && prefix == w {
				continue
			}
			kind, parameter := "Label", "labelID"
			if strings.HasSuffix(path, "/states") {
				kind, parameter = "State", "stateID"
			}
			b.add("GET", path, "Catalog", "List "+strings.ToLower(kind)+" records", nil, array(ref(kind)), 200)
			b.add("POST", path, "Catalog", "Create "+strings.ToLower(kind), ref(kind+"Create"), ref(kind), 201)
			if kind == "Label" {
				b.add("POST", path+"/bulk", "Catalog", "Create an atomic batch of scoped labels", object(S{"labels": S{"type": "array", "items": ref("LabelCreate"), "minItems": 1, "maxItems": 100}}, "labels"), array(ref("Label")), 201)
				b.describe("POST", path+"/bulk", "Requires Member. Each label uses this route's workspace or project scope. Parent IDs must already exist in the same hierarchy. Invalid fields, parents or duplicate names roll back the entire batch.")
			}
			b.add("PATCH", path+"/:"+parameter, "Catalog", "Update "+strings.ToLower(kind), ref(kind+"Input"), ref(kind), 200)
			b.add("DELETE", path+"/:"+parameter, "Catalog", "Delete "+strings.ToLower(kind), nil, nil, 204)
		}
	}
	b.add("GET", w+"/invitations", "Memberships", "List workspace invitations without token values", nil, array(ref("Invitation")), 200)
	b.add("POST", w+"/invitations", "Memberships", "Invite an email address to the workspace", ref("InviteInput"), ref("Invitation"), 201)
	b.add("DELETE", w+"/invitations/:invitationID", "Memberships", "Revoke an unaccepted invitation", nil, nil, 204)
	b.crud(w+"/projects", "projectID", "Projects", "Project", "ProjectCreate", "ProjectPatch")
	b.add("GET", w+"/projects/availability", "Projects", "Check a project identifier within this workspace", nil, object(S{"identifier": stringSchema(), "available": boolSchema()}, "identifier", "available"), 200)
	b.query("GET", w+"/projects/availability", requiredQuery("identifier", stringSchema(), "Candidate identifier; normalized to uppercase. Requires Member."))
	b.add("POST", p+"/join", "Projects", "Join a public project with the permitted member role", nil, ref("Membership"), 200)
	b.add("POST", p+"/leave", "Projects", "Leave explicit project membership while retaining a project admin", nil, nil, 204)
	b.describe("PATCH", p, "Requires project Admin. Feature and display settings merge without discarding other settings. Use the dedicated automation and estimate routes for those domains.")
	addWorkItemRoutes(b, w, p)
	addPlanningRoutes(b, w, p)
	addSupportRoutes(b, w)
	addIntegrationRoutes(b, w, p)
	addRequirementsRoutes(b, p)
	return b.entries
}
