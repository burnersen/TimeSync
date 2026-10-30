// settings_check.js: the settings form reads and writes what the Go side
// expects, and the presets and lists are valid.
const { loadGui, createChecker } = require("./check_harness");
const { gui, element } = loadGui();
const { check, finish } = createChecker();
gui.lang = "de";

console.log("\n=== server list ===");
check("lines, commas, duplicates, case", gui.parseServerList(" PTBtime1.ptb.de\nptbtime1.ptb.de, 0.pool.ntp.org;\n\n"), ["ptbtime1.ptb.de", "0.pool.ntp.org"]);
check("empty", gui.parseServerList("  \n "), []);

console.log("\n=== presets ===");
for (const [name, list] of Object.entries(gui.PRESETS)) {
  check(`${name} has 4 servers`, list.length, 4);
}

console.log("\n=== form round trip ===");
gui.fillSelects();
const cfg = {
  servers: ["nts://ptbtime1.ptb.de", "0.pool.ntp.org"], intervalMinutes: 30, syncOnStart: true, syncOnResume: false,
  syncOnNetworkChange: true, minCorrectionMs: 2, slewMaxMs: 500, bigJumpMinutes: 15, httpsFallback: true,
  httpsFallbackUrls: ["https://www.ptb.de/"], w32timeMode: "leave", allowOnDomain: false, eventLog: true,
};
// The stand-in select keeps whatever value is set, like a real one with
// that option present.
gui.fillForm(cfg);
const back = gui.readForm(cfg);
check("everything survives", back, cfg);
check("odd interval snaps to a choice", (gui.fillForm(Object.assign({}, cfg, { intervalMinutes: 50 })), element("set-interval").value), "60");

console.log("\n=== labels ===");
check("interval 15", gui.intervalLabel(15), "15 Minuten");
check("interval 60", gui.intervalLabel(60), "1 Stunde");
check("interval 240", gui.intervalLabel(240), "4 Stunden");
check("interval 1440", gui.intervalLabel(1440), "1 Tag");
finish();
