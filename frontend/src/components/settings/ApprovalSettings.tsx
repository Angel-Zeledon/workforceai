"use client";
import { useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { permissionsApi, statusOf, type PermissionsOverview } from "@/lib/permissionsApi";
import { exceptionLabel, type PlainLevel } from "@/lib/permissionsLogic";
import { Card, Empty, Loading } from "../ui";

/** Settings → Approvals: the level new agents start at and every active exception per agent (who and when), with remove. */
export function ApprovalSettings() {
  const { t } = useT();
  const router = useRouter();
  const openAgent = (id: string) => { useStore.getState().select(id); router.push("/"); };
  const [v, setV] = useState<PermissionsOverview | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try { setV(await permissionsApi.overview()); setErr(""); } catch { setErr("perm.error.load"); }
  }, []);
  useEffect(() => { void load(); }, [load]);

  const act = async (f: () => Promise<unknown>) => {
    setBusy(true); setErr("");
    try { await f(); await load(); } catch (e) { setErr(statusOf(e) === 403 ? "perm.error.forbidden" : "perm.error.action"); } finally { setBusy(false); }
  };

  if (!v) return <div className="mx-auto max-w-3xl px-4 py-4 sm:px-5 sm:py-6">{err ? <p className="text-xs text-err">{t(err)}</p> : <Loading rows={3} />}</div>;
  const rows = v.agents.flatMap((a) => [
    ...a.exceptions.map((e) => ({ key: `${a.agent_id}:x:${e.category}`, a, cat: e.category, label: exceptionLabel(e.mode, e.if).map((x) => t(x.key, x.vars)).join(", "),
      who: e.created_by ?? "", when: e.created_at, floor: false })),
    ...a.floor_lifts.map((l) => ({ key: `${a.agent_id}:f:${l.category}`, a, cat: l.category, label: t("perm.floor.liftedBadge", { by: l.by }), who: l.by, when: l.at, floor: true })),
  ]);
  const scopes = v.scopes ?? [];
  const nameOf = (id?: string) => (id ? v.agents.find((a) => a.agent_id === id)?.name ?? id : t("perm.scope.anyAgent"));
  const legacy = v.agents.filter((a) => !a.factory).length;

  return (
    <div data-testid="approval-settings" className="mx-auto max-w-3xl space-y-4 px-4 py-4 sm:px-6 sm:py-6">
      <Card title={t("perm.settings.default")}>
        <select data-testid="perm-default" disabled={!v.can_edit_default || busy} value={v.default_level}
          onChange={(e) => act(() => permissionsApi.setDefault(e.target.value as PlainLevel | ""))}
          className="w-full rounded-md border border-line-strong bg-panel px-2.5 py-1.5 text-[13px] text-ink disabled:opacity-50 sm:w-auto">
          <option value="">{t("perm.settings.defaultRole")}</option>
          {(["ask_all", "important", "alone"] as const).map((l) => <option key={l} value={l}>{t(`perm.level.${l}`)}</option>)}
        </select>
        <p className="mt-2 text-[11.5px] text-mute">{t("perm.settings.defaultHelp")}</p>
        {!v.can_edit_default && <p className="mt-1 text-[11.5px] text-mute">{t("perm.settings.adminOnly")}</p>}
      </Card>
      <Card title={t("perm.settings.list")}>
        {legacy > 0 && <p className="mb-3 text-[11.5px] text-mute">{t("perm.settings.legacyCount", { n: legacy })}</p>}
        {rows.length === 0 ? <Empty>{t("perm.settings.empty")}</Empty> : (
          <ul className="divide-y divide-line">
            {rows.map((r) => (
              <li key={r.key} data-testid={`perm-row-${r.key}`} className="flex flex-wrap items-center justify-between gap-2 py-2.5">
                <span className="min-w-0">
                  <button type="button" className="block text-left text-[12.5px] font-semibold text-ink hover:text-accent" onClick={() => openAgent(r.a.agent_id)}>{r.a.name}</button>
                  <span className="block text-[12px] text-ink2">{t(`perm.cat.${r.cat}`)} · <span className={r.floor ? "text-err" : ""}>{r.label}</span></span>
                  <span className="block text-[10.5px] text-mute">{t("perm.settings.by", { who: r.who, when: new Date(r.when).toLocaleString() })}</span>
                </span>
                <button type="button" disabled={busy} className="rounded-md border border-line-strong px-2.5 py-1 text-[11.5px] text-ink hover:bg-panel2 disabled:opacity-50"
                  onClick={() => act(() => r.floor ? permissionsApi.restoreFloor(r.a.agent_id, r.cat) : permissionsApi.removeException(r.a.agent_id, r.cat))}>
                  {r.floor ? t("perm.floor.restore") : t("perm.settings.revoke")}
                </button>
              </li>
            ))}
          </ul>
        )}
      </Card>
      {scopes.length > 0 && (
        <Card title={t("perm.scope.title")}>
          <ul className="divide-y divide-line">
            {scopes.map((g) => (
              <li key={g.id} data-testid={`perm-scope-${g.id}`} className="flex flex-wrap items-center justify-between gap-2 py-2.5">
                <span className="min-w-0">
                  <span className="block text-[12.5px] font-semibold text-ink">{nameOf(g.agent_id)}</span>
                  <span className="block text-[12px] text-ink2">{t(`perm.cat.${g.category}`)} · {t(`perm.scope.kind.${g.kind}`)} · {t("perm.scope.until", { date: new Date(g.expires_at).toLocaleDateString() })}</span>
                  <span className="block text-[10.5px] text-mute">{t("perm.settings.by", { who: g.created_by ?? "", when: new Date(g.created_at).toLocaleString() })}</span>
                </span>
                <button type="button" disabled={busy} className="rounded-md border border-line-strong px-2.5 py-1 text-[11.5px] text-ink hover:bg-panel2 disabled:opacity-50"
                  onClick={() => act(() => permissionsApi.revokeScope(g.id))}>{t("perm.settings.revoke")}</button>
              </li>
            ))}
          </ul>
        </Card>
      )}
      {err && <p role="alert" className="text-[11.5px] text-err">{t(err)}</p>}
    </div>
  );
}
