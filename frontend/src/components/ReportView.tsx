"use client";
import type { Report } from "@/lib/types";
import { fmtUsd } from "@/lib/meta";
import { fmtDateTime, useT } from "@/lib/i18n";
import { Dot, useAgentColor, useAgentName } from "./ui";

export function ReportView({ r }: { r: Report }) {
  const { t } = useT();
  const name = useAgentName();
  const color = useAgentColor();
  return (
    <article className="space-y-4">
      <header>
        <h2 className="text-lg font-semibold text-ink">{r.title}</h2>
        <div className="mt-1 flex flex-wrap items-center gap-3 text-[11px] text-mute">
          <span>{fmtDateTime(r.created_at)}</span>
          <span>{t("common.cost")} {fmtUsd(r.cost_usd)}</span>
          <span className="flex flex-wrap items-center gap-2">
            {r.contributors.map((c) => (<span key={c} className="flex items-center gap-1"><Dot color={color(c)} />{name(c)}</span>))}
          </span>
        </div>
      </header>
      <div className="rounded-md border border-accent/30 bg-accent/5 p-3 text-[13px] leading-relaxed text-ink2">
        <div className="mb-1 text-[10px] font-semibold uppercase tracking-widest text-accent">{t("report.executiveSummary")}</div>
        {r.summary}
      </div>
      {r.sections.map((s, i) => (
        <section key={i}>
          <h3 className="mb-1 text-[13px] font-semibold text-ink">{s.heading}</h3>
          <p className="whitespace-pre-line text-[12.5px] leading-relaxed text-ink2">{s.body}</p>
        </section>
      ))}
    </article>
  );
}
