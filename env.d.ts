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

// ── Feed subscription ────────────────────────────────────────────────────
type FeedHandle = {
  close(): void
}

type FeedCallback = (events: NostrEvent[], synced: boolean) => void

type FeedOpts = {
  since?: number
  until?: number
  limit?: number
}

type NappFeeds = {
  profile(pubkey: string, kinds: number[], callback: FeedCallback, opts?: FeedOpts): FeedHandle
  following(source: string, kinds: number[], callback: FeedCallback, opts?: FeedOpts): FeedHandle
  inbox(
    pubkey: string | string[],
    kinds: number[],
    callback: FeedCallback,
    opts?: FeedOpts
  ): FeedHandle
  outbox(
    pubkeys: string | string[],
    kinds: number[],
    callback: FeedCallback,
    opts?: FeedOpts
  ): FeedHandle
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

  // Profile metadata
  loadNostrUser(request: NostrUserRequest | string): Promise<NostrUser>

  // Local full-text profile search over the launcher's in-memory index
  // (built from stored kind:0s at startup, augmented on every loadNostrUser).
  searchUserLocal(term: string): Promise<NostrUser[]>
  // Remote NIP-50 kind:0 search on the user's search relays (or defaults).
  searchUser(term: string): Promise<NostrUser[]>

  // Event fetching
  loadEvent(code: string, relays?: string[], author?: string): Promise<NostrEvent | null>
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
  noteEncode(hex: string): string
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
  feeds: NappFeeds
  utils: NappUtils
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

/** What a kit helper is given, and what it hands back. */
type NappButtonOpts = {
  label?: string
  /** "accent" | "danger" | "quiet" | "plain" — the default is the neutral one. */
  variant?: "accent" | "danger" | "quiet" | "plain"
  /** One of the kit's glyph names, drawn before the label. */
  icon?: string
  /** The tooltip, and the name an icon-only button has. */
  title?: string
  disabled?: boolean
  type?: string
  className?: string
  onClick?: (e: Event) => void
}

type NappChipOpts = {
  label: string
  active?: boolean
  icon?: string
  title?: string
  className?: string
  onClick?: (e: Event) => void
}

type NappTabsOpts = {
  items: (string | { value: string; label: string })[]
  active?: string
  onChange?: (value: string) => void
  className?: string
}

type NappUi = {
  /** el(tag, className, ...children): children are elements or text. */
  el: (tag: string, className?: string, ...children: (Node | string | null)[]) => HTMLElement
  /** stack: a column. bar: a row of controls. card: a plate. */
  stack: (...children: (Node | string)[]) => HTMLElement
  bar: (...children: (Node | string)[]) => HTMLElement
  card: (...children: (Node | string)[]) => HTMLElement
  rule: () => HTMLElement
  /** The voices: a title, a name, the grey under it, an italic label. */
  title: (value: string) => HTMLElement
  heading: (value: string) => HTMLElement
  caption: (value: string) => HTMLElement
  hint: (value: string) => HTMLElement
  mono: (value: string) => HTMLElement
  code: (value: string) => HTMLElement
  codeBlock: (value: string) => HTMLElement
  empty: (value: string) => HTMLElement
  button: (opts?: NappButtonOpts) => HTMLButtonElement
  chip: (opts: NappChipOpts) => HTMLButtonElement
  /** The row, plus select(value) and the value itself. */
  tabs: (opts: NappTabsOpts) => HTMLElement & {
    value: string
    select: (value: string) => void
  }
  input: (opts?: {
    type?: string
    placeholder?: string
    value?: string
    className?: string
  }) => HTMLInputElement
  textarea: (opts?: {
    placeholder?: string
    value?: string
    rows?: number
    className?: string
  }) => HTMLTextAreaElement
  /** field wraps its control in a label, so the words focus it. */
  field: (opts: {
    label?: string
    control?: HTMLElement
    note?: string
    className?: string
  }) => HTMLElement
  check: (opts?: {
    label?: string
    note?: string
    checked?: boolean
    title?: string
    onChange?: (checked: boolean) => void
  }) => HTMLElement & { input: HTMLInputElement }
  radios: (opts: {
    name?: string
    options: (string | { value: string; label?: string; note?: string })[]
    value?: string
    onChange?: (value: string) => void
  }) => HTMLElement
  details: (
    opts: { summary: string | Node | (string | Node)[]; open?: boolean; className?: string },
    ...children: (Node | string)[]
  ) => HTMLDetailsElement
  /** rowList() and row() make rows that open, one at a time. */
  rowList: (className?: string) => HTMLElement & {
    row: (...summary: (string | Node)[]) => HTMLDetailsElement
  }
  /**
   * list() draws the rows and the add line; label(item) is the text and
   * controls(item) what sits at the row's end. It has add(item), delete(item)
   * and the items it holds.
   */
  list: (opts: {
    items?: unknown[]
    label?: (item: any) => string
    mono?: boolean
    controls?: (item: any, list: any) => Node | Node[]
    add?: { label: string; placeholder?: string; onAdd: (value: string) => string | void }
    empty?: string
    className?: string
  }) => HTMLElement & {
    items: unknown[]
    add: (item: unknown) => void
    delete: (item: unknown) => void
  }
  /** tone: "warn" (the default) | "danger" | "info". */
  notice: (
    message: string,
    opts?: { tone?: "warn" | "danger" | "info"; icon?: string | null }
  ) => HTMLElement
  /** tone: "accent" | "danger" | "dev". */
  badge: (label: string, opts?: { tone?: "accent" | "danger" | "dev" }) => HTMLElement
  icon: (name: string) => HTMLElement
  /** size: "s" | "m" | "l" | "xl". fade eases the image in. */
  appIcon: (opts?: { src?: string; size?: string; fade?: boolean; alt?: string }) => HTMLElement & {
    img: HTMLImageElement
  }
  links: (...children: (Node | string)[]) => HTMLElement
  /** busy(node) breathes it; busy(node, false) stops. */
  busy: <T extends HTMLElement>(node: T, on?: boolean) => T
  /** A ring 1em across: make it bigger through a font-size. */
  spinner: () => HTMLElement
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
// (napp-ui.css) in the page, ahead of the napp's own styles. It is classes
// only — no script, nothing to call — so plain markup looks like the
// launcher, and a napp that disagrees with the kit wins. The colors are the
// launcher's own tokens, set on <html> as --surface, --surface-alt, --text,
// --text-muted, --text-faint, --border, --chip, --chip-text, --accent,
// --accent-text, --danger, --dev and --dev-text, and they follow the theme
// the user picked.
//
// A class says how a thing looks, never where it goes: width, margins and
// placement are the parent's business.
//
//   layout   v-col (a column) · v-bar (a row) · v-card (a plate) · v-rule
//   type     v-title · v-heading · v-caption · v-hint (italic) · v-mono
//   buttons  v-btn, and v-btn--accent | --danger | --quiet | --plain
//   chips    v-chip, v-chip--on (the picked one)
//   tabs     v-tabs, v-tab, v-tab--on
//   fields   v-input · v-field (with v-field-note)
//   checks   v-check · v-radio · v-check-label (with v-check-text,
//            v-check-note) · v-radios
//   lists    v-items · v-item · v-item-label (--mono) · v-add (with
//            v-add-error) · v-rows · v-row
//   folding  v-disclosure (a <details>) · v-empty
//   code     v-code (inline) · v-codeblock (a <pre>)
//   badges   v-badge, v-badge--accent | --danger | --dev
//   icons    v-icon with v-icon-<name>: check, x, plus, minus, chevron,
//            arrow, trash, reload, search, external, warning, info, star,
//            copy, folder, user, home, settings, sun, moon, bolt, link,
//            save, window
//   images   v-appicon with v-appicon--s | --m | --l | --xl, --fade
//   notices  v-notice with v-notice--warn | --danger | --info
//   more     v-links · v-busy · v-spinner

// The kit's own measurements, for a napp that wants them: --v-gap,
// --v-gap-tight, --v-gap-loose, --v-pad, --v-pad-loose, --v-radius,
// --v-radius-field, --v-radius-card, --v-radius-dialog, --v-fast, --v-ease.
