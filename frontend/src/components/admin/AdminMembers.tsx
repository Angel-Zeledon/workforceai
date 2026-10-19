"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { go } from "@/lib/navBridge";
import { useCan, useSession } from "@/lib/session";
import { Btn } from "../ui";
import { gov, type Member } from "./governanceApi";
import { inputCls, Notice, Panel, tdCls, thCls, useErrText, useLoad, when } from "./AdminParts";

const ROLES = ["viewer", "member", "admin", "owner"] as const;
const RANK: Record<string, number> = { viewer: 1, member: 2, admin: 3, owner: 4 };

/** Members and roles. Same rules as auth.CanAssign / CanManage: owners manage everyone, admins only below admin. */
export function AdminMembers() {
  const { t, locale } = useT();
  const errText = useErrText();
  const me = useSession((s) => s.user);
  const myRole = useSession((s) => s.role);
  const canManage = useCan("members:manage");
  const { data, err, reload } = useLoad(() => gov.members());
  const [bad, setBad] = useState<string | null>(null);
  const assignable = ROLES.filter((r) => myRole === "owner" || (myRole === "admin" && RANK[r] < RANK.admin));
  const editable = (m: Member) => canManage && m.user_id !== me?.id && (myRole === "owner" || (myRole === "admin" && RANK[m.role] < RANK.admin));
  const change = async (m: Member, role: string) => {
    try { await gov.setRole(m.user_id, role); setBad(null); reload(); } catch (e) { setBad(errText(e)); }
  };
  return (
    <Panel testid="adm-members" title={t("adm.members.title")} body={t("adm.members.body")}>
      {(err || bad) && <Notice kind="err">{err || bad}</Notice>}
      <div className="overflow-x-auto rounded-lg border border-line">
        <table className="w-full min-w-[520px] border-collapse">
          <thead className="bg-panel2"><tr><th className={thCls}>{t("adm.api.name")}</th><th className={thCls}>{t("members.role")}</th><th className={thCls}>{t("adm.wh.col.when")}</th></tr></thead>
          <tbody>
            {(data?.members ?? []).map((m) => (
              <tr key={m.user_id} data-testid="adm-member-row" className="border-t border-line">
                <td className={tdCls}><b>{m.name}{m.user_id === me?.id ? ` · ${t("members.you")}` : ""}</b><span className="block text-[11px] text-mute">{m.email}</span></td>
                <td className={tdCls}>
                  {editable(m) ? (
                    <select aria-label={t("members.role")} className={inputCls} value={m.role} onChange={(e) => change(m, e.target.value)}>
                      {[...new Set([...assignable, m.role])].map((r) => <option key={r} value={r}>{t(`members.roles.${r}`)}</option>)}
                    </select>
                  ) : t(`members.roles.${m.role}`)}
                </td>
                <td className={`${tdCls} text-mute`}>{when(m.created_at, locale)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div><Btn kind="primary" onClick={() => go("/settings?s=members")}>{t("adm.members.invite")}</Btn></div>
    </Panel>
  );
}
