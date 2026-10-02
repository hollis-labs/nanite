import { getDynamicSlotComponent } from '@/lib/plugin-loader'

export function getSlotComponent(name: string, owner?: string, id?: string) {
  return getDynamicSlotComponent(name, owner, id)?.component
}
