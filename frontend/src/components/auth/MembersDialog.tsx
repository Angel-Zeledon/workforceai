"use client";
import { useCallback, useEffect, useState } from "react";
import { call } from "@/lib/api";
import { useT } from "@/lib/i18n";
import { useCan, useSession } from "@/lib/session";
import { Btn } from "../ui";
import { ErrorLine, inputCls, Modal } from "../connections/shared";

interface Member { user_id: string; email: string; name: string; role: string; created_at: string }
interface Invitation { id: string; email: string; role: string; status: string; expires_at: string }

const ROLES = ["viewer", "member", "admin", "owner"] as const;
const RANK: Record<string, number> = { viewer: 1, member: 2, admin: 3, owner: 4 };

/** Same rule as auth.CanAssign: owners grant anything, admins only member/viewer. */
const assignable = (actor: string | null) => ROLES.filter((r) => actor === "owner" || (actor === "admin" && RANK[r] < RANK.admin));

/** Members and invitations of the current organization (admin menu). Buttons follow the permissions of /auth/me. */
export function MembersDialog({ onClose }: { onClose: () => void }) {
  const { t } = useT();
  const me = useSession((s) => s.user);
  const myRole = useSession((s) => s.role);
  const canInvite = useCan("members:invite");
  const canManage = useCan("members:manage");
  const [members, setMembers] = useState<Member[]>([]);
  const [invites, setInvites] = useState<Invitation[]>([]);
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<string>("member");
  const [link, setLink] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const [m, i] = await Promise.all([
        call<{ members: Member[] }>("GET", "/auth/members"),
        call<{ invitations: Invitation[] }>("GET", "/invitations"),
      ]);
      setMembers(m.members ?? []);
      setInvites(i.invitations ?? []);
    } catch { setErr(t("members.err.load")); }
  }, [t]);
  useEffect(() => { load(); }, [load]);

  // Same rule as auth.CanManage: owners manage everyone else, admins only members below admin.
  const editable = (m: Member) => canManage && m.user_id !== me?.id && (myRole === "owner" || (myRole === "admin" && RANK[m.role] < RANK.admin));

  // The backend refuses to leave an organization without an owner: don't offer it.
  const soleOwner = (m: Member) => m.role === "owner" && members.filter((x) => x.role === "owner").length <= 1;

  const act = async (fn: () => Promise<unknown>) => {
    setErr(null);
    try { await fn(); await load(); } catch { setErr(t("members.err.action")); }
  };
  const invite = () => act(async () => {
    const r = await call<{ token: string }>("POST", "/invitations", { email, role });
    setLink(`${window.location.origin}/invite?token=${encodeURIComponent(r.token)}`);
    setCopied(false);
    setEmail("");
  });
  const copy = async () => {
    if (!link) return;
    try { await navigator.clipboard.writeText(link); setCopied(true); } catch { /* the link stays selectable */ }
  };

  return (
    <Modal onClose={onClose} testId="members-dialog" wide>
      <h2 className="font-display text-lg font-semibold">{t("members.title")}</h2>
      <p className="mt-1 text-xs text-mute">{t("members.body")}</p>

      <ul className="mt-3 divide-y divide-line rounded-xl border border-line" data-testid="members-list">
        {members.map((m) => (
          <li key={m.user_id} data-testid="member-row" className="flex items-center gap-3 px-3 py-2 text-xs">
            <span className="min-w-0 flex-1">
              <b className="block truncate text-[13px] text-ink">{m.name}{m.user_id === me?.id ? ` · ${t("members.you")}` : ""}</b>
              <span className="truncate text-mute">{m.email}</span>
            </span>
            {editable(m) ? (
              <select aria-label={t("members.role")} data-testid="member-role" className={`${inputCls} !w-auto`} value={m.role}
                onChange={(e) => act(() => call("PATCH", `/auth/members/${m.user_id}`, { role: e.target.value }))}>
                {assignable(myRole).map((r) => <option key={r} value={r}>{t(`members.roles.${r}`)}</option>)}
                {!assignable(myRole).includes(m.role as never) && <option value={m.role}>{t(`members.roles.${m.role}`)}</option>}
              </select>
            ) : (
              <span className="rounded-full bg-panel2 px-2 py-0.5 text-[11px] font-semibold text-ink2">{t(`members.roles.${m.role}`)}</span>
            )}
            {((m.user_id === me?.id && !soleOwner(m)) || editable(m)) && (
              <Btn kind="danger" data-testid="member-remove" onClick={() => act(() => call("DELETE", `/auth/members/${m.user_id}`))}>
                {m.user_id === me?.id ? t("members.leave") : t("members.remove")}
              </Btn>
            )}
          </li>
        ))}
      </ul>

      {canInvite && (
        <div className="mt-4 rounded-xl border border-line bg-panel2 p-3">
          <h3 className="text-sm font-semibold">{t("members.invite.title")}</h3>
          <div className="mt-2 flex flex-wrap gap-2">
            <input data-testid="invite-email" type="email" placeholder={t("auth.email")} className={`${inputCls} min-w-[200px] flex-1`} value={email} onChange={(e) => setEmail(e.target.value)} />
            <select data-testid="invite-role" aria-label={t("members.role")} className={`${inputCls} !w-auto`} value={role} onChange={(e) => setRole(e.target.value)}>
              {assignable(myRole).map((r) => <option key={r} value={r}>{t(`members.roles.${r}`)}</option>)}
            </select>
            <Btn kind="primary" data-testid="invite-send" disabled={!email.includes("@")} onClick={invite}>{t("members.invite.send")}</Btn>
          </div>
          {link && (
            <div className="mt-2 space-y-1" data-testid="invite-link-box">
              <p className="text-[11px] text-mute">{t("members.invite.linkOnce")}</p>
              <div className="flex gap-2">
                <input readOnly data-testid="invite-link" className={inputCls} value={link} onFocus={(e) => e.currentTarget.select()} />
                <Btn data-testid="invite-copy" onClick={copy}>{copied ? t("members.invite.copied") : t("members.invite.copy")}</Btn>
              </div>
            </div>
          )}
        </div>
      )}

      {invites.length > 0 && (
        <div className="mt-4">
          <h3 className="text-sm font-semibold">{t("members.invites")}</h3>
          <ul className="mt-2 divide-y divide-line rounded-xl border border-line" data-testid="invites-list">
            {invites.map((i) => (
              <li key={i.id} data-testid="invite-row" data-status={i.status} className="flex items-center gap-3 px-3 py-2 text-xs">
                <span className="min-w-0 flex-1 truncate">{i.email} · {t(`members.roles.${i.role}`)}</span>
                <span className="text-mute">{t(`members.status.${i.status}`)}</span>
                {canInvite && i.status === "pending" && assignable(myRole).includes(i.role as never) && (
                  <Btn data-testid="invite-revoke" onClick={() => act(() => call("DELETE", `/invitations/${i.id}`))}>{t("members.invite.revoke")}</Btn>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}
      <div className="mt-3"><ErrorLine msg={err} /></div>
      <div className="mt-4 flex justify-end"><Btn onClick={onClose}>{t("common.close")}</Btn></div>
    </Modal>
  );
}
