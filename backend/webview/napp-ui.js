// napp-ui.js: the helpers that build Verdana's kit elements, as
// window.napp.ui, for napps that declare `requires: ["ui"]`. The launcher
// injects this file beside napp-ui.css, before the napp's own scripts.
//
// Nothing here is required: the kit is classes, and every helper below is a
// few lines that put the right ones on a plain element. Use them when they
// save typing, write the markup when they do not — the two look the same.
//
//   const ui = window.napp.ui
//   ui.bar(ui.button({ label: "publish", variant: "brand", onClick: save }),
//          ui.button({ label: "cancel" }))

;(() => {
  const already = window.napp && window.napp.ui
  if (already) return

  // put appends children: an element goes in as it is, anything else as text.
  const put = (node, children) => {
    for (const child of children.flat(4)) {
      if (child === null || child === undefined || child === false) continue
      node.append(child instanceof Node ? child : document.createTextNode(String(child)))
    }
    return node
  }

  // classes adds to a node's class list, skipping what is not a class.
  const classes = (node, ...names) => {
    for (const name of names) if (name) node.classList.add(...name.split(/\s+/))
    return node
  }

  // variant adds a modifier to a class: "accent" on .v-btn is v-btn--accent.
  const variant = (node, prefix, name) => {
    if (name) node.classList.add(prefix + "--" + name)
    return node
  }

  const on = (node, fn) => {
    if (typeof fn === "function") node.addEventListener("click", fn)
    return node
  }

  const el = (tag, className, ...children) => {
    const node = document.createElement(tag)
    if (className) node.className = className
    return put(node, children)
  }

  // text makes the short voices: the title, a name, the grey under it, the
  // italic label over a field.
  const text = (tag, className, value) => {
    const node = el(tag, className)
    node.textContent = value === undefined || value === null ? "" : String(value)
    return node
  }

  // icon is one of the kit's line glyphs, at 1em in the text's own color.
  const icon = (name, className) => el("span", "v-icon " + (name ? "v-icon--" + name : ""), className)

  const button = ({ label, variant: kind, icon: glyph, title, disabled, type, className, onClick } = {}) => {
    const node = el("button", "v-btn", glyph ? icon(glyph) : null, label === undefined ? null : label)
    variant(node, "v-btn", kind)
    classes(node, className)
    if (type) node.type = type
    if (title) node.title = title
    node.disabled = !!disabled
    return on(node, onClick)
  }

  // chip is a small pick, and a pill of small caps. active fills it with the
  // brand; a tone says what it is (bad, warn) and static makes it a fact
  // rather than a pick. Long names crop inside it, so pass the whole one as
  // title to keep it readable on hover.
  const chip = ({ label, active, tone, icon: glyph, title, className, onClick } = {}) => {
    const node = el("button", "v-chip", el("span", "", glyph ? [icon(glyph), " ", label] : label))
    if (active) node.classList.add("v-chip--on")
    if (tone) node.classList.add("v-chip--" + tone)
    classes(node, className)
    if (title) node.title = title
    return on(node, onClick)
  }

  // tabs owns its selection: items are strings or { value, label }, onChange
  // fires on a click, select() changes it without firing, value reads it.
  const tabs = ({ items = [], active, onChange, className } = {}) => {
    const row = el("div", "v-tabs")
    classes(row, className)
    row.setAttribute("role", "tablist")
    const entries = items.map(item => (typeof item === "object" && item !== null ? item : { value: item, label: item }))
    const buttons = entries.map(entry => {
      const b = el("button", "v-tab", entry.label === undefined ? entry.value : entry.label)
      b.setAttribute("role", "tab")
      return on(b, () => {
        row.select(entry.value)
        if (typeof onChange === "function") onChange(entry.value)
      })
    })
    buttons.forEach(b => row.append(b))
    row.value = active === undefined ? entries[0] && entries[0].value : active
    row.select = value => {
      row.value = value
      buttons.forEach((b, i) => {
        const picked = entries[i].value === value
        b.classList.toggle("v-tab--on", picked)
        b.setAttribute("aria-selected", picked ? "true" : "false")
      })
    }
    row.select(row.value)
    return row
  }

  const input = ({ type = "text", placeholder, value, className, ...rest } = {}) => {
    const node = el("input", "v-input")
    if (className) node.className = "v-input " + className
    node.type = type
    if (placeholder) node.placeholder = placeholder
    if (value !== undefined) node.value = value
    Object.assign(node, rest)
    return node
  }

  const textarea = ({ placeholder, value, rows, className } = {}) => {
    const node = el("textarea", "v-input")
    if (className) node.className = "v-input " + className
    if (placeholder) node.placeholder = placeholder
    if (rows) node.rows = rows
    if (value !== undefined) node.value = value
    return node
  }

  // field is the form line: the italic label over the control, a note under.
  // The label wraps the control, so clicking the words focuses it.
  const field = ({ label, control, note, className } = {}) => {
    const node = el("label", "v-field", label ? text("span", "v-label", label) : null, control || null)
    if (note) node.append(text("span", "v-field-note", note))
    classes(node, className)
    return node
  }

  const checkInput = ({ checked, title, onChange, name } = {}) => {
    const node = el("input", "v-check")
    node.type = name ? "radio" : "checkbox"
    if (name) node.name = name
    if (title) node.title = title
    node.checked = !!checked
    node.addEventListener("change", () => {
      if (typeof onChange === "function") onChange(node.checked, node)
    })
    return node
  }

  // check is the box and its words as one label, so the words toggle it too.
  const check = ({ label, note, checked, title, onChange, className } = {}) => {
    const box = checkInput({ checked, title, onChange })
    const words = el("span", "v-check-text", label || null)
    if (note) words.append(text("span", "v-check-note", note))
    const node = el("label", "v-check-label", box, label || note ? words : null)
    classes(node, className)
    node.input = box
    return node
  }

  // radios picks one of several: options are strings or { value, label, note }.
  const radios = ({ name, options = [], value, onChange, className } = {}) => {
    const row = el("div", "v-radios")
    classes(row, className)
    for (const option of options) {
      const entry = typeof option === "object" && option !== null ? option : { value: option }
      const pick = check({
        label: entry.label === undefined ? entry.value : entry.label,
        note: entry.note,
        checked: entry.value === value,
        onChange: onChange ? () => onChange(entry.value) : null,
      })
      pick.input.name = name || "radio"
      pick.input.classList.add("v-radio")
      row.append(pick)
    }
    return row
  }

  // details folds content away under a summary, which can be text or a few
  // elements (a title and a count, say).
  const details = ({ summary, open, className }, ...children) => {
    const node = el("details", "v-disclosure")
    classes(node, className)
    const head = el("summary", "", Array.isArray(summary) ? summary : [summary])
    node.append(head)
    if (open) node.open = true
    return put(node, children)
  }

  // rows that open: rowList() and row() make a group of them, one open at a
  // time, the way a window's list of instances behaves.
  const rowList = (className) => {
    const list = el("div", "v-rows")
    classes(list, className)
    // one name for the whole list: opening a row closes the one before it
    const name = "v-rows-" + Math.random().toString(36).slice(2)
    list.row = (...summary) => {
      const node = el("details", "v-row")
      node.name = name
      node.append(el("summary", "", summary.length === 1 ? [summary[0]] : summary))
      list.append(node)
      return node
    }
    return list
  }

  // list is a column of editable rows: a label per item and the controls
  // beside it, with the add line and the empty line under. add() and
  // delete() change the items; items is what it holds right now.
  const list = ({ items = [], label, mono, controls, add, empty, className } = {}) => {
    const rows = el("div", "v-items")
    const nothing = empty ? text("div", "v-empty", empty) : null
    const node = el("div", "v-list")
    classes(node, className)
    const held = [...items]
    const draw = () => {
      rows.textContent = ""
      for (const item of held) {
        const row = el("div", "v-item")
        if (label) {
          const name = el("span", "v-item-label", label(item))
          if (mono) name.classList.add("v-item-label--mono")
          row.append(name)
        }
        if (controls) put(row, [controls(item, node)])
        rows.append(row)
      }
      if (nothing) nothing.hidden = held.length > 0
    }
    node.items = held
    node.add = item => {
      if (held.includes(item)) return
      held.push(item)
      draw()
    }
    node.delete = item => {
      const at = held.indexOf(item)
      if (at < 0) return
      held.splice(at, 1)
      draw()
    }
    if (add) {
      const entry = input({ placeholder: add.placeholder, className: "v-add-input" })
      const error = el("div", "v-add-error")
      const form = el("div", "v-add", entry, button({ label: add.label, onClick: () => {
        const value = entry.value.trim()
        if (!value) {
          error.textContent = "type something to add"
          return
        }
        const said = typeof add.onAdd === "function" ? add.onAdd(value) : undefined
        if (said) {
          error.textContent = String(said)
          return
        }
        entry.value = ""
        error.textContent = ""
      } }))
      form.append(error)
      node.append(form)
    }
    node.append(rows)
    if (nothing) node.append(nothing)
    draw()
    return node
  }

  const notice = (message, { tone, icon: glyph, className } = {}) => {
    const which = tone || "warn"
    const mark = glyph === null ? null : icon(glyph || (which === "bad" ? "x" : which === "info" ? "info" : "warning"))
    const node = el("div", "v-notice v-notice--" + which, mark, el("span", "", message))
    classes(node, className)
    return node
  }

  const badge = (label, { tone, className } = {}) => {
    const node = text("span", "v-badge", label)
    if (tone) node.classList.add("v-badge--" + tone)
    classes(node, className)
    return node
  }

  // appIcon holds its space until the image lands, and with fade it eases in.
  const appIcon = ({ src, size = "m", fade, alt = "", className } = {}) => {
    const node = el("span", "v-appicon v-appicon--" + size)
    if (fade) node.classList.add("v-appicon--fade")
    classes(node, className)
    const img = el("img")
    img.alt = alt
    img.addEventListener("load", () => img.classList.add("loaded"))
    if (src) img.src = src
    node.append(img)
    node.img = img
    return node
  }

  // busy breathes anything until it is done; busy(node, false) stops it.
  const busy = (node, on = true) => {
    node.classList.toggle("v-busy", !!on)
    if (on) node.setAttribute("aria-busy", "true")
    else node.removeAttribute("aria-busy")
    return node
  }

  // spinner is a ring 1em across, in the text's own color: set a font-size to
  // make it bigger, as a window does.
  const spinner = (className) => {
    const node = el("span", "v-spinner")
    classes(node, className)
    node.setAttribute("role", "status")
    node.setAttribute("aria-label", "loading")
    return node
  }

  // section is a named group of rows: the name shouted, a hairline under it.
  const section = (label, ...children) => el("div", "v-section", text("div", "v-label", label), children)

  // header is the first line of a screen: a name, and what acts on it.
  const header = (...children) => el("div", "v-header", children)

  // switch is a setting that is on or off, and where it stands.
  const toggle = ({ label, note, checked, onChange, className } = {}) => {
    const box = el("input")
    box.type = "checkbox"
    box.checked = !!checked
    box.addEventListener("change", () => {
      if (typeof onChange === "function") onChange(box.checked, box)
    })
    const words = el("span", "v-switch-text", label || null)
    if (note) words.append(el("span", "v-switch-note", note))
    const node = el("label", "v-switch", box, el("span", "v-switch-track"), words)
    classes(node, className)
    node.input = box
    return node
  }

  // meter says how far along something is: value 0…1, a tone for the rest.
  const meter = ({ value = 0, tone, className } = {}) => {
    const node = el("div", "v-meter")
    if (tone) node.classList.add("v-meter--" + tone)
    classes(node, className)
    node.value = value
    const set = v => node.style.setProperty("--v-value", Math.max(0, Math.min(1, v)) * 100 + "%")
    set(value)
    node.set = set
    return node
  }

  const kbd = value => text("kbd", "v-kbd", value)

  const code = value => text("code", "v-code", value)
  const codeBlock = value => text("pre", "v-codeblock", value)

  const links = (...children) => el("nav", "v-links", children)

  const ui = {
    // layout
    el,
    stack: (...children) => el("div", "v-col", children),
    bar: (...children) => el("div", "v-bar", children),
    card: (...children) => el("div", "v-card", children),
    plate: (...children) => el("div", "v-plate", children),
    header,
    section,
    /** grow is a row's filler: whatever follows it is pushed to the far end. */
    grow: () => el("span", "v-grow"),
    divider: () => el("hr", "v-divider"),
    // type
    title: value => text("h2", "v-title", value),
    display: value => text("h1", "v-display", value),
    heading: value => text("h3", "v-heading", value),
    /** The voice that names a group: small caps, tracked, grey. */
    label: value => text("div", "v-label", value),
    caption: value => text("span", "v-caption", value),
    hint: value => text("span", "v-hint", value),
    mono: value => text("span", "v-mono", value),
    kbd,
    code,
    codeBlock,
    empty: value => text("div", "v-empty", value),
    meter,
    // controls
    button,
    chip,
    tabs,
    input,
    textarea,
    field,
    check,
    radios,
    switch: toggle,
    // content
    details,
    rowList,
    list,
    notice,
    badge,
    icon,
    appIcon,
    links,
    busy,
    spinner,
  }

  // bridge.js fills in the rest of window.napp and keeps what is here.
  window.napp = Object.assign(window.napp || {}, { ui })
})()
