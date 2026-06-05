# PRD: Split API Surfaces

## Status

Draft — 2026-05-16
**Stakeholders:** llama-swap maintainers, API client users [assumed]

## Problem Statement

llama-swap supports both OpenAI-compatible and Anthropic-compatible endpoints. This compatibility is powerful, but users configuring tools can be confused when both API families share similar base URLs or when a client expects an explicit provider-specific base path.

Providing clearer split API surfaces would make llama-swap easier to integrate with CLI agents and reduce ambiguity between OpenAI and Anthropic modes. Existing routes must continue to work to avoid breaking current users, but they should be documented as obsolete shared paths so new integrations prefer the provider-specific surfaces.

## Goals

- Make OpenAI-compatible and Anthropic-compatible endpoints explicit in docs, UI, and routing.
- Preserve existing endpoint compatibility while marking shared `/v1` routes obsolete for new integrations.
- Allow integrations to generate less ambiguous config.
- Keep model selection and swapping behavior consistent across API families.

## Target Users / Personas

CLI agent user [assumed]: configures tools that distinguish between OpenAI and Anthropic providers.

API client developer [assumed]: writes local scripts against llama-swap and wants stable, obvious base URLs.

Operator [assumed]: exposes llama-swap to a small local network and wants to document correct endpoints for users.

## Scope

### In Scope

- Display explicit OpenAI and Anthropic base URLs in the UI.
- Document both API surfaces with examples.
- Add canonical provider-specific routes under `/openai/v1` and `/anthropic/v1`.
- Preserve existing `/v1/...` behavior as obsolete compatibility routes.
- Ensure request routing extracts model names correctly from both surfaces.
- Ensure canonical provider routes only expose endpoints that match that provider schema.

### Out of Scope

- Removing existing `/v1` endpoints.
- Adding cross-provider aliases such as `/openai/v1/messages` or `/anthropic/v1/chat/completions`.
- Translating every OpenAI feature to Anthropic or every Anthropic feature to OpenAI.
- Changing upstream server protocol behavior.
- Introducing provider-specific authentication in the first version.

## Functional Requirements

1. **Explicit API Surface Display**
   1.1 Users can see OpenAI-compatible and Anthropic-compatible base URLs in the dashboard.
   1.2 Users can copy base URLs and minimal curl examples.

2. **Route Compatibility**
   2.1 Existing OpenAI-compatible routes continue to work.
   2.2 Existing Anthropic-compatible routes continue to work.
   2.3 Existing shared `/v1` routes are marked obsolete in docs and UI copy for new integrations.
   2.4 Canonical split routes map to the same internal handlers as existing compatible routes.
   2.5 Canonical split routes are provider-specific: OpenAI routes only expose OpenAI-compatible endpoints, and Anthropic routes only expose Anthropic-compatible endpoints.

3. **Configuration**
   3.1 Generated integration snippets prefer canonical provider paths.
   3.2 Shared `/v1` paths remain available for existing configurations but are not the preferred generated output.

4. **Documentation**
   4.1 Documentation includes endpoint tables for both API families.
   4.2 Documentation explains compatibility between obsolete shared paths and canonical provider paths.

## Non-Functional Requirements (summary)

The change must be backward compatible. Route aliases should add negligible overhead and must not duplicate business logic. Provider-prefixed paths outside the canonical route table should use the server's default behavior for unimplemented endpoints.

## User Journeys / Key Flows

1. User opens Integrations, selects Anthropic mode, and copies `http://localhost:8080/anthropic/v1` as the base URL.
2. Existing user continues using obsolete-compatible `http://localhost:8080/v1/chat/completions` without changes.
3. API developer checks the dashboard and sees the canonical base URL for each endpoint family.
4. User attempts `http://localhost:8080/anthropic/v1/chat/completions`; because chat completions belongs to the OpenAI-compatible surface, llama-swap does not register that route and the server returns its default unimplemented endpoint response.

## Assumptions & Dependencies

| Item                   | Type       | Detail                                                                      |
| ---------------------- | ---------- | --------------------------------------------------------------------------- |
| Backward compatibility | Dependency | Existing `/v1` routes cannot break.                                         |
| Integration page       | Dependency | Split surfaces are most valuable when used by generated snippets.           |
| Route aliases          | Dependency | Provider-specific routes are canonical and must not duplicate handlers.      |

## Decisions

- Canonical OpenAI-compatible base path: `/openai/v1`.
- Canonical Anthropic-compatible base path: `/anthropic/v1`.
- Shared `/v1` routes remain available for compatibility but are obsolete for new integrations.
- Canonical provider routes only expose endpoints that match their provider schema.
