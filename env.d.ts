// ── Nostr core types (same as from @nostr/tools) ──────────────────────────
type NostrEvent = {
  id: string
  pubkey: string
  created_at: number
  kind: number
  tags: string[][]
  content: string
  sig: string
}

type EventTemplate = {
  kind: number
  tags: string[][]
  content: string
  created_at: number
}

interface VerifiedEvent extends NostrEvent {
  [verifiedSymbol]: true
}

declare const verifiedSymbol: unique symbol

// ── NIP-07 signer ────────────────────────────────────────────────────────
type NostrNip04 = {
  encrypt(pubkey: string, plaintext: string): Promise<string>
  decrypt(pubkey: string, ciphertext: string): Promise<string>
}

type NostrNip44 = {
  encrypt(pubkey: string, plaintext: string): Promise<string>
  decrypt(pubkey: string, ciphertext: string): Promise<string>
}

type NostrSigner = {
  getPublicKey(): Promise<string>
  signEvent(evt: EventTemplate): Promise<VerifiedEvent>
  nip04: NostrNip04
  nip44: NostrNip44
}

// ── Event store (NIP-DB) ─────────────────────────────────────────────────
type NostrDB = {
  add(event: NostrEvent): Promise<void>
  query(filters: unknown): Promise<NostrEvent[]>
  count(filters: unknown): Promise<number>
  event(id: string): Promise<NostrEvent | undefined>
  remove(ids: string[]): Promise<string[]>
  replaceable(kind: number, author: string, identifier?: string): Promise<NostrEvent | undefined>
  // Every event saved to the store from now on that matches filters, one by
  // one. What is already stored comes from query(). Leaving the loop ends it.
  subscribe(filters: NostrFilter | NostrFilter[]): AsyncGenerator<NostrEvent>
  supports(): string[]
}

// ── NIP-51 list helpers ──────────────────────────────────────────────────
type RelayItem = {
  url: string
  read: boolean
  write: boolean
}

type ListResult<I> = {
  event: NostrEvent | null
  items: I[]
}

// ── NIP-51 list item shapes (mirror @nostr/gadgets/lists) ─────────────────
type AddressPointer = {
  identifier: string
  pubkey: string
  kind: number
  relays: string[]
}

type EventPointer = {
  id: string
  kind?: number
  relays?: string[]
  author?: string
}

type Emoji = {
  shortcode: string
  url: string
}

type SimpleGroupItem = {
  groupId: string
  relay: string
  name?: string
}

type ResolvedSet<I> = {
  pointer: AddressPointer
  event: NostrEvent | null
  items: I[]
  title: string
  image?: string
  description?: string
}

// ── Addressable set helpers ──────────────────────────────────────────────
type SetResult<I> = {
  event: NostrEvent | null
  items: I[]
}

// ── Profile metadata ─────────────────────────────────────────────────────
type ProfileMetadata = {
  name?: string
  picture?: string
  about?: string
  display_name?: string
  website?: string
  banner?: string
  nip05?: string
  lud16?: string
  lud06?: string
}

type NostrUser = {
  pubkey: string
  npub: string
  shortName: string
  image?: string
  metadata: ProfileMetadata
  lastUpdated: number
}

type NostrUserRequest = {
  pubkey: string
  relays?: string[]
  refreshStyle?: boolean | NostrEvent | null
}

// ── Relay info (NIP-11) ─────────────────────────────────────────────────
type RelayInfoDocument = {
  url: string
  name?: string
  description?: string
  icon?: string
  pubkey?: string
  contact?: string
  supported_nips?: number[]
  software?: string
  version?: string
  // Local addition, missing upstream: the host returns @nostr/gadgets'
  // loadRelayInfo() result verbatim, which carries the relay's own key here.
  self?: string
}

// ── Publishing result ────────────────────────────────────────────────────
type PublishResult = {
  relays: { [url: string]: { ok: boolean; error?: string } }
  published: number
  failed: number
}

// ── Sync into the store ──────────────────────────────────────────────────
type SyncResult = {
  // false when some author/kind could not be fetched; error says which
  success: boolean
  // events saved to the store that it did not have before
  newEvents: number
  error?: string
}

// ── Raw relay subscription ───────────────────────────────────────────────
type NostrFilter = {
  ids?: string[]
  authors?: string[]
  kinds?: number[]
  since?: number
  until?: number
  limit?: number
  search?: string
  [tag: `#${string}`]: string[] | undefined
}

type SubscribeOpts = {
  /** Sent to the relays as the subscription id prefix. Defaults to "<author prefix>-<napp d tag>". */
  label?: string
  /** Milliseconds to wait for every relay's EOSE before giving up on the slow ones. Defaults to 20000. */
  maxEoseTimeout?: number
  /** Called once, with everything that arrived before EOSE (or the timeout), deduplicated. */
  eoseEventsCallback?: (events: NostrEvent[]) => void
  /** Called once for each new event that arrives after that. */
  liveEventCallback?: (event: NostrEvent) => void
  /** Called once when every relay has ended the subscription, with each relay's reason. Not called when the napp closes it. */
  closedCallback?: (reasons: { [relay: string]: string }) => void
}

// ── Data-loading utils ───────────────────────────────────────────────────
type NappUtils = {
  // NIP-51 lists — accepts hex pubkey, npub, or nprofile
  loadRelayList(pubkey: string): Promise<ListResult<RelayItem>>
  loadFollowsList(pubkey: string): Promise<ListResult<string>>
  loadMuteList(pubkey: string): Promise<ListResult<string>>
  loadBookmarks(pubkey: string): Promise<ListResult<string>>
  loadPins(pubkey: string): Promise<ListResult<string>>
  loadBlossomServers(pubkey: string): Promise<ListResult<string>>
  loadEmojis(pubkey: string): Promise<ListResult<string>>
  loadFavoriteRelays(pubkey: string): Promise<ListResult<string>>
  loadBlockedRelays(pubkey: string): Promise<ListResult<string>>
  loadSearchRelays(pubkey: string): Promise<ListResult<string>>
  loadDmRelays(pubkey: string): Promise<ListResult<string>>
  loadWikiAuthors(pubkey: string): Promise<ListResult<string>>
  loadWikiRelays(pubkey: string): Promise<ListResult<string>>
  loadFavoriteFollowSets(pubkey: string): Promise<ListResult<AddressPointer>> // kind 10021
  loadFavoriteScrolls(pubkey: string): Promise<ListResult<EventPointer>> // kind 10027
  loadProfileBadges(pubkey: string): Promise<ListResult<string | AddressPointer>> // kind 10008
  loadSimpleGroups(pubkey: string): Promise<ListResult<SimpleGroupItem>> // kind 10009
  loadGitAuthors(pubkey: string): Promise<ListResult<string>> // kind 10017
  loadGitRepositories(pubkey: string): Promise<ListResult<string>> // kind 10018
  loadMediaFollows(pubkey: string): Promise<ListResult<string>> // kind 10020
  loadFavoritePodcasts(pubkey: string): Promise<ListResult<string>> // kind 10054
  loadAuthoredPodcasts(pubkey: string): Promise<ListResult<string>> // kind 10064

  // Composite helpers resolving address-pointer items into their sets
  fetchFavoriteRelaysWithSets(pubkey: string): Promise<Array<string | ResolvedSet<string>>>
  fetchEmojisWithSets(pubkey: string): Promise<Array<Emoji | ResolvedSet<Emoji>>>
  fetchFavoriteFollowSetsWithSets(pubkey: string): Promise<Array<ResolvedSet<string>>>

  // Addressable sets
  loadFollowSets(pubkey: string): Promise<SetResult<string>>
  loadRelaySets(pubkey: string): Promise<SetResult<string>>
  loadEmojiSets(pubkey: string): Promise<SetResult<string>>

  // Relay info
  loadRelayInfo(url: string): Promise<RelayInfoDocument | null>

  // A plain REQ to exactly these relays (no outbox logic). Returns the closer.
  subscribe(relays: string[], filter: NostrFilter, opts?: SubscribeOpts): () => void

  // Profile metadata
  loadNostrUser(request: NostrUserRequest | string): Promise<NostrUser>

  // Local full-text profile search over the launcher's in-memory index
  // (built from stored kind:0s at startup, augmented on every loadNostrUser).
  searchUserLocal(term: string): Promise<NostrUser[]>
  // Remote NIP-50 kind:0 search on the user's search relays (or defaults).
  searchUser(term: string): Promise<NostrUser[]>

  // Event fetching
  // code: nip19 code / `nostr:` URI / bare hex id, or a decoded pointer
  // ({id,…} for nevent, {identifier,pubkey,kind,…} for naddr)
  loadEvent(code: string | EventPointer | AddressPointer, relays?: string[], author?: string): Promise<NostrEvent | null>
  // Batched by-id fetch — one REQ over the id union; non-64-hex ids are dropped.
  loadEvents(ids: string[]): Promise<NostrEvent[]>
  // Verify an event's id + signature on the host (nostr-tools verifyEvent).
  verifyEvent(event: NostrEvent): Promise<boolean>

  // Throwaway-key signing — for ephemeral/anonymous identities, NOT the
  // user's key. NO rpc: both run inside the napp's own frame (nostr-tools
  // signing, lazy-imported from the /nostr-crypto.js companion), so the
  // secret key never reaches the host — it only ever sees finished signed
  // events (e.g. via publish). No permission prompt: the user's identity
  // is never involved. generateKey returns a fresh secp256k1 keypair (hex).
  generateKey(): Promise<{ sk: string; pk: string }>
  signWithKey(event: EventTemplate, sk: string): Promise<NostrEvent>

  // Save bytes to the user's disk. Napp iframes deliberately omit the
  // `allow-downloads` sandbox token, so a napp cannot download on its own and
  // gets no error when it tries — this rpc is the only route out, and being an
  // rpc is what puts it behind the permission prompt. Prefer passing a Blob:
  // it survives structured clone by reference, so the bytes are not copied.
  saveFile(
    name: string,
    data: Blob | ArrayBuffer | ArrayBufferView,
    type?: string
  ): Promise<{ name: string; size: number }>

  // Copy text to the user's clipboard. The napp sandbox has no
  // `clipboard-write` delegation, so navigator.clipboard rejects inside the
  // iframe — this rpc is the only route, and being an rpc puts it behind the
  // permission prompt (which previews the text being copied). Max 100k chars.
  copyText(text: string): Promise<{ length: number }>

  // Publishing
  publish(event: NostrEvent, relays?: string[]): Promise<PublishResult>
}

// ── Sync nostr primitives (bech32 / TLV, no rpc) ─────────────────────────
type Nip19Decoded =
  | { type: "npub" | "note" | "nsec"; data: string }
  | { type: "nprofile"; data: { pubkey: string; relays: string[] } }
  | { type: "nevent"; data: { id: string; relays: string[]; author?: string; kind?: number } }
  | {
      type: "naddr"
      data: { identifier: string; pubkey: string; kind: number; relays: string[] }
    }

type NappNip19 = {
  decode(bech: string): Nip19Decoded
  npubEncode(hex: string): string
  /** alias for neventEncode({ id }): returns an nevent, not a note */
  noteEncode(id: string): string
  neventEncode(pointer: { id: string; relays?: string[]; author?: string; kind?: number }): string
  naddrEncode(pointer: {
    identifier: string
    pubkey: string
    kind: number
    relays?: string[]
  }): string
}

type NappFx = {
  isHex64(s: unknown): boolean
  parseCoordinate(coord: string): { kind: number; pubkey: string; identifier: string } | null
  formatCoordinate(coord: { kind: number; pubkey: string; identifier: string }): string
  satsFromBolt11(invoice: string): number | null
}

// ── Main napp object ─────────────────────────────────────────────────────
type Napp = {
  instance: string
  registerAction(
    pattern: string,
    fn?: ((name: string, payload: unknown) => Promise<unknown>) | null
  ): void
  action(
    name: string,
    payload?: unknown,
    opts?: { instance?: string; auxiliary?: boolean }
  ): Promise<unknown>
  /** Close this window (same as the header × — keeps state for restore). */
  close(): void
  // Local addition, missing upstream: implemented by bridge.js (posts
  // "napp-link", host validates + prompts + opens) and documented in README.
  link(url: string): void
  // Fetches into the store, from each author's write relays, their events of
  // these kinds between since and until (default now), skipping what an
  // earlier sync already brought. With limit, only the newest
  // that many per author and kind before until (and not before since), asking
  // relays just for what the store lacks; with neither since nor limit, limit
  // is 10. Read them back with nostrdb.query, or see them arrive with
  // nostrdb.subscribe. All syncs share at most 16 REQs open at a time.
  sync(
    authors: string[],
    kinds: number[],
    opts?: { since?: number; until?: number; limit?: number }
  ): Promise<SyncResult>
  // A live subscription to filter, which must have authors, on each author's
  // write relays (two per author, each relay asked only for its own authors).
  // Calls back like utils.subscribe. Every event is also saved to the store.
  outbox(filter: NostrFilter, opts?: SubscribeOpts): { close(): void }
  utils: NappUtils
  // With `requires: ["dev"]`: install (or replace) a local app from files,
  // metadata.json among them, as if the user had installed a folder holding
  // them. It becomes the local napp `local~<metadata id>` — installed, with
  // no author until it is published from its page — and opens (or its open
  // windows reload). The first install asks the user, and so does replacing
  // one another napp installed; null if they said no.
  dev: {
    installLocal(
      files: Array<{
        path: string
        data: Blob | ArrayBuffer | ArrayBufferView | string
        type?: string
      }>
    ): Promise<{ nappId: string } | null>
  }
  /** Sync bech32/nip19 helpers. */
  nip19: NappNip19
  /** Sync misc helpers (hex / coordinates / bolt11). */
  fx: NappFx
  /**
   * The ui kit's helpers, for napps that declare `requires: ["ui"]`. Each one
   * builds a plain element with the kit's classes on it; none of them is
   * needed to use the kit. See "The ui kit" at the bottom of this file.
   */
  ui?: NappUi
}

/**
 * What a kit helper is given, and what it hands back. The helpers, their
 * names and their options are the ones every napp launcher's kit has
 * (nostrapps' napp-env.d.ts is the reference); what they build wears this
 * kit's classes, which are the reference's classes too (btn, ui-*), styled
 * the launcher's way. The words this kit had for variants and tones before
 * (brand, bad, …) still work.
 */
type NappButtonOpts = {
  label?: string
  /**
   * The class btn-<variant>. "primary" is the one action a screen is about,
   * "danger" is destructive, "warning" is careful, "outline" (the default)
   * is there but not the point, "ghost" appears on hover, "link" is inline
   * text, "icon" is a square for one glyph. brand, bad, warn, line, quiet and
   * plain are the old names for primary, danger, warning, outline, ghost and
   * link.
   */
  variant?:
    | "primary" | "danger" | "warning" | "outline" | "ghost" | "link" | "icon"
    | "brand" | "bad" | "warn" | "line" | "quiet" | "plain"
  /** One of the kit's glyph names, drawn before the label; alone, a square. */
  icon?: string
  /** The tooltip, and the name an icon-only button has. */
  title?: string
  disabled?: boolean
  /** "button" unless said: a bare button would submit its form. */
  type?: "button" | "submit" | "reset"
  /** Extra classes, for the context. className works too. */
  class?: string
  className?: string
  onClick?: (e: MouseEvent) => void
}

type NappChipOpts = {
  label: string
  /** The picked one, filled with the brand. */
  active?: boolean
  /** "danger" | "warning" say what the chip is; "static" makes it a fact, not a pick. */
  tone?: "danger" | "warning" | "bad" | "warn" | "static"
  icon?: string
  title?: string
  class?: string
  className?: string
  onClick?: (e: MouseEvent) => void
}

type NappCheckOpts = {
  checked?: boolean
  title?: string
  onChange?: (checked: boolean) => void
  /** On the box, or on the <label> when there is one. */
  class?: string
  className?: string
  /** Text beside the box, as one <label> with it; the box is its .control (and .input). */
  label?: string
  /** A grey line under the label. */
  note?: string
}

type NappTabs<T extends string> = HTMLDivElement & {
  /** Move the selection without reporting it. */
  select(value: T): void
  readonly value: T | undefined
}

type NappList<T> = HTMLDivElement & {
  add(item: T): HTMLDivElement
  delete(item: T): void
  /** In order; assign to replace them all. */
  items: T[]
}

type NappAppIconSize = "xs" | "s" | "m" | "l" | "xl"

/** A person as authorPicker hands one over. */
type NappPickedAuthor = {
  pubkey: string
  name?: string
  display_name?: string
  picture?: string
  nip05?: string
  follows?: boolean
}

type NappUi = {
  /** el(tag, class, ...children): children are elements or text. */
  el<K extends keyof HTMLElementTagNameMap>(
    tag: K,
    cls?: string,
    ...children: (Node | string | null | undefined | false)[]
  ): HTMLElementTagNameMap[K]
  /** stack: a column. bar: a row of controls that wraps. */
  stack(...children: (Node | string)[]): HTMLDivElement
  bar(...children: (Node | string)[]): HTMLDivElement
  /** Text-like content spaced as it reads; its children carry no margins. */
  flow(...children: (Node | string)[]): HTMLDivElement
  /** card: a plate with a line. plate: one with no fill. */
  card(...children: (Node | string)[]): HTMLDivElement
  plate(...children: (Node | string)[]): HTMLDivElement
  /** header is a screen's first line; section is a named group. */
  header(...children: (Node | string)[]): HTMLDivElement
  section(label: string, ...children: (Node | string)[]): HTMLDivElement
  /** grow is a row's filler: what follows it is pushed to the far end. */
  grow(): HTMLSpanElement
  divider(): HTMLHRElement
  /**
   * Keep an element in view as the rest scrolls: at the top (the default,
   * at 0), the bottom, or both, each at an offset (a number is px). Only
   * that: its background is yours.
   */
  sticky<T extends HTMLElement>(el: T, opts?: { top?: number | string; bottom?: number | string }): T

  /** The voices: a title, a display, a heading, the grey under it, an italic hint. */
  title(value: string): HTMLHeadingElement
  display(value: string): HTMLHeadingElement
  heading(value: string): HTMLHeadingElement
  /** The voice that names a group: small caps, tracked, grey. */
  label(value: string): HTMLDivElement
  /** The same voice as a caption in a line. */
  overline(text: string, cls?: string): HTMLSpanElement
  caption(value: string): HTMLSpanElement
  hint(value: string): HTMLSpanElement
  mono(value: string): HTMLSpanElement
  kbd(value: string): HTMLElement
  /** Code in a line, a <code>. */
  code(text: string, cls?: string): HTMLElement
  /** Code or data in lines on a plate, a <pre>; long lines wrap. */
  codeBlock(text: string, cls?: string): HTMLPreElement
  /** The line for when a list or a section has nothing yet. */
  empty(text: string, cls?: string): HTMLDivElement
  /** meter says how far along something is; set(v) moves it, v is 0…1. */
  meter(opts?: { value?: number; tone?: "danger" | "warn" | "bad" }): HTMLDivElement & {
    value: number
    set(value: number): void
  }
  /** A word set round a circle, turning; sized by the font-size it sits in. */
  ring(word: string, cls?: string): SVGElement

  button(opts?: NappButtonOpts): HTMLButtonElement
  chip(opts: NappChipOpts): HTMLButtonElement
  /** One tab on its own; toggle .active on it to move the selection. */
  tab(opts: {
    label: string
    active?: boolean
    onClick?: (e: MouseEvent) => void
    title?: string
    class?: string
  }): HTMLButtonElement
  /**
   * A row of tabs that owns the selection and reports the value when it
   * changes; the first is picked when active is not given. segmented: a
   * window's sections, sharing the width.
   */
  tabs<T extends string>(opts: {
    items: Array<T | { value: T; label?: string; title?: string; class?: string }>
    active?: T
    onChange?: (value: T) => void
    segmented?: boolean
    class?: string
    className?: string
  }): NappTabs<T>
  /** States are attributes: required, aria-invalid, readonly. */
  input(opts?: {
    type?: string
    placeholder?: string
    value?: string
    autocomplete?: string
    spellcheck?: boolean
    class?: string
    className?: string
  }): HTMLInputElement
  textarea(opts?: {
    placeholder?: string
    value?: string
    rows?: number
    class?: string
    className?: string
  }): HTMLTextAreaElement
  /** The form line: the label over the control, a note under; the label focuses it. */
  field(opts: {
    label?: string
    control?: HTMLElement
    note?: string
    class?: string
    className?: string
  }): HTMLLabelElement
  /** The bare box, or with a label (or a note) the box and its words as one <label>. */
  check(opts: NappCheckOpts & { label: string }): HTMLLabelElement & { input: HTMLInputElement }
  check(opts?: NappCheckOpts): HTMLInputElement
  radio(opts: NappCheckOpts & { name: string; label: string }): HTMLLabelElement & { input: HTMLInputElement }
  radio(opts: NappCheckOpts & { name: string }): HTMLInputElement
  /**
   * One of N, labelled, as a group; .value reads and sets the pick. label is
   * a caption before them; collapse keeps it one line, the caption and the
   * pick, until clicked, and folds it back once one is chosen.
   */
  radios<T extends string>(opts: {
    name?: string
    options: Array<T | { value: T; label?: string; title?: string; note?: string }>
    value?: T
    onChange?: (value: T) => void
    label?: string
    collapse?: boolean
    class?: string
    className?: string
  }): HTMLDivElement & { value: T | undefined }
  /** switch is a setting that is on or off, and where it stands. */
  switch(opts?: {
    label?: string
    note?: string
    checked?: boolean
    onChange?: (checked: boolean) => void
  }): HTMLLabelElement & { input: HTMLInputElement }

  /**
   * A <details>: its <summary>, then the children; the summary is text, or
   * parts in a line. fold is the small one under content; sticky keeps the
   * summary at the top while the content scrolls under it.
   */
  details(
    opts: {
      summary: string | Node | Array<string | Node>
      open?: boolean
      fold?: boolean
      sticky?: boolean | { top: number | string }
      class?: string
      className?: string
    },
    ...children: (Node | string)[]
  ): HTMLDetailsElement
  /**
   * A column of rows: the row's text cut with an ellipsis, then the controls
   * given; a label that is an element brings its own voice. A row that
   * failed takes tone "danger".
   */
  itemList(cls?: string): HTMLDivElement
  item(
    opts: {
      label: string | HTMLElement
      title?: string
      tone?: "danger" | "bad"
      mono?: boolean
      class?: string
    },
    ...controls: HTMLElement[]
  ): HTMLDivElement
  /** Rows that open, one at a time; append a row's content to the row. */
  rowList(cls?: string): HTMLDivElement & {
    row(...summary: (string | Node)[]): HTMLDetailsElement
  }
  row(list: HTMLElement, ...summary: (string | Node)[]): HTMLDetailsElement
  /** A button that becomes a field and an add; onAdd returns the error to show, or nothing. */
  addControl(opts: {
    label: string
    placeholder?: string
    class?: string
    onAdd: (value: string) => string | void
  }): HTMLDivElement
  /** Rows, the add control and the operations; the row's label and controls are yours. */
  list<T>(opts?: {
    items?: T[]
    label?: (item: T) => string | HTMLElement
    title?: (item: T) => string
    mono?: boolean
    controls?: (item: T, row: HTMLDivElement) => Node | Node[]
    add?: { label: string; placeholder?: string; onAdd: (value: string) => string | void }
    empty?: string
    class?: string
    className?: string
  }): NappList<T>
  /** A line to heed: warn (the default), bad (or danger), good, info; icon: null for none. */
  notice(
    text: string,
    opts?: { tone?: "warn" | "danger" | "good" | "info" | "bad"; icon?: string | null; class?: string }
  ): HTMLDivElement
  /** A fact in small caps; a tone colors it. */
  badge(
    text: string,
    opts?: {
      tone?: "good" | "danger" | "warn" | "brand" | "solid" | "bad"
      title?: string
      class?: string
    }
  ): HTMLSpanElement
  /**
   * An inline svg, 1em, in currentColor; the names are the keys of icons.
   * flush puts its ink at that edge instead of its box; iconInk says how
   * far each glyph sits in, add yours there.
   */
  icon(name: string, opts?: { flush?: "start" | "end"; class?: string }): SVGElement
  /** The glyphs icon() draws, the body of a 16×16 viewBox each; add your own. */
  icons: Record<string, string>
  iconInk: Record<string, [number, number]>
  /**
   * A napp's picture on a plate: xs an author's in a line of text, s in a
   * row, m beside a name (default), l on a card, xl on its detail. .img is
   * the <img>, put in once there is a picture; fade eases it in.
   */
  appIcon(opts?: {
    src?: string
    size?: NappAppIconSize
    fade?: boolean
    alt?: string
    class?: string
  }): HTMLSpanElement & { img: HTMLImageElement }
  /**
   * A person, the same wherever one is named: the name (the profile's name,
   * else the viewer's petname for them, else the display name, else the
   * short npub), a picture before it when asked for (true: xs; or an appIcon
   * size), and after it the petname and a check for someone followed. It
   * fills itself in when the profile lands. A link to the profile action
   * unless link is false or onClick says otherwise.
   */
  author(
    pubkey: string,
    opts?: {
      picture?: boolean | NappAppIconSize
      petname?: boolean
      follows?: boolean
      prefix?: string
      link?: boolean
      onClick?: (pubkey: string, e: MouseEvent) => void
      class?: string
    }
  ): HTMLElement
  authors: {
    /**
     * How the kit loads a profile, encodes an npub, opens a profile and
     * searches for people; set for napps already. load: null for a napp that
     * looks profiles up itself and hands them over with set().
     */
    use(config: {
      load?: ((pubkey: string) => Promise<unknown>) | null
      npub?: (pubkey: string) => string
      open?: (pubkey: string) => void
      search?: (
        term: string,
        opts: { limit?: number; remote?: boolean; nip05?: boolean }
      ) => Promise<unknown[]>
    }): void
    /** A profile in hand (what loadNostrUser gives, or a kind 0's content). */
    set(pubkey: string, user: unknown): void
    /** The viewer's follow list, a kind 3 (null: logged out): petnames and who is followed. */
    viewer(follows: { tags?: unknown } | null): void
    /** The name as text, as known right now. */
    name(pubkey: string): string
    follows(pubkey: string): boolean
  }
  /**
   * People to pick as one types. With a trigger ("@"), a text box: the
   * trigger after a space or at a line's start opens a list at it, and
   * onPick's string replaces it and what follows. Without, a who field: the
   * value is the query (an npub, nprofile or hex is that one person) and
   * onPick's string becomes the value. Known profiles by default; remote
   * adds relay search, nip05 resolves a typed name@domain (and, since the
   * launcher checks none, shows no row's nip-05).
   */
  authorPicker(
    target: HTMLTextAreaElement | HTMLInputElement,
    opts: {
      trigger?: string
      onPick: (author: NappPickedAuthor) => string | void
      limit?: number
      remote?: boolean
      nip05?: boolean
      class?: string
    }
  ): { close(): void; detach(): void }
  links(...children: (Node | string)[]): HTMLElement
  /** busy(node) breathes it, with aria-busy; busy(node, false) stops. */
  busy<T extends HTMLElement>(node: T, on?: boolean): T
  /** A ring 1em across: make it bigger through a font-size. */
  spinner(cls?: string): HTMLSpanElement
  /** The launcher's face is in the kit already: this resolves at once. */
  fonts(): Promise<void>
}

// ── Augment global Window ────────────────────────────────────────────────
interface Window {
  nostr: NostrSigner
  nostrdb: NostrDB
  napp: Napp
}

// ── The ui kit ───────────────────────────────────────────────────────────
//
// "requires": ["ui"] in metadata.json makes the launcher put its kit
// (napp-ui.css) in the page, ahead of the napp's own styles. The kit carries
// the launcher's own face — Verdana, in three faces, inlined — so a napp is
// set in the launcher's typography whatever the machine has. It is classes,
// so plain markup looks like the launcher and a napp that disagrees with the
// kit wins; window.napp.ui (the helpers above) builds the same elements for a
// napp that would rather call than write markup. The colors are the launcher's
// own tokens, set on <html> as --surface, --surface-alt, --text, --text-muted,
// --text-faint, --border, --chip, --chip-text, --accent, --accent-text,
// --danger, --dev and --dev-text, --suggest and --suggest-text, and they
// follow the theme the user picked.
//
// A class says how a thing looks, never where it goes: width, margins and
// placement are the parent's business.
//
//   The names are the reference kit's (nostrapps' napp-ui.css), so markup
//   written for it looks right here; states are classes of their own (active,
//   open, danger, …) beside the base one.
//
//   layout    ui-stack (a column) · ui-bar (a row) · ui-grow (a row's filler)
//             · ui-card (a plate with a line) · ui-plate (one with no fill) ·
//             ui-header (a name and what acts on it) · ui-section (a named
//             group) · ui-divider · ui-flow (text spaced as it reads) ·
//             ui-divided (hairlines between children) · ui-sticky (with
//             top, bottom, and --sticky-top, --sticky-bottom)
//   type      ui-title · ui-heading · ui-heading-xxl · ui-overline (small
//             caps: what names a group) · ui-caption · ui-hint (italic) ·
//             ui-mono · ui-kbd · ui-quote (someone else's words)
//   buttons   btn, with btn-primary | btn-outline | btn-danger | btn-warning |
//             btn-ghost | btn-link | btn-icon (the bare btn is a neutral plate)
//   chips     btn btn-chip (with btn-chip-label), btn-primary while picked,
//             btn-ghost while not, btn-danger | btn-warning for a tone, static
//   tabs      ui-tabs, ui-tab, active · ui-segments, ui-segment, active (a
//             window's sections)
//   fields    ui-input (mono; required, aria-invalid, readonly; a <select>
//             too) · ui-field (with ui-field-note) · ui-writing (a textarea to
//             write in, no box)
//   checks    ui-check · ui-radio · ui-check-label (with ui-check-text,
//             ui-check-note) · ui-radios (collapse, open, ui-radios-value) ·
//             ui-switch (with ui-switch-track)
//   lists     ui-items · ui-item (danger) · ui-item-label (mono) · ui-add
//             (with ui-add-form, ui-add-input, ui-add-error) · ui-rows ·
//             ui-row · ui-list · ui-compact (a list a size down)
//   folding   ui-details (a <details>; sticky) · ui-fold (the small one under
//             content) · ui-summary (parts of a summary) · ui-empty
//   code      ui-code (inline) · ui-code-block (a <pre>)
//   badges    ui-badge, with good | danger | warn | brand | solid
//   icons     ui-icon with ui-icon-<name> (flush-start | flush-end): check,
//             x, close, plus, minus, chevron, arrow, forward, back, trash,
//             reload, reset, search, external, warning, warn, info, star,
//             copy, folder, user, home, settings, sun, moon, bolt, link,
//             save, window, move, tile, pack, grid
//   images    ui-app-icon with ui-app-icon-xs | -s | -m | -l | -xl, fade
//   people    ui-author (ui-author-name, ui-author-pet, ui-author-follows) ·
//             ui-author-picker (ui-author-pick, active, ui-author-pick-nip05)
//   notices   ui-notice with warn | danger | good | info
//   more      ui-links · ui-busy · ui-spinner · ui-meter (danger | warn, with
//             --v-value) · ui-ring

// The kit's own names for the launcher's colors, for a napp that paints with
// them: --v-ink, --v-ink-2, --v-ink-3, --v-fill, --v-fill-2, --v-line,
// --v-brand, --v-brand-ink, --v-bad, --v-warn, --v-warn-ink, plus the washes
// --v-brand-wash, --v-brand-wash-2, --v-bad-wash, --v-hover and --v-press.
//
// Its measurements, for a napp that wants them: --v-gap, --v-gap-tight,
// --v-gap-loose, --v-pad, --v-pad-loose, --v-pad-roomy, --v-radius,
// --v-radius-field, --v-radius-plate, --v-radius-card, --v-radius-pill, the
// type ladder --v-micro … --v-display, and --v-fast, --v-move, --v-ease.
