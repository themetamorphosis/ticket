# Progress — 2026-11-27

Repo: https://github.com/themetamorphosis/ticket (branch `main`).
Plan: Revision 7 (pasted, truncated in section 8). Rules: test-first, 100% coverage gates,
no ignore pragmas, real-PostgreSQL concurrency tests, local Conventional Commits, push
only manually after `make verify` passes. GitHub CI is ignored during development.

## Milestones

| Milestone | State |
|---|---|
| 1. Foundations (toolchain, config, API/worker skeleton, migrations, publish) | Done, pushed |
| Reservations (sharded inventory, holds, buyer booking flow) | Done, pushed |
| Payments (gateway interface, fake, bKash, PayStation, reconciliation, refunds, ledger) | Done, pushed |
| Ticket issuance and delivery (PDF + QR, outbox, email, SMS pickup) | Done, pushed |
| Staff (gate scanner, organizer dashboard, roles, audit, void/reissue/refund, revocation) | Done, pushed |

## Staff milestone

Steps 1–7 are done and pushed (5fb9b58..1280b41):

```
9ac1764 feat(admissions): admit each ticket exactly once across gates
b80e31a feat(ticketops): void, reissue and refund tickets with audit entries
9fcec19 feat(staffauth): invite staff and sign in with Argon2id and WebAuthn
19895ce feat(catalog): change capacity safely and edit event details
2f3b963 feat(ops): add dashboard read models and gate management
36a6da8 feat(api): staff listener with sign-in, scanner, dashboard and audit routes
0186378 feat(staff-web): staff sign-in, gate scanner and organizer dashboard
```

Test status at the last commit:
- All Go packages pass with the race detector at 100% statement coverage.
- `web/staff` has 73 tests and `web/buyer` has 71, both at 100% coverage.
- Lint is clean.

### Step 7: end-to-end gate check and `make verify` (done, committed as 1280b41)

- Staff site on 8089 serves pages; `/api/v1/ready` 200; `/api/v1/staff/me` 401 on 8089, 404 on 8088.
- `make staff-invite` works (roles: organizer, finance, support, gate); invite page renders.
- Staff security headers moved to `infra/nginx/staff-headers.inc` (nginx drops inherited add_header in locations).
- `make verify` passed.

**Next:** pick the next milestone from "Not started".


## Passkeys removed (not yet committed)

The owner chose password-only staff sign-in in all environments, overriding plan
section 7. Migration 00007 drops `webauthn_credential`. Routes are now
`/api/v1/staff/login` and `/api/v1/staff/invite/accept`. `make verify` passed.
With rate limits not started, nothing throttles password guessing.

## Not started

- Redis waiting room
- Rate limits
- `/basic/book` (no-JS booking)
- OG tag rewrite
- Shard rebalancing
- Real SMS adapter
- Infrastructure (Ansible, WireGuard, backups, …)
- Mutation-score gates (threshold not yet chosen)
- Contract tests
- Revision 6/7 items
- Launch and load testing
