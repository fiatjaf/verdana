package com.verdana.app

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.lifecycle.lifecycleScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import mobile.Mobile

// MainActivity is the Verdana launcher: log in, the installed and discovery
// lists, the profile, and the switcher that brings an open napp's window
// back to the front.
//
// It no longer hosts napps. Each napp is its own window (see NappActivity),
// with its own task and its own card in Recents, so this activity only ever
// shows the launcher — everything the backend says is read from VerdanaHost,
// which outlives any single activity.
class MainActivity : ComponentActivity() {

    private var showProfile by mutableStateOf(false)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        // the backend is already up (VerdanaApplication starts it before any
        // activity runs); this is just for the case where it could not be
        VerdanaHost.start(this)

        setContent { Launcher() }
    }

    // ─── launcher actions (called from composables) ──────────────────

    fun login(input: String) = Mobile.login(input)
    fun logout() = Mobile.logout()
    fun fetchNapps() = Mobile.fetch()
    fun saveRelays(text: String) = Mobile.setRelays(text)
    fun install(id: String) = Mobile.install(id)
    fun update(id: String) = Mobile.update(id)
    fun uninstall(id: String) = Mobile.uninstall(id)
    fun launch(id: String) = Mobile.launch(id)
    fun checkForUpdates() = Mobile.checkForUpdates()
    fun toggleTheme() = VerdanaHost.toggleTheme()

    // bringWindow puts an open napp's task in front — the launcher cannot
    // draw it itself any more, it is a window like any other
    fun bringWindow(instance: String) = VerdanaHost.surface(instance)

    // closeWindow asks a napp to shut down; the backend sends the request
    // back to its window, which finishes itself.
    fun closeWindow(instance: String) = Mobile.closeWindow(instance)

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
    private fun Launcher() {
        val state = VerdanaHost.state
        val theme = themeByName(state.theme)
        // a prompt with no window behind it is the launcher's own question;
        // one a napp asked for is shown over that napp's window instead
        val prompt = VerdanaHost.promptFor("")
        androidx.compose.material3.MaterialTheme(colorScheme = theme.compose()) {
            androidx.compose.material3.Surface(color = theme.bg) {
                BackHandler(enabled = showProfile && state.phase == "main" && prompt == null) {
                    showProfile = false
                }
                Column(Modifier.fillMaxSize()) {
                    if (state.phase != "login" && state.phase != "loading") {
                        AppHeader(
                            st = state,
                            theme = theme,
                            onActivate = { bringWindow(it) },
                            onClose = { closeWindow(it) },
                            onProfile = { showProfile = true },
                        )
                    }
                    Box(Modifier.weight(1f)) {
                        when {
                            state.phase == "login" -> LoginScreen(this@MainActivity, state)
                            showProfile && state.phase == "main" -> ProfileScreen(
                                this@MainActivity,
                                state,
                                onBack = { showProfile = false },
                            )
                            state.phase == "main" -> LauncherScreen(this@MainActivity, state)
                            else -> LoadingScreen()
                        }
                        if (prompt != null) {
                            PromptWindow(prompt) { ok, index, scope ->
                                VerdanaHost.answer(prompt, ok, index, scope)
                            }
                        }
                    }
                }
            }
        }
    }
}
