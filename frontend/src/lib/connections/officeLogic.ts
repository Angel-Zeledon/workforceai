// Connections of a department (D3). Contract: backend/internal/api/connections_offices.go. A connection may belong to
// one office ("" or "general" = the whole company). Only the agents of that office may be granted it; only its
// members see it when the office is private (the server already leaves the others out). An admin or the office's
// responsible manages it.

/** the office a connection belongs to; "" = the whole company (General). */
export function connOffice(c: { office_id?: string | null }): string {
  const id = (c.office_id ?? "").trim();
  return id === "general" ? "" : id;
}

/** whether an agent may be granted a connection: always for one of the whole company, else only its office's agents. */
export function agentFitsConn(connOfficeId: string, agentOfficeId: string | undefined): boolean {
  if (!connOfficeId) return true;
  return (agentOfficeId ?? "") === connOfficeId;
}

/** whether the person manages a connection: the server says so; an older server only knew admins. */
export function canManageConn(c: { can_manage?: boolean }, isAdmin: boolean): boolean {
  return c.can_manage ?? isAdmin;
}

/** whether the person may be the second human of a pending write grant: an admin of the organization, or, for a
 *  department's connection, its responsible (the server says so through can_manage). Same rule as the server. */
export function canCoSignWrite(c: { office_id?: string | null; can_manage?: boolean }, isOrgAdmin: boolean): boolean {
  return isOrgAdmin || (connOffice(c) !== "" && !!c.can_manage);
}

export interface OfficeChoice { id: string; name: string }

/**
 * The offices a person may give a connection: the whole company only for an admin; besides, every office they
 * manage (an admin manages them all, a responsible the ones they lead). `current` stays listed so a select can show it.
 */
export function officeChoices(
  offices: { id: string; name: string; general?: boolean; can_manage?: boolean }[],
  isAdmin: boolean,
  current = "",
): OfficeChoice[] {
  const out: OfficeChoice[] = [];
  if (isAdmin) out.push({ id: "", name: "" });
  for (const o of offices) {
    if (o.general || o.id === "general") continue;
    if (isAdmin || o.can_manage || o.id === current) out.push({ id: o.id, name: o.name });
  }
  return out;
}

/** whether the person may create connections at all: an admin, or the responsible of some office. */
export function canCreateConn(offices: { general?: boolean; id: string; can_manage?: boolean }[], isAdmin: boolean): boolean {
  return isAdmin || offices.some((o) => !o.general && o.id !== "general" && !!o.can_manage);
}
