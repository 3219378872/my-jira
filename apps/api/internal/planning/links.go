package planning

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
)

func (s *service) moduleLinks(c *gin.Context) {
	scope, err := s.scope(c, identity.Guest)
	if err != nil {
		data.Fail(c, err)
		return
	}
	id, err := httpapi.UUIDParam(c, "moduleID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	if _, err = s.read(c, s.deps.DB.SQL, modules, scope, id); err != nil {
		data.Fail(c, err)
		return
	}
	result, err := data.Many(c, s.deps.DB.SQL, "SELECT to_jsonb(l)-'deleted_at' FROM module_links l WHERE module_id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL ORDER BY created_at DESC,id", id, scope.WorkspaceID, scope.ProjectID)
	data.Send(c, result, err)
}

func (s *service) mutateModuleLink(c *gin.Context) {
	scope, err := s.scope(c, identity.Member)
	if err != nil {
		data.Fail(c, err)
		return
	}
	id, err := httpapi.UUIDParam(c, "moduleID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	linkID := uuid.New()
	if c.Param("linkID") != "" {
		linkID, err = httpapi.UUIDParam(c, "linkID")
		if err != nil {
			data.Fail(c, err)
			return
		}
	}
	input := data.Object{}
	if c.Request.Method != "DELETE" {
		input, err = data.Bind(c, "title", "url")
		if err != nil {
			data.Fail(c, err)
			return
		}
	}
	var result json.RawMessage
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := s.lockResource(c, q, modules, scope, id); err != nil {
			return err
		}
		title, link := "", ""
		if c.Request.Method != "POST" {
			if err := q.QueryRowContext(c.Request.Context(), "SELECT title,url FROM module_links WHERE id=$1 AND module_id=$2 AND workspace_id=$3 AND project_id=$4 AND deleted_at IS NULL FOR UPDATE", linkID, id, scope.WorkspaceID, scope.ProjectID).Scan(&title, &link); err != nil {
				return err
			}
		}
		if c.Request.Method == "DELETE" {
			if _, err := q.ExecContext(c.Request.Context(), "UPDATE module_links SET deleted_at=now(),updated_at=now() WHERE id=$1", linkID); err != nil {
				return err
			}
			return record(c, q, scope, modules, id, "link_deleted")
		}
		if _, ok := input["url"]; ok {
			value, err := input.String("url", true, 4096)
			if err != nil {
				return err
			}
			link = value
		}
		parsed, err := url.Parse(link)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.User != nil {
			return data.Invalid("Module links need an HTTP or HTTPS URL")
		}
		if _, ok := input["title"]; ok {
			value, err := input.String("title", false, 255)
			if err != nil {
				return err
			}
			title = value
		}
		if strings.TrimSpace(title) == "" {
			title = parsed.Hostname()
		}
		if c.Request.Method == "POST" {
			result, err = data.One(c, q, "INSERT INTO module_links(id,workspace_id,project_id,module_id,created_by,title,url) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING to_jsonb(module_links)-'deleted_at'", linkID, scope.WorkspaceID, scope.ProjectID, id, scope.Actor.UserID, title, link)
		} else {
			result, err = data.One(c, q, "UPDATE module_links SET title=$2,url=$3,updated_at=now() WHERE id=$1 RETURNING to_jsonb(module_links)-'deleted_at'", linkID, title, link)
		}
		if err != nil {
			return err
		}
		return record(c, q, scope, modules, id, "link_updated")
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	if c.Request.Method == "DELETE" {
		c.Status(204)
	} else {
		status := 200
		if c.Request.Method == "POST" {
			status = 201
		}
		httpapi.JSON(c, status, result)
	}
}
