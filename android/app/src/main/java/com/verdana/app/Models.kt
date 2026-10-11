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
// Windows tab can reopen; history is every action it was sent this run (only
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
)

data class Prompt(
    val id: Long,
    val title: String,
    val detail: String,
    val code: String,
    val napp: String,
    val options: List<PromptOption>,
    // remember says the answer can stick: the launcher then offers this
    // prompt only, this session, and always, and files the wider answers away.
    val remember: Boolean = false,
    // instance is the window the prompt belongs over, when a napp fired it.
    // Blank for questions the launcher asked itself.
    val instance: String = "",
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
    val relays: List<String>,
    val installed: List<Napp>,
    val discovery: List<Napp>,
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
        relays = (0 until relaysArr.length()).map { relaysArr.getString(it) },
        installed = napps("installed"),
        discovery = napps("discovery"),
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
            )
        },
        instance = o.optString("instance"),
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
        author = n.optString("author").trim('"'),
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
    )
}
