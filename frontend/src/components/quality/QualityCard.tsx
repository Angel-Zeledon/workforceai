"use client";
import { useCallback, useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { qualityApi, type AgentQuality, type Methodology } from "@/lib/qualityApi";
import { deltaText, LEVEL_COLOR, pctText, trendArrow } from "@/lib/qualityLogic";
import { Card } from "../ui";
import { GoldenCases } from "./GoldenCases";
import { MethodologyPopover } from "./MethodologyPopover";

/** Trend label with its arrow; "no trend" when there is not enough data in one of the two windows. */
export function TrendText({ q }: { q: Pick<AgentQuality, "trend" | "trend_delta"> }) {
  const { t } = useT();
  return <span>{trendArrow(q.trend)} {t(`quality.trend.${q.trend}`, { delta: deltaText(q.trend_delta) })}</span>;
}

/** Compact quality card of the agent profile: score, level, trend, components with their sample sizes, and the golden cases. */
export function QualityCard({ id }: { id: string }) {
  const { t } = useT();
  const [data, setData] = useState<{ m: Methodology; q: AgentQuality } | null>(null);
  const [err, setErr] = useState(false);

  const load = useCallback(async () => {
    try { const r = await qualityApi.agent(id); setData({ m: r.methodology, q: r.agent }); setErr(false); } catch { setErr(true); }
  }, [id]);
  useEffect(() => { setData(null); load(); }, [load]);

  const q = data?.q;
  return (
    <Card title={t("quality.card.title")} right={data && <MethodologyPopover m={data.m} />}>
      <div data-testid="quality-card">
        {!data ? <div className="text-xs text-mute">{err ? t("quality.error.load") : t("quality.loading")}</div> : q && (
          <>
            {q.score == null ? (
              <div data-testid="quality-insufficient">
                <div className="text-[13px] font-semibold text-ink2">{t("quality.insufficient")}</div>
                <p className="mt-0.5 text-[11px] text-mute">{t("quality.insufficient.help", { n: data.m.min_total_sample })}</p>
              </div>
            ) : (
              <div className="flex items-end gap-3">
                <div data-testid="quality-score" className="text-3xl font-semibold leading-none tracking-tight" style={{ color: LEVEL_COLOR[q.level ?? "fair"] }}>{q.score}</div>
                <div className="min-w-0 pb-0.5">
                  <div className="text-[12px] font-semibold" style={{ color: LEVEL_COLOR[q.level ?? "fair"] }}>{t(`quality.level.${q.level ?? "fair"}`)}</div>
                  <div data-testid="quality-trend" className="text-[11px] text-mute"><TrendText q={q} /></div>
                </div>
                <div className="ml-auto pb-0.5 text-[10.5px] text-mute">{t("quality.sample", { n: q.sample })}</div>
              </div>
            )}
            <ul className="mt-3 space-y-1.5">
              {q.components.map((c) => (
                <li key={c.key} data-testid={`quality-comp-${c.key}`} data-included={c.included}>
                  <div className="flex items-center justify-between gap-2 text-[11px]">
                    <span className={c.included ? "text-ink2" : "text-mute"}>{t(`quality.component.${c.key}`)}</span>
                    <span className="text-mute">{c.included ? `${pctText(c.rate)} · ${t("quality.component.sample", { n: c.sample })}` : t("quality.component.excluded", { min: c.min_sample, n: c.sample })}</span>
                  </div>
                  {c.included && <div className="mt-0.5 h-1 overflow-hidden rounded bg-line"><div className="h-full rounded bg-accent" style={{ width: pctText(c.rate) }} /></div>}
                </li>
              ))}
            </ul>
            <p className="mt-2 text-[10.5px] text-mute">{t("quality.advisory")}</p>
            <GoldenCases agentId={id} onChange={load} />
          </>
        )}
      </div>
    </Card>
  );
}
