package scenarios

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/apperror"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
)

type service struct{ deps platform.Dependencies }

func Register(r *gin.RouterGroup, deps platform.Dependencies) {
	s := &service{deps: deps}
	p := r.Group("/workspaces/:workspaceID/projects/:projectID/scenarios")
	p.GET("", s.list)
	p.POST("", s.create)
	p.GET("/use-case.svg", s.projectUseCases)
	p.GET("/:scenarioID", s.get)
	p.PATCH("/:scenarioID", s.update)
	p.DELETE("/:scenarioID", s.remove)
	p.POST("/:scenarioID/copy", s.copy)
	p.GET("/:scenarioID/versions", s.versions)
	p.GET("/:scenarioID/versions/:versionID", s.get)
	p.GET("/:scenarioID/sequence.svg", s.sequence)
	p.GET("/:scenarioID/use-case.svg", s.useCases)
}

func versionInput(input data.Object) (int64, error) {
	raw, present := input["version"]
	var version int64
	if !present || json.Unmarshal(raw, &version) != nil || version < 1 {
		return 0, data.Invalid("A positive integer version is required")
	}
	return version, nil
}

func requestedVersion(c *gin.Context, required bool) (int64, error) {
	text := c.Param("versionID")
	if text == "" {
		text = c.Query("version")
	}
	if text == "" && !required {
		return 0, nil
	}
	version, err := strconv.ParseInt(text, 10, 64)
	if err != nil || version < 1 {
		return 0, data.Invalid("version must be a positive integer")
	}
	return version, nil
}

func (s *service) list(c *gin.Context) {
	result := []Scenario{}
	err := s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		auth, err := scope(c, q, identity.Guest)
		if err != nil {
			return err
		}
		storyFilter := uuid.Nil
		if c.Query("story_id") != "" {
			storyFilter, err = uuid.Parse(c.Query("story_id"))
			if err != nil || storyFilter == uuid.Nil {
				return data.Invalid("story_id must be a UUID")
			}
		}
		ids, err := visibleIDs(c.Request.Context(), q, auth)
		if err != nil {
			return err
		}
		for _, id := range ids {
			value, err := load(c.Request.Context(), q, auth, id, false)
			if isHidden(err) {
				continue
			}
			if err != nil {
				return err
			}
			if storyFilter != uuid.Nil && value.StoryID != storyFilter {
				continue
			}
			if err := relationshipsVisible(c.Request.Context(), q, auth, &value); err != nil {
				return err
			}
			result = append(result, value)
		}
		return nil
	})
	private(c)
	data.Send(c, result, err)
}

func isHidden(err error) bool {
	var domain *data.Error
	var api *apperror.Error
	return errors.Is(err, sql.ErrNoRows) || (errors.As(err, &domain) && (domain.Status == 404 || domain.Status == 403)) || (errors.As(err, &api) && (api.Status == 404 || api.Status == 403))
}

func (s *service) get(c *gin.Context) {
	var result Scenario
	id, err := httpapi.UUIDParam(c, "scenarioID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	version, err := requestedVersion(c, false)
	if err == nil {
		err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
			auth, err := scope(c, q, identity.Guest)
			if err != nil {
				return err
			}
			result, err = load(c.Request.Context(), q, auth, id, false)
			if err != nil {
				return err
			}
			result, err = loadHistorical(c.Request.Context(), q, result, version)
			if err != nil {
				return err
			}
			return relationshipsVisible(c.Request.Context(), q, auth, &result)
		})
	}
	private(c)
	data.Send(c, result, err)
}

func (s *service) create(c *gin.Context) {
	input, err := data.Bind(c, contentFields...)
	if err != nil {
		data.Fail(c, err)
		return
	}
	value, err := decodeContent(input, nil)
	if err != nil {
		data.Fail(c, err)
		return
	}
	id := uuid.New()
	if err := validateContent(value, id); err != nil {
		data.Fail(c, err)
		return
	}
	var result Scenario
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		auth, err := scope(c, q, identity.Member)
		if err != nil {
			return err
		}
		story, err := loadStory(c.Request.Context(), q, auth, value.StoryID)
		if err != nil {
			return err
		}
		if err := lockSources(c.Request.Context(), q, auth, value.SourcePageIDs); err != nil {
			return err
		}
		if err := validateTargets(c.Request.Context(), q, auth, value, id); err != nil {
			return err
		}
		if err := stampSources(c.Request.Context(), q, &value, story.Version, value.SourcePageIDs, true); err != nil {
			return err
		}
		if err := currentCredentials(c.Request.Context(), q, auth, identity.Member); err != nil {
			return err
		}
		body, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(c.Request.Context(), `INSERT INTO business_scenarios(id,workspace_id,project_id,story_id,body,created_by,updated_by) VALUES($1,$2,$3,$4,$5::jsonb,$6,$6)`, id, auth.WorkspaceID, auth.ProjectID, value.StoryID, string(body), auth.Actor.UserID); err != nil {
			return err
		}
		if err := addSources(c.Request.Context(), q, auth, id, value.SourcePageIDs); err != nil {
			return err
		}
		result, err = load(c.Request.Context(), q, auth, id, false)
		return err
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	private(c)
	httpapi.JSON(c, http.StatusCreated, result)
}

func (s *service) update(c *gin.Context) {
	input, err := data.Bind(c, contentFields...)
	if err != nil {
		data.Fail(c, err)
		return
	}
	version, err := versionInput(input)
	if err != nil {
		data.Fail(c, err)
		return
	}
	id, err := httpapi.UUIDParam(c, "scenarioID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	var result Scenario
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		auth, err := scope(c, q, identity.Member)
		if err != nil {
			return err
		}
		old, err := load(c.Request.Context(), q, auth, id, true)
		if err != nil {
			return err
		}
		if old.Version != version {
			return data.Conflict("The scenario changed; reload its current version")
		}
		value, err := decodeContent(input, &old.Content)
		if err != nil {
			return err
		}
		if value.StoryID != old.StoryID {
			return data.Invalid("The primary Story cannot change; create a separate scenario for another Story")
		}
		if err := validateContent(value, id); err != nil {
			return err
		}
		if err := lockSources(c.Request.Context(), q, auth, value.SourcePageIDs); err != nil {
			return err
		}
		if _, changed := input["relationships"]; changed {
			if err := validateTargets(c.Request.Context(), q, auth, value, id); err != nil {
				return err
			}
		}
		review := old.ReviewNeeded
		if _, present := input["review_needed"]; present {
			review, err = input.Bool("review_needed")
			if err != nil {
				return err
			}
		}
		allSources := append(append([]uuid.UUID{}, old.ProvenanceSourcePageIDs...), value.SourcePageIDs...)
		if err := stampSources(c.Request.Context(), q, &value, old.Story.Version, allSources, !review); err != nil {
			return err
		}
		if err := currentCredentials(c.Request.Context(), q, auth, identity.Member); err != nil {
			return err
		}
		body, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if err := addSources(c.Request.Context(), q, auth, id, value.SourcePageIDs); err != nil {
			return err
		}
		if _, err := q.ExecContext(c.Request.Context(), `UPDATE business_scenarios SET body=$2::jsonb,version=version+1,review_needed=$3,updated_by=$4,updated_at=now() WHERE id=$1`, id, string(body), review, auth.Actor.UserID); err != nil {
			return err
		}
		result, err = load(c.Request.Context(), q, auth, id, false)
		if err != nil {
			return err
		}
		return relationshipsVisible(c.Request.Context(), q, auth, &result)
	})
	private(c)
	data.Send(c, result, err)
}

func (s *service) remove(c *gin.Context) {
	id, err := httpapi.UUIDParam(c, "scenarioID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	version, err := requestedVersion(c, true)
	if err == nil {
		err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
			auth, err := scope(c, q, identity.Member)
			if err != nil {
				return err
			}
			old, err := load(c.Request.Context(), q, auth, id, true)
			if err != nil {
				return err
			}
			if old.Version != version {
				return data.Conflict("The scenario changed; reload its current version")
			}
			_, err = q.ExecContext(c.Request.Context(), `UPDATE business_scenarios SET deleted_at=now(),updated_at=now(),version=version+1,updated_by=$2 WHERE id=$1`, id, auth.Actor.UserID)
			return err
		})
	}
	if err != nil {
		data.Fail(c, err)
		return
	}
	private(c)
	c.Status(http.StatusNoContent)
}

func cloneContent(old Scenario, id uuid.UUID, name string) Content {
	value := old.Content
	value.SourcePageIDs = append([]uuid.UUID{}, old.ProvenanceSourcePageIDs...)
	value.Name = name
	value.Participants = append([]Participant{}, old.Participants...)
	value.Steps = append([]Step{}, old.Steps...)
	value.Relationships = append([]Relationship{}, old.Relationships...)
	ids := map[uuid.UUID]uuid.UUID{old.ID: id}
	for i, p := range value.Participants {
		ids[p.ID] = uuid.New()
		value.Participants[i].ID = ids[p.ID]
	}
	for i, step := range value.Steps {
		ids[step.ID] = uuid.New()
		value.Steps[i].ID = ids[step.ID]
	}
	for i := range value.Steps {
		step := &value.Steps[i]
		step.FromID, step.ToID, step.ReturnOf = ids[step.FromID], ids[step.ToID], ids[step.ReturnOf]
	}
	for i := range value.Relationships {
		rel := &value.Relationships[i]
		rel.ID = uuid.New()
		if mapped, ok := ids[rel.FromID]; ok {
			rel.FromID = mapped
		}
		if mapped, ok := ids[rel.ToID]; ok {
			rel.ToID = mapped
		}
	}
	return value
}

func (s *service) copy(c *gin.Context) {
	input, err := data.Bind(c, "version", "name")
	if err != nil {
		data.Fail(c, err)
		return
	}
	version, err := versionInput(input)
	if err != nil {
		data.Fail(c, err)
		return
	}
	id, err := httpapi.UUIDParam(c, "scenarioID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	var result Scenario
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		auth, err := scope(c, q, identity.Member)
		if err != nil {
			return err
		}
		old, err := load(c.Request.Context(), q, auth, id, false)
		if err != nil {
			return err
		}
		if old.Version != version {
			return data.Conflict("The scenario changed; reload its current version")
		}
		name := string([]rune(old.Name)[:min(len([]rune(old.Name)), 233)]) + " (copy)"
		if _, present := input["name"]; present {
			name, err = input.String("name", true, 240)
			if err != nil {
				return err
			}
		}
		if err := relationshipsVisible(c.Request.Context(), q, auth, &old); err != nil {
			return err
		}
		newID := uuid.New()
		value := cloneContent(old, newID, name)
		if err := validateContent(value, newID); err != nil {
			return err
		}
		if err := currentCredentials(c.Request.Context(), q, auth, identity.Member); err != nil {
			return err
		}
		body, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(c.Request.Context(), `INSERT INTO business_scenarios(id,workspace_id,project_id,story_id,body,review_needed,created_by,updated_by) VALUES($1,$2,$3,$4,$5::jsonb,$6,$7,$7)`, newID, auth.WorkspaceID, auth.ProjectID, value.StoryID, string(body), old.ReviewNeeded, auth.Actor.UserID); err != nil {
			return err
		}
		if err := addSources(c.Request.Context(), q, auth, newID, value.SourcePageIDs); err != nil {
			return err
		}
		result, err = load(c.Request.Context(), q, auth, newID, false)
		return err
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	private(c)
	httpapi.JSON(c, http.StatusCreated, result)
}

func (s *service) versions(c *gin.Context) {
	id, err := httpapi.UUIDParam(c, "scenarioID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	result := []Version{}
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		auth, err := scope(c, q, identity.Guest)
		if err != nil {
			return err
		}
		if _, err := load(c.Request.Context(), q, auth, id, false); err != nil {
			return err
		}
		rows, err := q.QueryContext(c.Request.Context(), `SELECT id,version,created_at FROM business_scenario_versions WHERE scenario_id=$1 ORDER BY version DESC`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var version Version
			if err := rows.Scan(&version.ID, &version.Version, &version.CreatedAt); err != nil {
				return err
			}
			result = append(result, version)
		}
		return rows.Err()
	})
	private(c)
	data.Send(c, result, err)
}

func private(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
}

func svgResponse(c *gin.Context, filename, svg string, err error) {
	private(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	c.Header("Content-Security-Policy", "default-src 'none'; style-src 'none'; script-src 'none'; frame-ancestors 'none'; sandbox")
	if c.Query("download") == "1" {
		c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	}
	c.Data(200, "image/svg+xml; charset=utf-8", []byte(svg))
}

func (s *service) sequence(c *gin.Context) {
	id, err := httpapi.UUIDParam(c, "scenarioID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	version, err := requestedVersion(c, false)
	result := ""
	if err == nil {
		err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
			auth, err := scope(c, q, identity.Guest)
			if err != nil {
				return err
			}
			value, err := load(c.Request.Context(), q, auth, id, false)
			if err != nil {
				return err
			}
			value, err = loadHistorical(c.Request.Context(), q, value, version)
			if err == nil {
				result = SequenceSVG(value)
			}
			return err
		})
	}
	svgResponse(c, "sequence-"+id.String()+".svg", result, err)
}

func (s *service) projectUseCases(c *gin.Context) { s.renderUseCases(c, false) }
func (s *service) useCases(c *gin.Context)        { s.renderUseCases(c, true) }

func (s *service) renderUseCases(c *gin.Context, single bool) {
	id, version := uuid.Nil, int64(0)
	var err error
	var storyFilter map[uuid.UUID]bool
	if single {
		id, err = httpapi.UUIDParam(c, "scenarioID")
		if err == nil {
			version, err = requestedVersion(c, false)
		}
	} else if raw, provided := c.Request.URL.Query()["story_ids"]; provided {
		storyFilter = map[uuid.UUID]bool{}
		if len(raw) != 1 {
			err = data.Invalid("story_ids must be a single comma-separated UUID list")
		} else if strings.TrimSpace(raw[0]) != "" {
			for _, item := range strings.Split(raw[0], ",") {
				storyID, parseErr := uuid.Parse(strings.TrimSpace(item))
				if parseErr != nil || storyID == uuid.Nil {
					err = data.Invalid("story_ids must contain only nonzero UUIDs")
					break
				}
				storyFilter[storyID] = true
			}
		}
	}
	result := ""
	if err == nil {
		err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
			auth, err := scope(c, q, identity.Guest)
			if err != nil {
				return err
			}
			var primary Scenario
			wanted := map[uuid.UUID]bool{}
			if single {
				primary, err = load(c.Request.Context(), q, auth, id, false)
				if err != nil {
					return err
				}
				primary, err = loadHistorical(c.Request.Context(), q, primary, version)
				if err != nil {
					return err
				}
				wanted[id] = true
				for _, relation := range primary.Relationships {
					wanted[relation.FromID], wanted[relation.ToID] = true, true
				}
			}
			ids, err := visibleIDs(c.Request.Context(), q, auth)
			if err != nil {
				return err
			}
			values := []Scenario{}
			for _, currentID := range ids {
				if single && !wanted[currentID] {
					continue
				}
				if currentID == id {
					values = append(values, primary)
					continue
				}
				value, err := load(c.Request.Context(), q, auth, currentID, false)
				if isHidden(err) {
					continue
				}
				if err != nil {
					return err
				}
				if storyFilter != nil && !storyFilter[value.StoryID] {
					continue
				}
				values = append(values, value)
			}
			result = UseCaseSVG(values)
			return nil
		})
	}
	filename := "project-use-cases.svg"
	if single {
		filename = "use-case-" + strings.ToLower(id.String()) + ".svg"
	}
	svgResponse(c, filename, result, err)
}
