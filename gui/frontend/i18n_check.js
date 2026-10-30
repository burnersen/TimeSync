// i18n_check.js: every text exists in both languages, and every key the
// window uses is defined. A missing key would show up as its raw name.
const { loadGui, createChecker } = require("./check_harness");
const { gui, html } = loadGui();
const { check, finish } = createChecker();

const de = Object.keys(gui.I18N.de).sort();
const en = Object.keys(gui.I18N.en).sort();
console.log("\n=== both languages have the same keys ===");
check("keys only in German ", de.filter((k) => !en.includes(k)), []);
check("keys only in English", en.filter((k) => !de.includes(k)), []);

console.log("\n=== no empty texts ===");
for (const l of ["de", "en"]) {
  check(`empty texts in ${l}`, Object.entries(gui.I18N[l]).filter(([, v]) => !String(v).trim()).map(([k]) => k), []);
}

console.log("\n=== every key used exists ===");
const used = new Set();
for (const m of html.matchAll(/data-i18n(?:-title)?="([^"]+)"/g)) used.add(m[1]);
for (const m of html.matchAll(/\bt\("([A-Za-z0-9]+)"/g)) used.add(m[1]);
check("used but undefined", [...used].filter((k) => !(k in gui.I18N.en)).sort(), []);
check("found a reasonable number of uses", used.size > 100, true);

console.log("\n=== placeholders match between languages ===");
const holes = (s) => (String(s).match(/\{[a-z]+\}/gi) || []).sort();
check("placeholder mismatches", de.filter((k) => JSON.stringify(holes(gui.I18N.de[k])) !== JSON.stringify(holes(gui.I18N.en[k]))), []);

console.log("\n=== no dashes as punctuation in German texts ===");
check("German texts with a long dash", Object.entries(gui.I18N.de).filter(([, v]) => /[–—]/.test(v)).map(([k]) => k), []);
finish();
