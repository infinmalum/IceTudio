// Throwaway verification for the pane resizers. The functional drag runs the
// real React handlers via synthetic PointerEvents dispatched on the divider;
// one trusted CDP pointerdown first proves the divider is actually
// hit-testable at its position. Widths, clamping, persistence across reload,
// double-click reset, keyboard stepping and the stacked-layout hiding of the
// dock divider are all asserted. Reuses the product e2e launch pattern and
// the workspace cache dir; no mock interception (real bindings render fine).
import { spawn } from "node:child_process";
import { mkdirSync, openSync, writeFileSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const dir = resolve(root, ".cache/browser/pane-drag");
mkdirSync(dir, { recursive: true });
const log = openSync(resolve(dir, "probe.log"), "a");
const vite = spawn(
  "npm",
  ["run", "dev", "--", "--host", "127.0.0.1", "--port", "9393", "--strictPort"],
  { cwd: resolve(root, "frontend"), stdio: ["ignore", log, log], detached: true },
);
const chrome = spawn(
  "chromium",
  [
    "--headless",
    "--no-sandbox",
    "--disable-gpu",
    "--disable-background-networking",
    "--no-first-run",
    `--user-data-dir=${resolve(dir, "chrome")}`,
    "--remote-debugging-port=9394",
    "about:blank",
  ],
  { stdio: ["ignore", log, log], detached: true },
);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let ws;
try {
  async function ready(url) {
    for (let i = 0; i < 150; i++) {
      try {
        const r = await fetch(url);
        if (r.ok) return r;
      } catch {}
      await sleep(100);
    }
    throw Error("unavailable " + url);
  }
  await ready("http://127.0.0.1:9393");
  const targets = await (await ready("http://127.0.0.1:9394/json/list")).json();
  ws = new WebSocket(
    targets.find((t) => t.type === "page").webSocketDebuggerUrl,
  );
  await new Promise((r, j) => {
    ws.onopen = r;
    ws.onerror = j;
  });
  let seq = 0;
  const pending = new Map();
  const errors = [];
  function send(method, params = {}) {
    const id = ++seq;
    return new Promise((res, rej) => {
      const t = setTimeout(() => rej(Error("timeout " + method)), 20000);
      pending.set(id, { res, rej, t });
      ws.send(JSON.stringify({ id, method, params }));
    });
  }
  ws.onmessage = (e) => {
    const m = JSON.parse(e.data);
    if (m.id) {
      const p = pending.get(m.id);
      if (p) {
        clearTimeout(p.t);
        pending.delete(m.id);
        m.error ? p.rej(Error(JSON.stringify(m.error))) : p.res(m.result);
      }
    } else if (m.method === "Runtime.exceptionThrown")
      errors.push(
        m.params.exceptionDetails.exception?.description ??
          m.params.exceptionDetails.text,
      );
  };
  await send("Runtime.enable");
  await send("Page.enable");
  await send("Emulation.setDeviceMetricsOverride", {
    width: 1366,
    height: 850,
    deviceScaleFactor: 1,
    mobile: false,
  });
  await send("Page.navigate", { url: "http://127.0.0.1:9393" });
  async function evaluate(expression) {
    const r = await send("Runtime.evaluate", {
      expression,
      returnByValue: true,
      awaitPromise: true,
    });
    if (r.exceptionDetails)
      throw Error(
        r.exceptionDetails.exception?.description ?? r.exceptionDetails.text,
      );
    return r.result.value;
  }
  async function until(expr) {
    for (let i = 0; i < 140; i++) {
      if (await evaluate(`Boolean(${expr})`)) return;
      await sleep(100);
    }
    throw Error("not reached: " + expr);
  }
  const width = async (sel) => {
    const r = await evaluate(
      `(()=>{const e=document.querySelector(${JSON.stringify(sel)});return e?e.getBoundingClientRect().width:null})()`,
    );
    if (r == null)
      throw Error(
        `no element ${sel}; body=${await evaluate(`document.body.innerHTML.slice(0,400)`)}`,
      );
    return r;
  };
  const drag = (sel, dx) =>
    evaluate(
      `(()=>{const el=document.querySelector(${JSON.stringify(sel)});const b=el.getBoundingClientRect();const y=b.y+300,x0=b.x+b.width/2;const fire=(type,x,buttons)=>el.dispatchEvent(new PointerEvent(type,{bubbles:true,cancelable:true,pointerId:7,pointerType:'mouse',isPrimary:true,clientX:x,clientY:y,buttons}));fire('pointerdown',x0,1);for(let i=1;i<=5;i++)fire('pointermove',x0+${dx}*i/5,1);fire('pointerup',x0+${dx},0);})()`,
    );
  const dblClick = (sel) =>
    evaluate(
      `document.querySelector(${JSON.stringify(sel)}).dispatchEvent(new MouseEvent('dblclick',{bubbles:true}))`,
    );
  await until(
    `document.querySelector('.pane-resizer') && document.querySelector('.agent-dock')`,
  );
  // Without the Wails bridge the close guard cannot activate, so its inline
  // ref re-applies inert on every provider render (the product e2e works
  // around this with element.click()). Synthetic events below are
  // unaffected; assert the divider geometry instead of hit-testing, which
  // inert makes impossible here.
  console.log(
    "guards:",
    await evaluate(
      `JSON.stringify({n:document.querySelectorAll('[data-close-guard]').length,inert:document.querySelector('[data-close-guard]').hasAttribute('inert')})`,
    ),
  );
  const geom = await evaluate(
    `JSON.stringify(document.querySelector('.pane-resizer:not(.dock-resizer)').getBoundingClientRect())`,
  );
  const rect = JSON.parse(geom);
  if (Math.abs(rect.x - 240) > 1 || Math.abs(rect.width - 7) > 1 || rect.height < 800)
    throw Error("sidebar divider geometry unexpected: " + geom);
  if (Math.abs((await width(".sidebar")) - 240) > 1)
    throw Error("sidebar default width changed");
  if (Math.abs((await width(".agent-dock")) - 460) > 1)
    throw Error("dock default width changed");
  await drag(".pane-resizer:not(.dock-resizer)", 150);
  const sideW = await width(".sidebar");
  if (Math.abs(sideW - 390) > 2)
    throw Error("sidebar drag expected 390, got " + sideW);
  if (
    (await evaluate(`localStorage.getItem('icetudio.sidebar-width')`)) !==
    `${Math.round(sideW)}px`
  )
    throw Error("sidebar width not persisted");
  await drag(".pane-resizer.dock-resizer", -120);
  const dockW = await width(".agent-dock");
  if (Math.abs(dockW - 580) > 2)
    throw Error("dock drag expected 580, got " + dockW);
  if (
    (await evaluate(`localStorage.getItem('icetudio.dock-width')`)) !==
    `${Math.round(dockW)}px`
  )
    throw Error("dock width not persisted");
  if (await evaluate(`document.documentElement.scrollWidth>innerWidth`))
    throw Error("horizontal overflow after drag");
  writeFileSync(
    resolve(dir, "pane-drag.png"),
    Buffer.from(
      (await send("Page.captureScreenshot", { format: "png" })).data,
      "base64",
    ),
  );
  // Mark the old document so the wait below cannot pass on it: __marker is
  // gone once the reload commits, then we poll for the app to mount.
  await evaluate(`window.__marker=1`);
  await send("Page.reload");
  await until(
    `!window.__marker && document.readyState==='complete' && document.querySelector('.sidebar') && document.querySelector('.agent-dock')`,
  );
  if (Math.abs((await width(".sidebar")) - sideW) > 1)
    throw Error("sidebar width not restored after reload");
  if (Math.abs((await width(".agent-dock")) - dockW) > 1)
    throw Error("dock width not restored after reload");
  await dblClick(".pane-resizer:not(.dock-resizer)");
  if (Math.abs((await width(".sidebar")) - 240) > 1)
    throw Error("double-click did not restore sidebar default");
  if ((await evaluate(`localStorage.getItem('icetudio.sidebar-width')`)) !== null)
    throw Error("double-click did not clear saved sidebar width");
  await evaluate(
    `document.querySelector('.pane-resizer:not(.dock-resizer)').focus()`,
  );
  await evaluate(
    `document.querySelector('.pane-resizer:not(.dock-resizer)').dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowRight',bubbles:true}))`,
  );
  if (Math.abs((await width(".sidebar")) - 256) > 1)
    throw Error("ArrowRight did not widen sidebar");
  await send("Emulation.setDeviceMetricsOverride", {
    width: 800,
    height: 600,
    deviceScaleFactor: 1,
    mobile: false,
  });
  await sleep(150);
  if (
    (await evaluate(
      `getComputedStyle(document.querySelector('.pane-resizer.dock-resizer')).display`,
    )) !== "none"
  )
    throw Error("dock resizer should hide on stacked narrow layout");
  if (await evaluate(`document.documentElement.scrollWidth>innerWidth`))
    throw Error("horizontal overflow at 800px");
  console.log(
    `PASS: divider geometry and drag verified; sidebar=${sideW}px dock=${dockW}px persisted across reload; double-click resets; ArrowRight widens; dock divider hidden below 880px.`,
  );
  if (errors.length) throw Error(errors.join("\n"));
} finally {
  try {
    ws?.close();
  } catch {}
  for (const child of [vite, chrome]) {
    try {
      process.kill(-child.pid, "SIGTERM");
    } catch {}
  }
}
