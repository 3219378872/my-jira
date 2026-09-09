package files

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
)

const maxAssetBatchEntities = 100

type assetBatchTarget struct {
	kind string
	id   uuid.UUID
}

type assetBatchEntry struct {
	EntityType string            `json:"entity_type"`
	EntityID   uuid.UUID         `json:"entity_id"`
	Assets     []json.RawMessage `json:"assets"`
}

func assetBatchTargets(query url.Values) ([]assetBatchTarget, error) {
	targets := []assetBatchTarget{}
	seen := map[assetBatchTarget]bool{}
	count := 0
	for _, input := range []struct{ parameter, kind string }{{"work_item_ids", "work_item"}, {"page_ids", "page"}} {
		for _, value := range query[input.parameter] {
			for _, text := range strings.Split(value, ",") {
				count++
				if count > maxAssetBatchEntities {
					return nil, httpapi.NewError(400, "validation_failed", "Request at most 100 entity identifiers in total")
				}
				id, err := uuid.Parse(strings.TrimSpace(text))
				if err != nil || id == uuid.Nil {
					return nil, httpapi.NewError(400, "validation_failed", "Every entity identifier must be a nonzero UUID")
				}
				target := assetBatchTarget{input.kind, id}
				if !seen[target] {
					seen[target] = true
					targets = append(targets, target)
				}
			}
		}
	}
	if count == 0 {
		return nil, httpapi.NewError(400, "validation_failed", "Supply work_item_ids or page_ids")
	}
	return targets, nil
}

func batchAccessError(err error) error {
	var apiError *httpapi.Error
	if errors.As(err, &apiError) && (apiError.Status == 403 || apiError.Status == 404) {
		return sql.ErrNoRows
	}
	return err
}

// Resolve each parent before reading its assets. A workspace request may span
// projects; a project request can only name parents inside that exact project.
func (h *handler) batchEntityScope(ctx context.Context, q database.DBTX, requested identity.Scope, target assetBatchTarget) (identity.Scope, error) {
	table := "work_items"
	if target.kind == "page" {
		table = "pages"
	}
	var projectID uuid.NullUUID
	if err := q.QueryRowContext(ctx, "SELECT project_id FROM "+table+" WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL", target.id, requested.WorkspaceID).Scan(&projectID); err != nil {
		return identity.Scope{}, err
	}
	if requested.ProjectID != uuid.Nil && (!projectID.Valid || projectID.UUID != requested.ProjectID) {
		return identity.Scope{}, sql.ErrNoRows
	}
	policy := &identity.SQLPolicy{DB: q}
	var current identity.Scope
	var err error
	if projectID.Valid {
		current, err = policy.Project(ctx, requested.Actor, requested.WorkspaceID, projectID.UUID, identity.Guest)
	} else {
		current, err = policy.Workspace(ctx, requested.Actor, requested.WorkspaceID, identity.Guest)
	}
	if err != nil {
		return current, batchAccessError(err)
	}
	if target.kind == "page" {
		err = h.page(ctx, q, current, target.id, false)
	} else {
		err = h.issue(ctx, q, current, target.id)
	}
	return current, batchAccessError(err)
}

func batchEntityAssets(ctx context.Context, q database.DBTX, scope identity.Scope, target assetBatchTarget, deleted bool) ([]json.RawMessage, error) {
	column := "work_item_id"
	if target.kind == "page" {
		column = "page_id"
	}
	rows, err := q.QueryContext(ctx, "SELECT f.id,to_jsonb(f)-'object_key' FROM file_assets f WHERE f.workspace_id=$1 AND f.project_id IS NOT DISTINCT FROM $2::uuid AND f."+column+"=$3 AND (f.deleted_at IS NOT NULL)=$4 AND f.upload_status='completed' ORDER BY f.created_at DESC,f.id DESC", scope.WorkspaceID, nullable(scope.ProjectID), target.id, deleted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assets := []json.RawMessage{}
	for rows.Next() {
		var id uuid.UUID
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var asset map[string]json.RawMessage
		if err := json.Unmarshal(raw, &asset); err != nil {
			return nil, err
		}
		asset["download_url"], err = json.Marshal(assetURL(scope, id))
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(asset)
		if err != nil {
			return nil, err
		}
		assets = append(assets, encoded)
	}
	return assets, rows.Err()
}

func (h *handler) batch(c *gin.Context) {
	requested, err := h.scope(c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	query := c.Request.URL.Query()
	targets, err := assetBatchTargets(query)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	deleted := false
	if values, present := query["deleted"]; present {
		if len(values) != 1 || (values[0] != "true" && values[0] != "false") {
			failure(c, 400, "validation_failed", "deleted must be true or false")
			return
		}
		deleted = values[0] == "true"
	}
	result := make([]assetBatchEntry, 0, len(targets))
	err = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		for _, target := range targets {
			scope, err := h.batchEntityScope(c.Request.Context(), q, requested, target)
			if err != nil {
				return err
			}
			assets, err := batchEntityAssets(c.Request.Context(), q, scope, target, deleted)
			if err != nil {
				return err
			}
			result = append(result, assetBatchEntry{target.kind, target.id, assets})
		}
		return nil
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, result)
}
