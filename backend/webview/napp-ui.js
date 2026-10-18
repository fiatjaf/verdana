// napp-ui.js: the helpers that build Verdana's kit elements, as
// window.napp.ui, for napps that declare `requires: ["ui"]`. The launcher
// injects this file beside napp-ui.css, before the napp's own scripts.
//
// The helpers, and the classes they put on what they build, are the ones
// every napp launcher's kit has (nostrapps' napp-ui.js and napp-ui.css are
// the reference): btn, btn-primary, ui-tab, ui-input, …, with state classes
// such as active, open and danger. So a napp written against one kit, by
// helper or by hand, runs on this one; napp-ui.css is what makes those
// classes look like Verdana.
//
// Nothing here is required: the kit is classes, and every helper below is a
// few lines that put the right ones on a plain element. Use them when they
// save typing, write the markup when they do not — the two look the same.
//
//   const ui = window.napp.ui
//   ui.bar(ui.button({ label: "publish", variant: "primary", onClick: save }),
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
    for (const name of names) if (name) node.classList.add(...name.split(/\s+/).filter(Boolean))
    return node
  }

  // extra is the context's own classes: `class`, as the kits say it, or
  // `className`, as this one used to.
  const extra = opts => (opts ? opts.class || opts.className : undefined)


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

  // The words this kit used to have for variants and tones, said in the
  // kits' own. Theirs pass through.
  const VARIANTS = { brand: "primary", line: "outline", bad: "danger", warn: "warning", quiet: "ghost", plain: "link" }
  const TONES = { bad: "danger" }

  // A number is px, a string any CSS length.
  const length = v => (typeof v === "number" ? v + "px" : String(v))

  // ─── icons ─────────────────────────────────────────────────────
  // The glyphs, the body of a 16×16 viewBox each, stroked in the text's
  // color: a napp can add its own. iconInk is how far each one's ink sits in
  // from the start and the end of its box, in em, the round ends included —
  // what flush takes back so the ink meets the edge.
  const icons = {
    check: '<path d="M3.5 8.5l3 3 6-6.5"/>',
    x: '<path d="M4 4l8 8M12 4l-8 8"/>',
    plus: '<path d="M8 3.5v9M3.5 8h9"/>',
    minus: '<path d="M3.5 8h9"/>',
    chevron: '<path d="M6 3.5L10.5 8 6 12.5"/>',
    arrow: '<path d="M3 8h9.5M9 4.5L12.5 8 9 11.5"/>',
    back: '<path d="M13 8H3.5M7 4.5L3.5 8 7 11.5"/>',
    trash: '<path d="M3 4.5h10M6.5 4.5V3h3v1.5M4.5 4.5l.5 9h6l.5-9M6.8 7v4M9.2 7v4"/>',
    reload: '<path d="M12.8 8a4.8 4.8 0 1 1-1.7-3.7M13.2 2.8V6H10"/>',
    reset: '<path d="M13 13.5V9.5a4 4 0 0 0-4-4H4"/><path d="M7 2.5 4 5.5 7 8.5"/>',
    search: '<circle cx="7" cy="7" r="3.5"/><path d="M9.6 9.6L13.5 13.5"/>',
    external: '<path d="M9.5 3.5H12.5v3M12.5 3.5L7.5 8.5M11 9.5v3H3.5V5H6.5"/>',
    warning: '<path d="M8 2.5 14.5 13.5 1.5 13.5Z"/><path d="M8 6.5v3.2"/><path d="M8 11.6v.6"/>',
    info: '<circle cx="8" cy="8" r="5.5"/><path d="M8 7.4v4"/><path d="M8 5v.6"/>',
    star: '<path d="M8 2.5l1.7 3.6 3.9.5-2.8 2.7.7 3.9L8 11.3l-3.5 1.9.7-3.9L2.4 6.6l3.9-.5z"/>',
    copy: '<path d="M5.5 5.5h7v7h-7zM3.5 10.5v-7h7"/>',
    folder: '<path d="M2.5 4.5h4L8 6.5h5.5v6h-11z"/>',
    user: '<circle cx="8" cy="5.5" r="2.5"/><path d="M3.5 13.5a4.5 4.5 0 0 1 9 0"/>',
    home: '<path d="M3 7.5L8 3l5 4.5V13H3zM6.5 13V9.5h3V13"/>',
    settings: '<path d="M3 4.5h10M3 8h10M3 11.5h10"/><circle cx="6" cy="4.5" r="1.4"/><circle cx="10" cy="8" r="1.4"/><circle cx="5" cy="11.5" r="1.4"/>',
    sun: '<circle cx="8" cy="8" r="3"/><path d="M8 1.5v1.4M8 13.1v1.4M1.5 8h1.4M13.1 8h1.4M3.4 3.4l1 1M11.6 11.6l1 1M12.6 3.4l-1 1M4.4 11.6l-1 1"/>',
    moon: '<path d="M13 9.5A5.5 5.5 0 0 1 6.2 2.6a5.5 5.5 0 1 0 6.8 6.9z"/>',
    bolt: '<path d="M9 2L4.5 9h3L7 14l4.5-7h-3z"/>',
    link: '<path d="M6.5 9.5a2.5 2.5 0 0 0 3.5 0l2-2a2.5 2.5 0 0 0-3.5-3.5l-1 1M9.5 6.5a2.5 2.5 0 0 0-3.5 0l-2 2a2.5 2.5 0 0 0 3.5 3.5l1-1"/>',
    save: '<path d="M3.5 3.5h7L12.5 5.5v7h-9zM5.5 3.5v3h4v-3M5.5 12.5V9h5v3.5"/>',
    window: '<path d="M2.5 4h11v8h-11zM2.5 6.5h11"/>',
    move: '<path d="M2.5 8h7.5M7 5l3 3-3 3M13 4v8"/>',
    tile: '<path d="M2.5 2.5h4v4h-4zM9.5 2.5h4v4h-4zM2.5 9.5h4v4h-4zM9.5 9.5h4v4h-4z"/>',
    pack: '<path d="M2.5 6V2.5H6M13.5 6V2.5H10M2.5 10v3.5H6M13.5 10v3.5H10"/>',
    grid: '<path d="M2.5 2.5h11v11h-11zM8 2.5v11M2.5 8h11"/>',
    close: '<path d="M4 4l8 8M12 4l-8 8"/>',
    warn: '<path d="M8 2.5 14.5 13.5 1.5 13.5Z"/><path d="M8 6.5v3.2"/><path d="M8 11.6v.6"/>',
    forward: '<path d="M3 8h9.5M9 4.5L12.5 8 9 11.5"/>',
  }
  const iconInk = {
    check: [0.1719, 0.1719],
    x: [0.2031, 0.2031],
    plus: [0.1719, 0.1719],
    minus: [0.1719, 0.1719],
    chevron: [0.3281, 0.2969],
    arrow: [0.1406, 0.1719],
    back: [0.1719, 0.1406],
    trash: [0.1406, 0.1406],
    reload: [0.1531, 0.1281],
    reset: [0.2031, 0.1406],
    search: [0.1719, 0.1094],
    external: [0.1719, 0.1719],
    warning: [0.0469, 0.0469],
    info: [0.1094, 0.1094],
    star: [0.1031, 0.1031],
    copy: [0.1719, 0.1719],
    folder: [0.1094, 0.1094],
    user: [0.1719, 0.1719],
    home: [0.1406, 0.1406],
    settings: [0.1406, 0.1406],
    sun: [0.0469, 0.0469],
    moon: [0.0934, 0.1406],
    bolt: [0.2344, 0.2344],
    link: [0.1719, 0.1719],
    save: [0.1719, 0.1719],
    window: [0.1094, 0.1094],
    move: [0.1094, 0.1406],
    tile: [0.1094, 0.1094],
    pack: [0.1094, 0.1094],
    grid: [0.1094, 0.1094],
    close: [0.2031, 0.2031],
    warn: [0.0469, 0.0469],
    forward: [0.1406, 0.1719],
  }
  const SVG = "http://www.w3.org/2000/svg"

  // icon is one of the kit's line glyphs, an inline <svg> at 1em in the
  // text's own color. flush ("start" | "end") puts its ink at that edge
  // instead of its box, for an icon that ends a line under text.
  const icon = (name, opts = {}) => {
    if (typeof opts === "string") opts = { class: opts }
    const svg = document.createElementNS(SVG, "svg")
    svg.setAttribute("viewBox", "0 0 16 16")
    svg.setAttribute("width", "1em")
    svg.setAttribute("height", "1em")
    svg.setAttribute("fill", "none")
    svg.setAttribute("stroke", "currentColor")
    svg.setAttribute("stroke-width", "1.5")
    svg.setAttribute("stroke-linecap", "round")
    svg.setAttribute("stroke-linejoin", "round")
    svg.setAttribute("aria-hidden", "true")
    svg.classList.add("ui-icon")
    if (name) svg.dataset.icon = name
    if (opts.flush === "start" || opts.flush === "end") {
      svg.classList.add("flush-" + opts.flush)
      // the box narrows to the ink (CSS); the glyph keeps its size, cropped
      // on the far side, where its box is empty
      svg.setAttribute("preserveAspectRatio", opts.flush === "end" ? "xMinYMid slice" : "xMaxYMid slice")
    }
    const ink = iconInk[name]
    if (ink) {
      svg.style.setProperty("--ink-start", ink[0] + "em")
      svg.style.setProperty("--ink-end", ink[1] + "em")
    }
    classes(svg, extra(opts))
    svg.innerHTML = icons[name] || ""
    return svg
  }

  // ─── buttons ───────────────────────────────────────────────────
  // A glyph alone makes a square, named by its title for assistive tech.
  const button = ({ label, variant: kind, icon: glyph, title, disabled, type, onClick, ...opts } = {}) => {
    const alone = glyph && (label === undefined || label === null)
    const node = el("button", "btn", glyph ? icon(glyph) : null, alone ? null : label)
    node.classList.add("btn-" + (VARIANTS[kind] || kind || "outline"))
    if (alone && kind !== "icon") node.classList.add("btn-icon")
    classes(node, extra(opts))
    // inside a form a bare button would submit it
    node.type = type || "button"
    if (title) node.title = title
    if (alone && title) node.setAttribute("aria-label", title)
    node.disabled = !!disabled
    return on(node, onClick)
  }

  // chip is a small pick, and a pill of small caps: a .btn whose variant is
  // its state, primary while active and ghost while not. A tone says what it
  // is instead (danger, warning) and static makes it a fact rather than a
  // pick. Long names crop inside it, so pass the whole one as title to keep
  // it readable on hover.
  const CHIP_TONES = { bad: "danger", danger: "danger", warn: "warning", warning: "warning" }
  const chip = ({ label, active, tone, icon: glyph, title, onClick, ...opts } = {}) => {
    const node = el("button", "btn btn-chip", glyph ? icon(glyph) : null, el("span", "btn-chip-label", label))
    node.type = "button"
    node.classList.add(active ? "btn-primary" : CHIP_TONES[tone] ? "btn-" + CHIP_TONES[tone] : "btn-ghost")
    if (tone === "static") node.classList.add("static")
    classes(node, extra(opts))
    if (title) node.title = title
    return on(node, onClick)
  }

  // ─── tabs ──────────────────────────────────────────────────────
  // tab is one of them, on its own: toggle .active to move the selection.
  const tab = ({ label, active, title, onClick, ...opts } = {}) => {
    const node = el("button", "ui-tab", label === undefined ? null : label)
    node.type = "button"
    if (active) node.classList.add("active")
    classes(node, extra(opts))
    if (title) node.title = title
    return on(node, onClick)
  }

  // tabs owns its selection: items are strings or { value, label, title },
  // onChange fires once when a click changes it, select() changes it without
  // firing, value reads it. segmented makes it a window's sections, blocks
  // sharing the width, instead of a well of tabs.
  const tabs = ({ items = [], active, onChange, segmented, ...opts } = {}) => {
    const row = el("div", segmented ? "ui-segments" : "ui-tabs")
    classes(row, extra(opts))
    row.setAttribute("role", "tablist")
    const entries = items.map(item => (typeof item === "object" && item !== null ? item : { value: item }))
    let current = active === undefined ? entries[0] && entries[0].value : active
    const buttons = entries.map(entry => {
      const b = tab({ label: entry.label === undefined ? entry.value : entry.label, title: entry.title, class: entry.class })
      if (segmented) b.classList.replace("ui-tab", "ui-segment")
      b.setAttribute("role", "tab")
      b.dataset.value = entry.value
      return on(b, () => {
        if (entry.value === current) return
        row.select(entry.value)
        if (typeof onChange === "function") onChange(entry.value)
      })
    })
    buttons.forEach(b => row.append(b))
    row.select = value => {
      current = value
      buttons.forEach((b, i) => {
        const picked = entries[i].value === value
        b.classList.toggle("active", picked)
        b.setAttribute("aria-selected", picked ? "true" : "false")
      })
    }
    Object.defineProperty(row, "value", { get: () => current })
    row.select(current)
    return row
  }

  // ─── fields ────────────────────────────────────────────────────
  // States are attributes: required, aria-invalid, readonly.
  const input = ({ type = "text", placeholder, value, autocomplete, spellcheck, class: _c, className, ...rest } = {}) => {
    const node = el("input", "ui-input")
    classes(node, _c || className)
    node.type = type
    if (placeholder) node.placeholder = placeholder
    if (value !== undefined && value !== null) node.value = value
    if (autocomplete) node.setAttribute("autocomplete", autocomplete)
    if (spellcheck === false) node.spellcheck = false
    Object.assign(node, rest)
    return node
  }

  const textarea = ({ placeholder, value, rows, ...opts } = {}) => {
    const node = el("textarea", "ui-input")
    classes(node, extra(opts))
    if (placeholder) node.placeholder = placeholder
    if (rows) node.rows = rows
    if (value !== undefined) node.value = value
    return node
  }

  // field is the form line: the label shouted over the control, a note
  // under. The label wraps the control, so clicking the words focuses it.
  const field = ({ label, control, note, ...opts } = {}) => {
    const node = el("label", "ui-field", label ? text("span", "ui-overline", label) : null, control || null)
    if (note) node.append(text("span", "ui-field-note", note))
    classes(node, extra(opts))
    return node
  }

  // ─── checks and radios ─────────────────────────────────────────
  const box = (type, { checked, title, onChange, name } = {}) => {
    const node = el("input", type === "radio" ? "ui-check ui-radio" : "ui-check")
    node.type = type
    if (name) node.name = name
    if (title) node.title = title
    node.checked = !!checked
    node.addEventListener("change", () => {
      if (typeof onChange === "function") onChange(node.checked, node)
    })
    return node
  }

  // The box and its words as one label, so the words toggle it too; the box
  // is its .control (and .input).
  const labeled = (input, { label, note, ...opts }) => {
    const words = el("span", "ui-check-text", label === undefined ? null : label)
    if (note) words.append(text("span", "ui-check-note", note))
    const node = el("label", "ui-check-label", input, words)
    classes(node, extra(opts))
    node.input = input
    return node
  }

  // check is the bare box, or with a label (or a note) the box and its words.
  const check = (opts = {}) => {
    const node = box("checkbox", opts)
    if (opts.label === undefined && !opts.note) return classes(node, extra(opts))
    return labeled(node, opts)
  }

  // radio is check's round sibling, for one of several; radios builds the group.
  const radio = (opts = {}) => {
    const node = box("radio", opts)
    if (opts.label === undefined && !opts.note) return classes(node, extra(opts))
    return labeled(node, opts)
  }

  // radios picks one of several: options are strings or { value, label,
  // note, title }. value reads and sets the pick. label is a caption before
  // them; collapse keeps the group one line, the caption and the pick, until
  // it is clicked, and folds it back once one is chosen.
  let radiosSerial = 0
  const radios = ({ name, options = [], value, onChange, label, collapse, ...opts } = {}) => {
    const group = el("div", "ui-radios")
    if (collapse) group.classList.add("collapse")
    classes(group, extra(opts))
    group.setAttribute("role", "radiogroup")
    // a name of its own, so two groups never share their pick
    const shared = name || "ui-radios-" + radiosSerial++
    if (label) {
      group.setAttribute("aria-label", label)
      group.append(text("span", "ui-overline", label))
    }
    const inputs = new Map()
    const names = new Map()
    const pick = collapse ? el("button", "ui-radios-value ui-overline") : null
    const show = v => {
      if (pick) pick.textContent = v === undefined ? "" : names.has(v) ? names.get(v) : v
    }
    if (pick) {
      pick.type = "button"
      pick.addEventListener("click", () => {
        group.classList.add("open")
        const on = [...inputs.values()].find(i => i.checked) || inputs.values().next().value
        if (on) on.focus()
      })
      // choosing folds it back, the pick already chosen too (it fires no change)
      group.addEventListener("click", e => {
        if (e.target instanceof HTMLInputElement) group.classList.remove("open")
      })
      group.addEventListener("keydown", e => {
        if (e.key !== "Escape" || !group.classList.contains("open")) return
        group.classList.remove("open")
        pick.focus()
      })
      group.append(pick)
    }
    for (const option of options) {
      const entry = typeof option === "object" && option !== null ? option : { value: option }
      const words = entry.label === undefined ? entry.value : entry.label
      const node = radio({
        name: shared,
        label: words,
        note: entry.note,
        title: entry.title,
        checked: entry.value === value,
        onChange: () => {
          show(entry.value)
          if (typeof onChange === "function") onChange(entry.value)
        },
      })
      inputs.set(entry.value, node.input)
      names.set(entry.value, words)
      group.append(node)
    }
    show(value)
    Object.defineProperty(group, "value", {
      get: () => {
        for (const [v, i] of inputs) if (i.checked) return v
        return undefined
      },
      set: v => {
        for (const [k, i] of inputs) i.checked = k === v
        show(v)
      },
    })
    return group
  }

  // switch is a setting that is on or off, and where it stands.
  const toggle = ({ label, note, checked, onChange, ...opts } = {}) => {
    const input = el("input")
    input.type = "checkbox"
    input.checked = !!checked
    input.addEventListener("change", () => {
      if (typeof onChange === "function") onChange(input.checked, input)
    })
    const words = el("span", "ui-switch-text", label || null)
    if (note) words.append(el("span", "ui-switch-note", note))
    const node = el("label", "ui-switch", input, el("span", "ui-switch-track"), words)
    classes(node, extra(opts))
    node.input = input
    return node
  }

  // ─── folding ───────────────────────────────────────────────────
  // details folds content away under a summary: text, or parts in a line (a
  // title, then a count or a badge). fold is the small one under content;
  // sticky keeps the summary at the top while the content scrolls under it,
  // { top } that far down, under something else that sticks.
  const details = ({ summary, open, fold, sticky: stuck, ...opts } = {}, ...children) => {
    const node = el("details", fold ? "ui-fold" : "ui-details")
    if (stuck) node.classList.add("sticky")
    classes(node, extra(opts))
    const head = el("summary", "")
    if (stuck && typeof stuck === "object" && stuck.top !== undefined) head.style.setProperty("--sticky-top", length(stuck.top))
    if (Array.isArray(summary) || summary instanceof Node) head.append(el("span", "ui-summary", summary))
    else head.textContent = summary === undefined || summary === null ? "" : String(summary)
    node.append(head)
    if (open) node.open = true
    return put(node, children)
  }

  // sticky keeps an element in view as the rest scrolls: at the top (the
  // default, at 0), the bottom, or both, each at an offset. Only that: its
  // background is the napp's.
  const sticky = (node, { top, bottom } = {}) => {
    const low = bottom !== undefined && bottom !== null
    const high = top !== undefined && top !== null
    node.classList.add("ui-sticky")
    node.classList.toggle("bottom", low)
    node.classList.toggle("top", low && high)
    if (high) node.style.setProperty("--sticky-top", length(top))
    if (low) node.style.setProperty("--sticky-bottom", length(bottom))
    return node
  }

  // ─── lists ─────────────────────────────────────────────────────
  // itemList is the column, item one row of it: its label, cut with an
  // ellipsis, then the controls given. A label that is an element brings its
  // own voice (code(url) for an id or a url); a row that failed takes tone
  // "bad" (or "danger").
  const itemList = className => classes(el("div", "ui-items"), className)

  const item = ({ label, title, tone, mono, ...opts } = {}, ...controls) => {
    const row = el("div", "ui-item")
    if (tone) row.classList.add(TONES[tone] || tone)
    classes(row, extra(opts))
    const name = label instanceof Node ? label : el("span", "", label === undefined ? null : label)
    name.classList.add("ui-item-label")
    if (mono) name.classList.add("mono")
    name.title = title || name.textContent || ""
    row.append(name)
    return put(row, controls)
  }

  // rows that open: rowList() holds them, row(list, …summary) is a line that
  // opens to whatever is appended to it. A list's rows share a name, so one
  // is open at a time, and while it is the list shows only it.
  let rowsSerial = 0
  const row = (list, ...summary) => {
    const node = el("details", "ui-row")
    node.name = list.dataset.rows || ""
    node.append(el("summary", "", summary))
    list.append(node)
    return node
  }

  const rowList = className => {
    const list = classes(el("div", "ui-rows"), className)
    list.dataset.rows = "ui-rows-" + rowsSerial++
    list.row = (...summary) => row(list, ...summary)
    return list
  }

  // addControl is the deliberate way to add to a list: a button, and once
  // clicked a field and an add in its place. onAdd returns the error to show,
  // or nothing: then the field clears and stays, for the next one.
  const addControl = ({ label, placeholder, onAdd, ...opts } = {}) => {
    const node = classes(el("div", "ui-add"), extra(opts))
    const error = el("div", "ui-add-error")
    const open = () => {
      const entry = input({ placeholder, className: "ui-add-input", spellcheck: false, autocomplete: "off" })
      // an id, a url: as typed, on a phone too
      entry.setAttribute("autocapitalize", "off")
      entry.addEventListener("input", () => (error.textContent = ""))
      const form = el("form", "ui-add-form", entry, button({ label: "add", variant: "outline", type: "submit" }))
      form.addEventListener("submit", e => {
        e.preventDefault()
        const value = entry.value.trim()
        if (value) {
          const said = typeof onAdd === "function" ? onAdd(value) : undefined
          error.textContent = typeof said === "string" ? said : ""
          if (typeof said !== "string") entry.value = ""
        }
        entry.focus()
      })
      node.replaceChildren(form, error)
      entry.focus()
    }
    node.append(button({ label, variant: "outline", icon: "plus", onClick: open }))
    return node
  }

  // list is a column of rows that it keeps: label(item) is a row's text,
  // controls(item, row) what sits at its end, with the add control and the
  // empty line under. add() and delete() change the items, which are keyed
  // by identity; items reads them, and assigned replaces them all.
  const list = ({ items = [], label, title, mono, controls, add, empty: none, ...opts } = {}) => {
    const node = classes(el("div", "ui-list"), extra(opts))
    const rows = itemList()
    const entries = new Map()
    const nothing = text("div", "ui-empty", none || "")
    const settle = () => (nothing.hidden = !none || entries.size > 0)
    const put1 = entry => {
      const r = item({ label: label ? label(entry) : String(entry), title: title ? title(entry) : undefined, mono })
      if (controls) put(r, [controls(entry, r)])
      if (entries.has(entry)) entries.get(entry).remove()
      entries.set(entry, r)
      rows.append(r)
      settle()
      return r
    }
    node.add = put1
    node.delete = entry => {
      if (entries.has(entry)) entries.get(entry).remove()
      entries.delete(entry)
      settle()
    }
    Object.defineProperty(node, "items", {
      get: () => [...entries.keys()],
      set: next => {
        for (const r of entries.values()) r.remove()
        entries.clear()
        for (const entry of next || []) put1(entry)
        settle()
      },
    })
    node.append(rows, nothing)
    if (add) node.append(addControl(add))
    node.items = items
    return node
  }

  // ─── facts ─────────────────────────────────────────────────────
  const empty = (value, className) => classes(text("div", "ui-empty", value), className)

  // notice is a line to heed: warn (the default), danger, good or info.
  const notice = (message, { tone, icon: glyph, ...opts } = {}) => {
    const which = TONES[tone] || tone || "warn"
    const fallback = { danger: "x", info: "info", good: "check" }[which] || "warning"
    const mark = glyph === null ? null : icon(glyph || fallback)
    const node = el("div", "ui-notice " + which, mark, el("span", "", message))
    classes(node, extra(opts))
    return node
  }

  // badge is a fact in small caps; a tone (good, danger, warn, brand,
  // solid) colors it.
  const badge = (label, { tone, title, ...opts } = {}) => {
    const node = text("span", "ui-badge", label)
    if (tone) node.classList.add(TONES[tone] || tone)
    if (title) node.title = title
    classes(node, extra(opts))
    return node
  }

  // overline is the voice that names a group, as a caption in a line.
  const overline = (value, className) => classes(text("span", "ui-overline", value), className)

  const kbd = value => text("kbd", "ui-kbd", value)
  const code = (value, className) => classes(text("code", "ui-code", value), className)
  const codeBlock = (value, className) => classes(text("pre", "ui-code-block", value), className)

  const links = (...children) => el("nav", "ui-links", children)

  // ring is a word set round a circle, slowly turning: the label's voice
  // bent into a stamp. Its size follows the font-size it sits in; its place
  // is the napp's. The word goes round once, its letters spread to fill the
  // circle, and a dot sits in the seam.
  let ringSerial = 0
  const ring = (word, className) => {
    // drawn at 100 units per em: a path a few units long is measured short,
    // and the glyphs past its "end" dropped
    const em = 100
    const r = 1.6 * em
    const size = 2 * (r + 0.8 * em)
    const c = size / 2
    const around = 2 * Math.PI * r
    const n = word.length
    const glyph = 0.62 * em
    const dot = 0.3 * em
    const gap = (around - n * glyph - dot) / (n + 1)
    const span = n * glyph + (n - 1) * gap
    const svg = document.createElementNS(SVG, "svg")
    svg.setAttribute("viewBox", `0 0 ${size} ${size}`)
    svg.setAttribute("width", `${size / em}em`)
    svg.setAttribute("height", `${size / em}em`)
    svg.setAttribute("aria-hidden", "true")
    svg.classList.add("ui-ring")
    classes(svg, className)
    const id = "ui-ring-" + ringSerial++
    const defs = document.createElementNS(SVG, "defs")
    const path = document.createElementNS(SVG, "path")
    path.id = id
    // from the top, clockwise, so the word reads the right way up outside it
    path.setAttribute("d", `M${c} ${c - r}a${r} ${r} 0 1 1 0 ${2 * r}a${r} ${r} 0 1 1 0 ${-2 * r}`)
    defs.append(path)
    const run = (content, attrs) => {
      const t = document.createElementNS(SVG, "text")
      t.setAttribute("font-size", String(em))
      const along = document.createElementNS(SVG, "textPath")
      along.setAttribute("href", "#" + id)
      for (const [k, v] of Object.entries(attrs)) along.setAttribute(k, v)
      along.textContent = content
      t.append(along)
      return t
    }
    const letters = run(word, { textLength: span.toFixed(1), lengthAdjust: "spacing" })
    const seam = run("·", { startOffset: ((span + around) / 2).toFixed(1) })
    seam.setAttribute("text-anchor", "middle")
    svg.append(defs, letters, seam)
    return svg
  }

  // appIcon holds its space until the image lands, and with fade it eases
  // in. .img is the <img>, put in the plate once there is a picture for it:
  // given here, or set through .img later. Until then the plate stands alone.
  const appIcon = ({ src, size = "m", fade, alt = "", ...opts } = {}) => {
    const node = el("span", "ui-app-icon ui-app-icon-" + size)
    if (fade) node.classList.add("fade")
    classes(node, extra(opts))
    const img = el("img")
    img.alt = alt
    img.addEventListener("load", () => img.classList.add("loaded"))
    img.addEventListener("error", () => img.classList.remove("loaded"))
    Object.defineProperty(node, "img", {
      get: () => {
        if (img.parentNode !== node) node.append(img)
        return img
      },
    })
    if (src) node.img.src = src
    return node
  }

  // busy breathes anything until it is done; busy(node, false) stops it.
  const busy = (node, on = true) => {
    node.classList.toggle("ui-busy", !!on)
    if (on) node.setAttribute("aria-busy", "true")
    else node.removeAttribute("aria-busy")
    return node
  }

  // spinner is a ring 1em across, in the text's own color: set a font-size to
  // make it bigger, as a window does.
  const spinner = className => {
    const node = el("span", "ui-spinner")
    classes(node, className)
    node.setAttribute("role", "status")
    node.setAttribute("aria-label", "loading")
    return node
  }

  // meter says how far along something is: value 0…1, a tone for the rest.
  const meter = ({ value = 0, tone, ...opts } = {}) => {
    const node = el("div", "ui-meter")
    if (tone) node.classList.add(TONES[tone] || tone)
    classes(node, extra(opts))
    node.value = value
    const set = v => node.style.setProperty("--v-value", Math.max(0, Math.min(1, v)) * 100 + "%")
    set(value)
    node.set = set
    return node
  }

  // ─── people ────────────────────────────────────────────────────
  // author is a person, the same wherever one is named: the name, a picture
  // before it when asked for, and after it, when asked for, the petname the
  // viewer gave them and a check for someone followed. The name is the
  // profile's name, else the petname, else the display name, else the short
  // npub. It shows at once with what is known and fills itself in when the
  // profile lands: every element for a pubkey is filed under it, so one
  // arrival repaints exactly those. Lookups go out a few at a time.
  const authorsConfig = {}
  const authorInfo = new Map() // what a profile gave
  const authorAsked = new Map() // when a lookup last went out
  const authorEls = new Map() // pubkey → the elements to repaint
  const authorOpts = new WeakMap()
  let authorPetnames = new Map() // the viewer's follows: pubkey → petname, "" for none
  const authorQueue = []
  let authorTimer = 0
  const AUTHOR_PER_TICK = 32
  const AUTHOR_TICK_MS = 120
  const AUTHOR_ASK_AGAIN = 5 * 60e3

  const authorGone =
    typeof FinalizationRegistry === "function"
      ? new FinalizationRegistry(([pubkey, ref]) => {
          const set = authorEls.get(pubkey)
          if (!set) return
          set.delete(ref)
          if (!set.size) authorEls.delete(pubkey)
        })
      : null

  // what loadNostrUser gives, or a kind 0's content, or a search row
  const authorFrom = user => {
    const u = user && typeof user === "object" ? user : {}
    const m = u.metadata && typeof u.metadata === "object" ? u.metadata : u
    const words = v => (typeof v === "string" ? v.replace(/\s+/g, " ").trim() : "")
    const picture = words(u.image) || words(m.picture)
    return {
      name: words(m.name),
      displayName: words(m.display_name),
      picture: /^(https?:\/\/|data:image\/)/i.test(picture) ? picture : "",
    }
  }

  const authorNpub = pubkey => {
    try {
      return (authorsConfig.npub && authorsConfig.npub(pubkey)) || pubkey
    } catch {
      return pubkey
    }
  }

  const authorShort = pubkey => {
    const npub = authorNpub(pubkey)
    return npub === pubkey ? pubkey.slice(0, 8) + "…" : npub.slice(0, 8) + "…" + npub.slice(-4)
  }

  const authorName = pubkey => {
    const info = authorInfo.get(pubkey)
    return (info && info.name) || authorPetnames.get(pubkey) || (info && info.displayName) || authorShort(pubkey)
  }

  const authorPaint = node => {
    const pubkey = node.dataset.pubkey || ""
    const opts = authorOpts.get(node) || {}
    const shown = authorName(pubkey)
    const name = node.querySelector(".ui-author-name")
    const said = (opts.prefix || "") + shown
    if (name && name.textContent !== said) name.textContent = said
    node.title = authorNpub(pubkey)
    // the picture goes in once there is one and out again if it fails: the
    // plate alone holds its place, never an empty or a broken image
    const info = authorInfo.get(pubkey)
    const picture = info && info.picture
    const plate = node.querySelector(".ui-app-icon")
    if (plate && picture && plate.dataset.failed !== picture) {
      const img = plate.img
      if (img.getAttribute("src") !== picture) {
        img.referrerPolicy = "no-referrer"
        img.loading = "lazy"
        img.onerror = () => {
          plate.dataset.failed = img.getAttribute("src") || ""
          img.remove()
        }
        img.src = picture
      }
    }
    const pet = node.querySelector(".ui-author-pet")
    if (pet) {
      const petname = authorPetnames.get(pubkey) || ""
      pet.textContent = petname !== shown ? petname : ""
      pet.hidden = !pet.textContent
    }
    const follows = node.querySelector(".ui-author-follows")
    if (follows) follows.hidden = !authorPetnames.has(pubkey)
  }

  const authorRepaint = pubkey => {
    for (const ref of authorEls.get(pubkey) || []) {
      const node = ref.deref ? ref.deref() : ref
      if (node) authorPaint(node)
    }
  }

  const authorTick = () => {
    authorTimer = 0
    for (const pubkey of authorQueue.splice(0, AUTHOR_PER_TICK)) {
      Promise.resolve()
        .then(() => authorsConfig.load(pubkey))
        .then(user => authors.set(pubkey, user))
        .catch(() => {})
    }
    if (authorQueue.length) authorTimer = setTimeout(authorTick, AUTHOR_TICK_MS)
  }

  const authorAsk = pubkey => {
    if (!authorsConfig.load || authorInfo.has(pubkey)) return
    const asked = authorAsked.get(pubkey)
    if (asked && Date.now() - asked < AUTHOR_ASK_AGAIN) return
    authorAsked.set(pubkey, Date.now())
    authorQueue.push(pubkey)
    if (!authorTimer) authorTimer = setTimeout(authorTick, 0)
  }

  const authors = {
    // the kit's ways in: how to load a profile, encode an npub, open a
    // profile and search for people. Set already for napps; load: null for
    // a napp that looks profiles up itself and hands them over with set().
    use(config) {
      Object.assign(authorsConfig, config)
    },
    // a profile in hand: what loadNostrUser gives, or a kind 0's content.
    // An empty one still counts as known, but never over a fuller one.
    set(pubkey, user) {
      const info = authorFrom(user)
      if (!info.name && !info.displayName && !info.picture && authorInfo.has(pubkey)) return
      authorInfo.set(pubkey, info)
      authorRepaint(pubkey)
    },
    // the viewer's follow list, a kind 3 (null: logged out): its petnames
    // name people the profile does not, and its p tags are who is followed
    viewer(follows) {
      authorPetnames = new Map()
      const tags = follows && Array.isArray(follows.tags) ? follows.tags : []
      for (const t of tags) {
        if (!Array.isArray(t) || t[0] !== "p" || typeof t[1] !== "string") continue
        const pubkey = t[1].toLowerCase()
        if (!authorPetnames.has(pubkey)) authorPetnames.set(pubkey, typeof t[3] === "string" ? t[3].trim() : "")
      }
      for (const pubkey of authorEls.keys()) authorRepaint(pubkey)
    },
    // the name as text, as known right now: for a toast, a title, a filter
    name(pubkey) {
      authorAsk(pubkey)
      return authorName(pubkey)
    },
    follows(pubkey) {
      return authorPetnames.has(pubkey)
    },
  }

  const followMark = () => {
    const mark = el("span", "ui-author-follows", icon("check"))
    mark.title = "you follow them"
    return mark
  }

  const author = (pubkey, { picture, petname, follows, prefix, link, onClick, ...opts } = {}) => {
    const linked = link !== false
    const node = el(linked ? "a" : "span", "ui-author")
    classes(node, extra(opts))
    node.dataset.pubkey = pubkey
    if (linked) {
      node.href = "#"
      node.addEventListener("click", e => {
        e.preventDefault()
        if (typeof onClick === "function") onClick(pubkey, e)
        else if (authorsConfig.open) authorsConfig.open(pubkey)
      })
    }
    // the plate now, its picture when there is one (authorPaint)
    if (picture) node.append(appIcon({ size: picture === true ? "xs" : picture }))
    node.append(el("span", "ui-author-name"))
    if (petname) node.append(code("", "ui-author-pet"))
    if (follows) node.append(followMark())
    authorOpts.set(node, { prefix })
    const ref = typeof WeakRef === "function" ? new WeakRef(node) : node
    if (!authorEls.has(pubkey)) authorEls.set(pubkey, new Set())
    authorEls.get(pubkey).add(ref)
    if (authorGone) authorGone.register(node, [pubkey, ref])
    authorPaint(node)
    authorAsk(pubkey)
    return node
  }

  // authorPicker: people to pick from as one types. With a trigger ("@"),
  // a text box: the trigger after a space or at a line's start opens a list
  // at it, and what follows filters it. Without one, a who field: its value
  // is the query (an npub, nprofile or hex is that one person). The focus
  // stays in the text: ↑/↓ move, Enter or Tab picks, Esc shuts it until the
  // word is left. onPick says what the pick puts in the text. The list is a
  // top-layer popover, so no container clips it. Known profiles only,
  // unless remote asks for relay search too — then a second pass, once
  // typing pauses, brings what that adds.
  const CARET_STYLE = [
    "boxSizing", "width", "paddingTop", "paddingRight", "paddingBottom", "paddingLeft",
    "borderTopWidth", "borderRightWidth", "borderBottomWidth", "borderLeftWidth", "borderStyle",
    "fontFamily", "fontSize", "fontWeight", "fontStyle", "fontVariant", "fontStretch",
    "lineHeight", "letterSpacing", "wordSpacing", "textTransform", "textIndent", "tabSize",
  ]

  // where a character of a text box sits on screen: a hidden copy of the box
  // with the text up to it, and a marker there
  const caretPoint = (target, index) => {
    const cs = getComputedStyle(target)
    const copy = document.createElement("div")
    for (const prop of CARET_STYLE) copy.style[prop] = cs[prop]
    Object.assign(copy.style, {
      position: "absolute",
      visibility: "hidden",
      top: "0",
      left: "-9999px",
      height: "auto",
      overflow: "hidden",
      whiteSpace: target instanceof HTMLTextAreaElement ? "pre-wrap" : "pre",
      overflowWrap: "break-word",
    })
    copy.textContent = target.value.slice(0, index)
    const mark = document.createElement("span")
    mark.textContent = target.value.slice(index).split(/\s/)[0] || "."
    copy.append(mark)
    document.body.append(copy)
    const rect = target.getBoundingClientRect()
    const top = rect.top + mark.offsetTop - target.scrollTop
    const left = rect.left + mark.offsetLeft - target.scrollLeft
    const line = parseFloat(cs.lineHeight) || parseFloat(cs.fontSize) * 1.2
    copy.remove()
    return { left, top, bottom: top + line }
  }

  let pickerSerial = 0
  const authorPicker = (target, opts = {}) => {
    const limit = Math.max(1, opts.limit || 8)
    const trigger = opts.trigger || ""
    const remote = opts.remote === true
    const nip05 = opts.nip05 === true
    const listbox = classes(el("div", "ui-author-picker"), extra(opts))
    listbox.id = "ui-author-picker-" + pickerSerial++
    listbox.setAttribute("role", "listbox")
    const layered = typeof listbox.showPopover === "function"
    if (layered) listbox.popover = "manual"
    listbox.hidden = !layered
    let rows = []
    let active = 0
    let query = null
    let ask = 0 // the current question; an answer to an older one is dropped
    let settled = 0 // the question whose second pass has answered
    let localTimer = 0
    let remoteTimer = 0
    let shut = -1 // Esc: the trigger at this position stays shut while typed
    let quiet = false // our own input event, after a pick

    const isOpen = () => (layered ? listbox.matches(":popover-open") : !listbox.hidden)
    const show = () => {
      if (!listbox.isConnected) document.body.append(listbox)
      if (layered && !isOpen()) listbox.showPopover()
      if (!layered) listbox.hidden = false
      place()
    }
    const close = () => {
      ask++
      clearTimeout(localTimer)
      clearTimeout(remoteTimer)
      query = null
      rows = []
      if (isOpen()) {
        if (layered) listbox.hidePopover()
        else listbox.hidden = true
      }
      target.removeAttribute("aria-activedescendant")
    }

    const currentQuery = () => {
      const value = target.value
      const caret = target.selectionStart === null ? value.length : target.selectionStart
      if (target.selectionEnd !== caret) return null
      if (!trigger) return { start: 0, end: value.length, text: value.trim() }
      const before = value.slice(0, caret)
      const at = before.lastIndexOf(trigger)
      if (at < 0 || (at > 0 && !/\s/.test(before[at - 1]))) return null
      const typed = before.slice(at + trigger.length)
      if (/\s/.test(typed) || typed.length > 64) return null
      return { start: at, end: caret, text: typed }
    }

    const refresh = () => {
      if (quiet) return
      const q = currentQuery()
      if (!q) shut = -1
      if (!q || q.start === shut || document.activeElement !== target) return close()
      // nothing is looked up before a character follows the trigger: the
      // list opens at it with just that said (a field waits for one)
      if (!q.text) {
        close()
        if (!trigger) return
        query = q
        listbox.replaceChildren(empty("type to search"))
        show()
        return
      }
      query = q
      const n = ++ask
      clearTimeout(localTimer)
      clearTimeout(remoteTimer)
      localTimer = setTimeout(() => lookup(q.text, false, n), 60)
      if (remote || nip05) remoteTimer = setTimeout(() => lookup(q.text, true, n), 450)
    }

    // first pass: known profiles; second (when asked for): what relay search
    // and name@domain lookups add, replacing the first's rows
    const lookup = async (typed, second, n) => {
      const search = authorsConfig.search
      if (!search) return
      if (second && !nip05 && rows.length >= limit) return
      let found = []
      try {
        found = await search(typed, second ? { limit, remote, nip05 } : { limit })
      } catch {}
      if (n !== ask || (!second && settled === n)) return
      if (second) settled = n
      const picked = rows[active] && rows[active].pubkey
      rows = (Array.isArray(found) ? found : [])
        .filter(r => r && typeof r.pubkey === "string" && /^[0-9a-f]{64}$/.test(r.pubkey))
        .slice(0, limit)
      for (const r of rows) authors.set(r.pubkey, r)
      active = Math.max(0, rows.findIndex(r => r.pubkey === picked))
      if (rows.length) render()
      else if (second || !(remote || nip05)) {
        listbox.replaceChildren(empty("no one found"))
        show()
      }
    }

    const render = () => {
      listbox.replaceChildren(
        ...rows.map((r, i) => {
          const line = el("div", "ui-author-pick", author(r.pubkey, { picture: "s", link: false }))
          line.id = listbox.id + "-" + i
          line.setAttribute("role", "option")
          if (r.nip05) line.append(code(r.nip05, "ui-author-pick-nip05"))
          if (r.follows) line.append(followMark())
          // mousedown, not click: the text keeps its focus and its caret
          line.addEventListener("mousedown", e => {
            e.preventDefault()
            pick(i)
          })
          line.addEventListener("mousemove", () => {
            if (active !== i) setActive(i)
          })
          return line
        })
      )
      setActive(active)
      show()
    }

    const setActive = i => {
      active = i
      listbox.querySelectorAll(".ui-author-pick").forEach((line, j) => {
        line.classList.toggle("active", j === i)
        line.setAttribute("aria-selected", String(j === i))
        if (j === i) {
          line.scrollIntoView({ block: "nearest" })
          target.setAttribute("aria-activedescendant", line.id)
        }
      })
    }

    const pick = i => {
      const r = rows[i]
      const q = query
      close()
      if (!r || !q || typeof opts.onPick !== "function") return
      const out = opts.onPick(r)
      if (typeof out !== "string") return
      if (trigger) target.setRangeText(out, q.start, q.end, "end")
      else target.value = out
      quiet = true
      target.dispatchEvent(new Event("input", { bubbles: true }))
      quiet = false
    }

    // under the trigger (or the field), above it when the room below runs
    // out, and never past the window's edges
    const place = () => {
      const rect = target.getBoundingClientRect()
      let left = rect.left
      let below = rect.bottom
      let above = rect.top
      if (trigger && query) {
        const at = caretPoint(target, query.start)
        left = at.left
        below = at.bottom
        above = at.top
      }
      const w = listbox.offsetWidth
      const h = listbox.offsetHeight
      const top = below + h > window.innerHeight - 4 && above - h >= 4 ? above - h : below
      listbox.style.left = Math.max(4, Math.min(left, window.innerWidth - w - 4)) + "px"
      listbox.style.top = Math.max(4, top) + "px"
    }

    const onKey = e => {
      if (!isOpen() || e.isComposing) return
      const n = rows.length
      if (e.key === "Escape") {
        if (trigger && query) shut = query.start
        close()
      } else if (!n) {
        return
      } else if (e.key === "ArrowDown") setActive((active + 1) % n)
      else if (e.key === "ArrowUp") setActive((active - 1 + n) % n)
      else if ((e.key === "Enter" || e.key === "Tab") && !e.metaKey && !e.ctrlKey && !e.altKey) pick(active)
      else return
      e.preventDefault()
      e.stopImmediatePropagation()
    }

    const onScroll = () => {
      if (isOpen()) place()
    }
    const onBlur = () => close()
    // the caret moved without typing: in or out of a trigger's word
    const onKeyUp = e => {
      if (/^(ArrowLeft|ArrowRight|Home|End)$/.test(e.key)) refresh()
    }
    target.addEventListener("input", refresh)
    target.addEventListener("keyup", onKeyUp)
    target.addEventListener("click", refresh)
    target.addEventListener("focus", refresh)
    target.addEventListener("keydown", onKey, true)
    target.addEventListener("blur", onBlur)
    target.addEventListener("scroll", onScroll)
    window.addEventListener("resize", onScroll)
    target.setAttribute("aria-controls", listbox.id)

    return {
      close,
      detach() {
        close()
        target.removeEventListener("input", refresh)
        target.removeEventListener("keyup", onKeyUp)
        target.removeEventListener("click", refresh)
        target.removeEventListener("focus", refresh)
        target.removeEventListener("keydown", onKey, true)
        target.removeEventListener("blur", onBlur)
        target.removeEventListener("scroll", onScroll)
        window.removeEventListener("resize", onScroll)
        target.removeAttribute("aria-controls")
        listbox.remove()
      },
    }
  }

  // The launcher's ways in, looked up when used: bridge.js lands after this
  // script and fills in the rest of window.napp.
  const bridge = () => window.napp || {}

  // A typed npub, nprofile or hex is that one person.
  const directPubkey = term => {
    const t = term.trim().replace(/^nostr:/, "")
    if (/^[0-9a-f]{64}$/i.test(t)) return t.toLowerCase()
    try {
      const decoded = bridge().nip19.decode(t)
      if (decoded.type === "npub") return decoded.data
      if (decoded.type === "nprofile") return decoded.data.pubkey
    } catch {}
    return null
  }

  // A search row as authorPicker takes it: the profile's fields flat.
  const picked = (pubkey, user) => {
    const u = user && typeof user === "object" ? user : {}
    const m = u.metadata && typeof u.metadata === "object" ? u.metadata : u
    return {
      pubkey,
      name: m.name || "",
      display_name: m.display_name || "",
      picture: u.image || m.picture || "",
      nip05: m.nip05 || "",
      follows: authors.follows(pubkey),
    }
  }

  authors.use({
    load: pubkey => bridge().utils.loadNostrUser(pubkey),
    npub: pubkey => bridge().nip19.npubEncode(pubkey),
    open: pubkey => {
      const napp = bridge()
      if (napp.action) napp.action("profile", pubkey).catch(() => {})
    },
    // The launcher's search: profiles it has indexed, then with remote the
    // user's search relays. It checks no nip-05, so where one is asked to be
    // checked a row shows none, and a typed name@domain is resolved instead.
    search: async (term, { limit = 8, remote, nip05 } = {}) => {
      const utils = bridge().utils
      if (!utils) return []
      const one = directPubkey(term) || (nip05 && /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(term.trim()) ? term.trim() : null)
      if (one) {
        const user = await utils.loadNostrUser(one).catch(() => null)
        const pubkey = (user && user.pubkey) || (/^[0-9a-f]{64}$/.test(one) ? one : null)
        return pubkey ? [picked(pubkey, user)] : []
      }
      const seen = new Set()
      const rows = []
      const take = found => {
        for (const r of Array.isArray(found) ? found : []) {
          if (!r || typeof r.pubkey !== "string" || seen.has(r.pubkey)) continue
          seen.add(r.pubkey)
          rows.push(picked(r.pubkey, r))
        }
      }
      take(await utils.searchUserLocal(term).catch(() => []))
      if (remote && rows.length < limit) take(await utils.searchUser(term).catch(() => []))
      // followed people first, then as the search ranked them
      rows.sort((a, b) => Number(b.follows) - Number(a.follows))
      if (nip05) for (const r of rows) r.nip05 = ""
      return rows.slice(0, limit)
    },
  })

  // section is a named group of rows: the name shouted, a hairline under it.
  const section = (label, ...children) => el("div", "ui-section", text("div", "ui-overline", label), children)

  // header is the first line of a screen: a name, and what acts on it.
  const header = (...children) => el("div", "ui-header", children)

  const ui = {
    // layout
    el,
    stack: (...children) => el("div", "ui-stack", children),
    bar: (...children) => el("div", "ui-bar", children),
    /** flow spaces text-like content as it reads; its children carry no margins. */
    flow: (...children) => el("div", "ui-flow", children),
    card: (...children) => el("div", "ui-card", children),
    plate: (...children) => el("div", "ui-plate", children),
    header,
    section,
    /** grow is a row's filler: whatever follows it is pushed to the far end. */
    grow: () => el("span", "ui-grow"),
    divider: () => el("hr", "ui-divider"),
    sticky,
    // type
    title: value => text("h2", "ui-title", value),
    display: value => text("h1", "ui-heading-xxl", value),
    heading: value => text("h3", "ui-heading", value),
    /** The voice that names a group: small caps, tracked, grey. */
    label: value => text("div", "ui-overline", value),
    overline,
    caption: value => text("span", "ui-caption", value),
    hint: value => text("span", "ui-hint", value),
    mono: value => text("span", "ui-mono", value),
    kbd,
    code,
    codeBlock,
    empty,
    meter,
    ring,
    // controls
    button,
    chip,
    tab,
    tabs,
    input,
    textarea,
    field,
    check,
    radio,
    radios,
    switch: toggle,
    // content
    details,
    itemList,
    item,
    rowList,
    row,
    addControl,
    list,
    notice,
    badge,
    icon,
    icons,
    iconInk,
    appIcon,
    author,
    authors,
    authorPicker,
    links,
    busy,
    spinner,
    /** The launcher's face is in the kit already. */
    fonts: () => Promise.resolve(),
  }

  // bridge.js fills in the rest of window.napp and keeps what is here.
  window.napp = Object.assign(window.napp || {}, { ui })
})()
