;(() => {
  // The napplet host page: the launcher-owned main frame of a napplet's
  // window. The napplet itself runs below it in a sandboxed srcdoc iframe
  // (allow-scripts only, so an opaque origin, and the NIP-5D CSP inside), and
  // this page is the only thing it can talk to. Its whole job is to carry NAP
  // envelopes between that iframe and the Go host:
  //
  //   napplet --postMessage--> here --rpc("nap.msg")--> Go
  //   napplet <--postMessage-- here <--rpc result / __nap_push(...)-- Go
  //
  // It never interprets a NAP message itself. Every decision (what a type
  // means, whether it is allowed, who sent it) is made in Go, which knows the
  // napplet this window was opened for. Nothing here is reachable from the
  // napplet: a sandboxed frame without allow-same-origin cannot touch this
  // window's globals, only post messages to it.

  // init scripts may be injected into child frames too (WebView2 does that);
  // none of this belongs anywhere but the top frame
  if (window !== window.top) return

  // ── talking to the host ─────────────────────────────────────────
  // Desktop: __verdanaNappletRPC, a wrapper the child process defines in the
  // top frame that adds this window's secret token to every call. Android: the
  // __verdanaHost web message channel, which only this page's origin gets.
  const rpc = (() => {
    const decode = value => {
      const val = typeof value === "string" ? JSON.parse(value) : value
      if (val && val.__bridge_error) throw new Error(val.__bridge_error)
      return val
    }
    const encode = params => (params !== undefined ? JSON.stringify(params) : "null")

    if (typeof window.__verdanaNappletRPC === "function") {
      const bound = window.__verdanaNappletRPC
      return (method, params) => bound(method, encode(params)).then(decode)
    }

    const port = window.__verdanaHost
    if (!port) return () => Promise.reject(new Error("no napplet host to talk to"))

    const pending = new Map()
    let rpcSerial = 0
    port.onmessage = event => {
      let msg
      try {
        msg = JSON.parse(typeof event.data === "string" ? event.data : "")
      } catch {
        return
      }
      const waiter = msg && pending.get(msg.id)
      if (!waiter) return
      pending.delete(msg.id)
      if (msg.error) {
        waiter.reject(new Error(msg.error))
        return
      }
      try {
        waiter.resolve(msg.result === undefined ? null : decode(msg.result))
      } catch (err) {
        waiter.reject(err)
      }
    }
    return (method, params) =>
      new Promise((resolve, reject) => {
        const id = ++rpcSerial
        pending.set(id, { resolve, reject })
        port.postMessage(JSON.stringify({ t: "rpc", id, method, params: encode(params) }))
      })
  })()

  // the biggest envelope a napplet may send in one message; anything larger
  // is dropped like any other malformed message
  const MAX_ENVELOPE = 1024 * 1024

  let frame = null

  // ── Go -> napplet ───────────────────────────────────────────────
  // Envelopes come back as plain JSON. A resource result carries its bytes as
  // {__blob:{b64, mime}}; the napplet expects a real Blob, so it is rebuilt
  // here, the one place that can make one.
  const revive = value => {
    if (Array.isArray(value)) return value.map(revive)
    if (value && typeof value === "object") {
      const b = value.__blob
      if (b && typeof b.b64 === "string") {
        const bin = atob(b.b64)
        const bytes = new Uint8Array(bin.length)
        for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
        return new Blob([bytes], { type: typeof b.mime === "string" ? b.mime : "" })
      }
      const out = {}
      for (const k of Object.keys(value)) out[k] = revive(value[k])
      return out
    }
    return value
  }

  const deliver = envelopes => {
    if (!frame || !frame.contentWindow || envelopes == null) return
    const list = Array.isArray(envelopes) ? envelopes : [envelopes]
    for (const env of list) {
      if (!env || typeof env.type !== "string") continue
      // the napplet's origin is opaque, so "*" is the only target that reaches
      // it; the message is addressed by contentWindow, not by origin
      frame.contentWindow.postMessage(revive(env), "*")
    }
  }

  // unsolicited messages: relay events, inc events, theme/identity changes
  window.__nap_push = json => {
    try {
      deliver(typeof json === "string" ? JSON.parse(json) : json)
    } catch (err) {
      console.error("[napplet-host] bad push", err)
    }
  }

  // ── napplet -> Go ───────────────────────────────────────────────
  let outbound = Promise.resolve()
  window.addEventListener("message", event => {
    // sender binding: only this window's own napplet frame, never anyone else
    if (!frame || event.source !== frame.contentWindow) return
    const data = event.data
    if (!data || typeof data !== "object" || typeof data.type !== "string") return
    let json
    try {
      json = JSON.stringify(data)
    } catch {
      return
    }
    if (!json || json.length > MAX_ENVELOPE) return
    // one at a time: the desktop binding runs every call on its own thread,
    // so two calls in flight can reach Go in either order, and NAP needs the
    // napplet's order kept (shell.ready first, a subscribe before its close).
    // Go only queues the envelope before answering, so the wait is short.
    outbound = outbound
      .then(() => rpc("nap.msg", json))
      .then(deliver, err => console.error("[napplet-host]", err))
  })

  // ── the launcher's own hooks ────────────────────────────────────
  // the theme: the host page paints itself in it (the napplet gets
  // theme.changed from Go, through its own NAP domain)
  const applyTheme = (theme, vars) => {
    if (typeof vars === "string") {
      try {
        vars = vars ? JSON.parse(vars) : null
      } catch {
        vars = null
      }
    }
    window.__nappTheme = { name: theme, vars: vars || {} }
    const root = document.documentElement
    if (!root) return
    if (theme) root.style.colorScheme = theme === "dark" ? "dark" : "light"
    if (vars && vars.surface) root.style.background = vars.surface
  }
  window.__bridge_theme_change = applyTheme

  // napplets get intents and inc events as NAP pushes, never as napp actions;
  // a stray one must still be answered so nobody waits on it
  window.__bridge_dispatch_action = id => {
    rpc("napp.dispatchResult", { id, result: null }).catch(() => {})
  }

  // ── the frame ───────────────────────────────────────────────────
  // The frame goes in only once Go has handed over the verified document, so
  // its first load is the napplet itself. A napplet that reloads itself starts
  // over with a new shell.ready, which Go takes as a new session.
  const boot = async () => {
    let doc
    try {
      doc = await rpc("nap.boot")
    } catch (err) {
      document.body.textContent = "This napplet could not be started: " + ((err && err.message) || err)
      return
    }
    if (!doc || typeof doc.srcdoc !== "string") return
    if (!frame) {
      frame = document.createElement("iframe")
      // allow-scripts and nothing else: never allow-same-origin
      frame.setAttribute("sandbox", "allow-scripts")
      frame.setAttribute("referrerpolicy", "no-referrer")
      frame.setAttribute("title", typeof doc.title === "string" ? doc.title : "napplet")
      frame.style.cssText =
        "position:fixed;inset:0;width:100%;height:100%;border:0;margin:0;padding:0;display:block"
      frame.srcdoc = doc.srcdoc
      document.body.appendChild(frame)
      return
    }
    frame.srcdoc = doc.srcdoc
  }

  const start = () => {
    if (window.__nappTheme) applyTheme(window.__nappTheme.name, window.__nappTheme.vars)
    boot()
  }

  // a dev reload: Go has new bytes for us
  window.__nap_reload = () => {
    boot()
  }

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start, { once: true })
  else start()
})()
