const backgrounds = { light: '#f3f6fa', dark: '#0e1622' };

// Changes only this application's native chrome, dialogs and window background.
function applyAppearance(source, { nativeTheme, window }) {
  if (!['system', 'light', 'dark'].includes(source)) throw new Error('外观模式无效');
  nativeTheme.themeSource = source;
  const resolved = source === 'system' ? nativeTheme.shouldUseDarkColors ? 'dark' : 'light' : source;
  window.setBackgroundColor(backgrounds[resolved]);
  return { source, resolved };
}
module.exports = { applyAppearance };
