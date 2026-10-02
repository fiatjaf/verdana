;(() => {
  // A napp's settings window: the launcher-owned page that renders a
  // napplet's NAP-CONFIG schema as a form, and lists what the user let the
  // napp do. Nothing of the napplet runs here. The schema is data: only the
  // keywords the backend already checked are read, every string goes in as
  // text, never as HTML.
  //
  // The page sends back the settings the user set or touched, nothing else:
  // a setting left alone keeps following the napplet's default. Secrets are
  // never sent down; the page only learns which ones are set.

  if (window !== window.top) return

  // ── talking to the host ─────────────────────────────────────────
  // Desktop: __verdanaSettingsRPC, the child's token-carrying wrapper.
  // Android: the __verdanaHost web message channel.
  const rpc = (() => {
    const decode = value => {
      const val = typeof value === "string" ? JSON.parse(value) : value
      if (val && val.__bridge_error) throw new Error(val.__bridge_error)
      return val
    }
    const encode = params => (params !== undefined ? JSON.stringify(params) : "null")

    if (typeof window.__verdanaSettingsRPC === "function") {
      const bound = window.__verdanaSettingsRPC
      return (method, params) => bound(method, encode(params)).then(decode)
    }

    const port = window.__verdanaHost
    if (!port) return () => Promise.reject(new Error("no host to talk to"))

    const pending = new Map()
    let serial = 0
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
        const id = ++serial
        pending.set(id, { resolve, reject })
        port.postMessage(JSON.stringify({ t: "rpc", id, method, params: encode(params) }))
      })
  })()

  // ── theme ───────────────────────────────────────────────────────
  const applyTheme = (theme, vars) => {
    if (typeof vars === "string") {
      try {
        vars = vars ? JSON.parse(vars) : null
      } catch {
        vars = null
      }
    }
    const root = document.documentElement
    if (!root) return
    if (theme) root.style.colorScheme = theme === "dark" ? "dark" : "light"
    if (vars && typeof vars === "object") {
      for (const [k, v] of Object.entries(vars)) {
        if (/^[a-z0-9-]+$/.test(k) && typeof v === "string") root.style.setProperty("--" + k, v)
      }
    }
  }
  window.__bridge_theme_change = applyTheme

  // ── helpers ─────────────────────────────────────────────────────
  const el = (tag, attrs, ...children) => {
    const node = document.createElement(tag)
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v === undefined || v === null || v === false) continue
      if (k === "class") node.className = v
      else if (k.startsWith("on")) node.addEventListener(k.slice(2), v)
      else node.setAttribute(k, v === true ? "" : String(v))
    }
    for (const c of children) {
      if (c === undefined || c === null || c === false) continue
      node.append(c instanceof Node ? c : String(c))
    }
    return node
  }
  const str = v => (typeof v === "string" ? v : undefined)
  const isObj = v => v && typeof v === "object" && !Array.isArray(v)
  const join = (path, key) => (path ? path + "." + key : key)

  // properties in display order: x-napplet-order first, ascending, then
  // the rest; ties and the rest by key
  const ordered = props =>
    Object.entries(props || {})
      .filter(([, s]) => isObj(s))
      .sort(([ka, a], [kb, b]) => {
        const oa = typeof a["x-napplet-order"] === "number" && a["x-napplet-order"] >= 0 ? a["x-napplet-order"] : null
        const ob = typeof b["x-napplet-order"] === "number" && b["x-napplet-order"] >= 0 ? b["x-napplet-order"] : null
        if (oa !== null && ob !== null && oa !== ob) return oa - ob
        if (oa !== null && ob === null) return -1
        if (oa === null && ob !== null) return 1
        return ka < kb ? -1 : ka > kb ? 1 : 0
      })

  // ── state ───────────────────────────────────────────────────────
  let data = null // the last settings.load
  let fields = [] // { path, read(), touched() }
  let pendingSection = ""

  const describe = (schema, wrap) => {
    const text = str(schema.description) || str(schema.markdownDescription)
    if (text) wrap.append(el("div", { class: "hint" }, text))
    if (str(schema.deprecationMessage)) wrap.append(el("div", { class: "deprecated" }, schema.deprecationMessage))
  }

  // field builds the editor for one leaf and registers how to read it back.
  // read() returns undefined to leave the setting unset, null to clear a
  // secret, or the value.
  const field = (key, schema, path, value, required) => {
    const title = str(schema.title) || key
    const wrap = el("div", { class: "field", "data-path": path })
    const id = "f-" + path.replace(/[^a-zA-Z0-9_-]/g, "_")
    let touched = false
    const touch = () => {
      touched = true
      wrap.classList.remove("invalid")
    }
    let read

    const secret = schema.type === "string" && schema["x-napplet-secret"] === true
    if (Array.isArray(schema.enum)) {
      const select = el("select", { id, onchange: () => (touch(), hint()) })
      const blank = !required && value === undefined
      if (blank) select.append(el("option", { value: "" }, "—"))
      schema.enum.forEach((v, i) => {
        const opt = el("option", { value: String(i) }, typeof v === "string" ? v : JSON.stringify(v))
        if (JSON.stringify(v) === JSON.stringify(value)) opt.selected = true
        select.append(opt)
      })
      const descs = Array.isArray(schema.enumDescriptions) ? schema.enumDescriptions : []
      const descEl = el("div", { class: "hint" })
      const hint = () => {
        const d = descs[Number(select.value)]
        descEl.textContent = typeof d === "string" ? d : ""
      }
      hint()
      wrap.append(el("label", { for: id }, title), select, descEl)
      read = () => (select.value === "" ? undefined : schema.enum[Number(select.value)])
    } else if (schema.type === "boolean") {
      wrap.classList.add("check")
      const box = el("input", { type: "checkbox", id, onchange: touch })
      box.checked = value === true
      wrap.append(el("label", { for: id }, box, title))
      read = () => box.checked
    } else if (schema.type === "number" || schema.type === "integer") {
      const input = el("input", {
        type: "number",
        id,
        min: typeof schema.minimum === "number" ? schema.minimum : undefined,
        max: typeof schema.maximum === "number" ? schema.maximum : undefined,
        step: schema.type === "integer" ? 1 : "any",
        oninput: touch,
      })
      if (typeof value === "number") input.value = String(value)
      wrap.append(el("label", { for: id }, title), input)
      read = () => (input.value.trim() === "" ? undefined : Number(input.value))
    } else if (schema.type === "string") {
      // format is only a hint: never a widget that refuses what it does not like
      const formats = { color: "color", date: "date" }
      const type = secret ? "password" : formats[schema.format] || "text"
      const isSet = secret && data.secrets.includes(path)
      let cleared = false
      const input = el("input", {
        type,
        id,
        autocomplete: secret ? "off" : undefined,
        minlength: schema.minLength,
        maxlength: schema.maxLength,
        placeholder: secret ? (isSet ? "set (type to replace)" : "not set") : undefined,
        oninput: () => {
          touch()
          cleared = false
        },
      })
      if (!secret && typeof value === "string") input.value = value
      wrap.append(el("label", { for: id }, title))
      if (secret && isSet) {
        const clear = el(
          "button",
          {
            type: "button",
            class: "danger",
            onclick: () => {
              touch()
              cleared = true
              input.value = ""
              input.placeholder = "will be cleared"
            },
          },
          "Clear",
        )
        wrap.append(el("div", { class: "row" }, input, clear))
      } else {
        wrap.append(input)
      }
      read = () => {
        if (secret) {
          if (cleared) return null
          return input.value === "" ? undefined : input.value
        }
        return input.value === "" ? undefined : input.value
      }
    } else if (schema.type === "array" && isObj(schema.items)) {
      const items = schema.items
      const list = el("div")
      const rows = []
      const addRow = v => {
        const input =
          items.type === "boolean"
            ? el("input", { type: "checkbox", onchange: touch })
            : el("input", {
                type: items.type === "number" || items.type === "integer" ? "number" : "text",
                step: items.type === "integer" ? 1 : undefined,
                oninput: touch,
              })
        if (items.type === "boolean") input.checked = v === true
        else if (v !== undefined) input.value = String(v)
        const row = el("div", { class: "row" }, input)
        const entry = { input, row }
        row.append(
          el(
            "button",
            {
              type: "button",
              onclick: () => {
                touch()
                rows.splice(rows.indexOf(entry), 1)
                row.remove()
              },
            },
            "Remove",
          ),
        )
        rows.push(entry)
        list.append(row)
      }
      ;(Array.isArray(value) ? value : []).forEach(addRow)
      wrap.append(
        el("div", { class: "label" }, title),
        list,
        el("button", { type: "button", onclick: () => (touch(), addRow(undefined)) }, "Add"),
      )
      read = () =>
        rows.map(({ input }) => {
          if (items.type === "boolean") return input.checked
          if (items.type === "number" || items.type === "integer") return Number(input.value)
          return input.value
        })
    } else {
      return null
    }
    describe(schema, wrap)
    fields.push({ path, read, touched: () => touched, wrap })
    return wrap
  }

  // object renders a nested object's properties into a container
  const object = (schema, path, values, into) => {
    const required = Array.isArray(schema.required) ? schema.required : []
    for (const [key, sub] of ordered(schema.properties)) {
      const p = join(path, key)
      const v = isObj(values) ? values[key] : undefined
      if (sub.type === "object") {
        const set = el("fieldset", { "data-path": p }, el("legend", {}, str(sub.title) || key))
        describe(sub, set)
        object(sub, p, v, set)
        into.append(set)
        continue
      }
      const f = field(key, sub, p, v, required.includes(key))
      if (f) into.append(f)
    }
  }

  // collect builds the values to save: what was set before or touched now
  const collect = () => {
    const out = {}
    const set = new Set(data.set)
    for (const f of fields) {
      if (!f.touched() && !set.has(f.path)) continue
      const v = f.read()
      if (v === undefined) continue
      const parts = f.path.split(".")
      let at = out
      for (const part of parts.slice(0, -1)) at = at[part] = isObj(at[part]) ? at[part] : {}
      at[parts[parts.length - 1]] = v
    }
    return out
  }

  const status = (text, error) => {
    const s = document.getElementById("status")
    if (!s) return
    s.textContent = text || ""
    s.className = "status" + (error ? " error" : "")
  }

  const markInvalid = message => {
    // the backend names the setting first: "a.b is not a valid value"
    const path = String(message).split(" ")[0]
    for (const f of fields) if (f.path === path) f.wrap.classList.add("invalid")
  }

  const permissionsView = () => {
    const box = el("section", { "data-section": "__permissions" }, el("h2", {}, "Permissions"))
    if (!data.permissions.length) {
      box.append(el("p", { class: "muted" }, "Nothing remembered for this napp."))
      return box
    }
    const byPerm = new Map()
    for (const r of data.permissions) {
      if (!byPerm.has(r.permission)) byPerm.set(r.permission, [])
      byPerm.get(r.permission).push(r)
    }
    for (const [perm, rules] of byPerm) {
      const detail = el("div", {}, el("strong", {}, perm))
      for (const r of rules) {
        detail.append(el("div", { class: "hint" }, (r.subject ? r.subject + ": " : "") + r.decision + (r.target ? " → " + r.target : "")))
      }
      box.append(
        el(
          "div",
          { class: "perm" },
          detail,
          el("button", { type: "button", onclick: () => run("settings.forgetPermission", { permission: perm }, "Forgotten") }, "Forget"),
        ),
      )
    }
    box.append(
      el(
        "div",
        { class: "actions" },
        el("button", { type: "button", class: "danger", onclick: () => run("settings.forgetPermission", { permission: "" }, "Forgotten") }, "Forget all"),
      ),
    )
    return box
  }

  // the napp's own page: its NAP-CONFIG form and its permissions
  const nappPage = () => {
    const out = []
    let schema = null
    if (data.schema) {
      try {
        schema = typeof data.schema === "string" ? JSON.parse(data.schema) : data.schema
      } catch {
        schema = null
      }
    }
    if (!schema || !isObj(schema.properties)) {
      out.push(el("p", { class: "muted" }, "This napp has no settings of its own."))
    } else {
      // top-level properties grouped by x-napplet-section, in order of
      // first appearance; the unsectioned ones first, under no heading
      const groups = new Map([["", []]])
      for (const [key, sub] of ordered(schema.properties)) {
        const sec = str(sub["x-napplet-section"]) || ""
        if (!groups.has(sec)) groups.set(sec, [])
        groups.get(sec).push(key)
      }
      const form = el("form", { onsubmit: e => (e.preventDefault(), save()) })
      for (const [sec, keys] of groups) {
        if (!keys.length) continue
        const box = el("section", { "data-section": sec })
        if (sec) box.append(el("h2", {}, sec))
        const props = {}
        for (const k of keys) props[k] = schema.properties[k]
        object({ properties: props, required: schema.required }, "", data.values, box)
        form.append(box)
      }
      form.append(
        el(
          "div",
          { class: "actions" },
          el("span", { id: "status", class: "status" }),
          el("button", { type: "button", onclick: () => run("settings.reset", undefined, "Defaults restored") }, "Reset to defaults"),
          el("button", { type: "submit", class: "primary" }, "Save"),
        ),
      )
      out.push(form)
    }
    out.push(permissionsView())
    return out
  }

  // listEditor edits a list of urls; read() is what it holds now
  const listEditor = (title, hint, values, placeholder) => {
    const list = el("div")
    const inputs = []
    const add = v => {
      const input = el("input", { type: "text", inputmode: "url", placeholder, spellcheck: "false" })
      if (v) input.value = v
      const row = el("div", { class: "row" }, input)
      row.append(
        el(
          "button",
          {
            type: "button",
            onclick: () => {
              inputs.splice(inputs.indexOf(input), 1)
              row.remove()
            },
          },
          "Remove",
        ),
      )
      inputs.push(input)
      list.append(row)
    }
    ;(values || []).forEach(add)
    const box = el(
      "section",
      {},
      el("h2", {}, title),
      el("div", { class: "hint" }, hint),
      el("div", { style: "margin-top:8px" }, list),
      el("button", { type: "button", onclick: () => add("") }, "Add"),
    )
    return { box, read: () => inputs.map(i => i.value.trim()).filter(Boolean) }
  }

  // the launcher's own page: where napps are found and fetched from
  const verdanaPage = () => {
    const l = data.launcher || {}
    const themeMode = el(
      "select",
      { name: "themeMode" },
      el("option", { value: "system" }, "System"),
      el("option", { value: "light" }, "Light"),
      el("option", { value: "dark" }, "Dark"),
    )
    themeMode.value = l.themeMode || "system"
    const appearance = el(
      "section",
      {},
      el("h2", {}, "Appearance"),
      el("div", { class: "hint" }, "System follows your desktop appearance and updates open napps and napplets automatically."),
      el("label", { class: "field" }, el("span", {}, "Theme"), themeMode),
    )
    let autostart = null
    if (l.autostartSupported) {
      autostart = el("input", { type: "checkbox", name: "autostart" })
      autostart.checked = !!l.autostart
      appearance.append(
        el(
          "div",
          { class: "field check" },
          el(
            "label",
            {},
            autostart,
            el("span", {}, "Launch at login", el("span", { class: "hint" }, "Start Verdana in the background.")),
          ),
        ),
      )
    }
    const relays = listEditor(
      "Relays",
      "Napps and napplets are discovered on these relays.",
      l.relays,
      "wss://relay.example.com",
    )
    const servers = listEditor(
      "Blossom servers",
      "Napp and napplet files are fetched from these servers first, before the ones a napp or its author names. Every file is checked against its hash, wherever it comes from.",
      l.blossomServers,
      "https://blossom.example.com",
    )
    const form = el(
      "form",
      {
        onsubmit: e => {
          e.preventDefault()
          run(
            "settings.saveLauncher",
            {
              themeMode: themeMode.value,
              relays: relays.read(),
              blossomServers: servers.read(),
              ...(autostart ? { autostart: autostart.checked } : {}),
            },
            "Saved",
          )
        },
      },
      appearance,
      relays.box,
      servers.box,
      el(
        "div",
        { class: "actions" },
        el("span", { id: "status", class: "status" }),
        el("button", { type: "submit", class: "primary" }, "Save"),
      ),
    )
    return [form]
  }

  let tab = ""
  const render = () => {
    const app = document.getElementById("app")
    fields = []
    if (!data.napp) tab = "verdana"
    else if (!tab) tab = "napp"
    document.title = (data.name || "Napp") + " — Settings"
    const main = [el("h1", {}, data.name || "Napp"), el("div", { class: "muted" }, "Settings")]

    if (data.napp) {
      const tabBtn = (id, label) =>
        el(
          "button",
          {
            type: "button",
            role: "tab",
            "aria-selected": tab === id ? "true" : "false",
            class: "tab" + (tab === id ? " active" : ""),
            onclick: () => {
              tab = id
              render()
            },
          },
          label,
        )
      main.push(el("div", { class: "tabs", role: "tablist" }, tabBtn("napp", data.name || "Napp"), tabBtn("verdana", "Verdana")))
    }
    main.push(...(tab === "napp" ? nappPage() : verdanaPage()))
    app.replaceChildren(...main)

    const sec = pendingSection || data.section
    pendingSection = ""
    data.section = ""
    if (sec && tab === "napp") showSection(sec)
  }

  const showSection = name => {
    for (const box of document.querySelectorAll("section[data-section]")) {
      if (box.getAttribute("data-section") !== name) continue
      box.scrollIntoView({ block: "start", behavior: "smooth" })
      box.classList.add("highlight")
      setTimeout(() => box.classList.remove("highlight"), 1500)
    }
  }

  let busy = false
  const run = async (method, params, done) => {
    if (busy) return
    busy = true
    try {
      data = await rpc(method, params)
      render()
      status(done)
    } catch (err) {
      status((err && err.message) || String(err), true)
      markInvalid((err && err.message) || err)
    } finally {
      busy = false
    }
  }

  const save = () => run("settings.save", { values: collect() }, "Saved")

  const load = async () => {
    try {
      data = await rpc("settings.load")
      render()
    } catch (err) {
      document.getElementById("app").textContent = "Could not load settings: " + ((err && err.message) || err)
    }
  }

  // the backend's hooks: the schema changed under us, or the napplet asked
  // for a section while this window was already open
  window.__settings_reload = load
  window.__settings_section = name => {
    if (!data) {
      pendingSection = name
      return
    }
    if (data.napp && tab !== "napp") {
      tab = "napp"
      pendingSection = name
      render()
      return
    }
    showSection(name)
  }

  const start = () => {
    if (window.__nappTheme) applyTheme(window.__nappTheme.name, window.__nappTheme.vars)
    load()
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start, { once: true })
  else start()
})()
