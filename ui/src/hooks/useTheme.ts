// Reads the active theme from user settings and applies it to the DOM.
// Also exposes helpers for theme CRUD via ext_settings.
import { useEffect, useMemo, useCallback } from 'react'
import type { Theme } from '@/lib/theme/types'
import { BUILTIN_THEMES, NANITE_DEFAULT, getBuiltinTheme } from '@/lib/theme/defaults'
import { applyTheme } from '@/lib/theme/apply'
import { useSettings, useSettingsMutation } from './useSettings'
import type { UserSettings } from '@/lib/types'

const EXT_ACTIVE_KEY = 'active_theme'
const EXT_CUSTOM_KEY = 'custom_themes'

export function useTheme() {
  const { data: settings } = useSettings()
  const mutation = useSettingsMutation()

  const ext = (settings?.ext_settings ?? {}) as Record<string, unknown>
  const activeThemeId = (ext[EXT_ACTIVE_KEY] as string | undefined) ?? NANITE_DEFAULT.id
  const customThemes = useMemo<Theme[]>(
    () => (Array.isArray(ext[EXT_CUSTOM_KEY]) ? (ext[EXT_CUSTOM_KEY] as Theme[]) : []),
    [ext],
  )

  const allThemes: Theme[] = useMemo(
    () => [...BUILTIN_THEMES, ...customThemes],
    [customThemes],
  )

  const activeTheme: Theme = useMemo(
    () => allThemes.find((t) => t.id === activeThemeId) ?? NANITE_DEFAULT,
    [allThemes, activeThemeId],
  )

  // Apply the active theme to the DOM whenever it changes.
  useEffect(() => {
    applyTheme(activeTheme)
  }, [activeTheme])

  const setActiveTheme = useCallback(
    (id: string) => {
      mutation.mutate({
        ext_settings: {
          ...(settings?.ext_settings ?? {}),
          [EXT_ACTIVE_KEY]: id,
        },
      } as Partial<UserSettings>)
    },
    [mutation, settings?.ext_settings],
  )

  const saveCustomTheme = useCallback(
    (theme: Theme) => {
      const next = customThemes.filter((t) => t.id !== theme.id).concat({ ...theme, builtin: false })
      mutation.mutate({
        ext_settings: {
          ...(settings?.ext_settings ?? {}),
          [EXT_CUSTOM_KEY]: next,
          [EXT_ACTIVE_KEY]: theme.id,
        },
      } as Partial<UserSettings>)
    },
    [mutation, customThemes, settings?.ext_settings],
  )

  const deleteCustomTheme = useCallback(
    (id: string) => {
      const next = customThemes.filter((t) => t.id !== id)
      const newActive = activeThemeId === id ? NANITE_DEFAULT.id : activeThemeId
      mutation.mutate({
        ext_settings: {
          ...(settings?.ext_settings ?? {}),
          [EXT_CUSTOM_KEY]: next,
          [EXT_ACTIVE_KEY]: newActive,
        },
      } as Partial<UserSettings>)
    },
    [mutation, customThemes, activeThemeId, settings?.ext_settings],
  )

  const duplicateTheme = useCallback(
    (source: Theme, newName: string): Theme => {
      const id = `custom-${Date.now()}`
      return {
        ...source,
        id,
        name: newName,
        builtin: false,
        tokens: {
          dark: { ...source.tokens.dark },
          light: { ...source.tokens.light },
        },
      }
    },
    [],
  )

  return {
    activeTheme,
    activeThemeId,
    allThemes,
    customThemes,
    builtinThemes: BUILTIN_THEMES,
    setActiveTheme,
    saveCustomTheme,
    deleteCustomTheme,
    duplicateTheme,
    getBuiltinTheme,
  }
}

/**
 * Lightweight provider — call once near the app root. Reads user settings
 * and applies the selected theme to the DOM on mount and on changes.
 */
export function ThemeEffect() {
  useTheme()
  return null
}
