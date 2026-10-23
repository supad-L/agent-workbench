import React from 'react';
import { createRoot } from 'react-dom/client';
import Workbench from './workbench';
import { AppearanceProvider, applyDocumentAppearance, readAppearance, resolveAppearance } from './appearance';
import 'antd/dist/reset.css';
import './style.css';

const initialAppearance = readAppearance();
applyDocumentAppearance(resolveAppearance(initialAppearance));
createRoot(document.getElementById('root')!).render(
  <React.StrictMode><AppearanceProvider initialAppearance={initialAppearance}><Workbench /></AppearanceProvider></React.StrictMode>,
);
