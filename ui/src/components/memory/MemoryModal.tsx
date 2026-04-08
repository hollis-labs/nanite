import { useCallback, useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "@/components/ui/dialog";
import { useLayoutStore } from "@/stores/useLayoutStore";
import { MemoryBrowse } from "./MemoryBrowse";
import { MemoryDetail } from "./MemoryDetail";

type View = "browse" | "detail" | "create";

export function MemoryModal() {
  const open = useLayoutStore((s) => s.memoryModalOpen);
  const setOpen = useLayoutStore((s) => s.setMemoryModalOpen);
  const [view, setView] = useState<View>("browse");
  const [selectedKey, setSelectedKey] = useState<string | null>(null);

  const handleClose = useCallback(
    (isOpen: boolean) => {
      if (!isOpen) {
        setOpen(false);
        setTimeout(() => {
          setView("browse");
          setSelectedKey(null);
        }, 200);
      }
    },
    [setOpen],
  );

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent className="sm:max-w-2xl max-h-[80vh] flex flex-col p-0 overflow-hidden">
        <DialogTitle className="sr-only">Memories</DialogTitle>
        <DialogDescription className="sr-only">Browse and manage agent memories</DialogDescription>
        {view === "browse" && (
          <MemoryBrowse
            onSelect={(key) => {
              setSelectedKey(key);
              setView("detail");
            }}
            onCreate={() => {
              setSelectedKey(null);
              setView("create");
            }}
          />
        )}
        {(view === "detail" || view === "create") && (
          <MemoryDetail
            memoryKey={view === "create" ? null : selectedKey}
            onBack={() => {
              setView("browse");
              setSelectedKey(null);
            }}
          />
        )}
      </DialogContent>
    </Dialog>
  );
}
