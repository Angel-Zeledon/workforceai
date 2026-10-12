# Coins and the shop

Agents earn coins for the work they really finish. The organization spends them
on cosmetic items for the characters and the office. Nothing here changes what
an agent can do or how fast it works.

## Product rules (never relaxed)

- Coins are earned **only by real work** (a finished task, a finished project).
- Coins can **never be bought with real money**: no endpoint, job or setting
  credits coins other than the earning rules below. There are no gems and no
  pay-to-speed.
- The real dollar cost of the AI stays visible, separately, in the cost view.
  Coins are shown with their own icon and are never mixed with `$`.
- Nothing is invented: a coin exists only as a ledger entry that points to the
  task or project that earned it.

## Where it lives

| Piece | Location |
|---|---|
| Earning rules, catalog, service, observer | `backend/internal/rewards` |
| Catalog (data, es/en) | `backend/internal/rewards/catalog.json` |
| Postgres store, migration `530_rewards.sql` (+ down) | `backend/internal/infrastructure/postgres/rewards.go` |
| REST API | `backend/internal/api/rewards.go` (see `08-api.md` section 20) |
| WS events | `coins.earned`, `shop.purchased`, `item.equipped`, `item.unequipped`, `office.item_placed`, `office.item_removed` (see `03-eventos-realtime.md`) |

Without `DATABASE_URL` an in-memory store with the same code path is used.

## Ledger and wallet

- `coin_ledger` is **append-only** (the application role has no UPDATE/DELETE).
  Each entry: `kind` (`earn`|`spend`), positive `amount`, `reason`, `ref_kind`/`ref_id`
  (task, project or item), `agent_id`, `actor`, `detail` (base, bonus, complexity,
  reworks), `balance_after`, per-organization gapless `seq`.
- `coin_wallets` is the running balance of the organization (`CHECK balance >= 0`).
  The wallet row is locked for every earn and purchase of an organization, so
  parallel requests are serialized: a balance never goes negative and the
  sequence has no gaps. The coins are the organization's; the per-agent figure
  (`earned_by_agent`) is display only.
- **Idempotency**: the ledger key is unique per organization. A task pays with
  key `earn:task:<task id>`, a project with `earn:project:<project id>`, a
  purchase with `buy:<item id>` or the client's `Idempotency-Key`. Retries,
  restarts, replayed events and parallel duplicates are harmless.
- RLS by organization on every table (`tenant_isolation`).

## How tasks pay

The `Observer` decorates the event publisher: after `task.completed` and
`project.status_changed` went out unchanged, it pays in a background goroutine.
Earning never blocks or slows task execution and a failure only logs.

Earning rules (`rewards.Rules`, defaults in `DefaultRules`):

| Case | Coins |
|---|---|
| Task finished, complexity S / M / L / XL | 5 / 10 / 20 / 40 (`base`). Tasks without a complexity (plain requests such as the $50,000 scenario) count as `M` |
| Reviewed (Q1) and passed on the first try | base + 50 % (`first_try_bonus_pct`) |
| Reviewed and needed rework (or rework exhausted) | 50 % of base (`rework_pct`) |
| Project finished `done` with at least 2 finished tasks | 20 + 2 per finished task, at most 100 |
| Failed, blocked, skipped, cancelled tasks | 0 (no `task.completed` fires) |
| Milestones, human gates, waits | 0 (no work was done) |
| Task with an approval a human **rejected** | 0 |
| Review verdict `fail` | 0 |

Complexity comes from the project node (`projects.Service.RewardInfo`).
Tasks of the review are unreviewed unless the project turned the quality review on, so no bonus is
invented for work nobody judged.

Configuration (environment): `REWARDS_ENABLED=false` switches earning off;
`REWARDS_RULES` is a JSON override of any rule field (`enabled`, `base` per
complexity, `default_complexity`, `first_try_bonus_pct`, `rework_pct`,
`project_base`, `project_per_task`, `project_max`, `min_project_tasks`); invalid
values are refused and the defaults stay. The active rules are shown by
`GET /rewards/rules`.

## Shop

The catalog is data (`catalog.json`): about 60 items, names and descriptions in
es/en, a price in coins and a cosmetic rarity (`common`, `rare`, `epic`) that
changes nothing but the flavor. Each item lives in a **slot**:

- Character slots: `outfit` (jacket color), `hat`, `face` (glasses), `ears`
  (headphones), `neck` (scarf, bow tie), `badge`, `buddy` (small companion on the shoulder).
- Office slots (one item each, fixed positions): `plant_1`, `plant_2`, `lamp_1`, `rug_1`,
  `coffee_1`, `poster_1`, `poster_2`, `shelf_1`, `aquarium_1`, `arcade_1`, `trophy_1`, `window_1`.

An organization owns each item once. Buying spends the coins atomically
(ledger entry + wallet + inventory in one transaction). Equipping puts an owned
item on an agent (it replaces what that slot held); placing puts an owned
office item in its slot. Members and above can buy, equip and place; viewers
read. Everything is audited with the person as the actor.

## Frontend

Header coin counter (next to, and visibly different from, the dollar cost), a
floating "+N" over the agent that finished a task (respects reduce-motion), the
Shop screen (categories, preview, price, owned/equipped, buy/equip), the coin
history, items rendered on the procedural characters and in the office. Mock mode earns
coins when mock tasks complete and supports buying and equipping.
