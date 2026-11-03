"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { officesApi } from "@/lib/officesApi";
import { VISIBILITIES, accessIssue, upsertShare, type AgentVisibility, type DirectoryEntry, type OfficesResponse, type PersonRef, type Share, type ShareLevel } from "@/lib/officesLogic";
import { Modal } from "../templates/Modal";
import { btnGhost, btnPrimary, inputCls } from "./shared";

/** Who owns an agent and who may use it: owner (a person or an office), the visibility ladder and people one by one. */
export function AgentAccessDialog({ entry, offices, people, onClose, onSaved }: {
  entry: DirectoryEntry; offices: OfficesResponse; people: PersonRef[]; onClose: () => void; onSaved: () => void;
}) {
  const { t } = useT();
  const [owner, setOwner] = useState(`${entry.owner_kind}:${entry.owner_id}`);
  const [vis, setVis] = useState<AgentVisibility>(entry.visibility);
  const [shares, setShares] = useState<Share[]>(entry.shares);
  const [addUser, setAddUser] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [kind, id] = [owner.slice(0, owner.indexOf(":")) as "person" | "office", owner.slice(owner.indexOf(":") + 1)];
  const ownerChanged = owner !== `${entry.owner_kind}:${entry.owner_id}`;
  // the owner is only sent when it changes (keeping it needs no right to give the agent away)
  const input = ownerChanged ? { owner_kind: kind, owner_id: id, visibility: vis, shares } : { visibility: vis, shares };
  const issue = accessIssue({ owner_kind: kind, owner_id: id, visibility: vis, shares });
  const me = offices.me;
  // a person who is not an admin gives an agent to themself, to a member of an office they lead, or to their offices
  const officeChoices = offices.offices.filter((o) => me.admin || o.is_member || entry.owner_kind === "office" && o.id === entry.owner_id);
  const lead = offices.offices.find((o) => o.id === entry.office_id && o.is_lead);
  const personChoices = me.admin ? people : people.filter((p) => p.user_id === me.user_id || lead?.members.some((m) => m.user_id === p.user_id)
    || entry.owner_kind === "person" && p.user_id === entry.owner_id);

  const save = async () => {
    if (issue) return;
    setBusy(true); setError(null);
    try { await officesApi.setAccess(entry.id, input); onSaved(); }
    catch (e) { setError(t(String(e).includes("403") ? "offices.error.forbidden" : "offices.error.save")); }
    finally { setBusy(false); }
  };

  return (
    <Modal title={t("offices.access.title", { name: entry.name })} onClose={onClose} testid="agent-access-dialog">
      <div className="grid gap-4">
        <label className="flex flex-col gap-1 text-[12px] font-semibold">
          {t("offices.access.owner")}
          <select data-testid="agent-access-owner" value={owner} onChange={(e) => setOwner(e.target.value)} className={inputCls}>
            <optgroup label={t("offices.access.offices")}>
              {officeChoices.map((o) => <option key={o.id} value={`office:${o.id}`}>{o.general ? t("offices.general") : o.name}</option>)}
            </optgroup>
            <optgroup label={t("offices.access.people")}>
              {personChoices.map((p) => <option key={p.user_id} value={`person:${p.user_id}`}>{p.user_id === me.user_id ? t("offices.access.me", { name: p.name }) : p.name}</option>)}
            </optgroup>
          </select>
          <span className="text-[11px] font-normal text-mute">{t("offices.access.ownerHint")}</span>
        </label>
        <fieldset className="grid gap-1.5">
          <legend className="mb-1 text-[12px] font-semibold">{t("offices.access.who")}</legend>
          {VISIBILITIES.map((v) => (
            <label key={v} className={`flex cursor-pointer items-start gap-2 rounded-lg border px-3 py-2 ${vis === v ? "border-accent/50 bg-accent-soft" : "border-line"}`}>
              <input type="radio" name="agent-vis" value={v} checked={vis === v} onChange={() => setVis(v)} data-testid={`agent-access-vis-${v}`} className="mt-0.5" />
              <span>
                <span className="block text-[13px] font-semibold">{t(`offices.vis.${v}`)}</span>
                <span className="block text-[11px] text-mute">{t(`offices.vis.${v}.hint`)}</span>
              </span>
            </label>
          ))}
        </fieldset>
        <div className="grid gap-2">
          <span className="text-[12px] font-semibold">{t("offices.access.shares")}</span>
          <ul className="grid gap-1">
            {shares.map((s) => (
              <li key={s.user_id} className="flex items-center justify-between gap-2 text-[12px]">
                <span>{people.find((p) => p.user_id === s.user_id)?.name ?? s.name ?? s.user_id}</span>
                <span className="flex items-center gap-2">
                  <select aria-label={t("offices.access.level")} value={s.level} onChange={(e) => setShares(upsertShare(shares, { ...s, level: e.target.value as ShareLevel }))} className={`${inputCls} py-1 text-[12px]`}>
                    <option value="chat">{t("offices.share.chat")}</option>
                    <option value="edit">{t("offices.share.edit")}</option>
                  </select>
                  <button type="button" onClick={() => setShares(shares.filter((x) => x.user_id !== s.user_id))} className="text-red-600 hover:underline">{t("offices.memory.delete")}</button>
                </span>
              </li>
            ))}
            {shares.length === 0 && <li className="text-[12px] text-mute">{t("offices.access.noShares")}</li>}
          </ul>
          <div className="flex gap-2">
            <select data-testid="agent-access-share-user" value={addUser} onChange={(e) => setAddUser(e.target.value)} className={`${inputCls} text-[12px]`}>
              <option value="">{t("offices.access.addPerson")}</option>
              {people.filter((p) => !shares.some((s) => s.user_id === p.user_id)).map((p) => <option key={p.user_id} value={p.user_id}>{p.name}</option>)}
            </select>
            <button type="button" data-testid="agent-access-share-add" disabled={!addUser} className={btnGhost}
              onClick={() => { setShares(upsertShare(shares, { user_id: addUser, level: "chat" })); setAddUser(""); }}>{t("offices.add")}</button>
          </div>
        </div>
        {issue && <div role="alert" className="text-[12px] font-semibold text-red-600">{t(`offices.access.issue.${issue}`)}</div>}
        {error && <div role="alert" data-testid="agent-access-error" className="text-[12px] font-semibold text-red-600">{error}</div>}
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className={btnGhost}>{t("org.cancel")}</button>
          <button type="button" data-testid="agent-access-save" disabled={busy || !!issue} onClick={() => void save()} className={btnPrimary}>{busy ? t("org.saving") : t("org.save")}</button>
        </div>
      </div>
    </Modal>
  );
}
