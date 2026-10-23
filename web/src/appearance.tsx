import { createContext, useContext, useEffect, useLayoutEffect, useMemo, useState, type ReactNode } from 'react';
import { App as AntApp, ConfigProvider, Select, theme } from 'antd';
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
  const tokens = useMemo(() => ({
    algorithm: resolved === 'dark' ? theme.darkAlgorithm : theme.defaultAlgorithm,
    token: {
      colorPrimary: resolved === 'dark' ? '#8db5f0' : '#365d98',
      colorLink: resolved === 'dark' ? '#8db5f0' : '#365d98',
      colorLinkHover: resolved === 'dark' ? '#bdd5ff' : '#527bb6',
      colorLinkActive: resolved === 'dark' ? '#6e9bd8' : '#284e86',
      colorSuccess: resolved === 'dark' ? '#72c8b7' : '#268276',
      colorText: resolved === 'dark' ? '#e3ebf6' : '#23334a',
      colorTextSecondary: resolved === 'dark' ? '#abbbd1' : '#53657d',
      colorTextTertiary: resolved === 'dark' ? '#a1b1c8' : '#5d6e84',
      colorBgBase: resolved === 'dark' ? '#0e1622' : '#ffffff',
      colorBgLayout: resolved === 'dark' ? '#0e1622' : '#f3f6fa',
      colorBgContainer: resolved === 'dark' ? '#182435' : '#ffffff',
      colorBgElevated: resolved === 'dark' ? '#202e42' : '#ffffff',
      colorBorder: resolved === 'dark' ? '#3b4d65' : '#d9e2ee',
      colorBorderSecondary: resolved === 'dark' ? '#2f4057' : '#e2e9f2',
      fontFamily: '"PingFang SC", "Microsoft YaHei", system-ui, sans-serif',
      borderRadius: 7, fontSize: 14,
    },
    components: { Button: { controlHeight: 36, primaryColor: resolved === 'dark' ? '#0e1622' : '#ffffff' }, Input: { controlHeight: 38 }, Select: { controlHeight: 38 } },
  }), [resolved]);
  return <AppearanceContext.Provider value={value}><ConfigProvider locale={zhCN} theme={tokens}><AntApp>{children}</AntApp></ConfigProvider></AppearanceContext.Provider>;
}

export function AppearanceControl() {
  const appearance = useContext(AppearanceContext);
  if (!appearance) return null;
  return <div className="appearance-control">
    <label htmlFor="appearance-preference">外观</label>
    <Select id="appearance-preference" aria-label="外观模式" virtual={false} size="small" value={appearance.preference}
      onChange={appearance.setPreference} options={[{ value: 'system', label: '跟随系统' }, { value: 'light', label: '浅色' }, { value: 'dark', label: '深色' }]} />
    {appearance.preference === 'system' && <small>当前为{appearance.resolved === 'dark' ? '深色' : '浅色'}</small>}
    {appearance.saveError && <small role="status">{appearance.saveError}</small>}
  </div>;
}
