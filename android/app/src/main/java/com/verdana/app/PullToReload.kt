package com.verdana.app

import android.annotation.SuppressLint
import android.content.Context
import android.os.SystemClock
import android.view.MotionEvent
import android.view.View
import android.view.ViewGroup
import androidx.compose.ui.graphics.toArgb
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout

// PullToReload wraps a napp window's view in the usual pull-down-to-refresh
// gesture. What a refresh does is up to the window (it reloads the napp, see
// NappActivity.reload); this only decides when a pull is a pull.
//
// That is the hard part: a downward drag is also how a napp scrolls, or
// pans a map, or drags whatever it draws.
//   - a WebView only knows whether its document is scrolled, not whether the
//     element under the finger is (napps tend to scroll an inner container),
//     so the page tells us on every touchstart (see NappWebView's pull
//     script) and verdict() files the answer. A page that never answers
//     (broken, still loading) falls back to the document's own scroll, so a
//     stuck napp can still be reloaded.
//   - a wasm canvas gets every touch as its own pointer events, so a pull
//     there only counts when it starts at the very top edge.
@SuppressLint("ViewConstructor")
class PullToReload(
    context: Context,
    private val child: View,
    private val edgeOnly: Boolean,
    onReload: () -> Unit,
) : SwipeRefreshLayout(context) {

    private enum class Verdict { PENDING, ALLOWED, BLOCKED }

    private var verdict = Verdict.ALLOWED
    private var downAt = 0L
    private var downY = 0f

    private val edge = EDGE_DP * resources.displayMetrics.density

    init {
        addView(
            child,
            ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT),
        )
        setOnRefreshListener { onReload() }
        setOnChildScrollUpCallback { _, _ -> !pullable() }
    }

    // verdict is the page's answer to the touch that just went down: whether
    // whatever is under the finger is at its top and fine to be pulled.
    fun verdict(allowed: Boolean) {
        if (verdict == Verdict.PENDING) verdict = if (allowed) Verdict.ALLOWED else Verdict.BLOCKED
    }

    override fun onInterceptTouchEvent(ev: MotionEvent): Boolean {
        if (ev.actionMasked == MotionEvent.ACTION_DOWN) {
            downAt = SystemClock.uptimeMillis()
            downY = ev.y
            // the theme may have changed since the window opened
            val theme = themeByName(VerdanaHost.state.theme)
            setColorSchemeColors(theme.accent.toArgb())
            setProgressBackgroundColorSchemeColor(theme.card.toArgb())
            // The down itself must always get through: SwipeRefreshLayout
            // only records where a gesture starts when the child can't
            // scroll up at that moment, and without that start every move
            // after it is ignored. The page's verdict only matters for the
            // moves, which is also when it has had the time to arrive.
            verdict = Verdict.ALLOWED
            val intercepted = super.onInterceptTouchEvent(ev)
            if (!edgeOnly) verdict = Verdict.PENDING
            return intercepted
        }
        return super.onInterceptTouchEvent(ev)
    }

    private fun pullable(): Boolean {
        if (edgeOnly) return downY <= edge
        if (child.canScrollVertically(-1)) return false
        return when (verdict) {
            Verdict.ALLOWED -> true
            Verdict.BLOCKED -> false
            Verdict.PENDING -> SystemClock.uptimeMillis() - downAt > VERDICT_WAIT_MS
        }
    }

    companion object {
        // how far from the top a pull on a canvas may start
        private const val EDGE_DP = 48

        // how long a touch waits on the page's verdict before going by the
        // document's scroll alone
        private const val VERDICT_WAIT_MS = 150L
    }
}
