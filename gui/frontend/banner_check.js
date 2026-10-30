// banner_check.js: the right notes appear above the clocks, and only then.
const { loadGui, createChecker } = require("./check_harness");
const { gui } = loadGui();
const { check, finish } = createChecker();
gui.lang = "en";

const base = () => ({
  info: { cliFound: true, domain: false },
  check: { result: { verdict: { offset: 0, hints: [] }, samples: [] }, timeZone: { autoDstDisabled: false } },
  service: { installed: true, running: true, legacy: false, status: { monitorOnly: false, stats: {} } },
  hist: { stats: { consecutiveFailures: 0 } },
  settings: { config: { allowOnDomain: false } },
});
const titles = (s) => gui.computeBanners(s).map((b) => b.title);

console.log("\n=== a healthy machine shows nothing ===");
check("no banners", titles(base()), []);

console.log("\n=== each condition shows its banner ===");
let s = base();
s.check.result.verdict = { offset: 3600000 * 1e6, hints: ["dual-boot"] };
check("dual boot", titles(s), ["Exactly +1 h 00 min difference"]);
s = base(); s.check.result.verdict.hints = ["time-zone"];
check("time zone", titles(s), ["Check the time zone"]);
s = base(); s.check.timeZone.autoDstDisabled = true;
check("automatic DST off", titles(s), ["Automatic daylight saving time is off"]);
s = base(); s.check.result.verdict.hints = ["ntp-blocked"];
check("NTP blocked", titles(s), ["The time port is blocked"]);
s = base(); s.hist.stats = { cmosSuspect: true, lastBootOffsetMs: -180000, consecutiveFailures: 0 };
check("CMOS", titles(s), ["After switching on, the clock was -3 min 00 s off"]);
s = base(); s.service.status.monitorOnly = true;
check("company PC", titles(s), ["Company PC"]);
s = base(); s.info.domain = true;
check("company PC, not allowed in settings", titles(s), ["Company PC"]);
s.settings.config.allowOnDomain = true;
check("company PC, allowed: no banner", titles(s), []);
s = base(); s.service = { installed: false, running: false, legacy: true };
check("old 1.x service", titles(s), ["Old service found"]);
s = base(); s.service = { installed: false, running: false, legacy: false };
check("no service yet", titles(s), ["Tip: set up the background sync"]);
s = base(); s.hist.stats.consecutiveFailures = 4;
check("failing", titles(s), ["No synchronization for 4 attempts"]);
s = base(); s.info.cliFound = false;
check("ntp-sync.exe missing", titles(s).includes("ntp-sync.exe is missing"), true);
s = base(); s.service.status.configError = "broken";
check("damaged settings", titles(s), ["The settings are damaged"]);

console.log("\n=== banner buttons point at real actions ===");
s = base();
s.check.result.verdict.hints = ["dual-boot", "time-zone", "single-server"];
s.service = { installed: false, legacy: true };
s.info.domain = true;
const runs = gui.computeBanners(s).flatMap((b) => b.actions.map((a) => a.run));
check("every action exists", runs.filter((r) => typeof gui.ACTIONS[r] !== "function"), []);
check("some actions checked", runs.length >= 4, true);
finish();
