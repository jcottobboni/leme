import './persist' // first: restores persisted UI state before any store reads it
import { createRoot } from 'react-dom/client'
import '@fontsource-variable/inter'
import '@fontsource-variable/jetbrains-mono'
import { App } from './App'
import { ErrorBoundary } from './components/ErrorBoundary'
import { useTheme } from './features/shell/theme'
import './styles.css'

// Theme before the first paint (no dark flash on the light theme).
document.documentElement.dataset.theme = useTheme.getState().theme
createRoot(document.getElementById('root')!).render(<ErrorBoundary full label="Leme"><App /></ErrorBoundary>)
