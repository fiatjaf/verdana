package com.verdana.app

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.AndroidView

// NappScreen is one open napp: just the napp's WebView filling the body under
// the shared AppHeader. The WebView itself was created by the backend's
// OpenWindow callback; this only puts it on screen.
@Composable
fun NappScreen(shell: NappWebView) {
    AndroidView(
        factory = { shell.view },
        modifier = Modifier.fillMaxSize(),
    )
}
