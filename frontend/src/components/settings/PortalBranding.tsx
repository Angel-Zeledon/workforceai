"use client";
import { useEffect, useRef, useState } from "react";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { portalApi, type PortalBranding as Branding } from "@/lib/portalApi";
import { DEFAULT_ACCENT, safeAccent, textOn } from "@/lib/portalLogic";
import { Btn } from "../ui";

const input = "w-full rounded-lg border border-line-strong bg-panel px-2.5 py-2 text-base text-ink outline-none focus:border-accent disabled:opacity-50 sm:text-sm";

/** The firm's branding on the client portal (B13): name, accent color and logo. Owners and admins edit it. */
export function PortalBranding() {
  const { t } = useT();
  const canEdit = useCan("org:manage");
  const [b, setB] = useState<Branding | null>(null);
  const [name, setName] = useState("");
  const [color, setColor] = useState("");
  const [logo, setLogo] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const file = useRef<HTMLInputElement>(null);

  const loadLogo = async (has: boolean) => {
    const blob = has ? await portalApi.logo().catch(() => null) : null;
    setLogo((old) => { if (old) URL.revokeObjectURL(old); return blob ? URL.createObjectURL(blob) : null; });
  };
  useEffect(() => {
    portalApi.branding().then((x) => { setB(x); setName(x.display_name); setColor(x.accent_color); loadLogo(x.has_logo); })
      .catch(() => setMsg({ ok: false, text: t("portal.brand.loadError") }));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const save = async () => {
    setBusy(true); setMsg(null);
    try { const x = await portalApi.setBranding({ display_name: name.trim(), accent_color: color.trim() }); setB(x); setMsg({ ok: true, text: t("portal.brand.saved") }); }
    catch { setMsg({ ok: false, text: t("portal.brand.invalid") }); }
    setBusy(false);
  };
  const upload = async (f: File | undefined) => {
    if (!f) return;
    setBusy(true); setMsg(null);
    try { const x = await portalApi.uploadLogo(f); setB(x); await loadLogo(x.has_logo); setMsg({ ok: true, text: t("portal.brand.logoSaved") }); }
    catch { setMsg({ ok: false, text: t("portal.brand.logoError") }); }
    if (file.current) file.current.value = "";
    setBusy(false);
  };
  const removeLogo = async () => {
    setBusy(true);
    try { const x = await portalApi.deleteLogo(); setB(x); await loadLogo(false); } catch { setMsg({ ok: false, text: t("portal.brand.logoError") }); }
    setBusy(false);
  };

  const accent = safeAccent(color);
  const fg = textOn(accent);
  if (!b && !msg) return <div className="p-6 text-sm text-mute">…</div>;
  return (
    <div data-testid="portal-branding" className="mx-auto max-w-2xl space-y-5 px-5 py-6 sm:px-6">
      <div>
        <h2 className="font-display text-lg font-semibold text-ink">{t("portal.brand.title")}</h2>
        <p className="mt-1 text-[12.5px] leading-relaxed text-mute">{t("portal.brand.desc")}</p>
      </div>

      <div aria-label={t("portal.brand.preview")} className="overflow-hidden rounded-xl border border-line shadow-pop">
        <div className="flex items-center gap-3 px-4 py-3" style={{ background: accent, color: fg }}>
          {logo && <img src={logo} alt="" className="h-9 w-auto max-w-[120px] rounded bg-white/95 object-contain p-1" />}
          <div className="min-w-0">
            <div className="truncate text-[14px] font-semibold">{name.trim() || t("portal.brand.namePh")}</div>
            <div className="text-[11px] opacity-80">{t("portal.public.kicker")}</div>
          </div>
        </div>
        <div className="bg-panel px-4 py-3 text-[11px] text-mute">{t("portal.brand.preview")}</div>
      </div>

      <label className="block text-[12px] font-semibold text-mute">{t("portal.brand.name")}
        <input data-testid="portal-brand-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} disabled={!canEdit} placeholder={t("portal.brand.namePh")} className={`${input} mt-1`} />
        <span className="mt-0.5 block font-normal">{t("portal.brand.nameHint")}</span>
      </label>

      <div className="block text-[12px] font-semibold text-mute">{t("portal.brand.color")}
        <div className="mt-1 flex items-center gap-2">
          <input type="color" aria-label={t("portal.brand.color")} value={safeAccent(color || DEFAULT_ACCENT)} onChange={(e) => setColor(e.target.value)} disabled={!canEdit} className="h-10 w-12 cursor-pointer rounded border border-line-strong bg-panel p-1" />
          <input data-testid="portal-brand-color" value={color} onChange={(e) => setColor(e.target.value)} maxLength={7} placeholder={DEFAULT_ACCENT} disabled={!canEdit} className={`${input} max-w-[140px] font-mono`} />
          {color && <Btn onClick={() => setColor("")} disabled={!canEdit}>{t("portal.brand.reset")}</Btn>}
        </div>
      </div>

      <div className="text-[12px] font-semibold text-mute">{t("portal.brand.logo")}
        <p className="mt-0.5 font-normal">{t("portal.brand.logoHint")}</p>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <input ref={file} data-testid="portal-brand-logo" type="file" accept="image/png,image/jpeg,image/gif" disabled={!canEdit || busy} onChange={(e) => upload(e.target.files?.[0])}
            className="max-w-full text-xs text-ink file:mr-2 file:rounded-md file:border file:border-line-strong file:bg-panel file:px-3 file:py-1.5 file:text-xs file:font-medium file:text-ink" />
          {b?.has_logo && <Btn kind="danger" data-testid="portal-brand-logo-remove" disabled={!canEdit || busy} onClick={removeLogo}>{t("portal.brand.logoRemove")}</Btn>}
        </div>
      </div>

      {msg && <p role="status" data-testid="portal-brand-msg" className={`text-[12px] ${msg.ok ? "text-ok" : "text-err"}`}>{msg.text}</p>}
      {!canEdit && <p className="text-[12px] text-mute">{t("portal.brand.readOnly")}</p>}
      <div className="flex justify-end">
        <Btn kind="primary" data-testid="portal-brand-save" disabled={!canEdit || busy} onClick={save}>{t("portal.brand.save")}</Btn>
      </div>
    </div>
  );
}
