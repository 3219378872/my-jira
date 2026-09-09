package integrations

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/minio/minio-go/v7"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/jobs"
	"my-jira/apps/api/internal/platform/objectstore"
	"my-jira/apps/api/internal/workitems"
)

func RegisterJobs(mux *asynq.ServeMux, d platform.Dependencies) {
	h := &handler{d}
	mux.HandleFunc("work_item.changed", h.changedJob)
	mux.HandleFunc("entity.changed", h.entityChangedJob)
	mux.HandleFunc("webhook.deliver", h.webhookJob)
	mux.HandleFunc("export.generate", h.exportJob)
}

type changeEvent struct {
	EventID, WorkspaceID, ProjectID, WorkItemID, ActorID uuid.UUID
	Action                                               string
}

func decodeTask(t *asynq.Task, target any) (jobs.Envelope, error) {
	var env jobs.Envelope
	if e := json.Unmarshal(t.Payload(), &env); e != nil {
		return env, fmt.Errorf("invalid envelope: %w", asynq.SkipRetry)
	}
	if e := json.Unmarshal(env.Payload, target); e != nil {
		return env, fmt.Errorf("invalid payload: %w", asynq.SkipRetry)
	}
	return env, nil
}
func (h *handler) changedJob(ctx context.Context, t *asynq.Task) error {
	var body struct {
		EventID     uuid.UUID `json:"event_id"`
		WorkspaceID uuid.UUID `json:"workspace_id"`
		ProjectID   uuid.UUID `json:"project_id"`
		WorkItemID  uuid.UUID `json:"work_item_id"`
		ActorID     uuid.UUID `json:"actor_id"`
		Action      string    `json:"action"`
	}
	env, e := decodeTask(t, &body)
	if e != nil {
		return e
	}
	var name string
	var creator uuid.UUID
	var raw []byte
	e = h.d.DB.SQL.QueryRowContext(ctx, `SELECT name,created_by,to_jsonb(w)-'description_binary'-'deleted_at' FROM work_items w WHERE id=$1 AND workspace_id=$2 AND project_id=$3`, body.WorkItemID, body.WorkspaceID, body.ProjectID).Scan(&name, &creator, &raw)
	if e == sql.ErrNoRows {
		return nil
	} // A moved or removed item invalidates an old-scope event.
	if e != nil {
		return e
	}
	rows, e := h.d.DB.SQL.QueryContext(ctx, `SELECT user_id,'subscribed' FROM work_item_subscribers WHERE work_item_id=$1 AND deleted_at IS NULL UNION SELECT user_id,'assigned' FROM work_item_assignees WHERE work_item_id=$1 AND deleted_at IS NULL UNION SELECT created_by,'created' FROM work_items WHERE id=$1`, body.WorkItemID)
	if e != nil {
		return e
	}
	recipients := map[uuid.UUID][]string{}
	for rows.Next() {
		var id uuid.UUID
		var reason string
		if e = rows.Scan(&id, &reason); e != nil {
			rows.Close()
			return e
		}
		recipients[id] = append(recipients[id], reason)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	mentioned, e := h.eventMentions(ctx, body.EventID, body.WorkItemID)
	if e != nil {
		return e
	}
	for user := range mentioned {
		recipients[user] = append(recipients[user], "mentions")
	}
	for user, reasons := range recipients {
		if user == body.ActorID {
			continue
		}
		scope, e := h.d.Policy.Project(ctx, identity.Actor{UserID: user}, body.WorkspaceID, body.ProjectID, identity.Guest)
		if e != nil {
			continue
		}
		if scope.Role < identity.Member && !scope.GuestCanViewAll && creator != user {
			continue
		}
		var preferencesRaw []byte
		e = h.d.DB.SQL.QueryRowContext(ctx, `SELECT COALESCE((SELECT value FROM preferences WHERE workspace_id=$1 AND user_id=$2 AND project_id IS NULL AND scope='notifications' AND deleted_at IS NULL),'{}'::jsonb)`, body.WorkspaceID, user).Scan(&preferencesRaw)
		if e != nil {
			return e
		}
		preferences := map[string]any{}
		if e = json.Unmarshal(preferencesRaw, &preferences); e != nil {
			return e
		}
		allowed := false
		for _, reason := range reasons {
			if preferences[reason] != false {
				allowed = true
			}
		}
		if !allowed || (preferences["in_app"] == false && preferences["email"] == false) {
			continue
		}
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(env.EventID.String()+":"+user.String()))
		data, _ := json.Marshal(map[string]any{"event_id": body.EventID, "action": body.Action, "work_item_id": body.WorkItemID, "reasons": reasons, "silent": preferences["in_app"] == false})
		e = h.d.DB.WithinTx(ctx, func(q database.DBTX) error {
			if _, e := q.ExecContext(ctx, `INSERT INTO notifications(id,workspace_id,project_id,user_id,actor_id,entity_type,entity_id,title,body,data) VALUES($1,$2,$3,$4,$5,'work_item',$6,$7,$8,$9::jsonb) ON CONFLICT(id) DO NOTHING`, id, body.WorkspaceID, body.ProjectID, user, body.ActorID, body.WorkItemID, name, body.Action, string(data)); e != nil {
				return e
			}
			if preferences["email"] != false {
				return h.d.Jobs.Publish(ctx, q, "email.send", map[string]any{"notification_id": id}, "notification-email:"+id.String())
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	event := entityEvent{EventID: body.EventID, WorkspaceID: body.WorkspaceID, ProjectID: body.ProjectID, EntityID: body.WorkItemID, Event: "work_item.changed", Action: body.Action, Data: raw}
	if e = h.queueEntityHooks(ctx, event); e != nil {
		return e
	}
	if action, comment := map[string]string{"commented": "created", "comment_edited": "updated", "comment_updated": "updated", "comment_deleted": "deleted"}[body.Action]; comment {
		var commentData []byte
		e = h.d.DB.SQL.QueryRowContext(ctx, `SELECT (to_jsonb(cm)-'deleted_at') || CASE WHEN jsonb_typeof(a.new_value)='object' THEN a.new_value ELSE '{}'::jsonb END FROM activities a JOIN comments cm ON cm.id::text=COALESCE(a.new_value->>'comment_id',a.old_value->>'comment_id') WHERE a.id=$1 AND a.work_item_id=$2 AND cm.work_item_id=$2`, body.EventID, body.WorkItemID).Scan(&commentData)
		if e == sql.ErrNoRows {
			return nil
		}
		if e != nil {
			return e
		}
		event.Event, event.Action, event.Data = "comment.changed", action, commentData
		// A comment change and its work-item activity are separate webhook events.
		// Preserve deterministic IDs across retries without colliding with the
		// per-hook/event database uniqueness constraint.
		event.EventID = uuid.NewSHA1(body.EventID, []byte("comment.changed"))
		return h.queueEntityHooks(ctx, event)
	}
	return nil
}

func mentionIDs(value any, result map[uuid.UUID]bool) {
	switch node := value.(type) {
	case map[string]any:
		if node["type"] == "mention" {
			if attrs, ok := node["attrs"].(map[string]any); ok {
				if text, ok := attrs["id"].(string); ok {
					if id, e := uuid.Parse(text); e == nil {
						result[id] = true
					}
				}
			}
		}
		for _, child := range node {
			mentionIDs(child, result)
		}
	case []any:
		for _, child := range node {
			mentionIDs(child, result)
		}
	}
}

func (h *handler) eventMentions(ctx context.Context, eventID, itemID uuid.UUID) (map[uuid.UUID]bool, error) {
	result := map[uuid.UUID]bool{}
	var beforeRaw, afterRaw []byte
	var action string
	e := h.d.DB.SQL.QueryRowContext(ctx, `SELECT action,old_value,new_value FROM activities WHERE id=$1 AND work_item_id=$2`, eventID, itemID).Scan(&action, &beforeRaw, &afterRaw)
	if e == sql.ErrNoRows {
		return result, nil
	}
	if e != nil {
		return nil, e
	}
	var before, after any
	_ = json.Unmarshal(beforeRaw, &before)
	_ = json.Unmarshal(afterRaw, &after)
	if action == "commented" || action == "comment_updated" || action == "comment_edited" {
		if value, ok := after.(map[string]any); ok {
			if comment, ok := value["comment_id"].(string); ok && value["body_json"] == nil {
				var raw []byte
				e = h.d.DB.SQL.QueryRowContext(ctx, `SELECT body_json FROM comments WHERE id=$1 AND work_item_id=$2 AND deleted_at IS NULL`, comment, itemID).Scan(&raw)
				if e == sql.ErrNoRows {
					return result, nil
				}
				if e != nil {
					return nil, e
				}
				_ = json.Unmarshal(raw, &after)
			}
		}
	}
	mentionIDs(after, result)
	old := map[uuid.UUID]bool{}
	mentionIDs(before, old)
	for id := range old {
		delete(result, id)
	}
	return result, nil
}

func publicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32"} {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func validateDestination(ctx context.Context, value string) error {
	u, e := url.Parse(value)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("Webhook requires an HTTP or HTTPS URL without credentials")
	}
	ips, e := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if e != nil || len(ips) == 0 {
		return fmt.Errorf("Webhook host could not be resolved")
	}
	for _, ip := range ips {
		if !publicIP(ip.IP) {
			return fmt.Errorf("Webhook host must resolve to a public address")
		}
	}
	return nil
}

func webhookClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil {
			return nil, e
		}
		for _, ip := range ips {
			if !publicIP(ip.IP) {
				return nil, fmt.Errorf("private webhook destination rejected")
			}
		}
		var last error
		for _, ip := range ips {
			conn, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if e == nil {
				return conn, nil
			}
			last = e
		}
		if last == nil {
			last = fmt.Errorf("no destination addresses")
		}
		return nil, last
	}}}
}

func (h *handler) webhookJob(ctx context.Context, t *asynq.Task) error {
	return h.sendWebhook(ctx, t, webhookClient(), validateDestination)
}

func (h *handler) sendWebhook(ctx context.Context, t *asynq.Task, client *http.Client, validate func(context.Context, string) error) error {
	var b struct {
		WebhookID uuid.UUID       `json:"webhook_id"`
		EventID   uuid.UUID       `json:"event_id"`
		Event     string          `json:"event"`
		Action    string          `json:"action"`
		ProjectID *uuid.UUID      `json:"project_id"`
		Data      json.RawMessage `json:"data"`
	}
	_, e := decodeTask(t, &b)
	if e != nil {
		return e
	}
	var target, signingSecret string
	var wid, owner uuid.UUID
	var active bool
	e = h.d.DB.SQL.QueryRowContext(ctx, `SELECT workspace_id,created_by,url,secret,is_active AND (events ? '*' OR events ? $2) FROM webhooks WHERE id=$1 AND deleted_at IS NULL`, b.WebhookID, b.Event).Scan(&wid, &owner, &target, &signingSecret, &active)
	if e == sql.ErrNoRows {
		return nil
	}
	if e != nil {
		return e
	}
	if !active {
		return nil
	}
	projectID := uuid.Nil
	if b.ProjectID != nil {
		projectID = *b.ProjectID
	}
	if !h.canDeliver(ctx, owner, entityEvent{WorkspaceID: wid, ProjectID: projectID, Event: b.Event, Action: b.Action}) {
		return nil
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("delivery:"+b.WebhookID.String()+":"+b.EventID.String()+":"+b.Event))
	var delivered bool
	e = h.d.DB.SQL.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM webhook_deliveries WHERE id=$1 AND delivered_at IS NOT NULL)`, id).Scan(&delivered)
	if e != nil {
		return e
	}
	if delivered {
		return nil
	}
	payload, _ := json.Marshal(map[string]any{"id": b.EventID, "event": b.Event, "action": b.Action, "project_id": b.ProjectID, "data": b.Data})
	if _, e = h.d.DB.SQL.ExecContext(ctx, `INSERT INTO webhook_deliveries(id,workspace_id,webhook_id,event_id,request,attempts,response_body) VALUES($1,$2,$3,$4,$5::jsonb,0,'') ON CONFLICT(id) DO NOTHING`, id, wid, b.WebhookID, b.EventID, string(payload)); e != nil {
		return e
	}
	if e = validate(ctx, target); e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if e != nil {
		return e
	}
	mac := hmac.New(sha256.New, []byte(signingSecret))
	_, _ = mac.Write(payload)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-MyJira-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("X-MyJira-Event", b.Event)
	req.Header.Set("Idempotency-Key", b.EventID.String()+":"+b.Event)
	res, sendErr := client.Do(req)
	status := 0
	response := ""
	var completed *time.Time
	if sendErr != nil {
		response = sendErr.Error()
	} else {
		status = res.StatusCode
		content, _ := io.ReadAll(io.LimitReader(res.Body, 8192))
		_ = res.Body.Close()
		response = string(content)
		if status >= 200 && status < 300 {
			now := time.Now().UTC()
			completed = &now
		}
	}
	_, e = h.d.DB.SQL.ExecContext(ctx, `UPDATE webhook_deliveries SET response_status=$2,response_body=$3,attempts=attempts+1,delivered_at=$4,updated_at=now() WHERE id=$1`, id, status, response, completed)
	if e != nil {
		return e
	}
	if sendErr != nil {
		return sendErr
	}
	if completed == nil {
		return fmt.Errorf("webhook returned HTTP %d", status)
	}
	return nil
}

func (h *handler) exportJob(ctx context.Context, t *asynq.Task) (resultErr error) {
	var b struct {
		ExportID uuid.UUID `json:"export_id"`
	}
	_, e := decodeTask(t, &b)
	if e != nil {
		return e
	}
	var wid, user uuid.UUID
	var pid uuid.NullUUID
	var format, status string
	var filterJSON []byte
	e = h.d.DB.SQL.QueryRowContext(ctx, `SELECT workspace_id,project_id,requested_by,format,status,filters FROM exports WHERE id=$1 AND deleted_at IS NULL`, b.ExportID).Scan(&wid, &pid, &user, &format, &status, &filterJSON)
	if e != nil {
		return e
	}
	if status == "completed" {
		return nil
	}
	defer func() {
		if resultErr != nil {
			retries, _ := asynq.GetRetryCount(ctx)
			max, _ := asynq.GetMaxRetry(ctx)
			nextStatus := "queued"
			if retries >= max {
				nextStatus = "failed"
			}
			_, _ = h.d.DB.SQL.ExecContext(context.Background(), `UPDATE exports SET status=$2,error_message='Export processing failed; inspect the worker log',updated_at=now() WHERE id=$1`, b.ExportID, nextStatus)
		}
	}()
	if _, e = h.d.Policy.Workspace(ctx, identity.Actor{UserID: user}, wid, identity.Member); e != nil {
		_, _ = h.d.DB.SQL.ExecContext(ctx, `UPDATE exports SET status='failed',error_message='Workspace access was revoked' WHERE id=$1`, b.ExportID)
		return nil
	}
	if pid.Valid {
		if _, e = h.d.Policy.Project(ctx, identity.Actor{UserID: user}, wid, pid.UUID, identity.Member); e != nil {
			_, _ = h.d.DB.SQL.ExecContext(ctx, `UPDATE exports SET status='failed',error_message='Project access was revoked' WHERE id=$1`, b.ExportID)
			return nil
		}
	}
	_, e = h.d.DB.SQL.ExecContext(ctx, `UPDATE exports SET status='processing',error_message='',updated_at=now() WHERE id=$1`, b.ExportID)
	if e != nil {
		return e
	}
	query := `SELECT to_jsonb(w)-'deleted_at'-'description_binary' FROM work_items w JOIN projects p ON p.id=w.project_id WHERE w.workspace_id=$1 AND w.deleted_at IS NULL AND p.deleted_at IS NULL AND (p.network='public' OR EXISTS(SELECT 1 FROM project_members pm WHERE pm.project_id=p.id AND pm.user_id=$2 AND pm.is_active AND pm.deleted_at IS NULL))`
	args := []any{wid, user}
	if pid.Valid {
		query += " AND w.project_id=$3"
		args = append(args, pid.UUID)
	}
	var filters map[string]any
	if e = json.Unmarshal(filterJSON, &filters); e != nil {
		return e
	}
	delete(filters, "_scope_project_ids")
	if exportsDeleted(filters) {
		if pid.Valid {
			_, e = h.d.Policy.Project(ctx, identity.Actor{UserID: user}, wid, pid.UUID, identity.Admin)
		} else {
			_, e = h.d.Policy.Workspace(ctx, identity.Actor{UserID: user}, wid, identity.Admin)
		}
		if e != nil {
			_, e = h.d.DB.SQL.ExecContext(ctx, `UPDATE exports SET status='failed',error_message='Administrator access was revoked' WHERE id=$1`, b.ExportID)
			return e
		}
		if fmt.Sprint(filters["deleted"]) == "true" {
			query = strings.Replace(query, "w.deleted_at IS NULL", "w.deleted_at IS NOT NULL", 1)
		} else {
			query = strings.Replace(query, " AND w.deleted_at IS NULL", "", 1)
		}
	}
	query, args, e = workitems.CompileFilters(ctx, identity.Actor{UserID: user}, query, args, filters)
	if e != nil {
		return e
	}
	rows, e := h.d.DB.SQL.QueryContext(ctx, query+" ORDER BY w.project_id,w.sequence_id", args...)
	if e != nil {
		return e
	}
	records := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return e
		}
		var record map[string]any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if e = decoder.Decode(&record); e != nil {
			rows.Close()
			return e
		}
		records = append(records, record)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	allowedRecords := make([]map[string]any, 0, len(records))
	projectIDs := map[string]bool{}
	for _, record := range records {
		projectID, err := uuid.Parse(fmt.Sprint(record["project_id"]))
		if err != nil {
			return err
		}
		if !projectIDs[projectID.String()] {
			if _, err = h.d.Policy.Project(ctx, identity.Actor{UserID: user}, wid, projectID, identity.Member); err != nil {
				continue
			}
			projectIDs[projectID.String()] = true
		}
		allowedRecords = append(allowedRecords, record)
	}
	records = allowedRecords
	var content []byte
	ctype := "application/json"
	if format == "json" {
		content, e = json.MarshalIndent(records, "", "  ")
	} else {
		columns := []string{"id", "name", "sequence_id", "priority", "state_id", "project_id", "description_html", "start_date", "target_date", "estimate", "created_at", "updated_at"}
		grid := [][]string{columns}
		for _, record := range records {
			line := []string{}
			for _, col := range columns {
				v := ""
				if record[col] != nil {
					v = fmt.Sprint(record[col])
				}
				line = append(line, v)
			}
			grid = append(grid, line)
		}
		if format == "csv" {
			var out bytes.Buffer
			out.WriteString("\xef\xbb\xbf")
			writer := csv.NewWriter(&out)
			for _, line := range grid {
				for i, v := range line {
					if strings.HasPrefix(v, "=") || strings.HasPrefix(v, "+") || strings.HasPrefix(v, "-") || strings.HasPrefix(v, "@") || strings.HasPrefix(v, "\t") || strings.HasPrefix(v, "\r") {
						line[i] = "'" + v
					}
				}
				if e = writer.Write(line); e != nil {
					return e
				}
			}
			writer.Flush()
			e = writer.Error()
			content = out.Bytes()
			ctype = "text/csv; charset=utf-8"
		} else {
			content, e = xlsx(grid)
			ctype = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		}
	}
	if e != nil {
		return e
	}
	store, bucket, e := objectstore.Load(ctx, h.d.DB.SQL)
	if e != nil {
		return e
	}
	if e = objectstore.EnsureBucket(ctx, store, bucket); e != nil {
		return e
	}
	key := "exports/" + wid.String() + "/" + b.ExportID.String() + "." + format
	if _, e = store.PutObject(ctx, bucket, key, bytes.NewReader(content), int64(len(content)), minio.PutObjectOptions{ContentType: ctype}); e != nil {
		return e
	}
	ids := []string{}
	for id := range projectIDs {
		ids = append(ids, id)
	}
	scopeJSON, _ := json.Marshal(ids)
	_, e = h.d.DB.SQL.ExecContext(ctx, `UPDATE exports SET status='completed',object_key=$2,filters=jsonb_set(filters,'{_scope_project_ids}',$3::jsonb),completed_at=now(),updated_at=now() WHERE id=$1`, b.ExportID, key, string(scopeJSON))
	return e
}

func xlsx(grid [][]string) ([]byte, error) {
	var output bytes.Buffer
	z := zip.NewWriter(&output)
	parts := map[string]string{"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`, "_rels/.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`, "xl/workbook.xml": `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Work items" sheetId="1" r:id="rId1"/></sheets></workbook>`, "xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`}
	var sheet bytes.Buffer
	sheet.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for r, line := range grid {
		sheet.WriteString(`<row r="` + strconv.Itoa(r+1) + `">`)
		for _, value := range line {
			sheet.WriteString(`<c t="inlineStr"><is><t xml:space="preserve">`)
			if e := xml.EscapeText(&sheet, []byte(value)); e != nil {
				return nil, e
			}
			sheet.WriteString(`</t></is></c>`)
		}
		sheet.WriteString(`</row>`)
	}
	sheet.WriteString(`</sheetData></worksheet>`)
	parts["xl/worksheets/sheet1.xml"] = sheet.String()
	for name, value := range parts {
		w, e := z.Create(name)
		if e != nil {
			return nil, e
		}
		if _, e = io.WriteString(w, value); e != nil {
			return nil, e
		}
	}
	if e := z.Close(); e != nil {
		return nil, e
	}
	return output.Bytes(), nil
}

func (h *handler) downloadExport(c *gin.Context) {
	s, e := h.scope(c, identity.Member)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	id, e := httpapi.UUIDParam(c, "exportID")
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	var key, format, status, reportType string
	var pid uuid.NullUUID
	var scopeJSON []byte
	e = h.d.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT object_key,format,status,project_id,COALESCE(filters->'_scope_project_ids','[]'::jsonb),COALESCE(filters->>'report_type','') FROM exports WHERE id=$1 AND workspace_id=$2 AND requested_by=$3 AND deleted_at IS NULL`, id, s.WorkspaceID, s.Actor.UserID).Scan(&key, &format, &status, &pid, &scopeJSON, &reportType)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if pid.Valid {
		if _, e = h.d.Policy.Project(c.Request.Context(), s.Actor, s.WorkspaceID, pid.UUID, identity.Member); e != nil {
			httpapi.Fail(c, e)
			return
		}
	}
	if status != "completed" {
		fail(c, 409, "Export is not ready")
		return
	}
	var ids []uuid.UUID
	if e = json.Unmarshal(scopeJSON, &ids); e != nil {
		httpapi.Fail(c, e)
		return
	}
	for _, projectID := range ids {
		if _, e = h.d.Policy.Project(c.Request.Context(), s.Actor, s.WorkspaceID, projectID, identity.Member); e != nil {
			fail(c, 403, "Your access changed since this export was generated; create a new export")
			return
		}
	}
	store, bucket, e := objectstore.Load(c.Request.Context(), h.d.DB.SQL)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	object, e := store.GetObject(c.Request.Context(), bucket, key, minio.GetObjectOptions{})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	defer object.Close()
	info, e := object.Stat()
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	name := "work-items"
	if reportType == "analytics" {
		name = "analytics"
	}
	c.Header("Content-Disposition", `attachment; filename="`+name+`.`+format+`"`)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, no-store")
	c.DataFromReader(200, info.Size, info.ContentType, object, nil)
}
