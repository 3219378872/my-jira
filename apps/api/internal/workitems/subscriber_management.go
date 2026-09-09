package workitems

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
)

func subscriberVisible(role identity.Role, creator, subscriber uuid.UUID, guestCanViewAll bool) bool {
	return role >= identity.Member || guestCanViewAll || creator == subscriber
}

// Explicit membership is required before adding another delivery recipient.
// Shared locks prevent a pending demotion or removal from being overtaken by
// insertion of a new subscription with obsolete permissions.
func subscriberRole(c *gin.Context, q database.DBTX, scope identity.Scope, userID uuid.UUID) (identity.Role, bool, error) {
	var role identity.Role
	var guestCanViewAll bool
	err := q.QueryRowContext(c.Request.Context(), `SELECT CASE WHEN wm.role IN(5,20) THEN wm.role ELSE pm.role END,p.guest_can_view_all FROM workspace_members wm JOIN project_members pm ON pm.workspace_id=wm.workspace_id AND pm.user_id=wm.user_id JOIN users u ON u.id=wm.user_id JOIN projects p ON p.id=pm.project_id AND p.workspace_id=wm.workspace_id WHERE wm.workspace_id=$1 AND pm.project_id=$2 AND wm.user_id=$3 AND wm.is_active AND pm.is_active AND u.is_active AND wm.deleted_at IS NULL AND pm.deleted_at IS NULL AND u.deleted_at IS NULL AND p.deleted_at IS NULL FOR SHARE OF wm,pm,u,p`, scope.WorkspaceID, scope.ProjectID, userID).Scan(&role, &guestCanViewAll)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, invalid("Subscriber must be an active member of this project")
	}
	return role, guestCanViewAll, err
}

func (h *handler) manageSubscriber(c *gin.Context, remove bool) {
	scope, id, _, err := h.issueScope(c, identity.Guest)
	if err != nil {
		respondError(c, err)
		return
	}
	target := scope.Actor.UserID
	if c.Param("userID") != "" {
		target, err = httpapi.UUIDParam(c, "userID")
		if err != nil {
			respondError(c, err)
			return
		}
	} else if !remove && c.Request.Body != nil && c.Request.ContentLength != 0 {
		var input struct {
			UserID *uuid.UUID `json:"user_id"`
		}
		if err = bindInput(c, &input); err != nil {
			respondError(c, err)
			return
		}
		if input.UserID != nil {
			if *input.UserID == uuid.Nil {
				respondError(c, invalid("Invalid subscriber identifier"))
				return
			}
			target = *input.UserID
		}
	}
	if target != scope.Actor.UserID && scope.Role < identity.Member {
		respondError(c, httpapi.NewError(403, "forbidden", "Only project members can manage other subscribers"))
		return
	}
	err = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		minimum := identity.Guest
		if target != scope.Actor.UserID {
			minimum = identity.Member
		}
		current, err := (&identity.SQLPolicy{DB: q}).Project(c.Request.Context(), scope.Actor, scope.WorkspaceID, scope.ProjectID, minimum)
		if err != nil {
			return err
		}
		item, err := loadIssue(c, q, current, id, true)
		if err != nil {
			return err
		}
		if remove {
			_, err = q.ExecContext(c.Request.Context(), "UPDATE work_item_subscribers SET deleted_at=now(),updated_at=now() WHERE work_item_id=$1 AND workspace_id=$2 AND project_id=$3 AND user_id=$4 AND deleted_at IS NULL", id, scope.WorkspaceID, scope.ProjectID, target)
			return err
		}
		role, guestCanViewAll, err := subscriberRole(c, q, current, target)
		if err != nil {
			return err
		}
		creator, err := uuid.Parse(item["created_by"].(string))
		if err != nil {
			return err
		}
		if !subscriberVisible(role, creator, target, guestCanViewAll) {
			return invalid("This guest cannot receive updates for a work item created by someone else")
		}
		_, err = q.ExecContext(c.Request.Context(), `INSERT INTO work_item_subscribers(id,workspace_id,project_id,work_item_id,user_id) SELECT $1,$2,$3,$4,$5 WHERE NOT EXISTS(SELECT 1 FROM work_item_subscribers WHERE work_item_id=$4 AND user_id=$5 AND deleted_at IS NULL)`, uuid.New(), scope.WorkspaceID, scope.ProjectID, id, target)
		return err
	})
	if err != nil {
		respondError(c, err)
		return
	}
	if remove {
		c.Status(http.StatusNoContent)
		return
	}
	httpapi.JSON(c, http.StatusOK, gin.H{"subscribed": true, "user_id": target})
}
