package files

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/objectstore"
	"my-jira/apps/api/internal/support/data"
)

type handler struct{ d platform.Dependencies }

func Register(r *gin.RouterGroup, d platform.Dependencies) {
	h := &handler{d: d}
	for _, prefix := range []string{"/auth", "/workspaces/:workspaceID", "/workspaces/:workspaceID/projects/:projectID"} {
		p := r.Group(prefix)
		p.POST("/assets", h.upload)
		p.GET("/assets", h.list)
		p.GET("/assets/:assetID/download", h.download)
		p.DELETE("/assets/:assetID", h.remove)
		p.POST("/assets/:assetID/restore", h.restore)
		p.POST("/assets/:assetID/copy", h.copy)
		p.PATCH("/assets/:assetID", h.update)
	}
	r.GET("/workspaces/:workspaceID/assets/batch", h.batch)
	r.GET("/workspaces/:workspaceID/projects/:projectID/assets/batch", h.batch)
	p := r.Group("/workspaces/:workspaceID/projects/:projectID")
	p.POST("/issues/:issueID/attachments", h.upload)
	p.GET("/issues/:issueID/attachments", h.list)
}

func failure(c *gin.Context, status int, code, message string) {
	httpapi.Fail(c, httpapi.NewError(status, code, message))
}

func (h *handler) scope(c *gin.Context) (identity.Scope, error) {
	a, e := httpapi.Actor(c)
	if e != nil {
		return identity.Scope{}, e
	}
	if c.Param("workspaceID") == "" {
		if a.TokenWorkspaceID != uuid.Nil {
			return identity.Scope{}, httpapi.NewError(403, "forbidden", "API tokens cannot access personal assets")
		}
		return identity.Scope{Actor: a, Role: identity.Admin}, nil
	}
	w, e := httpapi.UUIDParam(c, "workspaceID")
	if e != nil {
		return identity.Scope{}, e
	}
	if c.Param("projectID") == "" {
		return h.d.Policy.Workspace(c.Request.Context(), a, w, identity.Guest)
	}
	p, e := httpapi.UUIDParam(c, "projectID")
	if e != nil {
		return identity.Scope{}, e
	}
	return h.d.Policy.Project(c.Request.Context(), a, w, p, identity.Guest)
}

func nullable(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func (h *handler) issue(ctx context.Context, q database.DBTX, s identity.Scope, id uuid.UUID) error {
	var author uuid.UUID
	e := q.QueryRowContext(ctx, `SELECT created_by FROM work_items WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL`, id, s.WorkspaceID, s.ProjectID).Scan(&author)
	if e != nil {
		return e
	}
	if s.Role < identity.Member && !s.GuestCanViewAll && author != s.Actor.UserID {
		return sql.ErrNoRows
	}
	return nil
}

func (h *handler) page(ctx context.Context, q database.DBTX, s identity.Scope, id uuid.UUID, write bool) error {
	current, e := data.PageProjectScope(ctx, q, s.Actor, s.WorkspaceID, s.ProjectID, identity.Guest)
	if e != nil {
		return e
	}
	var owner uuid.UUID
	if e := q.QueryRowContext(ctx, `SELECT owner_id FROM pages WHERE id=$1 AND workspace_id=$2 AND project_id IS NOT DISTINCT FROM $3::uuid AND deleted_at IS NULL AND (NOT is_private OR owner_id=$4) AND (NOT $5 OR (NOT is_locked AND archived_at IS NULL)) FOR SHARE`, id, s.WorkspaceID, nullable(s.ProjectID), s.Actor.UserID, write).Scan(&owner); e != nil {
		return e
	}
	if current.Role < identity.Member && owner != s.Actor.UserID && (write || !current.GuestCanViewAll) {
		return sql.ErrNoRows
	}
	return nil
}

func (h *handler) targets(ctx context.Context, q database.DBTX, s identity.Scope, issue, page *uuid.UUID, write bool) error {
	if issue != nil && page != nil {
		return httpapi.NewError(400, "validation_failed", "Choose either a work item or a page")
	}
	if issue != nil {
		if s.ProjectID == uuid.Nil {
			return httpapi.NewError(400, "validation_failed", "Work item assets require a project")
		}
		return h.issue(ctx, q, s, *issue)
	}
	if page != nil {
		if s.WorkspaceID == uuid.Nil {
			return httpapi.NewError(400, "validation_failed", "Page assets require a workspace")
		}
		return h.page(ctx, q, s, *page, write)
	}
	if write && s.Role < identity.Member {
		return httpapi.NewError(403, "forbidden", "Guests must attach files to an accessible work item or an owned page")
	}
	return nil
}

func assetURL(s identity.Scope, id uuid.UUID) string {
	prefix := "/api/v1/auth"
	if s.WorkspaceID != uuid.Nil {
		prefix = "/api/v1/workspaces/" + s.WorkspaceID.String()
	}
	if s.ProjectID != uuid.Nil {
		prefix += "/projects/" + s.ProjectID.String()
	}
	return prefix + "/assets/" + id.String() + "/download"
}

func parseOptional(value string) (*uuid.UUID, error) {
	if value == "" {
		return nil, nil
	}
	id, e := uuid.Parse(value)
	if e != nil {
		return nil, httpapi.NewError(400, "validation_failed", "Invalid related entity identifier")
	}
	return &id, nil
}

func (h *handler) upload(c *gin.Context) {
	s, e := h.scope(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	const maxSize = 25 << 20
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSize+(1<<20))
	if e = c.Request.ParseMultipartForm(1 << 20); e != nil {
		failure(c, 400, "invalid_upload", "Supply a multipart file smaller than 25 MiB")
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	issueText := c.Param("issueID")
	if issueText == "" {
		issueText = c.PostForm("work_item_id")
	}
	issue, e := parseOptional(issueText)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	page, e := parseOptional(c.PostForm("page_id"))
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if e = h.targets(c.Request.Context(), h.d.DB.SQL, s, issue, page, true); e != nil {
		httpapi.Fail(c, e)
		return
	}
	file, header, e := c.Request.FormFile("file")
	if e != nil {
		failure(c, 400, "invalid_upload", "File is required")
		return
	}
	defer file.Close()
	if header.Size < 1 || header.Size > maxSize {
		failure(c, 400, "invalid_upload", "File must contain between 1 byte and 25 MiB")
		return
	}
	name := filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || len(name) > 255 {
		failure(c, 400, "invalid_upload", "Invalid filename")
		return
	}
	first := make([]byte, 512)
	n, e := io.ReadFull(file, first)
	if e != nil && e != io.EOF && e != io.ErrUnexpectedEOF {
		httpapi.Fail(c, e)
		return
	}
	ctype := http.DetectContentType(first[:n])
	id := uuid.New()
	key := "assets/" + s.Actor.UserID.String() + "/" + id.String() + "/" + name
	store, bucket, e := objectstore.Load(c.Request.Context(), h.d.DB.SQL)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if e = objectstore.EnsureBucket(c.Request.Context(), store, bucket); e != nil {
		httpapi.Fail(c, e)
		return
	}
	_, e = store.PutObject(c.Request.Context(), bucket, key, io.MultiReader(bytes.NewReader(first[:n]), file), header.Size, minio.PutObjectOptions{ContentType: ctype})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	var raw []byte
	e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		// Repeat entity checks after object I/O so a page lock or deletion cannot
		// turn an upload into an unauthorized attachment.
		if e = h.targets(c.Request.Context(), q, s, issue, page, true); e != nil {
			return e
		}
		return q.QueryRowContext(c.Request.Context(), `INSERT INTO file_assets(id,workspace_id,project_id,work_item_id,page_id,uploaded_by,filename,content_type,size_bytes,object_key,upload_status,metadata) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'completed','{}') RETURNING to_jsonb(file_assets)-'object_key'`, id, nullable(s.WorkspaceID), nullable(s.ProjectID), issue, page, s.Actor.UserID, name, ctype, header.Size, key).Scan(&raw)
	})
	if e != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = store.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
		httpapi.Fail(c, e)
		return
	}
	respondAsset(c, 201, s, raw)
}

func respondAsset(c *gin.Context, status int, s identity.Scope, raw []byte) {
	var data map[string]any
	if e := json.Unmarshal(raw, &data); e != nil {
		httpapi.Fail(c, e)
		return
	}
	data["download_url"] = assetURL(s, uuid.MustParse(data["id"].(string)))
	httpapi.JSON(c, status, data)
}

type asset struct {
	ID, Owner       uuid.UUID
	Issue, Page     uuid.NullUUID
	Key, Name, Type string
	Size            int64
	Raw             []byte
}

func (h *handler) load(c *gin.Context, s identity.Scope, deleted bool) (asset, error) {
	var f asset
	id, e := httpapi.UUIDParam(c, "assetID")
	if e != nil {
		return f, e
	}
	e = h.d.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT id,uploaded_by,work_item_id,page_id,object_key,filename,content_type,size_bytes,to_jsonb(f)-'object_key' FROM file_assets f WHERE id=$1 AND workspace_id IS NOT DISTINCT FROM $2::uuid AND project_id IS NOT DISTINCT FROM $3::uuid AND (deleted_at IS NOT NULL)=$4 AND upload_status='completed'`, id, nullable(s.WorkspaceID), nullable(s.ProjectID), deleted).Scan(&f.ID, &f.Owner, &f.Issue, &f.Page, &f.Key, &f.Name, &f.Type, &f.Size, &f.Raw)
	if e != nil {
		return f, e
	}
	if s.WorkspaceID == uuid.Nil && f.Owner != s.Actor.UserID {
		return f, sql.ErrNoRows
	}
	if f.Issue.Valid {
		if e = h.issue(c.Request.Context(), h.d.DB.SQL, s, f.Issue.UUID); e != nil {
			return f, e
		}
	}
	if f.Page.Valid {
		if e = h.page(c.Request.Context(), h.d.DB.SQL, s, f.Page.UUID, false); e != nil {
			return f, e
		}
	}
	if !f.Issue.Valid && !f.Page.Valid && s.Role < identity.Member && f.Owner != s.Actor.UserID {
		return f, sql.ErrNoRows
	}
	return f, nil
}

func (h *handler) list(c *gin.Context) {
	s, e := h.scope(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	args := []any{nullable(s.WorkspaceID), nullable(s.ProjectID), c.Query("deleted") == "true", s.Actor.UserID}
	where := `f.workspace_id IS NOT DISTINCT FROM $1::uuid AND f.project_id IS NOT DISTINCT FROM $2::uuid AND (f.deleted_at IS NOT NULL)=$3 AND f.upload_status='completed'`
	issueText := c.Param("issueID")
	if issueText == "" {
		issueText = c.Query("work_item_id")
	}
	issue, e := parseOptional(issueText)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	page, e := parseOptional(c.Query("page_id"))
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if e = h.targets(c.Request.Context(), h.d.DB.SQL, s, issue, page, false); e != nil {
		httpapi.Fail(c, e)
		return
	}
	if issue != nil {
		where += " AND f.work_item_id=$5"
		args = append(args, *issue)
	} else if page != nil {
		where += " AND f.page_id=$5"
		args = append(args, *page)
	} else {
		where += " AND f.uploaded_by=$4"
	}
	// Previously uploaded resources disappear after the entity becomes private,
	// is deleted or the actor loses its guest visibility.
	where += ` AND (f.page_id IS NULL OR EXISTS(SELECT 1 FROM pages p WHERE p.id=f.page_id AND ` + data.VisiblePage("p", "$1", "$4") + `)) AND (f.work_item_id IS NULL OR EXISTS(SELECT 1 FROM work_items w WHERE w.id=f.work_item_id AND w.deleted_at IS NULL`
	if s.Role < identity.Member && !s.GuestCanViewAll {
		where += ` AND w.created_by=$4`
	}
	where += `))`
	rows, e := h.d.DB.SQL.QueryContext(c.Request.Context(), `SELECT to_jsonb(f)-'object_key' FROM file_assets f WHERE `+where+` ORDER BY f.created_at DESC,f.id DESC`, args...)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	defer rows.Close()
	data := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			httpapi.Fail(c, e)
			return
		}
		var v map[string]any
		if e = json.Unmarshal(raw, &v); e != nil {
			httpapi.Fail(c, e)
			return
		}
		v["download_url"] = assetURL(s, uuid.MustParse(v["id"].(string)))
		data = append(data, v)
	}
	if e = rows.Err(); e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 200, data)
}

func Stream(c *gin.Context, q database.DBTX, key, name, ctype string, size int64) {
	store, bucket, e := objectstore.Load(c.Request.Context(), q)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	object, e := store.GetObject(c.Request.Context(), bucket, key, minio.GetObjectOptions{})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	defer object.Close()
	if _, e = object.Stat(); e != nil {
		httpapi.Fail(c, e)
		return
	}
	disposition := "attachment"
	if c.Query("inline") == "true" && (ctype == "image/png" || ctype == "image/jpeg" || ctype == "image/gif" || ctype == "image/webp") {
		disposition = "inline"
	}
	c.Header("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, no-store")
	c.DataFromReader(200, size, ctype, object, nil)
}

func (h *handler) download(c *gin.Context) {
	s, e := h.scope(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	f, e := h.load(c, s, false)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	Stream(c, h.d.DB.SQL, f.Key, f.Name, f.Type, f.Size)
}

func (h *handler) modify(c *gin.Context, deleted bool) (identity.Scope, asset, error) {
	s, e := h.scope(c)
	if e != nil {
		return s, asset{}, e
	}
	f, e := h.load(c, s, deleted)
	if e != nil {
		return s, f, e
	}
	if f.Owner != s.Actor.UserID && s.Role < identity.Admin {
		return s, f, httpapi.NewError(403, "forbidden", "Only the uploader or an administrator can change this file")
	}
	if f.Page.Valid {
		e = h.page(c.Request.Context(), h.d.DB.SQL, s, f.Page.UUID, true)
	}
	return s, f, e
}

func (h *handler) remove(c *gin.Context) {
	_, f, e := h.modify(c, false)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	// Keep the object for the retention period so accidental removal is reversible.
	if _, e = h.d.DB.SQL.ExecContext(c.Request.Context(), `UPDATE file_assets SET deleted_at=now(),updated_at=now() WHERE id=$1 AND deleted_at IS NULL`, f.ID); e != nil {
		httpapi.Fail(c, e)
		return
	}
	c.Status(204)
}
func (h *handler) restore(c *gin.Context) {
	s, f, e := h.modify(c, true)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	var raw []byte
	e = h.d.DB.SQL.QueryRowContext(c.Request.Context(), `UPDATE file_assets SET deleted_at=NULL,updated_at=now() WHERE id=$1 AND deleted_at>now()-interval '30 days' RETURNING to_jsonb(file_assets)-'object_key'`, f.ID).Scan(&raw)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	respondAsset(c, 200, s, raw)
}
func (h *handler) update(c *gin.Context) {
	s, f, e := h.modify(c, false)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	body, e := httpapi.Bind[struct {
		Public   *bool   `json:"public"`
		Filename *string `json:"filename"`
	}](c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	name := f.Name
	if body.Filename != nil {
		name = strings.TrimSpace(*body.Filename)
		if name == "" || len(name) > 255 || strings.ContainsAny(name, "/\\\r\n\x00") {
			failure(c, 400, "validation_failed", "Invalid filename")
			return
		}
	}
	if body.Public != nil && (!f.Issue.Valid || s.Role < identity.Member) {
		failure(c, 403, "forbidden", "Only project members can publish work item attachments")
		return
	}
	var raw []byte
	e = h.d.DB.SQL.QueryRowContext(c.Request.Context(), `UPDATE file_assets SET filename=$2,metadata=CASE WHEN $3::boolean IS NULL THEN metadata ELSE jsonb_set(metadata,'{public}',to_jsonb($3::boolean)) END,updated_at=now() WHERE id=$1 RETURNING to_jsonb(file_assets)-'object_key'`, f.ID, name, body.Public).Scan(&raw)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	respondAsset(c, 200, s, raw)
}
func (h *handler) copy(c *gin.Context) {
	s, e := h.scope(c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	f, e := h.load(c, s, false)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	body, e := httpapi.Bind[struct {
		WorkItemID *uuid.UUID `json:"work_item_id"`
		PageID     *uuid.UUID `json:"page_id"`
	}](c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if e = h.targets(c.Request.Context(), h.d.DB.SQL, s, body.WorkItemID, body.PageID, true); e != nil {
		httpapi.Fail(c, e)
		return
	}
	store, bucket, e := objectstore.Load(c.Request.Context(), h.d.DB.SQL)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	id := uuid.New()
	key := fmt.Sprintf("assets/%s/%s/%s", s.Actor.UserID, id, f.Name)
	if _, e = store.CopyObject(c.Request.Context(), minio.CopyDestOptions{Bucket: bucket, Object: key}, minio.CopySrcOptions{Bucket: bucket, Object: f.Key}); e != nil {
		httpapi.Fail(c, e)
		return
	}
	var raw []byte
	e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if e = h.targets(c.Request.Context(), q, s, body.WorkItemID, body.PageID, true); e != nil {
			return e
		}
		return q.QueryRowContext(c.Request.Context(), `INSERT INTO file_assets(id,workspace_id,project_id,work_item_id,page_id,uploaded_by,filename,content_type,size_bytes,object_key,upload_status)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'completed') RETURNING to_jsonb(file_assets)-'object_key'`, id, nullable(s.WorkspaceID), nullable(s.ProjectID), body.WorkItemID, body.PageID, s.Actor.UserID, f.Name, f.Type, f.Size, key).Scan(&raw)
	})
	if e != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = store.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
		httpapi.Fail(c, e)
		return
	}
	respondAsset(c, 201, s, raw)
}

// Cleanup removes only expired owned assets, after a thirty-day recovery window.
func Cleanup(ctx context.Context, d platform.Dependencies) error {
	store, bucket, e := objectstore.Load(ctx, d.DB.SQL)
	if e != nil {
		return e
	}
	rows, e := d.DB.SQL.QueryContext(ctx, `SELECT id,object_key FROM file_assets WHERE (deleted_at<now()-interval '30 days' OR (upload_status<>'completed' AND created_at<now()-interval '1 day')) ORDER BY created_at LIMIT 200`)
	if e != nil {
		return e
	}
	type pending struct {
		id  uuid.UUID
		key string
	}
	items := []pending{}
	for rows.Next() {
		var v pending
		if e = rows.Scan(&v.id, &v.key); e != nil {
			rows.Close()
			return e
		}
		items = append(items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range items {
		if e = store.RemoveObject(ctx, bucket, v.key, minio.RemoveObjectOptions{}); e != nil {
			return e
		}
		if _, e = d.DB.SQL.ExecContext(ctx, `DELETE FROM file_assets WHERE id=$1 AND (deleted_at<now()-interval '30 days' OR (upload_status<>'completed' AND created_at<now()-interval '1 day'))`, v.id); e != nil {
			return e
		}
	}
	return nil
}
