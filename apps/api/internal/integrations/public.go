package integrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/microcosm-cc/bluemonday"
	"my-jira/apps/api/internal/files"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	datahooks "my-jira/apps/api/internal/support/data"
)

type siteConfig struct {
	ID, WorkspaceID, ProjectID         uuid.UUID
	Slug, Title, Description           string
	Comments, Reactions, Votes, Intake bool
	Settings                           json.RawMessage
}

func (h *handler) findSite(c *gin.Context) (siteConfig, error) {
	var s siteConfig
	e := h.d.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT s.id,s.workspace_id,s.project_id,s.slug,s.title,s.description,s.comments_enabled,s.reactions_enabled,s.votes_enabled,s.intake_enabled,s.settings FROM public_sites s JOIN projects p ON p.id=s.project_id JOIN workspaces w ON w.id=s.workspace_id WHERE s.slug=$1 AND s.is_enabled AND s.deleted_at IS NULL AND p.deleted_at IS NULL AND p.archived_at IS NULL AND w.deleted_at IS NULL`, c.Param("slug")).Scan(&s.ID, &s.WorkspaceID, &s.ProjectID, &s.Slug, &s.Title, &s.Description, &s.Comments, &s.Reactions, &s.Votes, &s.Intake, &s.Settings)
	return s, e
}

func lockPublicWrite(ctx context.Context, q database.DBTX, site siteConfig, item uuid.UUID, feature string) error {
	column, ok := map[string]string{"comments": "comments_enabled", "reactions": "reactions_enabled", "votes": "votes_enabled", "intake": "intake_enabled"}[feature]
	if !ok {
		return httpapi.NewError(400, "invalid_request", "Unknown public interaction")
	}
	projectLock := "SHARE"
	if item == uuid.Nil {
		projectLock = "UPDATE" // Intake creation allocates a project sequence.
	}
	var id uuid.UUID
	if e := q.QueryRowContext(ctx, `SELECT p.id FROM projects p JOIN workspaces ws ON ws.id=p.workspace_id WHERE p.id=$1 AND p.workspace_id=$2 AND p.deleted_at IS NULL AND p.archived_at IS NULL AND ws.deleted_at IS NULL FOR `+projectLock+` OF p FOR SHARE OF ws`, site.ProjectID, site.WorkspaceID).Scan(&id); e != nil {
		return e
	}
	var enabled bool
	if e := q.QueryRowContext(ctx, `SELECT `+column+` FROM public_sites WHERE id=$1 AND project_id=$2 AND slug=$3 AND is_enabled AND deleted_at IS NULL FOR SHARE`, site.ID, site.ProjectID, site.Slug).Scan(&enabled); e != nil {
		return e
	}
	if !enabled {
		return httpapi.NewError(403, "forbidden", "This public interaction is disabled")
	}
	if item != uuid.Nil {
		return q.QueryRowContext(ctx, `SELECT id FROM work_items WHERE id=$1 AND project_id=$2 AND workspace_id=$3 AND deleted_at IS NULL AND archived_at IS NULL AND NOT is_draft FOR UPDATE`, item, site.ProjectID, site.WorkspaceID).Scan(&id)
	}
	return nil
}
func (h *handler) site(c *gin.Context) {
	s, e := h.scope(c, identity.Admin)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	var raw []byte
	e = h.d.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT to_jsonb(s)-'deleted_at' FROM public_sites s WHERE project_id=$1 AND workspace_id=$2 AND deleted_at IS NULL`, s.ProjectID, s.WorkspaceID).Scan(&raw)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 200, json.RawMessage(raw))
}
func (h *handler) saveSite(c *gin.Context) {
	s, e := h.scope(c, identity.Admin)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	var b struct {
		Slug        string         `json:"slug"`
		Title       string         `json:"title"`
		Description string         `json:"description"`
		Enabled     bool           `json:"is_enabled"`
		Comments    bool           `json:"comments_enabled"`
		Reactions   bool           `json:"reactions_enabled"`
		Votes       bool           `json:"votes_enabled"`
		Intake      bool           `json:"intake_enabled"`
		Settings    map[string]any `json:"settings"`
	}
	if e = c.ShouldBindJSON(&b); e != nil {
		fail(c, 400, "Invalid site configuration")
		return
	}
	b.Slug = strings.ToLower(strings.TrimSpace(b.Slug))
	if len(b.Slug) < 3 || len(b.Slug) > 64 || strings.Trim(b.Slug, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
		fail(c, 400, "Site address must contain 3–64 lowercase letters, digits or hyphens")
		return
	}
	if b.Title == "" || len(b.Title) > 255 {
		fail(c, 400, "A site title is required")
		return
	}
	if b.Settings == nil {
		b.Settings = map[string]any{}
	}
	settings, _ := json.Marshal(b.Settings)
	var raw []byte
	e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, e := q.ExecContext(c.Request.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, s.ProjectID.String()); e != nil {
			return e
		}
		var id uuid.UUID
		e := q.QueryRowContext(c.Request.Context(), `SELECT id FROM public_sites WHERE project_id=$1 AND deleted_at IS NULL`, s.ProjectID).Scan(&id)
		if e == sql.ErrNoRows {
			id = uuid.New()
			_, e = q.ExecContext(c.Request.Context(), `INSERT INTO public_sites(id,workspace_id,project_id,slug,title,description,is_enabled,comments_enabled,reactions_enabled,votes_enabled,intake_enabled,settings) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb)`, id, s.WorkspaceID, s.ProjectID, b.Slug, b.Title, b.Description, b.Enabled, b.Comments, b.Reactions, b.Votes, b.Intake, string(settings))
		} else if e == nil {
			_, e = q.ExecContext(c.Request.Context(), `UPDATE public_sites SET slug=$2,title=$3,description=$4,is_enabled=$5,comments_enabled=$6,reactions_enabled=$7,votes_enabled=$8,intake_enabled=$9,settings=$10::jsonb,updated_at=now() WHERE id=$1`, id, b.Slug, b.Title, b.Description, b.Enabled, b.Comments, b.Reactions, b.Votes, b.Intake, string(settings))
		}
		if e != nil {
			return e
		}
		return q.QueryRowContext(c.Request.Context(), `SELECT to_jsonb(s)-'deleted_at' FROM public_sites s WHERE id=$1`, id).Scan(&raw)
	})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 200, json.RawMessage(raw))
}
func (h *handler) publicSite(c *gin.Context) {
	s, e := h.findSite(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 200, gin.H{"slug": s.Slug, "title": s.Title, "description": s.Description, "comments_enabled": s.Comments, "reactions_enabled": s.Reactions, "votes_enabled": s.Votes, "intake_enabled": s.Intake, "settings": s.Settings})
}

const publicIssueSelect = `SELECT jsonb_build_object('id',w.id,'name',w.name,'description_html',w.description_html,'sequence_id',w.sequence_id,'priority',w.priority,'start_date',w.start_date,'target_date',w.target_date,'created_at',w.created_at,'state',jsonb_build_object('id',s.id,'name',s.name,'color',s.color,'group',s.group_name),'vote_count',(SELECT count(*) FROM public_votes v WHERE v.work_item_id=w.id AND v.deleted_at IS NULL),'voted',EXISTS(SELECT 1 FROM public_votes v WHERE v.work_item_id=w.id AND v.user_id=$2 AND v.deleted_at IS NULL),'labels',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',l.id,'name',l.name,'color',l.color)) FROM work_item_labels rel JOIN labels l ON l.id=rel.label_id WHERE rel.work_item_id=w.id AND rel.deleted_at IS NULL AND l.deleted_at IS NULL),'[]'::jsonb)) FROM work_items w JOIN states s ON s.id=w.state_id WHERE w.project_id=$1 AND w.deleted_at IS NULL AND w.archived_at IS NULL AND NOT w.is_draft `

func (h *handler) publicIssues(c *gin.Context) {
	s, e := h.findSite(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	query := publicIssueSelect
	actor, _ := httpapi.Actor(c)
	args := []any{s.ProjectID, actor.UserID}
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		query += " AND w.name ILIKE $3"
		args = append(args, "%"+search+"%")
	}
	h.query(c, query+` ORDER BY w.position,w.id LIMIT 500`, args...)
}
func (h *handler) findPublicIssue(c *gin.Context, s siteConfig) (uuid.UUID, error) {
	id, e := httpapi.UUIDParam(c, "issueID")
	if e != nil {
		return id, e
	}
	var valid uuid.UUID
	e = h.d.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT id FROM work_items WHERE id=$1 AND project_id=$2 AND deleted_at IS NULL AND archived_at IS NULL AND NOT is_draft`, id, s.ProjectID).Scan(&valid)
	return id, e
}
func (h *handler) publicIssue(c *gin.Context) {
	s, e := h.findSite(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	id, e := h.findPublicIssue(c, s)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	var raw []byte
	actor, _ := httpapi.Actor(c)
	e = h.d.DB.SQL.QueryRowContext(c.Request.Context(), publicIssueSelect+` AND w.id=$3`, s.ProjectID, actor.UserID, id).Scan(&raw)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 200, json.RawMessage(raw))
}
func (h *handler) publicComments(c *gin.Context) {
	s, e := h.findSite(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if !s.Comments {
		fail(c, 403, "Public comments are disabled")
		return
	}
	id, e := h.findPublicIssue(c, s)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	h.query(c, `SELECT jsonb_build_object('id',cm.id,'body_html',cm.body_html,'created_at',cm.created_at,'edited_at',cm.edited_at,'author_id',cm.author_id,'author',jsonb_build_object('display_name',u.display_name,'avatar_url',u.avatar_url)) FROM comments cm JOIN users u ON u.id=cm.author_id WHERE cm.work_item_id=$1 AND cm.is_public AND cm.deleted_at IS NULL ORDER BY cm.created_at`, id)
}

func (h *handler) publicReactions(c *gin.Context) {
	s, e := h.findSite(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if !s.Reactions {
		fail(c, 403, "Reactions are disabled")
		return
	}
	id, e := h.findPublicIssue(c, s)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	actor, _ := httpapi.Actor(c)
	h.query(c, `SELECT jsonb_build_object('emoji',emoji,'count',count(*),'reacted',bool_or(user_id=$2)) FROM reactions WHERE work_item_id=$1 AND comment_id IS NULL AND deleted_at IS NULL GROUP BY emoji ORDER BY emoji`, id, actor.UserID)
}

func (h *handler) publicReaction(c *gin.Context) {
	s, e := h.findSite(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if !s.Reactions {
		fail(c, 403, "Reactions are disabled")
		return
	}
	id, e := h.findPublicIssue(c, s)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	a, e := httpapi.Actor(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	body, e := httpapi.Bind[struct {
		Emoji string `json:"emoji"`
	}](c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if strings.TrimSpace(body.Emoji) == "" || len(body.Emoji) > 32 {
		fail(c, 400, "Supply one reaction of up to 32 bytes")
		return
	}
	e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := lockPublicWrite(c.Request.Context(), q, s, id, "reactions"); err != nil {
			return err
		}
		if c.Request.Method == "DELETE" {
			_, err := q.ExecContext(c.Request.Context(), `DELETE FROM reactions WHERE work_item_id=$1 AND user_id=$2 AND emoji=$3 AND comment_id IS NULL`, id, a.UserID, body.Emoji)
			return err
		}
		_, err := q.ExecContext(c.Request.Context(), `INSERT INTO reactions(id,workspace_id,project_id,work_item_id,user_id,emoji)VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, uuid.New(), s.WorkspaceID, s.ProjectID, id, a.UserID, body.Emoji)
		return err
	})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	h.publicReactions(c)
}

func (h *handler) updatePublicComment(c *gin.Context) {
	s, e := h.findSite(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if !s.Comments {
		fail(c, 403, "Public comments are disabled")
		return
	}
	id, e := h.findPublicIssue(c, s)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	comment, e := httpapi.UUIDParam(c, "commentID")
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	a, e := httpapi.Actor(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if c.Request.Method == "DELETE" {
		e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
			if err := lockPublicWrite(c.Request.Context(), q, s, id, "comments"); err != nil {
				return err
			}
			var removed uuid.UUID
			if err := q.QueryRowContext(c.Request.Context(), `UPDATE comments SET deleted_at=now(),updated_at=now() WHERE id=$1 AND work_item_id=$2 AND author_id=$3 AND is_public AND deleted_at IS NULL RETURNING id`, comment, id, a.UserID).Scan(&removed); err != nil {
				return err
			}
			return datahooks.RecordIssue(c.Request.Context(), h.d, q, identity.Scope{WorkspaceID: s.WorkspaceID, ProjectID: s.ProjectID, Actor: a}, id, "comment_deleted", map[string]any{"comment_id": comment}, nil, false)
		})
		if e != nil {
			httpapi.Fail(c, e)
			return
		}
		c.Status(204)
		return
	}
	body, e := httpapi.Bind[struct {
		BodyHTML string `json:"body_html"`
	}](c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	body.BodyHTML = bluemonday.UGCPolicy().Sanitize(body.BodyHTML)
	if strings.TrimSpace(bluemonday.StrictPolicy().Sanitize(body.BodyHTML)) == "" || len(body.BodyHTML) > 65536 {
		fail(c, 400, "Supply a nonempty comment")
		return
	}
	var raw []byte
	e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := lockPublicWrite(c.Request.Context(), q, s, id, "comments"); err != nil {
			return err
		}
		var old string
		if err := q.QueryRowContext(c.Request.Context(), `SELECT body_html FROM comments WHERE id=$1 AND work_item_id=$2 AND author_id=$3 AND is_public AND deleted_at IS NULL FOR UPDATE`, comment, id, a.UserID).Scan(&old); err != nil {
			return err
		}
		if err := q.QueryRowContext(c.Request.Context(), `UPDATE comments SET body_html=$2,edited_at=now(),updated_at=now() WHERE id=$1 RETURNING jsonb_build_object('id',id,'author_id',author_id,'body_html',body_html,'created_at',created_at,'edited_at',edited_at)`, comment, body.BodyHTML).Scan(&raw); err != nil {
			return err
		}
		return datahooks.RecordIssue(c.Request.Context(), h.d, q, identity.Scope{WorkspaceID: s.WorkspaceID, ProjectID: s.ProjectID, Actor: a}, id, "comment_edited", map[string]any{"comment_id": comment, "body_html": old}, map[string]any{"comment_id": comment, "body_html": body.BodyHTML}, false)
	})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 200, json.RawMessage(raw))
}

func (h *handler) publicAttachments(c *gin.Context) {
	s, e := h.findSite(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	id, e := h.findPublicIssue(c, s)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	prefix := fmt.Sprintf("/api/v1/public/%s/issues/%s/attachments/", s.Slug, id)
	h.query(c, `SELECT jsonb_build_object('id',id,'filename',filename,'content_type',content_type,'size_bytes',size_bytes,'download_url',$2::text||id::text||'/download') FROM file_assets WHERE work_item_id=$1 AND deleted_at IS NULL AND upload_status='completed' AND metadata->>'public'='true' AND page_id IS NULL ORDER BY created_at`, id, prefix)
}

func (h *handler) publicAttachment(c *gin.Context) {
	s, e := h.findSite(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	id, e := h.findPublicIssue(c, s)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	asset, e := httpapi.UUIDParam(c, "assetID")
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	var key, name, ctype string
	var size int64
	e = h.d.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT object_key,filename,content_type,size_bytes FROM file_assets WHERE id=$1 AND work_item_id=$2 AND workspace_id=$3 AND project_id=$4 AND deleted_at IS NULL AND upload_status='completed' AND metadata->>'public'='true' AND page_id IS NULL`, asset, id, s.WorkspaceID, s.ProjectID).Scan(&key, &name, &ctype, &size)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	files.Stream(c, h.d.DB.SQL, key, name, ctype, size)
}

func (h *handler) publicComment(c *gin.Context) {
	s, e := h.findSite(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if !s.Comments {
		fail(c, 403, "Public comments are disabled")
		return
	}
	id, e := h.findPublicIssue(c, s)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	a, e := httpapi.Actor(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	var b struct {
		BodyHTML string `json:"body_html"`
	}
	if e = c.ShouldBindJSON(&b); e != nil || len(b.BodyHTML) > 65536 {
		fail(c, 400, "Invalid comment")
		return
	}
	b.BodyHTML = bluemonday.UGCPolicy().Sanitize(b.BodyHTML)
	if strings.TrimSpace(bluemonday.StrictPolicy().Sanitize(b.BodyHTML)) == "" {
		fail(c, 400, "Comment is empty")
		return
	}
	var raw []byte
	commentID := uuid.New()
	e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := lockPublicWrite(c.Request.Context(), q, s, id, "comments"); err != nil {
			return err
		}
		if e := q.QueryRowContext(c.Request.Context(), `INSERT INTO comments(id,workspace_id,project_id,work_item_id,author_id,body_html,body_json,is_public) VALUES($1,$2,$3,$4,$5,$6,'{}',true) RETURNING jsonb_build_object('id',id,'body_html',body_html,'created_at',created_at)`, commentID, s.WorkspaceID, s.ProjectID, id, a.UserID, b.BodyHTML).Scan(&raw); e != nil {
			return e
		}
		return datahooks.RecordIssue(c.Request.Context(), h.d, q, identity.Scope{WorkspaceID: s.WorkspaceID, ProjectID: s.ProjectID, Actor: a}, id, "commented", nil, map[string]any{"comment_id": commentID, "body_html": b.BodyHTML}, false)
	})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 201, json.RawMessage(raw))
}
func (h *handler) publicVote(c *gin.Context) {
	s, e := h.findSite(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if !s.Votes {
		fail(c, 403, "Voting is disabled")
		return
	}
	id, e := h.findPublicIssue(c, s)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	a, e := httpapi.Actor(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := lockPublicWrite(c.Request.Context(), q, s, id, "votes"); err != nil {
			return err
		}
		if c.Request.Method == "DELETE" {
			_, err := q.ExecContext(c.Request.Context(), `DELETE FROM public_votes WHERE work_item_id=$1 AND user_id=$2`, id, a.UserID)
			return err
		}
		_, err := q.ExecContext(c.Request.Context(), `INSERT INTO public_votes(id,workspace_id,project_id,work_item_id,user_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, uuid.New(), s.WorkspaceID, s.ProjectID, id, a.UserID)
		return err
	})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	var count int
	if e = h.d.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT count(*) FROM public_votes WHERE work_item_id=$1 AND deleted_at IS NULL`, id).Scan(&count); e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 200, gin.H{"vote_count": count, "voted": c.Request.Method != "DELETE"})
}
func (h *handler) publicIntake(c *gin.Context) {
	s, e := h.findSite(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if !s.Intake {
		fail(c, 403, "Submissions are disabled")
		return
	}
	a, e := httpapi.Actor(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	var b struct {
		Name            string `json:"name"`
		DescriptionHTML string `json:"description_html"`
	}
	if e = c.ShouldBindJSON(&b); e != nil || strings.TrimSpace(b.Name) == "" || len([]rune(b.Name)) > 255 || len(b.DescriptionHTML) > 262144 {
		fail(c, 400, "Supply a title and a description below 256 KiB")
		return
	}
	b.DescriptionHTML = bluemonday.UGCPolicy().Sanitize(b.DescriptionHTML)
	id := uuid.New()
	e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := lockPublicWrite(c.Request.Context(), q, s, uuid.Nil, "intake"); err != nil {
			return err
		}
		var seq int64
		if e := q.QueryRowContext(c.Request.Context(), `UPDATE projects SET next_sequence=next_sequence+1 WHERE id=$1 RETURNING next_sequence-1`, s.ProjectID).Scan(&seq); e != nil {
			return e
		}
		var state uuid.UUID
		if e := q.QueryRowContext(c.Request.Context(), `SELECT id FROM states WHERE project_id=$1 AND deleted_at IS NULL ORDER BY is_default DESC,position LIMIT 1`, s.ProjectID).Scan(&state); e != nil {
			return e
		}
		if _, e := q.ExecContext(c.Request.Context(), `INSERT INTO work_items(id,workspace_id,project_id,state_id,created_by,updated_by,name,description_html,sequence_id,position,is_draft) VALUES($1,$2,$3,$4,$5,$5,$6,$7,$8,$9,true)`, id, s.WorkspaceID, s.ProjectID, state, a.UserID, b.Name, b.DescriptionHTML, seq, float64(seq)*1024); e != nil {
			return e
		}
		if _, e := q.ExecContext(c.Request.Context(), `INSERT INTO intake_items(id,workspace_id,project_id,work_item_id,status,source,submitted_by,metadata) VALUES($1,$2,$3,$4,'pending','public',$5,'{}')`, uuid.New(), s.WorkspaceID, s.ProjectID, id, a.UserID); e != nil {
			return e
		}
		return datahooks.RecordIssue(c.Request.Context(), h.d, q, identity.Scope{WorkspaceID: s.WorkspaceID, ProjectID: s.ProjectID, Actor: a}, id, "created", nil, map[string]any{"name": b.Name}, true)
	})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 201, gin.H{"id": id, "status": "pending"})
}
