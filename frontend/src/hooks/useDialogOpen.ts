import { useEffect, type RefObject } from "react";

/** Keeps a native <dialog> element's open/closed state in sync with isOpen. */
export function useDialogOpen(dialogRef: RefObject<HTMLDialogElement | null>, isOpen: unknown) {
  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    if (isOpen && !dialog.open) {
      dialog.showModal();
    } else if (!isOpen && dialog.open) {
      dialog.close();
    }
  }, [dialogRef, isOpen]);
}
