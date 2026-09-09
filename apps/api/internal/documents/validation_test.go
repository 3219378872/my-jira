package documents

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
)

func testObject(text string) data.Object {
	var result data.Object
	_ = json.Unmarshal([]byte(text), &result)
	return result
}

func TestContentEditsRequireCurrentVersionAndUnlockedPage(t *testing.T) {
	owner := uuid.New()
	scope := identity.Scope{Actor: identity.Actor{UserID: owner}, Role: 15}
	p := page{OwnerID: owner, Version: 4}
	if err := authorizeEdit(scope, p, testObject(`{"content_html":"<p>x</p>"}`)); err == nil {
		t.Fatal("missing version allowed a potentially lost update")
	}
	if err := authorizeEdit(scope, p, testObject(`{"content_html":"<p>x</p>","version":3}`)); err == nil {
		t.Fatal("stale version accepted")
	}
	if err := authorizeEdit(scope, p, testObject(`{"content_html":"<p>x</p>","version":4}`)); err != nil {
		t.Fatal(err)
	}
	p.IsLocked = true
	if err := authorizeEdit(scope, p, testObject(`{"content_html":"<p>x</p>","version":4}`)); err == nil {
		t.Fatal("even owner must explicitly unlock before editing")
	}
	p.IsLocked = false
	now := time.Now()
	p.ArchivedAt = &now
	if err := authorizeEdit(scope, p, testObject(`{"content_html":"<p>x</p>","version":4}`)); err == nil {
		t.Fatal("archived page was editable")
	}
}

func TestMemberCannotChangePageAccess(t *testing.T) {
	scope := identity.Scope{Actor: identity.Actor{UserID: uuid.New()}, Role: 15}
	p := page{OwnerID: uuid.New(), Version: 1}
	if err := authorizeEdit(scope, p, testObject(`{"is_private":true}`)); err == nil {
		t.Fatal("non-owner member changed access")
	}
}

func TestPagePrivacyBelongsToOwnerIncludingAfterDemotion(t *testing.T) {
	owner := uuid.New()
	p := page{OwnerID: owner, Version: 1}
	for _, role := range []identity.Role{identity.Member, identity.Admin} {
		scope := identity.Scope{Actor: identity.Actor{UserID: uuid.New()}, Role: role}
		if err := authorizeEdit(scope, p, testObject(`{"is_private":true}`)); err == nil {
			t.Fatalf("non-owner role %d changed page privacy", role)
		}
		if err := authorizeEdit(scope, p, testObject(`{"is_private":false}`)); err != nil {
			t.Fatalf("same privacy value should remain a harmless metadata update: %v", err)
		}
	}
	ownerScope := identity.Scope{Actor: identity.Actor{UserID: owner}, Role: identity.Guest}
	if err := authorizeEdit(ownerScope, p, testObject(`{"is_private":true,"name":"Owned page","version":1}`)); err != nil {
		t.Fatalf("active guest owner lost ownership rights: %v", err)
	}
}

func TestDocumentInputSanitizesHTMLAndRejectsMalformedBinary(t *testing.T) {
	values, err := parse(testObject(`{"content_html":"<script>alert(1)</script><p>Safe</p>","content_json":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Safe"}]}]}}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if values["content_html"] != "<p>Safe</p>" {
		t.Fatalf("unexpected HTML: %v", values["content_html"])
	}
	if _, err := parse(testObject(`{"content_html":"<p></p>","content_json":{"type":"doc","content":[]},"content_binary":"not:base64"}`), false); err == nil {
		t.Fatal("malformed collaborative state accepted")
	}
}

func TestContentReplacementClearsStaleCollaborationSnapshot(t *testing.T) {
	for _, raw := range []string{`{"content_html":"<p>lost</p>"}`, `{"content_json":{"type":"doc","content":[]}}`, `{"content_binary":"AA=="}`, `{"content_html":"<p></p>","content_json":{}}`} {
		if _, err := parse(testObject(raw), false); err == nil {
			t.Fatalf("incomplete content replacement accepted: %s", raw)
		}
	}
	values, err := parse(testObject(`{"content_html":"<p></p>","content_json":{"type":"doc","content":[]}}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if binary, ok := values["content_binary"]; !ok || binary != nil {
		t.Fatal("REST content replacement retained a stale collaboration snapshot")
	}
	metadata, err := parse(testObject(`{"name":"Renamed"}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := metadata["content_binary"]; ok {
		t.Fatal("metadata update discarded collaboration state")
	}
}
