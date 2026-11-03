// Opening a pedido from anywhere in the app: on the Chats screen the panel opens beside the chat (no navigation, the
// person stays where they were); elsewhere it navigates to /chats?pedido=<id>.
import { go } from "./navBridge";

export const PEDIDO_OPEN_EVENT = "pedido:open";
export const pedidoLink = (requestId: string) => `/chats?pedido=${encodeURIComponent(requestId)}`;

export function openPedido(requestId: string) {
  if (typeof window !== "undefined" && window.location.pathname.startsWith("/chats")) {
    window.dispatchEvent(new CustomEvent<string>(PEDIDO_OPEN_EVENT, { detail: requestId }));
    return;
  }
  go(pedidoLink(requestId));
}
