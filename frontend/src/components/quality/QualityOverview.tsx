"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { useStore } from "@/lib/store";
import { qualityApi, type QualityOverviewResponse } from "@/lib/qualityApi";
import { LEVEL_COLOR } from "@/lib/qualityLogic";
import { Card, Empty } from "../ui";
import { MethodologyPopover } from "./MethodologyPopover";
import { TrendText } from "./QualityCard";

const WINDOWS = [7, 30, 90];

/** Organization ranking: the agents with a score first, the rest listed as "insufficient data". */
export function QualityOverview() {
  const { t } = useT();
  const select = useStore((s) => s.select);
  const [days, setDays] = useState(30);
  const [data, setData] = useState<QualityOverviewResponse | null>(null);
  const [err, setErr] = useState(false);

  useEffect(() => {
    let live = true;
    setErr(false);
    qualityApi.overview(days).then((r) => { if (live) setData(r); }).catch(() => { if (live) setErr(true); });
    return () => { live = false; };
  }, [days]);

  return (
    <div className="space-y-4" data-testid="quality-overview">
      <div>
        <h2 className="font-display text-lg font-semibold text-ink">{t("quality.overview.title")}</h2>
        <p className="mt-0.5 max-w-2xl text-xs text-mute">{t("quality.overview.help")}</p>
      </div>
      <Card title={t("quality.rank.title")} right={
        <div className="flex items-center gap-3">
          <label className="flex items-center gap-1.5 text-[11px] text-mute">{t("quality.window")}
            <select data-testid="quality-window" value={days} onChange={(e) => setDays(Number(e.target.value))} className="rounded-md border border-line bg-panel2 px-1.5 py-1 text-[11px] text-ink">
              {WINDOWS.map((n) => <option key={n} value={n}>{t("quality.window.days", { n })}</option>)}
            </select>
          </label>
          {data && <MethodologyPopover m={data.methodology} />}
        </div>
      }>
        {err ? <Empty>{t("quality.error.load")}</Empty> : !data ? <div className="text-xs text-mute">{t("quality.loading")}</div> : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[520px] text-left text-xs">
              <thead>
                <tr className="text-[10px] font-semibold uppercase tracking-[0.08em] text-mute">
                  <th className="py-1.5 pr-2">#</th>
                  <th className="py-1.5 pr-2">{t("quality.rank.agent")}</th>
                  <th className="py-1.5 pr-2">{t("quality.rank.score")}</th>
                  <th className="py-1.5 pr-2">{t("quality.rank.level")}</th>
                  <th className="py-1.5 pr-2">{t("quality.rank.trend")}</th>
                  <th className="py-1.5 pr-2">{t("quality.rank.sample")}</th>
                  <th className="py-1.5">{t("quality.rank.golden")}</th>
                </tr>
              </thead>
              <tbody>
                {data.agents.map((a, i) => (
                  <tr key={a.agent_id} data-testid={`quality-row-${a.agent_id}`} className="cursor-pointer border-t border-line hover:bg-panel2" onClick={() => select(a.agent_id)}>
                    <td className="py-2 pr-2 text-mute">{a.score == null ? "–" : i + 1}</td>
                    <td className="py-2 pr-2 font-medium text-ink">{a.name}</td>
                    {a.score == null ? (
                      <td colSpan={3} className="py-2 pr-2 text-mute">{t("quality.insufficient")}</td>
                    ) : (
                      <>
                        <td className="py-2 pr-2 font-semibold" style={{ color: LEVEL_COLOR[a.level ?? "fair"] }}>{a.score}</td>
                        <td className="py-2 pr-2" style={{ color: LEVEL_COLOR[a.level ?? "fair"] }}>{t(`quality.level.${a.level ?? "fair"}`)}</td>
                        <td className="py-2 pr-2 text-ink2"><TrendText q={a} /></td>
                      </>
                    )}
                    <td className="py-2 pr-2 text-mute">{a.sample}</td>
                    <td className="py-2 text-mute">{a.golden_cases}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      <p className="text-[10.5px] text-mute">{t("quality.advisory")}</p>
    </div>
  );
}
