# Document API

Page collections are `/api/v1/workspaces/:workspaceID/projects/:projectID/pages`
and `/api/v1/workspaces/:workspaceID/pages`. The latter stores workspace pages.
Every JSON result uses `data`.

GET/POST collection; GET/PATCH/DELETE `/:pageID`. Fields are `name,content_html,
content_json,content_binary,parent_id,is_private,is_locked,archived,position,icon`.
`content_binary` is base64 Yjs state. `archived` is a write-only boolean; responses
contain `archived_at`. A content/title PATCH requires the current integer `version`.
Stale versions return 409 and never replace persisted content. Dates are RFC3339.
Content replacement requires both `content_html` and `content_json` (a Tiptap
`doc` with a `content` array). Supplying only one representation is rejected.
If `content_binary` is omitted during content replacement, the old Yjs state is
cleared so collaboration hydrates from the replacement JSON. Metadata-only edits
preserve binary state. Use live `/documents/convert` to convert standalone HTML.

GET `/:pageID/content` returns `name,content_html,content_json,content_binary,version`
and `owner_id,can_edit` plus access/lock/archive state. PUT on that path accepts the three content fields
plus required `version`. It returns the updated page including content and version.
The live server forwards the authenticated browser cookie; there is no trusted-user
header bypass. Lock/archive and private-page rules apply to these routes too.

POST `/:pageID/duplicate` creates a copy owned by the caller. GET `/:pageID/versions`
lists snapshots; GET `/:pageID/versions/:versionID` retrieves one. POST
`/:pageID/versions/:versionID/restore` with current `version` restores the chosen
content as a new version. Existing history is retained.

GET/POST `/:pageID/comments`, PATCH/DELETE `/:pageID/comments/:commentID` use
`body_html,body_json,parent_id`. Only the author edits a comment; page admins may
also remove comments. HTML is sanitized before storage. All referenced pages,
versions, parents and comments are scoped to the current workspace and project.

Private pages are visible only to their owner, and only their owner can change
page privacy. Members edit unlocked active public pages; an active owner retains
edit rights after becoming a guest. The owner/admin changes lock/archive. A page must first be archived
before deletion, and must have no active child pages.
Project pages require an explicit active project membership even if the project
itself is public. Guests see only their owned pages while the project's
`guest_can_view_all` is false (the default). Enabling it also permits reading
other project pages that are not private, without granting content edit rights.
Workspace guests are never elevated by project roles. Document
write transactions lock and recheck current account/workspace/project membership,
so a pending role change is committed before deciding whether the edit is allowed.
Version, comment and resource reads independently recheck current page access.

GET `/summary` returns `{public_pages,private_pages,archived_pages,total}` for
accessible root pages in the collection. Active private pages count only for
their owner; descendants are not double-counted.

GET `/:pageID/resources` returns `{page_id,version,links,headings,assets}`.
`links` entries are `{url,text,kind}` with `kind=link|image|embed`; `headings` are
`{level,text,id}` in document order. They are extracted from sanitized content.
`assets` includes completed, active page attachments as
`{id,filename,content_type,size_bytes,created_at,uploaded_by,download_url}` and
never exposes storage keys. Page privacy and current collection scope apply.
