import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'

// Apply persisted theme class before first render to prevent flash
const persisted = localStorage.getItem('conduit-layout')
if (persisted) {
  try {
    const parsed = JSON.parse(persisted)
    const theme = parsed?.state?.theme
    if (theme === 'light' || theme === 'dark') {
      document.documentElement.classList.add(theme)
    } else {
      document.documentElement.classList.add('dark')
    }
  } catch {
    document.documentElement.classList.add('dark')
  }
} else {
  document.documentElement.classList.add('dark')
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
