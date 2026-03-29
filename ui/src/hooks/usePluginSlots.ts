import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { UISlotEntry, UISlotName } from "@/lib/types";

/**
 * Fetches all UI slot entries from the backend and returns the entries
 * for a specific slot name, sorted by priority (descending).
 *
 * Cached via React Query with a 30s stale time — slot registrations
 * change rarely (only on plugin load/unload).
 */
export function usePluginSlots(slotName: UISlotName): UISlotEntry[] {
  const { data } = useQuery({
    queryKey: ["plugin-ui-slots"],
    queryFn: api.listUISlots,
    staleTime: 30_000,
  });

  return data?.[slotName] ?? [];
}
