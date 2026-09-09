// Package openapi documents the independently authored JSON API. Paths come from
// the live Gin route registry; reviewed schemas describe the public contract.
package openapi

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"my-jira/apps/api/internal/platform/projection"
)

type S map[string]any

type operation struct {
	Summary, Tag, Description, Access string
	Request, Response                 S
	Status                            int
	Queries                           []any
	RequestMedia, ResponseMedia       string
	Raw                               bool
	RequestOptional                   bool
}

func Register(router *gin.Engine) {
	router.GET("/api/v1/openapi.json", func(c *gin.Context) {
		document, err := Build(router.Routes())
		if err != nil {
			c.JSON(500, gin.H{"error": gin.H{"code": "contract_incomplete", "message": "API documentation is temporarily unavailable"}})
			return
		}
		c.Header("Cache-Control", "public, max-age=300")
		c.JSON(200, document)
	})
}

func Build(routes gin.RoutesInfo) (S, error) {
	catalog := routeCatalog()
	paths := S{}
	missing := []string{}
	for _, route := range routes {
		if !strings.HasPrefix(route.Path, "/api/v1/") && route.Path != "/api/v1/workspaces" {
			continue
		}
		path := strings.TrimPrefix(route.Path, "/api/v1")
		entry, exists := catalog[route.Method+" "+path]
		if !exists {
			missing = append(missing, route.Method+" "+path)
			continue
		}
		parameters := []any{}
		segments := strings.Split(path, "/")
		for i, segment := range segments {
			if !strings.HasPrefix(segment, ":") {
				continue
			}
			name := strings.TrimPrefix(segment, ":")
			segments[i] = "{" + name + "}"
			schema := stringSchema()
			if strings.HasSuffix(name, "ID") {
				schema = uuidSchema()
				if name == "userID" && strings.Contains(route.Path, "/profiles/") {
					schema = S{"anyOf": []any{uuidSchema(), enum("me")}}
				}
			} else if name == "provider" {
				schema = enum("google", "github", "gitlab", "gitea")
			} else if name == "service" {
				schema = enum("email", "storage", "google", "github", "gitlab", "gitea", "ai", "unsplash")
			}
			parameters = append(parameters, S{"name": name, "in": "path", "required": true, "schema": schema})
		}
		parameters = append(parameters, entry.Queries...)
		if route.Method == "GET" && projection.Supports(route.Path) {
			parameters = append(parameters,
				query("fields", S{"type": "string", "maxLength": 2048}, "Comma-separated permitted work-item fields: "+strings.Join(projection.FieldNames(), ",")+". id is always included. Omitted fields retain all approved scalar fields when expand is supplied."),
				query("expand", S{"type": "string", "maxLength": 2048}, "Comma-separated associations: "+strings.Join(projection.ExpansionNames(), ",")+"; none selects no associations. Returned properties are state_detail,project_detail,assignee_details,label_details,estimate_point_detail. No query preserves the existing full representation."))
			entry.Description += " Field projection runs after the handler's current authorization and filtering. It preserves pagination and group metadata. Unknown or repeated field parameters return 400 for authorized reads; access denials keep their original status. The pre-projection JSON response is limited to 16 MiB; use a smaller list limit on 413."
		}
		path = strings.Join(segments, "/")
		if entry.Status == 0 {
			entry.Status = 200
		}
		responses := S{}
		if entry.Status == 204 {
			responses["204"] = S{"description": "The operation completed; no response body."}
		} else if entry.Status == 302 {
			responses["302"] = S{"description": "Browser redirect to the identity provider or application.", "headers": S{"Location": S{"schema": stringSchema(), "description": "Redirect destination."}}}
		} else {
			response := entry.Response
			if !entry.Raw {
				response = object(S{"data": response}, "data")
			}
			media := entry.ResponseMedia
			if media == "" {
				media = "application/json"
			}
			responses[fmt.Sprint(entry.Status)] = S{"description": "Successful response.", "content": S{media: S{"schema": response}}}
		}
		for code, text := range map[string]string{"400": "Invalid fields, JSON, identifiers, dates, or filters.", "401": "A current browser session or permitted workspace API token is required.", "403": "Insufficient role, invalid CSRF, or a disabled feature.", "404": "Resource is absent or outside the actor's current visible scope.", "409": "Concurrent version change, duplicate value, or a business invariant prevents the operation.", "429": "Request rate limit exceeded.", "500": "The operation could not be completed."} {
			responses[code] = S{"description": text, "content": S{"application/json": S{"schema": ref("Error")}}}
		}
		if route.Method == "GET" && projection.Supports(route.Path) {
			responses["413"] = S{"description": "The response exceeds the 16 MiB projection limit; request a smaller page.", "content": S{"application/json": S{"schema": ref("Error")}}}
		}
		if entry.Tag == "Services" || entry.Tag == "Integrations" || entry.Tag == "Files" {
			for _, code := range []string{"502", "503"} {
				responses[code] = S{"description": "External service failed or is not configured.", "content": S{"application/json": S{"schema": ref("Error")}}}
			}
		}
		id := strings.NewReplacer("/", "_", "{", "", "}", "", "-", "_", ".", "_").Replace(strings.ToLower(route.Method) + path)
		item := S{"operationId": id, "summary": entry.Summary, "tags": []string{entry.Tag}, "responses": responses, "security": security(entry.Access, route.Method)}
		if entry.Description != "" {
			item["description"] = entry.Description
		}
		if len(parameters) > 0 {
			item["parameters"] = parameters
		}
		if entry.Request != nil {
			media := entry.RequestMedia
			if media == "" {
				media = "application/json"
			}
			item["requestBody"] = S{"required": !entry.RequestOptional, "content": S{media: S{"schema": entry.Request}}}
		}
		group, _ := paths[path].(S)
		if group == nil {
			group = S{}
		}
		group[strings.ToLower(route.Method)] = item
		paths[path] = group
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("OpenAPI contracts missing for registered routes:\n%s", strings.Join(missing, "\n"))
	}
	return S{
		"openapi": "3.1.0", "jsonSchemaDialect": "https://json-schema.org/draft/2020-12/schema",
		"info":    S{"title": "my-jira original API", "version": "1.0.0", "description": "Independent application API. UUID identifiers, snake_case JSON, UTC timestamps, and YYYY-MM-DD business dates. Browser writes require a current CSRF header and cookie; workspace API tokens are separately scoped. Membership and private-resource access are rechecked. Work-item/page edits require current versions; a 409 never silently overwrites newer content. Ordinary JSON requests are limited to 2 MiB. This contract does not claim compatibility with another product's API."},
		"servers": []any{S{"url": "/api/v1", "description": "Current application origin"}}, "paths": paths,
		"components": S{"schemas": schemas(), "securitySchemes": S{
			"browserSession":  S{"type": "apiKey", "in": "cookie", "name": "mj_session", "description": "Opaque HttpOnly session cookie returned by login, setup, registration, magic-code verification or OAuth."},
			"csrfHeader":      S{"type": "apiKey", "in": "header", "name": "X-CSRF-Token", "description": "Current token from GET /auth/csrf or the authentication response; must match the CSRF cookie."},
			"csrfCookie":      S{"type": "apiKey", "in": "cookie", "name": "mj_csrf"},
			"workspaceBearer": S{"type": "http", "scheme": "bearer", "bearerFormat": "mjt_...", "description": "Revocable API token restricted to one workspace. Cannot call account, instance, public-site mutation or credential-creation routes."},
		}},
	}, nil
}

func security(access, method string) []any {
	if access == "anonymous" {
		return []any{}
	}
	if access == "optional" {
		return []any{S{}, S{"browserSession": []any{}}}
	}
	if access == "csrf" {
		return []any{S{"csrfHeader": []any{}, "csrfCookie": []any{}}}
	}
	browser := S{"browserSession": []any{}}
	if method != "GET" && method != "HEAD" {
		browser["csrfHeader"], browser["csrfCookie"] = []any{}, []any{}
	}
	result := []any{browser}
	if access == "workspace" {
		result = append(result, S{"workspaceBearer": []any{}})
	}
	return result
}
