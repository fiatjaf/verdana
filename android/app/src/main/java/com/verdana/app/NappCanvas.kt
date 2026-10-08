package com.verdana.app

import android.annotation.SuppressLint
import android.content.Context
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.Rect
import android.os.Handler
import android.os.HandlerThread
import android.view.Choreographer
import android.view.InputDevice
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.View
import mobile.Mobile
import java.nio.ByteBuffer
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.math.roundToInt

// A NappCanvas is a wasm napp's window on Android: a View showing the canvas
// the napp's module paints. The module itself runs in Go (backend/mobile's
// wasm.go, the same host the desktop's canvas shell uses); this asks it for a
// frame whenever there is reason to, shows the pixels it hands back, and
// passes the touches and keys on.
//
// Everything that runs the module happens on the window's own thread, never
// the main one: a napp that is slow, or stuck in a loop, is its own window
// freezing, not the whole launcher.
//
// The canvas is in dp, not in device pixels. A canvas napp has no idea of
// screen density, so at the phone's real resolution it would draw the size
// of a stamp; at dp it draws the size it would in a desktop window, and the
// bitmap is scaled up to the view.
@SuppressLint("ViewConstructor")
class NappCanvas(
    context: Context,
    val instance: String,
    private val background: Int,
    private val onFail: (String) -> Unit,
) : View(context) {

    private val thread = HandlerThread("napp-$instance").apply { start() }
    private val worker = Handler(thread.looper)

    // device pixels per canvas pixel
    private val scale = resources.displayMetrics.density

    // the canvas size, in canvas pixels; read by the worker
    @Volatile private var canvasW = 0
    @Volatile private var canvasH = 0

    @Volatile private var gone = false

    // one frame queued at a time: everything asking for a frame before it
    // runs is answered by that same frame
    private val queued = AtomicBoolean(false)

    // main thread only
    private var bitmap: Bitmap? = null
    private val paint = Paint(Paint.FILTER_BITMAP_FLAG)
    private val dst = Rect()

    init {
        isFocusable = true
        isFocusableInTouchMode = true
        setBackgroundColor(background)
    }

    // requestFrame asks for one more frame, from any thread.
    fun requestFrame() {
        if (gone) return
        if (queued.compareAndSet(false, true)) worker.post { frame() }
    }

    // frame runs on the worker.
    private fun frame() {
        queued.set(false)
        if (gone) return
        val w = canvasW
        val h = canvasH
        if (w <= 0 || h <= 0) return

        val pixels = try {
            Mobile.wasmFrame(instance, w.toLong(), h.toLong())
        } catch (e: Exception) {
            // a window on its way out has nothing left to say
            if (gone) return
            gone = true
            post { onFail(e.message ?: "the napp stopped") }
            return
        }
        val animating = Mobile.wasmAnimating(instance)
        if (pixels == null || pixels.size != w * h * 4) return

        post { show(pixels, w, h, animating) }
    }

    // show puts a frame on screen, on the main thread. An animating napp
    // gets its next frame on the next vsync, which is what keeps it from
    // spinning faster than the screen can show.
    private fun show(pixels: ByteArray, w: Int, h: Int, animating: Boolean) {
        if (gone) return
        var b = bitmap
        if (b == null || b.width != w || b.height != h) {
            b?.recycle()
            b = Bitmap.createBitmap(w, h, Bitmap.Config.ARGB_8888)
            bitmap = b
        }
        // ARGB_8888 is R, G, B, A in memory, alpha premultiplied: the
        // canvas byte for byte
        b!!.copyPixelsFromBuffer(ByteBuffer.wrap(pixels))
        invalidate()
        if (animating) Choreographer.getInstance().postFrameCallback { requestFrame() }
    }

    override fun onDraw(canvas: Canvas) {
        super.onDraw(canvas)
        val b = bitmap ?: return
        dst.set(0, 0, (b.width * scale).roundToInt(), (b.height * scale).roundToInt())
        canvas.drawBitmap(b, null, dst, paint)
    }

    override fun onSizeChanged(w: Int, h: Int, oldw: Int, oldh: Int) {
        super.onSizeChanged(w, h, oldw, oldh)
        canvasW = (w / scale).roundToInt().coerceAtLeast(1)
        canvasH = (h / scale).roundToInt().coerceAtLeast(1)
        requestFrame()
    }

    // ─── input ───────────────────────────────────────────────────────

    // input hands the module one event on the worker, after whatever frame
    // is running, and asks for the frame that shows what it did.
    private fun input(kind: Long, x: Int, y: Int, buttons: Long, a: Double = 0.0, b: Double = 0.0) {
        if (gone) return
        worker.post { Mobile.wasmInput(instance, kind, x.toLong(), y.toLong(), buttons, a, b) }
        requestFrame()
    }

    private fun text(s: String) {
        if (gone || s.isEmpty()) return
        worker.post { Mobile.wasmText(instance, s) }
        requestFrame()
    }

    private fun cx(px: Float) = (px / scale).roundToInt()

    // A finger is a mouse with its primary button held: it moves there,
    // presses, drags, lets go, and then leaves, so the napp does not keep
    // showing a hover where nothing is hovering any more.
    @SuppressLint("ClickableViewAccessibility")
    override fun onTouchEvent(e: MotionEvent): Boolean {
        val x = cx(e.getX(0))
        val y = cx(e.getY(0))
        when (e.actionMasked) {
            MotionEvent.ACTION_DOWN -> {
                requestFocus()
                input(Mobile.EventPointerMove, x, y, 0)
                input(Mobile.EventPointerDown, x, y, Mobile.ButtonPrimary)
            }
            MotionEvent.ACTION_MOVE -> input(Mobile.EventPointerMove, x, y, Mobile.ButtonPrimary)
            MotionEvent.ACTION_UP -> {
                input(Mobile.EventPointerUp, x, y, 0)
                input(Mobile.EventPointerMove, -1, -1, 0)
            }
            MotionEvent.ACTION_CANCEL -> {
                input(Mobile.EventPointerUp, -1, -1, 0)
                input(Mobile.EventPointerMove, -1, -1, 0)
            }
        }
        return true
    }

    // a mouse wheel (or a touchpad), for a phone with one plugged in. The
    // ABI scrolls the browser's way: positive is down and right.
    override fun onGenericMotionEvent(e: MotionEvent): Boolean {
        if (e.isFromSource(InputDevice.SOURCE_CLASS_POINTER)) {
            when (e.actionMasked) {
                MotionEvent.ACTION_SCROLL -> {
                    val h = e.getAxisValue(MotionEvent.AXIS_HSCROLL) * SCROLL_STEP
                    val v = -e.getAxisValue(MotionEvent.AXIS_VSCROLL) * SCROLL_STEP
                    input(Mobile.EventScroll, cx(e.x), cx(e.y), 0, h.toDouble(), v.toDouble())
                    return true
                }
                MotionEvent.ACTION_HOVER_MOVE -> {
                    input(Mobile.EventPointerMove, cx(e.x), cx(e.y), 0)
                    return true
                }
                MotionEvent.ACTION_HOVER_EXIT -> {
                    input(Mobile.EventPointerMove, -1, -1, 0)
                    return true
                }
            }
        }
        return super.onGenericMotionEvent(e)
    }

    // A hardware keyboard: a key event for every key the ABI can name, and a
    // text event for what it typed.
    override fun onKeyDown(keyCode: Int, e: KeyEvent): Boolean {
        val code = nappKey(e) ?: return super.onKeyDown(keyCode, e)
        input(Mobile.EventKeyDown, code, 0, nappMods(e), if (e.repeatCount > 0) 1.0 else 0.0)
        val c = e.getUnicodeChar(e.metaState)
        if (c > 0 && !Character.isISOControl(c) && !e.isCtrlPressed && !e.isMetaPressed) {
            text(String(Character.toChars(c)))
        }
        return true
    }

    override fun onKeyUp(keyCode: Int, e: KeyEvent): Boolean {
        val code = nappKey(e) ?: return super.onKeyUp(keyCode, e)
        input(Mobile.EventKeyUp, code, 0, nappMods(e))
        return true
    }

    private fun nappKey(e: KeyEvent): Int? = when (e.keyCode) {
        KeyEvent.KEYCODE_DEL -> Mobile.KeyBackspace.toInt()
        KeyEvent.KEYCODE_TAB -> Mobile.KeyTab.toInt()
        KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_NUMPAD_ENTER -> Mobile.KeyEnter.toInt()
        KeyEvent.KEYCODE_ESCAPE -> Mobile.KeyEscape.toInt()
        KeyEvent.KEYCODE_FORWARD_DEL -> Mobile.KeyDelete.toInt()
        KeyEvent.KEYCODE_DPAD_LEFT -> Mobile.KeyArrowLeft.toInt()
        KeyEvent.KEYCODE_DPAD_RIGHT -> Mobile.KeyArrowRight.toInt()
        KeyEvent.KEYCODE_DPAD_UP -> Mobile.KeyArrowUp.toInt()
        KeyEvent.KEYCODE_DPAD_DOWN -> Mobile.KeyArrowDown.toInt()
        KeyEvent.KEYCODE_MOVE_HOME -> Mobile.KeyHome.toInt()
        KeyEvent.KEYCODE_MOVE_END -> Mobile.KeyEnd.toInt()
        KeyEvent.KEYCODE_PAGE_UP -> Mobile.KeyPageUp.toInt()
        KeyEvent.KEYCODE_PAGE_DOWN -> Mobile.KeyPageDown.toInt()
        else -> {
            // the character the key types with no modifiers: lowercased,
            // the shift being in the mask
            val c = e.getUnicodeChar(0)
            if (c > 0 && !Character.isISOControl(c)) Character.toLowerCase(c) else null
        }
    }

    private fun nappMods(e: KeyEvent): Long {
        var m = 0L
        if (e.isShiftPressed) m = m or Mobile.ModShift
        if (e.isCtrlPressed) m = m or Mobile.ModCtrl
        if (e.isAltPressed) m = m or Mobile.ModAlt
        if (e.isMetaPressed) m = m or Mobile.ModMeta
        return m
    }

    // ─── lifecycle ───────────────────────────────────────────────────

    fun destroy() {
        gone = true
        thread.quitSafely()
        bitmap?.recycle()
        bitmap = null
    }

    companion object {
        // canvas pixels one notch of a mouse wheel scrolls
        private const val SCROLL_STEP = 40f
    }
}
