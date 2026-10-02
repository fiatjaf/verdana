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
    // "napplet" for a kind:35129 napplet, "" for a napp
    val format: String = "",
    // a napplet's icon is a bare blob hash, not one of its paths
    val iconSha: String = "",
    val archetypes: List<String> = listOf(),
) {
    val isNapplet: Boolean get() = format == "napplet"
}

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

data class WindowInfo(val instance: String, val nappId: String, val name: String, val action: String)

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

data class LauncherState(
    val phase: String,
    val loginErr: String,
    val profileName: String,
    val profilePicture: String,
    val pubkey: String,
    val fetchErr: String,
    val fetching: Boolean,
    val theme: String,
    val themeMode: String,
    val relays: List<String>,
    val installed: List<Napp>,
    val discovery: List<Napp>,
    val busy: List<String>,
    val windows: List<WindowInfo>,
    val updateCheckRunning: Boolean = false,
    val lookup: AddressLookup? = null,
    // nostrConnectUri is the nostrconnect:// uri the login screen shows as a
    // QR code for a NIP-46 signer, nostrConnectRelay the relay it names.
    val nostrConnectUri: String = "",
    val nostrConnectRelay: String = "",
)

// AddressLookup is the napp address typed into the discovery filter being
// looked up on relays: pending, found (nappId, also in discovery) or failed.
data class AddressLookup(
    val query: String,
    val pending: Boolean,
    val nappId: String,
    val err: String,
)

fun parseState(json: String): LauncherState {
    val o = JSONObject(json)
    fun napps(key: String): List<Napp> {
        val arr = o.optJSONArray(key) ?: JSONArray()
        return (0 until arr.length()).map { i ->
            val n = arr.getJSONObject(i)
            val pathsArr = n.optJSONArray("paths") ?: JSONArray()
            Napp(
                id = n.optString("id"),
                d = n.optString("d"),
                name = n.optString("name"),
                description = n.optString("description"),
                icon = n.optString("icon"),
                author = n.optString("author").trim('"'),
                authorName = n.optString("authorName"),
                actions = (0 until (n.optJSONArray("actions")?.length() ?: 0)).map {
                    n.optJSONArray("actions")!!.getString(it)
                },
                requires = (0 until (n.optJSONArray("requires")?.length() ?: 0)).map {
                    n.optJSONArray("requires")!!.getString(it)
                },
                servers = (0 until (n.optJSONArray("servers")?.length() ?: 0)).map {
                    n.optJSONArray("servers")!!.getString(it)
                },
                createdAt = n.optLong("created_at"),
                // backend carries newer event object here; presence is flag.
                updateAvailable = n.has("updateAvailable") && !n.isNull("updateAvailable"),
                paths = (0 until pathsArr.length()).map {
                    val p = pathsArr.getJSONObject(it)
                    NappPath(p.optString("path"), p.optString("sha256"))
                },
                format = n.optString("format"),
                iconSha = n.optString("iconSha"),
                archetypes = (0 until (n.optJSONArray("conventions")?.length() ?: 0)).mapNotNull {
                    n.optJSONArray("conventions")?.optJSONObject(it)?.optString("id")
                        ?.removePrefix("napplet:")?.substringBefore('/')?.takeIf(String::isNotBlank)
                }.distinct(),
            )
        }
    }

    val windowsArr = o.optJSONArray("windows") ?: JSONArray()
    val busyArr = o.optJSONArray("busy") ?: JSONArray()
    val relaysArr = o.optJSONArray("relays") ?: JSONArray()
    return LauncherState(
        phase = o.optString("phase"),
        loginErr = o.optString("loginErr"),
        profileName = o.optString("profileName"),
        profilePicture = o.optString("profilePicture"),
        pubkey = o.optString("pubkey"),
        fetchErr = o.optString("fetchErr"),
        fetching = o.optBoolean("fetching"),
        theme = o.optString("theme", "light"),
        themeMode = o.optString("themeMode", "system"),
        relays = (0 until relaysArr.length()).map { relaysArr.getString(it) },
        installed = napps("installed"),
        discovery = napps("discovery"),
        busy = (0 until busyArr.length()).map { busyArr.getString(it) },
        updateCheckRunning = o.optBoolean("updateCheckRunning"),
        nostrConnectUri = o.optString("nostrConnectUri"),
        nostrConnectRelay = o.optString("nostrConnectRelay"),
        lookup = o.optJSONObject("lookup")?.let { l ->
            AddressLookup(
                query = l.optString("query"),
                pending = l.optBoolean("pending"),
                nappId = l.optString("nappId"),
                err = l.optString("err"),
            )
        },
        windows = (0 until windowsArr.length()).map { i ->
            val w = windowsArr.getJSONObject(i)
            WindowInfo(
                instance = w.optString("instance"),
                nappId = w.optString("nappId"),
                name = w.optString("name"),
                action = w.optString("action"),
            )
        },
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
    val dir: String
    val requires: List<String>
    // "napplet": the window loads the napplet host page, not the napp's files
    val format: String
    val theme: String
    val themeVars: String
    val storage: String

    init {
        val o = JSONObject(json)
        instance = o.optString("instance")
        nappId = o.optString("nappId")
        name = o.optString("name")
        description = o.optString("description")
        dir = o.optString("dir")
        requires = (0 until (o.optJSONArray("requires")?.length() ?: 0)).map {
            o.optJSONArray("requires")!!.getString(it)
        }
        format = o.optString("format")
        theme = o.optString("theme", "light")
        themeVars = o.optString("themeVars", "{}")
        storage = o.optString("storage", "{}").ifBlank { "{}" }
    }

    val isNapplet: Boolean get() = format == "napplet"
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
        (0 until arr.length()).map { i ->
            val n = arr.getJSONObject(i)
            val pathsArr = n.optJSONArray("paths") ?: JSONArray()
            Napp(
                id = n.optString("id"),
                d = n.optString("d"),
                name = n.optString("name"),
                description = n.optString("description"),
                icon = n.optString("icon"),
                author = n.optString("author").trim('"'),
                authorName = n.optString("authorName"),
                actions = (0 until (n.optJSONArray("actions")?.length() ?: 0)).map {
                    n.optJSONArray("actions")!!.getString(it)
                },
                requires = (0 until (n.optJSONArray("requires")?.length() ?: 0)).map {
                    n.optJSONArray("requires")!!.getString(it)
                },
                servers = (0 until (n.optJSONArray("servers")?.length() ?: 0)).map {
                    n.optJSONArray("servers")!!.getString(it)
                },
                createdAt = n.optLong("created_at"),
                updateAvailable = n.has("updateAvailable") && !n.isNull("updateAvailable"),
                paths = (0 until pathsArr.length()).map {
                    val q = pathsArr.getJSONObject(it)
                    NappPath(q.optString("path"), q.optString("sha256"))
                },
                format = n.optString("format"),
                iconSha = n.optString("iconSha"),
            )
        }
    } catch (_: Exception) { listOf() }
}
