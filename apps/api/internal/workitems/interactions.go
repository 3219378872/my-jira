package workitems

import (
	"encoding/json"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
)

func (h *handler) comments(c *gin.Context) {
	s, id, _, e := h.issueScope(c, identity.Guest)
	if e != nil {
		respondError(c, e)
		return
	}
	data, e := execJSON(c, h.deps.DB.SQL, `SELECT (to_jsonb(cm)-'deleted_at')||jsonb_build_object('author',jsonb_build_object('id',u.id,'display_name',u.display_name,'avatar_url',u.avatar_url),'reactions',COALESCE((SELECT jsonb_agg(to_jsonb(r)-'deleted_at') FROM reactions r WHERE r.comment_id=cm.id AND r.deleted_at IS NULL),'[]'::jsonb)) FROM comments cm JOIN users u ON u.id=cm.author_id WHERE cm.work_item_id=$1 AND cm.workspace_id=$2 AND cm.project_id=$3 AND cm.deleted_at IS NULL ORDER BY cm.created_at,cm.id`, id, s.WorkspaceID, s.ProjectID)
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 200, data)
}

type commentInput struct {
	BodyHTML string         `json:"body_html"`
	BodyJSON map[string]any `json:"body_json"`
	ParentID *uuid.UUID     `json:"parent_id"`
}

func (h *handler) addComment(c *gin.Context) {
	s, id, _, e := h.issueScope(c, identity.Guest)
	if e != nil {
		respondError(c, e)
		return
	}
	var body commentInput
	if e = bindInput(c, &body); e != nil || strings.TrimSpace(body.BodyHTML) == "" || len(body.BodyHTML) > 262144 {
		respondError(c, invalid("Comment must contain text and be below 256 KiB"))
		return
	}
	body.BodyHTML = cleanHTML(body.BodyHTML)
	if strings.TrimSpace(body.BodyHTML) == "" {
		respondError(c, invalid("Comment must contain safe content"))
		return
	}
	if body.BodyJSON == nil {
		body.BodyJSON = map[string]any{}
	}
	raw, _ := json.Marshal(body.BodyJSON)
	commentID := uuid.New()
	var result json.RawMessage
	e = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, e := loadIssue(c, q, s, id, true); e != nil {
			return e
		}
		if body.ParentID != nil {
			var valid bool
			if e := q.QueryRowContext(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM comments WHERE id=$1 AND work_item_id=$2 AND deleted_at IS NULL)`, body.ParentID, id).Scan(&valid); e != nil {
				return e
			}
			if !valid {
				return invalid("Comment parent is not in this discussion")
			}
		}
		var b []byte
		if e := q.QueryRowContext(c.Request.Context(), `INSERT INTO comments(id,workspace_id,project_id,work_item_id,parent_id,author_id,body_html,body_json) VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb) RETURNING to_jsonb(comments)-'deleted_at'`, commentID, s.WorkspaceID, s.ProjectID, id, body.ParentID, s.Actor.UserID, body.BodyHTML, string(raw)).Scan(&b); e != nil {
			return e
		}
		result = b
		return h.record(c, q, s, id, "commented", nil, map[string]any{"comment_id": commentID, "body_html": body.BodyHTML, "body_json": body.BodyJSON})
	})
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 201, result)
}

func (h *handler) updateComment(c *gin.Context) {
	s, id, _, e := h.issueScope(c, identity.Guest)
	if e != nil {
		respondError(c, e)
		return
	}
	cid, e := uuid.Parse(c.Param("commentID"))
	if e != nil {
		respondError(c, invalid("Invalid comment identifier"))
		return
	}
	var body commentInput
	if e = bindInput(c, &body); e != nil || strings.TrimSpace(body.BodyHTML) == "" || len(body.BodyHTML) > 262144 {
		respondError(c, invalid("Invalid comment"))
		return
	}
	body.BodyHTML = cleanHTML(body.BodyHTML)
	if strings.TrimSpace(body.BodyHTML) == "" {
		respondError(c, invalid("Comment must contain safe content"))
		return
	}
	if body.BodyJSON == nil {
		body.BodyJSON = map[string]any{}
	}
	raw, _ := json.Marshal(body.BodyJSON)
	var result json.RawMessage
	e = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, e := loadIssue(c, q, s, id, true); e != nil {
			return e
		}
		var owner uuid.UUID
		var old string
		var oldJSON json.RawMessage
		if e := q.QueryRowContext(c.Request.Context(), `SELECT author_id,body_html,body_json FROM comments WHERE id=$1 AND work_item_id=$2 AND deleted_at IS NULL FOR UPDATE`, cid, id).Scan(&owner, &old, &oldJSON); e != nil {
			return e
		}
		if owner != s.Actor.UserID {
			return problem{403, "forbidden", "Only the author can edit this comment"}
		}
		var b []byte
		if e := q.QueryRowContext(c.Request.Context(), `UPDATE comments SET body_html=$3,body_json=$4::jsonb,edited_at=now(),updated_at=now() WHERE id=$1 AND work_item_id=$2 RETURNING to_jsonb(comments)-'deleted_at'`, cid, id, body.BodyHTML, string(raw)).Scan(&b); e != nil {
			return e
		}
		result = b
		return h.record(c, q, s, id, "comment_edited", map[string]any{"body_html": old, "body_json": oldJSON}, map[string]any{"comment_id": cid, "body_html": body.BodyHTML, "body_json": body.BodyJSON})
	})
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 200, result)
}

func (h *handler) deleteComment(c *gin.Context) {
	s, id, _, e := h.issueScope(c, identity.Guest)
	if e != nil {
		respondError(c, e)
		return
	}
	cid, e := uuid.Parse(c.Param("commentID"))
	if e != nil {
		respondError(c, invalid("Invalid comment identifier"))
		return
	}
	e = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, e := loadIssue(c, q, s, id, true); e != nil {
			return e
		}
		var owner uuid.UUID
		if e := q.QueryRowContext(c.Request.Context(), `SELECT author_id FROM comments WHERE id=$1 AND work_item_id=$2 AND deleted_at IS NULL FOR UPDATE`, cid, id).Scan(&owner); e != nil {
			return e
		}
		if owner != s.Actor.UserID && s.Role < identity.Admin {
			return problem{403, "forbidden", "Only the author or a project administrator can delete this comment"}
		}
		if _, e := q.ExecContext(c.Request.Context(), `UPDATE comments SET deleted_at=now(),updated_at=now() WHERE id=$1`, cid); e != nil {
			return e
		}
		return h.record(c, q, s, id, "comment_deleted", map[string]any{"comment_id": cid}, nil)
	})
	if e != nil {
		respondError(c, e)
		return
	}
	c.Status(204)
}

func (h *handler) links(c *gin.Context) {
	s, id, _, e := h.issueScope(c, identity.Guest)
	if e != nil {
		respondError(c, e)
		return
	}
	data, e := execJSON(c, h.deps.DB.SQL, `SELECT to_jsonb(l)-'deleted_at' FROM work_item_links l WHERE work_item_id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL ORDER BY created_at`, id, s.WorkspaceID, s.ProjectID)
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 200, data)
}
func (h *handler) addLink(c *gin.Context) {
	s, id, _, e := h.issueScope(c, identity.Guest)
	if e != nil {
		respondError(c, e)
		return
	}
	var body struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	}
	if e = bindInput(c, &body); e != nil {
		respondError(c, invalid("Invalid link"))
		return
	}
	parsed, e := url.Parse(body.URL)
	if e != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.User != nil || len(body.URL) > 4096 {
		respondError(c, invalid("Links require an HTTP or HTTPS URL"))
		return
	}
	if body.Title == "" {
		body.Title = parsed.Hostname()
	}
	if len([]rune(body.Title)) > 255 {
		respondError(c, invalid("Link title is too long"))
		return
	}
	var result []byte
	e = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, e := loadIssue(c, q, s, id, true); e != nil {
			return e
		}
		if e := q.QueryRowContext(c.Request.Context(), `INSERT INTO work_item_links(id,workspace_id,project_id,work_item_id,title,url,created_by) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING to_jsonb(work_item_links)-'deleted_at'`, uuid.New(), s.WorkspaceID, s.ProjectID, id, body.Title, body.URL, s.Actor.UserID).Scan(&result); e != nil {
			return e
		}
		return h.record(c, q, s, id, "link_added", nil, body)
	})
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 201, json.RawMessage(result))
}
func (h *handler) deleteLink(c *gin.Context) {
	s, id, _, e := h.issueScope(c, identity.Guest)
	if e != nil {
		respondError(c, e)
		return
	}
	lid, e := uuid.Parse(c.Param("linkID"))
	if e != nil {
		respondError(c, invalid("Invalid link identifier"))
		return
	}
	e = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, e := loadIssue(c, q, s, id, true); e != nil {
			return e
		}
		res, e := q.ExecContext(c.Request.Context(), `UPDATE work_item_links SET deleted_at=now() WHERE id=$1 AND work_item_id=$2 AND deleted_at IS NULL`, lid, id)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return absent()
		}
		return h.record(c, q, s, id, "link_deleted", map[string]any{"link_id": lid}, nil)
	})
	if e != nil {
		respondError(c, e)
		return
	}
	c.Status(204)
}

func (h *handler) relations(c *gin.Context) {
	s, id, _, e := h.issueScope(c, identity.Guest)
	if e != nil {
		respondError(c, e)
		return
	}
	query := `SELECT (to_jsonb(r)-'deleted_at')||jsonb_build_object('target',jsonb_build_object('id',w.id,'name',w.name,'sequence_id',w.sequence_id,'state_id',w.state_id,'priority',w.priority)) FROM work_item_relations r JOIN work_items w ON w.id=CASE WHEN r.source_id=$1 THEN r.target_id ELSE r.source_id END WHERE (r.source_id=$1 OR r.target_id=$1) AND r.workspace_id=$2 AND r.project_id=$3 AND w.workspace_id=$2 AND w.project_id=$3 AND r.deleted_at IS NULL AND w.deleted_at IS NULL`
	args := []any{id, s.WorkspaceID, s.ProjectID}
	if s.Role < identity.Member && !s.GuestCanViewAll {
		query += " AND w.created_by=$4"
		args = append(args, s.Actor.UserID)
	}
	data, e := execJSON(c, h.deps.DB.SQL, query+" ORDER BY r.created_at", args...)
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 200, data)
}
func (h *handler) addRelation(c *gin.Context) {
	s, id, _, e := h.issueScope(c, identity.Member)
	if e != nil {
		respondError(c, e)
		return
	}
	var body struct {
		TargetID uuid.UUID `json:"target_id"`
		Type     string    `json:"relation_type"`
	}
	if e = bindInput(c, &body); e != nil || body.TargetID == uuid.Nil || body.TargetID == id {
		respondError(c, invalid("Choose a different work item"))
		return
	}
	if body.Type != "related" && body.Type != "blocks" && body.Type != "blocked_by" && body.Type != "duplicate" {
		respondError(c, invalid("Invalid relation type"))
		return
	}
	source, target := id, body.TargetID
	if body.Type == "blocked_by" {
		source, target = target, source
		body.Type = "blocks"
	}
	if (body.Type == "related" || body.Type == "duplicate") && source.String() > target.String() {
		source, target = target, source
	}
	var result []byte
	e = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if e := lockGraphs(c, q, s.ProjectID); e != nil {
			return e
		}
		if _, e := loadIssue(c, q, s, id, true); e != nil {
			return e
		}
		if _, e := loadIssue(c, q, s, body.TargetID, true); e != nil {
			return invalid("Related work item is not accessible in this project")
		}
		if body.Type == "blocks" {
			var cycle bool
			if e := q.QueryRowContext(c.Request.Context(), `WITH RECURSIVE reachable(id,path) AS (SELECT $1::uuid,ARRAY[$1::uuid] UNION ALL SELECT r.target_id,x.path||r.target_id FROM work_item_relations r JOIN reachable x ON r.source_id=x.id WHERE r.relation_type='blocks' AND r.deleted_at IS NULL AND NOT r.target_id=ANY(x.path)) SELECT EXISTS(SELECT 1 FROM reachable WHERE id=$2)`, target, source).Scan(&cycle); e != nil {
				return e
			}
			if cycle {
				return invalid("This dependency would form a cycle")
			}
		}
		var exists bool
		if e := q.QueryRowContext(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM work_item_relations WHERE source_id=$1 AND target_id=$2 AND relation_type=$3 AND deleted_at IS NULL)`, source, target, body.Type).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return conflict("This relation already exists")
		}
		if e := q.QueryRowContext(c.Request.Context(), `INSERT INTO work_item_relations(id,workspace_id,project_id,source_id,target_id,relation_type) VALUES($1,$2,$3,$4,$5,$6) RETURNING to_jsonb(work_item_relations)-'deleted_at'`, uuid.New(), s.WorkspaceID, s.ProjectID, source, target, body.Type).Scan(&result); e != nil {
			return e
		}
		return h.record(c, q, s, id, "relation_added", nil, body)
	})
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 201, json.RawMessage(result))
}
func (h *handler) deleteRelation(c *gin.Context) {
	s, id, _, e := h.issueScope(c, identity.Member)
	if e != nil {
		respondError(c, e)
		return
	}
	rid, e := uuid.Parse(c.Param("relationID"))
	if e != nil {
		respondError(c, invalid("Invalid relation identifier"))
		return
	}
	e = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if e := lockGraphs(c, q, s.ProjectID); e != nil {
			return e
		}
		if _, e := loadIssue(c, q, s, id, true); e != nil {
			return e
		}
		res, e := q.ExecContext(c.Request.Context(), `UPDATE work_item_relations SET deleted_at=now() WHERE id=$1 AND (source_id=$2 OR target_id=$2) AND deleted_at IS NULL`, rid, id)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return absent()
		}
		return h.record(c, q, s, id, "relation_deleted", map[string]any{"relation_id": rid}, nil)
	})
	if e != nil {
		respondError(c, e)
		return
	}
	c.Status(204)
}

func (h *handler) subscribers(c *gin.Context) {
	scope, id, _, e := h.issueScope(c, identity.Guest)
	if e != nil {
		respondError(c, e)
		return
	}
	data, e := execJSON(c, h.deps.DB.SQL, `SELECT jsonb_build_object('id',s.id,'user_id',u.id,'display_name',u.display_name,'avatar_url',u.avatar_url) FROM work_item_subscribers s JOIN users u ON u.id=s.user_id AND u.is_active AND u.deleted_at IS NULL JOIN workspace_members wm ON wm.workspace_id=s.workspace_id AND wm.user_id=s.user_id AND wm.is_active AND wm.deleted_at IS NULL JOIN project_members pm ON pm.project_id=s.project_id AND pm.user_id=s.user_id AND pm.is_active AND pm.deleted_at IS NULL JOIN work_items w ON w.id=s.work_item_id JOIN projects p ON p.id=w.project_id AND p.deleted_at IS NULL WHERE s.work_item_id=$1 AND s.workspace_id=$2 AND s.project_id=$3 AND s.deleted_at IS NULL AND (CASE WHEN wm.role IN(5,20) THEN wm.role ELSE pm.role END>=15 OR p.guest_can_view_all OR w.created_by=u.id) ORDER BY s.created_at,s.id`, id, scope.WorkspaceID, scope.ProjectID)
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 200, data)
}
func (h *handler) subscribe(c *gin.Context) {
	h.manageSubscriber(c, false)
}
func (h *handler) unsubscribe(c *gin.Context) {
	h.manageSubscriber(c, true)
}

func (h *handler) reactions(c *gin.Context) {
	s, id, _, e := h.issueScope(c, identity.Guest)
	if e != nil {
		respondError(c, e)
		return
	}
	data, e := execJSON(c, h.deps.DB.SQL, `SELECT to_jsonb(r)-'deleted_at' FROM reactions r WHERE work_item_id=$1 AND workspace_id=$2 AND project_id=$3 AND comment_id IS NULL AND deleted_at IS NULL`, id, s.WorkspaceID, s.ProjectID)
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 200, data)
}
func (h *handler) addReaction(c *gin.Context) {
	s, id, _, e := h.issueScope(c, identity.Guest)
	if e != nil {
		respondError(c, e)
		return
	}
	var body struct {
		Emoji     string     `json:"emoji"`
		CommentID *uuid.UUID `json:"comment_id"`
	}
	if e = bindInput(c, &body); e != nil || utf8.RuneCountInString(body.Emoji) < 1 || utf8.RuneCountInString(body.Emoji) > 32 {
		respondError(c, invalid("Invalid reaction"))
		return
	}
	var result []byte
	e = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, e := loadIssue(c, q, s, id, true); e != nil {
			return e
		}
		if body.CommentID != nil {
			var valid bool
			if e := q.QueryRowContext(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM comments WHERE id=$1 AND work_item_id=$2 AND deleted_at IS NULL)`, body.CommentID, id).Scan(&valid); e != nil {
				return e
			}
			if !valid {
				return invalid("Comment does not belong to this work item")
			}
		}
		var exists bool
		if e := q.QueryRowContext(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM reactions WHERE ((work_item_id=$1 AND $4::uuid IS NULL) OR comment_id=$4) AND user_id=$2 AND emoji=$3 AND deleted_at IS NULL)`, id, s.Actor.UserID, body.Emoji, body.CommentID).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return conflict("You already added this reaction")
		}
		var workItemID any = id
		if body.CommentID != nil {
			workItemID = nil
		}
		return q.QueryRowContext(c.Request.Context(), `INSERT INTO reactions(id,workspace_id,project_id,work_item_id,comment_id,user_id,emoji) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING to_jsonb(reactions)-'deleted_at'`, uuid.New(), s.WorkspaceID, s.ProjectID, workItemID, body.CommentID, s.Actor.UserID, body.Emoji).Scan(&result)
	})
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 201, json.RawMessage(result))
}
func (h *handler) deleteReaction(c *gin.Context) {
	s, id, _, e := h.issueScope(c, identity.Guest)
	if e != nil {
		respondError(c, e)
		return
	}
	rid, e := uuid.Parse(c.Param("reactionID"))
	if e != nil {
		respondError(c, invalid("Invalid reaction identifier"))
		return
	}
	var n int64
	e = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, e := loadIssue(c, q, s, id, true); e != nil {
			return e
		}
		res, e := q.ExecContext(c.Request.Context(), `DELETE FROM reactions WHERE id=$1 AND (work_item_id=$2 OR comment_id IN(SELECT id FROM comments WHERE work_item_id=$2)) AND user_id=$3`, rid, id, s.Actor.UserID)
		if e != nil {
			return e
		}
		n, e = res.RowsAffected()
		return e
	})
	if e != nil {
		respondError(c, e)
		return
	}
	if n == 0 {
		respondError(c, absent())
		return
	}
	c.Status(204)
}
