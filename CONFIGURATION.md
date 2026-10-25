# Napp configurations

A **napp configuration** is a nostr event of kind `35133` describing how a
launcher should be set up: which napps to install, which napp handles an
action by default, which bundles go on the home tab, and what napps get when
they ask to sign, to publish or to open a link. It is addressable: a newer
event from the same author with the same `d` tag replaces the older one.

Verdana loads a configuration when you open its `naddr` or `nevent` (the
"open a nostr code" box, or a `nostr:` link). It asks before applying it, and
from then on lists it in **Settings → Napp configurations**, together with
every other configuration it ever loaded. There each one is a single line
that unfolds into what it sets, with a button to apply it, so configurations
can be applied one at a time, in any order, and applied again later.

Applying a configuration is a one-off: it writes its settings over the
launcher's current ones and installs what it names. Nothing is linked back to
the event afterwards, and there is no undoing it: the settings it wrote are
ordinary settings from then on, and the napps and bundles it added are
ordinary napps and bundles.

## Tags

All tags are optional except `d`. Unknown tags are ignored, and a tag that
doesn't make sense is skipped with a warning without failing the rest of the
configuration.

### `d` and `title`

```json
["d", "<identifier>"]
["title", "<name shown in the settings>"]
```

`name` is read as `title` too. Without either, the `d` tag is the name.

### `napp`: a napp to install

```json
["napp", "<kind>:<pubkey>:<d>", "<relay hint>"]
["napp", "<event id>", "<relay hint>", "<author pubkey>"]
```

The first form is the napp's address (kind `35130` for web napps, `35131` for
wasm napps); verdana installs the newest version it finds. The second form
pins one exact event; with an author given, an event signed by anyone else is
refused. The relay hint may be empty.

A napp that is already installed in the same version or a newer one is left
alone.

### `action`: a default handler

```json
["action", "<action name>", "<kind>:<pubkey>:<d>", "<mode>", "singleton" | ""]
```

When an action could go to more than one napp, verdana asks which one in a
picker window. A default handler changes that, according to its mode:

| mode        | what happens                                                                                                                                                                                                  |
|-------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `timed`     | The picker shows the default first, highlighted, with a 3 second countdown, and sends the action to it when the countdown runs out. Every 48dp the pointer or a finger travels in the window adds a second; clicking in the filter box stops the countdown for good. |
| `suggested` | The picker shows the default first, highlighted, and waits for the user.                                                                                                                                       |
| `forced`    | No picker at all: the action goes to the default.                                                                                                                                                              |

With `singleton` as the fifth element, the action goes into the first open
window of that napp when there is one, instead of a new window.

A default only applies when its napp is installed and declares the action
(a napp declaring `view` takes every `view:<kind>`). One default per action:
if a configuration names an action twice, the first tag counts, and applying
a configuration replaces the defaults of the actions it names.

Default handlers can only come from configurations. The settings list them
and can delete them, which makes the action ask again.

### `bundle`: a bundle on the home tab

```json
["bundle", "<name>", "<icon url>" | "", "<napp item>", "<napp item>", …]
```

Each item from the fourth element on is one napp of the bundle and the
actions it is sent when the bundle opens:

```
napp <kind>:<pubkey>:<d> <action> <payload> <action> <payload> …
```

Each action is followed by its payload as JSON, which may contain spaces
(`{"path": "/a b"}`), or `null` for none. An item with no actions just opens
the napp. An item may also hold several napps, each one starting at another
`napp`.

The bundle replaces any bundle of the same name and is pinned to the home
tab. The icon, when given, is an image URL shown instead of the bundle's
napps' icons.

### `sign` and `publish`: what napps get when they ask

```json
["sign", "ask" | "allow" | "deny"]
["publish", "ask" | "allow" | "deny"]
```

What a napp gets when it asks to sign an event or to publish one, unless the
user already answered for that napp (an "always allow" or "always deny" given
to a napp's own prompt comes first). These set the **Napps signing** and
**Napps publishing** settings.

### `auth-on-reads`

```json
["auth-on-reads", "yes" | "no"]
```

Whether verdana authenticates (NIP-42) to relays that ask for it when
reading. Publishing always does. This sets the **Authenticate when reading**
setting.

### `open-links`

```json
["open-links", "yes" | "non-image" | "no"]
```

Which links napps open in the browser without asking (`napp.link()`), unless
the user already answered for that napp:

| value       | links to images                                    | other links           |
|-------------|----------------------------------------------------|-----------------------|
| `yes`       | opened in the browser                              | opened in the browser |
| `non-image` | shown over the napp's window, with a button to open | opened in the browser |
| `no`        | shown over the napp's window, with a button to open | asked about first     |

This sets the **Open links without asking** setting.

## Example

This is the setup of the verdana on the computer this document was written
on, as a configuration. The napps are the ones installed there (local napps,
which have no event, can't be referenced and are left out), the bundles are
its bundles and the permissions are its settings. It had no default handlers
yet, so the `action` tags name the napps its picker counts say the user
picks most for those actions.

```json
{
  "kind": 35133,
  "content": "",
  "tags": [
    ["d","desktop"],
    ["title","Desktop setup"],
    ["napp","35131:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:activity","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:article-reader","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:articles","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:colored-feed","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:contacts","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:database","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:definitions-editor","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:definitions-resolver","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:definitions-viewer","wss://relay.nostrapps.com"],
    ["napp","35131:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:dialogue","wss://relay.nostrapps.com"],
    ["napp","35130:9ec7a778167afb1d30c4833de9322da0c08ba71a69e1911d5578d3144bb56437:drafts","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:favorite-relay-feeds","wss://relay.nostrapps.com"],
    ["napp","35130:9ec7a778167afb1d30c4833de9322da0c08ba71a69e1911d5578d3144bb56437:feed","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:follow","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:following_feed","wss://relay.nostrapps.com"],
    ["napp","35130:9ec7a778167afb1d30c4833de9322da0c08ba71a69e1911d5578d3144bb56437:follows","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:git_repositories","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:group-chat","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:group-editor","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:groupstream","wss://relay.nostrapps.com"],
    ["napp","35131:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:hours","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:inactive-follows","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:inbox","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:like","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:nadar","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:note-viewer","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:notifications","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:profile","wss://relay.nostrapps.com"],
    ["napp","35130:9ec7a778167afb1d30c4833de9322da0c08ba71a69e1911d5578d3144bb56437:profile","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:profile_feed","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:publish","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:raw","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:react","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:reaction-feed","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:reaction-viewer","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:reactions-inbox","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:relay_feed","wss://relay.nostrapps.com"],
    ["napp","35130:9ec7a778167afb1d30c4833de9322da0c08ba71a69e1911d5578d3144bb56437:relays","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:repository","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:thread-viewer","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:simple-groups","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:simple-reply","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:simple-retweet","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:stats","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:suggested-follows","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:text-edit","wss://relay.nostrapps.com"],
    ["napp","35131:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:thread-tree","wss://relay.nostrapps.com"],
    ["napp","35131:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:trending","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:upload-to-blossom","wss://relay.nostrapps.com"],
    ["napp","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:user-search","wss://relay.nostrapps.com"],
    ["napp","35131:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:words","wss://relay.nostrapps.com"],
    ["napp","35131:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:yoink","wss://relay.nostrapps.com"],
    ["action","view:1","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:thread-viewer","timed","singleton"],
    ["action","view:1111","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:thread-viewer","timed","singleton"],
    ["action","relay","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:relay_feed","suggested",""],
    ["action","profile","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:profile_feed","suggested",""],
    ["action","edit-numl","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:text-edit","forced",""],
    ["action","simple-group","35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:group-chat","timed","singleton"],
    ["bundle","Basic","","napp 35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:reactions-inbox profile \"3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d\"","napp 35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:following_feed profile \"3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d\"","napp 35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:inbox profile \"3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d\" inbox null"],
    ["bundle","Groups","","napp 35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:groupstream profile \"3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d\"","napp 35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:simple-groups profile \"3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d\"","napp 35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:group-chat simple-group \"naddr1qvzqqqyctqpzp5lvnyffx52rpfkc8g4zf99y3m40c2fes6js70ww2vjw3xqwy8g9qyvhwumn8ghj7urewfsk66ty9enxjct5dfskvtnrdaksqyz5decnw7pjtf2xwujstft5vujrp3u84q\""],
    ["bundle","Publish","","napp 35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:publish"],
    ["bundle","Basically Global","","napp 35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:relay_feed relay \"wss://pyramid.fiatjaf.com\"", "napp 35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:relay_feed relay \"wss://theforest.nostr1.com\"", "napp 35130:3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d:relay_feed relay \"wss://nostr.wine\""],
    ["sign","allow"],
    ["publish","allow"],
    ["auth-on-reads","yes"],
    ["open-links","non-image"]
  ]
}
```
