"use client";
import { useEffect, useMemo, useState } from "react";
import { useT } from "@/lib/i18n";
import type { DriveFile, DriveVersion } from "@/lib/drive";
import { pickEditAgent } from "@/lib/fileEdit";
import { fileKind } from "@/lib/fileView";
import { useStore } from "@/lib/store";
import { useAgentName } from "../ui";
import { sendChat } from "../chat/ChatThread";

/**
 * "Ask for a change" in the file viewer: a normal chat message to the chosen agent with the file attached (path + version)
 * and the edit instructions (fs.edit, smallest change, expected_version). No new transport: it is sendChat.
 * Default agent: the one that last edited the file, else the most relevant role, else the first.
 */
export function AskForChange({ file, versions }: { file: DriveFile; versions: DriveVersion[] }) {
  const { t } = useT();
  const name = useAgentName();
  const agents = useStore((s) => s.agents);
  const order = useStore((s) => s.agentOrder);
  const ids = useMemo(() => order.filter((id) => agents[id]), [order, agents]);
  const suggested = useMemo(() => pickEditAgent(versions, agents, order, fileKind(file.name, file.mime)), [versions, agents, order, file.name, file.mime]);
  const [picked, setPicked] = useState<string | null>(null);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [sent, setSent] = useState<{ to: string; kind: "change" | "comment" } | null>(null);
  const [err, setErr] = useState("");
  const agent = picked && agents[picked] ? picked : suggested;
  useEffect(() => { setSent(null); setText(""); setErr(""); setPicked(null); }, [file.id]);

  const send = async (kind: "change" | "comment") => {
    const v = text.trim();
    if (!v || busy) return;
    setBusy(true); setErr("");
    const body = t(kind === "change" ? "fileviewer.editText" : "fileviewer.commentText", { name: file.name, path: file.path, v: file.current_version, text: v });
    const ref = { file_id: file.id, path: file.path, name: file.name, mime: file.mime, size: file.size, version: file.current_version, action: "attach" };
    try {
      await sendChat(agent ? `agent:${agent}` : "office", body, "user", [file.path], [ref]);
      setText(""); setSent({ to: agent ?? "all", kind });
    } catch { setErr(t("chat.sendError")); }
    setBusy(false);
  };

  return (
    <div className="border-t border-line bg-panel p-3" data-testid="viewer-ask">
      <form onSubmit={(e) => { e.preventDefault(); send("change"); }} className="flex flex-col gap-1.5">
        <div className="flex items-center gap-2">
          <label htmlFor="viewer-ask" className="text-[11px] font-semibold text-ink2">{t("fileviewer.askSend")}</label>
          <span className="flex-1" />
          {ids.length > 0 && (
            <label className="flex items-center gap-1 text-[11px] text-mute">
              <span>{t("fileviewer.askAgent")}</span>
              <select data-testid="viewer-ask-agent" value={agent ?? ""} onChange={(e) => setPicked(e.target.value)}
                className="max-w-[160px] rounded-md border border-line bg-panel2 px-1.5 py-1 text-ink outline-none focus:border-accent">
                {ids.map((id) => <option key={id} value={id}>{name(id)}</option>)}
              </select>
            </label>
          )}
        </div>
        <textarea id="viewer-ask" data-testid="viewer-ask-input" rows={2} value={text} onChange={(e) => setText(e.target.value)} placeholder={t("fileviewer.askPlaceholder")}
          onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); send("change"); } }}
          className="min-h-[52px] w-full resize-y rounded-lg border border-line bg-panel2 px-2.5 py-1.5 text-[12.5px] text-ink outline-none placeholder:text-mute focus:border-accent" />
        <div className="flex items-center gap-1.5">
          <span className="flex-1 text-[10.5px] text-mute">{t("fileviewer.askHint")}</span>
          <button type="submit" data-testid="viewer-ask-send" disabled={busy || !text.trim()} className="whitespace-nowrap rounded-md border border-accent bg-accent px-2.5 py-1.5 text-xs font-semibold text-white hover:bg-accent-hover disabled:opacity-40">{t("fileviewer.askSend")}</button>
          <button type="button" data-testid="viewer-comment-send" disabled={busy || !text.trim()} onClick={() => send("comment")} className="whitespace-nowrap rounded-md border border-line px-2.5 py-1.5 text-xs font-medium text-ink2 hover:border-accent disabled:opacity-40">{t("fileviewer.comment")}</button>
        </div>
      </form>
      {sent && <div role="status" data-testid="viewer-ask-sent" className="mt-1.5 text-[11px] text-ok">{t(sent.kind === "change" ? "fileviewer.askSent" : "fileviewer.commentSent", { name: sent.to === "all" ? t("common.everyone") : name(sent.to).split(" ")[0] })}</div>}
      {err && <div role="alert" className="mt-1.5 text-[11px] text-err">{err}</div>}
    </div>
  );
}
