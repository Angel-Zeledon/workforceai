"use client";
import { useCallback, useEffect, useState } from "react";
import { agentOfficesApi } from "./agentOfficesApi";
import type { AgentOfficesView } from "./agentOfficesLogic";

/** The "also works in" offices of a list of agents. The server's multi-office switch is read from the first agent's view:
 *  when it is off nothing else is requested and `enabled` stays false, so no screen shows controls for other offices. */
export function useAgentOffices(ids: string[]) {
  const [enabled, setEnabled] = useState(false);
  const [views, setViews] = useState<Record<string, AgentOfficesView>>({});
  const key = ids.join("|");

  useEffect(() => {
    let live = true;
    setEnabled(false); setViews({});
    if (!ids.length) return;
    (async () => {
      const first = await agentOfficesApi.get(ids[0]).catch(() => null);
      if (!live || !first?.enabled) return;
      setEnabled(true); setViews({ [ids[0]]: first });
      const rest = ids.slice(1);
      for (let i = 0; i < rest.length && live; i += 4) {
        const got = await Promise.all(rest.slice(i, i + 4).map((id) => agentOfficesApi.get(id).then((v) => [id, v] as const).catch(() => null)));
        if (live) setViews((cur) => ({ ...cur, ...Object.fromEntries(got.filter((g): g is readonly [string, AgentOfficesView] => !!g)) }));
      }
    })();
    return () => { live = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  const setView = useCallback((id: string, v: AgentOfficesView) => setViews((cur) => ({ ...cur, [id]: v })), []);
  return { enabled, views, setView };
}
