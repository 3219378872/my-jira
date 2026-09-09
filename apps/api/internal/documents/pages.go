package documents

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/editor"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/liveauth"
	"my-jira/apps/api/internal/support/data"
)

type service struct{ deps platform.Dependencies }

var htmlPolicy = data.EditorHTMLPolicy()
var pageFields = []string{"name", "content_html", "content_json", "content_binary", "parent_id", "is_private", "is_locked", "archived", "position", "icon", "version"}

const pageProjection = "(to_jsonb(p)-'deleted_at'-'content_binary') || jsonb_build_object('content_binary',COALESCE(encode(p.content_binary,'base64'),''))"

func Register(r *gin.RouterGroup, deps platform.Dependencies) {
	s := &service{deps: deps}
	for _, path := range []string{"/workspaces/:workspaceID/pages", "/workspaces/:workspaceID/projects/:projectID/pages"} {
		p := r.Group(path)
		p.GET("", s.list)
		p.GET("/summary", s.summary)
		p.POST("", s.create)
		p.GET("/:pageID", s.get)
		p.PATCH("/:pageID", s.update)
		p.DELETE("/:pageID", s.remove)
		p.GET("/:pageID/content", s.content)
		p.PUT("/:pageID/content", s.update)
		p.GET("/:pageID/resources", s.resources)
		p.POST("/:pageID/duplicate", s.duplicate)
		p.GET("/:pageID/versions", s.versions)
		p.GET("/:pageID/versions/:versionID", s.version)
		p.POST("/:pageID/versions/:versionID/restore", s.restore)
		p.GET("/:pageID/comments", s.comments)
		p.POST("/:pageID/comments", s.addComment)
		p.PATCH("/:pageID/comments/:commentID", s.editComment)
		p.DELETE("/:pageID/comments/:commentID", s.deleteComment)
	}
}

func (s *service) scope(c *gin.Context, min identity.Role) (identity.Scope, error) {
	actor, err := httpapi.Actor(c)
	if err != nil {
		return identity.Scope{}, err
	}
	wid, err := httpapi.UUIDParam(c, "workspaceID")
	if err != nil {
		return identity.Scope{}, err
	}
	if c.Param("projectID") == "" {
		return data.PageProjectScope(c.Request.Context(), s.deps.DB.SQL, actor, wid, uuid.Nil, min)
	}
	pid, err := httpapi.UUIDParam(c, "projectID")
	if err != nil {
		return identity.Scope{}, err
	}
	return data.PageProjectScope(c.Request.Context(), s.deps.DB.SQL, actor, wid, pid, min)
}

func project(scope identity.Scope) any {
	if scope.ProjectID == uuid.Nil {
		return nil
	}
	return scope.ProjectID
}

type page struct {
	ID, OwnerID         uuid.UUID
	IsPrivate, IsLocked bool
	ArchivedAt          *time.Time
	Version             int64
	Raw                 json.RawMessage
}

func (s *service) load(c *gin.Context, q database.DBTX, scope identity.Scope, id uuid.UUID, lock bool) (page, error) {
	var result page
	query := "SELECT p.id,p.owner_id,p.is_private,p.is_locked,p.archived_at,p.version," + pageProjection + " FROM pages p WHERE p.id=$1 AND p.workspace_id=$2 AND p.project_id IS NOT DISTINCT FROM $3::uuid AND " + data.VisiblePage("p", "$2", "$4")
	if lock {
		query += " FOR UPDATE"
	}
	var raw []byte
	err := q.QueryRowContext(c.Request.Context(), query, id, scope.WorkspaceID, project(scope), scope.Actor.UserID).Scan(&result.ID, &result.OwnerID, &result.IsPrivate, &result.IsLocked, &result.ArchivedAt, &result.Version, &raw)
	if err != nil {
		return result, err
	}
	if result.IsPrivate && result.OwnerID != scope.Actor.UserID {
		return result, data.Missing()
	}
	result.Raw = json.RawMessage(raw)
	return result, nil
}

func (s *service) pageScope(c *gin.Context, min identity.Role) (identity.Scope, uuid.UUID, error) {
	scope, err := s.scope(c, min)
	if err != nil {
		return scope, uuid.Nil, err
	}
	id, err := httpapi.UUIDParam(c, "pageID")
	return scope, id, err
}

func (s *service) list(c *gin.Context) {
	scope, err := s.scope(c, 5)
	if err != nil {
		data.Fail(c, err)
		return
	}
	where := "p.workspace_id=$1 AND p.project_id IS NOT DISTINCT FROM $2::uuid AND " + data.VisiblePage("p", "$1", "$3")
	args := []any{scope.WorkspaceID, project(scope), scope.Actor.UserID}
	if c.Query("archived") == "true" {
		where += " AND p.archived_at IS NOT NULL"
	} else {
		where += " AND p.archived_at IS NULL"
	}
	if text := strings.TrimSpace(c.Query("search")); text != "" {
		args = append(args, "%"+text+"%")
		where += " AND p.name ILIKE $4"
	}
	result, err := data.Many(c, s.deps.DB.SQL, "SELECT (to_jsonb(p)-'deleted_at'-'content_binary'-'content_json'-'content_html') FROM pages p WHERE "+where+" ORDER BY p.position,p.updated_at DESC,p.id", args...)
	data.Send(c, result, err)
}

func (s *service) get(c *gin.Context) {
	scope, id, err := s.pageScope(c, 5)
	if err != nil {
		data.Fail(c, err)
		return
	}
	value, err := s.load(c, s.deps.DB.SQL, scope, id, false)
	data.Send(c, value.Raw, err)
}
func (s *service) content(c *gin.Context) {
	scope, id, err := s.pageScope(c, 5)
	if err != nil {
		data.Fail(c, err)
		return
	}
	p, err := s.load(c, s.deps.DB.SQL, scope, id, false)
	if err != nil {
		data.Fail(c, err)
		return
	}
	var body map[string]any
	if err := json.Unmarshal(p.Raw, &body); err != nil {
		data.Fail(c, err)
		return
	}
	canEdit := !p.IsLocked && p.ArchivedAt == nil && (scope.Role >= 15 || p.OwnerID == scope.Actor.UserID)
	httpapi.JSON(c, 200, gin.H{"name": body["name"], "content_html": body["content_html"], "content_json": body["content_json"], "content_binary": body["content_binary"], "version": p.Version, "is_locked": p.IsLocked, "is_private": p.IsPrivate, "archived_at": p.ArchivedAt, "can_edit": canEdit, "owner_id": p.OwnerID})
}

func parse(input data.Object, creating bool) (map[string]any, error) {
	_, hasHTML := input["content_html"]
	_, hasJSON := input["content_json"]
	_, hasBinary := input["content_binary"]
	if (hasHTML || hasJSON || hasBinary) && (!hasHTML || !hasJSON) {
		return nil, data.Invalid("Content replacement requires content_html and content_json together; convert HTML using the live /documents/convert endpoint")
	}
	if hasJSON {
		if err := editor.Validate(input["content_json"]); err != nil {
			return nil, data.Invalid(err.Error())
		}
		var doc struct {
			Type    string            `json:"type"`
			Content []json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(input["content_json"], &doc); err != nil || doc.Type != "doc" || doc.Content == nil {
			return nil, data.Invalid("content_json must be a Tiptap doc with a content array")
		}
	}
	values := map[string]any{}
	if creating {
		values = map[string]any{"name": "Untitled", "content_html": "<p></p>", "content_json": `{"type":"doc","content":[{"type":"paragraph"}]}`, "is_private": false, "is_locked": false, "position": 0.0, "icon": ""}
	}
	for key := range input {
		var value any
		var err error
		switch key {
		case "name":
			value, err = input.String(key, true, 255)
		case "icon":
			value, err = input.String(key, false, 200)
		case "content_html":
			var text string
			text, err = input.String(key, false, 1<<20)
			value = htmlPolicy.Sanitize(text)
		case "content_json":
			value, err = input.JSON(key)
		case "content_binary":
			var text string
			text, err = input.String(key, false, 2<<20)
			if err == nil {
				var decoded []byte
				decoded, err = base64.StdEncoding.DecodeString(text)
				if err != nil {
					err = data.Invalid("content_binary must contain base64")
				}
				if len(decoded) > 1<<20 {
					err = data.Invalid("The collaborative document is too large")
				}
				value = decoded
			}
		case "parent_id":
			value, err = input.UUID(key, true)
		case "is_private", "is_locked":
			value, err = input.Bool(key)
		case "position":
			value, err = input.Number(key)
		case "archived":
			var archived bool
			archived, err = input.Bool(key)
			key = "archived_at"
			if archived {
				value = time.Now().UTC()
			}
		case "version":
			continue
		default:
			err = data.Invalid("Unknown page field: " + key)
		}
		if err != nil {
			return nil, err
		}
		values[key] = value
	}
	if hasHTML && !hasBinary {
		// A REST replacement must be hydrated from the new JSON. Reusing the
		// previous Yjs snapshot would silently restore obsolete document content.
		values["content_binary"] = nil
	}
	return values, nil
}

func expectedVersion(input data.Object, current int64, required bool) error {
	if _, ok := input["version"]; !ok {
		if required {
			return data.Invalid("version is required for document edits")
		}
		return nil
	}
	var version int64
	if err := json.Unmarshal(input["version"], &version); err != nil || version < 1 {
		return data.Invalid("version must be a positive integer")
	}
	if version != current {
		return data.Conflict("The document changed; reload the current version before saving")
	}
	return nil
}

func lockHierarchy(c *gin.Context, q database.DBTX, scope identity.Scope) error {
	_, err := q.ExecContext(c.Request.Context(), "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "pages:"+scope.WorkspaceID.String()+":"+scope.ProjectID.String())
	return err
}

func (s *service) parent(c *gin.Context, q database.DBTX, scope identity.Scope, id uuid.UUID, value any) error {
	if value == nil {
		return nil
	}
	parentID := value.(uuid.UUID)
	if parentID == id {
		return data.Invalid("A page cannot be its own parent")
	}
	parent, err := s.load(c, q, scope, parentID, true)
	if err != nil {
		return err
	}
	if parent.ArchivedAt != nil {
		return data.Invalid("An archived page cannot be a parent")
	}
	var cycle bool
	err = q.QueryRowContext(c.Request.Context(), "WITH RECURSIVE ancestors AS (SELECT id,parent_id FROM pages WHERE id=$1 UNION SELECT p.id,p.parent_id FROM pages p JOIN ancestors a ON p.id=a.parent_id WHERE p.deleted_at IS NULL) SELECT EXISTS(SELECT 1 FROM ancestors WHERE id=$2)", parentID, id).Scan(&cycle)
	if err != nil {
		return err
	}
	if cycle {
		return data.Invalid("The parent would create a page hierarchy cycle")
	}
	return nil
}

func snapshot(c *gin.Context, q database.DBTX, scope identity.Scope, id uuid.UUID) error {
	_, err := q.ExecContext(c.Request.Context(), "INSERT INTO page_versions(id,workspace_id,page_id,saved_by,name,content_html,content_json,content_binary,version) SELECT $1,workspace_id,id,$2,name,content_html,content_json,content_binary,version FROM pages WHERE id=$3 AND deleted_at IS NULL", uuid.New(), scope.Actor.UserID, id)
	return err
}

func activity(c *gin.Context, q database.DBTX, scope identity.Scope, id uuid.UUID, action string) error {
	meta, _ := json.Marshal(map[string]any{"page_id": id})
	_, err := q.ExecContext(c.Request.Context(), "INSERT INTO activities(id,workspace_id,project_id,actor_id,action,field_name,metadata) VALUES($1,$2,$3,$4,$5,'',$6::jsonb)", uuid.New(), scope.WorkspaceID, project(scope), scope.Actor.UserID, "page."+action, string(meta))
	return err
}

func (s *service) insert(c *gin.Context, q database.DBTX, scope identity.Scope, id uuid.UUID, values map[string]any) (json.RawMessage, error) {
	if parentID, ok := values["parent_id"]; ok {
		if err := lockHierarchy(c, q, scope); err != nil {
			return nil, err
		}
		if err := s.parent(c, q, scope, id, parentID); err != nil {
			return nil, err
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	columns := []string{"id", "workspace_id", "project_id", "owner_id", "version"}
	args := []any{id, scope.WorkspaceID, project(scope), scope.Actor.UserID, 1}
	for _, key := range keys {
		columns = append(columns, key)
		args = append(args, values[key])
	}
	if _, err := q.ExecContext(c.Request.Context(), "INSERT INTO pages("+strings.Join(columns, ",")+") VALUES("+data.Markers(1, len(args))+")", args...); err != nil {
		return nil, err
	}
	if err := snapshot(c, q, scope, id); err != nil {
		return nil, err
	}
	if err := activity(c, q, scope, id, "created"); err != nil {
		return nil, err
	}
	p, err := s.load(c, q, scope, id, false)
	return p.Raw, err
}

func (s *service) create(c *gin.Context) {
	scope, err := s.scope(c, 15)
	if err != nil {
		data.Fail(c, err)
		return
	}
	if err := liveauth.VerifyBinary(c); err != nil {
		data.Fail(c, err)
		return
	}
	input, err := data.Bind(c, pageFields...)
	if err != nil {
		data.Fail(c, err)
		return
	}
	values, err := parse(input, true)
	if err != nil {
		data.Fail(c, err)
		return
	}
	var result json.RawMessage
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		var err error
		scope, err = data.PageProjectScope(c.Request.Context(), q, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Member)
		if err != nil {
			return err
		}
		result, err = s.insert(c, q, scope, uuid.New(), values)
		return err
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 201, result)
}

func authorizeEdit(scope identity.Scope, p page, input data.Object) error {
	if scope.Role < 15 && p.OwnerID != scope.Actor.UserID {
		return data.Forbidden()
	}
	contentEdit := false
	for _, key := range []string{"name", "content_html", "content_json", "content_binary"} {
		if _, ok := input[key]; ok {
			contentEdit = true
		}
	}
	if contentEdit && (p.IsLocked || p.ArchivedAt != nil) {
		return data.Conflict("Unlock and restore the page before editing its content")
	}
	if _, ok := input["is_private"]; ok {
		privacy, err := input.Bool("is_private")
		if err != nil {
			return err
		}
		if privacy != p.IsPrivate && p.OwnerID != scope.Actor.UserID {
			return data.Forbidden()
		}
	}
	for _, key := range []string{"is_locked", "archived", "parent_id"} {
		if _, ok := input[key]; ok && p.OwnerID != scope.Actor.UserID && scope.Role < 20 {
			return data.Forbidden()
		}
	}
	return expectedVersion(input, p.Version, contentEdit)
}

func (s *service) update(c *gin.Context) {
	scope, id, err := s.pageScope(c, 5)
	if err != nil {
		data.Fail(c, err)
		return
	}
	if err := liveauth.VerifyBinary(c); err != nil {
		data.Fail(c, err)
		return
	}
	fields := pageFields
	if c.Request.Method == "PUT" {
		fields = []string{"content_html", "content_json", "content_binary", "version"}
	}
	input, err := data.Bind(c, fields...)
	if err != nil {
		data.Fail(c, err)
		return
	}
	values, err := parse(input, false)
	if err != nil {
		data.Fail(c, err)
		return
	}
	if len(values) == 0 {
		data.Fail(c, data.Invalid("At least one page field is required"))
		return
	}
	var result json.RawMessage
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		var err error
		scope, err = data.PageProjectScope(c.Request.Context(), q, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Guest)
		if err != nil {
			return err
		}
		if _, ok := input["parent_id"]; ok {
			if err := lockHierarchy(c, q, scope); err != nil {
				return err
			}
		}
		p, err := s.load(c, q, scope, id, true)
		if err != nil {
			return err
		}
		if err := authorizeEdit(scope, p, input); err != nil {
			return err
		}
		if parentID, ok := values["parent_id"]; ok {
			if err := s.parent(c, q, scope, id, parentID); err != nil {
				return err
			}
		}
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		args := []any{id, scope.WorkspaceID}
		sets := []string{"updated_at=now()", "version=version+1"}
		for _, key := range keys {
			args = append(args, values[key])
			sets = append(sets, fmt.Sprintf("%s=$%d", key, len(args)))
		}
		if _, err := q.ExecContext(c.Request.Context(), "UPDATE pages SET "+strings.Join(sets, ",")+" WHERE id=$1 AND workspace_id=$2", args...); err != nil {
			return err
		}
		if err := snapshot(c, q, scope, id); err != nil {
			return err
		}
		if err := activity(c, q, scope, id, "updated"); err != nil {
			return err
		}
		result, err = data.One(c, q, "SELECT "+pageProjection+" FROM pages p WHERE p.id=$1", id)
		return err
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, result)
}

func (s *service) remove(c *gin.Context) {
	scope, id, err := s.pageScope(c, 5)
	if err != nil {
		data.Fail(c, err)
		return
	}
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		var err error
		scope, err = data.PageProjectScope(c.Request.Context(), q, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Guest)
		if err != nil {
			return err
		}
		if err := lockHierarchy(c, q, scope); err != nil {
			return err
		}
		p, err := s.load(c, q, scope, id, true)
		if err != nil {
			return err
		}
		if p.OwnerID != scope.Actor.UserID && scope.Role < 20 {
			return data.Forbidden()
		}
		if p.ArchivedAt == nil {
			return data.Conflict("Archive the page before deleting it")
		}
		var children bool
		if err := q.QueryRowContext(c.Request.Context(), "SELECT EXISTS(SELECT 1 FROM pages WHERE parent_id=$1 AND deleted_at IS NULL)", id).Scan(&children); err != nil {
			return err
		}
		if children {
			return data.Conflict("Move or delete child pages first")
		}
		if _, err := q.ExecContext(c.Request.Context(), "UPDATE pages SET deleted_at=now(),updated_at=now(),version=version+1 WHERE id=$1", id); err != nil {
			return err
		}
		return activity(c, q, scope, id, "deleted")
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	c.Status(204)
}

func (s *service) duplicate(c *gin.Context) {
	scope, id, err := s.pageScope(c, 15)
	if err != nil {
		data.Fail(c, err)
		return
	}
	var result json.RawMessage
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		var err error
		scope, err = data.PageProjectScope(c.Request.Context(), q, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Member)
		if err != nil {
			return err
		}
		p, err := s.load(c, q, scope, id, false)
		if err != nil {
			return err
		}
		var original data.Object
		if err := json.Unmarshal(p.Raw, &original); err != nil {
			return err
		}
		input := data.Object{}
		for _, key := range []string{"name", "content_html", "content_json", "content_binary", "icon", "is_private"} {
			input[key] = original[key]
		}
		name, _ := original.String("name", true, 255)
		runes := []rune(name)
		if len(runes) > 240 {
			runes = runes[:240]
		}
		input["name"], _ = json.Marshal(string(runes) + " (copy)")
		values, err := parse(input, true)
		if err != nil {
			return err
		}
		result, err = s.insert(c, q, scope, uuid.New(), values)
		return err
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 201, result)
}
