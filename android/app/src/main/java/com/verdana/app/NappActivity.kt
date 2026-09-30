package com.verdana.app

import android.app.ActivityManager
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.os.Bundle
import android.util.Log
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.systemBarsPadding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.lifecycleScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import mobile.Mobile

// NappActivity is one napp's window: its own task, its own card in Recents,
// its own WebView filling the screen. Android keys the task off the intent's
// data (verdana://napp/<instance>), so launching a different instance gets a
// window of its own and launching the same one brings this window back
// instead of opening a second.
//
// A window carries no launcher chrome: it is a napp, not a tab inside a
// browser. Back walks the napp's own history and then leaves the window; the
// only thing that ever covers a napp is a prompt it has to answer.
class NappActivity : ComponentActivity() {

    lateinit var instance: String
        private set

    lateinit var nappId: String
        private set

    private var shell: NappWebView? = null

    // NIP-55: while this window is in front it is the one forwarding signer
    // requests, whatever napp asked for them
    private val amberLauncher =
        registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { Amber.handle(it) }

    override fun onResume() {
        super.onResume()
        visible = true
        Amber.attach(amberLauncher)
    }

    override fun onPause() {
        super.onPause()
        visible = false
        Amber.detach(amberLauncher)
    }

    // ownsWindow says this activity is the window the backend talks to. A
    // task can be created for an instance that is already up — that one is a
    // duplicate, and closing it must not take the real window with it.
    private var ownsWindow = false

    // visible is whether this window is the one in front, which is not the
    // same as "has been resumed at some point"
    private var visible = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

        instance = intent?.data?.lastPathSegment.orEmpty()
        if (instance.isBlank()) {
            finish()
            return
        }

        // the spec rides along in the intent, so a task Android restored
        // after killing the process still knows what to serve
        val spec = windowSpec(intent)
        if (spec == null) {
            finish()
            return
        }
        nappId = spec.nappId

        // The webview goes up before the claim is made: claiming hands over
        // whatever the backend sent while this window was starting, and that
        // has to land on a webview that already exists.
        val web = NappWebView(
            activity = this,
            instance = instance,
            name = spec.name,
            spec = spec,
            onClose = { finishWindow() },
        )

        // intoExisting should have brought this window back instead of
        // making a second one; if a duplicate task showed up anyway, hand
        // the user to the window that is really running the napp and leave.
        if (!VerdanaHost.claim(this)) {
            web.destroy()
            window.decorView.post { VerdanaHost.surface(instance) }
            finish()
            return
        }
        ownsWindow = true
        shell = web

        describeTask(spec)

        setContent { NappWindow(web) }
    }

    // onNewIntent is what intoExisting delivers when the same instance is
    // launched again: the window was already there, so all that is left is
    // to come back to the front.
    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
    }

    override fun onDestroy() {
        super.onDestroy()
        shell?.destroy()
        shell = null
        if (!ownsWindow) return
        ownsWindow = false
        VerdanaHost.unregister(instance, this)
        // the window is really gone: whatever was waiting on it stops
        // waiting. A config change is not that — the window comes right
        // back with the same instance and the same spec.
        if (isChangingConfigurations) return
        try {
            Mobile.windowClosed(instance)
        } catch (e: Exception) {
            Log.e("Verdana", "could not tell the backend the window closed", e)
        }
    }

    // deliver is one wire message from the backend for this window.
    fun deliver(msgJSON: String) {
        runOnUiThread { shell?.applyMessage(msgJSON) }
    }

    // onScreen says whether the user is looking at this window right now.
    fun onScreen(): Boolean = !isFinishing && !isDestroyed && visible

    // finishWindow is the close path from either side: the napp asking to go
    // away, or the backend closing the window.
    fun finishWindow() {
        runOnUiThread { if (!isFinishing) finish() }
    }

    // describeTask is what the window looks like in Recents: the napp's own
    // name and icon, not Verdana's. The icon is a round trip through the
    // backend (disk, or the author's blossom servers), so the card shows the
    // name first and picks the icon up when it turns up.
    private fun describeTask(spec: WindowSpec) {
        val label: String = spec.name.ifBlank { spec.nappId.ifBlank { "Verdana" } }
        applyTaskDescription(label, null)

        lifecycleScope.launch(Dispatchers.IO) {
            val bytes = try {
                Mobile.nappIcon(spec.nappId)
            } catch (e: Exception) {
                null
            }
            val icon = bytes?.let { BitmapFactory.decodeByteArray(it, 0, it.size) }
            withContext(Dispatchers.Main) {
                if (!isFinishing && !isDestroyed) applyTaskDescription(label, icon)
            }
        }
    }

    private fun applyTaskDescription(label: String, icon: Bitmap?) {
        try {
            @Suppress("DEPRECATION")
            setTaskDescription(ActivityManager.TaskDescription(label, icon, 0))
        } catch (e: Exception) {
            Log.w("Verdana", "no task description for $instance", e)
        }
    }

    private fun windowSpec(intent: Intent?): WindowSpec? {
        val json = intent?.getStringExtra(EXTRA_SPEC) ?: return null
        return try {
            WindowSpec(json)
        } catch (e: Exception) {
            Log.e("Verdana", "unreadable window spec for $instance", e)
            null
        }
    }

    @androidx.compose.runtime.Composable
    private fun NappWindow(web: NappWebView) {
        val theme = themeByName(VerdanaHost.state.theme)
        val prompt = VerdanaHost.promptFor(instance)
        MaterialTheme(colorScheme = theme.compose()) {
            Surface(color = theme.bg, modifier = Modifier.fillMaxSize()) {
                // a prompt is a blocking question: back must not dismiss it
                BackHandler(enabled = prompt != null) {}
                BackHandler(enabled = prompt == null) {
                    if (web.view.canGoBack()) web.view.goBack() else finish()
                }

                Box(Modifier.fillMaxSize()) {
                    AndroidView(
                        factory = { web.view },
                        modifier = Modifier.fillMaxSize().systemBarsPadding(),
                    )
                    if (prompt != null) {
                        // the prompt covers the whole window, bars included:
                        // it is a blocking question, not a dialog over the
                        // napp's own chrome
                        Box(Modifier.fillMaxSize().systemBarsPadding()) {
                            PromptWindow(prompt) { ok, index, scope ->
                                VerdanaHost.answer(prompt, ok, index, scope)
                            }
                        }
                    }
                }
            }
        }
    }

    companion object {
        const val EXTRA_SPEC = "com.verdana.app.SPEC"
    }
}
