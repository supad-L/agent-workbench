import React from 'react';
import { createRoot } from 'react-dom/client';
import Workbench from './workbench';
import { AppearanceProvider, applyDocumentAppearance, applyPlatform, readAppearance, resolveAppearance } from './appearance';
import 'antd/dist/reset.css';
import './styles/tokens.css';
import './styles/base.css';
import './styles/layout.css';
import './styles/components.css';

applyPlatform();
const initialAppearance = readAppearance();
applyDocumentAppearance(resolveAppearance(initialAppearance));
createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <AppearanceProvider initialAppearance={initialAppearance}><Workbench /></AppearanceProvider>
  </React.StrictMode>,
);
