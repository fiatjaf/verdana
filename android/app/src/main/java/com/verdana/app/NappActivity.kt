package com.verdana.app

import android.app.ActivityManager
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.os.Bundle
import android.util.Log
import android.view.View
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.lifecycleScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import mobile.Mobile

// NappActivity is one napp's window: its own task, its own card in Recents,
// its own WebView filling the screen (or, for a wasm napp, the NappCanvas its
// module paints). Android keys the task off the intent's
// data (verdana://napp/<instance>), so launching a different instance gets a
// window of its own and launching the same one brings this window back
// instead of opening a second.
//
// A window carries no launcher chrome: it is a napp, not a tab inside a
// browser. Back walks the napp's own history and then leaves the window; the
// only thing that ever covers a napp is a prompt it has to answer.
//
// Android may kill the process with windows still in Recents, and bring one
// back later as a fresh activity in a fresh process, where the backend has
// no such window open. That window recovers: the backend opens its instance
// again from the record it kept on disk, and replays the actions it was sent
// to put it back where it was (see backend/window_records.go).
class NappActivity : ComponentActivity() {

    lateinit var instance: String
        private set

    lateinit var nappId: String
        private set

    // one of the two, depending on the napp's mode
    private var shell: NappWebView? = null
    private var canvas: NappCanvas? = null

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

    // recovering is a window Android restored after killing the process,
    // waiting for the backend to open it again (see recover)
    private var recovering = false

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

        if (!Mobile.knowsWindow(instance)) {
            recover(spec)
            return
        }
        build(spec)
    }

    // recover is a window the backend doesn't have open: Android restored it
    // after killing the process (or after destroying the activity on its
    // own). It shows nothing but the theme until the backend opens the
    // instance again, which lands in rebuild with a fresh spec; with nothing
    // to rebuild it from, the window just goes.
    private fun recover(spec: WindowSpec) {
        describeTask(spec)
        reopen()
    }

    // reload is the user pulling the window down: the napp starts over, as
    // if Android had killed it and the window were being recovered — a fresh
    // page (or a fresh wasm module) under the same instance, put back where
    // it was by replaying the actions it was sent, like the desktop's reload.
    private fun reload() {
        if (recovering || !ownsWindow || isFinishing || isDestroyed) return
        Log.i("Verdana", "reloading window $instance")
        shell?.destroy()
        shell = null
        canvas?.destroy()
        canvas = null
        ownsWindow = false
        VerdanaHost.unregister(instance, this)
        try {
            Mobile.windowClosed(instance)
        } catch (e: Exception) {
            Log.e("Verdana", "could not close window $instance for a reload", e)
        }
        reopen()
    }

    // reopen has the backend open this window's instance again, from the
    // record it keeps, and waits for that to land in rebuild.
    private fun reopen() {
        recovering = true
        setContent {
            Surface(color = themeByName(VerdanaHost.state.theme).bg, modifier = Modifier.fillMaxSize()) {}
        }
        VerdanaHost.awaitRecovery(this)
        lifecycleScope.launch(Dispatchers.IO) {
            val failed = try {
                Mobile.recoverWindow(instance)
                false
            } catch (e: Exception) {
                Log.w("Verdana", "window $instance can't be recovered", e)
                true
            }
            if (failed) withContext(Dispatchers.Main) {
                VerdanaHost.stopRecovery(this@NappActivity, gaveUp = false)
                finish()
            }
        }
    }

    // rebuild is the backend opening a recovering window's instance again:
    // the window is built from this spec, not the stale one it was restored
    // with, which also becomes what the intent carries from now on.
    fun rebuild(specJSON: String) {
        if (!recovering || isFinishing || isDestroyed) return
        VerdanaHost.stopRecovery(this, gaveUp = false)
        val spec = try {
            WindowSpec(specJSON)
        } catch (e: Exception) {
            Log.e("Verdana", "unreadable window spec for $instance", e)
            null
        }
        recovering = false
        if (spec == null) {
            Mobile.windowClosed(instance)
            finish()
            return
        }
        intent = Intent(intent).putExtra(EXTRA_SPEC, specJSON)
        build(spec)
    }

    private fun build(spec: WindowSpec) {
        // The view goes up before the claim is made: claiming hands over
        // whatever the backend sent while this window was starting, and that
        // has to land on a webview that already exists.
        val web: NappWebView?
        val view: View
        if (spec.mode == "wasm") {
            web = null
            canvas = NappCanvas(
                context = this,
                instance = instance,
                background = themeByName(VerdanaHost.state.theme).bg.toArgb(),
                onFail = { msg ->
                    Toast.makeText(this, "${spec.name.ifBlank { "The napp" }}: $msg", Toast.LENGTH_LONG).show()
                    finishWindow()
                },
            )
            view = canvas!!
        } else {
            web = NappWebView(
                activity = this,
                instance = instance,
                name = spec.name,
                spec = spec,
                onClose = { finishWindow() },
            )
            view = web.view
        }

        // intoExisting should have brought this window back instead of
        // making a second one; if a duplicate task showed up anyway, hand
        // the user to the window that is really running the napp and leave.
        if (!VerdanaHost.claim(this)) {
            web?.destroy()
            canvas?.destroy()
            canvas = null
            window.decorView.post { VerdanaHost.surface(instance) }
            finish()
            return
        }
        ownsWindow = true
        shell = web

        describeTask(spec)

        // a canvas has no history: back leaves the window
        val back = {
            if (web != null && web.view.canGoBack()) web.view.goBack() else finish()
        }
        val pull = PullToReload(this, view, edgeOnly = web == null) { reload() }
        web?.onPullVerdict = { pull.verdict(it) }
        setContent { NappWindow(pull, back) }
    }

    // onNewIntent is what intoExisting delivers when the same instance is
    // launched again: the window was already there, so all that is left is
    // to come back to the front.
    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        // a launch of this instance while it waits to be recovered carries
        // the spec it has to be built from
        if (recovering) intent.getStringExtra(EXTRA_SPEC)?.let { rebuild(it) }
    }

    override fun onDestroy() {
        super.onDestroy()
        if (recovering) {
            recovering = false
            VerdanaHost.stopRecovery(this, gaveUp = !isChangingConfigurations)
        }
        shell?.destroy()
        shell = null
        canvas?.destroy()
        canvas = null
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

    // wasmInvalidate is the backend saying a wasm napp has something new to
    // show.
    fun wasmInvalidate() {
        canvas?.requestFrame()
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
    private fun NappWindow(view: View, back: () -> Unit) {
        val theme = themeByName(VerdanaHost.state.theme)
        val prompt = VerdanaHost.promptFor(instance)
        MaterialTheme(colorScheme = theme.compose()) {
            Surface(color = theme.bg, modifier = Modifier.fillMaxSize()) {
                // a prompt is a blocking question: back must not dismiss it
                BackHandler(enabled = prompt != null) {}
                BackHandler(enabled = prompt == null) { back() }

                Box(Modifier.fillMaxSize()) {
                    // edge-to-edge means adjustResize does nothing on its own:
                    // the napp has to keep clear of the keyboard itself, or
                    // it ends up drawn underneath it
                    AndroidView(
                        factory = { view },
                        modifier = Modifier.fillMaxSize().safeDrawingPadding(),
                    )
                    if (prompt != null) {
                        // the prompt covers the whole window, bars included:
                        // it is a blocking question, not a dialog over the
                        // napp's own chrome
                        Box(Modifier.fillMaxSize().safeDrawingPadding()) {
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
