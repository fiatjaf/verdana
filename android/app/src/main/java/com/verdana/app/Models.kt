package com.verdana.app

import org.json.JSONArray
import org.json.JSONObject

// The backend crosses the gomobile boundary as JSON (see backend/mobile), so
// the UI parses everything it renders from these shapes.

data class NappPath(val path: String, val sha256: String)

data class Napp(
    val id: String,
    val d: String = "",
    val name: String,
    val description: String,
    val icon: String,
    val author: String,
    val authorName: String,
    val actions: List<String>,
    val requires: List<String>,
    val servers: List<String> = listOf(),
    val createdAt: Long = 0,
    val paths: List<NappPath>,
    val updateAvailable: Boolean = false,
)

data class ProfileDetail(
    val pubkey: String,
    val npub: String,
    val name: String,
    val displayName: String,
    val shortName: String,
    val about: String,
    val picture: String,
    val nip05: String,
    val website: String,
)

// a napp window. open is false for one closed earlier this run, which the
// Sessions tab can reopen; history is every action it was sent this run (only
// managedWindows carries it).
data class WindowInfo(
    val instance: String,
    val nappId: String,
    val name: String,
    val action: String,
    val open: Boolean = true,
    val history: List<BundleAction> = listOf(),
)

data class PromptOption(
    val label: String,
    val detail: String,
    val nappId: String = "",
    val instance: String = "",
    // the backend has already ordered these by how often the user picked
    // them; suggested says which ones carry that evidence
    val suggested: Boolean = false,
    val dev: Boolean = false,
    // name is the napp's own name, without the window, and icon its icon's
    // bytes as base64: only sent to a picker window (see PickerWindow)
    val name: String = "",
    val icon: String = "",
    // default is the action's default handler, from a napp configuration:
    // the backend put it first
    val default: Boolean = false,
)

// PromptSection is a heading of a prompt and the lines under it.
data class PromptSection(val heading: String, val lines: List<String>)

data class Prompt(
    val id: Long,
    val title: String,
    val detail: String,
    val code: String,
    val napp: String,
    val options: List<PromptOption>,
    // warning is something that could hurt, shown loud; items are rows the
    // detail ends by pointing at (the relays a publish goes to); summary,
    // when there is one, is the line code starts folded under.
    val warning: String = "",
    val items: List<String> = listOf(),
    // sections are what the prompt is about, grouped under headings
    val sections: List<PromptSection> = listOf(),
    val summary: String = "",
    // image is the url of an image the prompt is about: shown as big as it
    // fits, with "Open in browser" and "Close" beside it (see ImagePrompt)
    val image: String = "",
    // remember says the answer can stick: the launcher then offers this
    // prompt only, this session, and always, and files the wider answers away.
    val remember: Boolean = false,
    // instance is the window the prompt belongs over, when a napp fired it.
    // Blank for questions the launcher asked itself.
    val instance: String = "",
    // countdown, in milliseconds, is how long a picker waits before it
    // picks the default option by itself; 0 waits for the user
    val countdown: Long = 0,
)

// DefaultHandler is the napp an action goes to unless the user picks another
// (see backend/config.go): mode is "timed", "suggested" or "forced", and a
// singleton one goes into the napp's open window, when it has one.
data class DefaultHandler(
    val action: String,
    val target: String,
    val targetName: String,
    val mode: String,
    val singleton: Boolean,
)

// ConfigurationInfo is a napp configuration the launcher has loaded, as the
// settings list it (see backend/config.go): summary is its one line, lines
// what it sets, and applied whether this very version was applied.
data class ConfigurationInfo(
    val key: String,
    val name: String,
    val author: String,
    val authorName: String,
    val createdAt: Long,
    val summary: String,
    val lines: List<String>,
    val applied: Boolean,
)

// a bundle (see backend/bundles.go): napps opened together, each with the
// actions it gets. payload is the action's payload as raw JSON, "" for none.
data class BundleAction(val type: String, val payload: String)
data class BundleEntry(val nappId: String, val actions: List<BundleAction>)
data class BundleInfo(
    val name: String,
    val entries: List<BundleEntry>,
    val pinned: Boolean,
    val shortcut: Boolean,
    // icon is the url of the image a napp configuration gave the bundle,
    // shown instead of its napps' icons; "" for none
    val icon: String = "",
)

// BundleDraftEntry is one napp of a bundle in the editor: lines are its
// actions, one {"type": …, "payload": …} JSON object per line.
data class BundleDraftEntry(val nappId: String, val label: String, val lines: String)

fun parseBundleDraft(json: String): List<BundleDraftEntry> = try {
    val arr = JSONArray(json)
    (0 until arr.length()).map {
        val e = arr.getJSONObject(it)
        BundleDraftEntry(e.optString("nappId"), e.optString("label"), e.optString("lines"))
    }
} catch (_: Exception) { listOf() }

fun bundleDraftJSON(draft: List<BundleDraftEntry>): String = JSONArray().apply {
    draft.forEach {
        put(JSONObject().put("nappId", it.nappId).put("label", it.label).put("lines", it.lines))
    }
}.toString()

// HomeItem is one cell of the home tab: a napp or a bundle, never both. key
// is what movePin calls it.
data class HomeItem(val key: String, val napp: Napp?, val bundle: BundleInfo?)

data class LauncherState(
    val phase: String,
    val loginErr: String,
    val loginStatus: String,
    val profileName: String,
    val profilePicture: String,
    val pubkey: String,
    val fetchErr: String,
    val fetching: Boolean,
    val theme: String,
    val readAuth: Boolean = true,
    // what napps get when they ask to sign or publish and the user has not
    // answered for them: "ask", "allow" or "deny"
    val signPolicy: String = "ask",
    val publishPolicy: String = "ask",
    // which links napps open without asking: "yes" (all), "non-image" or "no"
    val openLinks: String = "no",
    val defaultHandlers: List<DefaultHandler> = listOf(),
    val configurations: List<ConfigurationInfo> = listOf(),
    val relays: List<String>,
    val installed: List<Napp>,
    // napps opened without installing them: on disk, but not on the installed list
    val previews: List<Napp> = listOf(),
    val discovery: List<Napp>,
    // napps published by the people the user follows (the napps tab's
    // contacts mode), fetched on demand
    val contacts: List<Napp> = listOf(),
    val contactsFetching: Boolean = false,
    val pinned: List<Napp> = listOf(),
    val home: List<HomeItem> = listOf(),
    val bundles: List<BundleInfo> = listOf(),
    val busy: List<String>,
    val windows: List<WindowInfo>,
    val managedWindows: List<WindowInfo> = listOf(),
    val updateCheckRunning: Boolean = false,
)

fun parseState(json: String): LauncherState {
    val o = JSONObject(json)
    fun napps(key: String): List<Napp> {
        val arr = o.optJSONArray(key) ?: JSONArray()
        return (0 until arr.length()).map { parseNapp(arr.getJSONObject(it)) }
    }

    val busyArr = o.optJSONArray("busy") ?: JSONArray()
    val relaysArr = o.optJSONArray("relays") ?: JSONArray()
    return LauncherState(
        phase = o.optString("phase"),
        loginErr = o.optString("loginErr"),
        loginStatus = o.optString("loginStatus"),
        profileName = o.optString("profileName"),
        profilePicture = o.optString("profilePicture"),
        pubkey = o.optString("pubkey"),
        fetchErr = o.optString("fetchErr"),
        fetching = o.optBoolean("fetching"),
        theme = o.optString("theme", "light"),
        readAuth = o.optBoolean("readAuth", true),
        signPolicy = o.optString("signPolicy", "ask"),
        publishPolicy = o.optString("publishPolicy", "ask"),
        openLinks = o.optString("openLinks", "no"),
        defaultHandlers = (0 until (o.optJSONArray("defaultHandlers")?.length() ?: 0)).map { i ->
            val h = o.getJSONArray("defaultHandlers").getJSONObject(i)
            DefaultHandler(
                action = h.optString("action"),
                target = h.optString("target"),
                targetName = h.optString("targetName"),
                mode = h.optString("mode"),
                singleton = h.optBoolean("singleton"),
            )
        },
        configurations = (0 until (o.optJSONArray("configurations")?.length() ?: 0)).map { i ->
            val c = o.getJSONArray("configurations").getJSONObject(i)
            val lines = c.optJSONArray("lines")
            ConfigurationInfo(
                key = c.optString("key"),
                name = c.optString("name"),
                author = c.optString("author"),
                authorName = c.optString("authorName"),
                createdAt = c.optLong("createdAt"),
                summary = c.optString("summary"),
                lines = (0 until (lines?.length() ?: 0)).map { lines!!.getString(it) },
                applied = c.optBoolean("applied"),
            )
        },
        relays = (0 until relaysArr.length()).map { relaysArr.getString(it) },
        installed = napps("installed"),
        previews = napps("previews"),
        discovery = napps("discovery"),
        contacts = napps("contacts"),
        contactsFetching = o.optBoolean("contactsFetching"),
        pinned = napps("pinned"),
        home = (0 until (o.optJSONArray("home")?.length() ?: 0)).map { i ->
            val h = o.optJSONArray("home")!!.getJSONObject(i)
            HomeItem(
                key = h.optString("key"),
                napp = h.optJSONObject("napp")?.let { parseNapp(it) },
                bundle = h.optJSONObject("bundle")?.let { parseBundle(it) },
            )
        },
        bundles = (0 until (o.optJSONArray("bundles")?.length() ?: 0)).map {
            parseBundle(o.optJSONArray("bundles")!!.getJSONObject(it))
        },
        busy = (0 until busyArr.length()).map { busyArr.getString(it) },
        updateCheckRunning = o.optBoolean("updateCheckRunning"),
        windows = parseWindows(o.optJSONArray("windows")),
        managedWindows = parseWindows(o.optJSONArray("managedWindows")),
    )
}

fun parsePrompt(json: String): Prompt? {
    if (json.isBlank()) return null
    val o = JSONObject(json)
    val optsArr = o.optJSONArray("options") ?: JSONArray()
    return Prompt(
        id = o.optLong("id"),
        title = o.optString("title"),
        detail = o.optString("detail"),
        code = o.optString("code"),
        warning = o.optString("warning"),
        items = o.optJSONArray("items")?.let { a -> (0 until a.length()).map { a.getString(it) } } ?: listOf(),
        sections = o.optJSONArray("sections")?.let { a ->
            (0 until a.length()).map { i ->
                val sec = a.getJSONObject(i)
                val lines = sec.optJSONArray("lines")
                PromptSection(
                    heading = sec.optString("heading"),
                    lines = lines?.let { l -> (0 until l.length()).map { l.getString(it) } } ?: listOf(),
                )
            }
        } ?: listOf(),
        summary = o.optString("summary"),
        image = o.optString("image"),
        napp = o.optString("napp"),
        remember = o.optBoolean("remember"),
        options = (0 until optsArr.length()).map { i ->
            val p = optsArr.getJSONObject(i)
            PromptOption(
                label = p.optString("label"),
                detail = p.optString("detail"),
                nappId = p.optString("nappId"),
                instance = p.optString("instance"),
                suggested = p.optBoolean("suggested"),
                dev = p.optBoolean("dev"),
                name = p.optString("name"),
                icon = p.optString("icon"),
                default = p.optBoolean("default"),
            )
        },
        instance = o.optString("instance"),
        countdown = o.optLong("countdown"),
    )
}

class WindowSpec(json: String) {
    val instance: String
    val nappId: String
    val name: String
    val description: String

    // mode is "wasm" for a napp that is a module painting a canvas (see
    // NappCanvas), "web" for the usual page
    val mode: String
    val dir: String
    val requires: List<String>
    val theme: String
    val themeVars: String
    val storage: String

    init {
        val o = JSONObject(json)
        instance = o.optString("instance")
        nappId = o.optString("nappId")
        name = o.optString("name")
        description = o.optString("description")
        mode = o.optString("mode", "web")
        dir = o.optString("dir")
        requires = (0 until (o.optJSONArray("requires")?.length() ?: 0)).map {
            o.optJSONArray("requires")!!.getString(it)
        }
        theme = o.optString("theme", "light")
        themeVars = o.optString("themeVars", "{}")
        storage = o.optString("storage", "{}").ifBlank { "{}" }
    }
}

fun parseProfile(json: String): ProfileDetail? {
    if (json.isBlank()) return null
    return try {
        val o = JSONObject(json)
        ProfileDetail(
            pubkey = o.optString("pubkey"),
            npub = o.optString("npub"),
            name = o.optString("name"),
            displayName = o.optString("displayName"),
            shortName = o.optString("shortName"),
            about = o.optString("about"),
            picture = o.optString("picture"),
            nip05 = o.optString("nip05"),
            website = o.optString("website"),
        )
    } catch (_: Exception) { null }
}

// Opened is what a pasted nostr code or nostr: link turned out to be (see
// OpenCode in backend/resolve.go): a "profile" page, a "napp" page, an
// "action" already handed to a napp or a "config" the user is being asked to
// apply, with nothing for the launcher to show.
data class Opened(val kind: String, val pubkey: String, val napp: Napp?)

fun parseOpened(json: String): Opened? = try {
    val o = JSONObject(json)
    Opened(
        kind = o.optString("kind"),
        pubkey = o.optString("pubkey"),
        napp = o.optJSONObject("napp")?.let { parseNapp(it) },
    )
} catch (_: Exception) { null }

fun parseNapps(json: String): List<Napp> {
    if (json.isBlank()) return listOf()
    return try {
        val arr = JSONArray(json)
        (0 until arr.length()).map { parseNapp(arr.getJSONObject(it)) }
    } catch (_: Exception) { listOf() }
}

private fun JSONObject.strings(key: String): List<String> {
    val arr = optJSONArray(key) ?: return listOf()
    return (0 until arr.length()).map { arr.getString(it) }
}

fun parseNapp(n: JSONObject): Napp {
    val pathsArr = n.optJSONArray("paths") ?: JSONArray()
    return Napp(
        id = n.optString("id"),
        d = n.optString("d"),
        name = n.optString("name"),
        description = n.optString("description"),
        icon = n.optString("icon"),
        // a local napp (installed from files, "local~…") has no author: the
        // backend sends the zero key, which is no one to show or open
        author = n.optString("author").trim('"').takeUnless { it.all { c -> c == '0' } } ?: "",
        authorName = n.optString("authorName"),
        actions = n.strings("actions"),
        requires = n.strings("requires"),
        servers = n.strings("servers"),
        createdAt = n.optLong("created_at"),
        // backend carries newer event object here; presence is flag.
        updateAvailable = n.has("updateAvailable") && !n.isNull("updateAvailable"),
        paths = (0 until pathsArr.length()).map {
            val p = pathsArr.getJSONObject(it)
            NappPath(p.optString("path"), p.optString("sha256"))
        },
    )
}

fun parseActions(arr: JSONArray?): List<BundleAction> {
    if (arr == null) return listOf()
    return (0 until arr.length()).map { j ->
        val a = arr.getJSONObject(j)
        // back to raw JSON: a string payload keeps its quotes
        val payload = when (val p = a.opt("payload")) {
            null, JSONObject.NULL -> ""
            is String -> JSONObject.quote(p)
            else -> p.toString()
        }
        BundleAction(a.optString("type"), payload)
    }
}

private fun parseWindows(arr: JSONArray?): List<WindowInfo> {
    if (arr == null) return listOf()
    return (0 until arr.length()).map { i ->
        val w = arr.getJSONObject(i)
        WindowInfo(
            instance = w.optString("instance"),
            nappId = w.optString("nappId"),
            name = w.optString("name"),
            action = w.optString("action"),
            open = w.optBoolean("open", true),
            history = parseActions(w.optJSONArray("history")),
        )
    }
}

fun parseBundle(b: JSONObject): BundleInfo {
    val entriesArr = b.optJSONArray("entries") ?: JSONArray()
    return BundleInfo(
        name = b.optString("name"),
        entries = (0 until entriesArr.length()).map { i ->
            val e = entriesArr.getJSONObject(i)
            BundleEntry(
                nappId = e.optString("nappId"),
                actions = parseActions(e.optJSONArray("actions")),
            )
        },
        pinned = b.optBoolean("pinned"),
        shortcut = b.optBoolean("shortcut"),
        icon = b.optString("icon"),
    )
}
