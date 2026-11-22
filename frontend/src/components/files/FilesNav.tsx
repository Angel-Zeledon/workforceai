"use client";
import type { ReactNode } from "react";
import { useT } from "@/lib/i18n";
import { pathKey } from "@/lib/deptLogic";
import type { Desk, OfficeRef } from "@/lib/filesLogic";
import { Icon, type IconName } from "../icons";
import { Avatar } from "../chat/ChatThread";

const heading = "px-2 pb-1 pt-1 text-[10.5px] font-semibold uppercase tracking-[0.08em] text-mute";

/**
 * The side bar of the Files screen, in the order a person thinks of it: my departments (and the open ones I may read),
 * Compartido con todos, Proyectos, Clientes and the agents' desks by name. The place being browsed shows its folder
 * tree (`tree`) in place of its plain entry.
 */
export function FilesNav({ root, depts, others, desks, tree, onOpen }: {
  /** The root of the place being browsed (a department's folder, /Shared, /Agents/<id>...; "/" at the home). */
  root: string; depts: OfficeRef[]; others: OfficeRef[]; desks: Desk[] | null; tree: ReactNode; onOpen: (path: string) => void;
}) {
  const { t } = useT();
  const cur = pathKey(root);
  const item = (path: string, label: string, icon: ReactNode, extra?: ReactNode, testid?: string) => {
    if (pathKey(path) === cur && tree) return <li key={path}>{tree}</li>;
    const here = pathKey(path) === cur;
    return (
      <li key={path}>
        <button type="button" data-testid={testid} onClick={() => onOpen(path)} aria-current={here ? "page" : undefined}
          className={`flex min-h-10 w-full items-center gap-1.5 rounded-md px-2 text-left text-[13px] md:min-h-8 ${here ? "bg-accent-soft font-semibold text-accent" : "text-ink hover:bg-panel2"}`}>
          {icon}
          <span className="min-w-0 flex-1 truncate" title={label}>{label}</span>
          {extra}
        </button>
      </li>
    );
  };
  const glyph = (name: IconName) => <Icon name={name} size={14} className="ml-[26px] shrink-0 text-mute" />;
  const dept = (o: OfficeRef, reader: boolean) => item(o.folder_path!.replace(/\/+$/, ""), o.name, glyph("folder"),
    reader ? <span className="shrink-0 text-[10.5px] text-mute">{t("files.readOnlyTag")}</span>
      : o.visibility === "private" ? <span title={t("files.privateTag")} className="shrink-0 text-mute"><Icon name="lock" size={12} /></span> : undefined,
    `files-nav-dept-${o.name}`);

  return (
    <nav aria-label={t("files.nav")} data-testid="files-nav" className="space-y-3">
      <div>
        <div className={heading}>{t("files.section.depts")}</div>
        <ul className="space-y-px">
          {depts.map((o) => dept(o, false))}
          {!depts.length && <li className="px-2 py-1 text-[12px] text-mute">{t("files.depts.none")}</li>}
        </ul>
        {others.length > 0 && <>
          <div className={`${heading} mt-2`}>{t("files.section.otherDepts")}</div>
          <ul className="space-y-px">{others.map((o) => dept(o, true))}</ul>
        </>}
      </div>
      <ul className="space-y-px border-t border-line pt-2">
        {item("/Shared", t("files.section.shared"), glyph("users"), undefined, "files-nav-shared")}
        {item("/Projects", t("files.section.projects"), glyph("layers"), undefined, "files-nav-projects")}
        {item("/Clients", t("files.section.clients"), glyph("folder"), undefined, "files-nav-clients")}
      </ul>
      <div className="border-t border-line pt-2">
        <button type="button" onClick={() => onOpen("/Agents")} data-testid="files-nav-agents" aria-current={cur === "/agents" ? "page" : undefined}
          className={`${heading} block w-full rounded-md text-left hover:text-ink ${cur === "/agents" ? "text-accent" : ""}`}>{t("files.section.agents")}</button>
        <ul className="space-y-px">
          {(desks ?? []).map((d) => item(d.path, d.name, <span className="ml-[26px]"><Avatar id={d.id} size={16} /></span>, undefined, `files-nav-desk-${d.id}`))}
          {desks && !desks.length && <li className="px-2 py-1 text-[12px] text-mute">{t("files.desks.empty")}</li>}
        </ul>
      </div>
    </nav>
  );
}
