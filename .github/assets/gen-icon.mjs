/**
 * Renders the icons from the masters, each centred on a transparent square:
 *
 *   .github/assets/icon.png                     512, tollroll-dunkel.svg, the Community Applications icon
 *   plugin/.../images/tollroll.png, icons/...   512, tollroll-unraid.svg, the Plugins list, menu and .plg icon
 *   plugin/.../images/tollroll-unraid.svg       the settings page header
 *
 * Deps (global): @resvg/resvg-js.
 * Run: node .github/assets/gen-icon.mjs
 */
import { copyFileSync, readFileSync, writeFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";
import { execSync } from "node:child_process";

const require = createRequire(import.meta.url);
const { Resvg } = require(`${execSync("npm root -g").toString().trim()}/@resvg/resvg-js`);
const __dir = dirname(fileURLToPath(import.meta.url));
const plugin = join(__dir, "..", "..", "plugin", "src", "tollroll", "usr", "local", "emhttp", "plugins", "tollroll");

// The logo is taller than wide, and Unraid draws icons into square boxes, so it sits
// in a square of its longer side instead of being stretched.
function square(file, size) {
  const src = readFileSync(join(__dir, file), "utf8").replace(/<\?xml[^>]*\?>\s*/, "");
  const [, , w, h] = src.match(/viewBox="([^"]+)"/)[1].trim().split(/[\s,]+/).map(Number);
  const side = Math.max(w, h);
  const inner = src.replace(/<svg\b([^>]*)>/, (_, attrs) =>
    `<svg${attrs.replace(/\s(width|height|x|y)="[^"]*"/g, "")} x="${(side - w) / 2}" y="${(side - h) / 2}" width="${w}" height="${h}">`);
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${side} ${side}" width="${size}" height="${size}">${inner}</svg>`;
  return new Resvg(svg, { fitTo: { mode: "width", value: size }, background: "rgba(0,0,0,0)" }).render().asPng();
}

function emit(path, data) {
  writeFileSync(path, data);
  console.log(`wrote ${path}`);
}

emit(join(__dir, "icon.png"), square("tollroll-dunkel.svg", 512));
const tile = square("tollroll-unraid.svg", 512);
emit(join(plugin, "images", "tollroll.png"), tile);
emit(join(plugin, "icons", "tollroll.png"), tile);
copyFileSync(join(__dir, "tollroll-unraid.svg"), join(plugin, "images", "tollroll-unraid.svg"));
console.log(`wrote ${join(plugin, "images", "tollroll-unraid.svg")}`);
