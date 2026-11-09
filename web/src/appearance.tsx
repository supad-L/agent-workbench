import { createContext, useContext, useEffect, useLayoutEffect, useMemo, useState, type ReactNode } from 'react';
import { App as AntApp, ConfigProvider, Segmented, theme, type ThemeConfig } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import { api } from './api';

export type AppearancePreference = 'system' | 'light' | 'dark';
export const appearanceStorageKey = 'agent-workbench.appearance.v1';
type AppearanceState = { preference: AppearancePreference; systemDark: boolean };
type AppearanceValue = { preference: AppearancePreference; resolved: 'light' | 'dark'; saveError: string; setPreference: (value: AppearancePreference) => void };
const AppearanceContext = createContext<AppearanceValue | null>(null);

export function readAppearance(): AppearanceState {
  let preference: AppearancePreference = 'system';
  try {
    const stored = window.localStorage.getItem(appearanceStorageKey);
    if (stored === 'system' || stored === 'light' || stored === 'dark') preference = stored;
  } catch { /* Theme changes remain available without browser storage. */ }
  return { preference, systemDark: window.matchMedia('(prefers-color-scheme: dark)').matches };
}

export function resolveAppearance(state: AppearanceState): 'light' | 'dark' {
  return state.preference === 'system' ? state.systemDark ? 'dark' : 'light' : state.preference;
}

export function applyDocumentAppearance(resolved: 'light' | 'dark') {
  document.documentElement.dataset.theme = resolved;
  document.documentElement.style.colorScheme = resolved;
}

/** Marks macOS (traffic-light inset) and native vibrancy (Electron on macOS). */
export function applyPlatform(userAgent = navigator.userAgent) {
  const mac = /Macintosh|Mac OS X/.test(userAgent);
  if (mac) document.documentElement.dataset.platform = 'mac';
  if (mac && /Electron\//.test(userAgent)) document.documentElement.dataset.vibrancy = 'sidebar';
}

const palette = {
  light: {
    accent: '#007aff', bg: '#ffffff', layout: '#f5f5f7', card: '#ffffff', elevated: '#ffffff',
    text: 'rgba(0,0,0,0.85)', text2: 'rgba(0,0,0,0.55)', text3: 'rgba(0,0,0,0.38)',
    border: 'rgba(0,0,0,0.14)', border2: 'rgba(0,0,0,0.08)', fill: 'rgba(0,0,0,0.05)',
    success: '#34c759', warning: '#ff9500', error: '#ff3b30',
  },
  dark: {
    accent: '#0a84ff', bg: '#1e1e1e', layout: '#19191a', card: '#2a2a2c', elevated: '#2c2c2e',
    text: 'rgba(255,255,255,0.88)', text2: 'rgba(255,255,255,0.55)', text3: 'rgba(255,255,255,0.38)',
    border: 'rgba(255,255,255,0.16)', border2: 'rgba(255,255,255,0.09)', fill: 'rgba(255,255,255,0.07)',
    success: '#30d158', warning: '#ff9f0a', error: '#ff453a',
  },
};

function themeFor(resolved: 'light' | 'dark'): ThemeConfig {
  const c = palette[resolved];
  return {
    algorithm: resolved === 'dark' ? theme.darkAlgorithm : theme.defaultAlgorithm,
    token: {
      colorPrimary: c.accent, colorInfo: c.accent, colorLink: c.accent,
      colorSuccess: c.success, colorWarning: c.warning, colorError: c.error,
      colorText: c.text, colorTextSecondary: c.text2, colorTextTertiary: c.text3, colorTextQuaternary: c.text3,
      colorBgBase: c.bg, colorBgLayout: c.layout, colorBgContainer: c.card, colorBgElevated: c.elevated,
      colorBorder: c.border, colorBorderSecondary: c.border2, colorSplit: c.border2,
      colorFillSecondary: c.fill, colorFillTertiary: c.fill, colorFillQuaternary: c.fill,
      fontFamily: '-apple-system, BlinkMacSystemFont, "SF Pro Text", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Segoe UI", sans-serif',
      fontSize: 13, fontSizeSM: 12, fontSizeLG: 14, fontSizeHeading4: 15, fontSizeHeading5: 13,
      lineHeight: 1.5, borderRadius: 6, borderRadiusSM: 5, borderRadiusLG: 10,
      controlHeight: 28, controlHeightSM: 22, controlHeightLG: 32,
      boxShadow: resolved === 'dark' ? '0 0 0 0.5px rgba(255,255,255,0.12), 0 10px 32px rgba(0,0,0,0.5)' : '0 0 0 0.5px rgba(0,0,0,0.12), 0 8px 28px rgba(0,0,0,0.16)',
      boxShadowSecondary: resolved === 'dark' ? '0 0 0 0.5px rgba(255,255,255,0.12), 0 10px 32px rgba(0,0,0,0.5)' : '0 0 0 0.5px rgba(0,0,0,0.12), 0 8px 28px rgba(0,0,0,0.16)',
      motionDurationMid: '0.15s', motionDurationSlow: '0.2s', wireframe: false,
    },
    components: {
      Button: { primaryShadow: 'none', defaultShadow: 'none', dangerShadow: 'none', fontWeight: 500, paddingInline: 12, contentFontSizeLG: 14 },
      Input: { activeShadow: `0 0 0 3px ${resolved === 'dark' ? 'rgba(10,132,255,0.35)' : 'rgba(0,122,255,0.22)'}`, paddingInline: 9 },
      InputNumber: { activeShadow: `0 0 0 3px ${resolved === 'dark' ? 'rgba(10,132,255,0.35)' : 'rgba(0,122,255,0.22)'}` },
      Select: { activeOutlineColor: resolved === 'dark' ? 'rgba(10,132,255,0.35)' : 'rgba(0,122,255,0.22)', optionSelectedBg: c.accent, optionSelectedColor: '#ffffff', optionActiveBg: c.fill },
      Segmented: { trackBg: resolved === 'dark' ? 'rgba(255,255,255,0.08)' : 'rgba(0,0,0,0.06)', itemSelectedBg: resolved === 'dark' ? '#5a5a5e' : '#ffffff', itemColor: c.text, itemHoverColor: c.text, itemHoverBg: 'transparent', trackPadding: 2, borderRadius: 7, borderRadiusSM: 6 },
      Table: { headerBg: 'transparent', headerColor: c.text2, headerSplitColor: 'transparent', rowHoverBg: c.fill, borderColor: c.border2, cellPaddingBlockSM: 7, cellPaddingInlineSM: 12, headerBorderRadius: 0 },
      Tabs: { horizontalMargin: '0 0 12px 0', itemColor: c.text2 },
      Drawer: { footerPaddingBlock: 12 },
      Modal: { borderRadiusLG: 12, titleFontSize: 15 },
      Tag: { defaultBg: c.fill, defaultColor: c.text2 },
      Switch: { colorPrimary: c.success, colorPrimaryHover: c.success },
      Card: { headerFontSize: 13 },
      Tooltip: { colorBgSpotlight: resolved === 'dark' ? '#3a3a3c' : 'rgba(40,40,42,0.94)' },
      Collapse: { headerPadding: '10px 14px', contentPadding: '4px 14px 12px' },
      Form: { itemMarginBottom: 0, verticalLabelPadding: '0 0 5px', labelColor: c.text, labelFontSize: 13 },
    },
  };
}

export function AppearanceProvider({ children, initialAppearance }: { children: ReactNode; initialAppearance?: AppearanceState }) {
  const [state, setState] = useState<AppearanceState>(() => initialAppearance || readAppearance());
  const [saveError, setSaveError] = useState('');
  const resolved = resolveAppearance(state);
  useEffect(() => {
    const query = window.matchMedia('(prefers-color-scheme: dark)');
    const update = () => setState(current => ({ ...current, systemDark: query.matches }));
    update();
    query.addEventListener('change', update);
    return () => query.removeEventListener('change', update);
  }, []);
  useLayoutEffect(() => {
    applyDocumentAppearance(resolved);
    // This bounded IPC only updates app appearance, never accounts or model connections.
    if (window.workbench) void api('appearance.set', { source: state.preference }).catch(() => {});
  }, [state.preference, resolved]);
  const value: AppearanceValue = {
    preference: state.preference, resolved, saveError,
    setPreference(preference) {
      setState(current => ({ ...current, preference }));
      try { window.localStorage.setItem(appearanceStorageKey, preference); setSaveError(''); }
      catch { setSaveError('外观偏好暂时无法保存，本次切换仍有效。'); }
    },
  };
  const tokens = useMemo(() => themeFor(resolved), [resolved]);
  return <AppearanceContext.Provider value={value}>
    <ConfigProvider locale={zhCN} theme={tokens} form={{ requiredMark: false, colon: false }}>
      <AntApp>{children}</AntApp>
    </ConfigProvider>
  </AppearanceContext.Provider>;
}

export function AppearanceControl() {
  const appearance = useContext(AppearanceContext);
  if (!appearance) return null;
  return <div className="appearance-control">
    <div className="appearance-label">外观</div>
    <Segmented<AppearancePreference> aria-label="外观模式" block value={appearance.preference} onChange={appearance.setPreference}
      options={[{ value: 'system', label: '跟随系统' }, { value: 'light', label: '浅色' }, { value: 'dark', label: '深色' }]} />
    {appearance.preference === 'system' && <small>当前为{appearance.resolved === 'dark' ? '深色' : '浅色'}</small>}
    {appearance.saveError && <small role="status">{appearance.saveError}</small>}
  </div>;
}
