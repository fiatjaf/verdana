package com.verdana.app

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.WindowInsetsSides
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.only
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.windowInsetsPadding
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
    private var showSettings by mutableStateOf(false)

    // a page an opened nostr code asks for (see openCode), for the
    // launcher screen to show and clear
    var openedPage by mutableStateOf<Opened?>(null)

    // a nostr: link another app sent us, waiting for the backend to be
    // logged in before it is opened
    var pendingLink by mutableStateOf<String?>(null)

    // the bundles in the Verdana icon's long-press menu (see
    // BundleShortcuts), read again whenever the launcher comes back
    var appMenuBundles by mutableStateOf(setOf<String>())
        private set

    // NIP-55: the launcher this activity hands to Amber while it is in
    // front, so signer requests started anywhere route through the window
    // the user is actually looking at
    private val amberLauncher =
        registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { Amber.handle(it) }

    override fun onResume() {
        super.onResume()
        Amber.attach(amberLauncher)
        appMenuBundles = BundleShortcuts.inAppMenu(this)
    }

    override fun onPause() {
        super.onPause()
        Amber.detach(amberLauncher)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        // the backend is already up (VerdanaApplication starts it before any
        // activity runs); this is just for the case where it could not be
        VerdanaHost.start(this)

        setContent { Launcher() }

        // started by a bundle's shortcut; not again when Android recreates
        // the activity with the same intent
        if (savedInstanceState == null) {
            runBundleIntent(intent)
            takeNostrLink(intent)
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        runBundleIntent(intent)
        takeNostrLink(intent)
    }

    // a nostr:<code> link opened from elsewhere goes the way a pasted code
    // does, once the launcher is logged in (see Launcher)
    private fun takeNostrLink(intent: Intent?) {
        val uri = intent?.data ?: return
        if (intent.action != Intent.ACTION_VIEW || !uri.scheme.equals("nostr", ignoreCase = true)) return
        setIntent(Intent(intent).setAction(Intent.ACTION_MAIN).setData(null))
        pendingLink = uri.toString()
    }

    private fun runBundleIntent(intent: Intent?) {
        val name = BundleShortcuts.bundleOf(intent) ?: return
        setIntent(Intent(intent).setAction(Intent.ACTION_MAIN))
        runBundle(name)
    }

    // ─── launcher actions (called from composables) ──────────────────

    fun login(input: String) = Mobile.login(input)
    fun loginWithGoogle() {
        lifecycleScope.launch { GoogleLogin.login(this@MainActivity) }
    }
    fun logout() = Mobile.logout()
    fun welcome(bootstrap: Boolean) = Mobile.welcome(bootstrap)
    fun fetchNapps() = Mobile.fetch()
    fun fetchContacts() = Mobile.fetchContacts()
    fun saveRelays(text: String) = Mobile.setRelays(text)
    fun install(id: String) = Mobile.install(id)
    // checkUpdate: bring an already previewed napp up to date before opening
    // it (everywhere but its own page, which checks when it opens)
    fun preview(id: String, checkUpdate: Boolean = true) = Mobile.preview(id, checkUpdate)
    fun update(id: String) = Mobile.update(id)
    fun updateAll() = Mobile.updateAll()
    fun uninstall(id: String) = Mobile.uninstall(id)
    fun launch(id: String) = Mobile.launch(id)
    fun pin(id: String) = Mobile.pin(id)
    fun unpin(id: String) = Mobile.unpin(id)
    fun movePin(id: String, to: Int) = Mobile.movePin(id, to.toLong())
    fun pinBundle(name: String) = Mobile.pinBundle(name)
    fun unpinBundle(name: String) = Mobile.unpinBundle(name)
    fun runBundle(name: String) = Mobile.runBundle(name)
    fun reopenWindow(instance: String) = Mobile.reopenWindow(instance)

    // the bundle editor's drafts: a new one from the windows picked on the
    // Sessions tab, or a stored bundle's (see backend/bundle_draft.go)
    fun bundleDraftFromWindows(instances: List<String>): List<BundleDraftEntry> =
        parseBundleDraft(Mobile.bundleDraftFromWindows(org.json.JSONArray(instances).toString()))
    fun bundleDraft(name: String): List<BundleDraftEntry> = parseBundleDraft(Mobile.bundleDraft(name))

    // saveBundle stores the editor's draft off the main thread; onDone gets
    // null when it worked, or what to fix
    fun saveBundle(oldName: String, name: String, draft: List<BundleDraftEntry>, onDone: (String?) -> Unit) {
        lifecycleScope.launch(Dispatchers.IO) {
            val err = try {
                Mobile.saveBundle(oldName, name, bundleDraftJSON(draft))
                null
            } catch (e: Exception) {
                e.message ?: "could not save the bundle"
            }
            // its shortcuts open it by name, and show its napps
            if (err == null && oldName.isNotBlank()) {
                BundleShortcuts.saved(this@MainActivity, oldName, name, draft.map { it.nappId })
            }
            val menu = BundleShortcuts.inAppMenu(this@MainActivity)
            withContext(Dispatchers.Main) { appMenuBundles = menu }
            withContext(Dispatchers.Main) { onDone(err) }
        }
    }

    fun deleteBundle(name: String) {
        lifecycleScope.launch(Dispatchers.IO) {
            try { Mobile.deleteBundle(name) } catch (_: Exception) {}
            BundleShortcuts.deleted(this@MainActivity, name)
            val menu = BundleShortcuts.inAppMenu(this@MainActivity)
            withContext(Dispatchers.Main) { appMenuBundles = menu }
        }
    }

    // a bundle's Android shortcuts: on the phone's home screen, and in the
    // menu of the Verdana icon (see BundleShortcuts)
    fun canPinToHomeScreen() = BundleShortcuts.canPinToHomeScreen(this)

    fun pinToHomeScreen(b: BundleInfo) {
        lifecycleScope.launch(Dispatchers.IO) {
            val ok = try {
                BundleShortcuts.pinToHomeScreen(this@MainActivity, b.name, b.entries.map { it.nappId })
            } catch (_: Exception) {
                false
            }
            if (!ok) withContext(Dispatchers.Main) { toast("The home screen is not taking shortcuts") }
        }
    }

    fun toggleAppMenu(b: BundleInfo) {
        lifecycleScope.launch(Dispatchers.IO) {
            val err = if (b.name in appMenuBundles) {
                BundleShortcuts.removeFromAppMenu(this@MainActivity, b.name)
                null
            } else {
                BundleShortcuts.addToAppMenu(this@MainActivity, b.name, b.entries.map { it.nappId })
            }
            val menu = BundleShortcuts.inAppMenu(this@MainActivity)
            withContext(Dispatchers.Main) {
                appMenuBundles = menu
                if (err != null) toast(err)
            }
        }
    }

    private fun toast(msg: String) = android.widget.Toast.makeText(this, msg, android.widget.Toast.LENGTH_LONG).show()
    fun checkForUpdates() = Mobile.checkForUpdates()
    fun checkForUpdate(id: String) = Mobile.checkForUpdate(id)
    fun toggleTheme() = VerdanaHost.toggleTheme()
    fun setReadAuth(on: Boolean) = Mobile.setReadAuth(on)
    fun setNappPolicy(permission: String, decision: String) = try {
        Mobile.setNappPolicy(permission, decision)
    } catch (e: Exception) {
        toast(e.message ?: "couldn't change that")
    }
    fun setOpenLinks(policy: String) = try {
        Mobile.setOpenLinks(policy)
    } catch (e: Exception) {
        toast(e.message ?: "couldn't change that")
    }
    fun removeDefaultHandler(action: String) = Mobile.removeDefaultHandler(action)
    fun applyConfiguration(key: String) = try {
        Mobile.applyConfiguration(key)
    } catch (e: Exception) {
        toast(e.message ?: "couldn't apply that")
    }

    // bringWindow puts an open napp's task in front — the launcher cannot
    // draw it itself any more, it is a window like any other
    fun bringWindow(instance: String) = VerdanaHost.surface(instance)

    // closeWindow asks a napp to shut down; the backend sends the request
    // back to its window, which finishes itself.
    fun closeWindow(instance: String) = Mobile.closeWindow(instance)

    // fires view:0 with this pubkey's profile, for the user to pick the napp
    // that shows it
    fun viewProfile(pubkeyHex: String) = Mobile.viewProfile(pubkeyHex)

    // openCode works out what a pasted nostr code (or nostr: link) is: a
    // user or a napp is put on openedPage for the launcher to show, any
    // other event the backend already handed to a napp. onDone gets null
    // when it worked, or what went wrong.
    fun openCode(input: String, onDone: (String?) -> Unit) {
        lifecycleScope.launch(Dispatchers.IO) {
            var err: String? = null
            val opened = try {
                parseOpened(Mobile.openCode(input))
            } catch (e: Exception) {
                err = e.message ?: "couldn't open that"
                null
            }
            withContext(Dispatchers.Main) {
                if (opened != null && (opened.kind == "profile" || opened.kind == "napp")) {
                    showProfile = false
                    openedPage = opened
                }
                onDone(err)
            }
        }
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

    fun loadProfile(pubkeyHex: String, onReady: (ProfileDetail?) -> Unit) {
        lifecycleScope.launch(Dispatchers.IO) {
            val p = try {
                parseProfile(Mobile.profile(pubkeyHex))
            } catch (e: Exception) {
                null
            }
            withContext(Dispatchers.Main) { onReady(p) }
        }
    }

    fun loadAuthorNapps(pubkeyHex: String, onReady: (List<Napp>) -> Unit) {
        lifecycleScope.launch(Dispatchers.IO) {
            val list = try {
                parseNapps(Mobile.authorNapps(pubkeyHex))
            } catch (e: Exception) {
                listOf()
            }
            withContext(Dispatchers.Main) { onReady(list) }
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
                // a nostr: link waits for the login to be done with
                val link = pendingLink
                androidx.compose.runtime.LaunchedEffect(link, state.phase) {
                    if (link != null && state.phase == "main") {
                        pendingLink = null
                        openCode(link) { err -> if (err != null) toast(err) }
                    }
                }
                BackHandler(enabled = (showProfile || showSettings) && state.phase == "main" && prompt == null) {
                    showProfile = false
                    showSettings = false
                }
                // edge-to-edge: the surface paints behind the system bars, but
                // the content keeps clear of the navigation bar, cutouts and the
                // keyboard; the top is left to the header, which paints its own
                // background under the status bar
                val hasHeader = state.phase != "login" && state.phase != "loading"
                Column(
                    Modifier
                        .fillMaxSize()
                        .windowInsetsPadding(
                            WindowInsets.safeDrawing.only(WindowInsetsSides.Horizontal + WindowInsetsSides.Bottom)
                        )
                ) {
                    if (hasHeader) {
                        AppHeader(
                            st = state,
                            theme = theme,
                            onActivate = { bringWindow(it) },
                            onClose = { closeWindow(it) },
                            onProfile = { showProfile = true; showSettings = false },
                            onSettings = { showSettings = true; showProfile = false },
                        )
                    }
                    Box(Modifier.weight(1f).then(if (hasHeader) Modifier else Modifier.statusBarsPadding())) {
                        when {
                            state.phase == "login" -> LoginScreen(this@MainActivity, state)
                            state.phase == "welcome" -> WelcomeScreen(this@MainActivity, state)
                            showProfile && state.phase == "main" -> ProfileScreen(this@MainActivity, state)
                            showSettings && state.phase == "main" -> SettingsScreen(this@MainActivity, state)
                            state.phase == "main" -> LauncherScreen(this@MainActivity, state)
                            else -> LoadingScreen()
                        }
                        if (prompt != null) {
                            PromptWindow(prompt) { ok, scope, skip ->
                                VerdanaHost.answer(prompt, ok, scope, skip)
                            }
                        }
                    }
                }
            }
        }
    }
}
