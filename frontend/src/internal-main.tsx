import React from 'react'
import ReactDOM from 'react-dom/client'
import { HashRouter } from 'react-router-dom'
import { ManagementApp } from './App'
import './styles.css'
import './staff.css'
import './internal.css'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <HashRouter><ManagementApp /></HashRouter>
  </React.StrictMode>,
)
