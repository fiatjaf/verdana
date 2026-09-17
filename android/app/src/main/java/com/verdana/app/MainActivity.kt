package com.verdana.app

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.view.ViewGroup
import android.webkit.WebView
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.lifecycleScope
import androidx.compose.ui.Modifier
import mobile.Mobile
import mobile.UI
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

// MainActivity is the whole Android launcher: it implements the mobile.UI
// interface (the platform half of backend.Host), renders the launcher state
// the backend publishes, and hosts one NappWebView per open napp.
class MainActivity : ComponentActivity(), UI {

    // state is the backend's launcher snapshot, republished as compose state.
    // Every backend callback lands here, so the UI redraws from the source.
    private var state by mutableStateOf(LauncherState(
        phase = "loading", loginErr = "", profileName = "", profilePicture = "",
        pubkey = "", fetchErr = "", fetching = false, theme = "light",
        relays = emptyList(), installed = emptyList(), discovery = emptyList(),
        busy = emptyList(), windows = emptyList(),
    ))

    private var prompt by mutableStateOf<Prompt?>(null)

    // promptShown is true while the prompt is the window on screen. Prompts
    // are just another window here: whenever one appears the UI force-switches
    // to it, and it stays reachable from the window switcher until answered.
    private var promptShown by mutableStateOf(false)

    // tabs is the open napp windows: instance → its webview shell.
    private val tabs = LinkedHashMap<String, NappWebView>()
    private var tabsVersion by mutableStateOf(0)

    private var relaysText by mutableStateOf("")

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        try {
            Mobile.start(filesDir.absolutePath, this)
        } catch (e: Exception) {
            android.util.Log.e("Verdana", "backend failed to start", e)
        }

        relaysText = state.relays.joinToString("\n").ifBlank { "relay.nostrapps.com" }

        // napps can follow the system dark mode from day one
        val initial = if ((resources.configuration.uiMode and
                android.content.res.Configuration.UI_MODE_NIGHT_MASK) ==
            android.content.res.Configuration.UI_MODE_NIGHT_YES
        ) "dark" else "light"
        Mobile.setTheme(initial, themeVarsJSON(themeByName(initial)))
        setCurrentThemeName(initial)

        setContent {
            VerdanaUi()
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        // the webviews die with the activity; tell the backend each window is
        // gone so nothing keeps waiting on it
        for (instance in tabs.keys.toList()) {
            Mobile.windowClosed(instance)
        }
        tabs.values.forEach { it.destroy() }
        tabs.clear()
    }

    // ─── go.mobile.UI ────────────────────────────────────────────────
    // Every one of these may be called from any thread; anything touching
    // compose state or views is posted to the main looper.

    override fun openWindow(instance: String, specJSON: String) {
        runOnUiThread {
            if (tabs.containsKey(instance)) {
                // singleton re-adopt: jump to the one already open
                activateTab(instance)
                return@runOnUiThread
            }
            val spec = WindowSpec(specJSON)
            val shell = NappWebView(this, instance, spec.name, spec)
            (shell.view.parent as? ViewGroup)?.removeView(shell.view)
            tabs[instance] = shell
            tabsVersion++
            // a new window is where the user wants to be: force-jump to it
            // (a pending prompt keeps overlaying whatever is on screen)
            activeTab = instance
            showHome = false
            showProfile = false
        }
    }

    override fun sendToWindow(instance: String, msgJSON: String) {
        runOnUiThread {
            tabs[instance]?.applyMessage(msgJSON)
        }
    }

    override fun closeWindow(instance: String) {
        runOnUiThread {
            tabs[instance]?.applyMessage("""{"t":"close"}""")
        }
    }

    override fun stateChanged() {
        val s = Mobile.state()
        runOnUiThread {
            state = parseState(s)
        }
    }

    override fun promptsChanged() {
        val p = Mobile.currentPrompt()
        runOnUiThread {
            prompt = parsePrompt(p)
            if (prompt != null) {
                // force-show the prompt over the current screen: the napp
                // that fired it is blocked until it is answered
                promptShown = true
                showHome = false
            }
        }
    }

    override fun copyText(text: String) {
        runOnUiThread {
            val cm = getSystemService(CLIPBOARD_SERVICE) as android.content.ClipboardManager
            cm.setPrimaryClip(android.content.ClipData.newPlainText("text", text))
            android.widget.Toast.makeText(this, "Copied", android.widget.Toast.LENGTH_SHORT).show()
        }
    }

    override fun saveFile(name: String, data: ByteArray): String {
        // MediaStore-downloaded folder, no storage permission needed
        val values = android.content.ContentValues().apply {
            put(android.provider.MediaStore.Downloads.DISPLAY_NAME, name)
            put(android.provider.MediaStore.Downloads.MIME_TYPE, mimeGuess(name))
        }
        val resolver = contentResolver
        val uri = resolver.insert(android.provider.MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)
            ?: throw java.io.IOException("could not create download entry")
        resolver.openOutputStream(uri)?.use { it.write(data) }
            ?: throw java.io.IOException("could not open download stream")
        return name
    }

    override fun saveFileTarget(): String = "your Downloads folder"

    override fun openLink(url: String) {
        runOnUiThread {
            try {
                startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)))
            } catch (e: Exception) {
                android.widget.Toast.makeText(this, "No browser to open $url", android.widget.Toast.LENGTH_SHORT).show()
            }
        }
    }

    private fun mimeGuess(name: String): String = when {
        name.endsWith(".html") -> "text/html"
        name.endsWith(".png") -> "image/png"
        name.endsWith(".jpg") || name.endsWith(".jpeg") -> "image/jpeg"
        name.endsWith(".json") -> "application/json"
        name.endsWith(".pdf") -> "application/pdf"
        name.endsWith(".txt") -> "text/plain"
        else -> "application/octet-stream"
    }

    // ─── napp tab management ─────────────────────────────────────────

    fun closeTab(instance: String) {
        runOnUiThread {
            val shell = tabs.remove(instance) ?: return@runOnUiThread
            shell.destroy()
            tabsVersion++
            if (activeTab == instance) activeTab = null
            Mobile.windowClosed(instance)
        }
    }

    fun activateTab(instance: String) {
        activeTab = instance
        promptShown = false
        showHome = false
        showProfile = false
    }

    // showPromptWindow brings the pending prompt back on screen (it is just
    // another window — the user can switch away and return to it).
    fun showPromptWindow() {
        promptShown = true
        showHome = false
        showProfile = false
    }

    // goHomeScreen dismisses the prompt window and lands on the launcher.
    fun goHomeScreen() {
        promptShown = false
        showHome = true
        showProfile = false
    }

    // showProfileScreen lands on the user-details screen (opened by tapping
    // the avatar in the header).
    fun showProfileScreen() {
        promptShown = false
        showProfile = true
    }

    // answerPrompt answers and leaves the prompt window. If the chosen option
    // routes to a window (existing or about to open), we force-jump there.
    fun answerPrompt(p: Prompt, ok: Boolean, index: Int) {
        promptShown = false
        showHome = false
        showProfile = false
        answer(p.id, ok, index)
        if (ok && index >= 0 && index < p.options.size) {
            val opt = p.options[index]
            when {
                opt.instance.isNotBlank() -> activateTab(opt.instance)
                opt.nappId.isNotBlank() ->
                    tabs.entries.firstOrNull { it.value.nappId() == opt.nappId }
                        ?.let { activateTab(it.key) }
            }
        }
    }

    private var activeTab by mutableStateOf<String?>(null)
    private var showHome by mutableStateOf(false)
    private var showProfile by mutableStateOf(false)

    // ─── launcher actions (called from composables) ──────────────────

    fun login(input: String) = Mobile.login(input)
    fun logout() = Mobile.logout()
    fun fetchNapps() = Mobile.fetch()
    fun saveRelays(text: String) {
        relaysText = text
        Mobile.setRelays(text)
    }
    fun install(id: String) = Mobile.install(id)
    fun uninstall(id: String) = Mobile.uninstall(id)
    fun launch(id: String) = Mobile.launch(id)
    fun checkForUpdates() = Mobile.checkForUpdates()
    fun answer(id: Long, ok: Boolean, index: Int) = Mobile.answerPrompt(id, ok, index.toLong())
    fun toggleTheme() {
        val next = if (state.theme == "dark") "light" else "dark"
        Mobile.setTheme(next, themeVarsJSON(themeByName(next)))
        setCurrentThemeName(next)
    }

    fun loadIcon(napp: Napp, onReady: (ByteArray?) -> Unit) {
        lifecycleScope.launch(Dispatchers.IO) {
            val bytes = try {
                Mobile.nappIcon(napp.id)
            } catch (e: Exception) {
                null
            }
            withContext(Dispatchers.Main) { onReady(bytes) }
        }
    }

    // ─── the tree ────────────────────────────────────────────────────

    @androidx.compose.runtime.Composable
    private fun VerdanaUi() {
        val theme = themeByName(state.theme)
        androidx.compose.material3.MaterialTheme(colorScheme = theme.compose()) {
            androidx.compose.material3.Surface(color = theme.bg) {
                // browser-like frame: the header (tab count + app name +
                // current window id + user picture) is always on top, and the
                // body shows whichever "window" is current — a napp, the
                // profile screen, the prompt window, or the launcher home.
                tabsVersion.let { _ -> // recompose when the tab set changes
                    val showingPrompt = promptShown && prompt != null
                    val currentShell = activeTab?.let { tabs[it] }
                    val currentInstance = activeTab?.takeIf { tabs.containsKey(it) }
                    val bodyShowingNapp = currentShell != null && !showHome && !showProfile
                    val subtitle = when {
                        showProfile && state.phase == "main" -> "profile"
                        bodyShowingNapp -> currentInstance.orEmpty()
                        state.phase == "main" -> "launcher"
                        else -> ""
                    }
                    Column(Modifier.fillMaxSize()) {
                        if (state.phase != "login" && state.phase != "loading") {
                            AppHeader(
                                st = state,
                                subtitle = subtitle,
                                promptActive = showingPrompt,
                                theme = theme,
                                onHome = { goHomeScreen() },
                                onShowPrompt = { showPromptWindow() },
                                onActivate = { activateTab(it) },
                                onClose = { closeTab(it) },
                                onProfile = { showProfileScreen() },
                            )
                        }
                        Box(Modifier.weight(1f)) {
                            when {
                                bodyShowingNapp -> NappScreen(currentShell!!)
                                state.phase == "login" -> LoginScreen(this@MainActivity, state)
                                showProfile && state.phase == "main" -> ProfileScreen(
                                    this@MainActivity,
                                    state,
                                    onBack = { goHomeScreen() },
                                )
                                state.phase == "main" -> LauncherScreen(this@MainActivity, state)
                                else -> LoadingScreen()
                            }
                            // the prompt is an overlay covering the window it
                            // came from (or whatever is on screen now)
                            if (showingPrompt) {
                                PromptWindow(
                                    prompt!!,
                                    onAnswer = { ok, index -> answerPrompt(prompt!!, ok, index) },
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}
