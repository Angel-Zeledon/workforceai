"use client";
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { useAudience } from "@/lib/audience";
import { officesApi } from "@/lib/officesApi";
import { LANDED_KEY, landOnDepartment, myDepartments } from "@/lib/deptLogic";

/**
 * D2: a company account that enters the app at "/" goes to its department's Drive (/d) instead of the 3D office. Once
 * per tab: the first entry leaves a mark in sessionStorage, so coming back to "/" later (from "Más") stays there. Without
 * storage (private mode, blocked) nothing is redirected.
 */
export function useDepartmentLanding(pathname: string) {
  const router = useRouter();
  const audience = useAudience();
  // true while this tab's entry at "/" may still land on the department
  const [entry, setEntry] = useState(false);
  useEffect(() => {
    let first = false;
    try { first = !sessionStorage.getItem(LANDED_KEY); sessionStorage.setItem(LANDED_KEY, "1"); } catch { /* no storage: never */ }
    if (first && pathname === "/") setEntry(true);
  }, []); // eslint-disable-line react-hooks/exhaustive-deps -- once, when the tab enters the app
  useEffect(() => {
    if (!entry) return;
    if (pathname !== "/" || (audience !== null && audience !== "company")) { setEntry(false); return; } // elsewhere, or not a company
    if (audience === null) return; // /me/welcome has not answered yet
    let live = true;
    officesApi.list()
      .then((r) => { if (live && landOnDepartment({ firstEntry: true, path: pathname, audience, departments: myDepartments(r.offices).length })) router.replace("/d"); })
      .catch(() => { /* stay in the office */ })
      .finally(() => { if (live) setEntry(false); });
    return () => { live = false; };
  }, [entry, pathname, audience, router]);
}
