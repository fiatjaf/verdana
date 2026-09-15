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
}

// ── Augment global Window ────────────────────────────────────────────────
interface Window {
  nostr: NostrSigner
  nostrdb: NostrDB
  napp: Napp
}
