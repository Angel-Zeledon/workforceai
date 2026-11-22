import { tr, getLocale, type Locale } from "./i18n-core";
import { classifyError, errorFacts, type ErrContext } from "./errorsCore";
import { useToasts } from "./toast";

export type { ErrContext } from "./errorsCore";

/** What a screen shows when something failed: a sentence about what is happening, what to do, and (only when nothing
 * more specific is known) a reference to share. Never a code, a status line or the text of an exception. */
export interface DescribedError {
  title: string;
  detail?: string;
  action?: string;
  /** Trying again can help (network, timeout, 5xx, a conflict): the screen may offer "Reintentar". */
  retry: boolean;
  ref?: string;
}

const localRefs = new WeakMap<object, string>();
const makeRef = (): string => Math.random().toString(16).slice(2, 10).padEnd(8, "0");

/** The reference of an error nobody knows: the backend's, or one made here and logged with the raw error. */
function refFor(err: unknown, given?: string): string {
  if (given) return given;
  const key = err !== null && typeof err === "object" ? (err as object) : null;
  const known = key ? localRefs.get(key) : undefined;
  if (known) return known;
  const ref = makeRef();
  if (key) localRefs.set(key, ref);
  // the raw detail stays in the console, under the same reference the person can quote
  if (typeof console !== "undefined") console.error(`[error ${ref}]`, err);
  return ref;
}

/** Translates anything caught into a message in the active language (see errorsCore.ts for the rules). */
export function describeError(err: unknown, context: ErrContext = {}, locale: Locale = getLocale()): DescribedError {
  const p = classifyError(err, context);
  const ref = p.needsRef ? refFor(err, p.ref) : p.ref;
  const t = (key: string) => tr(key, p.vars, locale);
  const out: DescribedError = { title: t(p.titleKey), retry: p.retry };
  if (p.detailKey) out.detail = t(p.detailKey);
  if (p.actionKey) out.action = t(p.actionKey);
  // a server-side failure with the backend's reference: still offer it, it is how support finds the log line
  if (ref) out.ref = ref;
  return out;
}

/** The message as one string (toasts, inline lines): title, what to do, and the reference when there is one. */
export function errorText(err: unknown, context: ErrContext = {}, locale: Locale = getLocale()): string {
  const d = describeError(err, context, locale);
  const parts = [d.title, d.detail, d.action].filter(Boolean) as string[];
  if (d.ref) parts.push(tr("err.ref", { ref: d.ref }, locale));
  return parts.join(" ");
}

/** The refusal is a 401: the session is gone, so the screen should send the person to sign in. */
export const isSessionError = (err: unknown): boolean => errorFacts(err).status === 401;

/** Shows a failed call as a toast with a plain sentence (replaces `toast(e.message, "err")`). */
export function toastError(err: unknown, context: ErrContext = {}): void {
  useToasts.getState().push(errorText(err, context), "err", 7000);
}
