"use client";
import { useEffect, useState } from "react";
import { fmtNumber, useT } from "@/lib/i18n";
import { docSearch, type DocIndexSettings } from "@/lib/docsearch";
import { Card } from "../ui";
import { ErrorLine, errText, useViewer } from "../connections/shared";

/** Organization setting: index the drive for passage search (capability 6). Owner/admin only; audited by the backend. */
export function DocSearchCard() {
  const { t } = useT();
  const { isAdmin } = useViewer();
  const [cfg, setCfg] = useState<DocIndexSettings | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  useEffect(() => { docSearch.settings().then(setCfg).catch(() => setCfg(null)); }, []); // older backend or no permission: the card hides
  if (!cfg) return null;
  const save = async (enabled: boolean) => {
    setErr(null); setSaved(false);
    try { setCfg(await docSearch.putSettings(enabled)); setSaved(true); } catch (e) { setErr(errText(e)); }
  };
  const emb = cfg.embeddings;
  const embLine = emb.available
    ? t("docsearch.semanticOn", { model: emb.model ?? "", used: fmtNumber(emb.tokens_today), cap: fmtNumber(emb.daily_token_cap) })
    : emb.reason === "embeddings_policy" ? t("docsearch.semanticPolicy") : t("docsearch.semanticOff");
  return (
    <div data-testid="ctl-docsearch">
      <Card title={t("docsearch.title")}>
        <div className="space-y-3 text-xs">
          <p className="text-mute">{t("docsearch.help")}</p>
          <label className="flex items-center gap-2 font-semibold">
            <input type="checkbox" data-testid="ctl-docsearch-enabled" checked={cfg.settings.enabled} disabled={!isAdmin} onChange={(e) => save(e.target.checked)} />
            {t("docsearch.enable")}
          </label>
          <p data-testid="ctl-docsearch-embeddings" className="text-[11px] text-mute">{embLine}</p>
          {!cfg.settings.enabled && <p className="text-[11px] text-mute">{t("docsearch.offNote")}</p>}
          {saved && <p className="text-emerald-700">{t("docsearch.saved")}</p>}
          <ErrorLine msg={err} />
        </div>
      </Card>
    </div>
  );
}
