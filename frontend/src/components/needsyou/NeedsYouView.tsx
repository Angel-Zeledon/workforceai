"use client";
import { useEffect, useState } from "react";
import Link from "next/link";
import { useT } from "@/lib/i18n";
import { api } from "@/lib/api";
import { initiativeApi } from "@/lib/initiativeApi";
import { signalText } from "@/lib/initiativeLogic";
import { go, openInOffice } from "@/lib/navBridge";
import { useStore } from "@/lib/store";
import { useNeedsYou } from "@/lib/needsYou";
import { actionsOf, filterKind, kindKey, kindTabs, titleRef, type NeedsYouAction, type NeedsYouItem } from "@/lib/needsYouLogic";
import { PageHeader } from "../nav/ui2";
import { Btn, Empty, Loading, timeAgo } from "../ui";

/**
 * "Te necesitan" (docs/architecture/needs-you.md): everything that waits for this person in one list, the most urgent
 * and the oldest first. The server decides what is listed (departments, who decides); safe actions are offered here,
 * the rest opens the screen where it is resolved.
 */
export function NeedsYouView() {
  const { t } = useT();
  const result = useNeedsYou((s) => s.result);
  const error = useNeedsYou((s) => s.error);
  const [kind, setKind] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [note, setNote] = useState<{ ok: boolean; text: string } | null>(null);
  const select = useStore((s) => s.select);

  useEffect(() => useNeedsYou.getState().watch(), []);

  const tabs = result ? kindTabs(result) : [];
  const shown = result ? filterKind(result.items, kind) : [];
  useEffect(() => { if (kind && result && !(result.counts[kind] > 0)) setKind(""); }, [kind, result]);

  const act = async (it: NeedsYouItem, a: NeedsYouAction) => {
    setBusy(`${it.kind}:${it.id}`); setNote(null);
    try {
      if (a === "approve" || a === "reject") await api.decide(it.id, a);
      else await initiativeApi[a](it.id);
      useNeedsYou.getState().drop(it.kind, it.id);
      setNote({ ok: true, text: t(`needsYou.done.${a}`) });
    } catch {
      setNote({ ok: false, text: t("needsYou.error.action") });
    }
    setBusy(null);
    void useNeedsYou.getState().load();
  };
  const open = (it: NeedsYouItem) => {
    if (it.kind === "trust" && it.agent_id) openInOffice(it.agent_id, select); // the trust card lives in the agent's panel
    else go(it.target || "/");
  };

  return (
    <div data-testid="needs-you" className="h-full overflow-y-auto bg-bg">
      <PageHeader testid="needs-you-header" title={t("needsYou.title")} description={t("needsYou.description")}
        actions={<Link href="/approvals/inbox" data-testid="needs-you-fast" className="text-[12.5px] font-medium text-accent hover:underline">{t("needsYou.fastApprovals")}</Link>} />
      <div className="mx-auto w-full max-w-3xl px-4 py-4 sm:px-6">
        {tabs.length > 1 && (
          <div role="tablist" aria-label={t("needsYou.filter")} className="mb-3 flex flex-wrap gap-1.5">
            {tabs.map((tb) => (
              <button key={tb.kind || "all"} type="button" role="tab" aria-selected={kind === tb.kind} data-testid={`needs-you-tab-${tb.kind || "all"}`}
                onClick={() => setKind(tb.kind)}
                className={`min-h-9 rounded-full border px-3 text-[12.5px] font-medium transition ${kind === tb.kind ? "border-accent bg-accent text-white" : "border-line-strong bg-panel text-ink hover:bg-panel2"}`}>
                {tb.kind ? t(kindKey(tb.kind)) : t("needsYou.all")} <span className="ml-1 opacity-75">{tb.count}</span>
              </button>
            ))}
          </div>
        )}

        {note && <div role={note.ok ? "status" : "alert"} className={`mb-3 text-[12.5px] ${note.ok ? "text-ok" : "text-err"}`}>{note.text}</div>}
        {result?.unavailable?.length ? <p data-testid="needs-you-partial" className="mb-3 text-[12px] text-mute">{t("needsYou.partial")}</p> : null}

        {!result && !error && <Loading rows={4} />}
        {!result && error && <p role="alert" className="text-[13px] text-err">{t("needsYou.error.load")}</p>}
        {result && result.total === 0 && (
          <Empty testid="needs-you-empty" icon="bell" title={t("needsYou.empty.title")}>{t("needsYou.empty.body")}</Empty>
        )}

        {shown.length > 0 && (
          <ul className="space-y-2" data-testid="needs-you-list">
            {shown.map((it) => (
              <Row key={`${it.kind}:${it.id}`} it={it} busy={busy === `${it.kind}:${it.id}`} onAct={(a) => act(it, a)} onOpen={() => open(it)} />
            ))}
          </ul>
        )}
        {result?.truncated && <p className="mt-3 text-[12px] text-mute">{t("needsYou.truncated", { shown: result.items.length, total: result.total })}</p>}
      </div>
    </div>
  );
}

function Row({ it, busy, onAct, onOpen }: { it: NeedsYouItem; busy: boolean; onAct: (a: NeedsYouAction) => void; onOpen: () => void }) {
  const { t } = useT();
  const title = (() => {
    if (it.kind === "suggestion") {
      const ref = signalText(it.signal || "", it.params);
      if (ref) return t(ref.title.key, ref.title.vars);
    }
    const r = titleRef(it);
    if ("text" in r) return r.text;
    return t(r.key, { ...r.vars, level: r.vars.level ? t(r.vars.level) : "" });
  })();
  const meta = [it.agent_name, it.office_name, it.project_name].filter(Boolean).join(" · ");
  const actions = actionsOf(it);
  const label: Record<NeedsYouAction, string> = {
    approve: t("needsYou.act.approve"), reject: t("needsYou.act.reject"), accept: t("needsYou.act.accept"), dismiss: t("needsYou.act.dismiss"),
  };
  return (
    <li data-testid={`needs-you-item-${it.kind}-${it.id}`} data-kind={it.kind} data-urgency={it.urgency}
      className={`rounded-[10px] border bg-panel p-3 sm:p-3.5 ${it.urgency === "high" ? "border-l-[3px] border-line border-l-accent" : "border-line"}`}>
      <div className="mb-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px]">
        <span className="rounded-full bg-accent/10 px-2 py-0.5 font-semibold text-accent">{t(kindKey(it.kind))}</span>
        {it.urgency === "high" && <span className="font-semibold text-ink">{t(it.risk === "high" || it.double ? "needsYou.urgent.risk" : "needsYou.urgent.waiting")}</span>}
        <span className="ml-auto text-mute">{timeAgo(it.created_at)}</span>
      </div>
      <div className="break-words text-[13.5px] font-semibold leading-snug text-ink [overflow-wrap:anywhere]">{title}</div>
      {it.detail && <p className="mt-0.5 line-clamp-2 break-words text-[12.5px] leading-snug text-ink2 [overflow-wrap:anywhere]">{it.detail}</p>}
      {meta && <p className="mt-0.5 truncate text-[12px] text-mute">{meta}</p>}
      <div className="mt-2.5 flex flex-wrap items-center gap-2">
        {actions.map((a, i) => (
          <Btn key={a} kind={i === 0 && (a === "approve" || a === "accept") ? "primary" : "ghost"} disabled={busy} data-testid={`needs-you-${a}-${it.id}`} onClick={() => onAct(a)}>{label[a]}</Btn>
        ))}
        <Btn disabled={busy} data-testid={`needs-you-open-${it.id}`} onClick={onOpen}>{t(`needsYou.open.${it.kind === "approval" || it.kind === "question" || it.kind === "trust" || it.kind === "suggestion" ? it.kind : "other"}`)}</Btn>
      </div>
    </li>
  );
}
