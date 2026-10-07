"use client";
import { useT } from "@/lib/i18n";
import { useConnections } from "@/lib/connections/store";
import { useStore } from "@/lib/store";
import { Card } from "../ui";
import { CapChip } from "./shared";

/** "What can each employee do": autonomy + connection grants + what they can never do (spec 11.3, inverse view). */
export function CapabilitiesTab() {
  const { t } = useT();
  const agentOrder = useStore((s) => s.agentOrder);
  const agents = useStore((s) => s.agents);
  const connections = useConnections((s) => s.connections);
  const grants = useConnections((s) => s.grants);
  const providers = useConnections((s) => s.providers);
  const agentControls = useConnections((s) => s.agentControls);
  const controls = useConnections((s) => s.controls);
  const defOf = (provider: string, cap: string) => providers.find((p) => p.id === provider)?.capabilities.find((d) => d.id === cap);

  return (
    <div>
      <p className="mb-3 text-xs text-mute">{t("conn.can.help")}</p>
      <div className="grid gap-3 lg:grid-cols-2">
        {agentOrder.map((id) => {
          const a = agents[id]; if (!a) return null;
          const mine = Object.values(connections).flatMap((c) => (grants[c.id] ?? []).filter((g) => g.agent_id === id && g.status === "active" && c.status !== "revoked").map((g) => ({ c, g })));
          const caps = mine.flatMap(({ c, g }) => g.capabilities.map((cap) => ({ cap, def: defOf(c.provider, cap) })));
          const canSend = caps.some((x) => x.cap === "mail.send");
          const paused = agentControls[id]?.control === "paused";
          return (
            <div key={id} data-testid={`ctl-can-do-${id}`} data-paused={paused} data-can-send={canSend}>
              <Card title={<>{a.name} · {a.title}</>} right={paused ? <span className="rounded-full bg-amber-500/20 px-2 text-[10px] font-semibold text-amber-700">{t("ctl.agent.paused")}</span> : undefined}>
                <div className="space-y-3 text-xs">
                  <p className="text-mute">{t("conn.can.autonomy")}: <b className="text-ink">{t(`autonomy.${a.autonomy}`)}</b></p>
                  <div>
                    <div className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-mute">{t("conn.can.connections")}</div>
                    {mine.length === 0 ? <p className="text-mute">{t("conn.can.noConnections")}</p> : (
                      <ul className="space-y-1">
                        {mine.map(({ c, g }) => (
                          <li key={g.id} className="flex flex-wrap items-center justify-between gap-1 rounded-xl border border-line bg-panel2 px-2 py-1">
                            <span className="font-semibold">{c.label}</span>
                            <span className="flex flex-wrap gap-1">{g.capabilities.map((cap) => <CapChip key={cap} cap={cap} risk={defOf(c.provider, cap)?.risk ?? "low"} />)}</span>
                          </li>
                        ))}
                      </ul>
                    )}
                  </div>
                  <ul className="list-disc space-y-0.5 pl-4 text-mute">
                    {caps.length > 0 && !caps.some((x) => x.def?.side_effects) && <li>{t("conn.can.readOnly")}</li>}
                    {canSend && <li data-testid={`ctl-can-do-${id}-send-rule`}>{t("conn.can.sendRule", { seconds: 60 })}</li>}
                    {controls?.mode === "read_only" && <li>{t("conn.can.globalReadOnly")}</li>}
                    <li>{t("conn.can.neverApprove")}</li>
                  </ul>
                  {a.tools.length > 0 && <p className="text-[11px] text-mute">{t("conn.can.tools")}: <span className="font-mono">{a.tools.join(", ")}</span></p>}
                </div>
              </Card>
            </div>
          );
        })}
      </div>
    </div>
  );
}
