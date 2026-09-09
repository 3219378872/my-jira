# my-jira development rules

## Objective

Reproduce the implemented community features and visual interaction of the local
Plane reference at commit `1fec307f91003df96351557af32ce87891a3678a`.
The source reference is `/home/dev/found/plane`; it must remain unchanged.
The authoritative scope and completion criteria are in `docs/PLAN.md` and
`docs/FEATURE_PARITY.md`.

## Architecture

- React, TypeScript, Vite, React Router, MobX and Tiptap for the web clients.
- Go, Gin, Ent and PostgreSQL for the business API. No Django runtime dependency.
- Redis and Asynq for durable background work.
- Node, Yjs and Hocuspocus for document collaboration.
- S3-compatible object storage, Docker Compose and Caddy for deployment.
- Keep browser, public, instance and external API authorization distinct.
- Implement application code independently. Do not import, copy, translate or
  mechanically rewrite Plane frontend code, styles, translations, icons, images,
  brand assets, package manifests or copyright notices.
- Plane is a read-only feature/behavior reference. Use independently authored
  design tokens, components, client state, API clients and collaboration code.
- Ask the user about material scope or architecture choices that are not already
  settled. Do not ask again for routine implementation and validation authority.

## Delivery rules

- Read-only discovery before changing a reference contract.
- A route stub, empty success response, menu, or screenshot is not a completed feature.
- Track implemented behavior, automated checks, browser evidence and external-provider
  verification separately. Never mark feature parity from compilation alone.
- User data and unrelated repositories/services are outside the mutation scope.
- Integration tests must use an explicitly named, isolated test database.
- Keep secrets out of source, logs and evidence.
- Use versioned database migrations. Do not mutate a production schema at startup.
- Test tenant isolation, membership changes, concurrent writes and rollback where applicable.
- Keep visual checks reproducible with fixed viewport, theme and fixture data.
- Add new implementation files using apply_patch; retain generated-file provenance.

## Parallel ownership

The primary agent owns root documentation, integration and deployment files.
Frontend work owns the independently authored `apps/web`, shared UI packages
and frontend toolchain manifests. Backend ownership is assigned by module; only
one agent runs Ent generation or changes shared schemas at a time.
