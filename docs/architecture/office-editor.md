# Office editor (edit mode)

The organization can redecorate its office: wall and floor colors, lighting mood, workstation (desk + chair) styles and
position, and free placement of furniture bought in the shop. This page is the contract between the backend, the
catalog / 3D models and the editor UI.

## What is saved: the layout document

One JSON document per organization (`office_layout`), replaced as a whole by the editor's **Save** and versioned for
optimistic concurrency. Anything missing falls back to the built-in look, so an organization that never opens the editor
is unchanged.

```jsonc
{
  "v": 1,
  "walls":  { "color": "#f3f4f7", "trim": "#3a414c", "windows": true },  // hex colors; windows: glass bands on (default) or solid walls
  "floor":  { "kind": "concrete", "tint": "#e7eaee" },               // kind: concrete|wood|parquet|carpet|tile|marble
  "light":  { "mood": "natural", "temp": 0, "brightness": 1 },       // mood: natural|warm|cool|sunset|night (preset); temp -1 warm .. +1 cool; brightness 0.5..1.5
  "desks":  { "<agent_id>": { "x": -9.5, "z": -4, "desk": "desk_walnut", "chair": "chair_mesh_teal" } },  // every field optional
  "pieces": [ { "uid": "p_k3j2", "item": "sofa_chesterfield", "x": 2.5, "z": 3, "rot": 1.5708, "s": 1, "y": 0 } ],
  "zones":    { "meeting": { "x": 9.5, "z": -6.2 }, "approvals": { "x": 9.5, "z": 5.4 }, "cafe": { "x": 9.2, "z": 1.2 } },   // the three rooms moved (center of each)
  "fixtures": { "plant_a": { "x": -12.8, "z": -8.6, "rot": 0, "hidden": false }, "rack": { "x": -12.9, "z": -0.5, "rot": 1.5708 } }, // plant_a..plant_d, rack: the built-in decoration moved, turned or taken away
  "seeded": true                                                      // legacy slot items were copied into `pieces`
}
```

* `x`, `z`: floor position in world units (the floor spans x in [-14.2, 14.2], z in [-10, 10]; the north wall is at z = -10).
* `rot`: rotation about Y in radians (the editor snaps to multiples of 90 degrees but any value is stored). `s`: uniform scale 0.6..1.6. `y`: height above the floor, only for `mount: "wall"` pieces.
* `desks[agent].desk` / `.chair` are **styles**, not objects: the same owned `desk_*` / `chair_*` item can dress every workstation.
* `pieces[].item` must be an owned `kind: "office"` item whose slot is `piece` (free placement) or one of the legacy slots (`plant_1`, `lamp_1`, ...). An item appears at most once in `pieces`. `window_1` (the view behind the glass) stays a legacy slot, not a piece.
* Limits: at most 150 pieces, 60 desk entries, uids <= 24 chars `[A-Za-z0-9_-]`, coordinates within +-40, colors `#rgb`/`#rrggbb`, everything else rejected with 400.

### Legacy slots

The 12 pre-existing office slots (`plant_1`, `poster_1`, ...) and `PUT/DELETE /office/items/{slot}` keep working. While
`layout.seeded` is false the 3D office renders slot items at their fixed spots (as before). The first time the editor
saves, the client copies every placed slot item into `pieces` at that spot and sets `seeded: true`; from then on
`pieces` is authoritative and the slot items are ignored by the renderer.

## Backend

* Table `office_layout(org_id pk, doc jsonb, version int, updated_at, updated_by)`; RLS like the other org tables; migration `660_office_layout.sql`. It must be classified in `internal/tenantdata` (export and purge) or the schema guard test fails.
* `GET /api/v1/office/layout` (`shop:read`) returns `{ "layout": {...}|null, "version": n }`. The same document is also included in `GET /shop/state` as `layout` and `layout_version`.
* `PUT /api/v1/office/layout` (`shop:buy`) with `{ "layout": {...}, "base_version": n }`. `base_version` must equal the stored version (0 when none) or the answer is `409 {code:"layout_conflict", version, layout}` with the current document. Success returns `{ "layout", "version" }` (version + 1). Validation as above; items must be owned by the organization (`not_owned`), unknown ids are rejected (`invalid`).
* On success emit the realtime frame `office.layout_updated` (payload `{ version, actor }`, not the whole document) and audit `office.layout_updated`. The frontend re-fetches on that frame.
* Catalog slots for new items: `piece` (free placement), `desk`, `chair`. `Place`/`Remove` keep applying only to the legacy slots; the three new slots are used through the layout only. Ownership (`/shop/purchases`) is unchanged: an item is bought once.

## Catalog: new office categories

New `kind: "office"` categories (ids prefixed by the category): `desk`, `chair`, `table`, `sofa`, `cabinet`, `divider`,
`whiteboard`, `appliance`, `light`. Every item has `price`, `rarity`, `name`/`description` (es, en), `color` and often
`color2`. Slots: `desk` -> `desk`, `chair` -> `chair`, everything else -> `piece`. Wall-mounted: `whiteboard` and
`light` items of mount `wall`; `light` items may also hang from the ceiling.

The frontend knows the shape of each item from `furnitureStyle.ts` (specs per id) and draws it in
`Furniture.tsx`; the flat shop preview is drawn from the same spec.

```ts
// frontend/src/components/office/furnitureStyle.ts
export type Mount = "floor" | "wall" | "ceiling";
export function footprint(item: ShopItem): { w: number; d: number };   // meters on the floor, before rotation/scale
export function mountOf(item: ShopItem): Mount;
export function lightOf(item: ShopItem): { color: string; intensity: number; distance: number } | null; // null = decorative only

// frontend/src/components/office/Furniture.tsx  (origin at the floor-center of the footprint, front faces +z)
export function PieceMesh(props: { item: ShopItem }): JSX.Element;      // table, sofa, cabinet, divider, whiteboard, appliance, light
export function DeskSet(props: { item?: ShopItem; accent: string; children?: React.ReactNode }): JSX.Element; // desk top + legs + modesty panel; undefined = the current oak desk; children = monitor, keyboard, props
export function ChairMesh(props: { item?: ShopItem }): JSX.Element;    // undefined = the current graphite chair
```

`DeskSet` keeps the current desk footprint (2.2 x 0.95, top at height 0.72) so monitors and characters sit the same.

## Frontend

* `lib/officeLayout.ts` (zustand): `layout`, `version`, `draft`, `load()`, `startEdit()`, `save()`, `cancel()`, undo/redo, and pure helpers (`collides`, `snap`, `DEFAULT_LAYOUT`). Mock mode keeps the layout in memory/localStorage (`lib/mock/rewards-mock.ts`).
* `OfficeEnv` reads walls/floor/light from `draft ?? layout`; `Desk` reads its style and position; `OfficeItems` renders `pieces`.
* `OfficeEditor` (HUD + 3D interaction): placement ghost with snap, drag, rotate (R), delete (Del), grid, collision (red ghost), a no-build band along the walkway.
* Characters read their desk through `deskOf`, which now honors `layout.desks[agent].x/z`.
* Permissions: the editor needs `shop:buy`; everyone else sees the saved layout.

## Light

The room's light comes only from `layout.light` (mood preset, `temp`, `brightness`); the old per-user ambient / lights on /
night switch is gone. The HUD "Light" button edits it outside edit mode and saves right away (debounced, rebasing on a
conflict); inside the editor the same controls edit the draft.
