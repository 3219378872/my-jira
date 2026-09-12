package scenarios

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
	"my-jira/apps/api/internal/workitems"
)

func scope(c *gin.Context, q database.DBTX, minimum identity.Role) (identity.Scope, error) {
	actor, err := httpapi.Actor(c)
	if err != nil {
		return identity.Scope{}, err
	}
	wid, err := httpapi.UUIDParam(c, "workspaceID")
	if err != nil {
		return identity.Scope{}, err
	}
	pid, err := httpapi.UUIDParam(c, "projectID")
	if err != nil {
		return identity.Scope{}, err
	}
	auth, err := data.PageProjectScope(c.Request.Context(), q, actor, wid, pid, minimum)
	if err != nil {
		return identity.Scope{}, err
	}
	if err := workitems.RequireRequirements(c.Request.Context(), q, wid, pid); err != nil {
		return identity.Scope{}, err
	}
	return auth, nil
}

func loadStory(ctx context.Context, q database.DBTX, auth identity.Scope, id uuid.UUID) (StoryContext, error) {
	var story StoryContext
	err := q.QueryRowContext(ctx, `SELECT w.id,w.name,w.state_id,st.name,st.group_name,w.version
 FROM work_items w JOIN states st ON st.id=w.state_id AND st.project_id=w.project_id AND st.workspace_id=w.workspace_id
 WHERE w.id=$1 AND w.workspace_id=$2 AND w.project_id=$3 AND w.deleted_at IS NULL AND w.archived_at IS NULL
 AND NOT w.is_draft AND w.requirement_type='story' AND ($4 OR w.created_by=$5) FOR SHARE OF w,st`,
		id, auth.WorkspaceID, auth.ProjectID, auth.Role >= identity.Member || auth.GuestCanViewAll, auth.Actor.UserID).
		Scan(&story.ID, &story.Name, &story.StateID, &story.StateName, &story.StateGroup, &story.Version)
	return story, err
}

// lockSources authorizes every retained source using its current visibility.
// Locks serialize a privacy or membership change with the entire operation,
// including reading historical snapshots and producing exports.
func lockSources(ctx context.Context, q database.DBTX, auth identity.Scope, pages []uuid.UUID) error {
	ordered := append([]uuid.UUID{}, pages...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].String() < ordered[j].String() })
	for _, id := range ordered {
		var pid uuid.NullUUID
		if err := q.QueryRowContext(ctx, `SELECT project_id FROM pages WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL AND archived_at IS NULL FOR SHARE`, id, auth.WorkspaceID).Scan(&pid); err != nil {
			return err
		}
		if _, err := data.PageProjectScope(ctx, q, auth.Actor, auth.WorkspaceID, pid.UUID, identity.Guest); err != nil {
			return err
		}
		var visible bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pages p WHERE p.id=$1 AND p.archived_at IS NULL AND `+data.VisiblePage("p", "$2", "$3")+`)`, id, auth.WorkspaceID, auth.Actor.UserID).Scan(&visible); err != nil {
			return err
		}
		if !visible {
			return data.Missing()
		}
	}
	return nil
}

func sourceIDs(ctx context.Context, q database.DBTX, id uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.QueryContext(ctx, `SELECT page_id FROM business_scenario_sources WHERE scenario_id=$1 ORDER BY page_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// The project scope is always separately locked and checked. This predicate is
// also used to filter before fetching content: hidden source prose never leaves
// PostgreSQL in collection, relationship or aggregate-diagram paths.
func visiblePredicate() string {
	return `s.workspace_id=$1 AND s.project_id=$2 AND s.deleted_at IS NULL
 AND EXISTS(SELECT 1 FROM work_items w WHERE w.id=s.story_id AND w.workspace_id=s.workspace_id AND w.project_id=s.project_id
   AND w.requirement_type='story' AND w.deleted_at IS NULL AND w.archived_at IS NULL AND NOT w.is_draft AND ($4 OR w.created_by=$3))
 AND NOT EXISTS(SELECT 1 FROM business_scenario_sources src LEFT JOIN pages p ON p.id=src.page_id
   WHERE src.scenario_id=s.id AND (p.id IS NULL OR p.archived_at IS NOT NULL OR NOT (` + data.VisiblePage("p", "$1", "$3") + `)))`
}

func visibleIDs(ctx context.Context, q database.DBTX, auth identity.Scope) ([]uuid.UUID, error) {
	rows, err := q.QueryContext(ctx, `SELECT s.id FROM business_scenarios s WHERE `+visiblePredicate()+` ORDER BY s.created_at,s.id`, auth.WorkspaceID, auth.ProjectID, auth.Actor.UserID, auth.Role >= identity.Member || auth.GuestCanViewAll)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func load(ctx context.Context, q database.DBTX, auth identity.Scope, id uuid.UUID, write bool) (Scenario, error) {
	var result Scenario
	var storyID uuid.UUID
	if err := q.QueryRowContext(ctx, `SELECT story_id FROM business_scenarios WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL`, id, auth.WorkspaceID, auth.ProjectID).Scan(&storyID); err != nil {
		return result, err
	}
	story, err := loadStory(ctx, q, auth, storyID)
	if err != nil {
		return result, err
	}
	pages, err := sourceIDs(ctx, q, id)
	if err != nil {
		return result, err
	}
	if err := lockSources(ctx, q, auth, pages); err != nil {
		return result, err
	}
	lock := " FOR SHARE"
	if write {
		lock = " FOR UPDATE"
	}
	var body []byte
	if err := q.QueryRowContext(ctx, `SELECT id,workspace_id,project_id,body,version,review_needed,created_at,updated_at
 FROM business_scenarios WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL`+lock,
		id, auth.WorkspaceID, auth.ProjectID).Scan(&result.ID, &result.WorkspaceID, &result.ProjectID, &body, &result.Version, &result.ReviewNeeded, &result.CreatedAt, &result.UpdatedAt); err != nil {
		return result, err
	}
	if err := json.Unmarshal(body, &result.Content); err != nil {
		return result, err
	}
	// An edit can append a new source before we acquire the scenario row lock.
	// Rechecking the retained set after that lock closes that visibility race.
	pages, err = sourceIDs(ctx, q, id)
	if err != nil {
		return result, err
	}
	if err := lockSources(ctx, q, auth, pages); err != nil {
		return result, err
	}
	minimum := identity.Guest
	if write {
		minimum = identity.Member
	}
	if err := currentCredentials(ctx, q, auth, minimum); err != nil {
		return result, err
	}
	result.Story, result.ProvenanceSourcePageIDs = story, pages
	return result, nil
}

// Explicit membership/source locks above define scenario visibility. The
// shared command guard additionally rechecks session/token revocation and
// expiry after domain waits, holding those credentials until commit. Recheck
// the project rollout gate while the same project lock remains held.
func currentCredentials(ctx context.Context, q database.DBTX, auth identity.Scope, minimum identity.Role) error {
	if _, err := workitems.CurrentScope(ctx, q, auth.Actor, auth.WorkspaceID, auth.ProjectID, minimum); err != nil {
		return err
	}
	return workitems.RequireRequirements(ctx, q, auth.WorkspaceID, auth.ProjectID)
}

func loadHistorical(ctx context.Context, q database.DBTX, current Scenario, version int64) (Scenario, error) {
	if version == 0 || version == current.Version {
		return current, nil
	}
	var body []byte
	if err := q.QueryRowContext(ctx, `SELECT snapshot->'body',(snapshot->>'version')::bigint,(snapshot->>'review_needed')::boolean,
 (snapshot->>'created_at')::timestamptz,(snapshot->>'updated_at')::timestamptz FROM business_scenario_versions WHERE scenario_id=$1 AND version=$2`,
		current.ID, version).Scan(&body, &current.Version, &current.ReviewNeeded, &current.CreatedAt, &current.UpdatedAt); err != nil {
		return current, err
	}
	err := json.Unmarshal(body, &current.Content)
	return current, err
}

func relationshipsVisible(ctx context.Context, q database.DBTX, auth identity.Scope, value *Scenario) error {
	ids, err := visibleIDs(ctx, q, auth)
	if err != nil {
		return err
	}
	visible := map[uuid.UUID]bool{value.ID: true}
	for _, id := range ids {
		visible[id] = true
	}
	for _, participant := range value.Participants {
		visible[participant.ID] = true
	}
	filtered := []Relationship{}
	for _, relation := range value.Relationships {
		if visible[relation.FromID] && visible[relation.ToID] {
			filtered = append(filtered, relation)
		}
	}
	value.Relationships = filtered
	return nil
}

func validateTargets(ctx context.Context, q database.DBTX, auth identity.Scope, value Content, id uuid.UUID) error {
	known := map[uuid.UUID]bool{id: true}
	for _, participant := range value.Participants {
		known[participant.ID] = true
	}
	for _, relation := range value.Relationships {
		for _, endpoint := range []uuid.UUID{relation.FromID, relation.ToID} {
			if !known[endpoint] {
				if _, err := load(ctx, q, auth, endpoint, false); err != nil {
					return data.Invalid("A relationship target is not a currently visible scenario in this project")
				}
				known[endpoint] = true
			}
		}
	}
	return nil
}

func addSources(ctx context.Context, q database.DBTX, auth identity.Scope, scenarioID uuid.UUID, ids []uuid.UUID) error {
	for _, id := range ids {
		if _, err := q.ExecContext(ctx, `INSERT INTO business_scenario_sources(scenario_id,workspace_id,page_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, scenarioID, auth.WorkspaceID, id); err != nil {
			return err
		}
	}
	return nil
}

func stampSources(ctx context.Context, q database.DBTX, value *Content, storyVersion int64, provenance []uuid.UUID, reviewed bool) error {
	versions := make(map[string]int64, len(value.SourcePageVersions)+len(provenance))
	for id, version := range value.SourcePageVersions {
		versions[id] = version
	}
	if reviewed || value.SourceStoryVersion == 0 {
		value.SourceStoryVersion = storyVersion
	}
	for _, id := range provenance {
		if _, exists := versions[id.String()]; exists && !reviewed {
			continue
		}
		var version int64
		if err := q.QueryRowContext(ctx, `SELECT version FROM pages WHERE id=$1`, id).Scan(&version); err != nil {
			return err
		}
		versions[id.String()] = version
	}
	value.SourcePageVersions = versions
	return nil
}
