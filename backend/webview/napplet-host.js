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

  // Ordinary NAP envelopes stay small. NAP-UPLOAD may carry up to 16 MiB of
  // raw bytes; base64 plus its JSON envelope needs a larger transport bound.
  const MAX_ENVELOPE = 1024 * 1024
  // A max-sized NAP-FS write is 1 MiB after decoding and about 1.34 MiB on
  // the JSON wire. Keep the larger allowance specific to fs.write.
  const MAX_FS_WRITE_ENVELOPE = 1536 * 1024
  const MAX_UPLOAD_BYTES = 16 * 1024 * 1024
  const MAX_UPLOAD_ENVELOPE = 24 * 1024 * 1024

  const bytesToBase64 = bytes => {
    let out = ""
    const chunk = 0x8000
    for (let i = 0; i < bytes.length; i += chunk) {
      out += String.fromCharCode(...bytes.subarray(i, i + chunk))
    }
    return btoa(out)
  }

  // postMessage gives this launcher-owned page real Blob/ArrayBuffer values,
  // but the native RPC carrier is JSON. Encode them here so napplets still use
  // NAP-UPLOAD's structured-clone API and never have to base64 their own data.
  const dehydrate = async (value, seen = new WeakSet()) => {
    if (value instanceof Blob) {
      if (value.size > MAX_UPLOAD_BYTES) throw new Error("upload is too large")
      const bytes = new Uint8Array(await value.arrayBuffer())
      return { __blob: { b64: bytesToBase64(bytes), mime: value.type || "" } }
    }
    if (value instanceof ArrayBuffer) {
      if (value.byteLength > MAX_UPLOAD_BYTES) throw new Error("upload is too large")
      return { __blob: { b64: bytesToBase64(new Uint8Array(value)), mime: "" } }
    }
    if (ArrayBuffer.isView(value)) {
      if (value.byteLength > MAX_UPLOAD_BYTES) throw new Error("upload is too large")
      const bytes = new Uint8Array(value.buffer, value.byteOffset, value.byteLength)
      return { __blob: { b64: bytesToBase64(bytes), mime: "" } }
    }
    if (Array.isArray(value)) {
      if (seen.has(value)) throw new Error("cyclic NAP envelope")
      seen.add(value)
      const out = []
      for (const item of value) out.push(await dehydrate(item, seen))
      seen.delete(value)
      return out
    }
    if (value && typeof value === "object") {
      if (seen.has(value)) throw new Error("cyclic NAP envelope")
      seen.add(value)
      const out = {}
      for (const key of Object.keys(value)) out[key] = await dehydrate(value[key], seen)
      seen.delete(value)
      return out
    }
    return value
  }

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
    // one at a time: the desktop binding runs every call on its own thread,
    // so two calls in flight can reach Go in either order, and NAP needs the
    // napplet's order kept (shell.ready first, a subscribe before its close).
    // Go only queues the envelope before answering, so the wait is short.
    outbound = outbound
      .then(async () => {
        const encoded = await dehydrate(data)
        const json = JSON.stringify(encoded)
        const limit = data.type === "upload.upload"
          ? MAX_UPLOAD_ENVELOPE
          : data.type === "fs.write" ? MAX_FS_WRITE_ENVELOPE : MAX_ENVELOPE
        if (!json || json.length > limit) throw new Error("NAP envelope is too large")
        return rpc("nap.msg", json)
      })
      .then(deliver, err => {
        console.error("[napplet-host]", err)
        refuse(data, err)
      })
  })

  // refuse answers a request that never reached Go (too large, unencodable,
  // or the rpc failed). The shim sets no deadline of its own, so without an
  // answer the napplet would wait for good. Mirrors napCall.fail in nap.go.
  const refuse = (data, err) => {
    if (typeof data.id !== "string" && typeof data.id !== "number") return
    const error = (err && err.message) || "request failed"
    const reply = { id: data.id }
    switch (data.type) {
      case "config.get":
        Object.assign(reply, { type: "config.schemaError", code: "internal-error", error })
        break
      case "notify.permission.request":
        Object.assign(reply, { type: "notify.permission.result", granted: false })
        break
      case "resource.bytes":
      case "resource.bytesMany":
      case "relay.publish":
        Object.assign(reply, { type: data.type + ".error", ok: false, error })
        break
      default:
        Object.assign(reply, { type: data.type + ".result", ok: false, error })
    }
    deliver(reply)
  }

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
    if (vars && typeof vars === "object") {
      for (const k of ["surface-alt", "border", "text"]) {
        if (typeof vars[k] === "string") root.style.setProperty("--" + k, vars[k])
      }
    }
  }
  window.__bridge_theme_change = applyTheme

  // napplets get intents and inc events as NAP pushes, never as napp actions;
  // a stray one must still be answered so nobody waits on it
  window.__bridge_dispatch_action = id => {
    rpc("napp.dispatchResult", { id, result: null }).catch(() => {})
  }

  // ── the chrome ──────────────────────────────────────────────────
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
