import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import jquery from 'jquery'
import './index.css'
import App from './App.tsx'

window.$ = window.jQuery = jquery
void import('bootstra.386/v5.3.1/js/dos.js').catch((error) => {
  document.body.style.visibility = 'visible'
  console.error('BOOTSTRA.386 loading animation failed', error)
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
