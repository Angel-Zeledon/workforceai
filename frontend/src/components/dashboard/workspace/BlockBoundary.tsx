"use client";
import { Component, type ReactNode } from "react";
import { tr } from "@/lib/i18n";

/**
 * One block of the workspace (team, conversation, "Te necesitan", "En curso", "Listo") fails alone: the rest keeps
 * working and the person can retry that block. Entering Oficina must never break because of one of them.
 */
export class BlockBoundary extends Component<{ name: string; children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  componentDidCatch(err: unknown) { console.error(`[workspace] block "${this.props.name}" failed`, err); }
  render() {
    if (!this.state.failed) return this.props.children;
    return (
      <div role="alert" data-testid={`block-error-${this.props.name}`} className="rounded-[10px] border border-line bg-panel2 px-3 py-3 text-[12.5px] text-ink2">
        <p>{tr("work.block.error")}</p>
        <button type="button" onClick={() => this.setState({ failed: false })} className="mt-1.5 min-h-10 rounded-md border border-line-strong bg-panel px-3 text-[12.5px] font-medium text-accent hover:bg-panel2">{tr("work.block.retry")}</button>
      </div>
    );
  }
}
