package com.verdana.app

import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.Path
import android.graphics.Rect
import android.graphics.RectF
import androidx.compose.ui.graphics.toArgb
import androidx.core.content.pm.ShortcutInfoCompat
import androidx.core.content.pm.ShortcutManagerCompat
import androidx.core.graphics.drawable.IconCompat
import mobile.Mobile
import java.util.UUID

// BundleShortcuts are the Android shortcuts of a bundle: one pinned to the
// phone's home screen, and one in the menu that shows up when the Verdana
// icon is held. Both are the same intent, MainActivity with
// ACTION_RUN_BUNDLE and the bundle's name, which opens it as a tap on its
// Home tile would.
//
// The home screen ones belong to the user once placed (they remove them
// from the home screen as any other); the menu ones are ours to add and
// take away. Both follow the bundle: a save rewrites their label, icon and
// name, a delete takes the menu one away and greys the home screen one out.
object BundleShortcuts {
    const val ACTION_RUN_BUNDLE = "com.verdana.app.RUN_BUNDLE"
    const val EXTRA_BUNDLE = "bundle"

    // the bundle name a shortcut's intent asks for, null for any other intent
    fun bundleOf(intent: Intent?): String? =
        intent?.takeIf { it.action == ACTION_RUN_BUNDLE }?.getStringExtra(EXTRA_BUNDLE)?.ifBlank { null }

    fun canPinToHomeScreen(ctx: Context) = ShortcutManagerCompat.isRequestPinShortcutSupported(ctx)

    // pinToHomeScreen asks the phone's launcher to place a shortcut; it
    // shows its own confirmation. Blocks on the napp icons: call it off the
    // main thread.
    fun pinToHomeScreen(ctx: Context, name: String, nappIds: List<String>): Boolean {
        // one already there is asked for again rather than duplicated
        val id = ours(ctx, ShortcutManagerCompat.FLAG_MATCH_PINNED)
            .firstOrNull { it.isEnabled && bundleName(it) == name }?.id
            ?: newId()
        return ShortcutManagerCompat.requestPinShortcut(ctx, build(ctx, id, name, nappIds), null)
    }

    // the bundles that are in the Verdana icon's menu
    fun inAppMenu(ctx: Context): Set<String> =
        ours(ctx, ShortcutManagerCompat.FLAG_MATCH_DYNAMIC).mapNotNull { bundleName(it) }.toSet()

    // addToAppMenu puts a bundle in the Verdana icon's menu, failing with
    // what to do when the menu is full. Blocks on the napp icons.
    fun addToAppMenu(ctx: Context, name: String, nappIds: List<String>): String? {
        val dynamic = ours(ctx, ShortcutManagerCompat.FLAG_MATCH_DYNAMIC)
        if (dynamic.any { bundleName(it) == name }) return null
        val max = ShortcutManagerCompat.getMaxShortcutCountPerActivity(ctx)
        if (dynamic.size >= max) return "The Verdana icon menu holds $max bundles at most: remove one first."
        return try {
            if (ShortcutManagerCompat.addDynamicShortcuts(ctx, listOf(build(ctx, newId(), name, nappIds)))) null
            else "Android is not taking more shortcuts right now, try again later."
        } catch (e: Exception) {
            e.message ?: "could not add the shortcut"
        }
    }

    fun removeFromAppMenu(ctx: Context, name: String) {
        val ids = ours(ctx, ShortcutManagerCompat.FLAG_MATCH_DYNAMIC).filter { bundleName(it) == name }.map { it.id }
        if (ids.isNotEmpty()) ShortcutManagerCompat.removeDynamicShortcuts(ctx, ids)
    }

    // saved rewrites every shortcut of the bundle that was called oldName to
    // what it is now. Blocks on the napp icons.
    fun saved(ctx: Context, oldName: String, name: String, nappIds: List<String>) {
        val matching = ours(ctx, ShortcutManagerCompat.FLAG_MATCH_DYNAMIC or ShortcutManagerCompat.FLAG_MATCH_PINNED)
            .filter { it.isEnabled && bundleName(it) == oldName }
            .distinctBy { it.id }
        if (matching.isEmpty()) return
        try {
            ShortcutManagerCompat.updateShortcuts(ctx, matching.map { build(ctx, it.id, name, nappIds) })
        } catch (_: Exception) {}
    }

    // deleted takes a deleted bundle out of the menu and greys out what is
    // left of it on the home screen, which can't be removed from here.
    fun deleted(ctx: Context, name: String) {
        removeFromAppMenu(ctx, name)
        val pinned = ours(ctx, ShortcutManagerCompat.FLAG_MATCH_PINNED)
            .filter { it.isEnabled && bundleName(it) == name }
            .map { it.id }
        if (pinned.isNotEmpty()) {
            ShortcutManagerCompat.disableShortcuts(ctx, pinned, "The bundle $name was deleted")
        }
    }

    // ─── internals ───────────────────────────────────────────────────

    private const val ID_PREFIX = "bundle:"

    // a fresh id per shortcut: a name can be renamed away and reused by
    // another bundle, so it can't be what identifies a shortcut
    private fun newId() = ID_PREFIX + UUID.randomUUID()

    private fun ours(ctx: Context, flags: Int): List<ShortcutInfoCompat> = try {
        ShortcutManagerCompat.getShortcuts(ctx, flags).filter { it.id.startsWith(ID_PREFIX) }
    } catch (_: Exception) {
        listOf()
    }

    private fun bundleName(s: ShortcutInfoCompat): String? = s.intent.getStringExtra(EXTRA_BUNDLE)

    private fun build(ctx: Context, id: String, name: String, nappIds: List<String>): ShortcutInfoCompat {
        val intent = Intent(ctx, MainActivity::class.java)
            .setAction(ACTION_RUN_BUNDLE)
            .putExtra(EXTRA_BUNDLE, name)
        return ShortcutInfoCompat.Builder(ctx, id)
            .setShortLabel(name)
            .setLongLabel(name)
            .setIcon(icon(ctx, nappIds))
            .setIntent(intent)
            .build()
    }

    // icon draws the bundle as its Home tile does: its napps' icons, up to
    // four, two by two on a tile, as an adaptive icon so the phone's
    // launcher gives it the same shape as every other.
    private fun icon(ctx: Context, nappIds: List<String>): IconCompat {
        val images = nappIds.distinct().take(4).mapNotNull { id ->
            try {
                Mobile.nappIcon(id)?.let { BitmapFactory.decodeByteArray(it, 0, it.size) }
            } catch (_: Exception) {
                null
            }
        }
        if (images.isEmpty()) return IconCompat.createWithResource(ctx, R.mipmap.ic_launcher)

        // 108dp at 4x, of which the launcher's mask keeps the middle 72dp
        // and is only sure to keep a 66dp circle: the grid fits inside that
        val full = 432
        val grid = 184f
        val gap = 12f
        val cell = (grid - gap) / 2
        val origin = (full - grid) / 2

        val bmp = Bitmap.createBitmap(full, full, Bitmap.Config.ARGB_8888)
        val canvas = Canvas(bmp)
        canvas.drawColor(themeByName(VerdanaHost.state.theme).chipBg.toArgb())
        val paint = Paint(Paint.ANTI_ALIAS_FLAG or Paint.FILTER_BITMAP_FLAG)
        images.forEachIndexed { i, img ->
            val x = origin + (i % 2) * (cell + gap)
            val y = origin + (i / 2) * (cell + gap)
            val dst = RectF(x, y, x + cell, y + cell)
            canvas.save()
            canvas.clipPath(Path().apply { addRoundRect(dst, cell / 8, cell / 8, Path.Direction.CW) })
            canvas.drawBitmap(img, centerCrop(img), dst, paint)
            canvas.restore()
        }
        return IconCompat.createWithAdaptiveBitmap(bmp)
    }

    // the middle square of an image, as the Home tile's ContentScale.Crop
    private fun centerCrop(img: Bitmap): Rect {
        val side = minOf(img.width, img.height)
        val left = (img.width - side) / 2
        val top = (img.height - side) / 2
        return Rect(left, top, left + side, top + side)
    }
}
