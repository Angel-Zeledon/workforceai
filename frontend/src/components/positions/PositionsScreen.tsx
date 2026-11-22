"use client";
import Link from "next/link";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useT } from "@/lib/i18n";
import { positionError, positionsApi, type PositionsView, type TemplateProposal } from "@/lib/positions";
import { orderTree, templateNameKey, type LibraryEntry, type Position } from "@/lib/positionsLogic";
import { Btn, Card, Empty, Loading } from "../ui";
import { Icon } from "../icons";
import { PositionEditor } from "./PositionEditor";

type Sel = { kind: "none" } | { kind: "new" } | { kind: "edit"; id: string };
type Seed = { name: string; reports_to: string; skills: string[]; library: LibraryEntry[]; template: Position["template"] };

/** "Puestos" of a department: the positions in their hierarchy, who holds each, and the editor of a position's
 * package (skills + folder library). Read-only for whoever cannot manage the department. */
export function PositionsScreen({ officeId }: { officeId: string }) {
  const { t } = useT();
  const [view, setView] = useState<PositionsView | null>(null);
  const [templates, setTemplates] = useState<TemplateProposal[]>([]);
  const [error, setError] = useState("");
  const [fatal, setFatal] = useState(false);
  const [busy, setBusy] = useState(false);
  const [sel, setSel] = useState<Sel>({ kind: "none" });

  const load = useCallback(async () => {
    try {
      const v = await positionsApi.list(officeId);
      setView({ ...v, positions: v.positions ?? [], agents: v.agents ?? [], skills: v.skills ?? [], bounds: { ...v.bounds, shared_in: v.bounds?.shared_in ?? [] } });
      setFatal(false); setError("");
    } catch (e) { setFatal(true); setError(positionError(e, t)); }
  }, [officeId, t]);
  useEffect(() => { setView(null); setSel({ kind: "none" }); void load(); }, [load]);
  useEffect(() => {
    let live = true;
    positionsApi.templates(officeId).then((x) => { if (live) setTemplates(x); }).catch(() => { if (live) setTemplates([]); });
    return () => { live = false; };
  }, [officeId]);

  const rows = useMemo(() => orderTree(view?.positions ?? []), [view]);
  const [seed, setSeed] = useState<Seed | null>(null);
  const holders = (p: Position) => (view?.agents ?? []).filter((a) => a.position_id === p.id).map((a) => a.name);
  const nameOf = (id: string) => view?.positions.find((p) => p.id === id)?.name ?? "";

  const fromTemplate = async (tp: TemplateProposal) => {
    if (!view) return;
    setBusy(true); setError("");
    try {
      const name = t(templateNameKey(tp.key), { dept: view.office.name });
      const created = await positionsApi.create(officeId, { name, ...tp.proposal, template: tp.key });
      await load(); setSeed(null); setSel({ kind: "edit", id: created.id });
    } catch (e) { setError(positionError(e, t)); }
    setBusy(false);
  };

  const dept = view?.office.name ?? "";
  const back = `/d/${encodeURIComponent(officeId)}`;
  const editing = sel.kind === "edit" ? view?.positions.find((p) => p.id === sel.id) ?? null : null;

  return (
    <div data-testid="positions-screen" className="mx-auto w-full max-w-5xl space-y-4 px-4 py-4 sm:px-6 sm:py-6">
      <header className="space-y-1">
        <Link href={back} data-testid="positions-back" className="inline-flex items-center gap-1 text-[12px] font-medium text-accent hover:underline">
          <Icon name="back" size={12} />{t("pos.back", { dept: dept || "…" })}
        </Link>
        <h1 className="font-display text-[20px] font-semibold tracking-tight text-ink sm:text-2xl">{t("pos.title")}</h1>
        {view && <p className="max-w-2xl text-[13px] leading-snug text-mute">{t("pos.subtitle", { dept })}</p>}
      </header>

      {!view && !fatal && <Loading rows={3} />}
      {fatal && (
        <div role="alert" className="space-y-2 rounded-[10px] border border-line px-4 py-3 text-[13px] text-ink">
          <p>{error}</p>
          <Btn onClick={() => { setFatal(false); setError(""); void load(); }}>{t("pos.retry")}</Btn>
        </div>
      )}

      {view && (
        <>
          {!view.can_manage && <p data-testid="positions-readonly" className="rounded-[10px] bg-panel2 px-3 py-2 text-[12px] text-ink2">{t("pos.readOnly")}</p>}
          {error && !fatal && <p role="alert" data-testid="positions-error" className="rounded-lg bg-red-500/10 px-2.5 py-2 text-[12px] text-red-700">{error}</p>}

          {view.can_manage && (
            <Card title={t("pos.fromTemplate")} right={<Btn kind="primary" data-testid="pos-new" className="inline-flex items-center gap-1" onClick={() => { setSeed(null); setSel({ kind: "new" }); }}><Icon name="plus" size={12} />{t("pos.new")}</Btn>}>
              <div className="grid gap-2 sm:grid-cols-3">
                {templates.map((tp) => (
                  <button key={tp.key} type="button" data-testid={`pos-template-${tp.key}`} disabled={busy} onClick={() => void fromTemplate(tp)}
                    className="rounded-[10px] border border-line px-3 py-2.5 text-left transition hover:border-accent hover:bg-accent-soft disabled:opacity-50">
                    <span className="block text-[13px] font-medium text-ink">{t(templateNameKey(tp.key), { dept })}</span>
                    <span className="block text-[11.5px] leading-snug text-mute">{t(`${templateNameKey(tp.key)}.hint`)}</span>
                  </button>
                ))}
              </div>
            </Card>
          )}

          <div className="grid gap-4 lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]">
            <div>
              {!rows.length ? (
                <Empty icon="users" title={t("pos.empty.title")} testid="positions-empty">{t("pos.empty.body")}</Empty>
              ) : (
                <ul data-testid="positions-list" className="space-y-1.5">
                  {rows.map(({ position: p, depth }) => {
                    const who = holders(p);
                    const on = sel.kind === "edit" && sel.id === p.id;
                    return (
                      <li key={p.id} style={{ marginLeft: Math.min(depth, 4) * 16 }}>
                        <button type="button" data-testid={`position-${p.id}`} aria-current={on} onClick={() => { setSeed(null); setSel({ kind: "edit", id: p.id }); }}
                          className={`w-full rounded-[10px] border px-3 py-2.5 text-left transition ${on ? "border-accent bg-accent-soft" : "border-line bg-panel hover:bg-panel2"}`}>
                          <span className="flex items-center gap-2">
                            {depth > 0 && <Icon name="arrow" size={11} className="shrink-0 text-mute" />}
                            <span className="min-w-0 flex-1 break-words text-[13.5px] font-medium text-ink">{p.name}</span>
                          </span>
                          <span className="mt-0.5 block text-[11.5px] text-mute">
                            {who.length ? t("pos.occupiedBy", { names: who.join(", ") }) : t("pos.vacant")}
                            {p.reports_to && nameOf(p.reports_to) ? ` · ${t("pos.reportsLine", { name: nameOf(p.reports_to) })}` : ""}
                          </span>
                          <span className="mt-0.5 block text-[11px] text-mute">
                            {t("pos.skillsN", { n: p.skills.length })} · {p.library.length ? t("pos.foldersN", { n: p.library.length }) : t("pos.noLibrary")}
                          </span>
                        </button>
                      </li>
                    );
                  })}
                </ul>
              )}
            </div>

            <div>
              {sel.kind === "new" && (
                <PositionEditor key={`new-${seed?.name ?? ""}`} view={view} position={null} seed={seed ?? undefined}
                  onSaved={async (id) => { await load(); if (id) setSel({ kind: "edit", id }); }} onClose={() => setSel({ kind: "none" })} />
              )}
              {sel.kind === "edit" && editing && (
                <PositionEditor key={`${editing.id}-${editing.updated_at}`} view={view} position={editing}
                  onSaved={async (id) => { await load(); if (!id) setSel({ kind: "none" }); }} onClose={() => setSel({ kind: "none" })} />
              )}
              {sel.kind === "none" && rows.length > 0 && <p className="rounded-[10px] border border-dashed border-line px-4 py-6 text-center text-[12.5px] text-mute">{t("pos.pick.hint")}</p>}
            </div>
          </div>
        </>
      )}
    </div>
  );
}
