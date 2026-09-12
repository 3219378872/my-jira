// Package application assembles the original HTTP modules in one place so the
// running API, OpenAPI generator, and contract tests use the same route graph.
package application

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"my-jira/apps/api/internal/automation"
	"my-jira/apps/api/internal/documents"
	"my-jira/apps/api/internal/files"
	"my-jira/apps/api/internal/foundation"
	"my-jira/apps/api/internal/integrations"
	"my-jira/apps/api/internal/openapi"
	"my-jira/apps/api/internal/planning"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/projection"
	"my-jira/apps/api/internal/quality"
	"my-jira/apps/api/internal/requirements"
	"my-jira/apps/api/internal/resources"
	"my-jira/apps/api/internal/scenarios"
	"my-jira/apps/api/internal/support"
	"my-jira/apps/api/internal/workitems"
)

func Router(deps platform.Dependencies, config foundation.Config) *gin.Engine {
	foundationServer := foundation.New(deps, config)
	router := gin.New()
	router.SetTrustedProxies(nil)
	router.Use(gin.Recovery())
	router.GET("/healthz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		if deps.DB == nil || deps.DB.SQL == nil || deps.DB.SQL.PingContext(ctx) != nil {
			c.JSON(503, gin.H{"status": "unavailable"})
			return
		}
		c.JSON(200, gin.H{"status": "ok"})
	})
	router.Use(foundationServer.Middleware())
	public := router.Group("/api/v1")
	foundationServer.RegisterPublic(public)
	integrations.RegisterPublic(public, deps)
	quality.RegisterPublic(public, deps)
	private := router.Group("/api/v1", foundationServer.RequireAuth(), projection.WorkItems())
	foundationServer.Register(private)
	workitems.Register(private, deps)
	planning.Register(private, deps)
	documents.Register(private, deps)
	support.Register(private, deps)
	files.Register(private, deps)
	integrations.Register(private, deps)
	requirements.Register(private, deps)
	resources.Register(private, deps)
	scenarios.Register(private, deps)
	automation.Register(private, deps)
	quality.Register(private, deps)
	openapi.Register(router)
	router.NoRoute(func(c *gin.Context) {
		c.JSON(404, gin.H{"error": gin.H{"code": "not_found", "message": "The requested route was not found"}})
	})
	return router
}
