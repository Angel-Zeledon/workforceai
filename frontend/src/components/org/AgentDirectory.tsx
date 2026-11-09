"use client";
import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useT } from "@/lib/i18n";
import { officesApi } from "@/lib/officesApi";
import { directoryGroups, visibilityKey, type DirectoryEntry, type OfficesResponse, type PersonRef } from "@/lib/officesLogic";
import { AgentAccessDialog } from "./AgentAccessDialog";
import { inputCls } from "./shared";

/** E2 directory of agents: mine, my office's, shared with me, the company's and the ones I only see (open offices),
 *  with their owner, office and who may use them. */
export function AgentDirectory({ people }: { people: PersonRef[] }) {
  const { t } = useT();
  const [entries, setEntries] = useState<DirectoryEntry[] | null>(null);
  const [offices, setOffices] = useState<OfficesResponse | null>(null);
  const [q, setQ] = useState("");
  const [error, setError] = useState(false);
  const [edit, setEdit] = useState<DirectoryEntry | null>(null);

  const load = useCallback(() => {
    setError(false);
    Promise.all([officesApi.directory(), officesApi.list()]).then(([d, o]) => { setEntries(d.agents); setOffices(o); }).catch(() => setError(true));
  }, []);
  useEffect(load, [load]);

  if (error) return <div role="alert" className="text-sm font-semibold text-red-600">{t("offices.error.load")} <button type="button" onClick={load} className="underline">{t("org.retry")}</button></div>;
  if (!entries || !offices) return <div className="text-sm text-mute">{t("org.loading")}</div>;
  const groups = directoryGroups(entries, offices.me, q);

  return (
    <div className="grid gap-4" data-testid="agent-directory">
      <div className="flex flex-wrap items-center gap-3">
        <p className="flex-1 text-[13px] text-mute">{t("offices.dir.intro")}</p>
        <input type="search" data-testid="agent-directory-search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("offices.dir.search")} className={`${inputCls} max-w-xs`} />
      </div>
      {groups.length === 0 && <p className="text-[13px] text-mute" data-testid="agent-directory-empty">{t(q.trim() ? "offices.dir.empty" : "offices.dir.none")}</p>}
      {groups.map((g) => (
        <section key={g.group} data-testid={`agent-directory-${g.group}`}>
          <h3 className="mb-1.5 text-[12px] font-semibold uppercase tracking-wide text-mute">{t(`offices.dir.group.${g.group}`)}</h3>
          <ul className="grid gap-1.5">
            {g.items.map((e) => (
              <li key={e.id} data-testid={`agent-dir-row-${e.id}`} className="flex flex-wrap items-center justify-between gap-2 rounded-[10px] border border-line bg-panel px-3 py-2">
                <span className="min-w-0">
                  <span className="text-[13px] font-semibold">{e.name}</span> <span className="text-[11px] text-mute">{e.title}</span>
                  <span className="mt-0.5 flex flex-wrap gap-x-3 text-[11px] text-mute">
                    <span>{e.owner_kind === "person" ? t("offices.dir.ownerPerson", { name: e.owner_name || "?" }) : t("offices.dir.ownerOffice", { name: e.office_id === "general" ? t("offices.general") : e.office_name })}</span>
                    <span>{t(visibilityKey(e))}</span>
                    {e.departed && <span className="font-semibold text-amber-700">{t("offices.dir.departed")}</span>}
                  </span>
                </span>
                <span className="flex items-center gap-2">
                  {e.rights.use ? <Link href={`/chats/${encodeURIComponent(e.id)}`} prefetch={false} data-testid={`agent-dir-chat-${e.id}`} className="text-[12px] font-semibold text-accent hover:underline">{t("offices.dir.chat")}</Link>
                    : <span className="text-[11px] text-mute">{t("offices.dir.onlySee")}</span>}
                  {e.rights.edit && <button type="button" data-testid={`agent-dir-access-${e.id}`} onClick={() => setEdit(e)} className="rounded-md border border-line px-2 py-1 text-[11px] font-semibold text-mute hover:text-ink">{t("offices.dir.access")}</button>}
                </span>
              </li>
            ))}
          </ul>
        </section>
      ))}
      {edit && <AgentAccessDialog entry={edit} offices={offices} people={people} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); load(); }} />}
    </div>
  );
}
