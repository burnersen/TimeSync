// TimeSync (https://github.com/burnersen/TimeSync)
// SPDX-License-Identifier: MIT

// check_harness.js runs the window's own script without a browser.
//
// It loads the shipped dist/index.html, not a copy: a copy would drift and
// then prove nothing. Elements are stand-ins that remember what was written
// to them, so a check can read back what the window would show.
const fs = require("fs");
const path = require("path");
const vm = require("vm");

function fakeElement(id) {
  const node = {
    id, textContent: "", value: "", checked: false, hidden: false, disabled: false,
    className: "", title: "", style: {}, dataset: {}, children: [], attrs: {},
    get firstChild() { return node.children[0] || null; },
    appendChild(c) { node.children.push(c); return c; },
    removeChild(c) { node.children.splice(node.children.indexOf(c), 1); return c; },
    setAttribute(k, v) { node.attrs[k] = String(v); },
    getAttribute(k) { return node.attrs[k]; },
    addEventListener() {},
    onclick: null,
  };
  return node;
}

function loadGui(options = {}) {
  const html = fs.readFileSync(path.join(__dirname, "dist", "index.html"), "utf8");
  const scripts = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map((m) => m[1]);
  if (scripts.length !== 1) throw new Error(`expected exactly one inline script, found ${scripts.length}`);
  const elements = {};
  const element = (id) => (elements[id] = elements[id] || fakeElement(id));
  const htmlNode = fakeElement("html");
  const document = {
    documentElement: htmlNode,
    getElementById: element,
    querySelectorAll: () => [],
    createElement: (tag) => fakeElement(tag),
  };
  const calls = { prefs: [] };
  const context = {
    console, Date, Math, JSON, String, Number, Object, Array, parseInt, setTimeout() {}, setInterval() {},
    document,
    window: { matchMedia: () => ({ matches: !!options.systemLight, addEventListener() {} }), confirm: () => true },
  };
  vm.createContext(context);
  vm.runInContext(scripts[0], context, { filename: "index.html" });
  return { gui: context, html, element, calls };
}

function createChecker() {
  let failed = 0, passed = 0;
  function check(name, got, want) {
    const ok = JSON.stringify(got) === JSON.stringify(want);
    if (ok) passed++; else failed++;
    console.log(`${ok ? "  ok  " : "  FAIL"} ${name}${ok ? "" : `\n         got:  ${JSON.stringify(got)}\n         want: ${JSON.stringify(want)}`}`);
  }
  function finish() {
    console.log(`\n${passed} passed, ${failed} failed`);
    if (failed > 0 || passed === 0) process.exit(1);
  }
  return { check, finish };
}

module.exports = { loadGui, createChecker };
