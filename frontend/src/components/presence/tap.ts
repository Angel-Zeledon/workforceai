"use client";
// The bridge between the single realtime socket (lib/ws.ts) and the presence / people-chat stores: ws.ts hands every
// frame to `tapFrame` and tells us when the socket opens or closes; we hand back a `sendFrame` to talk to the server.
import { usePresence } from "./store";
import { usePeopleChat } from "../chats/peopleChatStore";

type Sender = (frame: Record<string, unknown>) => boolean;
let sender: Sender | null = null;

/** Called by ws.ts with a function that writes on the open socket (null when it closes). */
export function setSocketSender(fn: Sender | null) {
  sender = fn;
  if (fn) { void usePresence.getState().load(); void usePeopleChat.getState().loadChannels(); }
}

/** Sends a presence frame ({type:"presence.move"|"presence.status"|"presence.ping", ...}); false when offline. */
export function sendPresence(frame: Record<string, unknown>): boolean {
  try { return sender ? sender(frame) : false; } catch { return false; }
}

/** Called by ws.ts for every frame. Cheap prefix test, so non-presence traffic costs nothing. */
export function tapFrame(f: { type: string; payload: any }) {
  if (f.type.startsWith("presence.")) usePresence.getState().apply(f.type, f.payload);
  else if (f.type.startsWith("pchat.")) usePeopleChat.getState().apply(f.type, f.payload);
}

// ---- hook point of the call stream -----------------------------------------------------------------------

/** What the "start conversation" button hands to whoever implements the call (a separate stream). */
export interface ConversationRequest { withUserId: string; withName: string; distance: number }
type Starter = (req: ConversationRequest) => void;
let starter: Starter | null = null;

/**
 * Registers the handler of "start conversation". Until the call stream registers one, the default opens the DM chat.
 * Returns the unregister function.
 */
export function registerConversationStarter(fn: Starter): () => void {
  starter = fn;
  return () => { if (starter === fn) starter = null; };
}
export function startConversation(req: ConversationRequest, fallback: Starter) {
  (starter ?? fallback)(req);
}
