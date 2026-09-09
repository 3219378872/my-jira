package workitems

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"my-jira/apps/api/internal/platform/httpapi"
)

type issueGroup struct {
	Key        string              `json:"key"`
	Label      string              `json:"label"`
	Total      int64               `json:"total"`
	Items      []json.RawMessage   `json:"items,omitempty"`
	Pagination *httpapi.Pagination `json:"pagination,omitempty"`
	Groups     []*issueGroup       `json:"groups,omitempty"`
}

var groupFields = map[string]string{
	"state_id": "state_id", "state_group": "state_detail,group", "priority": "priority", "project_id": "project_id",
	"assignee_id": "assignee_ids", "label_id": "label_ids", "cycle_id": "cycle_id", "module_id": "module_ids",
	"created_by": "created_by", "estimate_point_id": "estimate_point_id",
}

var priorityGroupOrder = []string{"urgent", "high", "medium", "low", "none"}
var stateGroupOrder = []string{"backlog", "unstarted", "started", "completed", "cancelled"}

type groupMetadata struct {
	label, projectID string
	position         float64
	createdAt        time.Time
	available        bool
}

type groupCatalog map[string]groupMetadata

func groupArray(field string) string {
	property := groupFields[field]
	if strings.HasSuffix(property, "_ids") {
		return "CASE WHEN jsonb_array_length(item->'" + property + "')=0 THEN '[\"none\"]'::jsonb ELSE item->'" + property + "' END"
	}
	return "jsonb_build_array(COALESCE(item#>>'{" + property + "}','none'))"
}

func pageOffset(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	value, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, invalid("Invalid cursor")
	}
	offset, err := strconv.Atoi(string(value))
	if err != nil || offset < 0 || offset > 10000000 {
		return 0, invalid("Invalid cursor")
	}
	return offset, nil
}

func pagination(total int64, offset, count int) *httpapi.Pagination {
	p := &httpapi.Pagination{Total: total, HasMore: int64(offset+count) < total}
	if p.HasMore {
		next := base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset + count)))
		p.NextCursor = &next
	}
	return p
}

func issueOrdering(c *gin.Context) (string, error) {
	orders := map[string]string{
		"position": "w.position", "created_at": "w.created_at", "updated_at": "w.updated_at", "name": "lower(w.name)",
		"sequence_id": "w.sequence_id", "start_date": "w.start_date", "target_date": "w.target_date", "completed_at": "w.completed_at",
		"estimate": "w.estimate", "state": "(SELECT position FROM states WHERE id=w.state_id)",
		"priority": "CASE w.priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 WHEN 'low' THEN 3 ELSE 4 END",
	}
	parts := strings.Split(c.DefaultQuery("order_by", "position"), ",")
	if len(parts) > 4 {
		return "", invalid("At most four ordering fields are supported")
	}
	result := []string{}
	for _, field := range parts {
		direction := " ASC"
		if strings.HasPrefix(field, "-") {
			direction = " DESC"
			field = strings.TrimPrefix(field, "-")
		}
		expression, ok := orders[field]
		if !ok {
			return "", invalid("Invalid order_by")
		}
		result = append(result, expression+direction+" NULLS LAST")
	}
	return strings.Join(result, ",") + ",w.id", nil
}

func (h *handler) groupedList(c *gin.Context, where string, args []any, limit, offset int, ordering string) {
	group, sub := c.Query("group_by"), c.Query("sub_group_by")
	if _, ok := groupFields[group]; !ok {
		respondError(c, invalid("Invalid group_by"))
		return
	}
	if sub != "" {
		if _, ok := groupFields[sub]; !ok || sub == group {
			respondError(c, invalid("sub_group_by must be a different supported field"))
			return
		}
	}
	actor, err := httpapi.Actor(c)
	if err != nil {
		respondError(c, err)
		return
	}
	b := filterBuilder{args: append([]any{}, args...), actor: actor.UserID}
	for _, selection := range []struct{ field, value string }{{group, c.Query("group_key")}, {sub, c.Query("sub_group_key")}} {
		if selection.value == "" {
			continue
		}
		if selection.field == "" {
			respondError(c, invalid("A group key requires its grouping field"))
			return
		}
		var raw json.RawMessage
		if selection.value == "none" && selection.field != "priority" {
			raw = []byte("null")
		} else {
			raw, _ = json.Marshal(selection.value)
		}
		part, e := b.node(filterNode{Field: selection.field, Value: raw}, 0)
		if e != nil {
			respondError(c, e)
			return
		}
		where += " AND (" + part + ")"
	}
	args = b.args
	n := len(args)
	countArgs := append(append([]any{}, args...), actor.UserID)
	cte := "WITH filtered(item) AS (" + issueSelectFor(fmt.Sprintf("$%d", n+1)) + where + ") "
	from := "FROM filtered CROSS JOIN LATERAL jsonb_array_elements_text(" + groupArray(group) + ") g(key) "
	subexpr := "''"
	if sub != "" {
		from += "CROSS JOIN LATERAL jsonb_array_elements_text(" + groupArray(sub) + ") sg(key) "
		subexpr = "sg.key"
	}
	groupOrder := "g.key"
	if sub != "" {
		groupOrder += ",sg.key"
	}
	rows, err := h.deps.DB.SQL.QueryContext(c.Request.Context(), cte+"SELECT g.key,"+subexpr+",count(DISTINCT item->>'id') "+from+"GROUP BY "+groupOrder+" ORDER BY "+groupOrder, countArgs...)
	if err != nil {
		respondError(c, err)
		return
	}
	type pair struct {
		key, sub string
		count    int64
	}
	pairs := []pair{}
	for rows.Next() {
		var p pair
		if err = rows.Scan(&p.key, &p.sub, &p.count); err != nil {
			rows.Close()
			respondError(c, err)
			return
		}
		pairs = append(pairs, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		respondError(c, err)
		return
	}
	groups := []*issueGroup{}
	byKey := map[string]*issueGroup{}
	for _, p := range pairs {
		g := byKey[p.key]
		if g == nil {
			g = &issueGroup{Key: p.key, Label: p.key}
			groups = append(groups, g)
			byKey[p.key] = g
		}
		if sub == "" {
			g.Total = p.count
		} else {
			g.Groups = append(g.Groups, &issueGroup{Key: p.sub, Label: p.sub, Total: p.count})
		}
	}
	showEmpty := c.Query("show_empty") == "true"
	keys := make([]string, 0, len(groups))
	for _, g := range groups {
		keys = append(keys, g.Key)
	}
	emptyProjects := []string{}
	if showEmpty && group == "state_id" && c.Param("projectID") != "" {
		emptyProjects = append(emptyProjects, c.Param("projectID"))
	}
	catalog, err := h.groupCatalog(c, group, keys, emptyProjects)
	if err != nil {
		respondError(c, err)
		return
	}
	if showEmpty {
		addEmptyGroups(group, c.Query("group_key"), c.Param("projectID"), catalog, &groups, byKey)
	}
	// Resolve labels and business positions together, before slicing group pages.
	// Neither label lookup nor sorting adds one query per group or work item.
	sortIssueGroups(group, groups, catalog)
	if sub != "" {
		subKeys, subProjects := []string{}, []string{}
		if showEmpty && sub == "state_id" && c.Param("projectID") != "" {
			subProjects = append(subProjects, c.Param("projectID"))
		}
		for _, g := range groups {
			for _, child := range g.Groups {
				subKeys = append(subKeys, child.Key)
			}
			if showEmpty && sub == "state_id" && group == "project_id" && g.Key != "none" {
				// Only projects already present in the authorized filtered result
				// contribute empty states; never enumerate the whole workspace.
				subProjects = append(subProjects, g.Key)
			}
		}
		subCatalog, err := h.groupCatalog(c, sub, subKeys, subProjects)
		if err != nil {
			respondError(c, err)
			return
		}
		for _, g := range groups {
			if showEmpty {
				seen := map[string]*issueGroup{}
				for _, child := range g.Groups {
					seen[child.Key] = child
				}
				projectID := c.Param("projectID")
				if group == "project_id" {
					projectID = g.Key
				}
				addEmptyGroups(sub, c.Query("sub_group_key"), projectID, subCatalog, &g.Groups, seen)
			}
			sortIssueGroups(sub, g.Groups, subCatalog)
		}
	}
	groupOffset, e := pageOffset(c.Query("group_cursor"))
	if e != nil {
		respondError(c, e)
		return
	}
	groupLimit := 100
	if text := c.Query("group_limit"); text != "" {
		groupLimit, e = strconv.Atoi(text)
		if e != nil || groupLimit < 1 || groupLimit > 200 {
			respondError(c, invalid("group_limit must be between 1 and 200"))
			return
		}
	}
	totalGroups := len(groups)
	if groupOffset > totalGroups {
		groupOffset = totalGroups
	}
	end := groupOffset + groupLimit
	if end > totalGroups {
		end = totalGroups
	}
	groups = groups[groupOffset:end]
	for _, g := range groups {
		if sub == "" {
			if g.Total == 0 {
				g.Pagination = pagination(0, offset, 0)
				continue
			}
			if err := h.fillGroup(c, g, where, args, group, g.Key, "", "", limit, offset, ordering); err != nil {
				respondError(c, err)
				return
			}
		} else {
			// The parent total counts each issue once even when it has several
			// assignees or labels in the subordinate grouping.
			hasItems := false
			for _, child := range g.Groups {
				hasItems = hasItems || child.Total > 0
			}
			if hasItems {
				if err := h.fillGroup(c, g, where, args, group, g.Key, "", "", 0, 0, ordering); err != nil {
					respondError(c, err)
					return
				}
			}
			for _, child := range g.Groups {
				if child.Total == 0 {
					child.Pagination = pagination(0, offset, 0)
					continue
				}
				if err := h.fillGroup(c, child, where, args, group, g.Key, sub, child.Key, limit, offset, ordering); err != nil {
					respondError(c, err)
					return
				}
			}
		}
	}
	var total int64
	err = h.deps.DB.SQL.QueryRowContext(c.Request.Context(), "SELECT count(*) FROM work_items w "+where, args...).Scan(&total)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": groups, "group_by": group, "sub_group_by": sub, "total_items": total, "pagination": pagination(int64(totalGroups), groupOffset, len(groups))})
}

func (h *handler) fillGroup(c *gin.Context, g *issueGroup, where string, args []any, group, key, sub, subkey string, limit, offset int, ordering string) error {
	actor, err := httpapi.Actor(c)
	if err != nil {
		return err
	}
	b := filterBuilder{args: append([]any{}, args...), actor: actor.UserID}
	for _, entry := range []struct{ field, key string }{{group, key}, {sub, subkey}} {
		if entry.field == "" {
			continue
		}
		var raw json.RawMessage
		if entry.key == "none" && entry.field != "priority" {
			raw = []byte("null")
		} else {
			raw, _ = json.Marshal(entry.key)
		}
		condition, e := b.node(filterNode{Field: entry.field, Value: raw}, 0)
		if e != nil {
			return e
		}
		where += " AND (" + condition + ")"
	}
	if err = h.deps.DB.SQL.QueryRowContext(c.Request.Context(), "SELECT count(*) FROM work_items w "+where, b.args...).Scan(&g.Total); err != nil {
		return err
	}
	if limit == 0 {
		return nil
	}
	n := len(b.args)
	b.args = append(b.args, actor.UserID, limit, offset)
	items, err := execJSON(c, h.deps.DB.SQL, issueSelectFor(fmt.Sprintf("$%d", n+1))+where+" ORDER BY "+ordering+fmt.Sprintf(" LIMIT $%d OFFSET $%d", n+2, n+3), b.args...)
	if err != nil {
		return err
	}
	g.Items = items
	g.Pagination = pagination(g.Total, offset, len(items))
	return nil
}

func (h *handler) groupCatalog(c *gin.Context, field string, keys, emptyStateProjects []string) (groupCatalog, error) {
	catalog := groupCatalog{}
	if field == "priority" || field == "state_group" {
		order := priorityGroupOrder
		if field == "state_group" {
			order = stateGroupOrder
		}
		for position, key := range order {
			catalog[key] = groupMetadata{label: key, position: float64(position), available: true}
		}
		return catalog, nil
	}
	unique := func(values []string) []string {
		result := []string{}
		seen := map[string]bool{}
		for _, key := range values {
			if key != "" && key != "none" && !seen[key] {
				seen[key] = true
				result = append(result, key)
			}
		}
		return result
	}
	keys, emptyStateProjects = unique(keys), unique(emptyStateProjects)
	if len(keys) == 0 && len(emptyStateProjects) == 0 {
		return catalog, nil
	}
	table, column, position, project := "", "name", "0::double precision", "''::text"
	switch field {
	case "state_id":
		table, position, project = "states", "position", "project_id::text"
	case "project_id":
		table = "projects"
	case "assignee_id", "created_by":
		table = "users"
		column = "display_name"
	case "label_id":
		table = "labels"
	case "cycle_id":
		table, position, project = "cycles", "position", "project_id::text"
	case "module_id":
		table, position, project = "modules", "position", "project_id::text"
	case "estimate_point_id":
		table, column, position, project = "estimate_points", "label", "position", "project_id::text"
	}
	if table == "" {
		return catalog, nil
	}
	args := []any{keys}
	where := "id::text=ANY($1::text[])"
	if field == "state_id" {
		args = append(args, emptyStateProjects, c.Param("workspaceID"))
		where = "workspace_id=$3 AND (" + where + " OR (deleted_at IS NULL AND project_id::text=ANY($2::text[])))"
	}
	rows, err := h.deps.DB.SQL.QueryContext(c.Request.Context(), "SELECT id::text,"+column+","+position+",created_at,"+project+",deleted_at IS NULL FROM "+table+" WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var value groupMetadata
		if err := rows.Scan(&key, &value.label, &value.position, &value.createdAt, &value.projectID, &value.available); err != nil {
			return nil, err
		}
		catalog[key] = value
	}
	return catalog, rows.Err()
}

func sortIssueGroups(field string, groups []*issueGroup, catalog groupCatalog) {
	for _, group := range groups {
		if metadata, ok := catalog[group.Key]; ok {
			group.Label = metadata.label
		}
		if group.Key == "none" {
			group.Label = "Unassigned"
			if field == "priority" {
				group.Label = "No priority"
			}
		}
	}
	positioned := field == "priority" || field == "state_group" || field == "state_id" || field == "cycle_id" || field == "module_id" || field == "estimate_point_id"
	sort.SliceStable(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if a.Key == "none" || b.Key == "none" {
			return a.Key != "none" && b.Key == "none"
		}
		if positioned {
			left, leftOK := catalog[a.Key]
			right, rightOK := catalog[b.Key]
			if leftOK != rightOK {
				return leftOK
			}
			if left.position != right.position {
				return left.position < right.position
			}
			if (field == "cycle_id" || field == "module_id") && !left.createdAt.Equal(right.createdAt) {
				return left.createdAt.Before(right.createdAt)
			}
		} else if left, right := strings.ToLower(a.Label), strings.ToLower(b.Label); left != right {
			return left < right
		}
		return a.Key < b.Key
	})
}

func addEmptyGroups(field, chosen, projectID string, catalog groupCatalog, groups *[]*issueGroup, seen map[string]*issueGroup) {
	keys := []string{}
	switch field {
	case "priority":
		keys = priorityGroupOrder
	case "state_group":
		keys = stateGroupOrder
	case "state_id":
		for key, value := range catalog {
			if projectID != "" && value.available && value.projectID == projectID {
				keys = append(keys, key)
			}
		}
	default:
		keys = []string{"none"}
	}
	if chosen != "" {
		allowed := []string{}
		for _, key := range keys {
			if key == chosen {
				allowed = append(allowed, key)
			}
		}
		keys = allowed
	}
	for _, key := range keys {
		if seen[key] == nil {
			g := &issueGroup{Key: key, Label: key}
			*groups = append(*groups, g)
			seen[key] = g
		}
	}
}
