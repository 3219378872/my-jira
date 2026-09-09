package platform

import (
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/jobs"
)

type Dependencies struct {
	DB     *database.Database
	Policy identity.Policy
	Jobs   jobs.Publisher
}
