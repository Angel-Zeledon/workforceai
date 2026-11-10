"use client";
import { useEffect } from "react";
import { create } from "zustand";
import { officesApi } from "@/lib/officesApi";
import type { OfficeView } from "@/lib/officesLogic";

/** Offices and the office of each agent, for the connections of a department (D3). Loaded once per page; without
 *  offices (or when they cannot be read) everything is the whole company, as before. */
interface ConnOffices {
  offices: OfficeView[];
  /** agent id -> office id ("" = General) */
  agentOffice: Record<string, string>;
  loaded: boolean;
  load: (force?: boolean) => void;
}

export const useConnOfficesStore = create<ConnOffices>((set, get) => ({
  offices: [], agentOffice: {}, loaded: false,
  load: (force = false) => {
    if (get().loaded && !force) return;
    set({ loaded: true });
    Promise.all([officesApi.list(), officesApi.directory()]).then(([o, d]) => {
      const agentOffice: Record<string, string> = {};
      for (const a of d.agents) agentOffice[a.id] = a.office_id === "general" ? "" : a.office_id;
      for (const off of o.offices) if (!off.general) for (const a of off.agents ?? []) agentOffice[a] = off.id; // the office's own list
      set({ offices: o.offices, agentOffice });
    }).catch(() => set({ offices: [], agentOffice: {} }));
  },
}));

export function useConnOffices() {
  const offices = useConnOfficesStore((x) => x.offices);
  const agentOffice = useConnOfficesStore((x) => x.agentOffice);
  const load = useConnOfficesStore((x) => x.load);
  useEffect(() => { load(); }, [load]);
  const name = (id: string) => offices.find((o) => o.id === id)?.name ?? id;
  const departments = offices.filter((o) => !o.general);
  return { offices, departments, agentOffice, name };
}
