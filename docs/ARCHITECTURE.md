# Architecture decisions

## ADR-001: independently implement the observed functionality

The user explicitly requires development from scratch without using Plane's
frontend code or copyright notices. Independently author all components, design
tokens, text, state stores, API clients and collaboration adapters. Do not copy,
translate or mechanically transform reference implementation code or assets.

The fixed Plane reference defines the functionality and desired visual character.
Behavioral parity is distinct from HTTP wire compatibility. The user confirmed
that existing Plane API consumers and stored data do not need compatibility.
Design the client contract and database independently; implement an external API
as a product capability without a legacy compatibility layer.

## ADR-002: one modular Go business backend

Use Gin and Ent/PostgreSQL. Separate domain logic from browser, external API,
public sharing, authentication and instance-administration adapters. Concrete
routes use the independent `/api/v1` contract documented in `API_CONTRACT.md`.

Workspace and project membership are checked at the authority that loads or
changes each resource. Related IDs must be checked in the same scope. Explicit
transactions cover sequence allocation, relationship changes and durable effects.

## ADR-003: shared entity state

Keep MobX entity maps and view indexes. A work item has one authoritative client
entity; list membership, pagination and grouping are separate view state. Server
responses, optimistic updates and failure rollback use the same mutation path.

## ADR-004: collaboration is a document capability

Hocuspocus/Yjs owns collaborative document synchronization, while Go owns access
decisions and persistent storage APIs. Keep binary Yjs state and the source
document schema recoverable. Derived HTML does not replace the collaboration state.

## ADR-005: staged evidence

Static contract coverage, implementation, integration tests, browser tests, visual
comparison and real-provider results are different evidence classes. Readiness
or a successful build never closes a functional parity item on its own.
