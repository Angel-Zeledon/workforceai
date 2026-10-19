"use client";
import { useT } from "@/lib/i18n";
import { useCan } from "@/lib/session";
import { Btn } from "../ui";
import { Icon } from "../icons";
import type { MeetingSettings, RoomState } from "./api";
import { fmtTimeShort, useRoomName } from "./shared";

/** The meeting rooms of the office: who is in each one now, what is next, and a button to open one right away. */
export function RoomsBoard({ rooms, settings, onOpenMeeting, onOpenRoom }: {
  rooms: RoomState[]; settings: MeetingSettings | null; onOpenMeeting: (id: string) => void; onOpenRoom: (roomId: string) => void;
}) {
  const { t } = useT();
  const canWrite = useCan("requests:create");
  const roomName = useRoomName(settings?.rooms);
  if (rooms.length === 0) return <p className="rounded-lg border border-dashed border-line-strong px-4 py-6 text-center text-[13px] text-mute">{t("meetings.rooms.empty")}</p>;
  return (
    <div data-testid="rooms-board" className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
      {rooms.map((r) => (
        <article key={r.id} data-testid={`room-card-${r.id}`} data-busy={r.current ? "1" : "0"} className="rounded-xl border border-line bg-panel p-4">
          <div className="flex items-start justify-between gap-2">
            <div>
              <h3 className="text-[14px] font-semibold text-ink">{roomName(r.id)}</h3>
              <p className="text-[11px] text-mute">{r.capacity ? t("meetings.rooms.capacity", { n: r.capacity }) : t("meetings.rooms.anyCapacity")}</p>
            </div>
            <span className={`inline-flex items-center gap-1 rounded px-1.5 py-[2px] text-[10px] font-semibold uppercase tracking-wide ${r.current ? "bg-err/10 text-err" : "bg-ok/10 text-ok"}`}>
              <span className={`h-1.5 w-1.5 rounded-full ${r.current ? "bg-err" : "bg-ok"}`} />{r.current ? t("meetings.rooms.inUse") : t("meetings.rooms.free")}
            </span>
          </div>
          <div className="mt-3 min-h-[2.5rem] space-y-1 text-xs">
            {r.current && (
              <button type="button" onClick={() => onOpenMeeting(r.current!.id)} className="block w-full truncate text-left text-ink hover:text-accent" data-testid="room-current">
                <Icon name="users" size={12} className="mr-1 inline" />{r.current.title} <span className="text-mute">· {t("meetings.rooms.until", { time: fmtTimeShort(r.current.end) })}</span>
              </button>
            )}
            {r.next && (
              <button type="button" onClick={() => onOpenMeeting(r.next!.id)} className="block w-full truncate text-left text-mute hover:text-accent" data-testid="room-next">
                {t("meetings.rooms.next")}: {fmtTimeShort(r.next.start)} · {r.next.title}
              </button>
            )}
            {!r.current && !r.next && <p className="text-mute">{t("meetings.rooms.nothing")}</p>}
          </div>
          {canWrite && <div className="mt-3"><Btn data-testid={`room-open-${r.id}`} disabled={!!r.current} onClick={() => onOpenRoom(r.id)}>{t("meetings.rooms.open")}</Btn></div>}
        </article>
      ))}
    </div>
  );
}
