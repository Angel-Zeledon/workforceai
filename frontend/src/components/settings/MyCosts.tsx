"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { gov, type UsageReport } from "../admin/governanceApi";
import { useSession } from "@/lib/session";

/** Chat calls cost fractions of a cent: show up to four decimals so a real spend never reads as $0.00. */
const usd = (n: number) => `$${n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: n > 0 && n < 0.01 ? 4 : 2 })}`;

/** E0: what the AI cost this month for what I asked (my chats and my requests). The server answers
 *  GET /governance/reports/usage?by=person with only my own row unless I am an owner or an admin. */
export function MyCosts() {
  const { t } = useT();
  const [rep, setRep] = useState<UsageReport | null>(null);
  const [err, setErr] = useState(false);
  useEffect(() => {
    let live = true;
    gov.usage("person").then((r) => { if (live) setRep(r); }).catch(() => { if (live) setErr(true); });
    return () => { live = false; };
  }, []);
  const me = useSession((s) => s.user?.id);
  // owners and admins get everybody's rows: pick mine by id (a member only ever gets their own)
  const mine = rep?.rows.find((r) => r.key !== "" && r.key === me);
  return (
    <div data-testid="my-costs" className="mx-auto max-w-2xl space-y-3 px-5 py-6 text-[13px] text-ink2">
      <p className="text-mute">{t("mycosts.body")}</p>
      {err && <p role="alert" className="rounded-lg border border-err/40 bg-err/10 p-3 text-err">{t("mycosts.error")}</p>}
      {!rep && !err && <p className="text-mute">{t("mycosts.loading")}</p>}
      {rep && (
        <div className="grid grid-cols-3 gap-3">
          <Stat label={t("mycosts.cost")} value={usd(mine?.cost_usd ?? 0)} testid="my-costs-usd" />
          <Stat label={t("mycosts.calls")} value={String(mine?.calls ?? 0)} />
          <Stat label={t("mycosts.requests")} value={String(mine?.requests ?? 0)} />
        </div>
      )}
      {rep && <p className="text-[12px] text-mute">{t("mycosts.period", { from: rep.from.slice(0, 10), to: rep.to.slice(0, 10) })}</p>}
    </div>
  );
}

function Stat({ label, value, testid }: { label: string; value: string; testid?: string }) {
  return (
    <div className="rounded-xl border border-line bg-panel p-4">
      <span className="block text-[11.5px] text-mute">{label}</span>
      <b data-testid={testid} className="mt-1 block text-[18px] font-semibold text-ink">{value}</b>
    </div>
  );
}
