package documents

import (
	"database/sql"
	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
)

func (s *service) versions(c *gin.Context) {
	scope, id, err := s.pageScope(c, 5)
	if err != nil {
		data.Fail(c, err)
		return
	}
	if _, err := s.load(c, s.deps.DB.SQL, scope, id, false); err != nil {
		data.Fail(c, err)
		return
	}
	result, err := data.Many(c, s.deps.DB.SQL, "SELECT to_jsonb(v)-'deleted_at'-'content_binary'-'content_json'-'content_html' FROM page_versions v WHERE v.page_id=$1 AND v.workspace_id=$2 AND v.deleted_at IS NULL AND EXISTS(SELECT 1 FROM pages p WHERE p.id=v.page_id AND p.project_id IS NOT DISTINCT FROM $3::uuid AND "+data.VisiblePage("p", "$2", "$4")+") ORDER BY v.version DESC", id, scope.WorkspaceID, project(scope), scope.Actor.UserID)
	data.Send(c, result, err)
}

func (s *service) version(c *gin.Context) {
	scope, id, err := s.pageScope(c, 5)
	if err != nil {
		data.Fail(c, err)
		return
	}
	vid, err := httpapi.UUIDParam(c, "versionID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	if _, err := s.load(c, s.deps.DB.SQL, scope, id, false); err != nil {
		data.Fail(c, err)
		return
	}
	result, err := data.One(c, s.deps.DB.SQL, "SELECT (to_jsonb(v)-'deleted_at'-'content_binary') || jsonb_build_object('content_binary',COALESCE(encode(v.content_binary,'base64'),'')) FROM page_versions v WHERE id=$1 AND page_id=$2 AND workspace_id=$3 AND deleted_at IS NULL AND EXISTS(SELECT 1 FROM pages p WHERE p.id=v.page_id AND p.project_id IS NOT DISTINCT FROM $4::uuid AND "+data.VisiblePage("p", "$3", "$5")+")", vid, id, scope.WorkspaceID, project(scope), scope.Actor.UserID)
	data.Send(c, result, err)
}

func (s *service) restore(c *gin.Context) {
	scope, id, err := s.pageScope(c, 15)
	if err != nil {
		data.Fail(c, err)
		return
	}
	vid, err := httpapi.UUIDParam(c, "versionID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	input, err := data.Bind(c, "version")
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
		p, err := s.load(c, q, scope, id, true)
		if err != nil {
			return err
		}
		if p.IsLocked || p.ArchivedAt != nil {
			return data.Conflict("Unlock and restore the page before restoring a content version")
		}
		if err := expectedVersion(input, p.Version, true); err != nil {
			return err
		}
		changed, err := q.ExecContext(c.Request.Context(), "UPDATE pages p SET name=v.name,content_html=v.content_html,content_json=v.content_json,content_binary=v.content_binary,version=p.version+1,updated_at=now() FROM page_versions v WHERE p.id=$1 AND v.id=$2 AND v.page_id=p.id AND v.workspace_id=$3 AND v.deleted_at IS NULL", id, vid, scope.WorkspaceID)
		if err != nil {
			return err
		}
		count, _ := changed.RowsAffected()
		if count != 1 {
			return data.Missing()
		}
		if err := snapshot(c, q, scope, id); err != nil {
			return err
		}
		if err := activity(c, q, scope, id, "version_restored"); err != nil {
			return err
		}
		p, err = s.load(c, q, scope, id, false)
		result = p.Raw
		return err
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, result)
}

func (s *service) comments(c *gin.Context) {
	scope, id, err := s.pageScope(c, 5)
	if err != nil {
		data.Fail(c, err)
		return
	}
	if _, err := s.load(c, s.deps.DB.SQL, scope, id, false); err != nil {
		data.Fail(c, err)
		return
	}
	result, err := data.Many(c, s.deps.DB.SQL, "SELECT (to_jsonb(cm)-'deleted_at') || jsonb_build_object('author',jsonb_build_object('id',u.id,'display_name',u.display_name,'avatar_url',u.avatar_url)) FROM page_comments cm JOIN users u ON u.id=cm.author_id WHERE cm.page_id=$1 AND cm.workspace_id=$2 AND cm.deleted_at IS NULL AND EXISTS(SELECT 1 FROM pages p WHERE p.id=cm.page_id AND p.project_id IS NOT DISTINCT FROM $3::uuid AND "+data.VisiblePage("p", "$2", "$4")+") ORDER BY cm.created_at,cm.id", id, scope.WorkspaceID, project(scope), scope.Actor.UserID)
	data.Send(c, result, err)
}

func commentInput(c *gin.Context) (data.Object, string, any, error) {
	input, err := data.Bind(c, "body_html", "body_json", "parent_id")
	if err != nil {
		return nil, "", nil, err
	}
	text, err := input.String("body_html", true, 100000)
	if err != nil {
		return nil, "", nil, err
	}
	text = htmlPolicy.Sanitize(text)
	if text == "" {
		return nil, "", nil, data.Invalid("The comment cannot be empty")
	}
	body, err := input.JSON("body_json")
	return input, text, body, err
}

func (s *service) addComment(c *gin.Context) {
	scope, id, err := s.pageScope(c, 5)
	if err != nil {
		data.Fail(c, err)
		return
	}
	input, text, body, err := commentInput(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	parent, err := input.UUID("parent_id", true)
	if err != nil {
		data.Fail(c, err)
		return
	}
	var result json.RawMessage
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		var err error
		scope, err = data.PageProjectScope(c.Request.Context(), q, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Guest)
		if err != nil {
			return err
		}
		p, err := s.load(c, q, scope, id, true)
		if err != nil {
			return err
		}
		if p.ArchivedAt != nil {
			return data.Conflict("Archived pages cannot receive comments")
		}
		if parent != nil {
			var found uuid.UUID
			if err := q.QueryRowContext(c.Request.Context(), "SELECT id FROM page_comments WHERE id=$1 AND page_id=$2 AND workspace_id=$3 AND deleted_at IS NULL", parent, id, scope.WorkspaceID).Scan(&found); err != nil {
				return err
			}
		}
		commentID := uuid.New()
		if _, err := q.ExecContext(c.Request.Context(), "INSERT INTO page_comments(id,workspace_id,project_id,page_id,author_id,parent_id,body_html,body_json) VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb)", commentID, scope.WorkspaceID, project(scope), id, scope.Actor.UserID, parent, text, body); err != nil {
			return err
		}
		if err := activity(c, q, scope, id, "comment_created"); err != nil {
			return err
		}
		result, err = data.One(c, q, "SELECT to_jsonb(cm)-'deleted_at' FROM page_comments cm WHERE id=$1", commentID)
		return err
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 201, result)
}

func (s *service) editComment(c *gin.Context)   { s.mutateComment(c, false) }
func (s *service) deleteComment(c *gin.Context) { s.mutateComment(c, true) }
func (s *service) mutateComment(c *gin.Context, remove bool) {
	scope, id, err := s.pageScope(c, 5)
	if err != nil {
		data.Fail(c, err)
		return
	}
	cid, err := httpapi.UUIDParam(c, "commentID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	var text string
	var body any
	if !remove {
		_, text, body, err = commentInput(c)
		if err != nil {
			data.Fail(c, err)
			return
		}
	}
	var result json.RawMessage
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		var err error
		scope, err = data.PageProjectScope(c.Request.Context(), q, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Guest)
		if err != nil {
			return err
		}
		if _, err := s.load(c, q, scope, id, true); err != nil {
			return err
		}
		var author uuid.UUID
		err = q.QueryRowContext(c.Request.Context(), "SELECT author_id FROM page_comments WHERE id=$1 AND page_id=$2 AND workspace_id=$3 AND deleted_at IS NULL FOR UPDATE", cid, id, scope.WorkspaceID).Scan(&author)
		if err != nil {
			return err
		}
		if author != scope.Actor.UserID && (!remove || scope.Role < 20) {
			return data.Forbidden()
		}
		var updated sql.Result
		if remove {
			updated, err = q.ExecContext(c.Request.Context(), "UPDATE page_comments SET deleted_at=now(),updated_at=now() WHERE id=$1", cid)
		} else {
			updated, err = q.ExecContext(c.Request.Context(), "UPDATE page_comments SET body_html=$2,body_json=$3::jsonb,edited_at=now(),updated_at=now() WHERE id=$1", cid, text, body)
		}
		if err != nil {
			return err
		}
		count, _ := updated.RowsAffected()
		if count != 1 {
			return data.Missing()
		}
		if err := activity(c, q, scope, id, "comment_updated"); err != nil {
			return err
		}
		if remove {
			return nil
		}
		result, err = data.One(c, q, "SELECT to_jsonb(cm)-'deleted_at' FROM page_comments cm WHERE id=$1", cid)
		return err
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	if remove {
		c.Status(204)
		return
	}
	httpapi.JSON(c, 200, result)
}
