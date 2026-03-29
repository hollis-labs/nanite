import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'

// Apply persisted theme class before first render to prevent flash
try {
  const persisted = localStorage.getItem('conduit-layout')
  const theme = persisted ? JSON.parse(persisted)?.state?.theme : undefined
  document.documentElement.classList.remove('dark', 'light')
  document.documentElement.classList.add(theme === 'light' ? 'light' : 'dark')
} catch {
  document.documentElement.classList.remove('dark', 'light')
  document.documentElement.classList.add('dark')
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
