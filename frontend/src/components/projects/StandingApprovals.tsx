"use client";
import { useCallback, useEffect, useState } from "react";
import { fmtDateTime, useT } from "@/lib/i18n";
import { projectsApi } from "@/lib/projects/api";
import type { StandingApproval } from "@/lib/types";
import { useCan } from "@/lib/session";
import { Btn, Card, Empty, useAgentName } from "../ui";

/** The "always allow this kind of action in this project" permissions of a project, with their history; any of them can be revoked. */
export function StandingApprovals({ projectId }: { projectId: string }) {
  const { t } = useT();
  const agentName = useAgentName();
  const canDecide = useCan("approvals:decide");
  const [items, setItems] = useState<StandingApproval[] | null>(null);
  const [err, setErr] = useState(false);
  const load = useCallback(async () => {
    try { setItems(await projectsApi.standing(projectId)); setErr(false); } catch { setErr(true); }
  }, [projectId]);
  useEffect(() => { void load(); }, [load]);
  const revoke = async (id: string) => {
    try { await projectsApi.revokeStanding(projectId, id); await load(); } catch { setErr(true); }
  };
  if (items === null && !err) return null;
  if (!err && items!.length === 0) return null; // nothing was ever remembered in this project
  return (
    <Card title={t("pv.standing.title")} className="mt-4">
      <p className="mb-2 text-[12px] text-mute">{t("pv.standing.hint")}</p>
      {err && <div role="alert" className="mb-2 text-[12px] font-semibold text-red-700">{t("pv.standing.error")}</div>}
      {items && items.length === 0 ? <Empty>{t("pv.standing.none")}</Empty> : (
        <ul data-testid="standing-list" className="space-y-2">
          {items?.map((s) => (
            <li key={s.id} data-testid={`standing-${s.id}`} data-status={s.status} className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-line bg-panel2 px-3 py-2 text-[12px]">
              <div className="min-w-0">
                <div className="font-semibold text-ink"><span className="font-mono">{s.tool}.{s.action}</span> · {s.agent_id ? agentName(s.agent_id) : t("pv.standing.anyAgent")}</div>
                <div className="text-mute">
                  {t(`pv.standing.status.${s.status}`)} · {t("pv.standing.expires", { when: fmtDateTime(s.expires_at) })} · {t("pv.standing.uses", { count: s.use_count })} · {t("pv.standing.by", { who: s.created_by })}
                </div>
              </div>
              {s.status === "active" && canDecide && <Btn data-testid={`standing-revoke-${s.id}`} kind="danger" onClick={() => revoke(s.id)}>{t("pv.standing.revoke")}</Btn>}
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}
