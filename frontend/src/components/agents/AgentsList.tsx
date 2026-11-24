"use client";
import Link from "next/link";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { stateMeta } from "@/lib/meta";
import { officesApi } from "@/lib/officesApi";
import type { DirectoryEntry, OfficesResponse } from "@/lib/officesLogic";
import { askers, baseOffice, groupByOffice, officeFilterChoices, officesSummary } from "@/lib/agentOfficesLogic";
import { useAgentOffices } from "@/lib/useAgentOffices";
import { Avatar } from "../chat/ChatThread";
import { inputCls } from "../org/shared";
import { useCurrentOffice } from "@/lib/currentOffice";

/** "Agentes": every agent you can see in rows grouped by base office, with a search and an office filter. Each row says
 *  who the agent is, its office (and the others it also works in, pending ones marked), who may ask it, its state, and
 *  the two actions: talk to it and open its sheet. */
export function AgentsList() {
  const { t } = useT();
  const [entries, setEntries] = useState<DirectoryEntry[] | null>(null);
  const [offices, setOffices] = useState<OfficesResponse | null>(null);
  const [error, setError] = useState(false);
  const [q, setQ] = useState("");
  const [officeLocal, setOffice] = useState("");
  // the office in the header wins over this screen's own filter
  const { current, hasSelector } = useCurrentOffice();
  const office = hasSelector && current !== "all" ? current : officeLocal;
  const agents = useStore((s) => s.agents);
  const general = t("offices.general");

  const load = useCallback(() => {
    setError(false);
    Promise.all([officesApi.directory(), officesApi.list()]).then(([d, o]) => { setEntries(d.agents); setOffices(o); }).catch(() => setError(true));
  }, []);
  useEffect(load, [load]);

  const ids = useMemo(() => (entries ?? []).map((e) => e.id), [entries]);
  const multi = useAgentOffices(ids);

  const others = useMemo(() => {
    const out: Record<string, ReturnType<typeof officesSummary>["others"]> = {};
    for (const e of entries ?? []) out[e.id] = officesSummary(baseOffice(e), multi.views[e.id], general).others;
    return out;
  }, [entries, multi.views, general]);

  if (error) return <div role="alert" className="text-sm font-semibold text-red-600">{t("offices.error.load")} <button type="button" onClick={load} className="underline">{t("org.retry")}</button></div>;
  if (!entries || !offices) return <div className="text-sm text-mute">{t("org.loading")}</div>;

  const groups = groupByOffice(entries, { query: q, office, generalLabel: general, others });
  const choices = officeFilterChoices(entries, general);
  const showFilter = choices.length > 1 && !(hasSelector && current !== "all");

  return (
    <div className="grid gap-4" data-testid="agent-directory">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
        <input type="search" data-testid="agent-directory-search" aria-label={t("ag.search")} value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("ag.search")} className={`${inputCls} w-full min-w-0 sm:flex-1`} />
        {showFilter && (
          <select data-testid="agent-directory-office" aria-label={t("ag.filter.office")} value={officeLocal} onChange={(e) => setOffice(e.target.value)} className={`${inputCls} w-full sm:w-auto sm:min-w-[13rem] sm:max-w-xs`}>
            <option value="">{t("ag.filter.all")}</option>
            {choices.map((c) => <option key={c.id} value={c.id}>{c.label}</option>)}
          </select>
        )}
      </div>
      {groups.length === 0 && <p className="text-[13px] text-mute" data-testid="agent-directory-empty">{t(q.trim() || office ? "ag.empty" : "offices.dir.none")}</p>}
      {groups.map((g) => (
        <section key={g.office.id} data-testid={`agent-directory-${g.office.id}`} aria-labelledby={`ag-grp-${g.office.id}`}>
          <h3 id={`ag-grp-${g.office.id}`} className="mb-1 flex items-baseline gap-2 border-b border-line pb-1 text-[13px] font-semibold text-ink">
            {g.label}<span className="text-[12px] font-normal text-mute">{g.items.length}</span>
          </h3>
          <ul>
            {g.items.map((e) => {
              const view = multi.views[e.id];
              const who = askers(e, view);
              const state = agents[e.id]?.state;
              const meta = state ? stateMeta(state) : null;
              const extra = [...(others[e.id] ?? [])];
              return (
                <li key={e.id} data-testid={`agent-dir-row-${e.id}`} className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-line/70 py-2.5">
                  <Link href={`/agents/${encodeURIComponent(e.id)}`} prefetch={false} className="flex min-w-0 flex-1 basis-56 items-center gap-2.5 hover:text-accent">
                    <Avatar id={e.id} size={30} />
                    <span className="min-w-0">
                      <span className="block truncate text-[13.5px] font-semibold">{e.name}</span>
                      <span className="block truncate text-[12px] text-mute">{e.title}</span>
                    </span>
                  </Link>
                  <div className="min-w-0 flex-1 basis-56 text-[12px] leading-snug text-mute">
                    <div data-testid={`agent-dir-office-${e.id}`}><span className="text-ink2">{officesSummary(baseOffice(e), null, general).baseLabel}</span>
                      {extra.length > 0 && (
                        <span data-testid={`agent-dir-also-${e.id}`}> · {t("ag.row.alsoIn")} {extra.map((o, i) => (
                          <span key={o.id}>{i > 0 && ", "}{o.pending ? t("ag.row.pending", { name: o.label }) : o.label}</span>
                        ))}</span>
                      )}
                    </div>
                    <div data-testid={`agent-dir-asks-${e.id}`}>
                      {t("ag.row.asks")}: {t(`ag.asks.${who.kind}`, { n: who.people })}{who.extra.length > 0 && ` + ${who.extra.join(", ")}`}
                      {e.departed && <span className="ml-2 font-semibold text-amber-700">{t("offices.dir.departed")}</span>}
                    </div>
                  </div>
                  {meta && <span className="inline-flex items-center gap-1.5 text-[12px] text-ink2" data-testid={`agent-dir-state-${e.id}`}><span aria-hidden className="h-2 w-2 rounded-full" style={{ background: meta.color }} />{meta.label}</span>}
                  <span className="flex items-center gap-3 text-[12px] font-semibold">
                    {e.rights.use ? <Link href={`/chats/${encodeURIComponent(e.id)}`} prefetch={false} data-testid={`agent-dir-chat-${e.id}`} className="min-h-9 content-center text-accent hover:underline sm:min-h-0">{t("ag.row.talk")}</Link>
                      : <span className="text-[11px] font-normal text-mute">{t("offices.dir.onlySee")}</span>}
                    <Link href={`/agents/${encodeURIComponent(e.id)}`} prefetch={false} data-testid={`agent-dir-sheet-${e.id}`} className="min-h-9 content-center text-ink2 hover:text-accent hover:underline sm:min-h-0">{t("ag.row.sheet")}</Link>
                  </span>
                </li>
              );
            })}
          </ul>
        </section>
      ))}
    </div>
  );
}
