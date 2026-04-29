/**
 * useArtifactUpload — shared file-upload helper for artifact creation.
 *
 * F4 (CW-20260429-0004): the upload path was previously inlined in
 * ChatComposer; the right-rail Artifacts panel needs the same surface so the
 * panel itself can host a dropzone. Extracting here so both consumers go
 * through one path and keep the same artifact-creation semantics
 * (POST /api/artifacts/upload + cache invalidation).
 *
 * Returns:
 *   - uploading       : boolean, true while a batch is in flight
 *   - dragOver        : boolean, drag-state hint for the wrapping element
 *   - setDragOver     : direct setter (callers may want their own enter/leave
 *                       logic, e.g. to dim only when files are present)
 *   - handleDrop      : drop handler — preventDefault + upload all dropped
 *                       files, then invalidate the artifacts query
 *   - handleFileUpload: programmatic upload from a FileList (file-picker case)
 *
 * Behavior matches the pre-extraction ChatComposer flow exactly: serial
 * uploads, errors logged but not surfaced (the BE returns 4xx/5xx and the
 * caller relies on the next refetch to reconcile state).
 */

import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useState } from "react";
import { api } from "@/lib/api";

export interface UseArtifactUploadResult {
  uploading: boolean;
  dragOver: boolean;
  setDragOver: (v: boolean) => void;
  handleDrop: (e: React.DragEvent) => Promise<void>;
  handleFileUpload: (files: FileList | null) => Promise<void>;
}

export function useArtifactUpload(sessionId: string | null): UseArtifactUploadResult {
  const queryClient = useQueryClient();
  const [dragOver, setDragOver] = useState(false);
  const [uploading, setUploading] = useState(false);

  const handleDrop = useCallback(
    async (e: React.DragEvent) => {
      e.preventDefault();
      setDragOver(false);
      if (!sessionId || !e.dataTransfer.files.length) return;
      setUploading(true);
      try {
        // Per-file try/catch so one failure doesn't strand remaining files,
        // and call sites that use `void handleDrop(e)` don't see unhandled
        // rejections. Matches the documented "log but don't surface" contract
        // (PR #93 Copilot feedback).
        for (const file of Array.from(e.dataTransfer.files)) {
          try {
            await api.uploadArtifact(sessionId, file);
          } catch (err) {
            console.error("Failed to upload artifact:", file.name, err);
          }
        }
      } finally {
        setUploading(false);
        queryClient.invalidateQueries({ queryKey: ["artifacts", sessionId] });
      }
    },
    [sessionId, queryClient],
  );

  const handleFileUpload = useCallback(
    async (files: FileList | null) => {
      if (!files || files.length === 0 || !sessionId) return;
      setUploading(true);
      try {
        for (const file of Array.from(files)) {
          await api.uploadArtifact(sessionId, file);
        }
      } catch (err) {
        // Match the pre-extraction behavior — log but do not surface.
        console.error("Failed to upload artifact:", err);
      } finally {
        setUploading(false);
        queryClient.invalidateQueries({ queryKey: ["artifacts", sessionId] });
      }
    },
    [sessionId, queryClient],
  );

  return { uploading, dragOver, setDragOver, handleDrop, handleFileUpload };
}
