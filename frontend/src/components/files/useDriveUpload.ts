"use client";
import { useState } from "react";
import { useT } from "@/lib/i18n";
import { drive, useUploadLimit, type UploadResult } from "@/lib/drive";
import { filesErrorKey } from "@/lib/filesLogic";
import { toast } from "@/lib/toast";

/**
 * Uploads into a folder in multipart batches with one progress bar for the whole upload (the Files screen). Errors come
 * back in words (never the backend's code): files over the cap are named, and so are the ones the server refused.
 */
export function useDriveUpload(folder: string, onDone: () => void) {
  const { t } = useT();
  const maxBytes = useUploadLimit();
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState<number | null>(null);
  const [error, setError] = useState("");

  const upload = async (files: FileList | File[]) => {
    const all = Array.from(files);
    const tooBig = all.filter((f) => f.size > maxBytes);
    const list = all.filter((f) => f.size <= maxBytes);
    setBusy(true); setError("");
    const total = list.reduce((n, f) => n + f.size, 0) || 1;
    let done = 0;
    const failed: UploadResult[] = [];
    let thrown = "";
    try {
      setProgress(0);
      // batches of at most 20 files / ~200 MB: a failed request only repeats its own batch
      for (let i = 0; i < list.length;) {
        const batch: File[] = [];
        let bytes = 0;
        while (i < list.length && batch.length < 20 && (batch.length === 0 || bytes + list[i].size <= 200 << 20)) { bytes += list[i].size; batch.push(list[i++]); }
        const res = await drive.uploadMany(folder, batch.map((f) => ({ name: f.name, file: f })), (loaded) => setProgress(Math.min(1, (done + loaded) / total)));
        done += bytes;
        failed.push(...res.filter((r) => r.status >= 300));
      }
      onDone();
      if (list.length > failed.length) toast(t("toast.uploaded"));
    } catch (e) {
      thrown = t(filesErrorKey(e));
    } finally {
      setBusy(false); setProgress(null);
    }
    const msgs: string[] = [];
    if (thrown) msgs.push(thrown);
    if (tooBig.length) msgs.push(`${tooBig.map((f) => f.name).join(", ")}: ${t("att.err.file_too_large", { mb: Math.round(maxBytes / (1 << 20)) })}`);
    if (failed.length) msgs.push(`${failed.map((f) => f.name).join(", ")}: ${t(filesErrorKey({ code: failed[0].code, status: failed[0].status }))}`);
    setError(msgs.join(" "));
  };
  return { upload, busy, progress, error, clearError: () => setError("") };
}
