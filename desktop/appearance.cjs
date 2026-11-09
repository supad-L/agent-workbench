// macOS keeps a transparent window so the native sidebar material shows through;
// the page paints its own opaque content background.
const backgrounds = { light: '#ffffff', dark: '#1e1e1e' };
const transparent = '#00000000';

// Changes only this application's native chrome, dialogs and window background.
function applyAppearance(source, { nativeTheme, window, platform = process.platform }) {
  if (!['system', 'light', 'dark'].includes(source)) throw new Error('外观模式无效');
  nativeTheme.themeSource = source;
  const resolved = source === 'system' ? nativeTheme.shouldUseDarkColors ? 'dark' : 'light' : source;
  window.setBackgroundColor(platform === 'darwin' ? transparent : backgrounds[resolved]);
  return { source, resolved };
}
module.exports = { applyAppearance };
