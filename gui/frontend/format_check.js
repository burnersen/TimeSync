// format_check.js: offsets, verdicts and results read the way people talk.
const { loadGui, createChecker } = require("./check_harness");
const { gui } = loadGui();
const { check, finish } = createChecker();
const NS = 1e6; // nanoseconds per millisecond, as Go sends durations

console.log("\n=== offsets ===");
gui.lang = "de";
check("12 ms                ", gui.formatOffset(12.4), "+12 ms");
check("-3.4 s in German     ", gui.formatOffset(-3400), "-3,4 s");
check("2 h 5 min            ", gui.formatOffset((2 * 60 + 5) * 60000), "+2 Std. 05 Min.");
check("4 min 7 s            ", gui.formatOffset(-(4 * 60 + 7) * 1000), "-4 Min. 07 s");
check("400 days             ", gui.formatOffset(400 * 86400000), "+400,0 Tage");
gui.lang = "en";
check("-3.4 s in English    ", gui.formatOffset(-3400), "-3.4 s");

console.log("\n=== severity ===");
check("100 ms is fine       ", gui.severity(100), "ok");
check("-2 s is slightly off ", gui.severity(-2000), "warn");
check("1 h is wrong         ", gui.severity(3600000), "bad");

const verdict = (v, samples) => ({ verdict: Object.assign({ hints: [] }, v), samples: samples || [] });

console.log("\n=== verdicts ===");
gui.lang = "de";
let d = gui.describeVerdict(verdict({ action: "none", reason: "in-sync", offset: 3 * NS, agreeing: 4, asked: 4 }), Date.parse("2026-10-30T18:00:00"));
check("accurate             ", d.cls, "ok");
check("  title              ", d.title, "Deine Uhr geht genau (Abweichung +3 ms)");
check("  sub                ", d.sub, "geprüft um 18:00:00 · 4 von 4 Servern einig");
d = gui.describeVerdict(verdict({ action: "refuse", reason: "no-valid-answers" }, [{ error: "x", errorKind: "timeout" }, { error: "y", errorKind: "timeout" }]), 0);
check("no answers: blocked port", [d.cls, d.sub.startsWith("Im Firmennetz")], ["bad", true]);
d = gui.describeVerdict(verdict({ action: "refuse", reason: "no-valid-answers" }, [{ error: "x", errorKind: "dns" }]), 0);
check("no answers: DNS      ", d.sub.includes("Internetverbindung"), true);
d = gui.describeVerdict(verdict({ action: "refuse", reason: "big-jump-needs-agreement", offset: 2 * 3600000 * NS }), 0);
check("big jump refused     ", [d.cls, d.sub.includes("+2 Std. 00 Min.")], ["bad", true]);
d = gui.describeVerdict(verdict({ action: "step", reason: "step", offset: 40000 * NS, agreeing: 3, asked: 3, lowPrecision: true }), 0);
check("web time mentioned   ", [d.cls, d.sub.includes("Webzeit")], ["bad", true]);
check("nothing measured yet ", gui.describeVerdict(null, 0).title, "Noch nicht geprüft");

console.log("\n=== results of a sync ===");
const res = (applied, offMs, extra) => ({ ok: true, result: Object.assign({ applied, verdict: { offset: offMs * NS, hints: [] } }, extra || {}) });
check("step                 ", gui.describeAction(res("step", 1500)).text, "Uhr gestellt, sie lag +1,5 s daneben.");
check("slew                 ", gui.describeAction(res("slew", 120, { slew: { ratePpm: 500, duration: 240e9 } })).text, "Die Uhr wird sanft angeglichen (+120 ms in etwa 4 Min.).");
check("failed               ", gui.describeAction(res("failed", 5000, { error: "Zugriff verweigert" })).cls, "bad");
check("cancelled prompt     ", gui.describeAction({ cancelled: true }).cls, "warn");
check("error without result ", gui.describeAction({ ok: false, error: "kaputt" }).text, "Fehler: kaputt");

console.log("\n=== drift ===");
check("too few samples      ", gui.driftText({ driftSamples: 2, lagSecondsPerDay: 1 }), "noch zu wenige Messungen");
check("loses time           ", gui.driftText({ driftSamples: 5, lagSecondsPerDay: 1.44 }), "geht pro Tag etwa 1,44 s nach");
check("gains time           ", gui.driftText({ driftSamples: 5, lagSecondsPerDay: -0.5 }), "geht pro Tag etwa 0,50 s vor");

console.log("\n=== time zone line ===");
check("Berlin in summer     ", gui.tzText({ keyName: "W. Europe Standard Time", utcOffsetMinutes: 120, hasDst: true, inDst: true }), "W. Europe Standard Time, UTC+02:00, Sommerzeit");
check("India                ", gui.tzText({ keyName: "India Standard Time", utcOffsetMinutes: 330, hasDst: false }), "India Standard Time, UTC+05:30");
finish();
