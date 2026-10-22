import fs from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { Resvg } from '@resvg/resvg-js';
const svg = await fs.readFile(new URL('../assets/logo.svg', import.meta.url));
const dir = new URL('../assets/app.iconset/', import.meta.url); await fs.mkdir(dir, {recursive:true});
for (const size of [16, 32, 128, 256, 512]) {
  for (const scale of [1,2]) {
    const png = new Resvg(svg, {fitTo:{mode:'width', value:size*scale}}).render().asPng();
    await fs.writeFile(new URL(`icon_${size}x${size}${scale===2?'@2x':''}.png`, dir), png);
    if (size===512 && scale===2) await fs.writeFile(new URL('../assets/app.png', import.meta.url), png);
  }
}
if (process.platform==='darwin') execFileSync('/usr/bin/iconutil', ['-c','icns',new URL(dir).pathname,'-o',new URL('../assets/app.icns',import.meta.url).pathname]);
console.log('应用图标已生成');
