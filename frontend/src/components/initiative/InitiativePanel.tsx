"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { initiativeApi, type Initiative, type InitiativeSettings } from "@/lib/initiativeApi";
import { Btn, Card, Dot, Empty, timeAgo, useAgentColor, useAgentName } from "../ui";

const REFRESH_MS = 30_000;

/**
 * Suggestions agents raised on their own ("the margin dropped 3 %, shall I look into it?"). They are only
 * notifications: accepting one submits a normal request (so approvals, caps and the kill switch still apply),
 * dismissing discards it. Hidden when the server does not offer the endpoint.
 */
export function InitiativePanel() {
  const { t } = useT();
  const name = useAgentName();
  const color = useAgentColor();
  const canAct = useCan("requests:create");
  const [items, setItems] = useState<Initiative[] | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");

  const load = useCallback(() => { initiativeApi.list("pending").then(setItems).catch(() => setItems(null)); }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, REFRESH_MS);
    return () => clearInterval(id);
  }, [load]);
  if (items === null) return null;

  const act = async (it: Initiative, kind: "accept" | "dismiss") => {
    setBusy(it.id); setErr(""); setNote("");
    try {
      await initiativeApi[kind](it.id);
      setNote(t(kind === "accept" ? "initiative.accepted" : "initiative.dismissed"));
    } catch { setErr(t("initiative.error")); }
    setBusy(null);
    load();
  };

  return (
    <div data-testid="initiative-panel">
      <Card title={t("initiative.title")}>
        {items.length === 0 ? <Empty>{t("initiative.empty")}</Empty> : (
          <ul className="space-y-2">
            {items.map((it) => (
              <li key={it.id} data-testid={`initiative-${it.id}`} className="rounded-md border border-line bg-panel2 p-3">
                <div className="mb-1 flex items-center justify-between gap-2 text-[11px] text-mute">
                  <span className="flex items-center gap-1.5"><Dot color={color(it.agent_id)} />{t("initiative.from", { agent: name(it.agent_id) })}</span>
                  <span>{timeAgo(it.created_at)}</span>
                </div>
                <div className="text-[13px] font-semibold text-ink">{it.title}</div>
                {it.body && <p className="mt-1 line-clamp-4 break-words text-[12px] leading-snug text-ink2 [overflow-wrap:anywhere]">{it.body}</p>}
                {canAct ? (
                  <div className="mt-2.5 flex items-center gap-2">
                    <Btn kind="primary" disabled={busy === it.id} data-testid={`initiative-accept-${it.id}`} onClick={() => act(it, "accept")}>{t("initiative.accept")}</Btn>
                    <Btn disabled={busy === it.id} data-testid={`initiative-dismiss-${it.id}`} onClick={() => act(it, "dismiss")}>{t("initiative.dismiss")}</Btn>
                  </div>
                ) : <div className="mt-2 text-[11px] text-mute">{t("initiative.noPermission")}</div>}
              </li>
            ))}
          </ul>
        )}
        <p className="mt-3 text-[11px] text-mute">{t("initiative.hint")}</p>
        {note && <div role="status" className="mt-2 text-[11px] font-semibold text-emerald-700">{note}</div>}
        {err && <div role="alert" className="mt-2 text-[11px] text-red-700">{err}</div>}
      </Card>
    </div>
  );
}

const num = (v: string) => Math.max(0, Math.min(1000, Math.floor(Number(v) || 0)));

/** Initiative budget of the organization (owner/admin): how many suggestions agents may raise per day and month. */
export function InitiativeBudgetCard() {
  const { t } = useT();
  const canManage = useCan("org:manage");
  const [cfg, setCfg] = useState<InitiativeSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");

  useEffect(() => { if (canManage) initiativeApi.settings().then(setCfg).catch(() => setCfg(null)); }, [canManage]);
  if (!canManage || !cfg) return null;

  const save = async () => {
    setBusy(true); setErr(""); setNote("");
    try { setCfg(await initiativeApi.putSettings(cfg)); setNote(t("initiative.budget.saved")); } catch { setErr(t("initiative.error")); }
    setBusy(false);
  };

  return (
    <div data-testid="initiative-budget-card">
      <Card title={t("initiative.budget.title")}>
        <div className="space-y-3 text-xs">
          <p className="text-mute">{t("initiative.budget.help")}</p>
          <label className="flex items-center gap-2 font-semibold">
            <input type="checkbox" data-testid="initiative-enabled" checked={cfg.enabled} disabled={busy} onChange={(e) => setCfg({ ...cfg, enabled: e.target.checked })} />
            {t("initiative.budget.enabled")}
          </label>
          <div className="flex flex-wrap gap-4">
            <label className="flex items-center gap-2">
              {t("initiative.budget.daily")}
              <input type="number" min={0} max={1000} data-testid="initiative-daily" value={cfg.daily_limit} disabled={busy || !cfg.enabled}
                onChange={(e) => setCfg({ ...cfg, daily_limit: num(e.target.value) })}
                className="w-20 rounded-md border border-line-strong bg-panel px-2 py-1 text-sm text-ink outline-none focus:border-accent" />
            </label>
            <label className="flex items-center gap-2">
              {t("initiative.budget.monthly")}
              <input type="number" min={0} max={1000} data-testid="initiative-monthly" value={cfg.monthly_limit} disabled={busy || !cfg.enabled}
                onChange={(e) => setCfg({ ...cfg, monthly_limit: num(e.target.value) })}
                className="w-20 rounded-md border border-line-strong bg-panel px-2 py-1 text-sm text-ink outline-none focus:border-accent" />
            </label>
          </div>
          <div className="flex items-center gap-2">
            <Btn kind="primary" disabled={busy} data-testid="initiative-save" onClick={save}>{t("initiative.budget.save")}</Btn>
            {note && <span role="status" className="text-emerald-700">{note}</span>}
            {err && <span role="alert" className="text-red-700">{err}</span>}
          </div>
        </div>
      </Card>
    </div>
  );
}
