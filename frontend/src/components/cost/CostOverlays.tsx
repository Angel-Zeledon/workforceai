"use client";
import { useEffect } from "react";
import { useCost } from "@/lib/cost";
import { BudgetAlerts } from "./BudgetAlerts";
import { EstimateGate } from "./EstimateGate";

/** Cost-control overlays mounted once in the Shell: estimate confirmation + budget stops. */
export function CostOverlays() {
  const loadStatus = useCost((s) => s.loadStatus);
  useEffect(() => { void loadStatus(); }, [loadStatus]);
  return (
    <>
      <BudgetAlerts />
      <EstimateGate />
    </>
  );
}
