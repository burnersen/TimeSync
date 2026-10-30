// theme_check.js: the day/night switch, and a palette that answers every
// colour name (a forgotten one means unreadable text on a white panel).
const { loadGui, createChecker } = require("./check_harness");
const { check, finish } = createChecker();

function paletteOf(html, selector) {
  const start = html.indexOf(selector + " {");
  const block = html.slice(start, html.indexOf("}", start));
  return [...block.matchAll(/^\s*(--[a-z0-9-]+)\s*:/gim)].map((m) => m[1]).sort();
}

let { gui, html, element } = loadGui();
console.log("\n=== palettes ===");
const night = paletteOf(html, ":root");
const day = paletteOf(html, 'html[data-theme="light"]');
check("day answers every night colour", night.filter((n) => !day.includes(n) && n !== "--radius"), []);

console.log("\n=== choices ===");
gui.applyTheme("light");
check("light on html", gui.document.documentElement.dataset.theme, "light");
check("light button pressed", element("theme-light").getAttribute("aria-pressed"), "true");
gui.applyTheme("neon");
check("unknown falls back to system (dark here)", [gui.themeChoice, gui.document.documentElement.dataset.theme], ["system", "dark"]);
({ gui } = loadGui({ systemLight: true }));
gui.applyTheme("system");
check("system follows Windows (light)", gui.document.documentElement.dataset.theme, "light");

console.log("\n=== start colour matches the page ===");
const mainGo = require("fs").readFileSync(require("path").join(__dirname, "..", "main.go"), "utf8");
const bg = html.match(/:root \{[^}]*--bg: #([0-9a-f]{6})/i)[1];
const rgb = [0, 2, 4].map((i) => parseInt(bg.slice(i, i + 2), 16));
check("dark start colour = --bg", mainGo.includes(`R: ${rgb[0]}, G: ${rgb[1]}, B: ${rgb[2]}`), true);
const bgLight = html.match(/html\[data-theme="light"\] \{[^}]*--bg: #([0-9a-f]{6})/i)[1];
const rgbL = [0, 2, 4].map((i) => parseInt(bgLight.slice(i, i + 2), 16));
check("light start colour = --bg", mainGo.includes(`R: ${rgbL[0]}, G: ${rgbL[1]}, B: ${rgbL[2]}`), true);
finish();
