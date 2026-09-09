package planning

import (
	"github.com/gin-gonic/gin"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
)

// Date availability is an advisory check. Overlapping cycles remain expressible
// through the API, matching the reference's separate date-check workflow.
func (s *service) checkCycleDates(c *gin.Context) {
	scope, err := s.scope(c, identity.Member)
	if err != nil {
		data.Fail(c, err)
		return
	}
	input, err := data.Bind(c, "start_date", "end_date", "exclude_cycle_id")
	if err != nil {
		data.Fail(c, err)
		return
	}
	start, err := input.Date("start_date")
	if err != nil || start == nil {
		data.Fail(c, data.Invalid("start_date is required"))
		return
	}
	end, err := input.Date("end_date")
	if err != nil || end == nil || start.(string) > end.(string) {
		data.Fail(c, data.Invalid("end_date must be on or after start_date"))
		return
	}
	exclude, err := input.UUID("exclude_cycle_id", true)
	if err != nil {
		data.Fail(c, err)
		return
	}
	if exclude != nil {
		var exists bool
		err = s.deps.DB.SQL.QueryRowContext(c.Request.Context(), "SELECT EXISTS(SELECT 1 FROM cycles WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL)", exclude, scope.WorkspaceID, scope.ProjectID).Scan(&exists)
		if err != nil {
			data.Fail(c, err)
			return
		}
		if !exists {
			data.Fail(c, data.Missing())
			return
		}
	}
	conflicts, err := data.Many(c, s.deps.DB.SQL, `SELECT jsonb_build_object('id',id,'name',name,'start_date',start_date,'end_date',end_date,'archived_at',archived_at) FROM cycles WHERE workspace_id=$1 AND project_id=$2 AND deleted_at IS NULL AND ($5::uuid IS NULL OR id<>$5) AND start_date<=$4::date AND end_date>=$3::date ORDER BY start_date,id`, scope.WorkspaceID, scope.ProjectID, start, end, exclude)
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, gin.H{"available": len(conflicts) == 0, "conflicts": conflicts})
}
