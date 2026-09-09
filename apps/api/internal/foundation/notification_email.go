package foundation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
)

type resolvedEmail struct{ To, Subject, Text string }

// notificationEmail deliberately resolves content at delivery time. Jobs carry
// an identifier so delayed delivery cannot rely on revoked access, old email
// addresses, or a title from a formerly accessible entity.
func notificationEmail(ctx context.Context, deps platform.Dependencies, id uuid.UUID) (*resolvedEmail, error) {
	var workspaceID, userID uuid.UUID
	var projectID, entityID uuid.NullUUID
	var kind, email, workspaceSlug string
	var raw []byte
	err := deps.DB.SQL.QueryRowContext(ctx, `SELECT n.workspace_id,n.project_id,n.user_id,n.entity_type,n.entity_id,n.data,u.email,w.slug FROM notifications n JOIN users u ON u.id=n.user_id JOIN workspaces w ON w.id=n.workspace_id WHERE n.id=$1 AND n.deleted_at IS NULL AND (n.actor_id IS NULL OR n.actor_id<>n.user_id) AND u.is_active AND u.deleted_at IS NULL AND w.deleted_at IS NULL`, id).Scan(&workspaceID, &projectID, &userID, &kind, &entityID, &raw, &email, &workspaceSlug)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !entityID.Valid || entityID.UUID == uuid.Nil {
		return nil, nil
	}
	var metadata struct {
		Reasons []string `json:"reasons"`
	}
	if err = json.Unmarshal(raw, &metadata); err != nil {
		return nil, err
	}
	var preferenceRaw []byte
	err = deps.DB.SQL.QueryRowContext(ctx, `SELECT value FROM preferences WHERE workspace_id=$1 AND project_id IS NULL AND user_id=$2 AND scope='notifications' AND deleted_at IS NULL`, workspaceID, userID).Scan(&preferenceRaw)
	preferences := map[string]any{}
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err == nil {
		if err = json.Unmarshal(preferenceRaw, &preferences); err != nil {
			return nil, err
		}
	}
	enabled := func(key string) bool {
		value, exists := preferences[key]
		return !exists || value == true
	}
	if !enabled("email") {
		return nil, nil
	}
	interested := false
	for _, reason := range metadata.Reasons {
		switch reason {
		case "mentions", "assigned", "subscribed", "created":
			interested = interested || enabled(reason)
		}
	}
	if !interested {
		return nil, nil
	}
	actor := identity.Actor{UserID: userID}
	var scope identity.Scope
	if projectID.Valid {
		scope, err = deps.Policy.Project(ctx, actor, workspaceID, projectID.UUID, identity.Guest)
	} else {
		scope, err = deps.Policy.Workspace(ctx, actor, workspaceID, identity.Guest)
	}
	if err != nil {
		if unavailableLocation(err) {
			return nil, nil
		}
		return nil, err
	}
	var name, reference string
	path := "/w/" + workspaceSlug
	switch kind {
	case "issue", "work_item":
		if !projectID.Valid {
			return nil, nil
		}
		var creator uuid.UUID
		err = deps.DB.SQL.QueryRowContext(ctx, `SELECT wi.name,p.identifier||'-'||wi.sequence_id::text,wi.created_by FROM work_items wi JOIN projects p ON p.id=wi.project_id WHERE wi.id=$1 AND wi.workspace_id=$2 AND wi.project_id=$3 AND wi.deleted_at IS NULL AND p.deleted_at IS NULL AND p.archived_at IS NULL`, entityID.UUID, workspaceID, projectID.UUID).Scan(&name, &reference, &creator)
		if err == nil && scope.Role < identity.Member && !scope.GuestCanViewAll && creator != userID {
			return nil, nil
		}
		path += "/projects/" + projectID.UUID.String() + "/issues/" + entityID.UUID.String()
	case "page", "view":
		table, route := "pages", "pages"
		if kind == "view" {
			table, route = "saved_views", "views"
		}
		// Table names come exclusively from the two fixed branches above.
		visibility := `(NOT entity.is_private OR entity.owner_id=$4)`
		if kind == "page" {
			visibility = data.VisiblePage("entity", "$2", "$4")
		}
		err = deps.DB.SQL.QueryRowContext(ctx, `SELECT entity.name FROM `+table+` entity WHERE entity.id=$1 AND entity.workspace_id=$2 AND entity.project_id IS NOT DISTINCT FROM $3::uuid AND entity.deleted_at IS NULL AND `+visibility, entityID.UUID, workspaceID, projectID, userID).Scan(&name)
		if projectID.Valid {
			path += "/projects/" + projectID.UUID.String()
		}
		path += "/" + route + "/" + entityID.UUID.String()
	case "cycle", "module":
		if !projectID.Valid {
			return nil, nil
		}
		table := "cycles"
		if kind == "module" {
			table = "modules"
		}
		err = deps.DB.SQL.QueryRowContext(ctx, `SELECT name FROM `+table+` WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL`, entityID.UUID, workspaceID, projectID.UUID).Scan(&name)
		path += "/projects/" + projectID.UUID.String() + "/" + table + "/" + entityID.UUID.String()
	default:
		// Unknown entity kinds cannot safely use a stored notification title.
		return nil, nil
	}
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(reference + " " + name)
	subject := "[my-jira] " + strings.NewReplacer("\r", " ", "\n", " ").Replace(title)
	text := fmt.Sprintf("There is an update to %s.\n\n", title)
	origin := strings.TrimRight(strings.TrimSpace(strings.Split(os.Getenv("APP_ORIGIN"), ",")[0]), "/")
	if origin != "" {
		text += "View the current details: " + origin + path
	} else {
		text += "Open your my-jira workspace to view the current details."
	}
	return &resolvedEmail{To: email, Subject: subject, Text: text}, nil
}
