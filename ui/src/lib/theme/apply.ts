// Applies a Theme to the DOM by writing a <style> element with :root and .light
// selectors that override index.css token values at runtime.
import type { Theme, TokenValues } from './types'

const STYLE_ID = 'nanite-theme-override'

function serializeTokens(tokens: TokenValues): string {
  return Object.entries(tokens)
    .map(([key, value]) => `  --c-${key}: ${value};`)
    .join('\n')
}

export function applyTheme(theme: Theme): void {
  const css = `:root {\n${serializeTokens(theme.tokens.dark)}\n}\n.light {\n${serializeTokens(theme.tokens.light)}\n}\n`
  let style = document.getElementById(STYLE_ID) as HTMLStyleElement | null
  if (!style) {
    style = document.createElement('style')
    style.id = STYLE_ID
    document.head.appendChild(style)
  }
  style.textContent = css
}

export function clearThemeOverride(): void {
  const style = document.getElementById(STYLE_ID)
  if (style) style.remove()
}
