"use client";
import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode, type RefObject } from "react";
import { useStore } from "@/lib/store";
import { useT } from "@/lib/i18n";
import {
  addCard, applyResult, commandsApi, confirmText, echoUser, runLocal, useRegistry,
} from "@/lib/commands/client";
import {
  buildArgs, detectLang, matchCommands, mentionAt, norm, parseArgs, parseInput, primaryAlias, replaceRange, resolveCommand,
} from "@/lib/commands/logic";
import type { Command, Completion, Lang, RunBody, RunResponse, Suggestion, Surface } from "@/lib/commands/registry";
import { Icon } from "../../icons";
import { CommandMenu, type MenuItem } from "./CommandMenu";
import { ConfirmDialog } from "./CommandCard";
import { HelpPanel } from "./HelpPanel";

const COMPLETE_KINDS = new Set(["agent", "project", "file", "skill", "template"]);
const uid = () => Math.random().toString(36).slice(2) + Date.now().toString(36);

interface Pending { cmd: Command; body: RunBody; message: string }
interface Pick { text: string; caret: number }

/**
 * Slash commands for a chat composer: `/` menu, argument hints and completion, @mentions, running, confirmation,
 * help panel and contextual suggestions. The composer keeps its own text; this hook decides what to show and do with it.
 */
export function useSlashComposer({ conv, surface, text, caret, setText, inputRef, onSent }: {
  conv: string; surface: Surface; text: string; caret: number; setText: (t: string, caret?: number) => void; inputRef: RefObject<HTMLInputElement | HTMLTextAreaElement | null>; onSent?: () => void;
}) {
  const { t, locale } = useT();
  const lang = locale as Lang;
  const commands = useRegistry((s) => s.commands);
  const load = useRegistry((s) => s.load);
  const agents = useStore((s) => s.agents);
  const msgCount = useStore((s) => s.chat[conv]?.length ?? 0);
  const [active, setActive] = useState(0);
  const [dismissed, setDismissed] = useState<string | null>(null);
  const [comp, setComp] = useState<{ key: string; items: Completion[] }>({ key: "", items: [] });
  const [running, setRunning] = useState<string | null>(null);
  const [pending, setPending] = useState<Pending | null>(null);
  const [helpOpen, setHelpOpen] = useState(false);
  const [err, setErr] = useState("");
  const [sug, setSug] = useState<Suggestion[]>([]);
  const [sugOff, setSugOff] = useState<string[]>([]);
  const menuId = useRef(`cmd-menu-${uid()}`).current;
  useEffect(() => { void load(); }, [load]);

  const allowed = useMemo(() => commands.filter((c) => !c.surfaces || c.surfaces.length === 0 || c.surfaces.includes(surface)), [commands, surface]);
  const parsed = useMemo(() => parseInput(text), [text]);
  const cmd = useMemo(() => (parsed.slash && parsed.hasSpace ? resolveCommand(commands, parsed.name) : undefined), [parsed, commands]);
  const st = useMemo(() => (cmd ? parseArgs(cmd, parsed.rest) : null), [cmd, parsed.rest]);
  const argIdx = st?.current ?? 0;
  const arg = cmd?.args[argIdx];
  const mention = useMemo(() => (parsed.slash && !parsed.hasSpace ? null : mentionAt(text, caret)), [parsed, text, caret]);

  // ---- argument completion (debounced) ----
  const tokText = st?.currentTok?.text.replace(/^@/, "") ?? "";
  const compKey = arg && !mention && COMPLETE_KINDS.has(arg.kind) ? `${cmd!.id}:${arg.kind}:${tokText}` : "";
  useEffect(() => {
    if (!compKey || !arg) return;
    let stale = false;
    const h = setTimeout(() => {
      commandsApi.complete(arg.kind, tokText, 8).then((items) => { if (!stale) setComp({ key: compKey, items }); }).catch(() => { if (!stale) setComp({ key: compKey, items: [] }); });
    }, 150);
    return () => { stale = true; clearTimeout(h); };
  }, [compKey, arg, tokText]);

  // ---- menu model ----
  interface Entry extends MenuItem { pick: Pick; exact?: boolean; blocked?: boolean }
  const entries: Entry[] = useMemo(() => {
    if (parsed.slash && !parsed.hasSpace) {
      const q = norm(parsed.name);
      return matchCommands(allowed, parsed.name, lang).map(({ cmd: c, matched, alias }) => {
        const shown = q && matched.startsWith(q) ? matched : alias;
        const dis = c.allowed === false;
        const ex = q !== "" && matched === q;
        return {
          key: c.id, label: `/${shown}`, hint: `${c.title[lang]} · ${c.description[lang]}`, example: c.example[lang], group: t(`cmds.group.${c.group}`), icon: c.icon,
          disabled: dis, disabledReason: t("cmds.disabled"), pick: { text: `/${shown} `, caret: shown.length + 2 }, exact: ex && !c.args.some((a) => a.required),
        };
      });
    }
    if (mention) {
      const q = norm(mention.query);
      return Object.values(agents).filter((a) => !q || norm(a.name).includes(q)).slice(0, 8).map((a) => {
        const first = a.name.split(" ")[0];
        const r = replaceRange(text, mention.start, mention.end, `@${first}`);
        return { key: a.id, label: a.name, hint: a.role, icon: "user", group: t("cmds.mention.label"), pick: { text: r.text, caret: r.caret }, exact: norm(first) === q && q !== "" };
      });
    }
    if (cmd && arg && st) {
      const start = st.currentTok ? parsed.name.length + 2 + st.currentTok.start : text.length;
      const end = st.currentTok ? parsed.name.length + 2 + st.currentTok.end : text.length;
      const mk = (value: string, label: string, hint?: string): Entry => {
        const pre = text.slice(0, start);
        const r = replaceRange(pre + text.slice(end), start, start, value);
        return { key: `${arg.kind}:${value}`, label, hint, icon: arg.kind === "agent" ? "user" : arg.kind === "file" ? "doc" : arg.kind === "project" ? "folder" : "layers", pick: { text: r.text, caret: r.caret }, exact: norm(value) === norm(tokText) && tokText !== "" };
      };
      if (arg.kind === "enum" && arg.enum) return arg.enum.filter((v) => !tokText || norm(v).startsWith(norm(tokText))).map((v) => mk(v, v));
      if (compKey && comp.key === compKey) return comp.items.map((i) => mk(i.value, i.label, i.hint));
    }
    return [];
  }, [parsed, allowed, lang, mention, agents, cmd, arg, st, text, tokText, compKey, comp, t]);

  const open = entries.length > 0 && dismissed !== text;
  const nameOpen = parsed.slash && !parsed.hasSpace && dismissed !== text;
  const showMenu = nameOpen || open;
  useEffect(() => { setActive(0); }, [parsed.name, parsed.hasSpace, mention?.query, compKey, comp.key, entries.length]);
  useEffect(() => { if (dismissed !== null && dismissed !== text) setDismissed(null); }, [text, dismissed]);

  // ---- suggestions ----
  useEffect(() => {
    let stale = false;
    const h = setTimeout(() => {
      commandsApi.suggest(surface, conv).then((r) => { if (!stale) setSug(r); }).catch(() => { if (!stale) setSug([]); });
    }, 500);
    return () => { stale = true; clearTimeout(h); };
  }, [surface, conv, Math.floor(msgCount / 4)]); // eslint-disable-line react-hooks/exhaustive-deps
  const suggestion = sug.find((s) => !sugOff.includes(s.command) && commands.some((c) => c.id === s.command));

  const focusAt = useCallback((n: number) => {
    requestAnimationFrame(() => { const el = inputRef.current; if (el) { el.focus(); el.setSelectionRange(n, n); } });
  }, [inputRef]);
  const apply = (p: Pick) => { setText(p.text, p.caret); focusAt(p.caret); };

  // ---- running ----
  const agentId = conv.startsWith("agent:") ? conv.slice(6) : undefined;
  const exec = async (c: Command, body: RunBody) => {
    setRunning(c.id); setErr("");
    let res: RunResponse;
    try { res = await commandsApi.run(body); } catch { res = { status: "error", error: { code: "network", message: t("cmds.error") } }; }
    setRunning(null);
    if (res.status === "needs_confirmation") { setPending({ cmd: c, body, message: confirmText(res, body.lang) || t("cmds.confirm.default") }); return; }
    applyResult(conv, c, res, t("cmds.error"));
    onSent?.();
  };

  /** Returns true when the text was a slash command (handled here, the caller must not send it as chat). */
  const submit = (): boolean => {
    const v = text.trim();
    if (!v.startsWith("/")) return false;
    const p = parseInput(v);
    const c = resolveCommand(commands, p.name);
    if (!c) { setErr(t("cmds.unknown", { name: p.name || "/" })); return true; }
    if (c.allowed === false) { setErr(t("cmds.disabled")); return true; }
    const a = parseArgs(c, p.rest);
    if (a.missing.length) { setErr(t("cmds.missing", { args: a.missing.join(", "), example: c.example[lang] })); return true; }
    const cl = detectLang(c, p.name, lang);
    const local = runLocal(c, cl, Object.values(a.values)[0] ?? "");
    if (local.handled) { setText("", 0); setErr(""); if (local.help) setHelpOpen(true); return true; }
    setText("", 0); setErr("");
    echoUser(conv, v);
    onSent?.();
    void exec(c, {
      command: c.id, args: buildArgs(c, a), raw: v, lang: cl, idempotency_key: uid(),
      context: { surface, ...(agentId ? { agent_id: agentId } : {}), conversation_id: conv },
    });
    return true;
  };

  const confirm = () => { const p = pending; setPending(null); if (p) void exec(p.cmd, { ...p.body, confirm: true }); inputRef.current?.focus(); };
  const cancel = () => { setPending(null); addCard(conv, { kind: "message", text: t("cmds.cancelled"), command: pending?.cmd.id }); inputRef.current?.focus(); };

  // ---- keyboard ----
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement | HTMLTextAreaElement>) => {
    if (!showMenu) return;
    if (e.key === "Escape") { e.preventDefault(); setDismissed(text); return; }
    if (entries.length === 0) return;
    if (e.key === "ArrowDown") { e.preventDefault(); setActive((a) => (a + 1) % entries.length); }
    else if (e.key === "ArrowUp") { e.preventDefault(); setActive((a) => (a - 1 + entries.length) % entries.length); }
    else if (e.key === "Enter" || e.key === "Tab") {
      const it = entries[Math.min(active, entries.length - 1)];
      if (e.key === "Enter" && it.exact) return; // already typed in full: let the form run it
      e.preventDefault();
      if (!it.disabled) apply(it.pick);
    }
  };

  const insertCommand = (c: Command) => {
    setHelpOpen(false);
    const a = primaryAlias(c, lang);
    apply({ text: `/${a} `, caret: a.length + 2 });
  };

  // ---- pieces to render ----
  const hasMenu = showMenu && (entries.length > 0 || (nameOpen && parsed.name !== ""));
  const menu: ReactNode = hasMenu ? (
    <CommandMenu id={menuId} items={entries} active={Math.min(active, Math.max(0, entries.length - 1))} onPick={(i) => entries[i] && apply(entries[i].pick)} onHover={setActive}
      label={t(mention ? "cmds.mention.label" : nameOpen ? "cmds.menu.label" : "cmds.complete.label")} emptyText={t("cmds.empty")} />
  ) : null;

  const chips: ReactNode = cmd ? (
    <ul data-testid="cmd-args" aria-label={t("cmds.args.label")} className="flex flex-wrap items-center gap-1 px-0.5">
      <li className="rounded-md bg-accent-soft px-1.5 py-0.5 font-mono text-[11px] font-semibold text-accent-hover">/{parsed.name}</li>
      {cmd.args.map((a, i) => {
        const filled = !!st?.values[a.name];
        return (
          <li key={a.name} aria-current={i === argIdx ? "step" : undefined} title={a.required ? t("cmds.arg.required") : t("cmds.arg.optional")}
            className={`rounded-md border px-1.5 py-0.5 text-[11px] ${filled ? "border-accent/40 bg-accent-soft/60 text-ink" : i === argIdx ? "border-accent text-ink" : "border-dashed border-line-strong text-mute"}`}>
            {filled ? st!.values[a.name] : `${a.variadic ? `${a.name}…` : a.name}${a.required ? "*" : ""}`}
          </li>
        );
      })}
      <li className="truncate font-mono text-[10.5px] text-mute">{cmd.example[lang]}</li>
    </ul>
  ) : null;

  const status: ReactNode = (
    <div role="status" aria-live="polite" className="empty:hidden">
      {running && (
        <span data-testid="cmd-running" className="inline-flex items-center gap-1.5 rounded-md bg-accent-soft px-2 py-0.5 text-[11px] font-medium text-accent-hover">
          <span className="ac-dots" aria-hidden><span /><span /><span /></span>{t("cmds.running", { cmd: running })}
        </span>
      )}
    </div>
  );

  const suggestionChip: ReactNode = suggestion && !text ? (
    <div data-testid="cmd-suggestion" className="flex items-center gap-1.5">
      <button type="button" onClick={() => { const c = commands.find((x) => x.id === suggestion.command)!; insertCommand(c); }}
        className="inline-flex min-w-0 items-center gap-1.5 rounded-full border border-line bg-panel2 px-2.5 py-0.5 text-[11px] text-ink2 hover:border-accent hover:text-ink">
        <Icon name="alert" size={11} className="shrink-0 text-accent" /><span className="truncate">{suggestion.reason[lang] || suggestion.reason.es}</span>
      </button>
      <button type="button" aria-label={t("cmds.suggest.dismiss")} onClick={() => setSugOff((x) => [...x, suggestion.command])} className="rounded p-0.5 text-mute hover:text-ink"><Icon name="x" size={11} /></button>
    </div>
  ) : null;

  const dialogs: ReactNode = (
    <>
      {pending && <ConfirmDialog message={pending.message} onConfirm={confirm} onCancel={cancel} />}
      {helpOpen && <HelpPanel commands={commands} onPick={insertCommand} onClose={() => { setHelpOpen(false); inputRef.current?.focus(); }} />}
    </>
  );

  const aria = {
    role: "combobox" as const,
    "aria-autocomplete": "list" as const,
    "aria-expanded": hasMenu,
    "aria-controls": hasMenu ? menuId : undefined,
    "aria-activedescendant": hasMenu && entries.length ? `${menuId}-${Math.min(active, entries.length - 1)}` : undefined,
    "aria-haspopup": "listbox" as const,
  };

  return { menu, chips, status, suggestionChip, dialogs, aria, onKeyDown, submit, err, clearErr: () => setErr(""), openHelp: () => setHelpOpen(true), running: !!running, hasMenu };
}
