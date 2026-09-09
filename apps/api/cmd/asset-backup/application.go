package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/application"
	"my-jira/apps/api/internal/foundation"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/jobs"
	"my-jira/apps/api/internal/platform/serviceconfig"
)

var restoreDatabasePattern = regexp.MustCompile(`^myjira_restore_test_[a-z0-9_]+$`)

func requireRestoreDatabase(ctx context.Context, db *database.Database) error {
	var name string
	if err := db.SQL.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil {
		return err
	}
	if !restoreDatabasePattern.MatchString(name) {
		return fmt.Errorf("application restore verification requires an isolated myjira_restore_test_ database")
	}
	return nil
}

func verifyApplication(ctx context.Context, db *database.Database, snapshot manifest, targetBucket string) (result error) {
	if err := requireRestoreDatabase(ctx, db); err != nil {
		return err
	}
	values, err := serviceconfig.Load(ctx, db.SQL, "storage")
	if err != nil {
		return err
	}
	values["bucket"] = targetBucket
	if err := serviceconfig.Save(ctx, db.SQL, "storage", values); err != nil {
		return err
	}
	entries := map[string]entry{}
	for _, item := range snapshot.Objects {
		entries[item.Key] = item
	}
	type asset struct {
		id, owner          uuid.UUID
		workspace, project uuid.NullUUID
		key, contentType   string
		size               int64
	}
	assets := []asset{}
	rows, err := db.SQL.QueryContext(ctx, `SELECT id,uploaded_by,workspace_id,project_id,object_key,content_type,size_bytes FROM file_assets WHERE deleted_at IS NULL AND upload_status='completed' ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var item asset
		if err := rows.Scan(&item.id, &item.owner, &item.workspace, &item.project, &item.key, &item.contentType, &item.size); err != nil {
			rows.Close()
			return err
		}
		assets = append(assets, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	users := []uuid.UUID{}
	rows, err = db.SQL.QueryContext(ctx, `SELECT id FROM users WHERE is_active AND deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		users = append(users, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	sessions, tokens := map[uuid.UUID]uuid.UUID{}, map[uuid.UUID]string{}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, id := range sessions {
			_, err := db.SQL.ExecContext(cleanupCtx, "DELETE FROM sessions WHERE id=$1", id)
			result = errors.Join(result, err)
		}
	}()
	for _, id := range users {
		sessionID, token := uuid.New(), uuid.NewString()+uuid.NewString()
		tokenHash := sha256.Sum256([]byte(token))
		if _, err := db.SQL.ExecContext(ctx, `INSERT INTO sessions(id,user_id,token_hash,csrf_hash,expires_at,user_agent) VALUES($1,$2,$3,$3,now()+interval '30 minutes','isolated backup verification')`, sessionID, id, hex.EncodeToString(tokenHash[:])); err != nil {
			return err
		}
		sessions[id], tokens[id] = sessionID, token
	}
	gin.SetMode(gin.TestMode)
	deps := platform.Dependencies{DB: db, Policy: &identity.SQLPolicy{DB: db.SQL}, Jobs: jobs.Outbox{}}
	server := httptest.NewServer(application.Router(deps, foundation.Config{}))
	defer server.Close()
	client := server.Client()
	client.Timeout = 30 * time.Second
	verified, inaccessible := 0, 0
	for _, asset := range assets {
		expected, exists := entries[asset.key]
		if !exists || expected.Size != asset.size || expected.ContentType != asset.contentType {
			return fmt.Errorf("restored asset %s does not match snapshot object metadata", asset.id)
		}
		path := "/api/v1/auth"
		if asset.workspace.Valid {
			path = "/api/v1/workspaces/" + asset.workspace.UUID.String()
		}
		if asset.project.Valid {
			path += "/projects/" + asset.project.UUID.String()
		}
		path += "/assets/" + asset.id.String() + "/download"
		request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+path, nil)
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			return fmt.Errorf("restored asset %s allowed an unauthenticated download", asset.id)
		}
		allowed := false
		candidates := append([]uuid.UUID{asset.owner}, users...)
		seen := map[uuid.UUID]bool{}
		for _, user := range candidates {
			if seen[user] || tokens[user] == "" {
				continue
			}
			seen[user] = true
			request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+path, nil)
			request.AddCookie(&http.Cookie{Name: "mj_session", Value: tokens[user]})
			response, err := client.Do(request)
			if err != nil {
				return err
			}
			if response.StatusCode == 403 || response.StatusCode == 404 {
				response.Body.Close()
				continue
			}
			if response.StatusCode != 200 {
				response.Body.Close()
				return fmt.Errorf("restored asset %s failed authenticated HTTP download with status %d", asset.id, response.StatusCode)
			}
			checksum := sha256.New()
			size, err := io.Copy(checksum, io.LimitReader(response.Body, expected.Size+1))
			response.Body.Close()
			if err != nil {
				return err
			}
			if size != expected.Size || hex.EncodeToString(checksum.Sum(nil)) != expected.SHA256 || response.Header.Get("Content-Type") != asset.contentType {
				return fmt.Errorf("restored asset %s failed authenticated HTTP content verification", asset.id)
			}
			allowed = true
			verified++
			break
		}
		if !allowed {
			inaccessible++
		}
	}
	if len(assets) > 0 && verified == 0 {
		return fmt.Errorf("no live restored asset could be verified through current authorized HTTP access")
	}
	fmt.Printf("Verified %d restored attachments through authenticated application HTTP and rejected anonymous downloads; %d currently inaccessible assets were verified at the object layer only.\n", verified, inaccessible)
	return nil
}
