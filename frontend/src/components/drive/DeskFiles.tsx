"use client";
import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { drive, useDriveNav, type DriveFile } from "@/lib/drive";
import { useStore } from "@/lib/store";
import { Btn, Card, Empty } from "../ui";
import { Icon } from "../icons";

/** An agent's desk in the drive (/Agents/<id>): the files it keeps while it works. */
export function DeskFiles({ id }: { id: string }) {
  const { t } = useT();
  const show = useDriveNav((s) => s.show);
  const last = useStore((s) => s.files[id]);
  const tick = useStore((s) => s.agentTick[id] ?? 0);
  const [files, setFiles] = useState<DriveFile[] | null>(null);
  const desk = `/Agents/${id}`;
  useEffect(() => {
    let alive = true;
    drive.list(desk).then((l) => { if (alive) setFiles(l.files); }).catch(() => { if (alive) setFiles([]); });
    return () => { alive = false; };
  }, [desk, tick]);
  return (
    <Card title={t("desk.title")} right={<Btn data-testid="desk-open-drive" onClick={() => show(desk)}>{t("desk.openInDrive")}</Btn>}>
      <div data-testid="desk-files" className="space-y-1.5 p-3">
        {last && Date.now() - last.ts < 120_000 && (
          <div data-testid="desk-last-activity" className="rounded-lg bg-accent/5 px-2 py-1 text-[11px] text-accent">
            {t(`desk.action.${last.action}`, { path: last.path })}
          </div>
        )}
        {files === null ? <Empty>{t("drive.loading")}</Empty> : !files.length ? <Empty>{t("desk.empty")}</Empty> : files.map((f) => (
          <button type="button" key={f.id} data-testid={`desk-file-${f.name}`} onClick={() => show(desk)}
            className="flex w-full items-center gap-2 rounded-lg border border-line px-2 py-1.5 text-left text-xs hover:border-accent">
            <Icon name="doc" size={13} /><span className="min-w-0 flex-1 truncate text-ink">{f.name}</span>
            <span className="text-mute">v{f.current_version}</span>
          </button>
        ))}
      </div>
    </Card>
  );
}
