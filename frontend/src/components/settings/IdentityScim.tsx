"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { identityApi, type ScimView } from "@/lib/identityApi";
import { Btn } from "../ui";
import { ErrorLine } from "../connections/shared";
import { CopyField, IdCard, idErrorKey } from "./identityShared";

/** SCIM 2.0: lets the identity provider create and deactivate accounts. One bearer token per organization, shown once. */
export function IdentityScim() {
  const { t } = useT();
  const [view, setView] = useState<ScimView | null>(null);
  const [fresh, setFresh] = useState<string | null>(null); // the token, in memory only until the person leaves
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => { identityApi.scim().then(setView).catch((x) => setErr(t(idErrorKey(x)))); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, []);

  const run = async (fn: () => Promise<void>) => { setBusy(true); setErr(null); try { await fn(); } catch (x) { setErr(t(idErrorKey(x))); } setBusy(false); };
  const rotate = () => {
    if (view?.token.exists && !window.confirm(t("idn.scim.rotateConfirm"))) return;
    return run(async () => { const r = await identityApi.rotateScim(); setFresh(r.token); setView(await identityApi.scim()); });
  };
  const revoke = () => { if (!window.confirm(t("idn.scim.revokeConfirm"))) return; return run(async () => { await identityApi.revokeScim(); setFresh(null); setView(await identityApi.scim()); }); };

  return (
    <IdCard testid="idn-scim" title={t("idn.scim.title")} desc={t("idn.scim.body")}>
      {view && <CopyField testid="idn-scim-url" label={t("idn.scim.url")} value={view.base_url} />}
      {fresh && (
        <div className="space-y-1.5" data-testid="idn-scim-fresh">
          <CopyField testid="idn-scim-token" label={t("idn.scim.token")} value={fresh} />
          <p role="status" className="text-[12px] font-medium text-warn">{t("idn.scim.once")}</p>
        </div>
      )}
      {view && !fresh && (
        <p className="text-[12.5px] text-ink2" data-testid="idn-scim-status">
          {view.token.exists
            ? t("idn.scim.active", { when: view.token.last_used_at ? new Date(view.token.last_used_at).toLocaleString() : t("idn.scim.never") })
            : t("idn.scim.none")}
        </p>
      )}
      <ErrorLine msg={err} />
      <div className="flex flex-wrap gap-2">
        <Btn kind="primary" onClick={rotate} disabled={busy || !view} data-testid="idn-scim-rotate">{t(view?.token.exists ? "idn.scim.rotate" : "idn.scim.create")}</Btn>
        {view?.token.exists && <Btn kind="danger" onClick={revoke} disabled={busy} data-testid="idn-scim-revoke">{t("idn.scim.revoke")}</Btn>}
      </div>
      <p className="text-[11.5px] leading-relaxed text-mute">{t("idn.scim.roles")}</p>
    </IdCard>
  );
}
