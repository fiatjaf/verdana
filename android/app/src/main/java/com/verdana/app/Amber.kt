package com.verdana.app

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.content.pm.ResolveInfo
import android.net.Uri
import androidx.activity.result.ActivityResult
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.net.toUri
import mobile.Mobile

// Amber.kt is Verdana's NIP-55 client: everything that has to reach the
// phone's signer app (Amber and friends) goes through here.
//
// Requests are foreground intents — `nostrsigner:<payload>` with a `type`
// extra — launched from whichever Verdana window is in front. The signer
// answers through that window's launcher callback, which hands the answer
// straight back to the Go backend (Mobile.answerAmber), where the request
// originated. The Go side owns ids, timeouts and state; this file only
// builds intents and moves answers.

object Amber {

    // the launcher of the currently foregrounded Verdana activity: every
    // activity registers its own with registerForActivityResult and hands
    // it over here while it is in front, so a sign request started anywhere
    // goes through whatever window the user is looking at
    private var launcher: ActivityResultLauncher<Intent>? = null

    fun attach(l: ActivityResultLauncher<Intent>) { launcher = l }
    fun detach(l: ActivityResultLauncher<Intent>) { if (launcher === l) launcher = null }

    // the callback the activities' launchers run: the signer answered, so
    // move the answer over to the backend. Unrelated results (no request
    // id) are ignored.
    fun handle(r: ActivityResult) {
        val d = r.data ?: return
        val id = d.getStringExtra("id")
        if (id == null || id.isBlank()) return
        val ok = r.resultCode == Activity.RESULT_OK && !d.getBooleanExtra("rejected", false)
        val answer = d.getStringExtra("result") ?: d.getStringExtra("event") ?: ""
        try {
            Mobile.answerAmber(id, answer, ok)
        } catch (_: Exception) { }
    }

    // request builds the signer intent for one operation and launches it.
    // The uri carries the payload (an event JSON, a plaintext, a
    // ciphertext); the extras say which operation, which user and which
    // counterparty, with the id so the answer can find the asking request
    // on the way back. Returns false when nothing could be launched.
    fun request(op: String, payload: String, pubkey: String, counterpart: String, pkg: String, id: String): Boolean {
        val intent = Intent(Intent.ACTION_VIEW, "nostrsigner:$payload".toUri()).apply {
            putExtra("type", op)
            putExtra("current_user", pubkey)
            putExtra("id", id)
            if (counterpart.isNotBlank()) putExtra("pubKey", counterpart)
            if (pkg.isNotBlank()) setPackage(pkg)
            addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP)
        }
        val l = launcher ?: return false
        return try {
            l.launch(intent); true
        } catch (_: Exception) {
            false
        }
    }

    // loginIntent builds the get_public_key request the login screen sends
    // to pick a signer up: it names the operations Verdana wants silently
    // granted along with the key.
    fun loginIntent(pkg: String?): Intent =
        Intent(Intent.ACTION_VIEW, "nostrsigner:".toUri()).apply {
            putExtra("type", "get_public_key")
            putExtra(
                "permissions",
                """[{"type":"sign_event"},
                    {"type":"nip04_encrypt"},{"type":"nip04_decrypt"},
                    {"type":"nip44_encrypt"},{"type":"nip44_decrypt"}]""".replace("\n", " "),
            )
            if (pkg != null) setPackage(pkg)
            addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP)
        }

    fun installed(ctx: Context): Boolean =
        signerApps(ctx).isNotEmpty()

    // signerApps is everyone installed who answers the nostrsigner scheme,
    // for the login picker when there is more than one signer app.
    fun signerApps(ctx: Context): List<ResolveInfo> =
        ctx.packageManager.queryIntentActivities(
            Intent(Intent.ACTION_VIEW, "nostrsigner:".toUri()), 0,
        )

    val resolveInfoPackage: (ResolveInfo) -> String = { it.activityInfo.packageName }
}
