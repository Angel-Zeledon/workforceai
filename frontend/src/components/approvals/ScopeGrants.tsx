"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { permissionsApi, type ScopeGrant } from "@/lib/permissionsApi";
import { Card } from "../ui";

/** "In this project / batch it does not ask for…": the scopes created with "approve and do not ask again", visible and
 *  revocable in the project and in the batch. Nothing shown when there are none. */
export function ScopeGrants({ projectId, batchId }: { projectId?: string; batchId?: string }) {
  const { t } = useT();
  const agents = useStore((s) => s.agents);
  const [items, setItems] = useState<ScopeGrant[]>([]);
  const [err, setErr] = useState(false);
  const load = useCallback(async () => {
    try { setItems(await permissionsApi.scopes({ project_id: projectId, batch_id: batchId })); setErr(false); } catch { setErr(true); }
  }, [projectId, batchId]);
  useEffect(() => { void load(); }, [load]);
  if (!items.length && !err) return null;
  return (
    <Card title={t("perm.scope.title")} className="mt-4">
      <p className="mb-2 text-[12px] text-mute">{t("perm.scope.hint")}</p>
      {err && <p role="alert" className="mb-2 text-[12px] text-err">{t("perm.error.load")}</p>}
      <ul className="divide-y divide-line">
        {items.map((g) => (
          <li key={g.id} data-testid={`scope-grant-${g.id}`} className="flex flex-wrap items-center justify-between gap-2 py-2 text-[12px]">
            <span className="min-w-0">
              <span className="block font-medium text-ink">{t(`perm.cat.${g.category}`)} · {g.agent_id ? (agents[g.agent_id]?.name ?? g.agent_id) : t("perm.scope.anyAgent")}</span>
              <span className="block text-[11px] text-mute">{t(`perm.scope.kind.${g.kind}`)} · {t("perm.scope.until", { date: new Date(g.expires_at).toLocaleDateString() })} · {g.created_by}</span>
            </span>
            <button type="button" className="rounded-md border border-line-strong px-2.5 py-1 text-[11.5px] text-ink hover:bg-panel2"
              onClick={async () => { try { await permissionsApi.revokeScope(g.id); await load(); } catch { setErr(true); } }}>{t("perm.settings.revoke")}</button>
          </li>
        ))}
      </ul>
    </Card>
  );
}
